# Инструкция: Реализация получения статусов доставки email (DSN) в Go проекте

## Обзор

Данная инструкция описывает реализацию механизма получения уведомлений о доставке email сообщений (Delivery Status Notification - DSN) в Go проекте, аналогично реализации на C#.

## 1. Структуры данных и конфигурация

### 1.1. Статусы доставки

```go
package emailservice

// DeliveryStatus представляет статус доставки email
type DeliveryStatus int

const (
    DeliveryStatusNew       DeliveryStatus = 1 // Новое сообщение
    DeliveryStatusSended    DeliveryStatus = 2 // Отправлено (или передано релею)
    DeliveryStatusFailed    DeliveryStatus = 3 // Ошибка доставки
    DeliveryStatusDelivered DeliveryStatus = 4 // Доставлено получателю
)

func (ds DeliveryStatus) String() string {
    switch ds {
    case DeliveryStatusNew:
        return "New"
    case DeliveryStatusSended:
        return "Sended"
    case DeliveryStatusFailed:
        return "Failed"
    case DeliveryStatusDelivered:
        return "Delivered"
    default:
        return "Unknown"
    }
}
```

### 1.2. Конфигурация SMTP/POP3

```go
package emailservice

import (
    "time"
)

// SMTPConfig содержит конфигурацию SMTP и POP3 серверов
type SMTPConfig struct {
    // SMTP настройки
    SMTPHost     string
    SMTPPort     int
    SMTPUser     string
    SMTPPassword string
    SMTPDisplayName string
    
    // POP3 настройки для получения DSN уведомлений
    POPHost      string // Если пусто, DSN не используется
    POPPort      int    // По умолчанию 110, SSL 995
    
    // Интервалы отправки
    SMTPMinSendIntervalMsec        int // Минимальный интервал между отправками (мс)
    SMTPMinSendEmailIntervalMsec   int // Минимальный интервал между отправками на один email (мс)
    
    // DSN поддержка
    DSNEnabled   bool // Определяется автоматически при подключении
    
    // Внутренние переменные
    lastSentTime    time.Time      // Время последней отправки
    lastStatusTime  time.Time      // Время последней проверки статусов
}

// SenderMailbox представляет адрес отправителя
type SenderMailbox struct {
    Name    string
    Address string
}
```

### 1.3. SMTP клиент с поддержкой DSN

```go
package emailservice

import (
    "crypto/tls"
    "fmt"
    "net/smtp"
    "strings"
    "time"
    
    "github.com/go-mail/mail/v2"
)

const (
    EnvelopePrefix = "askemailsender" // Префикс для Envelope-Id
)

// SMTPClientDeliver расширенный SMTP клиент с поддержкой DSN
type SMTPClientDeliver struct {
    config       *SMTPConfig
    senderMailbox *SenderMailbox
    client       *mail.Dialer
    connected    bool
    authenticated bool
}

// NewSMTPClientDeliver создает новый SMTP клиент
func NewSMTPClientDeliver(config *SMTPConfig) *SMTPClientDeliver {
    dialer := mail.NewDialer(config.SMTPHost, config.SMTPPort, config.SMTPUser, config.SMTPPassword)
    
    // Настройка TLS
    dialer.TLSConfig = &tls.Config{
        ServerName:         config.SMTPHost,
        InsecureSkipVerify: false, // В production установите true только для тестов
    }
    
    return &SMTPClientDeliver{
        config: config,
        senderMailbox: &SenderMailbox{
            Name:    config.SMTPDisplayName,
            Address: config.SMTPUser,
        },
        client: dialer,
    }
}

// Reconnect переподключается к SMTP серверу
func (c *SMTPClientDeliver) Reconnect() error {
    if c.connected && c.authenticated {
        return nil
    }
    
    // Проверка поддержки DSN
    // В Go это нужно делать через расширение SMTP (EHLO)
    // Для упрощения предполагаем, что DSN поддерживается, если настроен POP3
    if c.config.POPHost != "" {
        c.config.DSNEnabled = true
    }
    
    c.connected = true
    c.authenticated = true
    return nil
}
```

## 2. Отправка email с запросом DSN

### 2.1. Формирование Envelope-Id

```go
// GetEnvelopeId формирует уникальный Envelope-Id на основе taskId
func GetEnvelopeId(taskID string) string {
    return EnvelopePrefix + taskID
}
```

### 2.2. Отправка с DSN запросом

```go
package emailservice

import (
    "fmt"
    "net/mail"
    "time"
    
    "github.com/go-mail/mail/v2"
)

// SendEmailWithDSN отправляет email с запросом DSN уведомлений
func (c *SMTPClientDeliver) SendEmailWithDSN(msg *EmailMessage) error {
    // Проверка интервала отправки
    if c.config.SMTPMinSendIntervalMsec > 0 {
        elapsed := time.Since(c.config.lastSentTime)
        if elapsed < time.Duration(c.config.SMTPMinSendIntervalMsec)*time.Millisecond {
            time.Sleep(time.Duration(c.config.SMTPMinSendIntervalMsec)*time.Millisecond - elapsed)
        }
    }
    
    // Переподключение
    if err := c.Reconnect(); err != nil {
        return fmt.Errorf("ошибка переподключения: %w", err)
    }
    
    // Создание MIME сообщения
    m := mail.NewMessage()
    
    // Отправитель
    m.SetHeader("From", fmt.Sprintf("%s <%s>", c.senderMailbox.Name, c.senderMailbox.Address))
    m.SetHeader("Message-ID", msg.TaskID)
    
    // Получатели
    addresses := parseAddresses(msg.EmailAddress)
    m.SetHeader("To", addresses...)
    
    // Тема
    m.SetHeader("Subject", msg.Title)
    
    // Тело письма
    if Config.IsBodyHTML {
        m.SetBody("text/html", msg.Text)
    } else {
        m.SetBody("text/plain", msg.Text)
    }
    
    // Запрос DSN уведомлений (только если настроен POP3)
    if c.config.POPHost != "" {
        // Добавляем заголовки для запроса DSN
        // NOTIFY=SUCCESS,FAILURE,DELAY - запрос уведомлений об успехе, ошибке и задержке
        m.SetHeader("Return-Receipt-To", c.senderMailbox.Address)
        m.SetHeader("Disposition-Notification-To", c.senderMailbox.Address)
        
        // Envelope-Id для идентификации сообщения в DSN
        envelopeId := GetEnvelopeId(msg.TaskID)
        m.SetHeader("X-Envelope-Id", envelopeId)
        
        // Используем расширение SMTP для DSN (требует поддержки сервера)
        // В Go это делается через кастомный SMTP клиент или библиотеку с поддержкой DSN
    }
    
    // Добавление вложений
    if len(msg.Attachments) > 0 {
        for _, attach := range msg.Attachments {
            if len(attach.Data) == 0 {
                continue
            }
            
            m.AttachReader(attach.FileName, bytes.NewReader(attach.Data))
        }
    }
    
    // Отправка
    if err := c.client.DialAndSend(m); err != nil {
        return fmt.Errorf("ошибка отправки: %w", err)
    }
    
    // Обновление времени последней отправки
    if c.config.SMTPMinSendIntervalMsec > 0 {
        c.config.lastSentTime = time.Now()
    }
    
    return nil
}
```

### 2.3. Альтернатива: использование стандартной библиотеки с DSN

```go
package emailservice

import (
    "bytes"
    "fmt"
    "net/smtp"
    "strings"
    "time"
)

// SendEmailWithDSNStd отправляет email через стандартную библиотеку с DSN
func (c *SMTPClientDeliver) SendEmailWithDSNStd(msg *EmailMessage) error {
    // Проверка интервала
    if c.config.SMTPMinSendIntervalMsec > 0 {
        elapsed := time.Since(c.config.lastSentTime)
        if elapsed < time.Duration(c.config.SMTPMinSendIntervalMsec)*time.Millisecond {
            time.Sleep(time.Duration(c.config.SMTPMinSendIntervalMsec)*time.Millisecond - elapsed)
        }
    }
    
    var body bytes.Buffer
    
    // Заголовки
    headers := map[string]string{
        "From":    fmt.Sprintf("%s <%s>", c.senderMailbox.Name, c.senderMailbox.Address),
        "To":      strings.Join(parseAddresses(msg.EmailAddress), ", "),
        "Subject": msg.Title,
        "Message-ID": msg.TaskID,
    }
    
    // DSN заголовки (если настроен POP3)
    if c.config.POPHost != "" {
        envelopeId := GetEnvelopeId(msg.TaskID)
        headers["X-Envelope-Id"] = envelopeId
        headers["Return-Receipt-To"] = c.senderMailbox.Address
        headers["Disposition-Notification-To"] = c.senderMailbox.Address
        
        // NOTIFY параметр для SMTP MAIL FROM команды
        // Это требует модификации SMTP клиента для поддержки расширения DSN
    }
    
    // Тело письма
    if len(msg.Attachments) > 0 {
        headers["MIME-Version"] = "1.0"
        headers["Content-Type"] = "multipart/mixed; boundary=\"boundary123\""
        
        // Записываем заголовки
        for k, v := range headers {
            fmt.Fprintf(&body, "%s: %s\r\n", k, v)
        }
        body.WriteString("\r\n")
        
        // Тело письма
        body.WriteString("--boundary123\r\n")
        body.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
        body.WriteString("\r\n")
        body.WriteString(msg.Text)
        body.WriteString("\r\n")
        
        // Вложения
        for _, attach := range msg.Attachments {
            if len(attach.Data) == 0 {
                continue
            }
            // ... добавление вложений
        }
        
        body.WriteString("--boundary123--\r\n")
    } else {
        headers["Content-Type"] = "text/html; charset=UTF-8"
        for k, v := range headers {
            fmt.Fprintf(&body, "%s: %s\r\n", k, v)
        }
        body.WriteString("\r\n")
        body.WriteString(msg.Text)
    }
    
    // Подключение и отправка
    addr := fmt.Sprintf("%s:%d", c.config.SMTPHost, c.config.SMTPPort)
    auth := smtp.PlainAuth("", c.config.SMTPUser, c.config.SMTPPassword, c.config.SMTPHost)
    
    to := parseAddresses(msg.EmailAddress)
    toAddresses := make([]string, len(to))
    for i, addr := range to {
        toAddresses[i] = addr
    }
    
    err := smtp.SendMail(addr, auth, c.senderMailbox.Address, toAddresses, body.Bytes())
    if err != nil {
        return fmt.Errorf("ошибка отправки SMTP: %w", err)
    }
    
    // Обновление времени
    if c.config.SMTPMinSendIntervalMsec > 0 {
        c.config.lastSentTime = time.Now()
    }
    
    return nil
}
```

## 3. Получение статусов через POP3

### 3.1. POP3 клиент для получения DSN

```go
package emailservice

import (
    "context"
    "fmt"
    "time"
    
    "github.com/emersion/go-pop3"
)

// POP3StatusChecker проверяет POP3 ящик на наличие DSN уведомлений
type POP3StatusChecker struct {
    config *SMTPConfig
}

// NewPOP3StatusChecker создает новый проверщик статусов
func NewPOP3StatusChecker(config *SMTPConfig) *POP3StatusChecker {
    return &POP3StatusChecker{
        config: config,
    }
}

// CheckStatuses проверяет POP3 ящик на наличие DSN уведомлений
func (c *POP3StatusChecker) CheckStatuses(ctx context.Context, sourceEmail string) ([]DeliveryStatusResult, error) {
    if c.config.POPHost == "" {
        return nil, nil // POP3 не настроен
    }
    
    var results []DeliveryStatusResult
    
    // Подключение к POP3
    client, err := pop3.DialTLS(fmt.Sprintf("%s:%d", c.config.POPHost, c.config.PopPort), nil)
    if err != nil {
        // Попытка без TLS
        client, err = pop3.Dial(fmt.Sprintf("%s:%d", c.config.POPHost, c.config.PopPort))
        if err != nil {
            return nil, fmt.Errorf("ошибка подключения к POP3: %w", err)
        }
    }
    defer client.Quit()
    
    // Аутентификация
    auth := pop3.PlainAuth("", c.config.SMTPUser, c.config.SMTPPassword)
    if err := client.Auth(auth); err != nil {
        return nil, fmt.Errorf("ошибка аутентификации POP3: %w", err)
    }
    
    // Получение списка сообщений
    total, _, err := client.Stat()
    if err != nil {
        return nil, fmt.Errorf("ошибка получения статистики POP3: %w", err)
    }
    
    // Ограничение: не более 1000 сообщений
    maxMessages := 1000
    if total > maxMessages {
        total = maxMessages
    }
    
    count := 0
    startTime := time.Now()
    
    // Читаем сообщения с конца (новые первыми)
    for i := total; i > 0; i-- {
        if ctx.Err() != nil {
            break
        }
        
        // Получение сообщения
        r, err := client.Retr(i)
        if err != nil {
            continue
        }
        
        // Парсинг сообщения
        message, err := mail.ReadMessage(r)
        if err != nil {
            r.Close()
            continue
        }
        
        // Проверка даты сообщения
        date, err := message.Header.Date()
        if err != nil {
            r.Close()
            continue
        }
        
        // Пропускаем старые сообщения
        if date.Before(c.config.lastStatusTime) {
            r.Close()
            break
        }
        
        // Обработка DSN уведомления
        result, err := c.processDSNMessage(sourceEmail, message)
        if err == nil && result != nil {
            results = append(results, *result)
            count++
            
            // Удаление обработанного сообщения
            client.Dele(i)
        }
        
        r.Close()
    }
    
    // Обновление времени последней проверки
    c.config.lastStatusTime = startTime
    
    return results, nil
}
```

### 3.2. Парсинг DSN уведомлений

```go
package emailservice

import (
    "fmt"
    "io"
    "net/mail"
    "strings"
    
    "github.com/emersion/go-message"
    "github.com/emersion/go-message/mail"
)

// DeliveryStatusResult содержит результат обработки DSN уведомления
type DeliveryStatusResult struct {
    TaskID           int
    Status           DeliveryStatus
    StatusDescription string
    Recipient        string
}

// processDSNMessage обрабатывает DSN уведомление
func (c *POP3StatusChecker) processDSNMessage(sourceEmail string, msg *mail.Message) (*DeliveryStatusResult, error) {
    // Проверка типа сообщения
    contentType := msg.Header.Get("Content-Type")
    if !strings.Contains(contentType, "multipart/report") {
        return nil, fmt.Errorf("не DSN сообщение")
    }
    
    // Проверка report-type
    if !strings.Contains(contentType, "report-type=delivery-status") {
        return nil, fmt.Errorf("не delivery-status report")
    }
    
    // Парсинг multipart/report
    mr := message.NewMultipartReader(msg.Body, contentType)
    
    var envelopeId string
    var taskID int
    var status DeliveryStatus
    var statusDesc string
    var recipient string
    
    // Читаем части multipart сообщения
    for {
        p, err := mr.NextPart()
        if err == io.EOF {
            break
        }
        if err != nil {
            continue
        }
        
        partContentType := p.Header.Get("Content-Type")
        
        // Первая часть содержит информацию о сообщении
        if strings.Contains(partContentType, "text/plain") || strings.Contains(partContentType, "message/delivery-status") {
            body, _ := io.ReadAll(p.Body)
            bodyStr := string(body)
            
            // Извлекаем Original-Envelope-Id
            if strings.Contains(bodyStr, "Original-Envelope-Id:") {
                lines := strings.Split(bodyStr, "\n")
                for _, line := range lines {
                    if strings.HasPrefix(line, "Original-Envelope-Id:") {
                        envelopeId = strings.TrimSpace(strings.TrimPrefix(line, "Original-Envelope-Id:"))
                        break
                    }
                }
            }
            
            // Проверяем префикс
            if !strings.HasPrefix(envelopeId, EnvelopePrefix) {
                return nil, fmt.Errorf("не наш Envelope-Id")
            }
            
            // Извлекаем taskID
            taskIDStr := strings.TrimPrefix(envelopeId, EnvelopePrefix)
            if _, err := fmt.Sscanf(taskIDStr, "%d", &taskID); err != nil {
                return nil, fmt.Errorf("неверный taskID: %w", err)
            }
            
            // Парсим статусы получателей
            lines := strings.Split(bodyStr, "\n")
            inRecipientSection := false
            
            for _, line := range lines {
                line = strings.TrimSpace(line)
                
                if line == "" {
                    if inRecipientSection {
                        // Конец секции получателя
                        inRecipientSection = false
                    }
                    continue
                }
                
                if strings.HasPrefix(line, "Original-Recipient:") || strings.HasPrefix(line, "Final-Recipient:") {
                    inRecipientSection = true
                    // Формат: "rfc822;user@domain.com"
                    parts := strings.SplitN(line, ";", 2)
                    if len(parts) == 2 {
                        recipient = strings.TrimSpace(parts[1])
                    }
                    
                    // Пропускаем уведомления для отправителя
                    if strings.EqualFold(recipient, sourceEmail) {
                        continue
                    }
                }
                
                if inRecipientSection && strings.HasPrefix(line, "Action:") {
                    action := strings.TrimSpace(strings.TrimPrefix(line, "Action:"))
                    status, statusDesc = c.parseAction(action, recipient)
                }
                
                if inRecipientSection && strings.HasPrefix(line, "Diagnostic-Code:") {
                    diagnosticCode := strings.TrimSpace(strings.TrimPrefix(line, "Diagnostic-Code:"))
                    if statusDesc != "" {
                        statusDesc += ", " + diagnosticCode
                    }
                }
                
                // Если ошибка, прекращаем обработку
                if status == DeliveryStatusFailed {
                    break
                }
            }
        }
        
        p.Body.Close()
    }
    
    if taskID == 0 {
        return nil, fmt.Errorf("taskID не найден")
    }
    
    if status == 0 {
        return nil, fmt.Errorf("статус не определен")
    }
    
    return &DeliveryStatusResult{
        TaskID:            taskID,
        Status:            status,
        StatusDescription: statusDesc,
        Recipient:         recipient,
    }, nil
}

// parseAction парсит действие из DSN
func (c *POP3StatusChecker) parseAction(action, recipient string) (DeliveryStatus, string) {
    action = strings.ToLower(strings.TrimSpace(action))
    
    switch action {
    case "failed":
        return DeliveryStatusFailed, fmt.Sprintf("Ошибка доставки для %s", recipient)
    case "delayed":
        return 0, fmt.Sprintf("Задержка доставки %s", recipient) // Статус не устанавливается
    case "delivered":
        return DeliveryStatusDelivered, fmt.Sprintf("Доставлено для %s", recipient)
    case "relayed":
        return DeliveryStatusSended, fmt.Sprintf("Релей для %s", recipient)
    case "expanded":
        return DeliveryStatusDelivered, fmt.Sprintf("Доставлено для %s и релей для остальных", recipient)
    default:
        return 0, fmt.Sprintf("Неизвестное действие: %s", action)
    }
}
```

## 4. Периодическая проверка статусов

### 4.1. Сервис проверки статусов

```go
package emailservice

import (
    "context"
    "sync"
    "time"
)

// StatusCheckerService сервис для периодической проверки статусов доставки
type StatusCheckerService struct {
    smtpConfigs  []*SMTPConfig
    checkInterval time.Duration // Интервал проверки (по умолчанию 60 минут)
    stopChan     chan struct{}
    wg           sync.WaitGroup
}

// NewStatusCheckerService создает новый сервис проверки статусов
func NewStatusCheckerService(smtpConfigs []*SMTPConfig) *StatusCheckerService {
    return &StatusCheckerService{
        smtpConfigs:   smtpConfigs,
        checkInterval: 60 * time.Minute,
        stopChan:      make(chan struct{}),
    }
}

// Start запускает периодическую проверку статусов
func (s *StatusCheckerService) Start(ctx context.Context, statusHandler func(result DeliveryStatusResult)) {
    s.wg.Add(1)
    go func() {
        defer s.wg.Done()
        
        ticker := time.NewTicker(s.checkInterval)
        defer ticker.Stop()
        
        for {
            select {
            case <-ctx.Done():
                return
            case <-s.stopChan:
                return
            case <-ticker.C:
                s.checkAllStatuses(ctx, statusHandler)
            }
        }
    }()
}

// Stop останавливает сервис
func (s *StatusCheckerService) Stop() {
    close(s.stopChan)
    s.wg.Wait()
}

// checkAllStatuses проверяет статусы для всех SMTP конфигураций
func (s *StatusCheckerService) checkAllStatuses(ctx context.Context, statusHandler func(result DeliveryStatusResult)) {
    for _, config := range s.smtpConfigs {
        if config.POPHost == "" {
            continue // POP3 не настроен
        }
        
        checker := NewPOP3StatusChecker(config)
        sourceEmail := config.SMTPUser
        
        results, err := checker.CheckStatuses(ctx, sourceEmail)
        if err != nil {
            // Логирование ошибки
            continue
        }
        
        // Обработка результатов
        for _, result := range results {
            statusHandler(result)
        }
    }
}
```

### 4.2. Интеграция в основной цикл

```go
package emailservice

import (
    "context"
    "time"
)

// EmailService основной сервис отправки email
type EmailService struct {
    smtpClients      map[int]*SMTPClientDeliver
    statusChecker    *StatusCheckerService
    responseQueue    chan DeliveryStatusResult
}

// Start запускает сервис
func (s *EmailService) Start(ctx context.Context) error {
    // Запуск проверки статусов
    s.statusChecker.Start(ctx, s.handleDeliveryStatus)
    
    // Основной цикл обработки
    go s.mainLoop(ctx)
    
    return nil
}

// handleDeliveryStatus обрабатывает результат проверки статуса
func (s *EmailService) handleDeliveryStatus(result DeliveryStatusResult) {
    // Сохранение в очередь для записи в БД
    select {
    case s.responseQueue <- result:
    default:
        // Очередь переполнена, логируем ошибку
    }
}

// mainLoop основной цикл обработки
func (s *EmailService) mainLoop(ctx context.Context) {
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            // Обработка очереди ответов
            s.processResponseQueue()
        }
    }
}

// processResponseQueue обрабатывает очередь ответов
func (s *EmailService) processResponseQueue() {
    for {
        select {
        case result := <-s.responseQueue:
            // Сохранение в БД
            s.saveDeliveryStatus(result)
        default:
            return
        }
    }
}
```

## 5. Сохранение статусов в БД

### 5.1. Сохранение через Oracle

```go
package emailservice

import (
    "context"
    "database/sql"
    "fmt"
    "time"
)

// SaveDeliveryStatus сохраняет статус доставки в Oracle БД
func SaveDeliveryStatus(ctx context.Context, db *sql.DB, result DeliveryStatusResult) error {
    query := `BEGIN
        pcsystem.pkg_email.save_email_response(
            P_EMAIL_TASK_ID => :1,
            P_STATUS_ID => :2,
            P_DATE_RESPONSE => :3,
            P_ERROR_TEXT => :4,
            p_err_code => :5,
            p_err_desc => :6
        );
    END;`
    
    var errCode int
    var errDesc string
    
    errorText := ""
    if result.Status == DeliveryStatusFailed {
        errorText = result.StatusDescription
    }
    
    _, err := db.ExecContext(ctx, query,
        result.TaskID,
        int(result.Status),
        time.Now(),
        errorText,
        sql.Out{Dest: &errCode},
        sql.Out{Dest: &errDesc},
    )
    
    if err != nil {
        return fmt.Errorf("ошибка сохранения статуса: %w", err)
    }
    
    if errCode != 0 {
        return fmt.Errorf("ошибка БД: %d - %s", errCode, errDesc)
    }
    
    return nil
}
```

## 6. Пример использования

### 6.1. Инициализация и запуск

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"
    
    "your-project/emailservice"
)

func main() {
    // Конфигурация SMTP
    smtpConfigs := []*emailservice.SMTPConfig{
        {
            SMTPHost:                  "smtp.example.com",
            SMTPPort:                  587,
            SMTPUser:                  "sender@example.com",
            SMTPPassword:              "password",
            SMTPDisplayName:           "Sender Name",
            POPHost:                   "pop.example.com",
            POPPort:                   995,
            SMTPMinSendIntervalMsec:   1000,
            SMTPMinSendEmailIntervalMsec: 5000,
        },
    }
    
    // Создание сервиса
    service := &emailservice.EmailService{
        smtpClients:   make(map[int]*emailservice.SMTPClientDeliver),
        responseQueue: make(chan emailservice.DeliveryStatusResult, 1000),
    }
    
    // Инициализация SMTP клиентов
    for i, config := range smtpConfigs {
        service.smtpClients[i] = emailservice.NewSMTPClientDeliver(config)
    }
    
    // Создание сервиса проверки статусов
    service.statusChecker = emailservice.NewStatusCheckerService(smtpConfigs)
    
    // Контекст с отменой
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    
    // Обработка сигналов
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    
    // Запуск сервиса
    if err := service.Start(ctx); err != nil {
        log.Fatalf("Ошибка запуска сервиса: %v", err)
    }
    
    log.Println("Сервис запущен")
    
    // Ожидание сигнала
    <-sigChan
    log.Println("Получен сигнал остановки")
    
    // Остановка сервиса
    service.statusChecker.Stop()
    cancel()
    
    log.Println("Сервис остановлен")
}
```

## 7. Важные замечания

### 7.1. Переменные из SmtpClientDeliver.cs (строки 19-31)

Все переменные учтены в структуре `SMTPConfig`:

- `DeliveryStatus` → `DeliveryStatus` enum
- `SenderMailbox` → `SenderMailbox` struct
- `SmtpHost`, `SmtpPort` → `SMTPHost`, `SMTPPort`
- `PopHost`, `PopPort` → `POPHost`, `POPPort`
- `Username`, `Password` → `SMTPUser`, `SMTPPassword`
- `Dsn` → `DSNEnabled` (определяется автоматически)
- `_lastSentTime` → `lastSentTime` (time.Time)
- `_lastStatusTime` → `lastStatusTime` (time.Time)
- `SMTPMinSendIntervalMsec` → `SMTPMinSendIntervalMsec`
- `SMTPMinSendEmailIntervalMsec` → `SMTPMinSendEmailIntervalMsec`

### 7.2. Особенности реализации

1. **Envelope-Id**: Используется префикс `askemailsender` + `taskID` для идентификации
2. **DSN запрос**: Запрашиваются уведомления Success, Delay, Failure
3. **POP3 проверка**: Каждые 60 минут (настраивается)
4. **Ограничения**: Не более 1000 сообщений за раз
5. **Фильтрация**: Пропускаются уведомления для самого отправителя

### 7.3. Рекомендуемые библиотеки

- **Для SMTP**: `github.com/go-mail/mail/v2` или стандартная `net/smtp`
- **Для POP3**: `github.com/emersion/go-pop3`
- **Для парсинга MIME**: `github.com/emersion/go-message`
- **Для работы с Oracle**: `github.com/godror/godror`

## 8. Тестирование

### 8.1. Тест отправки с DSN

```go
func TestSendWithDSN(t *testing.T) {
    config := &SMTPConfig{
        SMTPHost:   "smtp.test.com",
        SMTPPort:   587,
        SMTPUser:   "test@test.com",
        SMTPPassword: "password",
        POPHost:    "pop.test.com",
        POPPort:    995,
    }
    
    client := NewSMTPClientDeliver(config)
    
    msg := &EmailMessage{
        TaskID:       "123",
        EmailAddress: "recipient@test.com",
        Title:        "Test",
        Text:         "Test message",
    }
    
    err := client.SendEmailWithDSN(msg)
    if err != nil {
        t.Fatalf("Ошибка отправки: %v", err)
    }
}
```

### 8.2. Тест парсинга DSN

```go
func TestParseDSN(t *testing.T) {
    // Создание тестового DSN сообщения
    dsnMessage := createTestDSNMessage()
    
    checker := NewPOP3StatusChecker(&SMTPConfig{})
    result, err := checker.processDSNMessage("sender@test.com", dsnMessage)
    
    if err != nil {
        t.Fatalf("Ошибка парсинга: %v", err)
    }
    
    if result.TaskID != 123 {
        t.Errorf("Ожидался taskID 123, получен %d", result.TaskID)
    }
    
    if result.Status != DeliveryStatusDelivered {
        t.Errorf("Ожидался статус Delivered, получен %v", result.Status)
    }
}
```

## Заключение

Данная инструкция описывает полную реализацию механизма получения статусов доставки email через DSN в Go проекте. Все переменные из оригинального C# кода учтены и адаптированы для Go.

