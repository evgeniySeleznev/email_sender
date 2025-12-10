-- ============================================================================
-- СКРИПТ: Создание письма с вложением типа 1 (Crystal Reports) и отправка в очередь AQ
-- ============================================================================
-- Этот скрипт создает задание на отправку письма с вложением типа 1 (Crystal Reports)
--
-- ИНСТРУКЦИЯ ПО ИСПОЛЬЗОВАНИЮ:
-- 1. Настройте переменные в разделе НАСТРОЙКИ (строки 20-30)
-- 2. Убедитесь, что email_type_id и parametr_value настроены так,
--    чтобы процедура create_email_attach нашла нужные вложения в справочниках
-- 3. Запустите скрипт
-- ============================================================================

DECLARE
    -- ========== НАСТРОЙКИ ==========
    v_email_type_id NUMBER := 10;                    -- ID типа email из справочника
    v_parametr_id NUMBER := 3;                       -- parametr_id (например, 3 = document_id)
    v_parametr_value NUMBER := 12345;                -- Значение параметра
    v_email_address VARCHAR2(500) := 'evgen.seleznev@gmail.com';  -- Email получателя
    v_email_title VARCHAR2(1000) := 'Результат исследования (Иммуногематологическое исследование от 06 окт 2025)';
    v_email_text VARCHAR2(4000) := 'Это письмо сформировано автоматически. Пожалуйста, не отвечайте на него.

Уважаемый Евгений!

Результат лабораторного исследования Иммуногематологическое исследование от 06 окт 2025 прикреплен к данному письму во вложении.

Данное заключение не является диагнозом и должно быть интерпретировано лечащим врачом.

--

Ваш личный кабинет: https://lk.nmicr.ru

--

МНИОИ им. П.А. Герцена - Филиал ФГБУ "НМИЦ Радиологии" Минздрава России

125284, г. Москва, 2-й Боткинский пр., д. 3

--

Выполнено на ПО ГАИС Асклепиус

www.асклепиус.рф';
    v_branch_id NUMBER := 2;                         -- ID территории
    v_smtp_id NUMBER := 1;                           -- ID SMTP сервера
    
    -- ========== ВНУТРЕННИЕ ПЕРЕМЕННЫЕ ==========
    v_task_id NUMBER;
    v_err_code NUMBER;
    v_err_desc VARCHAR2(4000);
    v_attach_count NUMBER;
    v_param_count NUMBER;
BEGIN
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('Создание письма с вложением типа 1 (Crystal Reports)');
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
        pcsystem.seq_email_task.NEXTVAL,
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
    RETURNING email_task_id INTO v_task_id;
    
    DBMS_OUTPUT.PUT_LINE('  ✓ Задание создано: ' || v_task_id);
    
    -- ========== ШАГ 2: Создание вложений типа 1 (Crystal Reports) ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 2: Создание вложений типа 1 (Crystal Reports)...');
    
    BEGIN
        pcsystem.pkg_email.create_email_attach(
            p_email_task_id => v_task_id,
            p_parametr_value => v_parametr_value,
            p_err_code => v_err_code,
            p_err_desc => v_err_desc
        );
        
        IF v_err_code != 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА создания вложений: ' || v_err_desc);
            DBMS_OUTPUT.PUT_LINE('  Код ошибки: ' || v_err_code);
            DBMS_OUTPUT.PUT_LINE('  Задание создано с ID: ' || v_task_id);
            RETURN;
        END IF;
        
        -- Проверяем созданные вложения типа 1
        SELECT COUNT(*) INTO v_attach_count
        FROM pcsystem.email_attach
        WHERE email_task_id = v_task_id
          AND report_type = 1;
        
        IF v_attach_count = 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ⚠ ПРЕДУПРЕЖДЕНИЕ: Не создано ни одного вложения типа 1!');
            DBMS_OUTPUT.PUT_LINE('  Проверьте связь в v_com_email_type_attach_type для email_type_id = ' || v_email_type_id);
            RETURN;
        END IF;
        
        DBMS_OUTPUT.PUT_LINE('  ✓ Создано вложений типа 1: ' || v_attach_count);
        
        -- Проверяем и исправляем пустые имена вложений
        DECLARE
            v_empty_name_count NUMBER;
        BEGIN
            SELECT COUNT(*)
            INTO v_empty_name_count
            FROM pcsystem.email_attach
            WHERE email_task_id = v_task_id
              AND report_type = 1
              AND (email_attach_name IS NULL OR TRIM(email_attach_name) = '');
            
            IF v_empty_name_count > 0 THEN
                DBMS_OUTPUT.PUT_LINE('  ⚠ Найдено вложений с пустым именем: ' || v_empty_name_count);
                DBMS_OUTPUT.PUT_LINE('  Исправляем имена вложений...');
                
                UPDATE pcsystem.email_attach
                SET email_attach_name = REPLACE(REPLACE(email_attach_file, '.rpt', ''), '.RPT', '') || '.pdf'
                WHERE email_task_id = v_task_id
                  AND report_type = 1
                  AND (email_attach_name IS NULL OR TRIM(email_attach_name) = '')
                  AND email_attach_file IS NOT NULL;
                
                UPDATE pcsystem.email_attach
                SET email_attach_name = 'report_' || email_attach_id || '.pdf'
                WHERE email_task_id = v_task_id
                  AND report_type = 1
                  AND (email_attach_name IS NULL OR TRIM(email_attach_name) = '');
                
                DBMS_OUTPUT.PUT_LINE('  ✓ Имена вложений исправлены');
            END IF;
        END;
        
        -- Создаем параметры для вложений типа 1
        pcsystem.pkg_email.create_email_attach_param(
            p_email_task_id => v_task_id,
            p_parametr_value => v_parametr_value,
            p_err_code => v_err_code,
            p_err_desc => v_err_desc
        );
        
        IF v_err_code != 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ⚠ Предупреждение при создании параметров: ' || v_err_desc);
        ELSE
            DBMS_OUTPUT.PUT_LINE('  ✓ Параметры для вложений типа 1 созданы');
        END IF;
        
        -- Проверяем созданные параметры
        SELECT COUNT(*) INTO v_param_count
        FROM pcsystem.email_attach_param eap
        JOIN pcsystem.email_attach ea ON eap.email_attach_id = ea.email_attach_id
        WHERE ea.email_task_id = v_task_id
          AND ea.report_type = 1;
        
        DBMS_OUTPUT.PUT_LINE('  ✓ Всего параметров создано: ' || v_param_count);
        
    EXCEPTION
        WHEN OTHERS THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА при создании вложений типа 1: ' || SQLERRM);
            RETURN;
    END;
    
    -- ========== ШАГ 2.5: Переопределение каталога и файла для тестирования (ОПЦИОНАЛЬНО) ==========
    -- Раскомментируйте этот блок, чтобы переопределить каталог и файл Crystal Reports
    -- Это полезно для тестирования SOAP сервиса с любым файлом без привязки к справочникам
    /*
    DECLARE
        v_test_catalog VARCHAR2(1000) := 'Manager';              -- Каталог (ApplicationName) для тестирования
        v_test_file VARCHAR2(1000) := 'ImmunohistochemistryResume.rpt';  -- Файл (ReportName) для тестирования
        v_updated_count NUMBER;
    BEGIN
        DBMS_OUTPUT.PUT_LINE('');
        DBMS_OUTPUT.PUT_LINE('ШАГ 2.5: Переопределение каталога и файла для тестирования...');
        DBMS_OUTPUT.PUT_LINE('  Каталог: ' || v_test_catalog);
        DBMS_OUTPUT.PUT_LINE('  Файл: ' || v_test_file);
        
        UPDATE pcsystem.email_attach
        SET email_attach_catalog = v_test_catalog,
            email_attach_file = v_test_file
        WHERE email_task_id = v_task_id
          AND report_type = 1;
        
        v_updated_count := SQL%ROWCOUNT;
        
        IF v_updated_count > 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ✓ Обновлено вложений: ' || v_updated_count);
            DBMS_OUTPUT.PUT_LINE('  ВНИМАНИЕ: Используются тестовые значения вместо значений из справочника!');
        ELSE
            DBMS_OUTPUT.PUT_LINE('  ⚠ Не найдено вложений для обновления');
        END IF;
    END;
    */
    
    -- ========== ШАГ 3: Вывод информации о созданных данных ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 3: Информация о созданных данных...');
    DBMS_OUTPUT.PUT_LINE('  Задание ID: ' || v_task_id);
    DBMS_OUTPUT.PUT_LINE('  Email получателя: ' || v_email_address);
    DBMS_OUTPUT.PUT_LINE('  Заголовок: ' || v_email_title);
    DBMS_OUTPUT.PUT_LINE('  Вложений типа 1: ' || v_attach_count);
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
            DBMS_OUTPUT.PUT_LINE('      Имя для получателя: ' || NVL(rec.email_attach_name, '(не указано)'));
            DBMS_OUTPUT.PUT_LINE('      DB Login: ' || NVL(rec.db_login, '(НЕ УКАЗАН - будет использовано из конфигурации)'));
            DBMS_OUTPUT.PUT_LINE('      DB Pass: ' || CASE WHEN rec.db_pass IS NULL OR TRIM(rec.db_pass) = '' THEN '(НЕ УКАЗАН - будет использовано из конфигурации)' ELSE '***' END);
        END LOOP;
    END IF;
    
    -- ========== ШАГ 4: Обновление date_delay_send НЕПОСРЕДСТВЕННО ПЕРЕД отправкой ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 4: Обновление date_delay_send непосредственно перед отправкой...');
    
    UPDATE pcsystem.email_task
    SET date_delay_send = SYSDATE + 1/86400
    WHERE email_task_id = v_task_id;
    
    DBMS_OUTPUT.PUT_LINE('  ✓ date_delay_send обновлено на SYSDATE + 1 секунда');
    DBMS_OUTPUT.PUT_LINE('  Текущее время: ' || TO_CHAR(SYSDATE, 'YYYY-MM-DD HH24:MI:SS'));
    DBMS_OUTPUT.PUT_LINE('  date_delay_send: ' || TO_CHAR(SYSDATE + 1/86400, 'YYYY-MM-DD HH24:MI:SS'));
    
    -- ========== ШАГ 5: Отправка задания в очередь AQ ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 5: Отправка задания в очередь AQ...');
    
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
            DBMS_OUTPUT.PUT_LINE('  Задание создано, но не отправлено в очередь!');
            DBMS_OUTPUT.PUT_LINE('  Задание ID: ' || v_task_id);
            RETURN;
        ELSE
            DBMS_OUTPUT.PUT_LINE('  ✓ Задание успешно отправлено в очередь AQ!');
        END IF;
    EXCEPTION
        WHEN OTHERS THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ИСКЛЮЧЕНИЕ при отправке в очередь: ' || SQLERRM);
            DBMS_OUTPUT.PUT_LINE('  SQLCODE: ' || SQLCODE);
            RETURN;
    END;
    
    -- ========== ИТОГОВАЯ ИНФОРМАЦИЯ ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('✓ ВСЕ ШАГИ ВЫПОЛНЕНЫ УСПЕШНО');
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('Итоговая информация:');
    DBMS_OUTPUT.PUT_LINE('  Задание ID: ' || v_task_id);
    DBMS_OUTPUT.PUT_LINE('  Email получателя: ' || v_email_address);
    DBMS_OUTPUT.PUT_LINE('  Вложений типа 1 создано: ' || v_attach_count);
    DBMS_OUTPUT.PUT_LINE('  Параметров создано: ' || NVL(v_param_count, 0));
    DBMS_OUTPUT.PUT_LINE('  Статус: Отправлено в очередь AQ');
    DBMS_OUTPUT.PUT_LINE('');
    
EXCEPTION
    WHEN OTHERS THEN
        DBMS_OUTPUT.PUT_LINE('');
        DBMS_OUTPUT.PUT_LINE('========================================');
        DBMS_OUTPUT.PUT_LINE('✗ КРИТИЧЕСКАЯ ОШИБКА');
        DBMS_OUTPUT.PUT_LINE('========================================');
        DBMS_OUTPUT.PUT_LINE('SQLCODE: ' || SQLCODE);
        DBMS_OUTPUT.PUT_LINE('SQLERRM: ' || SQLERRM);
        
        IF v_task_id IS NOT NULL THEN
            DBMS_OUTPUT.PUT_LINE('Задание было создано с ID: ' || v_task_id);
        END IF;
        
        RAISE;
END;
/

COMMIT;

