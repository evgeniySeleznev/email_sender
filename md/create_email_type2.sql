-- ============================================================================
-- СКРИПТ: Создание письма с вложением типа 2 (CLOB) и отправка в очередь AQ
-- ============================================================================
-- Этот скрипт создает задание на отправку письма с вложением типа 2 (CLOB из БД)
--
-- ИНСТРУКЦИЯ ПО ИСПОЛЬЗОВАНИЮ:
-- 1. Настройте переменные в разделе НАСТРОЙКИ (строки 20-30)
-- 2. Для типа 2: укажите ID существующего вложения для копирования CLOB
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
    
    -- ========== НАСТРОЙКИ ДЛЯ ВЛОЖЕНИЯ ТИПА 2 ==========
    v_type2_source_attach_id NUMBER := 1093635;      -- ID существующего вложения типа 2 для копирования CLOB
    
    -- ========== ВНУТРЕННИЕ ПЕРЕМЕННЫЕ ==========
    v_task_id NUMBER;
    v_attach_id NUMBER;
    v_err_code NUMBER;
    v_err_desc VARCHAR2(4000);
    v_attach_count NUMBER;
BEGIN
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('Создание письма с вложением типа 2 (CLOB)');
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('Параметры:');
    DBMS_OUTPUT.PUT_LINE('  email_type_id: ' || v_email_type_id);
    DBMS_OUTPUT.PUT_LINE('  parametr_id: ' || v_parametr_id);
    DBMS_OUTPUT.PUT_LINE('  parametr_value: ' || v_parametr_value);
    DBMS_OUTPUT.PUT_LINE('  email_address: ' || v_email_address);
    DBMS_OUTPUT.PUT_LINE('  branch_id: ' || v_branch_id);
    DBMS_OUTPUT.PUT_LINE('  smtp_id: ' || v_smtp_id);
    DBMS_OUTPUT.PUT_LINE('  source_attach_id: ' || v_type2_source_attach_id);
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
    
    -- ========== ШАГ 2: Создание вложения типа 2 (CLOB) ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 2: Создание вложения типа 2 (CLOB)...');
    
    BEGIN
        DECLARE
            v_source_clob CLOB;
            v_source_name VARCHAR2(1000);
            v_attach_name VARCHAR2(1000);
        BEGIN
            -- Получаем данные из исходного вложения
            SELECT report_clob, email_attach_name
            INTO v_source_clob, v_source_name
            FROM pcsystem.email_attach
            WHERE email_attach_id = v_type2_source_attach_id
              AND report_clob IS NOT NULL
              AND ROWNUM = 1;
            
            -- Проверяем, что данные не пустые
            IF v_source_clob IS NULL OR LENGTH(v_source_clob) = 0 THEN
                DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: email_attach_id = ' || v_type2_source_attach_id || ' - CLOB пуст');
                DBMS_OUTPUT.PUT_LINE('  Задание создано с ID: ' || v_task_id);
                RETURN;
            END IF;
            
            -- Используем имя из исходного вложения или генерируем по умолчанию
            v_attach_name := v_source_name;
            IF v_attach_name IS NULL OR LENGTH(TRIM(v_attach_name)) = 0 THEN
                v_attach_name := 'attachment_type2_' || v_parametr_value || '.pdf';
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
                1,  -- email_attach_type_id (можно изменить на нужный)
                v_attach_name,
                2,  -- report_type = 2 (CLOB из БД)
                v_source_clob  -- Копируем CLOB из исходного вложения
            )
            RETURNING email_attach_id INTO v_attach_id;
            
            v_attach_count := 1;
            DBMS_OUTPUT.PUT_LINE('  ✓ Вложение типа 2 создано: ' || v_attach_id);
            DBMS_OUTPUT.PUT_LINE('    Источник: email_attach_id = ' || v_type2_source_attach_id);
            DBMS_OUTPUT.PUT_LINE('    Имя: ' || v_attach_name);
            DBMS_OUTPUT.PUT_LINE('    Размер CLOB: ' || LENGTH(v_source_clob) || ' символов');
            
        EXCEPTION
            WHEN NO_DATA_FOUND THEN
                DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: email_attach_id = ' || v_type2_source_attach_id || ' не найден или CLOB пуст');
                DBMS_OUTPUT.PUT_LINE('  Задание создано с ID: ' || v_task_id);
                RETURN;
            WHEN OTHERS THEN
                DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА при обработке email_attach_id = ' || v_type2_source_attach_id || ': ' || SQLERRM);
                RETURN;
        END;
    EXCEPTION
        WHEN OTHERS THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА при создании вложения типа 2: ' || SQLERRM);
            RETURN;
    END;
    
    -- Проверяем созданное вложение
    SELECT COUNT(*) INTO v_attach_count
    FROM pcsystem.email_attach
    WHERE email_task_id = v_task_id
      AND report_type = 2;
    
    IF v_attach_count = 0 THEN
        DBMS_OUTPUT.PUT_LINE('');
        DBMS_OUTPUT.PUT_LINE('  ⚠ ВНИМАНИЕ: Не создано ни одного вложения типа 2!');
        DBMS_OUTPUT.PUT_LINE('  Задание создано с ID: ' || v_task_id);
        RETURN;
    END IF;
    
    -- ========== ШАГ 3: Вывод информации о созданных данных ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 3: Информация о созданных данных...');
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
                LENGTH(ea.report_clob) AS clob_size,
                ea.report_type
            FROM pcsystem.email_attach ea
            WHERE ea.email_task_id = v_task_id
              AND ea.report_type = 2
            ORDER BY ea.email_attach_id
        ) LOOP
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('    Вложение ID: ' || rec.email_attach_id);
            DBMS_OUTPUT.PUT_LINE('      Тип: ' || rec.report_type || ' (CLOB из БД)');
            DBMS_OUTPUT.PUT_LINE('      Имя для получателя: ' || rec.email_attach_name);
            DBMS_OUTPUT.PUT_LINE('      Размер CLOB: ' || NVL(TO_CHAR(rec.clob_size), '0') || ' символов');
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
    DBMS_OUTPUT.PUT_LINE('  Вложений типа 2 создано: ' || v_attach_count);
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






