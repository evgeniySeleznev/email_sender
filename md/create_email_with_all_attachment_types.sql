-- ============================================================================
-- СКРИПТ: Создание письма с тремя типами вложений (1, 2, 3) и отправка в очередь AQ
-- ============================================================================
-- Этот скрипт создает задание на отправку письма с тремя вложениями:
--   - Тип 1: Crystal Reports (через процедуру create_email_attach)
--   - Тип 2: CLOB из БД (создается вручную)
--   - Тип 3: Готовый файл (создается вручную)
--
-- ВАЖНО: Если при отправке возникает ошибка ORA-01427 (подзапрос возвращает
-- более одной строки), это означает, что представление v_email_xml не может
-- корректно обработать несколько вложений разных типов одновременно.
-- В этом случае:
--   1. Проверьте структуру представления v_email_xml
--   2. Попробуйте создать отдельные задания для каждого типа вложения
--   3. Обратитесь к администратору БД для исправления представления
--
-- ИНСТРУКЦИЯ ПО ИСПОЛЬЗОВАНИЮ:
-- 1. Настройте переменные в разделе НАСТРОЙКИ (строки 20-50)
-- 2. Для типа 1: убедитесь, что email_type_id и parametr_value настроены так,
--    чтобы процедура create_email_attach нашла нужные вложения в справочниках
-- 3. Для типа 2: укажите ID существующего вложения для копирования CLOB или
--    используйте один из способов заполнения report_clob (см. комментарии)
-- 4. Для типа 3: укажите путь к существующему файлу или edoc_id для автоматического создания
-- 5. Запустите скрипт
-- ============================================================================

DECLARE
    -- ========== НАСТРОЙКИ ==========
    v_email_type_id NUMBER := 10;                    -- ID типа email из справочника
    v_parametr_id NUMBER := 3;                       -- parametr_id (например, 3 = document_id)
    v_parametr_value NUMBER := 12345;                -- Значение параметра (для типа 1)
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
    
    -- ========== НАСТРОЙКИ ДЛЯ ВЛОЖЕНИЙ ==========
    -- Для типа 1 (Crystal Reports) - используется процедура create_email_attach
    -- Убедитесь, что для данного email_type_id и parametr_value настроены вложения в справочниках
    
    -- Для типа 2 (CLOB) - копирование из существующего вложения
    v_type2_source_attach_id NUMBER := 1093635;      -- ID существующего вложения типа 2 для копирования CLOB
    -- АЛЬТЕРНАТИВА: Если хотите создать CLOB вручную, раскомментируйте блок в ШАГ 3
    
    -- Для типа 3 (Файл) - путь к файлу или edoc_id
    v_type3_report_file VARCHAR2(4000) := NULL;     -- Полный путь к файлу (если NULL, будет использован edoc_id)
    v_type3_edoc_id NUMBER := 61;                   -- edoc_id для автоматического создания через create_email_attach_esign
    v_type3_attach_name VARCHAR2(1000) := NULL;       -- Имя вложения (если NULL, будет извлечено из пути или edoc)
    
    -- ========== ВНУТРЕННИЕ ПЕРЕМЕННЫЕ ==========
    v_task_id NUMBER;
    v_attach_id NUMBER;
    v_err_code NUMBER;
    v_err_desc VARCHAR2(4000);
    v_attach_count NUMBER;
    v_type1_count NUMBER := 0;
    v_type2_count NUMBER := 0;
    v_type3_count NUMBER := 0;
BEGIN
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('Создание письма с тремя типами вложений');
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
    
    BEGIN
        pcsystem.pkg_email.create_email_attach(
            p_email_task_id => v_task_id,
            p_parametr_value => v_parametr_value,
            p_err_code => v_err_code,
            p_err_desc => v_err_desc
        );
        
        IF v_err_code != 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ⚠ Предупреждение при создании вложений типа 1: ' || v_err_desc);
            DBMS_OUTPUT.PUT_LINE('  Код ошибки: ' || v_err_code);
            DBMS_OUTPUT.PUT_LINE('  Возможно, для данного email_type_id нет настроенных вложений в справочниках');
        ELSE
            DBMS_OUTPUT.PUT_LINE('  ✓ Вложения типа 1 созданы успешно');
        END IF;
        
        -- Проверяем созданные вложения типа 1
        SELECT COUNT(*) INTO v_type1_count
        FROM pcsystem.email_attach
        WHERE email_task_id = v_task_id
          AND report_type = 1;
        
        IF v_type1_count > 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ✓ Создано вложений типа 1: ' || v_type1_count);
            
            -- Проверяем и исправляем пустые имена вложений типа 1
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
                    DBMS_OUTPUT.PUT_LINE('  ⚠ Найдено вложений типа 1 с пустым именем: ' || v_empty_name_count);
                    DBMS_OUTPUT.PUT_LINE('  Исправляем имена вложений...');
                    
                    -- Обновляем пустые имена, используя имя файла отчета
                    UPDATE pcsystem.email_attach
                    SET email_attach_name = REPLACE(REPLACE(email_attach_file, '.rpt', ''), '.RPT', '') || '.pdf'
                    WHERE email_task_id = v_task_id
                      AND report_type = 1
                      AND (email_attach_name IS NULL OR TRIM(email_attach_name) = '')
                      AND email_attach_file IS NOT NULL;
                    
                    -- Если имя файла тоже пустое, используем значение по умолчанию
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
        ELSE
            DBMS_OUTPUT.PUT_LINE('  ⚠ Не создано ни одного вложения типа 1');
            DBMS_OUTPUT.PUT_LINE('  Проверьте связь в v_com_email_type_attach_type для email_type_id = ' || v_email_type_id);
        END IF;
    EXCEPTION
        WHEN OTHERS THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА при создании вложений типа 1: ' || SQLERRM);
    END;
    
    -- ========== ШАГ 3: Создание вложения типа 2 (CLOB) ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 3: Создание вложения типа 2 (CLOB)...');
    
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
                DBMS_OUTPUT.PUT_LINE('  Пропускаем создание вложения типа 2');
            ELSE
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
                
                v_type2_count := 1;
                DBMS_OUTPUT.PUT_LINE('  ✓ Вложение типа 2 создано: ' || v_attach_id);
                DBMS_OUTPUT.PUT_LINE('    Источник: email_attach_id = ' || v_type2_source_attach_id);
                DBMS_OUTPUT.PUT_LINE('    Имя: ' || v_attach_name);
                DBMS_OUTPUT.PUT_LINE('    Размер CLOB: ' || LENGTH(v_source_clob) || ' символов');
            END IF;
        EXCEPTION
            WHEN NO_DATA_FOUND THEN
                DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: email_attach_id = ' || v_type2_source_attach_id || ' не найден или CLOB пуст');
                DBMS_OUTPUT.PUT_LINE('  Пропускаем создание вложения типа 2');
                DBMS_OUTPUT.PUT_LINE('  АЛЬТЕРНАТИВА: Вы можете создать CLOB вручную, раскомментировав блок ниже');
                -- ============================================================================
                -- АЛЬТЕРНАТИВНЫЙ СПОСОБ: Создание CLOB вручную
                -- ============================================================================
                -- Раскомментируйте этот блок, если хотите создать CLOB вручную:
                /*
                DECLARE
                    v_base64_string VARCHAR2(32767);  -- Вставьте сюда Base64 строку из файла
                BEGIN
                    -- ВСТАВЬТЕ СЮДА Base64 строку вашего PDF файла
                    v_base64_string := 'JVBERi0xLjQKJeLjz9MK...';  -- Ваша Base64 строка
                    
                    -- Инициализируем CLOB и копируем строку
                    DBMS_LOB.CREATETEMPORARY(v_source_clob, TRUE);
                    DBMS_LOB.WRITEAPPEND(v_source_clob, LENGTH(v_base64_string), v_base64_string);
                    
                    -- Создаем вложение
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
                        1,
                        'attachment_type2_' || v_parametr_value || '.pdf',
                        2,
                        v_source_clob
                    )
                    RETURNING email_attach_id INTO v_attach_id;
                    
                    v_type2_count := 1;
                    DBMS_OUTPUT.PUT_LINE('  ✓ Вложение типа 2 создано вручную: ' || v_attach_id);
                END;
                */
            WHEN OTHERS THEN
                DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА при обработке email_attach_id = ' || v_type2_source_attach_id || ': ' || SQLERRM);
        END;
    EXCEPTION
        WHEN OTHERS THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА при создании вложения типа 2: ' || SQLERRM);
    END;
    
    -- ========== ШАГ 4: Создание вложения типа 3 (Готовый файл) ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 4: Создание вложения типа 3 (Готовый файл)...');
    
    BEGIN
        DECLARE
            v_report_file VARCHAR2(4000);
            v_attach_name VARCHAR2(1000);
        BEGIN
            -- Определяем способ создания вложения типа 3
            IF v_type3_edoc_id IS NOT NULL THEN
                -- Способ 1: Использование процедуры create_email_attach_esign
                DBMS_OUTPUT.PUT_LINE('  Используется процедура create_email_attach_esign');
                DBMS_OUTPUT.PUT_LINE('  edoc_id: ' || v_type3_edoc_id);
                
                pcsystem.pkg_email.create_email_attach_esign(
                    p_email_task_id => v_task_id,
                    p_parametr_value => v_type3_edoc_id,
                    p_err_code => v_err_code,
                    p_err_desc => v_err_desc
                );
                
                IF v_err_code != 0 THEN
                    DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА создания вложения через create_email_attach_esign: ' || v_err_desc);
                    DBMS_OUTPUT.PUT_LINE('  Код ошибки: ' || v_err_code);
                ELSE
                    -- Получаем информацию о созданном вложении
                    SELECT email_attach_id, email_attach_name, report_file
                    INTO v_attach_id, v_attach_name, v_report_file
                    FROM pcsystem.email_attach
                    WHERE email_task_id = v_task_id
                      AND report_type = 3
                    FETCH FIRST 1 ROWS ONLY;
                    
                    v_type3_count := 1;
                    DBMS_OUTPUT.PUT_LINE('  ✓ Вложение типа 3 создано через create_email_attach_esign: ' || v_attach_id);
                    DBMS_OUTPUT.PUT_LINE('    Имя файла: ' || v_attach_name);
                    DBMS_OUTPUT.PUT_LINE('    Полный путь: ' || v_report_file);
                END IF;
            ELSIF v_type3_report_file IS NOT NULL THEN
                -- Способ 2: Создание вручную с указанным путем
                v_report_file := v_type3_report_file;
                
                -- Определяем имя вложения
                IF v_type3_attach_name IS NOT NULL THEN
                    v_attach_name := v_type3_attach_name;
                ELSE
                    -- Извлекаем имя из пути
                    v_attach_name := SUBSTR(v_report_file, INSTR(v_report_file, '/', -1) + 1);
                    IF v_attach_name = v_report_file THEN
                        -- Пробуем обратный слэш для Windows
                        v_attach_name := SUBSTR(v_report_file, INSTR(v_report_file, '\', -1) + 1);
                    END IF;
                    IF v_attach_name = v_report_file THEN
                        v_attach_name := 'attachment_type3_' || v_parametr_value || '.pdf';
                    END IF;
                END IF;
                
                -- Создаем вложение вручную
                INSERT INTO pcsystem.email_attach (
                    email_attach_id,
                    email_task_id,
                    email_attach_type_id,
                    email_attach_name,
                    report_type,
                    report_file
                ) VALUES (
                    pcsystem.seq_email_attach.NEXTVAL,
                    v_task_id,
                    1,  -- email_attach_type_id (можно изменить на нужный)
                    v_attach_name,
                    3,  -- report_type = 3 (готовый файл)
                    v_report_file
                )
                RETURNING email_attach_id INTO v_attach_id;
                
                v_type3_count := 1;
                DBMS_OUTPUT.PUT_LINE('  ✓ Вложение типа 3 создано вручную: ' || v_attach_id);
                DBMS_OUTPUT.PUT_LINE('    Имя файла: ' || v_attach_name);
                DBMS_OUTPUT.PUT_LINE('    Полный путь: ' || v_report_file);
            ELSE
                DBMS_OUTPUT.PUT_LINE('  ⚠ Пропущено: не указан ни v_type3_report_file, ни v_type3_edoc_id');
                DBMS_OUTPUT.PUT_LINE('  Укажите один из параметров для создания вложения типа 3');
            END IF;
        EXCEPTION
            WHEN NO_DATA_FOUND THEN
                DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА: Вложение не найдено после создания через create_email_attach_esign');
            WHEN OTHERS THEN
                DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА при создании вложения типа 3: ' || SQLERRM);
        END;
    EXCEPTION
        WHEN OTHERS THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ОШИБКА при создании вложения типа 3: ' || SQLERRM);
    END;
    
    -- Проверяем общее количество созданных вложений
    SELECT COUNT(*) INTO v_attach_count
    FROM pcsystem.email_attach
    WHERE email_task_id = v_task_id;
    
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('========================================');
    DBMS_OUTPUT.PUT_LINE('Итоговая статистика вложений:');
    DBMS_OUTPUT.PUT_LINE('  Тип 1 (Crystal Reports): ' || v_type1_count);
    DBMS_OUTPUT.PUT_LINE('  Тип 2 (CLOB): ' || v_type2_count);
    DBMS_OUTPUT.PUT_LINE('  Тип 3 (Файл): ' || v_type3_count);
    DBMS_OUTPUT.PUT_LINE('  Всего: ' || v_attach_count);
    DBMS_OUTPUT.PUT_LINE('========================================');
    
    IF v_attach_count = 0 THEN
        DBMS_OUTPUT.PUT_LINE('');
        DBMS_OUTPUT.PUT_LINE('  ⚠ ВНИМАНИЕ: Не создано ни одного вложения!');
        DBMS_OUTPUT.PUT_LINE('  Задание создано с ID: ' || v_task_id);
        DBMS_OUTPUT.PUT_LINE('  Вы можете создать вложения позже и отправить задание вручную.');
        RETURN;
    END IF;
    
    -- ========== ШАГ 5: Вывод информации о созданных данных ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 5: Информация о созданных данных...');
    DBMS_OUTPUT.PUT_LINE('  Задание ID: ' || v_task_id);
    DBMS_OUTPUT.PUT_LINE('  Email получателя: ' || v_email_address);
    DBMS_OUTPUT.PUT_LINE('  Заголовок: ' || v_email_title);
    DBMS_OUTPUT.PUT_LINE('');
    
    IF v_attach_count > 0 THEN
        DBMS_OUTPUT.PUT_LINE('  Детали вложений:');
        FOR rec IN (
            SELECT 
                ea.email_attach_id,
                ea.email_attach_name,
                ea.report_type,
                CASE ea.report_type
                    WHEN 1 THEN 'Crystal Reports'
                    WHEN 2 THEN 'CLOB из БД'
                    WHEN 3 THEN 'Готовый файл'
                    ELSE 'Неизвестный тип'
                END AS report_type_name,
                ea.email_attach_catalog,
                ea.email_attach_file,
                LENGTH(ea.report_clob) AS clob_size,
                ea.report_file
            FROM pcsystem.email_attach ea
            WHERE ea.email_task_id = v_task_id
            ORDER BY ea.report_type, ea.email_attach_id
        ) LOOP
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('    Вложение ID: ' || rec.email_attach_id);
            DBMS_OUTPUT.PUT_LINE('      Тип: ' || rec.report_type || ' (' || rec.report_type_name || ')');
            DBMS_OUTPUT.PUT_LINE('      Имя для получателя: ' || rec.email_attach_name);
            
            IF rec.report_type = 1 THEN
                DBMS_OUTPUT.PUT_LINE('      Каталог: ' || NVL(rec.email_attach_catalog, '(не указан)'));
                DBMS_OUTPUT.PUT_LINE('      Файл: ' || NVL(rec.email_attach_file, '(не указан)'));
            ELSIF rec.report_type = 2 THEN
                DBMS_OUTPUT.PUT_LINE('      Размер CLOB: ' || NVL(TO_CHAR(rec.clob_size), '0') || ' символов');
            ELSIF rec.report_type = 3 THEN
                DBMS_OUTPUT.PUT_LINE('      Путь к файлу: ' || NVL(rec.report_file, '(не указан)'));
            END IF;
        END LOOP;
    END IF;
    
    -- ========== ШАГ 6: Финальная проверка перед отправкой ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 6: Финальная проверка перед отправкой...');
    
    -- Проверяем, что все вложения имеют заполненные имена
    DECLARE
        v_empty_names_count NUMBER;
    BEGIN
        SELECT COUNT(*)
        INTO v_empty_names_count
        FROM pcsystem.email_attach
        WHERE email_task_id = v_task_id
          AND (email_attach_name IS NULL OR TRIM(email_attach_name) = '');
        
        IF v_empty_names_count > 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ⚠ ВНИМАНИЕ: Найдено вложений с пустым именем: ' || v_empty_names_count);
            DBMS_OUTPUT.PUT_LINE('  Исправляем имена вложений...');
            
            -- Для типа 1: используем имя файла отчета
            UPDATE pcsystem.email_attach
            SET email_attach_name = REPLACE(REPLACE(email_attach_file, '.rpt', ''), '.RPT', '') || '.pdf'
            WHERE email_task_id = v_task_id
              AND report_type = 1
              AND (email_attach_name IS NULL OR TRIM(email_attach_name) = '')
              AND email_attach_file IS NOT NULL;
            
            -- Для типа 2: используем значение по умолчанию
            UPDATE pcsystem.email_attach
            SET email_attach_name = 'attachment_type2_' || email_attach_id || '.pdf'
            WHERE email_task_id = v_task_id
              AND report_type = 2
              AND (email_attach_name IS NULL OR TRIM(email_attach_name) = '');
            
            -- Для типа 3: извлекаем из пути или используем значение по умолчанию
            UPDATE pcsystem.email_attach
            SET email_attach_name = CASE
                WHEN report_file IS NOT NULL THEN
                    CASE
                        WHEN INSTR(report_file, '/', -1) > 0 THEN SUBSTR(report_file, INSTR(report_file, '/', -1) + 1)
                        WHEN INSTR(report_file, '\', -1) > 0 THEN SUBSTR(report_file, INSTR(report_file, '\', -1) + 1)
                        ELSE 'attachment_type3_' || email_attach_id || '.pdf'
                    END
                ELSE 'attachment_type3_' || email_attach_id || '.pdf'
            END
            WHERE email_task_id = v_task_id
              AND report_type = 3
              AND (email_attach_name IS NULL OR TRIM(email_attach_name) = '');
            
            -- Если имя все еще пустое, используем общее значение по умолчанию
            UPDATE pcsystem.email_attach
            SET email_attach_name = 'attachment_' || email_attach_id || '.pdf'
            WHERE email_task_id = v_task_id
              AND (email_attach_name IS NULL OR TRIM(email_attach_name) = '');
            
            DBMS_OUTPUT.PUT_LINE('  ✓ Имена вложений исправлены');
        ELSE
            DBMS_OUTPUT.PUT_LINE('  ✓ Все вложения имеют заполненные имена');
        END IF;
    END;
    
    -- ========== ШАГ 7: Обновление date_delay_send НЕПОСРЕДСТВЕННО ПЕРЕД отправкой ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 7: Обновление date_delay_send непосредственно перед отправкой...');
    
    UPDATE pcsystem.email_task
    SET date_delay_send = SYSDATE + 1/86400  -- +1 секунда для гарантии положительного DELAY
    WHERE email_task_id = v_task_id;
    
    DBMS_OUTPUT.PUT_LINE('  ✓ date_delay_send обновлено на SYSDATE + 1 секунда');
    DBMS_OUTPUT.PUT_LINE('  Текущее время: ' || TO_CHAR(SYSDATE, 'YYYY-MM-DD HH24:MI:SS'));
    DBMS_OUTPUT.PUT_LINE('  date_delay_send: ' || TO_CHAR(SYSDATE + 1/86400, 'YYYY-MM-DD HH24:MI:SS'));
    
    -- ========== ШАГ 8: Отправка задания в очередь AQ ==========
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('ШАГ 8: Отправка задания в очередь AQ...');
    
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
                DBMS_OUTPUT.PUT_LINE('  Это происходит, когда представление v_email_xml не может корректно');
                DBMS_OUTPUT.PUT_LINE('  обработать несколько вложений разных типов одновременно.');
                DBMS_OUTPUT.PUT_LINE('');
                DBMS_OUTPUT.PUT_LINE('  ДИАГНОСТИКА:');
                
                -- Проверяем количество вложений каждого типа
                DECLARE
                    v_type1_cnt NUMBER;
                    v_type2_cnt NUMBER;
                    v_type3_cnt NUMBER;
                    v_empty_names NUMBER;
                BEGIN
                    SELECT COUNT(*) INTO v_type1_cnt FROM pcsystem.email_attach WHERE email_task_id = v_task_id AND report_type = 1;
                    SELECT COUNT(*) INTO v_type2_cnt FROM pcsystem.email_attach WHERE email_task_id = v_task_id AND report_type = 2;
                    SELECT COUNT(*) INTO v_type3_cnt FROM pcsystem.email_attach WHERE email_task_id = v_task_id AND report_type = 3;
                    SELECT COUNT(*) INTO v_empty_names FROM pcsystem.email_attach WHERE email_task_id = v_task_id AND (email_attach_name IS NULL OR TRIM(email_attach_name) = '');
                    
                    DBMS_OUTPUT.PUT_LINE('    Вложений типа 1: ' || v_type1_cnt);
                    DBMS_OUTPUT.PUT_LINE('    Вложений типа 2: ' || v_type2_cnt);
                    DBMS_OUTPUT.PUT_LINE('    Вложений типа 3: ' || v_type3_cnt);
                    DBMS_OUTPUT.PUT_LINE('    Вложений с пустым именем: ' || v_empty_names);
                END;
                
                DBMS_OUTPUT.PUT_LINE('');
                DBMS_OUTPUT.PUT_LINE('  ВОЗМОЖНЫЕ РЕШЕНИЯ:');
                DBMS_OUTPUT.PUT_LINE('    1. Проверьте представление v_email_xml - возможно, оно использует');
                DBMS_OUTPUT.PUT_LINE('       подзапросы, которые не поддерживают несколько вложений');
                DBMS_OUTPUT.PUT_LINE('    2. Попробуйте создать задание с одним вложением для проверки');
                DBMS_OUTPUT.PUT_LINE('    3. Обратитесь к администратору БД для исправления представления v_email_xml');
                DBMS_OUTPUT.PUT_LINE('    4. Проверьте, что все вложения имеют заполненное поле email_attach_name');
                DBMS_OUTPUT.PUT_LINE('');
                DBMS_OUTPUT.PUT_LINE('  АЛЬТЕРНАТИВНОЕ РЕШЕНИЕ:');
                DBMS_OUTPUT.PUT_LINE('    Попробуйте создать три отдельных задания, каждое с одним типом вложения,');
                DBMS_OUTPUT.PUT_LINE('    или исправьте представление v_email_xml для поддержки нескольких вложений.');
            ELSE
                DBMS_OUTPUT.PUT_LINE('  Возможные причины:');
                DBMS_OUTPUT.PUT_LINE('    - Проблемы с очередью AQ');
                DBMS_OUTPUT.PUT_LINE('    - Недостаточно прав для записи в очередь');
                DBMS_OUTPUT.PUT_LINE('    - Ошибка формирования XML из v_email_xml');
            END IF;
            
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('  Задание создано, но не отправлено в очередь!');
            DBMS_OUTPUT.PUT_LINE('  Задание ID: ' || v_task_id);
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('  Проверьте данные задания:');
            DBMS_OUTPUT.PUT_LINE('    SELECT * FROM pcsystem.email_task WHERE email_task_id = ' || v_task_id || ';');
            DBMS_OUTPUT.PUT_LINE('    SELECT * FROM pcsystem.email_attach WHERE email_task_id = ' || v_task_id || ';');
            DBMS_OUTPUT.PUT_LINE('');
            DBMS_OUTPUT.PUT_LINE('  Попробуйте отправить вручную (может не помочь, если проблема в v_email_xml):');
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
            RETURN;
        ELSE
            DBMS_OUTPUT.PUT_LINE('  ✓ Задание успешно отправлено в очередь AQ!');
        END IF;
    EXCEPTION
        WHEN OTHERS THEN
            DBMS_OUTPUT.PUT_LINE('  ✗ ИСКЛЮЧЕНИЕ при отправке в очередь: ' || SQLERRM);
            DBMS_OUTPUT.PUT_LINE('  SQLCODE: ' || SQLCODE);
            DBMS_OUTPUT.PUT_LINE('  Задание ID: ' || v_task_id);
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
    DBMS_OUTPUT.PUT_LINE('  Вложений типа 1: ' || v_type1_count);
    DBMS_OUTPUT.PUT_LINE('  Вложений типа 2: ' || v_type2_count);
    DBMS_OUTPUT.PUT_LINE('  Вложений типа 3: ' || v_type3_count);
    DBMS_OUTPUT.PUT_LINE('  Всего вложений: ' || v_attach_count);
    DBMS_OUTPUT.PUT_LINE('  Статус: Отправлено в очередь AQ');
    DBMS_OUTPUT.PUT_LINE('');
    DBMS_OUTPUT.PUT_LINE('Следующие шаги:');
    DBMS_OUTPUT.PUT_LINE('  1. Убедитесь, что Go-сервис email-service запущен');
    DBMS_OUTPUT.PUT_LINE('  2. Сервис автоматически прочитает сообщение из очереди AQ');
    DBMS_OUTPUT.PUT_LINE('  3. Сервис обработает все три типа вложений:');
    DBMS_OUTPUT.PUT_LINE('     - Тип 1: Сгенерирует отчет через SOAP Crystal Reports');
    DBMS_OUTPUT.PUT_LINE('     - Тип 2: Получит CLOB из БД и декодирует Base64');
    DBMS_OUTPUT.PUT_LINE('     - Тип 3: Прочитает файл с диска');
    DBMS_OUTPUT.PUT_LINE('  4. Сервис отправит письмо через SMTP со всеми вложениями');
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
            DBMS_OUTPUT.PUT_LINE('  2. Создайте недостающие вложения вручную');
            DBMS_OUTPUT.PUT_LINE('  3. Обновите date_delay_send: UPDATE pcsystem.email_task SET date_delay_send = SYSDATE + 1/86400 WHERE email_task_id = ' || v_task_id || ';');
            DBMS_OUTPUT.PUT_LINE('  4. Отправьте в очередь: pcsystem.pkg_email.send_email_request(...)');
        END IF;
        
        RAISE;
END;
/

COMMIT;

