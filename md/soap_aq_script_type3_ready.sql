-- ============================================================================
-- ГОТОВЫЙ СКРИПТ: Отправка письма с вложением типа 3 из таблицы edoc
-- ============================================================================
-- Этот скрипт готов к использованию с данными из таблицы pcsystem.edoc.
-- Используется процедура create_email_attach_esign для автоматического
-- создания вложения на основе edoc_id.
--
-- ИНСТРУКЦИЯ ПО ИСПОЛЬЗОВАНИЮ:
-- 1. Укажите нужный edoc_id в переменной v_parametr_value (строка 38)
-- 2. Укажите email получателя в переменной v_email_address (строка 39)
-- 3. Настройте остальные параметры при необходимости (email_type_id, branch_id, smtp_id)
-- 4. Запустите скрипт
--
-- ПРИМЕР ДАННЫХ ИЗ ТАБЛИЦЫ EDOC:
--   edoc_id | file_name              | file_path
--   --------|------------------------|------------------
--   61      | ASK_6365065_1.XML      | \OBN\SEMD\2021\08\
--   62      | ASK_6383031_1.XML      | \OBN\SEMD\2021\08\
--   64      | ASK_6363978_1.XML     | \OBN\SEMD\2021\08\
--   ...     | ...                    | ...
--
-- Процедура create_email_attach_esign автоматически:
-- 1. Найдет запись в таблице edoc по edoc_id
-- 2. Сформирует полный путь: storage.storage_path || edoc.file_path || edoc.file_name
-- 3. Создаст вложение типа 3 с путем к файлу
-- 4. Использует имя файла из edoc.file_name
-- ============================================================================

DECLARE
    -- ========== НАСТРОЙКИ ==========
    v_email_type_id NUMBER := 10;                    -- ID типа email из справочника
    v_parametr_id NUMBER := 5;                        -- parametr_id (5 = edoc_id для ЭЦП документов)
    v_parametr_value NUMBER := 61;                    -- edoc_id из таблицы edoc (ИЗМЕНИТЕ НА НУЖНЫЙ!)
    v_email_address VARCHAR2(500) := 'recipient@example.com';  -- Email получателя (ИЗМЕНИТЕ!)
    v_email_title VARCHAR2(1000) := 'Электронный документ с ЭЦП';
    v_email_text VARCHAR2(4000) := 'Вам направлен электронный документ с электронной цифровой подписью.';
    v_branch_id NUMBER := 1;                         -- ID территории
    v_smtp_id NUMBER := 1;                           -- ID SMTP сервера
    
    -- ========== ВНУТРЕННИЕ ПЕРЕМЕННЫЕ ==========
    v_task_id NUMBER;
    v_attach_id NUMBER;
    v_err_code NUMBER;
    v_err_desc VARCHAR2(4000);
    v_attach_count NUMBER;
    v_email_attach_name VARCHAR2(1000);
    v_report_file VARCHAR2(4000);
BEGIN
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('Создание задания с вложением типа 3 из edoc');
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('Параметры:');
    DBMS_OUTPUT.PUT_LINE('  email_type_id: ' || v_email_type_id);
    DBMS_OUTPUT.PUT_LINE('  parametr_id: ' || v_parametr_id || ' (5 = edoc_id для ЭЦП)');
    DBMS_OUTPUT.PUT_LINE('  parametr_value: ' || v_parametr_value || ' (edoc_id)');
    DBMS_OUTPUT.PUT_LINE('  email_address: ' || v_email_address);
    DBMS_OUTPUT.PUT_LINE('  branch_id: ' || v_branch_id);
    DBMS_OUTPUT.PUT_LINE('  smtp_id: ' || v_smtp_id);
    DBMS_OUTPUT.PUT_LINE('');
    
    -- Проверяем существование edoc_id перед началом работы
    DECLARE
        v_edoc_exists NUMBER;
        v_edoc_file_name VARCHAR2(1000);
        v_edoc_file_path VARCHAR2(4000);
    BEGIN
        SELECT COUNT(*), MAX(file_name), MAX(file_path)
        INTO v_edoc_exists, v_edoc_file_name, v_edoc_file_path
        FROM pcsystem.edoc
        WHERE edoc_id = v_parametr_value;
        
        IF v_edoc_exists = 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: edoc_id ' || v_parametr_value || ' не найден в таблице edoc!');
            DBMS_OUTPUT.PUT_LINE('  Проверьте существование записи:');
            DBMS_OUTPUT.PUT_LINE('    SELECT * FROM pcsystem.edoc WHERE edoc_id = ' || v_parametr_value || ';');
            RETURN;
        END IF;
        
        DBMS_OUTPUT.PUT_LINE('  ✓ edoc_id найден в таблице edoc');
        DBMS_OUTPUT.PUT_LINE('  ✓ Имя файла: ' || v_edoc_file_name);
        DBMS_OUTPUT.PUT_LINE('  ✓ Путь: ' || v_edoc_file_path);
        DBMS_OUTPUT.PUT_LINE('');
    END;
    
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
    
    -- ========== ШАГ 2: Создание вложения типа 3 через create_email_attach_esign ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 2: Создание вложения типа 3 через create_email_attach_esign...');
    
    BEGIN
        DBMS_OUTPUT.PUT_LINE('  Используется процедура create_email_attach_esign');
        DBMS_OUTPUT.PUT_LINE('  edoc_id: ' || v_parametr_value);
        
        pcsystem.pkg_email.create_email_attach_esign(
            p_email_task_id => v_task_id,
            p_parametr_value => v_parametr_value,
            p_err_code => v_err_code,
            p_err_desc => v_err_desc
        );
        
        IF v_err_code != 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА создания вложения: ' || v_err_desc);
            DBMS_OUTPUT.PUT_LINE('  Код ошибки: ' || v_err_code);
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('  Возможные причины:');
            DBMS_OUTPUT.PUT_LINE('    - edoc_id не найден в таблице edoc');
            DBMS_OUTPUT.PUT_LINE('    - Не настроена связь с таблицей storage');
            DBMS_OUTPUT.PUT_LINE('    - Файл не существует по сформированному пути');
            DBMS_OUTPUT.PUT_LINE('    - Ошибка в процедуре create_email_attach_esign');
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('  Проверьте:');
            DBMS_OUTPUT.PUT_LINE('    SELECT * FROM pcsystem.edoc WHERE edoc_id = ' || v_parametr_value || ';');
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('  Задание создано с ID: ' || v_task_id);
            DBMS_OUTPUT.PUT_LINE('  Вы можете попробовать создать вложение вручную или исправить ошибку.');
            RETURN;
        END IF;
        
        DBMS_OUTPUT.PUT_LINE('  ✓ Вложение создано успешно через create_email_attach_esign');
        
        -- Получаем информацию о созданном вложении
        SELECT 
            email_attach_id,
            email_attach_name,
            report_file
        INTO 
            v_attach_id,
            v_email_attach_name,
            v_report_file
        FROM pcsystem.email_attach
        WHERE email_task_id = v_task_id
          AND report_type = 3
        FETCH FIRST 1 ROWS ONLY;
        
        DBMS_OUTPUT.PUT_LINE('  ✓ Вложение ID: ' || v_attach_id);
        DBMS_OUTPUT.PUT_LINE('  ✓ Имя файла: ' || v_email_attach_name);
        DBMS_OUTPUT.PUT_LINE('  ✓ Полный путь к файлу: ' || v_report_file);
        
    EXCEPTION
        WHEN NO_DATA_FOUND THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: Вложение не найдено после создания!');
            DBMS_OUTPUT.PUT_LINE('  Проверьте таблицу email_attach:');
            DBMS_OUTPUT.PUT_LINE('    SELECT * FROM pcsystem.email_attach WHERE email_task_id = ' || v_task_id || ';');
            RETURN;
        WHEN OTHERS THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: ' || SQLERRM);
            DBMS_OUTPUT.PUT_LINE('  SQLCODE: ' || SQLCODE);
            RETURN;
    END;
    
    -- Проверяем, что вложение создано
    SELECT COUNT(*) INTO v_attach_count
    FROM pcsystem.email_attach
    WHERE email_task_id = v_task_id
      AND report_type = 3;
    
    IF v_attach_count = 0 THEN
        DBMS_OUTPUT.PUT_LINE('');
        DBMS_OUTPUT.PUT_LINE('  ⚠ ВНИМАНИЕ: Не создано ни одного вложения типа 3!');
        DBMS_OUTPUT.PUT_LINE('  Задание создано с ID: ' || v_task_id);
        DBMS_OUTPUT.PUT_LINE('  Вы можете создать вложение позже и отправить задание вручную.');
        RETURN;
    END IF;
    
    DBMS_OUTPUT.PUT_LINE('  ✓ Создано вложений типа 3: ' || v_attach_count);
    
    -- ========== ШАГ 3: Вывод информации о созданных данных ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 3: Информация о созданных данных...');
    DBMS_OUTPUT.PUT_LINE('  Задание ID: ' || v_task_id);
    DBMS_OUTPUT.PUT_LINE('  Email получателя: ' || v_email_address);
    DBMS_OUTPUT.PUT_LINE('  Заголовок: ' || v_email_title);
    DBMS_OUTPUT.PUT_LINE('  Вложений типа 3: ' || v_attach_count);
    DBMS_OUTPUT.PUT_LINE('');
    
    IF v_attach_count > 0 THEN
        DBMS_OUTPUT.PUT_LINE('  Детали вложений:');
        FOR rec IN (
            SELECT 
                ea.email_attach_id,
                ea.email_attach_name,
                ea.report_file,
                ea.report_type
            FROM pcsystem.email_attach ea
            WHERE ea.email_task_id = v_task_id
              AND ea.report_type = 3
            ORDER BY ea.email_attach_id
        ) LOOP
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('    Вложение ID: ' || rec.email_attach_id);
            DBMS_OUTPUT.PUT_LINE('      Имя для получателя: ' || rec.email_attach_name);
            DBMS_OUTPUT.PUT_LINE('      Полный путь к файлу: ' || rec.report_file);
            DBMS_OUTPUT.PUT_LINE('      Тип: ' || rec.report_type || ' (готовый файл)');
            DBMS_OUTPUT.PUT_LINE('      ВАЖНО: Убедитесь, что файл существует по указанному пути!');
        END LOOP;
    END IF;
    
    -- ========== ШАГ 4: Обновление date_delay_send НЕПОСРЕДСТВЕННО ПЕРЕД отправкой ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 4: Обновление date_delay_send непосредственно перед отправкой...');
    
    UPDATE pcsystem.email_task
    SET date_delay_send = SYSDATE + 1/86400  -- +1 секунда для гарантии положительного DELAY
    WHERE email_task_id = v_task_id;
    
    DBMS_OUTPUT.PUT_LINE('  ✓ date_delay_send обновлено на SYSDATE + 1 секунда');
    DBMS_OUTPUT.PUT_LINE('  Текущее время: ' || TO_CHAR(SYSDATE, 'YYYY-MM-DD HH24:MI:SS'));
    DBMS_OUTPUT.PUT_LINE('  date_delay_send: ' || TO_CHAR(SYSDATE + 1/86400, 'YYYY-MM-DD HH24:MI:SS'));
    
    -- ========== ШАГ 5: Отправка задания в очередь AQ ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 5: Отправка задания в очередь AQ...');
    
    pcsystem.pkg_email.send_email_request(
        p_email_task_id => v_task_id,
        p_err_code => v_err_code,
        p_err_desc => v_err_desc
    );
    
    IF v_err_code != 0 THEN
        DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА отправки в очередь: ' || v_err_desc);
        DBMS_OUTPUT.PUT_LINE('  Код ошибки: ' || v_err_code);
        DBMS_OUTPUT.PUT_LINE('');
        DBMS_OUTPUT.PUT_LINE('  Возможные причины:');
        DBMS_OUTPUT.PUT_LINE('    - Проблемы с очередью AQ');
        DBMS_OUTPUT.PUT_LINE('    - Недостаточно прав для записи в очередь');
        DBMS_OUTPUT.PUT_LINE('    - Ошибка формирования XML из v_email_xml');
        DBMS_OUTPUT.PUT_LINE('');
        DBMS_OUTPUT.PUT_LINE('  Задание создано, но не отправлено в очередь!');
        DBMS_OUTPUT.PUT_LINE('  Задание ID: ' || v_task_id);
        DBMS_OUTPUT.PUT_LINE('  Вы можете попробовать отправить вручную:');
        DBMS_OUTPUT.PUT_LINE('    -- Сначала обновите date_delay_send:');
        DBMS_OUTPUT.PUT_LINE('    UPDATE pcsystem.email_task');
        DBMS_OUTPUT.PUT_LINE('    SET date_delay_send = SYSDATE + 1/86400');
        DBMS_OUTPUT.PUT_LINE('    WHERE email_task_id = ' || v_task_id || ';');
        DBMS_OUTPUT.PUT_LINE('');
        DBMS_OUTPUT.PUT_LINE('    -- Затем отправьте в очередь:');
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
        RETURN;
    END IF;
    
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
    DBMS_OUTPUT.PUT_LINE('  edoc_id: ' || v_parametr_value);
    DBMS_OUTPUT.PUT_LINE('  Вложений типа 3 создано: ' || v_attach_count);
    DBMS_OUTPUT.PUT_LINE('  Статус: Отправлено в очередь AQ');
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('Следующие шаги:');
    DBMS_OUTPUT.PUT_LINE('  1. Убедитесь, что Go-сервис email-service запущен');
    DBMS_OUTPUT.PUT_LINE('  2. Сервис автоматически прочитает сообщение из очереди AQ');
    DBMS_OUTPUT.PUT_LINE('  3. Сервис прочитает файл с диска по указанному пути');
    DBMS_OUTPUT.PUT_LINE('  4. Сервис отправит письмо через SMTP с вложением');
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
    DBMS_OUTPUT.PUT_LINE('ВАЖНО: Убедитесь, что файл существует по указанному пути!');
    DBMS_OUTPUT.PUT_LINE('  Если файл не найден, Go-сервис вернет ошибку при обработке.');
    DBMS_OUTPUT.PUT_LINE('  Полный путь к файлу формируется как:');
    DBMS_OUTPUT.PUT_LINE('    storage.storage_path || edoc.file_path || edoc.file_name');
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
            DBMS_OUTPUT.PUT_LINE('  2. Создайте вложение типа 3: INSERT INTO pcsystem.email_attach (...) VALUES (...);');
            DBMS_OUTPUT.PUT_LINE('  3. Обновите date_delay_send: UPDATE pcsystem.email_task SET date_delay_send = SYSDATE + 1/86400 WHERE email_task_id = ' || v_task_id || ';');
            DBMS_OUTPUT.PUT_LINE('  4. Отправьте в очередь: pcsystem.pkg_email.send_email_request(...)');
        END IF;
        
        RAISE;
END;
/

COMMIT;
