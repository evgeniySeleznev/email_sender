# Как загрузить существующий документ из базы Oracle

## Проблема
PDF приходит, но не читается - это может быть из-за некорректного тестового PDF. Нужно загрузить реальный документ из БД.

## Быстрый способ: Использовать готовые документы из email_attach

**Самый простой вариант** - найти уже созданное вложение с PDF и скопировать его:

```sql
-- Поиск готовых документов типа 2 в таблице email_attach
SELECT 
    email_attach_id,
    email_attach_name,
    LENGTH(report_clob) AS size_chars,
    created_date
FROM pcsystem.email_attach
WHERE report_type = 2
  AND report_clob IS NOT NULL
  AND LENGTH(report_clob) > 0
ORDER BY created_date DESC
FETCH FIRST 10 ROWS ONLY;
```

Затем в скрипте `soap_aq_script_type2.sql` раскомментируйте **ВАРИАНТ 4Б** и укажите найденный `email_attach_id`.

---

## Шаг 1: Найдите, где хранятся документы в вашей БД

Если в `email_attach` нет готовых документов, выполните запросы из файла `FIND_DOCUMENTS_IN_DB.sql`:

```sql
-- Поиск таблиц с CLOB/BLOB полями, которые могут содержать PDF
SELECT 
    owner,
    table_name,
    column_name,
    data_type
FROM all_tab_columns
WHERE owner = 'PCSYSTEM'
  AND data_type IN ('CLOB', 'BLOB')
  AND (UPPER(column_name) LIKE '%DOC%' 
       OR UPPER(column_name) LIKE '%FILE%'
       OR UPPER(column_name) LIKE '%PDF%'
       OR UPPER(column_name) LIKE '%REPORT%')
ORDER BY table_name, column_name;
```

**Возможные таблицы:**
- `pcsystem.edoc` - электронные документы (если `parametr_id = 5`)
- `pcsystem.document` - документы (если `parametr_id = 3`)
- `pcsystem.email_attach` - вложения из предыдущих заданий
- Другие таблицы вашей БД

## Шаг 2: Определите структуру таблицы

```sql
-- Пример для таблицы edoc
SELECT 
    edoc_id,
    file_name,
    file_path,
    -- Проверьте, есть ли поле с данными:
    -- file_data, file_blob, file_clob, report_clob и т.д.
FROM pcsystem.edoc
WHERE edoc_id = :your_document_id
  AND ROWNUM = 1;
```

## Шаг 3: Выберите способ загрузки

### Вариант А: Документ хранится в CLOB (уже в Base64)

Если в таблице есть поле типа CLOB с данными в Base64:

```sql
-- В скрипте soap_aq_script_type2.sql раскомментируйте ВАРИАНТ 4А:
BEGIN
    SELECT report_clob  -- замените на имя вашего поля
    INTO v_report_clob
    FROM pcsystem.your_table_name  -- замените на имя таблицы
    WHERE your_id_column = v_parametr_value  -- замените на условие
      AND ROWNUM = 1;
    
    IF v_report_clob IS NULL OR LENGTH(v_report_clob) = 0 THEN
        RAISE_APPLICATION_ERROR(-20001, 'CLOB пуст');
    END IF;
    
    DBMS_OUTPUT.PUT_LINE('  ✓ CLOB получен, размер: ' || LENGTH(v_report_clob) || ' символов');
EXCEPTION
    WHEN NO_DATA_FOUND THEN
        RAISE_APPLICATION_ERROR(-20002, 'Документ не найден');
END;
```

### Вариант Б: Документ хранится в BLOB (нужно конвертировать в Base64)

Если в таблице есть поле типа BLOB:

```sql
-- В скрипте раскомментируйте ВАРИАНТ 4В:
DECLARE
    v_blob_data BLOB;
    v_buffer RAW(32767);
    v_base64_part VARCHAR2(32767);
    v_offset NUMBER := 1;
    v_amount NUMBER;
BEGIN
    SELECT file_blob  -- замените на имя вашего BLOB поля
    INTO v_blob_data
    FROM pcsystem.your_table_name
    WHERE id = v_parametr_value
      AND ROWNUM = 1;
    
    IF v_blob_data IS NULL THEN
        RAISE_APPLICATION_ERROR(-20005, 'BLOB пуст');
    END IF;
    
    -- Инициализируем CLOB для результата
    DBMS_LOB.CREATETEMPORARY(v_report_clob, TRUE);
    
    -- Конвертируем BLOB в Base64 по частям
    WHILE v_offset <= DBMS_LOB.GETLENGTH(v_blob_data) LOOP
        v_amount := LEAST(32767, DBMS_LOB.GETLENGTH(v_blob_data) - v_offset + 1);
        DBMS_LOB.READ(v_blob_data, v_amount, v_offset, v_buffer);
        v_base64_part := UTL_ENCODE.BASE64_ENCODE(v_buffer);
        DBMS_LOB.WRITEAPPEND(v_report_clob, LENGTH(v_base64_part), v_base64_part);
        v_offset := v_offset + v_amount;
    END LOOP;
    
    DBMS_OUTPUT.PUT_LINE('  ✓ BLOB конвертирован в Base64, размер: ' || LENGTH(v_report_clob) || ' символов');
EXCEPTION
    WHEN NO_DATA_FOUND THEN
        RAISE_APPLICATION_ERROR(-20006, 'Документ не найден');
END;
```

### Вариант В: Документ хранится как файл на диске (file_path)

Если в таблице есть путь к файлу (например, `edoc.file_path`):

**Используйте СПОСОБ 1** из скрипта - чтение файла с диска:

```sql
-- 1. Создайте директорию Oracle, если еще не создана:
CREATE OR REPLACE DIRECTORY DOC_DIR AS '/path/to/documents';  -- Linux
-- или
CREATE OR REPLACE DIRECTORY DOC_DIR AS 'C:\documents';  -- Windows

-- 2. В скрипте раскомментируйте СПОСОБ 1 и укажите путь:
DECLARE
    v_file_path VARCHAR2(1000) := 'DOC_DIR';
    v_file_name VARCHAR2(1000);
BEGIN
    -- Получаем имя файла из таблицы
    SELECT file_name INTO v_file_name
    FROM pcsystem.edoc
    WHERE edoc_id = v_parametr_value;
    
    -- Затем используйте код из СПОСОБА 1 для чтения файла
    -- ...
END;
```

### Вариант Г: Копирование из существующего вложения

Если у вас уже есть вложение с PDF в таблице `email_attach`:

```sql
-- В скрипте раскомментируйте ВАРИАНТ 4Б:
DECLARE
    v_source_attach_id NUMBER := 123456;  -- ID существующего вложения
BEGIN
    SELECT report_clob INTO v_report_clob
    FROM pcsystem.email_attach
    WHERE email_attach_id = v_source_attach_id
      AND report_clob IS NOT NULL
      AND ROWNUM = 1;
    
    IF v_report_clob IS NULL OR LENGTH(v_report_clob) = 0 THEN
        RAISE_APPLICATION_ERROR(-20003, 'CLOB пуст');
    END IF;
    
    DBMS_OUTPUT.PUT_LINE('  ✓ CLOB скопирован из email_attach_id = ' || v_source_attach_id);
END;
```

## Шаг 4: Настройте скрипт

1. **Откройте** `soap_aq_script_type2.sql`

2. **Закомментируйте способ 3А** (добавьте `/*` и `*/` вокруг блока способа 3А)

3. **Раскомментируйте нужный вариант** способа 4 (удалите `/*` и `*/`)

4. **Замените** имена таблиц и полей на ваши:
   - `your_table_name` → имя вашей таблицы
   - `your_id_column` → имя поля с ID документа
   - `file_blob` / `report_clob` → имя поля с данными

5. **Установите правильное значение** `v_parametr_value`:
   ```sql
   v_parametr_value NUMBER := 12345;  -- ID вашего документа
   ```

## Шаг 5: Проверка данных

Перед запуском скрипта проверьте, что данные существуют:

```sql
-- Для CLOB:
SELECT 
    LENGTH(report_clob) AS clob_size,
    SUBSTR(report_clob, 1, 100) AS first_chars
FROM pcsystem.your_table_name
WHERE id = :your_document_id;

-- Для BLOB:
SELECT 
    DBMS_LOB.GETLENGTH(file_blob) AS blob_size
FROM pcsystem.your_table_name
WHERE id = :your_document_id;
```

## Частые проблемы

### Проблема: "ORA-01403: no data found"
**Решение:** Проверьте, что документ с указанным ID существует:
```sql
SELECT COUNT(*) FROM pcsystem.your_table_name WHERE id = :your_id;
```

### Проблема: "CLOB пуст"
**Решение:** Убедитесь, что поле не NULL и содержит данные:
```sql
SELECT * FROM pcsystem.your_table_name WHERE id = :your_id AND report_clob IS NOT NULL;
```

### Проблема: PDF все еще не читается
**Решение:** 
1. Убедитесь, что данные в Base64 (для CLOB)
2. Проверьте, что это действительно PDF (должен начинаться с `%PDF` после декодирования)
3. Попробуйте сохранить CLOB в файл и проверить вручную:
   ```sql
   -- Экспорт CLOB в файл для проверки
   DECLARE
       v_clob CLOB;
       v_file UTL_FILE.FILE_TYPE;
   BEGIN
       SELECT report_clob INTO v_clob FROM ...;
       v_file := UTL_FILE.FOPEN('TEST_DIR', 'test.pdf', 'WB');
       -- Запись данных...
   END;
   ```

## Примеры для конкретных таблиц

### Если документы в таблице edoc:

```sql
-- Вариант 1: Если есть поле file_data (CLOB в Base64)
SELECT file_data INTO v_report_clob
FROM pcsystem.edoc
WHERE edoc_id = v_parametr_value;

-- Вариант 2: Если нужно прочитать файл с диска
-- Используйте file_path из edoc и СПОСОБ 1
```

### Если документы в таблице document:

```sql
SELECT document_data INTO v_report_clob
FROM pcsystem.document
WHERE document_id = v_parametr_value;
```

## Рекомендации

1. **Начните с простого:** Используйте ВАРИАНТ 4Б (копирование из существующего вложения), если у вас есть рабочий пример
2. **Проверьте формат данных:** Убедитесь, что данные действительно в Base64 (для CLOB)
3. **Тестируйте постепенно:** Сначала проверьте, что данные загружаются, затем проверьте отправку
