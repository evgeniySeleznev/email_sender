-- ============================================================================
-- ЗАПРОС: Поиск вложений по пути к файлу
-- ============================================================================
-- Этот запрос находит все вложения, где путь к файлу содержит указанную папку
--
-- ИСПОЛЬЗОВАНИЕ:
-- 1. Замените путь в условии WHERE на нужный
-- 2. Запустите запрос
-- ============================================================================

-- ============================================================================
-- ВАРИАНТ 1: Поиск вложений типа 3 с указанным путем
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
    et.parametr_id,
    et.parametr_value,
    ea.email_attach_id,
    ea.email_attach_name,
    ea.report_file,
    ea.report_type,
    -- Извлекаем имя файла из пути
    CASE 
        WHEN INSTR(ea.report_file, '/', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '/', -1) + 1)
        WHEN INSTR(ea.report_file, '\', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '\', -1) + 1)
        ELSE ea.report_file
    END AS file_name_only,
    -- Проверяем, есть ли edoc_id в parametr_value (если parametr_id = 5)
    CASE 
        WHEN et.parametr_id = 5 THEN et.parametr_value
        ELSE NULL
    END AS possible_edoc_id
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3  -- Тип 3: Готовый файл
  AND (
      -- Поиск по пути (учитываем оба варианта разделителей)
      UPPER(ea.report_file) LIKE '%OBN\EDOC\2025\11\10%'
      OR UPPER(ea.report_file) LIKE '%OBN/EDOC/2025/11/10%'
      OR UPPER(ea.report_file) LIKE '%OBN\\EDOC\\2025\\11\\10%'
  )
ORDER BY et.date_response DESC NULLS LAST, et.date_request DESC;

-- ============================================================================
-- ВАРИАНТ 2: Поиск всех типов вложений с указанным путем (включая типы 1 и 2)
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
    ea.report_type,
    CASE ea.report_type
        WHEN 1 THEN 'Crystal Reports'
        WHEN 2 THEN 'CLOB из БД'
        WHEN 3 THEN 'Готовый файл'
        ELSE 'Неизвестный тип'
    END AS report_type_name
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE (
      UPPER(ea.report_file) LIKE '%OBN\EDOC\2025\11\10%'
      OR UPPER(ea.report_file) LIKE '%OBN/EDOC/2025/11/10%'
      OR UPPER(ea.report_file) LIKE '%OBN\\EDOC\\2025\\11\\10%'
  )
ORDER BY et.date_response DESC NULLS LAST, et.date_request DESC;
*/

-- ============================================================================
-- ВАРИАНТ 3: Поиск с группировкой по именам файлов (сколько раз каждый файл отправлялся)
-- ============================================================================
/*
SELECT 
    -- Извлекаем имя файла из пути
    CASE 
        WHEN INSTR(ea.report_file, '/', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '/', -1) + 1)
        WHEN INSTR(ea.report_file, '\', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '\', -1) + 1)
        ELSE ea.report_file
    END AS file_name_only,
    ea.report_file AS full_path,
    COUNT(*) AS send_count,
    COUNT(DISTINCT et.email_task_id) AS email_count,
    MIN(et.date_response) AS first_sent,
    MAX(et.date_response) AS last_sent,
    LISTAGG(DISTINCT et.email_address, ', ') WITHIN GROUP (ORDER BY et.email_address) AS recipients
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
  AND (
      UPPER(ea.report_file) LIKE '%OBN\EDOC\2025\11\10%'
      OR UPPER(ea.report_file) LIKE '%OBN/EDOC/2025/11/10%'
      OR UPPER(ea.report_file) LIKE '%OBN\\EDOC\\2025\\11\\10%'
  )
GROUP BY 
    CASE 
        WHEN INSTR(ea.report_file, '/', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '/', -1) + 1)
        WHEN INSTR(ea.report_file, '\', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '\', -1) + 1)
        ELSE ea.report_file
    END,
    ea.report_file
ORDER BY send_count DESC, last_sent DESC;
*/

-- ============================================================================
-- ВАРИАНТ 4: Поиск с информацией для повторного использования
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
        WHEN INSTR(ea.report_file, '/', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '/', -1) + 1)
        WHEN INSTR(ea.report_file, '\', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '\', -1) + 1)
        ELSE ea.report_file
    END AS file_name_only,
    -- Инструкция для повторного использования
    CASE 
        WHEN et.parametr_id = 5 THEN 
            'Для повторного использования: v_type3_edoc_id := ' || et.parametr_value || ';'
        ELSE 
            'Для повторного использования: v_type3_report_file := ''' || ea.report_file || ''';'
    END AS reuse_instruction
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
  AND (
      UPPER(ea.report_file) LIKE '%OBN\EDOC\2025\11\10%'
      OR UPPER(ea.report_file) LIKE '%OBN/EDOC/2025/11/10%'
      OR UPPER(ea.report_file) LIKE '%OBN\\EDOC\\2025\\11\\10%'
  )
ORDER BY et.date_response DESC NULLS LAST;
*/

-- ============================================================================
-- ВАРИАНТ 5: Поиск с проверкой в таблице edoc (если файлы были созданы через edoc_id)
-- ============================================================================
/*
SELECT 
    et.email_task_id,
    et.email_address,
    et.date_response,
    et.parametr_id,
    et.parametr_value AS edoc_id,
    ea.email_attach_id,
    ea.email_attach_name,
    ea.report_file,
    e.file_name AS edoc_file_name,
    e.file_path AS edoc_file_path,
    -- Полный путь из edoc
    (SELECT storage_path FROM pcsystem.storage WHERE ROWNUM = 1) || e.file_path || e.file_name AS full_path_from_edoc
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
LEFT JOIN pcsystem.edoc e ON et.parametr_id = 5 AND et.parametr_value = e.edoc_id
WHERE ea.report_type = 3
  AND (
      UPPER(ea.report_file) LIKE '%OBN\EDOC\2025\11\10%'
      OR UPPER(ea.report_file) LIKE '%OBN/EDOC/2025/11/10%'
      OR UPPER(ea.report_file) LIKE '%OBN\\EDOC\\2025\\11\\10%'
      OR (e.file_path IS NOT NULL AND UPPER(e.file_path) LIKE '%OBN\EDOC\2025\11\10%')
      OR (e.file_path IS NOT NULL AND UPPER(e.file_path) LIKE '%OBN/EDOC/2025/11/10%')
  )
ORDER BY et.date_response DESC NULLS LAST;
*/

-- ============================================================================
-- ВАРИАНТ 6: Статистика по папке
-- ============================================================================
/*
SELECT 
    COUNT(*) AS total_attachments,
    COUNT(DISTINCT et.email_task_id) AS total_emails,
    COUNT(DISTINCT ea.report_file) AS unique_files,
    MIN(et.date_response) AS first_sent_date,
    MAX(et.date_response) AS last_sent_date,
    COUNT(CASE WHEN et.email_status_id = 4 THEN 1 END) AS successfully_sent,
    COUNT(CASE WHEN et.email_status_id = 3 THEN 1 END) AS failed_sent
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
  AND (
      UPPER(ea.report_file) LIKE '%OBN\EDOC\2025\11\10%'
      OR UPPER(ea.report_file) LIKE '%OBN/EDOC/2025/11/10%'
      OR UPPER(ea.report_file) LIKE '%OBN\\EDOC\\2025\\11\\10%'
  );
*/

