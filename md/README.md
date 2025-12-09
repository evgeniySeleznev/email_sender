# Email Service на Go

Сервис для отправки email сообщений через Oracle AQ очередь, аналогичный C# сервису из `B:\mailservice`.

## Особенности

- Подключение к очереди Oracle AQ (`askaq.aq_ask`)
- Вычитывание сообщений из очереди с consumer `SUB_EMAIL_SENDER`
- Graceful shutdown с гарантией завершения запросов и транзакций
- Переподключение к БД с ожиданием завершения активных операций
- Структурированное логирование через zap
- Кодстайл аналогичен проекту `B:\smsSender`

## Структура проекта

```
email-service/
├── cmd/
│   └── email-service/
│       └── main.go
├── internal/
│   ├── config/
│   │   └── config.go
│   ├── database/
│   │   ├── oracle.go
│   │   ├── queue.go
│   │   └── procedures.go
│   ├── email/
│   │   └── service.go
│   ├── logger/
│   │   └── logger.go
│   ├── parser/
│   │   └── xml.go
│   └── service/
│       └── service.go
├── config.ini
├── go.mod
└── README.md
```

## Конфигурация

Создайте файл `config.ini` на основе примера:

```ini
[ORACLE]
Instance = c113

[main]
username = your_username
password = your_password
dsn = your_dsn

[queue]
queue_name = askaq.aq_ask
consumer_name = SUB_EMAIL_SENDER

[SMTP]
Host = smtp.yandex.ru
Port = 25
User = no_reply@asklepius.ru
Password = your_password
DisplayName = ГАИС.Асклепиус
EnableSSL = True
MinSendIntervalMsec = 1000
SMTPMinSendEmailIntervalMsec = 1000
POPHost = pop.yandex.ru
POPPort = 995

[Mode]
Debug = True
SendHiddenCopyToSelf = True
IsBodyHTML = True
MaxErrorCountForAutoRestart = 50

[Schedule]
TimeStart = 08:00
TimeEnd = 21:00

[Log]
LogLevel = 4
MaxArchiveFiles = 10
```

## Установка зависимостей

```bash
go mod download
```

## Запуск

```bash
go run cmd/email-service/main.go
```

## Статусы выполнения

- ✅ Структура проекта
- ✅ Конфигурация
- ✅ Логирование
- ✅ Подключение к Oracle
- ✅ Работа с очередью Oracle AQ
- ✅ Парсер XML сообщений
- ✅ Процедуры БД
- ⏳ SMTP клиент (базовая структура создана)
- ⏳ Обработка вложений (базовая структура создана)
- ⏳ POP3 клиент для статусов доставки (базовая структура создана)
- ⏳ Основной сервис (базовая структура создана)

## Примечания

Проект находится в стадии разработки. Базовые компоненты созданы и готовы к расширению. Основные паттерны из проекта `smsSender` реализованы:

- Безопасная работа с БД через `WithDB()` и `WithDBTx()`
- Счетчик активных операций для предотвращения переподключения во время транзакций
- Graceful shutdown с обработкой уже вычитанных сообщений
- Использование временных пакетов Oracle для работы с OUT-параметрами
- Структурированное логирование через zap

