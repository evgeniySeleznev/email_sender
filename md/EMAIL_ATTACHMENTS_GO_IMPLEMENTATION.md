# Документация: Реализация работы с вложениями в Go-проекте для рассылки email

## 1. Обзор архитектуры

Система поддерживает три типа вложений:
- **Тип 1**: Отчеты через Crystal Reports (или аналогичный сервис генерации отчетов)
- **Тип 2**: Вложения из CLOB в Oracle БД
- **Тип 3**: Файлы с файловой системы

## 2. Структуры данных

### 2.1. Основные структуры

```go
package emailservice

import (
    "bytes"
    "time"
)

// AttachmentType определяет тип вложения
type AttachmentType int

const (
    AttachmentTypeReport AttachmentType = iota + 1 // Тип 1: Отчет через сервис
    AttachmentTypeClob                              // Тип 2: CLOB из Oracle
    AttachmentTypeFile                              // Тип 3: Файл с диска
)

// EmailAttachment представляет вложение к письму
type EmailAttachment struct {
    Type         AttachmentType
    FileName     string
    Data         []byte
    
    // Для типа 1 (Report)
    Catalog      string
    File         string
    DbLogin      string
    DbPass       string
    AttachParams map[string]string
    
    // Для типа 2 (Clob)
    ClobAttachId int
    
    // Для типа 3 (File)
    ReportFile   string
}

// EmailMessage представляет email сообщение
type EmailMessage struct {
    TaskID       string
    SmtpID       int
    EmailAddress string
    Title        string
    Text         string
    Schedule     bool
    ActiveDate   time.Time
    Attachments  []*EmailAttachment
}
```

## 3. Парсинг XML сообщений

### 3.1. Парсинг основного сообщения

```go
package emailservice

import (
    "encoding/xml"
    "fmt"
    "strconv"
    "strings"
    "time"
)

// XMLRoot представляет корневой элемент XML из очереди Oracle
type XMLRoot struct {
    XMLName xml.Name `xml:"root"`
    Head    XMLHead  `xml:"head"`
    Body    XMLBody  `xml:"body"`
}

type XMLHead struct {
    DateActiveFrom string `xml:"date_active_from"`
}

type XMLBody struct {
    Content string `xml:",chardata"`
}

// EmailXML представляет внутренний XML сообщения
type EmailXML struct {
    XMLName      xml.Name         `xml:"email"`
    EmailTaskID  string           `xml:"email_task_id,attr"`
    SmtpID       string           `xml:"smtp_id,attr"`
    EmailAddress string           `xml:"email_address,attr"`
    EmailTitle   string           `xml:"email_title,attr"`
    EmailText    string           `xml:"email_text,attr"`
    SendingSchedule string        `xml:"sending_schedule,attr"`
    Attaches     []AttachmentXML  `xml:"attach"`
}

type AttachmentXML struct {
    XMLName              xml.Name          `xml:"attach"`
    ReportType           string            `xml:"report_type,attr"`
    EmailAttachID        string            `xml:"email_attach_id,attr"`
    EmailAttachName      string            `xml:"email_attach_name,attr"`
    EmailAttachCatalog   string            `xml:"email_attach_catalog,attr"`
    EmailAttachFile      string            `xml:"email_attach_file,attr"`
    DbLogin              string            `xml:"db_login,attr"`
    DbPass               string            `xml:"db_pass,attr"`
    ReportFile           string            `xml:"report_file,attr"`
    AttachParams         []AttachParamXML  `xml:"attach_param"`
}

type AttachParamXML struct {
    XMLName  xml.Name `xml:"attach_param"`
    Name     string   `xml:"email_attach_param_name,attr"`
    Value    string   `xml:"email_attach_param_value,attr"`
}

// ParseEmailMessage парсит XML сообщение из очереди Oracle
func ParseEmailMessage(xmlData []byte) (*EmailMessage, error) {
    var root XMLRoot
    if err := xml.Unmarshal(xmlData, &root); err != nil {
        return nil, fmt.Errorf("ошибка парсинга корневого XML: %w", err)
    }
    
    // Извлекаем внутренний XML из CDATA секции
    bodyContent := strings.TrimSpace(root.Body.Content)
    if bodyContent == "" {
        return nil, fmt.Errorf("body content (CDATA) пуст")
    }
    
    // Парсим активную дату
    activeDate := time.Now()
    if root.Head.DateActiveFrom != "" {
        parsedDate, err := time.Parse(time.RFC3339, root.Head.DateActiveFrom)
        if err == nil {
            activeDate = parsedDate
        }
    }
    
    // Парсим внутренний XML
    var emailXML EmailXML
    if err := xml.Unmarshal([]byte(bodyContent), &emailXML); err != nil {
        return nil, fmt.Errorf("ошибка парсинга внутреннего XML: %w", err)
    }
    
    // Валидация обязательных полей
    if emailXML.EmailTaskID == "" || emailXML.EmailAddress == "" || 
       emailXML.EmailTitle == "" || emailXML.EmailText == "" {
        return nil, fmt.Errorf("указаны не все параметры отправления")
    }
    
    // Конвертируем SmtpID
    smtpID := 0
    if emailXML.SmtpID != "" {
        id, err := strconv.Atoi(emailXML.SmtpID)
        if err == nil {
            smtpID = id
        }
    }
    
    // Парсим флаг schedule
    schedule := emailXML.SendingSchedule == "1"
    
    // Парсим вложения
    attachments, err := parseAttachments(emailXML.Attaches, emailXML.EmailTaskID)
    if err != nil {
        return nil, fmt.Errorf("ошибка парсинга вложений: %w", err)
    }
    
    return &EmailMessage{
        TaskID:       strings.TrimSpace(emailXML.EmailTaskID),
        SmtpID:       smtpID,
        EmailAddress: emailXML.EmailAddress,
        Title:        emailXML.EmailTitle,
        Text:         emailXML.EmailText,
        Schedule:     schedule,
        ActiveDate:   activeDate,
        Attachments:  attachments,
    }, nil
}

// parseAttachments парсит список вложений
func parseAttachments(attachXMLs []AttachmentXML, taskID string) ([]*EmailAttachment, error) {
    attachments := make([]*EmailAttachment, 0, len(attachXMLs))
    
    for _, attachXML := range attachXMLs {
        if attachXML.ReportType == "" {
            return nil, fmt.Errorf("не указан report_type вложения")
        }
        
        reportType, err := strconv.Atoi(attachXML.ReportType)
        if err != nil {
            return nil, fmt.Errorf("неверный report_type: %s", attachXML.ReportType)
        }
        
        attachment := &EmailAttachment{
            FileName:     attachXML.EmailAttachName,
            AttachParams: make(map[string]string),
        }
        
        switch AttachmentType(reportType) {
        case AttachmentTypeClob: // Тип 2
            if attachXML.EmailAttachID == "" {
                return nil, fmt.Errorf("не указан email_attach_id для типа 2")
            }
            clobID, err := strconv.Atoi(attachXML.EmailAttachID)
            if err != nil {
                return nil, fmt.Errorf("неверный email_attach_id: %s", attachXML.EmailAttachID)
            }
            attachment.Type = AttachmentTypeClob
            attachment.ClobAttachId = clobID
            attachment.FileName = attachXML.EmailAttachName
            
        case AttachmentTypeFile: // Тип 3
            if attachXML.ReportFile == "" {
                return nil, fmt.Errorf("не указан report_file для типа 3")
            }
            attachment.Type = AttachmentTypeFile
            attachment.ReportFile = attachXML.ReportFile
            // Извлекаем имя файла из пути
            parts := strings.Split(attachXML.ReportFile, "/")
            if len(parts) > 0 {
                parts = strings.Split(attachXML.ReportFile, "\\")
            }
            if len(parts) > 0 {
                attachment.FileName = parts[len(parts)-1]
            }
            
        default: // Тип 1 (Report)
            if attachXML.EmailAttachCatalog == "" || attachXML.EmailAttachFile == "" ||
               attachXML.EmailAttachName == "" || attachXML.DbLogin == "" ||
               attachXML.DbPass == "" {
                return nil, fmt.Errorf("не указаны все параметры для типа 1")
            }
            attachment.Type = AttachmentTypeReport
            attachment.Catalog = attachXML.EmailAttachCatalog
            attachment.File = attachXML.EmailAttachFile
            attachment.FileName = attachXML.EmailAttachName
            attachment.DbLogin = attachXML.DbLogin
            attachment.DbPass = attachXML.DbPass
            
            // Парсим параметры отчета
            for _, param := range attachXML.AttachParams {
                attachment.AttachParams[param.Name] = param.Value
            }
        }
        
        attachments = append(attachments, attachment)
    }
    
    return attachments, nil
}
```

## 4. Обработка вложений

### 4.1. Интерфейс для загрузки вложений

```go
package emailservice

import (
    "context"
    "io"
)

// AttachmentLoader интерфейс для загрузки данных вложений
type AttachmentLoader interface {
    // LoadReport загружает отчет через сервис генерации отчетов (тип 1)
    LoadReport(ctx context.Context, attach *EmailAttachment) ([]byte, error)
    
    // LoadClob загружает CLOB из Oracle БД (тип 2)
    LoadClob(ctx context.Context, taskID string, clobID int) ([]byte, error)
    
    // LoadFile загружает файл с диска (тип 3)
    LoadFile(ctx context.Context, filePath string) ([]byte, error)
}

// LoadAttachments загружает все вложения для сообщения
func LoadAttachments(ctx context.Context, msg *EmailMessage, loader AttachmentLoader) error {
    for _, attach := range msg.Attachments {
        var data []byte
        var err error
        
        switch attach.Type {
        case AttachmentTypeReport:
            data, err = loader.LoadReport(ctx, attach)
            if err != nil {
                return fmt.Errorf("ошибка загрузки отчета %s: %w", attach.FileName, err)
            }
            
        case AttachmentTypeClob:
            taskID, _ := strconv.Atoi(msg.TaskID)
            data, err = loader.LoadClob(ctx, msg.TaskID, attach.ClobAttachId)
            if err != nil {
                return fmt.Errorf("ошибка загрузки CLOB %d: %w", attach.ClobAttachId, err)
            }
            
        case AttachmentTypeFile:
            data, err = loader.LoadFile(ctx, attach.ReportFile)
            if err != nil {
                return fmt.Errorf("ошибка загрузки файла %s: %w", attach.ReportFile, err)
            }
        }
        
        if len(data) == 0 {
            return fmt.Errorf("вложение %s пусто", attach.FileName)
        }
        
        attach.Data = data
    }
    
    return nil
}
```

### 4.2. Реализация загрузки CLOB из Oracle

```go
package emailservice

import (
    "context"
    "database/sql"
    "encoding/base64"
    "fmt"
    
    _ "github.com/godror/godror" // или другая библиотека для Oracle
)

// OracleAttachmentLoader реализует AttachmentLoader для Oracle
type OracleAttachmentLoader struct {
    db *sql.DB
}

// NewOracleAttachmentLoader создает новый загрузчик для Oracle
func NewOracleAttachmentLoader(db *sql.DB) *OracleAttachmentLoader {
    return &OracleAttachmentLoader{db: db}
}

// LoadClob загружает CLOB из Oracle БД
func (l *OracleAttachmentLoader) LoadClob(ctx context.Context, taskID string, clobID int) ([]byte, error) {
    var clobData sql.NullString
    
    query := `BEGIN
        pcsystem.pkg_email.get_email_report_clob(
            p_email_attach_id => :1,
            p_result => :2
        );
    END;`
    
    var result string
    err := l.db.QueryRowContext(ctx, query, clobID).Scan(&result)
    if err != nil {
        return nil, fmt.Errorf("ошибка вызова get_email_report_clob: %w", err)
    }
    
    if result == "" {
        return nil, fmt.Errorf("пустой документ")
    }
    
    // Декодируем из Base64
    data, err := base64.StdEncoding.DecodeString(result)
    if err != nil {
        return nil, fmt.Errorf("ошибка декодирования Base64: %w", err)
    }
    
    if len(data) == 0 {
        return nil, fmt.Errorf("пустой документ Base64")
    }
    
    return data, nil
}

// LoadFile загружает файл с диска
func (l *OracleAttachmentLoader) LoadFile(ctx context.Context, filePath string) ([]byte, error) {
    return os.ReadFile(filePath)
}

// LoadReport загружает отчет через сервис (требует реализации)
func (l *OracleAttachmentLoader) LoadReport(ctx context.Context, attach *EmailAttachment) ([]byte, error) {
    // Реализация зависит от вашего сервиса генерации отчетов
    // Например, вызов SOAP/REST API
    return nil, fmt.Errorf("не реализовано")
}
```

### 4.3. Реализация загрузки отчетов через сервис

```go
package emailservice

import (
    "bytes"
    "context"
    "encoding/base64"
    "encoding/xml"
    "fmt"
    "net/http"
    "time"
)

// ReportServiceLoader реализует загрузку отчетов через веб-сервис
type ReportServiceLoader struct {
    client     *http.Client
    serviceURL string
}

// NewReportServiceLoader создает новый загрузчик отчетов
func NewReportServiceLoader(serviceURL string) *ReportServiceLoader {
    return &ReportServiceLoader{
        client: &http.Client{
            Timeout: 30 * time.Second,
        },
        serviceURL: serviceURL,
    }
}

// ReportInfoXML представляет XML для запроса информации об отчете
type ReportInfoXML struct {
    XMLName xml.Name `xml:"Report"`
    Main    MainXML  `xml:"Main"`
}

type MainXML struct {
    XMLName        xml.Name `xml:"Main"`
    ApplicationName string  `xml:"Application_Name,attr"`
    DBInstance     string   `xml:"DB_Instance,attr"`
    DBPass         string   `xml:"DB_Pass,attr"`
    DBUser         string   `xml:"DB_User,attr"`
    ExportFormat   int      `xml:"ExportFormat,attr"`
    ReportName     string   `xml:"Report_Name,attr"`
}

// LoadReport загружает отчет через веб-сервис
func (l *ReportServiceLoader) LoadReport(ctx context.Context, attach *EmailAttachment) ([]byte, error) {
    // 1. Получаем информацию об отчете
    reportInfo, err := l.getReportInfo(ctx, attach)
    if err != nil {
        return nil, fmt.Errorf("ошибка получения информации об отчете: %w", err)
    }
    
    // 2. Применяем параметры отчета
    for name, value := range attach.AttachParams {
        // Обновляем параметры в reportInfo
        // (зависит от структуры XML)
    }
    
    // 3. Получаем отчет
    reportXML := l.buildReportRequest(reportInfo, attach.AttachParams)
    reportData, err := l.callReportService(ctx, reportXML)
    if err != nil {
        return nil, fmt.Errorf("ошибка вызова сервиса отчетов: %w", err)
    }
    
    // 4. Декодируем из Base64
    data, err := base64.StdEncoding.DecodeString(reportData)
    if err != nil {
        return nil, fmt.Errorf("ошибка декодирования Base64: %w", err)
    }
    
    return data, nil
}

func (l *ReportServiceLoader) getReportInfo(ctx context.Context, attach *EmailAttachment) (string, error) {
    reportInfo := ReportInfoXML{
        Main: MainXML{
            ApplicationName: attach.Catalog,
            DBInstance:      "", // Из конфигурации
            DBPass:          attach.DbPass,
            DBUser:          attach.DbLogin,
            ExportFormat:    5,  // PDF
            ReportName:      attach.File,
        },
    }
    
    var buf bytes.Buffer
    encoder := xml.NewEncoder(&buf)
    encoder.Indent("", "")
    if err := encoder.Encode(reportInfo); err != nil {
        return "", err
    }
    
    // Вызов getReportInfo через SOAP/REST
    // Реализация зависит от вашего API
    return "", nil
}

func (l *ReportServiceLoader) buildReportRequest(reportInfo string, params map[string]string) string {
    // Построение XML запроса с параметрами
    return ""
}

func (l *ReportServiceLoader) callReportService(ctx context.Context, requestXML string) (string, error) {
    // Вызов getReport через SOAP/REST
    // Реализация зависит от вашего API
    return "", nil
}
```

## 5. Отправка email с вложениями

### 5.1. Использование библиотеки go-mail

```go
package emailservice

import (
    "bytes"
    "fmt"
    "io"
    "mime"
    "net/mail"
    "path/filepath"
    
    "github.com/go-mail/mail/v2"
)

// EmailSender отправляет email сообщения
type EmailSender struct {
    dialer *mail.Dialer
    from   mail.Address
}

// NewEmailSender создает новый отправитель email
func NewEmailSender(host string, port int, username, password, fromEmail, fromName string) *EmailSender {
    dialer := mail.NewDialer(host, port, username, password)
    
    return &EmailSender{
        dialer: dialer,
        from: mail.Address{
            Name:    fromName,
            Address: fromEmail,
        },
    }
}

// SendEmail отправляет email сообщение с вложениями
func (s *EmailSender) SendEmail(msg *EmailMessage) error {
    m := mail.NewMessage()
    
    // Отправитель
    m.SetHeader("From", s.from.String())
    m.SetHeader("Message-ID", msg.TaskID)
    
    // Получатели (поддержка нескольких адресов через запятую или точку с запятой)
    addresses := parseAddresses(msg.EmailAddress)
    m.SetHeader("To", addresses...)
    
    // Тема
    m.SetHeader("Subject", msg.Title)
    
    // Тело письма (HTML или Plain)
    m.SetBody("text/html", msg.Text) // или "text/plain"
    
    // Добавляем вложения
    for _, attach := range msg.Attachments {
        if len(attach.Data) == 0 {
            continue
        }
        
        // Определяем MIME тип по расширению файла
        ext := filepath.Ext(attach.FileName)
        mimeType := mime.TypeByExtension(ext)
        if mimeType == "" {
            mimeType = "application/octet-stream"
        }
        
        m.AttachReader(attach.FileName, bytes.NewReader(attach.Data), mail.SetHeader(map[string][]string{
            "Content-Type": {mimeType},
        }))
    }
    
    // Отправка
    if err := s.dialer.DialAndSend(m); err != nil {
        return fmt.Errorf("ошибка отправки email: %w", err)
    }
    
    return nil
}

// parseAddresses парсит строку адресов
func parseAddresses(addresses string) []string {
    // Заменяем запятые на точки с запятой
    addresses = strings.ReplaceAll(addresses, ",", ";")
    
    parts := strings.Split(addresses, ";")
    result := make([]string, 0, len(parts))
    
    for _, part := range parts {
        addr := strings.TrimSpace(part)
        if addr != "" {
            result = append(result, addr)
        }
    }
    
    return result
}
```

### 5.2. Альтернатива: использование стандартной библиотеки

```go
package emailservice

import (
    "bytes"
    "encoding/base64"
    "fmt"
    "mime"
    "mime/multipart"
    "net/mail"
    "net/smtp"
    "path/filepath"
    "strings"
)

// SMTPEmailSender отправляет email через SMTP
type SMTPEmailSender struct {
    host     string
    port     int
    username string
    password string
    from     mail.Address
}

// NewSMTPEmailSender создает новый SMTP отправитель
func NewSMTPEmailSender(host string, port int, username, password, fromEmail, fromName string) *SMTPEmailSender {
    return &SMTPEmailSender{
        host:     host,
        port:     port,
        username: username,
        password: password,
        from: mail.Address{
            Name:    fromName,
            Address: fromEmail,
        },
    }
}

// SendEmail отправляет email с вложениями
func (s *SMTPEmailSender) SendEmail(msg *EmailMessage) error {
    var body bytes.Buffer
    
    // Заголовки
    headers := map[string]string{
        "From":         s.from.String(),
        "To":           strings.Join(parseAddresses(msg.EmailAddress), ", "),
        "Subject":      msg.Title,
        "MIME-Version": "1.0",
    }
    
    // Если есть вложения, используем multipart/mixed
    if len(msg.Attachments) > 0 {
        headers["Content-Type"] = "multipart/mixed; boundary=\"boundary123\""
        
        // Записываем заголовки
        for k, v := range headers {
            fmt.Fprintf(&body, "%s: %s\r\n", k, v)
        }
        body.WriteString("\r\n")
        
        // Тело письма
        body.WriteString("--boundary123\r\n")
        body.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
        body.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
        body.WriteString("\r\n")
        body.WriteString(msg.Text)
        body.WriteString("\r\n")
        
        // Вложения
        for _, attach := range msg.Attachments {
            if len(attach.Data) == 0 {
                continue
            }
            
            ext := filepath.Ext(attach.FileName)
            mimeType := mime.TypeByExtension(ext)
            if mimeType == "" {
                mimeType = "application/octet-stream"
            }
            
            body.WriteString("--boundary123\r\n")
            fmt.Fprintf(&body, "Content-Type: %s\r\n", mimeType)
            fmt.Fprintf(&body, "Content-Disposition: attachment; filename=\"%s\"\r\n", attach.FileName)
            body.WriteString("Content-Transfer-Encoding: base64\r\n")
            body.WriteString("\r\n")
            
            // Кодируем в Base64
            encoder := base64.NewEncoder(base64.StdEncoding, &body)
            encoder.Write(attach.Data)
            encoder.Close()
            body.WriteString("\r\n")
        }
        
        body.WriteString("--boundary123--\r\n")
    } else {
        // Простое письмо без вложений
        headers["Content-Type"] = "text/html; charset=UTF-8"
        
        for k, v := range headers {
            fmt.Fprintf(&body, "%s: %s\r\n", k, v)
        }
        body.WriteString("\r\n")
        body.WriteString(msg.Text)
    }
    
    // Подключение к SMTP серверу
    addr := fmt.Sprintf("%s:%d", s.host, s.port)
    auth := smtp.PlainAuth("", s.username, s.password, s.host)
    
    to := parseAddresses(msg.EmailAddress)
    toAddresses := make([]string, len(to))
    for i, addr := range to {
        toAddresses[i] = addr
    }
    
    err := smtp.SendMail(addr, auth, s.from.Address, toAddresses, body.Bytes())
    if err != nil {
        return fmt.Errorf("ошибка отправки SMTP: %w", err)
    }
    
    return nil
}
```

## 6. Полный пример использования

```go
package main

import (
    "context"
    "log"
    "os"
    
    "your-project/emailservice"
)

func main() {
    // 1. Читаем XML из очереди Oracle
    xmlData := []byte(`<root>
        <head><date_active_from>2024-01-01T00:00:00Z</date_active_from></head>
        <body><![CDATA[
            <email email_task_id="123" email_address="test@example.com" 
                   email_title="Test" email_text="Hello">
                <attach report_type="2" email_attach_id="456" 
                        email_attach_name="document.pdf"/>
            </email>
        ]]></body>
    </root>`)
    
    // 2. Парсим сообщение
    msg, err := emailservice.ParseEmailMessage(xmlData)
    if err != nil {
        log.Fatalf("Ошибка парсинга: %v", err)
    }
    
    // 3. Инициализируем загрузчик вложений
    db, _ := sql.Open("godror", "connection_string")
    loader := emailservice.NewOracleAttachmentLoader(db)
    
    // 4. Загружаем вложения
    ctx := context.Background()
    if err := emailservice.LoadAttachments(ctx, msg, loader); err != nil {
        log.Fatalf("Ошибка загрузки вложений: %v", err)
    }
    
    // 5. Отправляем email
    sender := emailservice.NewEmailSender(
        "smtp.example.com",
        587,
        "username",
        "password",
        "from@example.com",
        "Sender Name",
    )
    
    if err := sender.SendEmail(msg); err != nil {
        log.Fatalf("Ошибка отправки: %v", err)
    }
    
    log.Println("Email успешно отправлен")
}
```

## 7. Рекомендуемые библиотеки

- **Для работы с Oracle**: `github.com/godror/godror` или `github.com/sijms/go-ora`
- **Для отправки email**: `github.com/go-mail/mail/v2` или стандартная `net/smtp`
- **Для парсинга XML**: стандартная `encoding/xml`
- **Для работы с Base64**: стандартная `encoding/base64`

## 8. Обработка ошибок и логирование

```go
package emailservice

import (
    "log"
    "os"
)

// Logger интерфейс для логирования
type Logger interface {
    Debug(taskID string, format string, args ...interface{})
    Info(taskID string, format string, args ...interface{})
    Error(taskID string, err error, format string, args ...interface{})
}

// StdLogger реализация через стандартный log
type StdLogger struct {
    debugLog *log.Logger
    infoLog  *log.Logger
    errorLog *log.Logger
}

func NewStdLogger() *StdLogger {
    return &StdLogger{
        debugLog: log.New(os.Stdout, "[DEBUG] ", log.LstdFlags),
        infoLog:  log.New(os.Stdout, "[INFO] ", log.LstdFlags),
        errorLog: log.New(os.Stderr, "[ERROR] ", log.LstdFlags),
    }
}

func (l *StdLogger) Debug(taskID string, format string, args ...interface{}) {
    l.debugLog.Printf("[%s] "+format, append([]interface{}{taskID}, args...)...)
}

func (l *StdLogger) Info(taskID string, format string, args ...interface{}) {
    l.infoLog.Printf("[%s] "+format, append([]interface{}{taskID}, args...)...)
}

func (l *StdLogger) Error(taskID string, err error, format string, args ...interface{}) {
    l.errorLog.Printf("[%s] "+format+": %v", append([]interface{}{taskID}, args...), err)
}
```

## 9. Заключение

Данный документ описывает полную реализацию работы с вложениями в Go-проекте. Основные компоненты:

1. **Структуры данных** для представления сообщений и вложений
2. **Парсинг XML** из очереди Oracle
3. **Загрузка вложений** трех типов (Report, Clob, File)
4. **Отправка email** с вложениями через SMTP

Адаптируйте код под вашу инфраструктуру и требования.

