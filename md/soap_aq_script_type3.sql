-- ============================================================================
-- СКРИПТ: Создание задания с вложением типа 3 (готовый файл с диска) и отправка в очередь AQ
-- ============================================================================
-- Этот скрипт создает задание на отправку письма с вложением типа 3 (готовый файл).
-- Тип 3 используется для электронных документов с ЭЦП (электронной цифровой подписью),
-- которые уже подписаны и хранятся на файловой системе сервера.
--
-- СВЯЗЬ С ЭЦП:
-- ============
-- Тип 3 вложений специально предназначен для отправки электронных документов с ЭЦП.
-- Эти документы:
-- 1. Уже подписаны электронной цифровой подписью (ЭЦП)
-- 2. Хранятся на файловой системе сервера в виде готовых PDF файлов
-- 3. Не требуют генерации через SOAP сервис (в отличие от типа 1)
-- 4. Не требуют загрузки из БД в формате CLOB (в отличие от типа 2)
--
-- Процедура create_email_attach_esign работает с таблицей edoc (электронные документы)
-- и формирует полный путь к файлу как:
--   storage.storage_path || edoc.file_path || edoc.file_name
--
-- Где:
--   - storage.storage_path - базовый путь к хранилищу документов
--   - edoc.file_path - относительный путь к файлу в хранилище
--   - edoc.file_name - имя файла документа
--
-- ИНСТРУКЦИЯ ПО ИСПОЛЬЗОВАНИЮ:
-- 1. Убедитесь, что файл существует на сервере по указанному пути
-- 2. Если используете edoc_id, убедитесь, что запись существует в таблице edoc
-- 3. Установите правильные значения переменных в разделе НАСТРОЙКИ
-- 4. Выберите подходящий способ создания вложения (ВАРИАНТ 1 или 2) в разделе ШАГ 2
-- 5. Запустите скрипт
-- ============================================================================

DECLARE
    -- ========== НАСТРОЙКИ ==========
    v_email_type_id NUMBER := 10;                    -- ID типа email из справочника
    v_parametr_id NUMBER := 5;                        -- parametr_id (5 = edoc_id для ЭЦП документов)
    v_parametr_value NUMBER := 12345;                -- Значение параметра (edoc_id)
    v_email_address VARCHAR2(500) := 'recipient@example.com';  -- Email получателя
    v_email_title VARCHAR2(1000) := 'Электронный документ с ЭЦП';
    v_email_text VARCHAR2(4000) := 'Вам направлен электронный документ с электронной цифровой подписью.';
    v_branch_id NUMBER := 1;                         -- ID территории
    v_smtp_id NUMBER := 1;                           -- ID SMTP сервера
    
    -- ========== НАСТРОЙКИ ДЛЯ ВЛОЖЕНИЯ ТИПА 3 ==========
    -- ВАРИАНТ 1: Использование процедуры create_email_attach_esign (РЕКОМЕНДУЕТСЯ)
    -- Процедура автоматически создаст вложение типа 3 на основе edoc_id
    -- и сформирует полный путь к файлу из таблиц storage и edoc
    
    -- ВАРИАНТ 2: Ручное создание вложения с указанием пути к файлу
    -- Используйте этот вариант, если файл не связан с таблицей edoc
    v_report_file VARCHAR2(4000);                     -- Полный путь к файлу на сервере
    -- Примеры путей:
    -- v_report_file := '/opt/storage/documents/2024/12/document_12345.pdf';
    -- v_report_file := 'C:\Storage\Documents\2024\12\document_12345.pdf';
    -- v_report_file := '\\server\share\documents\document_12345.pdf';
    
    v_email_attach_name VARCHAR2(1000);              -- Имя вложения (для получателя)
    
    -- ========== ВНУТРЕННИЕ ПЕРЕМЕННЫЕ ==========
    v_task_id NUMBER;
    v_attach_id NUMBER;
    v_err_code NUMBER;
    v_err_desc VARCHAR2(4000);
    v_attach_count NUMBER;
BEGIN
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('Создание задания с вложением типа 3 (готовый файл)');
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('Параметры:');
    DBMS_OUTPUT.PUT_LINE('  email_type_id: ' || v_email_type_id);
    DBMS_OUTPUT.PUT_LINE('  parametr_id: ' || v_parametr_id || ' (5 = edoc_id для ЭЦП)');
    DBMS_OUTPUT.PUT_LINE('  parametr_value: ' || v_parametr_value || ' (edoc_id)');
    DBMS_OUTPUT.PUT_LINE('  email_address: ' || v_email_address);
    DBMS_OUTPUT.PUT_LINE('  branch_id: ' || v_branch_id);
    DBMS_OUTPUT.PUT_LINE('  smtp_id: ' || v_smtp_id);
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
    
    -- ========== ШАГ 2: Создание вложения типа 3 ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 2: Создание вложения типа 3 (готовый файл)...');
    DBMS_OUTPUT.PUT_LINE('  ВНИМАНИЕ: Выберите один из вариантов ниже!');
    DBMS_OUTPUT.PUT_LINE('');
    
    -- ============================================================================
    -- ВАРИАНТ 1: Использование процедуры create_email_attach_esign (РЕКОМЕНДУЕТСЯ)
    -- ============================================================================
    -- Этот вариант использует процедуру create_email_attach_esign, которая:
    -- 1. Ищет запись в таблице edoc по edoc_id (parametr_value)
    -- 2. Формирует полный путь к файлу: storage.storage_path || edoc.file_path || edoc.file_name
    -- 3. Создает запись в email_attach с report_type = 3 и report_file = полный путь
    -- 4. Использует имя файла из edoc.file_name
    --
    -- ТРЕБОВАНИЯ:
    -- - parametr_id должен быть равен 5 (edoc_id)
    -- - parametr_value должен быть валидным edoc_id
    -- - Запись должна существовать в таблице edoc
    -- - Должна быть настроена связь с таблицей storage для получения storage_path
    -- ============================================================================
    
    -- Раскомментируйте блок ниже для использования ВАРИАНТА 1:
    /*
    BEGIN
        DBMS_OUTPUT.PUT_LINE('  Используется ВАРИАНТ 1: create_email_attach_esign');
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
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('  Проверьте:');
            DBMS_OUTPUT.PUT_LINE('    SELECT * FROM pcsystem.edoc WHERE edoc_id = ' || v_parametr_value || ';');
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
        DBMS_OUTPUT.PUT_LINE('  ✓ Путь к файлу: ' || v_report_file);
        
    EXCEPTION
        WHEN NO_DATA_FOUND THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: Вложение не найдено после создания!');
            DBMS_OUTPUT.PUT_LINE('  Проверьте таблицу email_attach:');
            DBMS_OUTPUT.PUT_LINE('    SELECT * FROM pcsystem.email_attach WHERE email_task_id = ' || v_task_id || ';');
            RETURN;
        WHEN OTHERS THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: ' || SQLERRM);
            RETURN;
    END;
    */
    
    -- ============================================================================
    -- ВАРИАНТ 2: Ручное создание вложения с указанием пути к файлу
    -- ============================================================================
    -- Этот вариант позволяет создать вложение типа 3 вручную, указав полный путь к файлу.
    -- Используйте этот вариант, если:
    -- - Файл не связан с таблицей edoc
    -- - Нужно указать произвольный путь к файлу
    -- - Файл находится в нестандартном месте
    --
    -- ВАЖНО:
    -- - Путь должен быть абсолютным (полным)
    -- - Файл должен существовать на сервере по указанному пути
    -- - Сервер должен иметь права на чтение файла
    -- - Путь должен быть доступен для Go-сервиса email-service
    -- ============================================================================
    
    -- Раскомментируйте и настройте блок ниже для использования ВАРИАНТА 2:
    /*
    BEGIN
        DBMS_OUTPUT.PUT_LINE('  Используется ВАРИАНТ 2: Ручное создание вложения');
        
        -- УКАЖИТЕ ПУТЬ К ФАЙЛУ:
        v_report_file := '/opt/storage/documents/2024/12/document_12345.pdf';
        -- ИЛИ для Windows:
        -- v_report_file := 'C:\Storage\Documents\2024\12\document_12345.pdf';
        -- ИЛИ для сетевого пути:
        -- v_report_file := '\\server\share\documents\document_12345.pdf';
        
        -- УКАЖИТЕ ИМЯ ВЛОЖЕНИЯ (для получателя):
        v_email_attach_name := 'Документ_с_ЭЦП.pdf';
        -- ИЛИ используйте имя из пути:
        -- v_email_attach_name := SUBSTR(v_report_file, INSTR(v_report_file, '/', -1) + 1);
        -- Для Windows:
        -- v_email_attach_name := SUBSTR(v_report_file, INSTR(v_report_file, '\', -1) + 1);
        
        IF v_report_file IS NULL OR LENGTH(TRIM(v_report_file)) = 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: v_report_file не заполнен!');
            DBMS_OUTPUT.PUT_LINE('  Укажите путь к файлу в переменной v_report_file');
            RETURN;
        END IF;
        
        IF v_email_attach_name IS NULL OR LENGTH(TRIM(v_email_attach_name)) = 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ⚠ Предупреждение: имя вложения не указано, используется имя из пути');
            v_email_attach_name := SUBSTR(v_report_file, INSTR(v_report_file, '/', -1) + 1);
            IF v_email_attach_name = v_report_file THEN
                -- Пробуем обратный слэш для Windows
                v_email_attach_name := SUBSTR(v_report_file, INSTR(v_report_file, '\', -1) + 1);
            END IF;
        END IF;
        
        DBMS_OUTPUT.PUT_LINE('  ✓ Путь к файлу: ' || v_report_file);
        DBMS_OUTPUT.PUT_LINE('  ✓ Имя вложения: ' || v_email_attach_name);
        
        -- Создаем вложение вручную
        INSERT INTO pcsystem.email_attach (
            email_attach_id,
            email_task_id,
            email_attach_name,
            report_type,
            report_file
        ) VALUES (
            pcsystem.seq_email_attach.NEXTVAL,
            v_task_id,
            v_email_attach_name,
            3,  -- report_type = 3 (готовый файл)
            v_report_file
        )
        RETURNING email_attach_id INTO v_attach_id;
        
        DBMS_OUTPUT.PUT_LINE('  ✓ Вложение создано успешно');
        DBMS_OUTPUT.PUT_LINE('  ✓ Вложение ID: ' || v_attach_id);
        
    EXCEPTION
        WHEN OTHERS THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА создания вложения: ' || SQLERRM);
            RETURN;
    END;
    */
    
    -- ============================================================================
    -- ВАРИАНТ 2А: Копирование вложения из существующего email_attach
    -- ============================================================================
    -- Если у вас уже есть вложение типа 3 в другой задаче, можно скопировать его
    -- Раскомментируйте и укажите email_attach_id для копирования:
    /*
    DECLARE
        v_source_attach_id NUMBER := 1093635;  -- УКАЖИТЕ ID существующего вложения типа 3
        v_source_report_file VARCHAR2(4000);
        v_source_attach_name VARCHAR2(1000);
    BEGIN
        DBMS_OUTPUT.PUT_LINE('  Используется ВАРИАНТ 2А: Копирование из существующего вложения');
        DBMS_OUTPUT.PUT_LINE('  Источник: email_attach_id = ' || v_source_attach_id);
        
        -- Получаем данные из существующего вложения
        SELECT 
            report_file,
            email_attach_name
        INTO 
            v_source_report_file,
            v_source_attach_name
        FROM pcsystem.email_attach
        WHERE email_attach_id = v_source_attach_id
          AND report_type = 3;
        
        IF v_source_report_file IS NULL THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: report_file пуст в источнике!');
            RETURN;
        END IF;
        
        DBMS_OUTPUT.PUT_LINE('  ✓ Путь к файлу (скопирован): ' || v_source_report_file);
        DBMS_OUTPUT.PUT_LINE('  ✓ Имя вложения (скопировано): ' || v_source_attach_name);
        
        -- Создаем новое вложение с теми же данными
        INSERT INTO pcsystem.email_attach (
            email_attach_id,
            email_task_id,
            email_attach_name,
            report_type,
            report_file
        ) VALUES (
            pcsystem.seq_email_attach.NEXTVAL,
            v_task_id,
            v_source_attach_name,
            3,  -- report_type = 3
            v_source_report_file
        )
        RETURNING email_attach_id INTO v_attach_id;
        
        DBMS_OUTPUT.PUT_LINE('  ✓ Вложение скопировано успешно');
        DBMS_OUTPUT.PUT_LINE('  ✓ Новое вложение ID: ' || v_attach_id);
        
    EXCEPTION
        WHEN NO_DATA_FOUND THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: Вложение с ID ' || v_source_attach_id || ' не найдено или не является типом 3!');
            DBMS_OUTPUT.PUT_LINE('  Проверьте: SELECT * FROM pcsystem.email_attach WHERE email_attach_id = ' || v_source_attach_id || ';');
            RETURN;
        WHEN OTHERS THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: ' || SQLERRM);
            RETURN;
    END;
    */
    
    -- ========== ПРОВЕРКА: Убедитесь, что один из вариантов раскомментирован ==========
    -- Если ни один вариант не раскомментирован, выводим предупреждение
    SELECT COUNT(*) INTO v_attach_count
    FROM pcsystem.email_attach
    WHERE email_task_id = v_task_id
      AND report_type = 3;
    
    IF v_attach_count = 0 THEN
        DBMS_OUTPUT.PUT_LINE('');
        DBMS_OUTPUT.PUT_LINE('  ⚠ ВНИМАНИЕ: Не создано ни одного вложения типа 3!');
        DBMS_OUTPUT.PUT_LINE('  Необходимо раскомментировать один из вариантов в разделе ШАГ 2:');
        DBMS_OUTPUT.PUT_LINE('    - ВАРИАНТ 1: Использование create_email_attach_esign (для edoc)');
        DBMS_OUTPUT.PUT_LINE('    - ВАРИАНТ 2: Ручное создание с указанием пути');
        DBMS_OUTPUT.PUT_LINE('    - ВАРИАНТ 2А: Копирование из существующего вложения');
        DBMS_OUTPUT.PUT_LINE('');
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
            DBMS_OUTPUT.PUT_LINE('      Путь к файлу: ' || rec.report_file);
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
