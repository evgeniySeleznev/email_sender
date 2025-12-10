-- ============================================================================
-- ЗАПРОС: Поиск файлов типа 3 (с ЭЦП), которые НЕ являются PDF
-- ============================================================================
-- Этот запрос находит все вложения типа 3, которые имеют расширения,
-- отличные от PDF, и показывает статистику по типам файлов
--
-- ИСПОЛЬЗОВАНИЕ:
-- 1. Запустите запрос для просмотра всех не-PDF файлов типа 3
-- 2. Используйте фильтры WHERE для уточнения результатов
-- ============================================================================

-- ============================================================================
-- ВАРИАНТ 1: Все файлы типа 3 с расширениями, отличными от PDF
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
    -- Извлекаем расширение из пути к файлу
    CASE 
        WHEN INSTR(ea.report_file, '.', -1) > 0 THEN 
            UPPER(SUBSTR(ea.report_file, INSTR(ea.report_file, '.', -1) + 1))
        ELSE 'БЕЗ РАСШИРЕНИЯ'
    END AS file_extension_from_path,
    -- Извлекаем расширение из имени вложения
    CASE 
        WHEN INSTR(ea.email_attach_name, '.', -1) > 0 THEN 
            UPPER(SUBSTR(ea.email_attach_name, INSTR(ea.email_attach_name, '.', -1) + 1))
        ELSE 'БЕЗ РАСШИРЕНИЯ'
    END AS file_extension_from_name,
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
WHERE ea.report_type = 3  -- Тип 3: Готовый файл (с ЭЦП)
  AND (
      -- Исключаем PDF файлы
      NOT (
          UPPER(ea.report_file) LIKE '%.PDF'
          OR UPPER(ea.email_attach_name) LIKE '%.PDF'
          OR UPPER(ea.report_file) LIKE '%.PDF%'
          OR UPPER(ea.email_attach_name) LIKE '%.PDF%'
      )
  )
ORDER BY et.date_response DESC NULLS LAST, et.date_request DESC;

-- ============================================================================
-- ВАРИАНТ 2: Статистика по расширениям файлов типа 3
-- ============================================================================
SELECT 
    -- Определяем расширение (приоритет отдаем пути к файлу)
    CASE 
        WHEN INSTR(ea.report_file, '.', -1) > 0 THEN 
            UPPER(SUBSTR(ea.report_file, INSTR(ea.report_file, '.', -1) + 1))
        WHEN INSTR(ea.email_attach_name, '.', -1) > 0 THEN 
            UPPER(SUBSTR(ea.email_attach_name, INSTR(ea.email_attach_name, '.', -1) + 1))
        ELSE 'БЕЗ РАСШИРЕНИЯ'
    END AS file_extension,
    COUNT(*) AS total_count,
    COUNT(DISTINCT et.email_task_id) AS email_count,
    COUNT(CASE WHEN et.email_status_id = 4 THEN 1 END) AS successfully_sent,
    COUNT(CASE WHEN et.email_status_id = 3 THEN 1 END) AS failed_sent,
    MIN(et.date_response) AS first_sent_date,
    MAX(et.date_response) AS last_sent_date,
    -- Примеры файлов каждого типа
    LISTAGG(
        CASE 
            WHEN INSTR(ea.report_file, '/', -1) > 0 THEN 
                SUBSTR(ea.report_file, INSTR(ea.report_file, '/', -1) + 1)
            WHEN INSTR(ea.report_file, '\', -1) > 0 THEN 
                SUBSTR(ea.report_file, INSTR(ea.report_file, '\', -1) + 1)
            ELSE ea.report_file
        END, 
        ', '
    ) WITHIN GROUP (ORDER BY et.date_response DESC) AS example_files
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
GROUP BY 
    CASE 
        WHEN INSTR(ea.report_file, '.', -1) > 0 THEN 
            UPPER(SUBSTR(ea.report_file, INSTR(ea.report_file, '.', -1) + 1))
        WHEN INSTR(ea.email_attach_name, '.', -1) > 0 THEN 
            UPPER(SUBSTR(ea.email_attach_name, INSTR(ea.email_attach_name, '.', -1) + 1))
        ELSE 'БЕЗ РАСШИРЕНИЯ'
    END
ORDER BY total_count DESC;

-- ============================================================================
-- ВАРИАНТ 3: Только не-PDF файлы с детальной информацией
-- ============================================================================
/*
SELECT 
    et.email_task_id,
    et.email_address,
    et.date_response,
    ea.email_attach_id,
    ea.email_attach_name,
    ea.report_file,
    -- Определяем расширение
    CASE 
        WHEN INSTR(ea.report_file, '.', -1) > 0 THEN 
            UPPER(SUBSTR(ea.report_file, INSTR(ea.report_file, '.', -1) + 1))
        WHEN INSTR(ea.email_attach_name, '.', -1) > 0 THEN 
            UPPER(SUBSTR(ea.email_attach_name, INSTR(ea.email_attach_name, '.', -1) + 1))
        ELSE 'БЕЗ РАСШИРЕНИЯ'
    END AS file_extension,
    -- Определяем тип файла
    CASE 
        WHEN UPPER(ea.report_file) LIKE '%.XML' OR UPPER(ea.email_attach_name) LIKE '%.XML' THEN 'XML'
        WHEN UPPER(ea.report_file) LIKE '%.DOC%' OR UPPER(ea.email_attach_name) LIKE '%.DOC%' THEN 'DOC/DOCX'
        WHEN UPPER(ea.report_file) LIKE '%.XLS%' OR UPPER(ea.email_attach_name) LIKE '%.XLS%' THEN 'XLS/XLSX'
        WHEN UPPER(ea.report_file) LIKE '%.TXT' OR UPPER(ea.email_attach_name) LIKE '%.TXT' THEN 'TXT'
        WHEN UPPER(ea.report_file) LIKE '%.ZIP' OR UPPER(ea.email_attach_name) LIKE '%.ZIP' THEN 'ZIP'
        WHEN UPPER(ea.report_file) LIKE '%.RAR' OR UPPER(ea.email_attach_name) LIKE '%.RAR' THEN 'RAR'
        ELSE 'ДРУГОЙ'
    END AS file_type_category
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
  AND (
      -- Исключаем PDF
      NOT (
          UPPER(ea.report_file) LIKE '%.PDF'
          OR UPPER(ea.email_attach_name) LIKE '%.PDF'
          OR UPPER(ea.report_file) LIKE '%.PDF%'
          OR UPPER(ea.email_attach_name) LIKE '%.PDF%'
      )
  )
ORDER BY et.date_response DESC NULLS LAST;
*/

-- ============================================================================
-- ВАРИАНТ 4: Сравнение PDF vs не-PDF файлов типа 3
-- ============================================================================
/*
SELECT 
    CASE 
        WHEN (
            UPPER(ea.report_file) LIKE '%.PDF'
            OR UPPER(ea.email_attach_name) LIKE '%.PDF'
            OR UPPER(ea.report_file) LIKE '%.PDF%'
            OR UPPER(ea.email_attach_name) LIKE '%.PDF%'
        ) THEN 'PDF'
        ELSE 'НЕ PDF'
    END AS file_category,
    COUNT(*) AS total_count,
    COUNT(DISTINCT et.email_task_id) AS email_count,
    COUNT(CASE WHEN et.email_status_id = 4 THEN 1 END) AS successfully_sent,
    ROUND(COUNT(CASE WHEN et.email_status_id = 4 THEN 1 END) * 100.0 / COUNT(*), 2) AS success_rate_percent
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
GROUP BY 
    CASE 
        WHEN (
            UPPER(ea.report_file) LIKE '%.PDF'
            OR UPPER(ea.email_attach_name) LIKE '%.PDF'
            OR UPPER(ea.report_file) LIKE '%.PDF%'
            OR UPPER(ea.email_attach_name) LIKE '%.PDF%'
        ) THEN 'PDF'
        ELSE 'НЕ PDF'
    END
ORDER BY file_category;
*/

-- ============================================================================
-- ВАРИАНТ 5: Поиск конкретных типов не-PDF файлов
-- ============================================================================
/*
SELECT 
    et.email_task_id,
    et.email_address,
    et.date_response,
    ea.email_attach_id,
    ea.email_attach_name,
    ea.report_file,
    CASE 
        WHEN INSTR(ea.report_file, '.', -1) > 0 THEN 
            UPPER(SUBSTR(ea.report_file, INSTR(ea.report_file, '.', -1) + 1))
        WHEN INSTR(ea.email_attach_name, '.', -1) > 0 THEN 
            UPPER(SUBSTR(ea.email_attach_name, INSTR(ea.email_attach_name, '.', -1) + 1))
        ELSE 'БЕЗ РАСШИРЕНИЯ'
    END AS file_extension
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
  AND (
      -- Ищем конкретные типы файлов (раскомментируйте нужные)
      UPPER(ea.report_file) LIKE '%.XML'
      OR UPPER(ea.email_attach_name) LIKE '%.XML'
      -- OR UPPER(ea.report_file) LIKE '%.DOC%'
      -- OR UPPER(ea.email_attach_name) LIKE '%.DOC%'
      -- OR UPPER(ea.report_file) LIKE '%.XLS%'
      -- OR UPPER(ea.email_attach_name) LIKE '%.XLS%'
  )
ORDER BY et.date_response DESC NULLS LAST;
*/

