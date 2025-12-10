-- ============================================================================
-- ПОИСК СТРОК С НЕСКОЛЬКИМИ ПОЛУЧАТЕЛЯМИ В ТАБЛИЦЕ email_task
-- ============================================================================
-- Эти запросы помогут найти записи, где в поле email_address указано несколько адресов
-- ============================================================================

-- 1. Поиск строк с несколькими получателями (разделитель: запятая)
-- ============================================================================
SELECT 
    email_task_id,
    email_address,
    LENGTH(email_address) AS address_length,
    (LENGTH(email_address) - LENGTH(REPLACE(email_address, ',', ''))) AS comma_count,
    (LENGTH(email_address) - LENGTH(REPLACE(email_address, ',', '')) + 1) AS recipient_count,
    email_title,
    created_date
FROM pcsystem.email_task
WHERE email_address LIKE '%,%'  -- Содержит запятую
ORDER BY created_date DESC
FETCH FIRST 50 ROWS ONLY;

-- 2. Поиск строк с несколькими получателями (разделитель: точка с запятой)
-- ============================================================================
SELECT 
    email_task_id,
    email_address,
    LENGTH(email_address) AS address_length,
    (LENGTH(email_address) - LENGTH(REPLACE(email_address, ';', ''))) AS semicolon_count,
    (LENGTH(email_address) - LENGTH(REPLACE(email_address, ';', '')) + 1) AS recipient_count,
    email_title,
    created_date
FROM pcsystem.email_task
WHERE email_address LIKE '%;%'  -- Содержит точку с запятой
ORDER BY created_date DESC
FETCH FIRST 50 ROWS ONLY;

-- 3. Поиск строк с несколькими получателями (любой разделитель: запятая ИЛИ точка с запятой)
-- ============================================================================
SELECT 
    email_task_id,
    email_address,
    LENGTH(email_address) AS address_length,
    CASE 
        WHEN email_address LIKE '%,%' THEN 
            (LENGTH(email_address) - LENGTH(REPLACE(email_address, ',', '')) + 1)
        WHEN email_address LIKE '%;%' THEN 
            (LENGTH(email_address) - LENGTH(REPLACE(email_address, ';', '')) + 1)
        ELSE 1
    END AS recipient_count,
    CASE 
        WHEN email_address LIKE '%,%' THEN 'запятая'
        WHEN email_address LIKE '%;%' THEN 'точка с запятой'
        ELSE 'один получатель'
    END AS separator_type,
    email_title,
    created_date
FROM pcsystem.email_task
WHERE email_address LIKE '%,%' OR email_address LIKE '%;%'
ORDER BY created_date DESC
FETCH FIRST 50 ROWS ONLY;

-- 4. Поиск строк с несколькими символами @ (может указывать на несколько адресов)
-- ============================================================================
SELECT 
    email_task_id,
    email_address,
    LENGTH(email_address) AS address_length,
    (LENGTH(email_address) - LENGTH(REPLACE(email_address, '@', ''))) AS at_symbol_count,
    email_title,
    created_date
FROM pcsystem.email_task
WHERE (LENGTH(email_address) - LENGTH(REPLACE(email_address, '@', ''))) > 1  -- Больше одного @
ORDER BY created_date DESC
FETCH FIRST 50 ROWS ONLY;

-- 5. Комплексный поиск: все возможные варианты нескольких получателей
-- ============================================================================
SELECT 
    email_task_id,
    email_address,
    LENGTH(email_address) AS address_length,
    CASE 
        WHEN email_address LIKE '%,%' THEN 
            (LENGTH(email_address) - LENGTH(REPLACE(email_address, ',', '')) + 1)
        WHEN email_address LIKE '%;%' THEN 
            (LENGTH(email_address) - LENGTH(REPLACE(email_address, ';', '')) + 1)
        WHEN (LENGTH(email_address) - LENGTH(REPLACE(email_address, '@', ''))) > 1 THEN
            (LENGTH(email_address) - LENGTH(REPLACE(email_address, '@', '')))
        ELSE 1
    END AS estimated_recipient_count,
    CASE 
        WHEN email_address LIKE '%,%' THEN 'запятая'
        WHEN email_address LIKE '%;%' THEN 'точка с запятой'
        WHEN (LENGTH(email_address) - LENGTH(REPLACE(email_address, '@', ''))) > 1 THEN 'несколько @'
        ELSE 'один получатель'
    END AS separator_type,
    email_title,
    email_status_id,
    created_date
FROM pcsystem.email_task
WHERE email_address LIKE '%,%' 
   OR email_address LIKE '%;%'
   OR (LENGTH(email_address) - LENGTH(REPLACE(email_address, '@', ''))) > 1
ORDER BY created_date DESC
FETCH FIRST 100 ROWS ONLY;

-- 6. Статистика: сколько записей с несколькими получателями
-- ============================================================================
SELECT 
    'С запятой' AS separator_type,
    COUNT(*) AS total_count
FROM pcsystem.email_task
WHERE email_address LIKE '%,%'
UNION ALL
SELECT 
    'С точкой с запятой' AS separator_type,
    COUNT(*) AS total_count
FROM pcsystem.email_task
WHERE email_address LIKE '%;%'
UNION ALL
SELECT 
    'С несколькими @' AS separator_type,
    COUNT(*) AS total_count
FROM pcsystem.email_task
WHERE (LENGTH(email_address) - LENGTH(REPLACE(email_address, '@', ''))) > 1
UNION ALL
SELECT 
    'Всего с несколькими получателями' AS separator_type,
    COUNT(*) AS total_count
FROM pcsystem.email_task
WHERE email_address LIKE '%,%' 
   OR email_address LIKE '%;%'
   OR (LENGTH(email_address) - LENGTH(REPLACE(email_address, '@', ''))) > 1;

-- 7. Детальный просмотр: разбивка адресов по получателям
-- ============================================================================
-- ВНИМАНИЕ: Этот запрос работает только если разделитель - запятая
-- Для точки с запятой замените ',' на ';' в функции REGEXP_SUBSTR
SELECT 
    email_task_id,
    email_address,
    email_title,
    -- Извлекаем первый адрес
    TRIM(REGEXP_SUBSTR(email_address, '[^,]+', 1, 1)) AS recipient_1,
    -- Извлекаем второй адрес (если есть)
    CASE 
        WHEN email_address LIKE '%,%' THEN TRIM(REGEXP_SUBSTR(email_address, '[^,]+', 1, 2))
        ELSE NULL
    END AS recipient_2,
    -- Извлекаем третий адрес (если есть)
    CASE 
        WHEN (LENGTH(email_address) - LENGTH(REPLACE(email_address, ',', ''))) >= 2 THEN 
            TRIM(REGEXP_SUBSTR(email_address, '[^,]+', 1, 3))
        ELSE NULL
    END AS recipient_3,
    -- Количество получателей
    (LENGTH(email_address) - LENGTH(REPLACE(email_address, ',', '')) + 1) AS recipient_count,
    created_date
FROM pcsystem.email_task
WHERE email_address LIKE '%,%'
ORDER BY created_date DESC
FETCH FIRST 20 ROWS ONLY;

-- 8. Поиск с использованием регулярных выражений (более точный)
-- ============================================================================
-- Ищет строки, где есть несколько email адресов (формат: xxx@xxx.xxx)
SELECT 
    email_task_id,
    email_address,
    REGEXP_COUNT(email_address, '[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}') AS email_count,
    email_title,
    created_date
FROM pcsystem.email_task
WHERE REGEXP_COUNT(email_address, '[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}') > 1
ORDER BY created_date DESC
FETCH FIRST 50 ROWS ONLY;

-- 9. Поиск с разбивкой на отдельные адреса (для запятой как разделителя)
-- ============================================================================
WITH email_list AS (
    SELECT 
        email_task_id,
        email_address,
        email_title,
        created_date,
        -- Разбиваем строку на части по запятой
        REGEXP_SUBSTR(email_address, '[^,]+', 1, LEVEL) AS single_email,
        LEVEL AS email_position
    FROM pcsystem.email_task
    WHERE email_address LIKE '%,%'
    CONNECT BY REGEXP_SUBSTR(email_address, '[^,]+', 1, LEVEL) IS NOT NULL
       AND PRIOR email_task_id = email_task_id
       AND PRIOR SYS_GUID() IS NOT NULL
)
SELECT 
    email_task_id,
    email_address AS full_address,
    email_position,
    TRIM(single_email) AS single_email,
    email_title,
    created_date
FROM email_list
ORDER BY email_task_id DESC, email_position
FETCH FIRST 100 ROWS ONLY;
