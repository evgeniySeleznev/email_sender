-- ============================================================================
-- ИСПРАВЛЕННЫЙ СКРИПТ: Создание задания через INSERT и отправка в очередь AQ
-- ============================================================================
-- Исправлена ошибка ORA-25203: DELAY должен быть положительным
-- Проблема: date_delay_send обновлялся в шаге 4, но к моменту отправки в очередь
-- (шаг 6) уже проходило время, и DELAY становился отрицательным
-- Решение: обновляем date_delay_send непосредственно перед отправкой в очередь
--
-- ВАЖНО ДЛЯ ВЛОЖЕНИЙ ТИПА 1 (Crystal Reports):
-- Для корректной работы необходимо, чтобы в таблице email_attach были заполнены
-- поля db_login и db_pass. Если они не заполнены, Go-сервис попытается использовать
-- значения из конфигурации (Oracle.User/Oracle.Password), но если и там они не указаны,
-- возникнет ошибка "NullPointerException" на стороне Crystal Reports сервера.
--
-- Проверка наличия db_login/db_pass выполняется автоматически после создания вложений.
-- ============================================================================

DECLARE
    -- ========== НАСТРОЙКИ ==========
    v_email_type_id NUMBER := 10;                    -- ID типа email из справочника
    v_parametr_id NUMBER := 3;                       -- parametr_id (например, 3 = document_id)
    v_parametr_value NUMBER := 12345;                -- Значение параметра
    v_email_address VARCHAR2(500) := 'recipient@example.com';  -- Email получателя
    v_email_title VARCHAR2(1000) := 'Заголовок письма';
    v_email_text VARCHAR2(4000) := 'Текст письма';
    v_branch_id NUMBER := 1;                         -- ID территории
    v_smtp_id NUMBER := 1;                           -- ID SMTP сервера
    
    -- ========== ВНУТРЕННИЕ ПЕРЕМЕННЫЕ ==========
    v_task_id NUMBER;
    v_err_code NUMBER;
    v_err_desc VARCHAR2(4000);
    v_attach_count NUMBER;
    v_param_count NUMBER;
BEGIN
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('Создание задания через INSERT');
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('Параметры:');
    DBMS_OUTPUT.PUT_LINE('  email_type_id: ' || v_email_type_id);
    DBMS_OUTPUT.PUT_LINE('  parametr_id: ' || v_parametr_id);
    DBMS_OUTPUT.PUT_LINE('  parametr_value: ' || v_parametr_value);
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
    
    -- ========== ШАГ 2: Создание вложений типа 1 (Crystal Reports) ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 2: Создание вложений типа 1 (Crystal Reports)...');
    
    pcsystem.pkg_email.create_email_attach(
        p_email_task_id => v_task_id,
        p_parametr_value => v_parametr_value,
        p_err_code => v_err_code,
        p_err_desc => v_err_desc
    );
    
    IF v_err_code != 0 THEN
        DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА создания вложений: ' || v_err_desc);
        DBMS_OUTPUT.PUT_LINE('  Код ошибки: ' || v_err_code);
        DBMS_OUTPUT.PUT_LINE('');
        DBMS_OUTPUT.PUT_LINE('  ВНИМАНИЕ: Задание создано, но вложения не добавлены!');
        DBMS_OUTPUT.PUT_LINE('  Задание ID: ' || v_task_id);
        RETURN;
    END IF;
    
    -- Проверяем созданные вложения
    SELECT COUNT(*) INTO v_attach_count
    FROM pcsystem.email_attach
    WHERE email_task_id = v_task_id
      AND report_type = 1;
    
    IF v_attach_count = 0 THEN
        DBMS_OUTPUT.PUT_LINE('  ⚠ ПРЕДУПРЕЖДЕНИЕ: Не создано ни одного вложения типа 1!');
        DBMS_OUTPUT.PUT_LINE('  Возможно, для данного email_type_id нет настроенных вложений в справочниках');
        DBMS_OUTPUT.PUT_LINE('  Проверьте связь в v_com_email_type_attach_type');
    ELSE
        DBMS_OUTPUT.PUT_LINE('  ✓ Создано вложений: ' || v_attach_count);
        
        -- Проверяем наличие db_login и db_pass для вложений типа 1
        DECLARE
            v_missing_creds_count NUMBER;
        BEGIN
            SELECT COUNT(*)
            INTO v_missing_creds_count
            FROM pcsystem.email_attach
            WHERE email_task_id = v_task_id
              AND report_type = 1
              AND (db_login IS NULL OR TRIM(db_login) = '' OR db_pass IS NULL OR TRIM(db_pass) = '');
            
            IF v_missing_creds_count > 0 THEN
                DBMS_OUTPUT.PUT_LINE('');
                DBMS_OUTPUT.PUT_LINE('  ⚠ ВНИМАНИЕ: Найдено ' || v_missing_creds_count || ' вложений без db_login или db_pass!');
                DBMS_OUTPUT.PUT_LINE('  Go-сервис будет использовать значения из конфигурации (Oracle.User/Oracle.Password)');
                DBMS_OUTPUT.PUT_LINE('  Если эти значения не указаны в конфигурации, возникнет ошибка при обработке.');
                DBMS_OUTPUT.PUT_LINE('  Рекомендуется заполнить db_login и db_pass в таблице email_attach:');
                DBMS_OUTPUT.PUT_LINE('    UPDATE pcsystem.email_attach');
                DBMS_OUTPUT.PUT_LINE('    SET db_login = ''ваш_логин'', db_pass = ''ваш_пароль''');
                DBMS_OUTPUT.PUT_LINE('    WHERE email_task_id = ' || v_task_id || ' AND report_type = 1;');
            END IF;
        END;
    END IF;
    
    -- ========== ШАГ 3: Создание параметров для вложений ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 3: Создание параметров для вложений...');
    
    IF v_attach_count > 0 THEN
        pcsystem.pkg_email.create_email_attach_param(
            p_email_task_id => v_task_id,
            p_parametr_value => v_parametr_value,
            p_err_code => v_err_code,
            p_err_desc => v_err_desc
        );
        
        IF v_err_code != 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ⚠ Предупреждение: ' || v_err_desc);
            DBMS_OUTPUT.PUT_LINE('  Код ошибки: ' || v_err_code);
            DBMS_OUTPUT.PUT_LINE('  (Возможно, параметры уже существуют или не требуются)');
        ELSE
            DBMS_OUTPUT.PUT_LINE('  ✓ Параметры созданы успешно');
        END IF;
        
        -- Проверяем созданные параметры
        SELECT COUNT(*) INTO v_param_count
        FROM pcsystem.email_attach_param eap
        JOIN pcsystem.email_attach ea ON eap.email_attach_id = ea.email_attach_id
        WHERE ea.email_task_id = v_task_id
          AND ea.report_type = 1;
        
        DBMS_OUTPUT.PUT_LINE('  ✓ Всего параметров создано: ' || v_param_count);
    ELSE
        DBMS_OUTPUT.PUT_LINE('  ⚠ Пропущено (нет вложений для создания параметров)');
        v_param_count := 0;
    END IF;
    
    -- ========== ШАГ 4: Вывод информации о созданных данных ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 4: Информация о созданных данных...');
    DBMS_OUTPUT.PUT_LINE('  Задание ID: ' || v_task_id);
    DBMS_OUTPUT.PUT_LINE('  Email получателя: ' || v_email_address);
    DBMS_OUTPUT.PUT_LINE('  Заголовок: ' || v_email_title);
    DBMS_OUTPUT.PUT_LINE('  Вложений: ' || v_attach_count);
    DBMS_OUTPUT.PUT_LINE('  Параметров: ' || NVL(v_param_count, 0));
    DBMS_OUTPUT.PUT_LINE('');
    
    IF v_attach_count > 0 THEN
        DBMS_OUTPUT.PUT_LINE('  Детали вложений:');
        FOR rec IN (
            SELECT 
                ea.email_attach_id,
                ea.email_attach_catalog,
                ea.email_attach_file,
                ea.email_attach_name,
                ea.db_login,
                ea.db_pass,
                ea.report_type
            FROM pcsystem.email_attach ea
            WHERE ea.email_task_id = v_task_id
              AND ea.report_type = 1
            ORDER BY ea.email_attach_id
        ) LOOP
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('    Вложение ID: ' || rec.email_attach_id);
            DBMS_OUTPUT.PUT_LINE('      Каталог (ApplicationName): ' || rec.email_attach_catalog);
            DBMS_OUTPUT.PUT_LINE('      Файл (ReportName): ' || rec.email_attach_file);
            DBMS_OUTPUT.PUT_LINE('      Имя для получателя: ' || rec.email_attach_name);
            DBMS_OUTPUT.PUT_LINE('      DB Login: ' || NVL(rec.db_login, '(НЕ УКАЗАН - будет использовано из конфигурации)'));
            DBMS_OUTPUT.PUT_LINE('      DB Pass: ' || CASE WHEN rec.db_pass IS NULL OR TRIM(rec.db_pass) = '' THEN '(НЕ УКАЗАН - будет использовано из конфигурации)' ELSE '***' END);
            
            -- Параметры вложения
            DECLARE
                v_has_params BOOLEAN := FALSE;
            BEGIN
                FOR param_rec IN (
                    SELECT 
                        email_attach_param_name,
                        email_attach_param_value
                    FROM pcsystem.email_attach_param
                    WHERE email_attach_id = rec.email_attach_id
                    ORDER BY email_attach_param_name
                ) LOOP
                    IF NOT v_has_params THEN
                        DBMS_OUTPUT.PUT_LINE('      Параметры:');
                        v_has_params := TRUE;
                    END IF;
                    DBMS_OUTPUT.PUT_LINE('        ' || param_rec.email_attach_param_name || 
                                        ' = ' || param_rec.email_attach_param_value);
                END LOOP;
                
                IF NOT v_has_params THEN
                    DBMS_OUTPUT.PUT_LINE('      Параметры: нет');
                END IF;
            END;
        END LOOP;
    END IF;
    
    -- ========== ШАГ 5: Обновление date_delay_send НЕПОСРЕДСТВЕННО ПЕРЕД отправкой ==========
    -- ИСПРАВЛЕНИЕ: Обновляем date_delay_send непосредственно перед отправкой в очередь,
    -- чтобы гарантировать, что DELAY будет положительным или нулевым
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 5: Обновление date_delay_send непосредственно перед отправкой...');
    
    -- Устанавливаем date_delay_send в текущее время или в будущее (например, +1 секунда)
    -- для гарантии положительного DELAY
    UPDATE pcsystem.email_task
    SET date_delay_send = SYSDATE + 1/86400  -- +1 секунда для гарантии положительного DELAY
    WHERE email_task_id = v_task_id;
    
    DBMS_OUTPUT.PUT_LINE('  ✓ date_delay_send обновлено на SYSDATE + 1 секунда');
    DBMS_OUTPUT.PUT_LINE('  Текущее время: ' || TO_CHAR(SYSDATE, 'YYYY-MM-DD HH24:MI:SS'));
    DBMS_OUTPUT.PUT_LINE('  date_delay_send: ' || TO_CHAR(SYSDATE + 1/86400, 'YYYY-MM-DD HH24:MI:SS'));
    
    -- ========== ШАГ 6: Отправка задания в очередь AQ ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 6: Отправка задания в очередь AQ...');
    
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
        DBMS_OUTPUT.PUT_LINE('    pcsystem.pkg_email.send_email_request(');
        DBMS_OUTPUT.PUT_LINE('      p_email_task_id => ' || v_task_id || ',');
        DBMS_OUTPUT.PUT_LINE('      p_err_code => :err_code,');
        DBMS_OUTPUT.PUT_LINE('      p_err_desc => :err_desc');
        DBMS_OUTPUT.PUT_LINE('    );');
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
    DBMS_OUTPUT.PUT_LINE('  Вложений создано: ' || v_attach_count);
    DBMS_OUTPUT.PUT_LINE('  Параметров создано: ' || NVL(v_param_count, 0));
    DBMS_OUTPUT.PUT_LINE('  Статус: Отправлено в очередь AQ');
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('Следующие шаги:');
    DBMS_OUTPUT.PUT_LINE('  1. Убедитесь, что Go-сервис email-service запущен');
    DBMS_OUTPUT.PUT_LINE('  2. Сервис автоматически прочитает сообщение из очереди AQ');
    DBMS_OUTPUT.PUT_LINE('  3. Сервис сгенерирует отчет через SOAP Crystal Reports');
    DBMS_OUTPUT.PUT_LINE('  4. Сервис отправит письмо через SMTP');
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
            DBMS_OUTPUT.PUT_LINE('  2. Создайте вложения: pcsystem.pkg_email.create_email_attach(...)');
            DBMS_OUTPUT.PUT_LINE('  3. Обновите date_delay_send: UPDATE pcsystem.email_task SET date_delay_send = SYSDATE + 1/86400 WHERE email_task_id = ' || v_task_id || ';');
            DBMS_OUTPUT.PUT_LINE('  4. Отправьте в очередь: pcsystem.pkg_email.send_email_request(...)');
        END IF;
        
        RAISE;
END;
/

COMMIT;

