# Документация: Реализация работы с SOAP API Crystal Reports в Go-проекте

## 1. Обзор архитектуры

Система использует SOAP веб-сервис Crystal Reports для генерации отчетов. Процесс состоит из двух этапов:

1. **getReportInfo** - получение информации об отчете и его параметрах
2. **getReport** - генерация отчета с примененными параметрами (возвращает Base64 строку)

### 1.1. Структура SOAP API

- **Namespace**: `http://webservices.crystal.progcom/`
- **Endpoint**: Динамически получается из Oracle БД через `pcsystem.PKG_EMAIL.GET_SOAP_ADDRESS()`
- **Формат**: SOAP 1.1, Document/Literal стиль
- **Транспорт**: HTTP

## 2. Получение URL сервиса из Oracle

### 2.1. Метод получения URL

```go
package emailservice

import (
    "context"
    "database/sql"
    "fmt"
)

// GetCrystalReportsURL получает URL SOAP сервиса Crystal Reports из Oracle БД
func GetCrystalReportsURL(ctx context.Context, db *sql.DB) (string, error) {
    var url string
    
    query := `BEGIN
        pcsystem.PKG_EMAIL.GET_SOAP_ADDRESS(L_SOAP_ADDRES => :1);
    END;`
    
    err := db.QueryRowContext(ctx, query).Scan(&url)
    if err != nil {
        return "", fmt.Errorf("ошибка получения SOAP адреса: %w", err)
    }
    
    if url == "" {
        return "", fmt.Errorf("SOAP адрес пуст")
    }
    
    return url, nil
}
```

### 2.2. Альтернатива с использованием godror

```go
package emailservice

import (
    "context"
    "fmt"
    
    "github.com/godror/godror"
)

// GetCrystalReportsURLGodror получает URL через godror
func GetCrystalReportsURLGodror(ctx context.Context, conn *godror.Connection) (string, error) {
    var url string
    
    cursor := conn.NewCursor()
    defer cursor.Close()
    
    err := cursor.Execute(ctx, `BEGIN
        pcsystem.PKG_EMAIL.GET_SOAP_ADDRESS(L_SOAP_ADDRES => :1);
    END;`, godror.Named("L_SOAP_ADDRES", godror.Out{Dest: &url}))
    
    if err != nil {
        return "", fmt.Errorf("ошибка получения SOAP адреса: %w", err)
    }
    
    return url, nil
}
```

## 3. Структуры данных для XML запросов

### 3.1. Структуры для getReportInfo

```go
package emailservice

import (
    "encoding/xml"
)

// ReportRequest представляет XML запрос для Crystal Reports
type ReportRequest struct {
    XMLName xml.Name `xml:"Report"`
    Main    MainInfo `xml:"Main"`
}

// MainInfo содержит основную информацию об отчете
type MainInfo struct {
    XMLName        xml.Name `xml:"Main"`
    ApplicationName string   `xml:"Application_Name,attr"`
    DBInstance     string   `xml:"DB_Instance,attr"`
    DBPass         string   `xml:"DB_Pass,attr"`
    DBUser         string   `xml:"DB_User,attr"`
    ExportFormat   int      `xml:"ExportFormat,attr"` // 5 = PDF
    ReportName     string   `xml:"Report_Name,attr"`
}

// ReportInfoResponse представляет ответ от getReportInfo
type ReportInfoResponse struct {
    XMLName xml.Name `xml:"ReportInfo"`
    Params  []Param  `xml:"param"`
}

// Param представляет параметр отчета
type Param struct {
    XMLName   xml.Name `xml:"param"`
    Name      string   `xml:"name,attr"`
    Value     string   `xml:"value,attr"`
    FieldType int      `xml:"field_type,attr"`
    Multi     bool     `xml:"multi,attr"`
    Prompt    string   `xml:"prompt,attr"`
    RangeKind string   `xml:"range_kind,attr"`
    ValueType int      `xml:"value_type,attr"`
}

// ReportWithParams представляет запрос с параметрами для getReport
type ReportWithParams struct {
    XMLName    xml.Name      `xml:"Report"`
    Main       MainInfo      `xml:"Main"`
    MainReport *MainReport   `xml:"main_report,omitempty"`
}

// MainReport содержит параметры отчета
type MainReport struct {
    XMLName     xml.Name      `xml:"main_report"`
    ReportParams ReportParams `xml:"report_params"`
}

// ReportParams содержит список параметров
type ReportParams struct {
    XMLName xml.Name `xml:"report_params"`
    Param   Param    `xml:"param"`
}
```

## 4. SOAP клиент для Crystal Reports

### 4.1. Базовая структура клиента

```go
package emailservice

import (
    "bytes"
    "context"
    "encoding/xml"
    "fmt"
    "io"
    "net/http"
    "strings"
    "time"
)

// CrystalReportsClient клиент для работы с SOAP API Crystal Reports
type CrystalReportsClient struct {
    baseURL    string
    httpClient *http.Client
    namespace  string
}

// NewCrystalReportsClient создает новый клиент
func NewCrystalReportsClient(baseURL string) *CrystalReportsClient {
    return &CrystalReportsClient{
        baseURL: strings.TrimSuffix(baseURL, "/"),
        httpClient: &http.Client{
            Timeout: 60 * time.Second,
        },
        namespace: "http://webservices.crystal.progcom/",
    }
}

// buildSOAPEnvelope создает SOAP конверт
func (c *CrystalReportsClient) buildSOAPEnvelope(action string, bodyXML string) string {
    envelope := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
    <soap:Body>
        <%s xmlns="%s">
            <XMLString><![CDATA[%s]]></XMLString>
        </%s>
    </soap:Body>
</soap:Envelope>`, action, c.namespace, bodyXML, action)
    
    return envelope
}

// callSOAP выполняет SOAP запрос
func (c *CrystalReportsClient) callSOAP(ctx context.Context, action string, bodyXML string) (string, error) {
    soapEnvelope := c.buildSOAPEnvelope(action, bodyXML)
    
    req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewBufferString(soapEnvelope))
    if err != nil {
        return "", fmt.Errorf("ошибка создания запроса: %w", err)
    }
    
    req.Header.Set("Content-Type", "text/xml; charset=utf-8")
    req.Header.Set("SOAPAction", "")
    
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return "", fmt.Errorf("ошибка выполнения запроса: %w", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        bodyBytes, _ := io.ReadAll(resp.Body)
        return "", fmt.Errorf("HTTP ошибка %d: %s", resp.StatusCode, string(bodyBytes))
    }
    
    bodyBytes, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", fmt.Errorf("ошибка чтения ответа: %w", err)
    }
    
    return string(bodyBytes), nil
}

// parseSOAPResponse извлекает содержимое из SOAP ответа
func (c *CrystalReportsClient) parseSOAPResponse(soapResponse, action string) (string, error) {
    // Простой парсинг XML для извлечения значения return
    decoder := xml.NewDecoder(strings.NewReader(soapResponse))
    
    var inReturn bool
    var result strings.Builder
    
    for {
        tok, err := decoder.Token()
        if err == io.EOF {
            break
        }
        if err != nil {
            return "", fmt.Errorf("ошибка парсинга XML: %w", err)
        }
        
        switch se := tok.(type) {
        case xml.StartElement:
            if se.Name.Local == "return" {
                inReturn = true
            }
        case xml.EndElement:
            if se.Name.Local == "return" {
                inReturn = false
            }
        case xml.CharData:
            if inReturn {
                result.Write(se)
            }
        }
    }
    
    return result.String(), nil
}
```

### 4.2. Метод getReportInfo

```go
// GetReportInfo получает информацию об отчете
func (c *CrystalReportsClient) GetReportInfo(ctx context.Context, req *ReportRequest) (*ReportInfoResponse, error) {
    // Сериализуем запрос в XML
    var buf bytes.Buffer
    encoder := xml.NewEncoder(&buf)
    encoder.Indent("", "")
    
    if err := encoder.Encode(req); err != nil {
        return nil, fmt.Errorf("ошибка сериализации запроса: %w", err)
    }
    
    requestXML := buf.String()
    
    // Выполняем SOAP запрос
    soapResponse, err := c.callSOAP(ctx, "getReportInfo", requestXML)
    if err != nil {
        return nil, fmt.Errorf("ошибка SOAP запроса: %w", err)
    }
    
    // Извлекаем результат
    resultXML, err := c.parseSOAPResponse(soapResponse, "getReportInfoResponse")
    if err != nil {
        return nil, fmt.Errorf("ошибка парсинга ответа: %w", err)
    }
    
    // Парсим XML ответа
    var reportInfo ReportInfoResponse
    if err := xml.Unmarshal([]byte(resultXML), &reportInfo); err != nil {
        return nil, fmt.Errorf("ошибка десериализации ответа: %w", err)
    }
    
    return &reportInfo, nil
}
```

### 4.3. Метод getReport

```go
// GetReport генерирует отчет и возвращает Base64 строку
func (c *CrystalReportsClient) GetReport(ctx context.Context, req *ReportWithParams) (string, error) {
    // Сериализуем запрос в XML
    var buf bytes.Buffer
    encoder := xml.NewEncoder(&buf)
    encoder.Indent("", "")
    
    if err := encoder.Encode(req); err != nil {
        return "", fmt.Errorf("ошибка сериализации запроса: %w", err)
    }
    
    requestXML := buf.String()
    
    // Выполняем SOAP запрос
    soapResponse, err := c.callSOAP(ctx, "getReport", requestXML)
    if err != nil {
        return "", fmt.Errorf("ошибка SOAP запроса: %w", err)
    }
    
    // Извлекаем Base64 строку из ответа
    base64Data, err := c.parseSOAPResponse(soapResponse, "getReportResponse")
    if err != nil {
        return "", fmt.Errorf("ошибка парсинга ответа: %w", err)
    }
    
    return strings.TrimSpace(base64Data), nil
}
```

## 5. Реализация загрузчика отчетов

### 5.1. Полная реализация ReportServiceLoader

```go
package emailservice

import (
    "context"
    "encoding/base64"
    "encoding/xml"
    "fmt"
    "strings"
)

// CrystalReportsLoader реализует AttachmentLoader для Crystal Reports
type CrystalReportsLoader struct {
    client *CrystalReportsClient
    dbName string
}

// NewCrystalReportsLoader создает новый загрузчик отчетов
func NewCrystalReportsLoader(baseURL, dbName string) *CrystalReportsLoader {
    return &CrystalReportsLoader{
        client: NewCrystalReportsClient(baseURL),
        dbName: dbName,
    }
}

// LoadReport загружает отчет через Crystal Reports SOAP API
func (l *CrystalReportsLoader) LoadReport(ctx context.Context, attach *EmailAttachment) ([]byte, error) {
    // Шаг 1: Создаем запрос для getReportInfo
    reportRequest := &ReportRequest{
        Main: MainInfo{
            ApplicationName: attach.Catalog,
            DBInstance:     l.dbName,
            DBPass:         attach.DbPass,
            DBUser:         attach.DbLogin,
            ExportFormat:   5, // PDF
            ReportName:     attach.File,
        },
    }
    
    // Шаг 2: Получаем информацию об отчете
    reportInfo, err := l.client.GetReportInfo(ctx, reportRequest)
    if err != nil {
        return nil, fmt.Errorf("ошибка получения информации об отчете: %w", err)
    }
    
    // Шаг 3: Создаем запрос с параметрами для getReport
    reportWithParams := &ReportWithParams{
        Main: reportRequest.Main,
    }
    
    // Применяем параметры отчета
    if len(attach.AttachParams) > 0 && len(reportInfo.Params) > 0 {
        // Находим нужные параметры и обновляем их значения
        params := make([]Param, 0, len(attach.AttachParams))
        
        for _, infoParam := range reportInfo.Params {
            param := infoParam
            // Обновляем значение, если оно передано в attach.AttachParams
            if value, ok := attach.AttachParams[infoParam.Name]; ok {
                param.Value = value
            }
            params = append(params, param)
        }
        
        if len(params) > 0 {
            reportWithParams.MainReport = &MainReport{
                ReportParams: ReportParams{
                    Param: params[0], // Для простоты берем первый параметр
                },
            }
        }
    }
    
    // Шаг 4: Генерируем отчет
    base64Data, err := l.client.GetReport(ctx, reportWithParams)
    if err != nil {
        return nil, fmt.Errorf("ошибка генерации отчета: %w", err)
    }
    
    // Шаг 5: Декодируем из Base64
    data, err := base64.StdEncoding.DecodeString(base64Data)
    if err != nil {
        return nil, fmt.Errorf("ошибка декодирования Base64: %w", err)
    }
    
    if len(data) == 0 {
        return nil, fmt.Errorf("пустой отчет")
    }
    
    return data, nil
}
```

### 5.2. Улучшенная версия с поддержкой множественных параметров

```go
// ReportWithMultipleParams расширенная версия с поддержкой нескольких параметров
type ReportWithMultipleParams struct {
    XMLName    xml.Name      `xml:"Report"`
    Main       MainInfo      `xml:"Main"`
    MainReport *MainReportMultiple `xml:"main_report,omitempty"`
}

type MainReportMultiple struct {
    XMLName     xml.Name      `xml:"main_report"`
    ReportParams ReportParamsMultiple `xml:"report_params"`
}

type ReportParamsMultiple struct {
    XMLName xml.Name `xml:"report_params"`
    Params  []Param  `xml:"param"`
}

// LoadReportAdvanced улучшенная версия с поддержкой множественных параметров
func (l *CrystalReportsLoader) LoadReportAdvanced(ctx context.Context, attach *EmailAttachment) ([]byte, error) {
    // Шаг 1: Создаем запрос для getReportInfo
    reportRequest := &ReportRequest{
        Main: MainInfo{
            ApplicationName: attach.Catalog,
            DBInstance:     l.dbName,
            DBPass:         attach.DbPass,
            DBUser:         attach.DbLogin,
            ExportFormat:   5,
            ReportName:     attach.File,
        },
    }
    
    // Шаг 2: Получаем информацию об отчете
    reportInfo, err := l.client.GetReportInfo(ctx, reportRequest)
    if err != nil {
        return nil, fmt.Errorf("ошибка получения информации об отчете: %w", err)
    }
    
    // Шаг 3: Создаем запрос с параметрами
    reportWithParams := &ReportWithMultipleParams{
        Main: reportRequest.Main,
    }
    
    // Применяем все параметры
    if len(attach.AttachParams) > 0 {
        params := make([]Param, 0)
        
        // Создаем карту параметров из attach.AttachParams для быстрого поиска
        paramValues := make(map[string]string)
        for k, v := range attach.AttachParams {
            paramValues[k] = v
        }
        
        // Обновляем параметры из reportInfo значениями из attach.AttachParams
        for _, infoParam := range reportInfo.Params {
            param := infoParam
            if value, ok := paramValues[infoParam.Name]; ok {
                param.Value = value
            }
            params = append(params, param)
        }
        
        if len(params) > 0 {
            reportWithParams.MainReport = &MainReportMultiple{
                ReportParams: ReportParamsMultiple{
                    Params: params,
                },
            }
        }
    }
    
    // Сериализуем запрос
    var buf bytes.Buffer
    encoder := xml.NewEncoder(&buf)
    encoder.Indent("", "")
    if err := encoder.Encode(reportWithParams); err != nil {
        return nil, fmt.Errorf("ошибка сериализации: %w", err)
    }
    
    requestXML := buf.String()
    
    // Выполняем SOAP запрос getReport
    soapResponse, err := l.client.callSOAP(ctx, "getReport", requestXML)
    if err != nil {
        return nil, fmt.Errorf("ошибка SOAP запроса: %w", err)
    }
    
    // Извлекаем Base64 строку
    base64Data, err := l.client.parseSOAPResponse(soapResponse, "getReportResponse")
    if err != nil {
        return nil, fmt.Errorf("ошибка парсинга ответа: %w", err)
    }
    
    base64Data = strings.TrimSpace(base64Data)
    
    // Декодируем из Base64
    data, err := base64.StdEncoding.DecodeString(base64Data)
    if err != nil {
        return nil, fmt.Errorf("ошибка декодирования Base64: %w", err)
    }
    
    if len(data) == 0 {
        return nil, fmt.Errorf("пустой отчет")
    }
    
    return data, nil
}
```

## 6. Примеры использования

### 6.1. Базовый пример

```go
package main

import (
    "context"
    "database/sql"
    "log"
    
    "your-project/emailservice"
)

func main() {
    // 1. Получаем URL из Oracle
    db, _ := sql.Open("godror", "connection_string")
    defer db.Close()
    
    ctx := context.Background()
    url, err := emailservice.GetCrystalReportsURL(ctx, db)
    if err != nil {
        log.Fatalf("Ошибка получения URL: %v", err)
    }
    
    // 2. Создаем клиент
    loader := emailservice.NewCrystalReportsLoader(url, "ORCL")
    
    // 3. Создаем вложение с параметрами отчета
    attach := &emailservice.EmailAttachment{
        Type:     emailservice.AttachmentTypeReport,
        Catalog:  "MyCatalog",
        File:     "MyReport.rpt",
        FileName: "report.pdf",
        DbLogin:  "username",
        DbPass:   "password",
        AttachParams: map[string]string{
            "P_CH_LIST": "123,456,789",
        },
    }
    
    // 4. Загружаем отчет
    data, err := loader.LoadReport(ctx, attach)
    if err != nil {
        log.Fatalf("Ошибка загрузки отчета: %v", err)
    }
    
    log.Printf("Отчет загружен, размер: %d байт", len(data))
}
```

### 6.2. Интеграция с системой отправки email

```go
// В методе LoadAttachments из основного документа
func LoadAttachments(ctx context.Context, msg *EmailMessage, loader AttachmentLoader) error {
    for _, attach := range msg.Attachments {
        var data []byte
        var err error
        
        switch attach.Type {
        case AttachmentTypeReport:
            // Для Crystal Reports используем специальный загрузчик
            if crystalLoader, ok := loader.(*CrystalReportsLoader); ok {
                data, err = crystalLoader.LoadReport(ctx, attach)
            } else {
                data, err = loader.LoadReport(ctx, attach)
            }
            if err != nil {
                return fmt.Errorf("ошибка загрузки отчета %s: %w", attach.FileName, err)
            }
            
        case AttachmentTypeClob:
            // ... остальные типы
        }
        
        attach.Data = data
    }
    
    return nil
}
```

## 7. Обработка ошибок

### 7.1. SOAP Fault обработка

```go
// SOAPFault представляет SOAP ошибку
type SOAPFault struct {
    XMLName xml.Name `xml:"Fault"`
    Code    string   `xml:"faultcode"`
    String  string   `xml:"faultstring"`
    Detail  string   `xml:"detail"`
}

// parseSOAPFault извлекает информацию об ошибке из SOAP ответа
func (c *CrystalReportsClient) parseSOAPFault(soapResponse string) (*SOAPFault, error) {
    decoder := xml.NewDecoder(strings.NewReader(soapResponse))
    
    var fault SOAPFault
    if err := decoder.Decode(&fault); err != nil {
        return nil, err
    }
    
    return &fault, nil
}

// callSOAPWithErrorHandling улучшенная версия с обработкой SOAP Fault
func (c *CrystalReportsClient) callSOAPWithErrorHandling(ctx context.Context, action string, bodyXML string) (string, error) {
    soapEnvelope := c.buildSOAPEnvelope(action, bodyXML)
    
    req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewBufferString(soapEnvelope))
    if err != nil {
        return "", fmt.Errorf("ошибка создания запроса: %w", err)
    }
    
    req.Header.Set("Content-Type", "text/xml; charset=utf-8")
    req.Header.Set("SOAPAction", "")
    
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return "", fmt.Errorf("ошибка выполнения запроса: %w", err)
    }
    defer resp.Body.Close()
    
    bodyBytes, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", fmt.Errorf("ошибка чтения ответа: %w", err)
    }
    
    bodyStr := string(bodyBytes)
    
    // Проверяем на SOAP Fault
    if strings.Contains(bodyStr, "soap:Fault") || strings.Contains(bodyStr, "Fault") {
        fault, err := c.parseSOAPFault(bodyStr)
        if err == nil {
            return "", fmt.Errorf("SOAP Fault: %s - %s", fault.Code, fault.String)
        }
    }
    
    if resp.StatusCode != http.StatusOK {
        return "", fmt.Errorf("HTTP ошибка %d: %s", resp.StatusCode, bodyStr)
    }
    
    return bodyStr, nil
}
```

## 8. Рекомендуемые библиотеки

- **Для работы с Oracle**: `github.com/godror/godror` или `github.com/sijms/go-ora`
- **Для работы с XML**: стандартная `encoding/xml`
- **Для работы с Base64**: стандартная `encoding/base64`
- **Для HTTP клиента**: стандартная `net/http` (или `github.com/go-resty/resty` для расширенных возможностей)

## 9. Пример SOAP запросов/ответов

### 9.1. Запрос getReportInfo

```xml
<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
    <soap:Body>
        <getReportInfo xmlns="http://webservices.crystal.progcom/">
            <XMLString><![CDATA[
                <Report>
                    <Main Application_Name="MyCatalog" 
                          DB_Instance="ORCL" 
                          DB_Pass="password" 
                          DB_User="user" 
                          ExportFormat="5" 
                          Report_Name="MyReport.rpt"/>
                </Report>
            ]]></XMLString>
        </getReportInfo>
    </soap:Body>
</soap:Envelope>
```

### 9.2. Ответ getReportInfo

```xml
<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
    <soap:Body>
        <getReportInfoResponse xmlns="http://webservices.crystal.progcom/">
            <return>
                <ReportInfo>
                    <param name="P_CH_LIST" 
                           value="" 
                           field_type="3" 
                           multi="false" 
                           prompt="" 
                           range_kind="1" 
                           value_type="11"/>
                </ReportInfo>
            </return>
        </getReportInfoResponse>
    </soap:Body>
</soap:Envelope>
```

### 9.3. Запрос getReport

```xml
<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
    <soap:Body>
        <getReport xmlns="http://webservices.crystal.progcom/">
            <XMLString><![CDATA[
                <Report>
                    <Main Application_Name="MyCatalog" 
                          DB_Instance="ORCL" 
                          DB_Pass="password" 
                          DB_User="user" 
                          ExportFormat="5" 
                          Report_Name="MyReport.rpt"/>
                    <main_report>
                        <report_params>
                            <param name="P_CH_LIST" 
                                   value="123,456,789" 
                                   field_type="3" 
                                   multi="false" 
                                   prompt="" 
                                   range_kind="1" 
                                   value_type="11"/>
                        </report_params>
                    </main_report>
                </Report>
            ]]></XMLString>
        </getReport>
    </soap:Body>
</soap:Envelope>
```

### 9.4. Ответ getReport

```xml
<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
    <soap:Body>
        <getReportResponse xmlns="http://webservices.crystal.progcom/">
            <return>JVBERi0xLjQKJeLjz9MKMy... (Base64 encoded PDF)</return>
        </getReportResponse>
    </soap:Body>
</soap:Envelope>
```

## 10. Особенности реализации

### 10.1. Динамический URL

URL SOAP сервиса получается динамически из Oracle БД при инициализации, что позволяет изменять адрес сервиса без перекомпиляции.

### 10.2. Двухэтапный процесс

1. Сначала вызывается `getReportInfo` для получения структуры параметров отчета
2. Затем вызывается `getReport` с заполненными параметрами для генерации отчета

### 10.3. Base64 кодирование

Отчет возвращается в виде Base64 строки, которую необходимо декодировать перед использованием.

### 10.4. CDATA секции

XML запросы оборачиваются в CDATA секции для корректной передачи специальных символов.

## 11. Заключение

Данная реализация предоставляет полный функционал для работы с SOAP API Crystal Reports в Go проекте:

1. **Получение URL** из Oracle БД
2. **SOAP клиент** с поддержкой двух методов API
3. **Парсинг XML** запросов и ответов
4. **Обработка параметров** отчетов
5. **Декодирование Base64** результатов
6. **Обработка ошибок** включая SOAP Fault

Код готов к использованию и может быть адаптирован под конкретные требования проекта.

