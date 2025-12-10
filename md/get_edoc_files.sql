-- ============================================================================
-- СКРИПТ: Извлечение названий и путей файлов из таблицы edoc
-- ============================================================================
-- Этот скрипт извлекает информацию о файлах из таблицы pcsystem.edoc
-- и показывает их названия и пути.
--
-- ИСПОЛЬЗОВАНИЕ:
-- 1. Настройте условия WHERE для фильтрации нужных записей
-- 2. Запустите скрипт
-- ============================================================================

-- ============================================================================
-- ВАРИАНТ 1: Простой SELECT для просмотра всех записей
-- ============================================================================
SELECT 
    edoc_id,
    file_name,
    file_path,
    -- Полный путь (если нужно объединить с storage.storage_path):
    -- (SELECT storage_path FROM pcsystem.storage WHERE ROWNUM = 1) || file_path || file_name AS full_path
    file_path || file_name AS full_relative_path
FROM pcsystem.edoc
-- Добавьте условия WHERE для фильтрации:
-- WHERE edoc_id = :your_edoc_id
-- WHERE file_name LIKE '%.PDF'
-- WHERE file_path LIKE '%2025%'
ORDER BY edoc_id DESC
FETCH FIRST 100 ROWS ONLY;  -- Ограничение для безопасности (измените или уберите при необходимости)

-- ============================================================================
-- ВАРИАНТ 2: С полным путем из таблицы storage
-- ============================================================================
/*
SELECT 
    e.edoc_id,
    e.file_name,
    e.file_path,
    s.storage_path,
    s.storage_path || e.file_path || e.file_name AS full_path
FROM pcsystem.edoc e
CROSS JOIN (
    SELECT storage_path 
    FROM pcsystem.storage 
    WHERE ROWNUM = 1
) s
-- Добавьте условия WHERE для фильтрации:
-- WHERE e.edoc_id = :your_edoc_id
ORDER BY e.edoc_id DESC
FETCH FIRST 100 ROWS ONLY;
*/

-- ============================================================================
-- ВАРИАНТ 3: С использованием процедуры (как в create_email_attach_esign)
-- ============================================================================
/*
-- Этот вариант показывает, как процедура формирует полный путь:
DECLARE
    v_edoc_id NUMBER := 61;  -- Укажите нужный edoc_id
    v_file_name VARCHAR2(1000);
    v_file_path VARCHAR2(4000);
    v_storage_path VARCHAR2(4000);
    v_full_path VARCHAR2(4000);
BEGIN
    -- Получаем данные из edoc
    SELECT file_name, file_path
    INTO v_file_name, v_file_path
    FROM pcsystem.edoc
    WHERE edoc_id = v_edoc_id;
    
    -- Получаем путь из storage
    SELECT storage_path
    INTO v_storage_path
    FROM pcsystem.storage
    WHERE ROWNUM = 1;
    
    -- Формируем полный путь (как в процедуре create_email_attach_esign)
    v_full_path := v_storage_path || v_file_path || v_file_name;
    
    DBMS_OUTPUT.PUT_LINE('edoc_id: ' || v_edoc_id);
    DBMS_OUTPUT.PUT_LINE('file_name: ' || v_file_name);
    DBMS_OUTPUT.PUT_LINE('file_path: ' || v_file_path);
    DBMS_OUTPUT.PUT_LINE('storage_path: ' || v_storage_path);
    DBMS_OUTPUT.PUT_LINE('full_path: ' || v_full_path);
END;
/
*/

-- ============================================================================
-- ВАРИАНТ 4: Вывод в формате для использования в других скриптах
-- ============================================================================
/*
-- Этот вариант выводит данные в формате, удобном для копирования в другие скрипты
SELECT 
    'edoc_id: ' || edoc_id || ', file_name: ' || file_name || ', file_path: ' || file_path AS info
FROM pcsystem.edoc
WHERE edoc_id IN (61, 62, 64)  -- Укажите нужные edoc_id
ORDER BY edoc_id;
*/

-- ============================================================================
-- ВАРИАНТ 5: Статистика по файлам
-- ============================================================================
/*
SELECT 
    COUNT(*) AS total_files,
    COUNT(DISTINCT file_path) AS unique_paths,
    COUNT(DISTINCT SUBSTR(file_name, INSTR(file_name, '.', -1) + 1)) AS unique_extensions,
    MIN(edoc_id) AS min_edoc_id,
    MAX(edoc_id) AS max_edoc_id
FROM pcsystem.edoc;
*/

-- ============================================================================
-- ВАРИАНТ 6: Поиск файлов по имени или пути
-- ============================================================================
/*
-- Поиск по имени файла
SELECT 
    edoc_id,
    file_name,
    file_path,
    file_path || file_name AS full_relative_path
FROM pcsystem.edoc
WHERE UPPER(file_name) LIKE '%ИСКОМОЕ_ИМЯ%'
ORDER BY edoc_id DESC;

-- Поиск по пути
SELECT 
    edoc_id,
    file_name,
    file_path,
    file_path || file_name AS full_relative_path
FROM pcsystem.edoc
WHERE file_path LIKE '%ИСКОМЫЙ_ПУТЬ%'
ORDER BY edoc_id DESC;
*/

