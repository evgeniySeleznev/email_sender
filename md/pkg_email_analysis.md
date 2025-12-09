# Анализ пакета pkg_email для работы с вложениями

## Обзор

Пакет `pcsystem.pkg_email` предназначен для работы с электронной почтой в системе. Основная функциональность включает создание писем, обработку вложений разных типов, отправку через SMTP и интеграцию с очередями AQ.

## Структура пакета

### 1. Создание письма

#### 1.1. `create_email_request`
**Назначение:** Распарсивание сообщения из очереди AQ для отправки EMAIL

**Параметры:**
- `p_msg` (IN XMLTYPE) - XML сообщение из очереди
- `p_err_code` (OUT NUMBER) - код ошибки
- `p_err_desc` (OUT VARCHAR2) - описание ошибки

**Логика:**
- Проверяет возможность отправки по параметру `SEND_EMAIL`
- Извлекает из XML: событие, процедуру, дату создания, тело сообщения
- Определяет территорию (branch_id) по параметру
- Определяет тип Email по типу события
- В зависимости от `parametr_id` вызывает:
  - `parametr_id = 3` (document_id): проверяет необходимость ЭЦП, если не требуется - вызывает `send_email`
  - `parametr_id = 5` (edoc_id): проверяет наличие уже отправленного документа и наличие отчета, если условия выполнены - вызывает `send_email_esign`
  - Остальные: вызывает `send_email`

#### 1.2. `send_email`
**Назначение:** Процедура для отправки обычного Email

**Параметры:**
- `p_email_type_id` (IN NUMBER) - идентификатор типа Email
- `p_parametr_value` (IN NUMBER) - значение параметра
- `p_branch_id` (IN NUMBER) - идентификатор территории
- `p_smtp_id` (IN NUMBER) - идентификатор SMTP сервера

**Последовательность вызовов:**
1. Определение параметров Email (parametr_id, recipient_id, sender_id)
2. `get_recipient` - определение идентификатора получателя
3. `get_email_address` - определение адреса электронной почты
4. `get_email_title` - формирование заголовка письма
5. `get_email_text` - формирование текста письма
6. `get_date_delay_send` - получение времени задержки отправки
7. `create_email_task` - создание задания на отправку
8. **`create_email_attach`** - получение вложений для Email ⭐
9. **`create_email_attach_param`** - получение параметров для вложений ⭐
10. `send_email_request` - создание задания для службы отправки

#### 1.3. `send_email_esign`
**Назначение:** Процедура для отправки Email с ЭЦП

**Параметры:** Аналогичны `send_email`

**Отличия от `send_email`:**
- Вместо `create_email_attach` и `create_email_attach_param` вызывается **`create_email_attach_esign`** ⭐
- Используется для отправки электронных документов с подписью

---

## 2. Процедуры работы с вложениями ⭐

### 2.1. `create_email_attach`
**Назначение:** Процедура для получения вложений EMAIL

**Параметры:**
- `p_email_task_id` (IN NUMBER) - идентификатор задания на отправку
- `p_parametr_value` (IN NUMBER) - значение параметра

**Логика:**
1. Получает список вложений из представлений:
   - `pcsystem.email_task`
   - `pcsystem.v_com_email_type_attach_type`
   - `pcsystem.v_com_email_attach_type`

2. Для каждого вложения:
   - Вызывает `get_email_attach_name` для получения имени вложения
   - Вставляет запись в таблицу `email_attach` со следующими полями:
     - `email_attach_id` (из последовательности `seq_email_attach`)
     - `email_task_id`
     - `email_attach_type_id`
     - `email_attach_catalog` - каталог приложения
     - `email_attach_file` - имя файла отчета
     - `email_attach_name` - имя вложения (сформированное)
     - `db_login` - логин БД для отчета
     - `db_pass` - пароль БД для отчета
     - `report_type` - тип отчета (1, 2, 3)

**Важно:** Эта процедура создает записи о вложениях, но не генерирует сами файлы. Генерация происходит позже в службе отправки.

### 2.2. `create_email_attach_param`
**Назначение:** Процедура для получения параметров для вложений EMAIL

**Параметры:**
- `p_email_task_id` (IN NUMBER) - идентификатор задания
- `p_parametr_value` (IN NUMBER) - значение параметра

**Логика:**
1. Получает список параметров для вложений из:
   - `pcsystem.email_task`
   - `pcsystem.email_attach`
   - `pcsystem.v_com_email_attach_type`
   - `pcsystem.v_com_email_attach_param`

2. Для каждого параметра:
   - Вызывает `get_email_attach_param_value` для получения значения параметра
   - Вставляет запись в таблицу `email_attach_param`:
     - `email_attach_id`
     - `email_attach_param_name`
     - `email_attach_param_value`

3. **Дополнительно:** Вставляет параметры для плашки ЭЦП (param_id = 33) из:
   - `pcsystem.email_attach`
   - `pcsystem.tdic_report`
   - `pcsystem.rep_params`
   - `pcsystem.tdic_rep_param`

**Использование:** Параметры используются при генерации Crystal Reports через SOAP сервис.

### 2.3. `create_email_attach_esign`
**Назначение:** Процедура для получения вложений EMAIL с ЭЦП

**Параметры:**
- `p_email_task_id` (IN NUMBER) - идентификатор задания
- `p_parametr_value` (IN NUMBER) - edoc_id

**Логика:**
1. Вставляет запись в таблицу `email_attach` с готовым PDF файлом:
   - `email_attach_id` (из последовательности)
   - `email_task_id`
   - `email_attach_name` - имя файла из `edoc.file_name`
   - `report_type` = 3 (готовый файл)
   - `report_file` - полный путь: `storage.storage_path || edoc.file_path || edoc.file_name`

**Отличие:** В отличие от `create_email_attach`, здесь вложение уже готово (PDF с ЭЦП), не требуется генерация через SOAP.

### 2.4. `get_email_attach_name`
**Назначение:** Процедура для получения имени вложения

**Параметры:**
- `p_email_attach_type_id` (IN NUMBER) - идентификатор типа вложения
- `p_parametr_value` (IN NUMBER) - значение параметра
- `p_email_attach_name` (OUT VARCHAR2) - имя вложения

**Логика:**
1. Получает запрос `email_attach_name_query` из `pcsystem.v_com_email_attach_type`
2. Выполняет динамический запрос для формирования имени
3. Очищает имя от недопустимых символов: `TRANSLATE(TRIM(name), ' ./\|:"*?<>', '_')`
4. Заменяет 'pdf' на '.pdf' для расширения файла

**Использование:** Вызывается из `create_email_attach` для каждого вложения.

### 2.5. `get_email_attach_param_value`
**Назначение:** Процедура для получения значения параметра вложения

**Параметры:**
- `p_email_attach_type_id` (IN NUMBER) - идентификатор типа вложения
- `p_email_attach_param_name` (IN VARCHAR2) - имя параметра
- `p_parametr_value` (IN NUMBER) - значение параметра
- `p_email_attach_param_value` (OUT VARCHAR2) - значение параметра

**Логика:**
1. Получает запрос `email_attach_param_query` из `pcsystem.v_com_email_attach_param`
2. Выполняет динамический запрос для получения значения параметра

**Использование:** Вызывается из `create_email_attach_param` для каждого параметра вложения.

---

## 3. Типы вложений (report_type)

### Тип 1: Crystal Reports
**Описание:** Отчеты, генерируемые через SOAP сервис Crystal Reports

**Характеристики:**
- Требуются параметры: `email_attach_catalog`, `email_attach_file`, `db_login`, `db_pass`
- Параметры отчета хранятся в `email_attach_param`
- Генерация происходит через SOAP запросы:
  1. `getReportInfo` - получение информации об отчете и его параметрах
  2. `getReport` - генерация отчета с параметрами в формате PDF (Base64)

**Использование в процедурах:**
- `create_email_attach` - создает запись с типом 1
- `create_email_attach_param` - добавляет параметры для генерации

### Тип 2: CLOB из БД
**Описание:** Готовый PDF, хранящийся в БД как CLOB (Base64)

**Характеристики:**
- Данные хранятся в поле `email_attach.report_clob`
- Уже декодированы из Base64
- Используется для отчетов, сгенерированных заранее

**Использование в процедурах:**
- `send_email_manually` - может создавать вложения типа 2 с `report_clob`
- Функция `get_email_report_clob` - получает CLOB по `email_attach_id`

### Тип 3: Готовый файл
**Описание:** Готовый файл на файловой системе (обычно PDF с ЭЦП)

**Характеристики:**
- Полный путь хранится в `email_attach.report_file`
- Файл уже существует на диске
- Используется для электронных документов с ЭЦП

**Использование в процедурах:**
- `create_email_attach_esign` - создает вложение типа 3 с путем к файлу
- `send_email_manually` - может создавать вложения типа 3 с файлом

---

## 4. Таблицы и последовательности

### Таблицы

#### `email_task`
Основная таблица заданий на отправку Email:
- `email_task_id` - идентификатор задания
- `email_type_id` - тип Email
- `parametr_id` - идентификатор параметра
- `parametr_value` - значение параметра
- `email_address` - адрес получателя
- `email_title` - заголовок письма
- `email_text` - текст письма
- `email_status_id` - статус отправки (1 - новый, 3 - ошибка, 4 - отправлено)
- `date_request` - дата создания задания
- `date_delay_send` - дата задержки отправки
- `date_response` - дата ответа от службы
- `branch_id` - идентификатор территории
- `smtp_id` - идентификатор SMTP сервера

#### `email_attach`
Таблица вложений:
- `email_attach_id` - идентификатор вложения
- `email_task_id` - ссылка на задание
- `email_attach_type_id` - тип вложения (из справочника)
- `email_attach_catalog` - каталог приложения (для Crystal Reports)
- `email_attach_file` - имя файла отчета (для Crystal Reports)
- `email_attach_name` - имя вложения (для получателя)
- `db_login` - логин БД (для Crystal Reports)
- `db_pass` - пароль БД (для Crystal Reports)
- `report_type` - тип отчета (1, 2, 3)
- `report_clob` - CLOB с данными (для типа 2)
- `report_file` - путь к файлу (для типа 3)

#### `email_attach_param`
Таблица параметров вложений:
- `email_attach_id` - ссылка на вложение
- `email_attach_param_name` - имя параметра
- `email_attach_param_value` - значение параметра

### Последовательности
- `seq_email_task` - для генерации `email_task_id`
- `seq_email_attach` - для генерации `email_attach_id`

---

## 5. Представления (Views)

### `v_com_email_type_attach_type`
Связь типов Email с типами вложений

### `v_com_email_attach_type`
Справочник типов вложений:
- `email_attach_type_id`
- `email_attach_catalog`
- `email_attach_file`
- `email_attach_name_query` - запрос для формирования имени
- `db_login`, `db_pass`
- `report_type`

### `v_com_email_attach_param`
Справочник параметров вложений:
- `email_attach_type_id`
- `email_attach_param_name`
- `email_attach_param_query` - запрос для получения значения параметра

---

## 6. Процедуры для ручной отправки

### 6.1. `send_email_manually`
**Назначение:** Процедура отправки письма из приложения вручную

**Параметры:**
- `p_staff_id` (IN NUMBER) - идентификатор сотрудника
- `p_branch_id` (IN NUMBER) - идентификатор территории
- `p_email_address` (IN VARCHAR2) - адрес получателя
- `p_report_params` (IN CLOB) - XML параметры отчета (опционально)
- `p_report_clob` (IN CLOB) - CLOB с отчетом (опционально)
- `p_report_file` (IN VARCHAR2) - путь к файлу (опционально)

**Логика:**
1. Определяет тип отчета:
   - Если `p_report_file` указан: тип 3, получает `edoc_id` из имени файла
   - Если `p_report_params` указан: тип 2, парсит XML для получения `report_id`
2. Формирует заголовок и текст письма
3. Создает задание (`email_task`) с `email_type_id = 98`
4. Создает вложение (`email_attach`):
   - Если `p_report_params`: тип 2, извлекает параметры из XML
   - Если `p_report_file`: тип 3, использует путь к файлу
5. Добавляет параметры отчета в `email_attach_param` (если есть)
6. Отправляет задание через `send_email_request`

---

## 7. Процедуры для рассылки отчетов из Г.О.

### 7.1. `create_rv_emailing_task`
**Назначение:** Формирование задания на автоматическую рассылку Email с отчетами из ГО

**Логика:**
1. Проверяет расписание рассылки из `reportview_emailing`
2. Для каждого активного расписания:
   - Формирует заголовок и текст письма
   - Создает задание с `email_type_id = 11`
   - Создает вложения типа 1 (Crystal Reports)
   - Добавляет параметры отчета, включая периоды (`P_ST_DATE`, `P_FIN_DATE`)
3. Отправляет задание

### 7.2. `get_rv_emailing_period`
**Назначение:** Получение значения периода из справочника периодов

**Параметры:**
- `p_period_id` (IN NUMBER) - идентификатор периода
- `p_side` (IN NUMBER) - сторона (1 - начало, 2 - конец)

**Возвращает:** Дата в формате строки

---

## 8. Процедуры для переотправки

### 8.1. `resend_email`
**Назначение:** Переотправка неотправленных писем

**Логика:**
1. Очищает `report_clob` для писем старше 14 дней
2. Находит письма со статусом 1 (новый) или 3 (ошибка):
   - Созданные не более 14 дней назад
   - С задержкой отправки более 1 часа назад
   - Учитывает расписание отправки (8-21 час для `sending_schedule = 1`)
   - Исключает ошибки с `resend = 0`
3. Сбрасывает `date_delay_send` и вызывает `send_email_request`

---

## 9. Процедуры для службы отправки

### 9.1. `send_email_request`
**Назначение:** Создание задания для службы отправки Email

**Параметры:**
- `p_email_task_id` (IN NUMBER) - идентификатор задания

**Логика:**
1. Получает XML представление письма из `v_email_xml`
2. Создает событие AQ с кодом 401 ("Отправка EMAIL")
3. Передает XML в очередь через `askaq.pkg_msg.fire_aq_event`

### 9.2. `save_email_response`
**Назначение:** Сохранение ответа от службы отправки

**Параметры:**
- `p_email_task_id` (IN NUMBER) - идентификатор задания
- `p_status_id` (IN NUMBER) - статус (3 - ошибка, 4 - успешно)
- `p_date_response` (IN DATE) - дата ответа
- `p_error_text` (IN VARCHAR2) - текст ошибки

**Дополнительная логика:**
- При статусе 3: обновляет `verified = 0` для контактов пациентов с ошибками валидации
- При статусе 4: обновляет `verified = 1` для успешно отправленных писем пациентам

---

## 10. Вспомогательные функции

### 10.1. `get_soap_address`
**Назначение:** Получение SOAP-адреса сервера CRYS

**Возвращает:** Значение параметра `SOAP_ADDRESS` из `configuration`

### 10.2. `get_test_email`
**Назначение:** Получение тестового адреса электронной почты

**Возвращает:** Значение параметра `TEST_EMAIL_ADDRESS` из `configuration`

---

## 11. Рекомендации для тестирования

### Тестирование типа 1 (Crystal Reports)

1. **Подготовка данных:**
   ```sql
   -- Создать запись в email_task
   -- Создать запись в email_attach с report_type = 1
   -- Указать: catalog, file, db_login, db_pass
   -- Создать параметры в email_attach_param
   ```

2. **Проверка:**
   - Проверить наличие всех параметров отчета
   - Проверить доступность SOAP сервиса
   - Проверить корректность параметров БД
   - Проверить формирование имени вложения

### Тестирование типа 2 (CLOB)

1. **Подготовка данных:**
   ```sql
   -- Создать запись в email_task
   -- Создать запись в email_attach с report_type = 2
   -- Заполнить report_clob с Base64 данными PDF
   ```

2. **Проверка:**
   - Проверить наличие данных в `report_clob`
   - Проверить корректность декодирования Base64
   - Проверить формирование имени вложения

### Тестирование типа 3 (Готовый файл)

1. **Подготовка данных:**
   ```sql
   -- Создать запись в email_task
   -- Создать запись в email_attach с report_type = 3
   -- Указать report_file с полным путем к файлу
   ```

2. **Проверка:**
   - Проверить существование файла на диске
   - Проверить права доступа к файлу
   - Проверить формирование имени вложения из пути

### Тестирование процедур создания вложений

1. **`create_email_attach`:**
   - Вызвать с валидным `email_task_id`
   - Проверить создание записей в `email_attach`
   - Проверить вызов `get_email_attach_name` для каждого вложения

2. **`create_email_attach_param`:**
   - Вызвать после `create_email_attach`
   - Проверить создание параметров в `email_attach_param`
   - Проверить добавление параметров для плашки ЭЦП (если требуется)

3. **`create_email_attach_esign`:**
   - Вызвать с `edoc_id` существующего электронного документа
   - Проверить создание записи с `report_type = 3`
   - Проверить формирование пути к файлу

---

## 12. Схема потока данных для вложений

```
create_email_request (из AQ)
    ↓
send_email / send_email_esign
    ↓
create_email_task
    ↓
create_email_attach
    ├─→ get_email_attach_name (для каждого вложения)
    └─→ INSERT INTO email_attach
    ↓
create_email_attach_param (только для send_email)
    ├─→ get_email_attach_param_value (для каждого параметра)
    └─→ INSERT INTO email_attach_param
    ↓
send_email_request (создание события AQ 401)
    ↓
Служба отправки:
    ├─→ Тип 1: SOAP запрос к Crystal Reports
    ├─→ Тип 2: Декодирование CLOB из БД
    └─→ Тип 3: Чтение файла с диска
```

---

## 13. Важные замечания

1. **Порядок вызовов:** `create_email_attach` всегда вызывается перед `create_email_attach_param`, так как параметры ссылаются на созданные вложения.

2. **Типы вложений:** Тип определяется полем `report_type` в таблице `email_attach`, а не `email_attach_type_id` (который является справочным).

3. **Параметры ЭЦП:** При создании параметров автоматически добавляются параметры для плашки ЭЦП (param_id = 33), если отчет поддерживает их.

4. **Очистка данных:** Процедура `resend_email` очищает `report_clob` для писем старше 14 дней для экономии места.

5. **Валидация контактов:** При сохранении ответа от службы автоматически обновляется статус валидации контактов пациентов.

6. **Динамические запросы:** Многие процедуры используют `EXECUTE IMMEDIATE` для выполнения динамических запросов из справочников, что позволяет гибко настраивать логику без изменения кода.

---

## 14. Связь с Go-кодом

В Go-коде (`email/attachments.go`) реализована обработка вложений:

- **Тип 1:** `processCrystalReport` - вызывает SOAP сервис Crystal Reports
- **Тип 2:** `processCLOB` - получает CLOB из БД через `GetEmailReportClob`
- **Тип 3:** `processFile` - читает файл с диска по пути `report_file`

Процедуры Oracle создают записи в БД, а Go-код обрабатывает эти записи и генерирует/получает файлы для отправки.

