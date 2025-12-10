-- ============================================================================
-- ЗАПРОС: Просмотр PDF файлов типа 3, прикрепленных к отправленным письмам
-- ============================================================================
-- Этот запрос показывает все вложения типа 3 (готовые файлы), которые были
-- прикреплены к отправленным письмам.
--
-- ИСПОЛЬЗОВАНИЕ:
-- 1. Запустите запрос для просмотра всех отправленных вложений типа 3
-- 2. Используйте фильтры WHERE для уточнения результатов
-- ============================================================================

-- ============================================================================
-- ВАРИАНТ 1: Основной запрос - все отправленные вложения типа 3
-- ============================================================================
SELECT 
    et.email_task_id,
    et.email_address,
    et.email_title,
    et.email_status_id,
    CASE et.email_status_id
        WHEN 1 THEN 'Новый'
        WHEN 2 THEN 'Отправлено'
        WHEN 3 THEN 'Ошибка'
        WHEN 4 THEN 'Успешно отправлено'
        ELSE 'Неизвестный статус'
    END AS status_name,
    et.date_request,
    et.date_response,
    ea.email_attach_id,
    ea.email_attach_name,
    ea.report_file,
    ea.report_type,
    -- Извлекаем расширение файла
    CASE 
        WHEN INSTR(ea.report_file, '.', -1) > 0 THEN 
            UPPER(SUBSTR(ea.report_file, INSTR(ea.report_file, '.', -1) + 1))
        ELSE 'БЕЗ РАСШИРЕНИЯ'
    END AS file_extension,
    -- Извлекаем имя файла из пути
    CASE 
        WHEN INSTR(ea.report_file, '/', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '/', -1) + 1)
        WHEN INSTR(ea.report_file, '\', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '\', -1) + 1)
        ELSE ea.report_file
    END AS file_name_only
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3  -- Тип 3: Готовый файл
  AND et.email_status_id IN (2, 4)  -- Отправлено или успешно отправлено
ORDER BY et.date_response DESC NULLS LAST, et.date_request DESC
FETCH FIRST 100 ROWS ONLY;  -- Ограничение для безопасности (измените или уберите при необходимости)

-- ============================================================================
-- ВАРИАНТ 2: Только успешно отправленные (статус 4)
-- ============================================================================
/*
SELECT 
    et.email_task_id,
    et.email_address,
    et.email_title,
    et.date_request,
    et.date_response,
    ea.email_attach_id,
    ea.email_attach_name,
    ea.report_file,
    CASE 
        WHEN INSTR(ea.report_file, '/', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '/', -1) + 1)
        WHEN INSTR(ea.report_file, '\', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '\', -1) + 1)
        ELSE ea.report_file
    END AS file_name_only
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
  AND et.email_status_id = 4  -- Только успешно отправленные
ORDER BY et.date_response DESC;
*/

-- ============================================================================
-- ВАРИАНТ 3: Только PDF файлы (по расширению или имени)
-- ============================================================================
/*
SELECT 
    et.email_task_id,
    et.email_address,
    et.email_title,
    et.date_response,
    ea.email_attach_id,
    ea.email_attach_name,
    ea.report_file,
    CASE 
        WHEN INSTR(ea.report_file, '/', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '/', -1) + 1)
        WHEN INSTR(ea.report_file, '\', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '\', -1) + 1)
        ELSE ea.report_file
    END AS file_name_only
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
  AND et.email_status_id IN (2, 4)
  AND (
      UPPER(ea.report_file) LIKE '%.PDF'
      OR UPPER(ea.email_attach_name) LIKE '%.PDF'
      OR UPPER(ea.report_file) LIKE '%.PDF%'
  )
  -- Ограничение по дате: октябрь 2025
  AND (
      (et.date_response >= DATE '2025-10-01' AND et.date_response < DATE '2025-11-01')
      OR (et.date_response IS NULL AND et.date_request >= DATE '2025-10-01' AND et.date_request < DATE '2025-11-01')
  )
ORDER BY et.date_response DESC NULLS LAST, et.date_request DESC;
*/

-- ============================================================================
-- ВАРИАНТ 4: Статистика по файлам типа 3
-- ============================================================================
/*
SELECT 
    COUNT(*) AS total_sent_type3,
    COUNT(DISTINCT et.email_task_id) AS total_emails,
    COUNT(DISTINCT ea.report_file) AS unique_files,
    MIN(et.date_response) AS first_sent_date,
    MAX(et.date_response) AS last_sent_date,
    -- Статистика по расширениям
    COUNT(CASE WHEN UPPER(ea.report_file) LIKE '%.PDF' OR UPPER(ea.email_attach_name) LIKE '%.PDF' THEN 1 END) AS pdf_count,
    COUNT(CASE WHEN UPPER(ea.report_file) LIKE '%.XML' OR UPPER(ea.email_attach_name) LIKE '%.XML' THEN 1 END) AS xml_count,
    COUNT(CASE WHEN UPPER(ea.report_file) LIKE '%.DOC%' OR UPPER(ea.email_attach_name) LIKE '%.DOC%' THEN 1 END) AS doc_count
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
  AND et.email_status_id IN (2, 4);
*/

-- ============================================================================
-- ВАРИАНТ 5: Поиск по конкретному email адресу
-- ============================================================================
/*
SELECT 
    et.email_task_id,
    et.email_address,
    et.email_title,
    et.date_response,
    ea.email_attach_id,
    ea.email_attach_name,
    ea.report_file
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
  AND et.email_status_id IN (2, 4)
  AND UPPER(et.email_address) LIKE '%EVGEN.SELEZNEV@GMAIL.COM%'  -- Замените на нужный email
ORDER BY et.date_response DESC;
*/

-- ============================================================================
-- ВАРИАНТ 6: Поиск по пути к файлу или имени файла
-- ============================================================================
/*
SELECT 
    et.email_task_id,
    et.email_address,
    et.date_response,
    ea.email_attach_id,
    ea.email_attach_name,
    ea.report_file
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
  AND et.email_status_id IN (2, 4)
  AND (
      UPPER(ea.report_file) LIKE '%ИСКОМЫЙ_ПУТЬ%'
      OR UPPER(ea.email_attach_name) LIKE '%ИСКОМОЕ_ИМЯ%'
  )
ORDER BY et.date_response DESC;
*/

-- ============================================================================
-- ВАРИАНТ 7: Группировка по путям к файлам (какие файлы отправлялись чаще всего)
-- ============================================================================
/*
SELECT 
    ea.report_file,
    COUNT(*) AS send_count,
    COUNT(DISTINCT et.email_task_id) AS email_count,
    MIN(et.date_response) AS first_sent,
    MAX(et.date_response) AS last_sent,
    LISTAGG(DISTINCT et.email_address, ', ') WITHIN GROUP (ORDER BY et.email_address) AS recipients
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
  AND et.email_status_id IN (2, 4)
GROUP BY ea.report_file
HAVING COUNT(*) > 1  -- Только файлы, которые отправлялись более одного раза
ORDER BY send_count DESC;
*/

