-- ============================================================================
-- СКРИПТ: Создание задания с вложением типа 2 (CLOB из БД) и отправка в очередь AQ
-- ============================================================================
-- Этот скрипт создает задание на отправку письма с вложением типа 2 (CLOB).
-- В отличие от типа 1 (Crystal Reports), для типа 2 данные PDF уже должны быть
-- готовы в БД и хранятся в поле report_clob в формате Base64.
--
-- ИНСТРУКЦИЯ ПО ИСПОЛЬЗОВАНИЮ:
-- 1. Найдите, где хранятся PDF документы в вашей БД (см. md/LOAD_DOCUMENT_FROM_DB.md)
-- 2. Выберите подходящий способ загрузки (СПОСОБ 1, 2, 3, 4) в разделе ШАГ 3
-- 3. Раскомментируйте выбранный способ и настройте под вашу структуру БД
-- 4. Установите правильные значения переменных в разделе НАСТРОЙКИ
-- 5. Запустите скрипт
--
-- ВАЖНО: Способ 3А (тестовый PDF) закомментирован, т.к. PDF может не читаться.
--         Для реальных документов используйте СПОСОБ 4 (загрузка из БД).
-- ============================================================================

DECLARE
    -- ========== НАСТРОЙКИ ==========
    v_email_type_id NUMBER := 10;                    -- ID типа email из справочника
    v_parametr_id NUMBER := 3;                       -- parametr_id (например, 3 = document_id)
    v_parametr_value NUMBER := 12345;                -- Значение параметра
    v_email_address VARCHAR2(500) := 'evgen.seleznev@gmail.com';  -- Email получателя
    v_email_title VARCHAR2(1000) := 'Заголовок письма';
    v_email_text VARCHAR2(4000) := '<html><body>' ||
        '<h1 style="color: #2c3e50;">Добро пожаловать!</h1>' ||
        '<p>Это <strong>важное</strong> письмо с <em>разнообразным</em> <u>HTML форматированием</u>.</p>' ||
        '<p style="color: #e74c3c;">Текст может быть <span style="background-color: #f1c40f;">выделен цветом</span> и иметь разные стили.</p>' ||
        '<h2 style="color: #3498db;">Список преимуществ:</h2>' ||
        '<ul><li><b>Первый пункт</b> с жирным текстом</li>' ||
        '<li><i>Второй пункт</i> с курсивом</li>' ||
        '<li>Третий пункт с <a href="https://example.com" style="color: #9b59b6;">ссылкой</a></li></ul>' ||
        '<p>Вы также можете посетить наш <a href="https://example.com" style="color: #27ae60; text-decoration: none;">сайт</a> для получения дополнительной информации.</p>' ||
        '<hr style="border: 1px solid #bdc3c7;">' ||
        '<p style="font-size: 12px; color: #7f8c8d;">Это письмо было отправлено автоматически. Пожалуйста, не отвечайте на него.</p>' ||
        '</body></html>';
    v_branch_id NUMBER := 1;                         -- ID территории
    v_smtp_id NUMBER := 1;                           -- ID SMTP сервера
    
    -- ========== НАСТРОЙКИ ДЛЯ ВЛОЖЕНИЯ ТИПА 2 ==========
    v_email_attach_type_id NUMBER := 1;             -- ID типа вложения из справочника v_com_email_attach_type
    v_email_attach_name VARCHAR2(1000);             -- Имя вложения (будет получено через get_email_attach_name)
    v_report_clob CLOB;                             -- CLOB с данными PDF в формате Base64
    -- ПРИМЕЧАНИЕ: v_report_clob должен быть заполнен данными PDF в Base64.
    -- 
    -- ВАЖНО: Если используете СПОСОБ 4 (загрузка из БД), укажите:
    -- - Имя таблицы, где хранятся документы
    -- - Имя поля с данными (CLOB/BLOB)
    -- - Условие WHERE для поиска документа
    -- 
    -- Примеры таблиц, где могут храниться документы:
    -- - pcsystem.edoc (электронные документы)
    -- - pcsystem.document (документы)
    -- - pcsystem.email_attach (из предыдущих заданий)
    -- - Другие таблицы вашей БД
    
    -- ========== ВНУТРЕННИЕ ПЕРЕМЕННЫЕ ==========
    v_task_id NUMBER;
    v_attach_id NUMBER;
    v_err_code NUMBER;
    v_err_desc VARCHAR2(4000);
    v_attach_count NUMBER;
BEGIN
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('Создание задания с вложением типа 2 (CLOB)');
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('Параметры:');
    DBMS_OUTPUT.PUT_LINE('  email_type_id: ' || v_email_type_id);
    DBMS_OUTPUT.PUT_LINE('  parametr_id: ' || v_parametr_id);
    DBMS_OUTPUT.PUT_LINE('  parametr_value: ' || v_parametr_value);
    DBMS_OUTPUT.PUT_LINE('  email_address: ' || v_email_address);
    DBMS_OUTPUT.PUT_LINE('  branch_id: ' || v_branch_id);
    DBMS_OUTPUT.PUT_LINE('  smtp_id: ' || v_smtp_id);
    DBMS_OUTPUT.PUT_LINE('  email_attach_type_id: ' || v_email_attach_type_id);
    DBMS_OUTPUT.PUT_LINE('');
    
    -- ========== ШАГ 1: Создание задания через INSERT ==========
    DBMS_OUTPUT.PUT_LINE('ШАГ 1: Создание задания через INSERT...');
    
    INSERT INTO pcsystem.email_task (
        email_task_id,
        email_type_id,
        parametr_id,
        parametr_value,
        email_address,
        email_title,
        email_text,
        email_status_id,
        date_request,
        date_delay_send,
        branch_id,
        smtp_id
    ) VALUES (
        pcsystem.seq_email_task.NEXTVAL,  -- Генерируем ID
        v_email_type_id,
        v_parametr_id,
        v_parametr_value,
        v_email_address,
        v_email_title,
        v_email_text,
        1,                                 -- email_status_id (1 = новый)
        SYSDATE,                           -- date_request
        SYSDATE,                           -- date_delay_send (будет обновлено перед отправкой)
        v_branch_id,
        v_smtp_id
    )
    RETURNING email_task_id INTO v_task_id;  -- Получаем ID сразу
    
    DBMS_OUTPUT.PUT_LINE('  ✓ Задание создано: ' || v_task_id);
    
    -- ========== ШАГ 2: Получение имени вложения ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 2: Получение имени вложения...');
    
    -- Для типа 2 имя вложения можно задать вручную или использовать имя по умолчанию
    -- Если нужно получить имя из справочника, раскомментируйте блок ниже
    -- ВНИМАНИЕ: get_email_attach_name может требовать дополнительные параметры в вашей версии БД
    
    -- Вариант 1: Использовать имя по умолчанию (РЕКОМЕНДУЕТСЯ ДЛЯ ТЕСТИРОВАНИЯ)
    v_email_attach_name := 'attachment_' || v_parametr_value || '.pdf';
    DBMS_OUTPUT.PUT_LINE('  ✓ Имя вложения (по умолчанию): ' || v_email_attach_name);
    
    -- Вариант 2: Задать имя вручную (раскомментируйте и укажите нужное имя)
    -- v_email_attach_name := 'Мой_отчет.pdf';
    -- DBMS_OUTPUT.PUT_LINE('  ✓ Имя вложения (задано вручную): ' || v_email_attach_name);
    
    -- Вариант 3: Получить имя из справочника через get_email_attach_name
    -- ВНИМАНИЕ: Если используете этот вариант, убедитесь, что сигнатура функции соответствует вашей БД
    -- Возможно, потребуются дополнительные параметры (p_err_code, p_err_desc)
    /*
    BEGIN
        pcsystem.pkg_email.get_email_attach_name(
            p_email_attach_type_id => v_email_attach_type_id,
            p_parametr_value => v_parametr_value,
            p_email_attach_name => v_email_attach_name
        );
        
        IF v_email_attach_name IS NULL OR LENGTH(TRIM(v_email_attach_name)) = 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ⚠ Предупреждение: имя вложения пусто, используется имя по умолчанию');
            v_email_attach_name := 'attachment_' || v_parametr_value || '.pdf';
        END IF;
        
        DBMS_OUTPUT.PUT_LINE('  ✓ Имя вложения: ' || v_email_attach_name);
    EXCEPTION
        WHEN OTHERS THEN
            DBMS_OUTPUT.PUT_LINE('  ⚠ Ошибка при получении имени вложения: ' || SQLERRM);
            DBMS_OUTPUT.PUT_LINE('  Будет использовано имя по умолчанию');
            v_email_attach_name := 'attachment_' || v_parametr_value || '.pdf';
            DBMS_OUTPUT.PUT_LINE('  ✓ Имя вложения (по умолчанию): ' || v_email_attach_name);
    END;
    */
    
    -- ========== ШАГ 3: Получение данных CLOB для вложения ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 3: Получение данных CLOB для вложения...');
    DBMS_OUTPUT.PUT_LINE('  ВНИМАНИЕ: Необходимо заполнить v_report_clob данными PDF в Base64!');
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('  Доступные способы заполнения:');
    DBMS_OUTPUT.PUT_LINE('    1. СПОСОБ 1: Чтение файла с диска через UTL_FILE (для больших файлов)');
    DBMS_OUTPUT.PUT_LINE('    2. СПОСОБ 2: Использование BFILE (альтернатива способу 1)');
    DBMS_OUTPUT.PUT_LINE('    3. СПОСОБ 3: Прямая вставка Base64 строки (для маленьких файлов)');
    DBMS_OUTPUT.PUT_LINE('    4. СПОСОБ 3А: Быстрый тест с минимальным PDF (для проверки работы)');
    DBMS_OUTPUT.PUT_LINE('    5. СПОСОБ 4: Получение из таблицы БД (для продакшена)');
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('  ИНСТРУКЦИЯ: Раскомментируйте один из способов ниже и настройте под свои нужды');
    DBMS_OUTPUT.PUT_LINE('');
    
    -- ============================================================================
    -- СПОСОБ 1: Чтение файла с диска и конвертация в Base64 (РЕКОМЕНДУЕТСЯ ДЛЯ ТЕСТИРОВАНИЯ)
    -- ============================================================================
    -- Для тестирования: прочитайте любой PDF файл с диска и конвертируйте в Base64
    -- 
    -- ТРЕБОВАНИЯ:
    -- 1. Создайте директорию в Oracle (если еще не создана):
    --    CREATE OR REPLACE DIRECTORY TEST_DIR AS 'C:\temp';  -- или другой путь
    -- 2. Положите тестовый PDF файл в эту директорию
    -- 3. Раскомментируйте код ниже и укажите имя файла
    --
    -- ПРИМЕЧАНИЕ: Для работы нужны права на чтение файлов через UTL_FILE
    -- ============================================================================
    /*
    DECLARE
        v_file_path VARCHAR2(1000) := 'TEST_DIR';           -- Имя директории Oracle
        v_file_name VARCHAR2(1000) := 'test.pdf';          -- Имя файла
        v_file_handle UTL_FILE.FILE_TYPE;
        v_buffer RAW(32767);
        v_base64_buffer VARCHAR2(32767);
        v_file_size NUMBER := 0;
        v_bytes_read NUMBER;
    BEGIN
        -- Инициализируем CLOB для результата
        DBMS_LOB.CREATETEMPORARY(v_report_clob, TRUE);
        
        -- Открываем файл для чтения в бинарном режиме
        v_file_handle := UTL_FILE.FOPEN(v_file_path, v_file_name, 'RB', 32767);
        
        -- Читаем файл по частям и конвертируем в Base64
        BEGIN
            LOOP
                -- Читаем порцию данных
                BEGIN
                    UTL_FILE.GET_RAW(v_file_handle, v_buffer, 32767);
                    v_bytes_read := UTL_RAW.LENGTH(v_buffer);
                EXCEPTION
                    WHEN NO_DATA_FOUND THEN
                        EXIT; -- Файл прочитан полностью
                END;
                
                -- Конвертируем RAW в Base64 (UTL_ENCODE работает с RAW)
                v_base64_buffer := UTL_ENCODE.BASE64_ENCODE(v_buffer);
                
                -- Добавляем Base64 строку в CLOB
                DBMS_LOB.WRITEAPPEND(v_report_clob, LENGTH(v_base64_buffer), v_base64_buffer);
                
                v_file_size := v_file_size + v_bytes_read;
            END LOOP;
        EXCEPTION
            WHEN NO_DATA_FOUND THEN
                NULL; -- Файл прочитан полностью
        END;
        
        UTL_FILE.FCLOSE(v_file_handle);
        
        DBMS_OUTPUT.PUT_LINE('  ✓ Файл прочитан: ' || v_file_name);
        DBMS_OUTPUT.PUT_LINE('  ✓ Размер файла: ' || v_file_size || ' байт');
        DBMS_OUTPUT.PUT_LINE('  ✓ Размер Base64: ' || LENGTH(v_report_clob) || ' символов');
    EXCEPTION
        WHEN OTHERS THEN
            IF UTL_FILE.IS_OPEN(v_file_handle) THEN
                UTL_FILE.FCLOSE(v_file_handle);
            END IF;
            IF DBMS_LOB.ISTEMPORARY(v_report_clob) = 1 THEN
                DBMS_LOB.FREETEMPORARY(v_report_clob);
            END IF;
            RAISE_APPLICATION_ERROR(-20003, 'Ошибка чтения файла: ' || SQLERRM);
    END;
    */
    
    -- ============================================================================
    -- СПОСОБ 2: Использование BFILE (альтернативный способ)
    -- ============================================================================
    -- Если у вас есть доступ к BFILE, можно использовать этот способ:
    /*
    DECLARE
        v_bfile BFILE;
        v_file_length NUMBER;
        v_buffer RAW(32767);
        v_base64_buffer VARCHAR2(32767);
        v_amount NUMBER;
        v_offset NUMBER := 1;
    BEGIN
        -- Открываем файл
        v_bfile := BFILENAME('TEST_DIR', 'test.pdf');
        DBMS_LOB.FILEOPEN(v_bfile, DBMS_LOB.FILE_READONLY);
        
        -- Получаем размер файла
        v_file_length := DBMS_LOB.GETLENGTH(v_bfile);
        
        -- Инициализируем CLOB для результата
        DBMS_LOB.CREATETEMPORARY(v_report_clob, TRUE);
        
        -- Читаем файл по частям и конвертируем в Base64
        WHILE v_offset <= v_file_length LOOP
            -- Определяем размер порции для чтения
            v_amount := LEAST(32767, v_file_length - v_offset + 1);
            
            -- Читаем порцию данных
            DBMS_LOB.READ(v_bfile, v_amount, v_offset, v_buffer);
            
            -- Конвертируем RAW в Base64
            v_base64_buffer := UTL_ENCODE.BASE64_ENCODE(v_buffer);
            
            -- Добавляем Base64 строку в CLOB
            DBMS_LOB.WRITEAPPEND(v_report_clob, LENGTH(v_base64_buffer), v_base64_buffer);
            
            v_offset := v_offset + v_amount;
        END LOOP;
        
        DBMS_LOB.FILECLOSE(v_bfile);
        
        DBMS_OUTPUT.PUT_LINE('  ✓ Файл прочитан через BFILE, размер: ' || LENGTH(v_report_clob) || ' символов');
    EXCEPTION
        WHEN OTHERS THEN
            IF DBMS_LOB.FILEISOPEN(v_bfile) = 1 THEN
                DBMS_LOB.FILECLOSE(v_bfile);
            END IF;
            IF DBMS_LOB.ISTEMPORARY(v_report_clob) = 1 THEN
                DBMS_LOB.FREETEMPORARY(v_report_clob);
            END IF;
            RAISE_APPLICATION_ERROR(-20004, 'Ошибка чтения BFILE: ' || SQLERRM);
    END;
    */
    
    -- ============================================================================
    -- СПОСОБ 3: Прямое указание Base64 строки (РЕКОМЕНДУЕТСЯ ДЛЯ БЫСТРОГО ТЕСТИРОВАНИЯ)
    -- ============================================================================
    -- Для быстрого тестирования можно вставить Base64 строку напрямую
    -- 
    -- Как получить Base64 из файла:
    -- 1. В Windows PowerShell:
    --    [Convert]::ToBase64String([IO.File]::ReadAllBytes("C:\path\to\file.pdf"))
    --    Скопируйте результат и вставьте в v_base64_string ниже
    -- 2. В Linux/Mac:
    --    base64 file.pdf | tr -d '\n'  (удаляет переносы строк)
    -- 3. Онлайн: https://www.base64encode.org/
    --
    -- ВАЖНО: Base64 строка должна быть БЕЗ переносов строк!
    -- ============================================================================
    /*
    DECLARE
        v_base64_string VARCHAR2(32767);  -- Вставьте сюда Base64 строку из файла
    BEGIN
        -- ВСТАВЬТЕ СЮДА Base64 строку вашего PDF файла
        -- Пример для минимального PDF (раскомментируйте для теста):
        v_base64_string := 'JVBERi0xLjQKJeLjz9MKMyAwIG9iago8PAovVHlwZSAvQ2F0YWxvZwovUGFnZXMgMiAwIFIKPj4KZW5kb2JqCjIgMCBvYmoKPDwKL1R5cGUgL1BhZ2VzCi9LaWRzIFszIDAgUl0KL0NvdW50IDEKPj4KZW5kb2JqCjMgMCBvYmoKPDwKL1R5cGUgL1BhZ2UKL1BhcmVudCAyIDAgUgovTWVkaWFCb3ggWzAgMCA2MTIgNzkyXQo+PgplbmRvYmoKeHJlZgowIDQKMDAwMDAwMDAwMCA2NTUzNSBmIAowMDAwMDAwMDA5IDAwMDAwIG4gCjAwMDAwMDAwNTggMDAwMDAgbiAKMDAwMDAwMDExNSAwMDAwMCBuIAp0cmFpbGVyCjw8Ci9TaXplIDQKL1Jvb3QgMSAwIFIKPj4Kc3RhcnR4cmVmCjE3OAolJUVPRg==';
        
        -- Инициализируем CLOB и копируем строку
        DBMS_LOB.CREATETEMPORARY(v_report_clob, TRUE);
        DBMS_LOB.WRITEAPPEND(v_report_clob, LENGTH(v_base64_string), v_base64_string);
        
        DBMS_OUTPUT.PUT_LINE('  ✓ Использован Base64 из строки, размер: ' || LENGTH(v_report_clob) || ' символов');
    END;
    */
    
    -- ============================================================================
    -- СПОСОБ 3А: Быстрый тест с минимальным PDF (ЗАКОММЕНТИРОВАН - используйте способ 4 для реальных документов)
    -- ============================================================================
    -- Этот способ создает минимальный валидный PDF прямо в коде
    -- ВНИМАНИЕ: PDF может не читаться корректно! Для реальных документов используйте СПОСОБ 4
    -- ============================================================================
    /*
    BEGIN
        -- Создаем минимальный PDF в Base64 (это валидный пустой PDF)
        DECLARE
            v_pdf_content VARCHAR2(2000);
            v_base64_string VARCHAR2(3000);
        BEGIN
            -- Минимальный PDF файл (бинарные данные)
            v_pdf_content := '%PDF-1.4' || CHR(10) ||
                             '1 0 obj' || CHR(10) ||
                             '<< /Type /Catalog /Pages 2 0 R >>' || CHR(10) ||
                             'endobj' || CHR(10) ||
                             '2 0 obj' || CHR(10) ||
                             '<< /Type /Pages /Kids [3 0 R] /Count 1 >>' || CHR(10) ||
                             'endobj' || CHR(10) ||
                             '3 0 obj' || CHR(10) ||
                             '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>' || CHR(10) ||
                             'endobj' || CHR(10) ||
                             'xref' || CHR(10) ||
                             '0 4' || CHR(10) ||
                             '0000000000 65535 f' || CHR(10) ||
                             '0000000009 00000 n' || CHR(10) ||
                             '0000000058 00000 n' || CHR(10) ||
                             '0000000115 00000 n' || CHR(10) ||
                             'trailer' || CHR(10) ||
                             '<< /Size 4 /Root 1 0 R >>' || CHR(10) ||
                             'startxref' || CHR(10) ||
                             '178' || CHR(10) ||
                             '%%EOF';
            
            -- Конвертируем в RAW, затем в Base64
            v_base64_string := UTL_ENCODE.BASE64_ENCODE(UTL_RAW.CAST_TO_RAW(v_pdf_content));
            
            -- Инициализируем CLOB и копируем Base64 строку
            DBMS_LOB.CREATETEMPORARY(v_report_clob, TRUE);
            DBMS_LOB.WRITEAPPEND(v_report_clob, LENGTH(v_base64_string), v_base64_string);
            
            DBMS_OUTPUT.PUT_LINE('  ✓ Создан тестовый PDF, размер Base64: ' || LENGTH(v_report_clob) || ' символов');
        END;
    END;
    */
    
    -- ============================================================================
    -- СПОСОБ 4: Получение из таблицы БД (для продакшена)
    -- ============================================================================
    -- Если данные уже есть в БД, используйте этот способ
    -- 
    -- ВАРИАНТ 4А: Если PDF хранится в таблице edoc (электронные документы)
    -- Раскомментируйте и адаптируйте под вашу структуру:
    /*
    BEGIN
        -- Пример для таблицы edoc (если документы хранятся там)
        -- Адаптируйте запрос под вашу структуру БД
        SELECT 
            -- Если данные уже в Base64 в CLOB/BLOB поле:
            -- report_data  -- замените на имя вашего поля
            -- 
            -- Если нужно прочитать файл с диска (как в типе 3):
            -- Используйте СПОСОБ 1 или СПОСОБ 2 вместо этого
            --
            -- Если данные в BLOB, конвертируйте в Base64:
            UTL_ENCODE.BASE64_ENCODE(DBMS_LOB.SUBSTR(file_blob, DBMS_LOB.GETLENGTH(file_blob), 1))
        INTO v_report_clob
        FROM pcsystem.edoc  -- замените на имя вашей таблицы
        WHERE edoc_id = v_parametr_value  -- замените на ваше условие
          AND ROWNUM = 1;
        
        IF v_report_clob IS NULL OR LENGTH(v_report_clob) = 0 THEN
            RAISE_APPLICATION_ERROR(-20001, 'CLOB пуст или не найден для parametr_value = ' || v_parametr_value);
        END IF;
        
        -- Инициализируем CLOB если нужно
        IF v_report_clob IS NULL THEN
            DBMS_LOB.CREATETEMPORARY(v_report_clob, TRUE);
        END IF;
        
        DBMS_OUTPUT.PUT_LINE('  ✓ CLOB получен из БД, размер: ' || LENGTH(v_report_clob) || ' символов');
    EXCEPTION
        WHEN NO_DATA_FOUND THEN
            RAISE_APPLICATION_ERROR(-20002, 'Не найдены данные для parametr_value = ' || v_parametr_value);
    END;
    */
    
    -- ВАРИАНТ 4Б: Копирование из существующего вложения email_attach (АКТИВИРОВАН)
    -- ============================================================================
    -- Копируем данные из существующих вложений: 1095236, 1093551, 1093504
    -- ВАЖНО: Этот блок не заполняет v_report_clob, т.к. мы создадим несколько вложений
    -- ============================================================================
    -- Блок закомментирован, т.к. создание вложений происходит в ШАГ 4 через цикл
    /*
    -- Этот блок не используется, т.к. мы создаем несколько вложений в цикле
    */
    
    -- ВАРИАНТ 4В: Если PDF хранится в BLOB и нужно конвертировать в Base64
    /*
    DECLARE
        v_blob_data BLOB;
        v_buffer RAW(32767);
        v_base64_part VARCHAR2(32767);
        v_offset NUMBER := 1;
        v_amount NUMBER;
    BEGIN
        -- Получаем BLOB из таблицы
        SELECT file_blob INTO v_blob_data  -- замените file_blob на имя вашего поля
        FROM pcsystem.your_table_name
        WHERE id = v_parametr_value
          AND ROWNUM = 1;
        
        IF v_blob_data IS NULL THEN
            RAISE_APPLICATION_ERROR(-20005, 'BLOB пуст для parametr_value = ' || v_parametr_value);
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
            RAISE_APPLICATION_ERROR(-20006, 'Не найдены данные для parametr_value = ' || v_parametr_value);
    END;
    */
    
    -- ============================================================================
    -- ДЛЯ БЫСТРОГО ТЕСТИРОВАНИЯ: Раскомментируйте один из способов выше
    -- Рекомендуется использовать СПОСОБ 1 (чтение файла с диска)
    -- ============================================================================
    -- ПРИМЕЧАНИЕ: Для варианта 4Б проверка v_report_clob не требуется,
    -- т.к. данные копируются напрямую при создании вложений
    
    -- ========== ШАГ 4: Создание вложений типа 2 (CLOB) из существующих ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 4: Создание вложений типа 2 (CLOB) из существующих...');
    
    -- ID существующего вложения для копирования
    DECLARE
        v_source_attach_id NUMBER := 1093635;  -- ID вложения для копирования
        v_source_clob CLOB;
        v_source_name VARCHAR2(1000);
        v_created_count NUMBER := 0;
        v_current_attach_id NUMBER;
    BEGIN
        -- Получаем данные из исходного вложения
        SELECT report_clob, email_attach_name
        INTO v_source_clob, v_source_name
        FROM pcsystem.email_attach
        WHERE email_attach_id = v_source_attach_id
          AND report_clob IS NOT NULL
          AND ROWNUM = 1;
        
        -- Проверяем, что данные не пустые
        IF v_source_clob IS NULL OR LENGTH(v_source_clob) = 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: email_attach_id = ' || v_source_attach_id || ' - CLOB пуст');
            DBMS_OUTPUT.PUT_LINE('  Задание создано с ID: ' || v_task_id);
            DBMS_OUTPUT.PUT_LINE('  Вы можете создать вложение вручную.');
            RETURN;
        END IF;
        
        -- Получаем имя вложения для нового вложения
        -- Используем имя из исходного вложения или генерируем по умолчанию
        v_email_attach_name := v_source_name;
        
        -- Если имя пустое, генерируем имя по умолчанию
        IF v_email_attach_name IS NULL OR LENGTH(v_email_attach_name) = 0 THEN
            v_email_attach_name := 'attachment_' || v_parametr_value || '.pdf';
        END IF;
        
        -- Создаем новое вложение с скопированными данными
        INSERT INTO pcsystem.email_attach (
            email_attach_id,
            email_task_id,
            email_attach_type_id,
            email_attach_name,
            report_type,
            report_clob
        ) VALUES (
            pcsystem.seq_email_attach.NEXTVAL,
            v_task_id,
            v_email_attach_type_id,
            v_email_attach_name,
            2,  -- report_type = 2 (CLOB из БД)
            v_source_clob  -- Копируем CLOB из исходного вложения
        )
        RETURNING email_attach_id INTO v_current_attach_id;
        
        v_created_count := 1;
        DBMS_OUTPUT.PUT_LINE('  ✓ Вложение создано: ' || v_current_attach_id);
        DBMS_OUTPUT.PUT_LINE('    Источник: email_attach_id = ' || v_source_attach_id);
        DBMS_OUTPUT.PUT_LINE('    Имя: ' || v_email_attach_name);
        DBMS_OUTPUT.PUT_LINE('    Размер CLOB: ' || LENGTH(v_source_clob) || ' символов');
        
    EXCEPTION
        WHEN NO_DATA_FOUND THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: email_attach_id = ' || v_source_attach_id || ' не найден или CLOB пуст');
            DBMS_OUTPUT.PUT_LINE('  Задание создано с ID: ' || v_task_id);
            DBMS_OUTPUT.PUT_LINE('  Вы можете создать вложение вручную.');
            RETURN;
        WHEN OTHERS THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА при обработке email_attach_id = ' || v_source_attach_id || ': ' || SQLERRM);
            DBMS_OUTPUT.PUT_LINE('  Задание создано с ID: ' || v_task_id);
            RETURN;
    END;
    
    -- Проверяем созданные вложения
    SELECT COUNT(*) INTO v_attach_count
    FROM pcsystem.email_attach
    WHERE email_task_id = v_task_id
      AND report_type = 2;
    
    IF v_attach_count = 0 THEN
        DBMS_OUTPUT.PUT_LINE('  ⚠ ПРЕДУПРЕЖДЕНИЕ: Вложения не найдены после создания!');
    ELSE
        DBMS_OUTPUT.PUT_LINE('  ✓ Подтверждено создание вложений (всего типа 2: ' || v_attach_count || ')');
    END IF;
    
    -- ========== ШАГ 5: Вывод информации о созданных данных ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 5: Информация о созданных данных...');
    DBMS_OUTPUT.PUT_LINE('  Задание ID: ' || v_task_id);
    DBMS_OUTPUT.PUT_LINE('  Email получателя: ' || v_email_address);
    DBMS_OUTPUT.PUT_LINE('  Заголовок: ' || v_email_title);
    DBMS_OUTPUT.PUT_LINE('  Вложений типа 2: ' || v_attach_count);
    DBMS_OUTPUT.PUT_LINE('');
    
    IF v_attach_count > 0 THEN
        DBMS_OUTPUT.PUT_LINE('  Детали вложений:');
        FOR rec IN (
            SELECT 
                ea.email_attach_id,
                ea.email_attach_name,
                ea.report_type,
                LENGTH(ea.report_clob) AS clob_size
            FROM pcsystem.email_attach ea
            WHERE ea.email_task_id = v_task_id
              AND ea.report_type = 2
            ORDER BY ea.email_attach_id
        ) LOOP
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('    Вложение ID: ' || rec.email_attach_id);
            DBMS_OUTPUT.PUT_LINE('      Имя для получателя: ' || rec.email_attach_name);
            DBMS_OUTPUT.PUT_LINE('      Тип отчета: ' || rec.report_type || ' (CLOB из БД)');
            DBMS_OUTPUT.PUT_LINE('      Размер CLOB: ' || rec.clob_size || ' символов');
        END LOOP;
    END IF;
    
    -- ========== ШАГ 6: Обновление date_delay_send НЕПОСРЕДСТВЕННО ПЕРЕД отправкой ==========
    -- ИСПРАВЛЕНИЕ: Обновляем date_delay_send непосредственно перед отправкой в очередь,
    -- чтобы гарантировать, что DELAY будет положительным или нулевым
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 6: Обновление date_delay_send непосредственно перед отправкой...');
    
    -- Устанавливаем date_delay_send в текущее время или в будущее (например, +1 секунда)
    -- для гарантии положительного DELAY
    UPDATE pcsystem.email_task
    SET date_delay_send = SYSDATE + 1/86400  -- +1 секунда для гарантии положительного DELAY
    WHERE email_task_id = v_task_id;
    
    DBMS_OUTPUT.PUT_LINE('  ✓ date_delay_send обновлено на SYSDATE + 1 секунда');
    DBMS_OUTPUT.PUT_LINE('  Текущее время: ' || TO_CHAR(SYSDATE, 'YYYY-MM-DD HH24:MI:SS'));
    DBMS_OUTPUT.PUT_LINE('  date_delay_send: ' || TO_CHAR(SYSDATE + 1/86400, 'YYYY-MM-DD HH24:MI:SS'));
    
    -- ========== ШАГ 7: Отправка задания в очередь AQ ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 7: Отправка задания в очередь AQ...');
    
    -- ПРОВЕРКА: Диагностика перед отправкой
    -- Проверяем количество вложений
    SELECT COUNT(*) INTO v_attach_count
    FROM pcsystem.email_attach
    WHERE email_task_id = v_task_id;
    
    DBMS_OUTPUT.PUT_LINE('  Всего вложений для задания: ' || v_attach_count);
    
    -- Пробуем отправить в очередь
    BEGIN
        pcsystem.pkg_email.send_email_request(
            p_email_task_id => v_task_id,
            p_err_code => v_err_code,
            p_err_desc => v_err_desc
        );
        
        IF v_err_code != 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА отправки в очередь: ' || v_err_desc);
            DBMS_OUTPUT.PUT_LINE('  Код ошибки: ' || v_err_code);
            DBMS_OUTPUT.PUT_LINE('');
            
            -- Специальная обработка ошибки ORA-01427
            IF v_err_code = -1427 OR v_err_desc LIKE '%ORA-01427%' OR v_err_desc LIKE '%подзапрос%' THEN
                DBMS_OUTPUT.PUT_LINE('  ⚠ ОШИБКА ORA-01427: Подзапрос возвращает более одной строки');
                DBMS_OUTPUT.PUT_LINE('  Это может быть связано с несколькими вложениями типа 2.');
                DBMS_OUTPUT.PUT_LINE('  Возможные решения:');
                DBMS_OUTPUT.PUT_LINE('    1. Проверьте представление v_email_xml - возможно, оно не поддерживает несколько вложений типа 2');
                DBMS_OUTPUT.PUT_LINE('    2. Попробуйте создать задание с одним вложением для проверки');
                DBMS_OUTPUT.PUT_LINE('    3. Обратитесь к администратору БД для исправления представления v_email_xml');
            ELSE
                DBMS_OUTPUT.PUT_LINE('  Возможные причины:');
                DBMS_OUTPUT.PUT_LINE('    - Проблемы с очередью AQ');
                DBMS_OUTPUT.PUT_LINE('    - Недостаточно прав для записи в очередь');
                DBMS_OUTPUT.PUT_LINE('    - Ошибка формирования XML из v_email_xml');
            END IF;
            
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('  Задание создано, но не отправлено в очередь!');
            DBMS_OUTPUT.PUT_LINE('  Задание ID: ' || v_task_id);
            DBMS_OUTPUT.PUT_LINE('  Вложений создано: ' || v_attach_count);
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('  ВАРИАНТ 1: Попробуйте отправить вручную (может не помочь, если проблема в v_email_xml):');
            DBMS_OUTPUT.PUT_LINE('    UPDATE pcsystem.email_task');
            DBMS_OUTPUT.PUT_LINE('    SET date_delay_send = SYSDATE + 1/86400');
            DBMS_OUTPUT.PUT_LINE('    WHERE email_task_id = ' || v_task_id || ';');
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('    DECLARE');
            DBMS_OUTPUT.PUT_LINE('      v_err_code NUMBER;');
            DBMS_OUTPUT.PUT_LINE('      v_err_desc VARCHAR2(4000);');
            DBMS_OUTPUT.PUT_LINE('    BEGIN');
            DBMS_OUTPUT.PUT_LINE('      pcsystem.pkg_email.send_email_request(');
            DBMS_OUTPUT.PUT_LINE('        p_email_task_id => ' || v_task_id || ',');
            DBMS_OUTPUT.PUT_LINE('        p_err_code => v_err_code,');
            DBMS_OUTPUT.PUT_LINE('        p_err_desc => v_err_desc');
            DBMS_OUTPUT.PUT_LINE('      );');
            DBMS_OUTPUT.PUT_LINE('      DBMS_OUTPUT.PUT_LINE(''Код ошибки: '' || v_err_code);');
            DBMS_OUTPUT.PUT_LINE('      DBMS_OUTPUT.PUT_LINE(''Описание: '' || v_err_desc);');
            DBMS_OUTPUT.PUT_LINE('    END;');
            DBMS_OUTPUT.PUT_LINE('    /');
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('  ВАРИАНТ 2: Проверьте XML, который формирует v_email_xml:');
            DBMS_OUTPUT.PUT_LINE('    SELECT xml_data FROM pcsystem.v_email_xml WHERE email_task_id = ' || v_task_id || ';');
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('  ВАРИАНТ 3: Если проблема в нескольких вложениях, попробуйте создать задание с одним вложением');
            RETURN;
        ELSE
            DBMS_OUTPUT.PUT_LINE('  ✓ Задание успешно отправлено в очередь AQ!');
        END IF;
    EXCEPTION
        WHEN OTHERS THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ИСКЛЮЧЕНИЕ при отправке в очередь: ' || SQLERRM);
            DBMS_OUTPUT.PUT_LINE('  SQLCODE: ' || SQLCODE);
            DBMS_OUTPUT.PUT_LINE('  Задание ID: ' || v_task_id);
            DBMS_OUTPUT.PUT_LINE('  Вложений создано: ' || v_attach_count);
            RETURN;
    END;
    
    DBMS_OUTPUT.PUT_LINE('  ✓ Задание успешно отправлено в очередь AQ');
    
    -- ========== ИТОГОВАЯ ИНФОРМАЦИЯ ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('✓ ВСЕ ШАГИ ВЫПОЛНЕНЫ УСПЕШНО');
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('Итоговая информация:');
    DBMS_OUTPUT.PUT_LINE('  Задание ID: ' || v_task_id);
    DBMS_OUTPUT.PUT_LINE('  Email получателя: ' || v_email_address);
    DBMS_OUTPUT.PUT_LINE('  Вложений типа 2 создано: ' || v_attach_count);
    DBMS_OUTPUT.PUT_LINE('  Статус: Отправлено в очередь AQ');
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('Следующие шаги:');
    DBMS_OUTPUT.PUT_LINE('  1. Убедитесь, что Go-сервис email-service запущен');
    DBMS_OUTPUT.PUT_LINE('  2. Сервис автоматически прочитает сообщение из очереди AQ');
    DBMS_OUTPUT.PUT_LINE('  3. Сервис получит CLOB из БД через get_email_report_clob()');
    DBMS_OUTPUT.PUT_LINE('  4. Сервис декодирует Base64 и отправит письмо через SMTP');
    DBMS_OUTPUT.PUT_LINE('  5. Проверьте статус отправки:');
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('     SELECT ');
    DBMS_OUTPUT.PUT_LINE('       email_task_id,');
    DBMS_OUTPUT.PUT_LINE('       email_address,');
    DBMS_OUTPUT.PUT_LINE('       email_status_id,');
    DBMS_OUTPUT.PUT_LINE('       CASE email_status_id');
    DBMS_OUTPUT.PUT_LINE('         WHEN 1 THEN ''Новый''');
    DBMS_OUTPUT.PUT_LINE('         WHEN 2 THEN ''Отправлено''');
    DBMS_OUTPUT.PUT_LINE('         WHEN 3 THEN ''Ошибка''');
    DBMS_OUTPUT.PUT_LINE('         WHEN 4 THEN ''Успешно отправлено''');
    DBMS_OUTPUT.PUT_LINE('       END AS status_name,');
    DBMS_OUTPUT.PUT_LINE('       date_response,');
    DBMS_OUTPUT.PUT_LINE('       error_text');
    DBMS_OUTPUT.PUT_LINE('     FROM pcsystem.email_task');
    DBMS_OUTPUT.PUT_LINE('     WHERE email_task_id = ' || v_task_id || ';');
    DBMS_OUTPUT.PUT_LINE('');
    
EXCEPTION
    WHEN OTHERS THEN
        DBMS_OUTPUT.PUT_LINE('');
        DBMS_OUTPUT.PUT_LINE('========================================');
        DBMS_OUTPUT.PUT_LINE('✗ КРИТИЧЕСКАЯ ОШИБКА');
        DBMS_OUTPUT.PUT_LINE('========================================');
        DBMS_OUTPUT.PUT_LINE('SQLCODE: ' || SQLCODE);
        DBMS_OUTPUT.PUT_LINE('SQLERRM: ' || SQLERRM);
        DBMS_OUTPUT.PUT_LINE('');
        
        IF v_task_id IS NOT NULL THEN
            DBMS_OUTPUT.PUT_LINE('Задание было создано с ID: ' || v_task_id);
            DBMS_OUTPUT.PUT_LINE('Вы можете попробовать продолжить вручную:');
            DBMS_OUTPUT.PUT_LINE('  1. Проверьте вложения: SELECT * FROM pcsystem.email_attach WHERE email_task_id = ' || v_task_id || ';');
            DBMS_OUTPUT.PUT_LINE('  2. Если вложения нет, создайте его:');
            DBMS_OUTPUT.PUT_LINE('     INSERT INTO pcsystem.email_attach (...) VALUES (...);');
            DBMS_OUTPUT.PUT_LINE('  3. Обновите date_delay_send: UPDATE pcsystem.email_task SET date_delay_send = SYSDATE + 1/86400 WHERE email_task_id = ' || v_task_id || ';');
            DBMS_OUTPUT.PUT_LINE('  4. Отправьте в очередь: pcsystem.pkg_email.send_email_request(...)');
        END IF;
        
        RAISE;
END;
/

COMMIT;
