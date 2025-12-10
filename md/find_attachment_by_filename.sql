-- ============================================================================
-- ЗАПРОС: Поиск вложения по имени файла
-- ============================================================================
-- Этот запрос находит вложение типа 3 по имени файла
-- и показывает информацию, необходимую для повторного использования
--
-- ИСПОЛЬЗОВАНИЕ:
-- 1. Замените имя файла в условии WHERE на нужное
-- 2. Запустите запрос
-- ============================================================================

-- ============================================================================
-- ВАРИАНТ 1: Поиск по точному имени файла (email_attach_name или в report_file)
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
    -- Извлекаем имя файла из пути для сравнения
    CASE 
        WHEN INSTR(ea.report_file, '/', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '/', -1) + 1)
        WHEN INSTR(ea.report_file, '\', -1) > 0 THEN 
            SUBSTR(ea.report_file, INSTR(ea.report_file, '\', -1) + 1)
        ELSE ea.report_file
    END AS file_name_from_path,
    -- Проверяем, есть ли edoc_id в parametr_value (если parametr_id = 5)
    CASE 
        WHEN et.parametr_id = 5 THEN et.parametr_value
        ELSE NULL
    END AS possible_edoc_id
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3  -- Тип 3: Готовый файл
  AND (
      -- Поиск по имени вложения
      UPPER(ea.email_attach_name) LIKE '%ВЫПИСНОЙ_ЭПИКРИЗ_21584_25_ОТ_2025_11_10_92588079.PDF%'
      -- Или по имени файла в пути
      OR UPPER(ea.report_file) LIKE '%ВЫПИСНОЙ_ЭПИКРИЗ_21584_25_ОТ_2025_11_10_92588079.PDF%'
      -- Или по части имени (если точное имя не найдено)
      OR UPPER(ea.email_attach_name) LIKE '%92588079%'
      OR UPPER(ea.report_file) LIKE '%92588079%'
  )
ORDER BY et.date_response DESC NULLS LAST, et.date_request DESC;

-- ============================================================================
-- ВАРИАНТ 2: Поиск по части имени (более гибкий)
-- ============================================================================
/*
SELECT 
    et.email_task_id,
    et.email_address,
    et.date_response,
    ea.email_attach_id,
    ea.email_attach_name,
    ea.report_file,
    -- Информация для повторного использования
    CASE 
        WHEN et.parametr_id = 5 THEN 
            'edoc_id = ' || et.parametr_value || ' (можно использовать в create_email_attach_esign)'
        ELSE 
            'report_file = ''' || ea.report_file || ''' (можно использовать напрямую)'
    END AS reuse_info
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
  AND (
      UPPER(ea.email_attach_name) LIKE '%92588079%'
      OR UPPER(ea.report_file) LIKE '%92588079%'
      OR UPPER(ea.email_attach_name) LIKE '%ВЫПИСНОЙ_ЭПИКРИЗ%'
      OR UPPER(ea.report_file) LIKE '%ВЫПИСНОЙ_ЭПИКРИЗ%'
  )
ORDER BY et.date_response DESC NULLS LAST;
*/

-- ============================================================================
-- ВАРИАНТ 3: Поиск с проверкой в таблице edoc (если файл был создан через edoc_id)
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
      UPPER(ea.email_attach_name) LIKE '%ВЫПИСНОЙ_ЭПИКРИЗ_21584_25_ОТ_2025_11_10_92588079.PDF%'
      OR UPPER(ea.report_file) LIKE '%ВЫПИСНОЙ_ЭПИКРИЗ_21584_25_ОТ_2025_11_10_92588079.PDF%'
      OR UPPER(e.file_name) LIKE '%ВЫПИСНОЙ_ЭПИКРИЗ_21584_25_ОТ_2025_11_10_92588079.PDF%'
  )
ORDER BY et.date_response DESC NULLS LAST;
*/

-- ============================================================================
-- ВАРИАНТ 4: Простой поиск по номеру в имени файла (92588079)
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
        WHEN et.parametr_id = 5 THEN 
            'Для повторного использования: v_type3_edoc_id := ' || et.parametr_value || ';'
        ELSE 
            'Для повторного использования: v_type3_report_file := ''' || ea.report_file || ''';'
    END AS reuse_instruction
FROM pcsystem.email_task et
INNER JOIN pcsystem.email_attach ea ON et.email_task_id = ea.email_task_id
WHERE ea.report_type = 3
  AND (
      ea.email_attach_name LIKE '%92588079%'
      OR ea.report_file LIKE '%92588079%'
  )
ORDER BY et.date_response DESC NULLS LAST;
*/





