# Подробное объяснение работы обработки Crystal Reports

## Обзор процесса

Обработка Crystal Reports вложений (тип 1) использует SOAP веб-сервис для генерации отчетов. Процесс состоит из двух основных этапов:

1. **getReportInfo** - получение информации об отчете и его параметрах
2. **getReport** - генерация отчета с примененными параметрами (возвращает Base64 строку)

---

## Полный путь обработки Crystal Reports

### Этап 1: Точка входа - обработка вложения типа 1

```46:163:email/attachments.go
// processCrystalReport обрабатывает Crystal Reports вложение через Web Service
func (p *AttachmentProcessor) processCrystalReport(ctx context.Context, attach *Attachment, taskID int64) (*AttachmentData, error) {
	// Получаем URL Web Service из БД
	url, err := p.dbConn.GetWebServiceUrl()
	if err != nil {
		return nil, fmt.Errorf("ошибка получения URL Web Service: %w", err)
	}

	// Получаем DBInstance из конфигурации
	cfg := p.dbConn.GetConfig()
	if cfg == nil {
		return nil, fmt.Errorf("конфигурация не загружена")
	}

	dbInstance := cfg.Oracle.Instance
	if dbInstance == "" {
		dbInstance = cfg.Oracle.DSN
	}
	if dbInstance == "" {
		return nil, fmt.Errorf("не указан DBInstance в конфигурации")
	}

	if logger.Log != nil {
		logger.Log.Debug("Обработка Crystal Reports вложения",
			zap.Int64("taskID", taskID),
			zap.String("catalog", attach.Catalog),
			zap.String("file", attach.File),
			zap.String("url", url),
			zap.String("dbInstance", dbInstance))
	}

	// Создаем SOAP клиент
	client := NewCrystalReportsClient(url)

	// Шаг 1: Создаем запрос для getReportInfo
	reportRequest := &ReportRequest{
		Main: MainInfo{
			ApplicationName: attach.Catalog,
			DBInstance:     dbInstance,
			DBPass:         attach.DbPass,
			DBUser:         attach.DbLogin,
			ExportFormat:   ExportFormatPDF,
			ReportName:     attach.File,
		},
	}

	// Шаг 2: Получаем информацию об отчете
	reportInfo, err := client.GetReportInfo(ctx, reportRequest)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения информации об отчете: %w", err)
	}

	if logger.Log != nil {
		logger.Log.Debug("Получена информация об отчете",
			zap.Int64("taskID", taskID),
			zap.Int("paramsCount", len(reportInfo.Params)))
	}

	// Шаг 3: Создаем запрос с параметрами для getReport
	reportWithParams := &ReportWithParams{
		Main: reportRequest.Main,
	}

	// Применяем параметры отчета
	if len(attach.AttachParams) > 0 && len(reportInfo.Params) > 0 {
		params := make([]Param, 0, len(reportInfo.Params))

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
			reportWithParams.MainReport = &MainReport{
				ReportParams: ReportParams{
					Params: params,
				},
			}
		}
	}

	// Шаг 4: Генерируем отчет
	base64Data, err := client.GetReport(ctx, reportWithParams)
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

	if logger.Log != nil {
		logger.Log.Debug("Crystal Reports отчет успешно сгенерирован",
			zap.Int64("taskID", taskID),
			zap.Int("size", len(data)))
	}

	return &AttachmentData{
		FileName: attach.FileName,
		Data:     data,
	}, nil
}
```

**Строка 49**: Получение URL SOAP сервиса из Oracle БД через `GetWebServiceUrl()` (вызывает `pcsystem.PKG_EMAIL.GET_SOAP_ADDRESS()`).

**Строка 55-66**: Получение `DBInstance` из конфигурации (используется для подключения к БД при генерации отчета).

**Строка 78**: Создание SOAP клиента с полученным URL.

**Строка 81-90**: Формирование запроса `ReportRequest` с основной информацией:
- `ApplicationName` - каталог отчета (из XML атрибута `email_attach_catalog`)
- `DBInstance` - экземпляр БД из конфигурации
- `DBPass` - пароль БД (из XML атрибута `db_pass`)
- `DBUser` - пользователь БД (из XML атрибута `db_login`)
- `ExportFormat` - формат экспорта (5 = PDF)
- `ReportName` - имя файла отчета (из XML атрибута `email_attach_file`)

**Строка 93**: Вызов `GetReportInfo` для получения информации об отчете и его параметрах.

**Строка 105-135**: Формирование запроса `ReportWithParams` с параметрами:
- Берется структура параметров из `reportInfo.Params`
- Значения параметров обновляются из `attach.AttachParams` (параметры из XML)
- Если параметр есть в `attach.AttachParams`, его значение подставляется

**Строка 138**: Вызов `GetReport` для генерации отчета с параметрами.

**Строка 144**: Декодирование Base64 строки в бинарные данные.

**Строка 159-162**: Возврат `AttachmentData` с именем файла и бинарными данными отчета.

---

## Этап 2: Получение URL Web Service из БД

### Файл: `db/procedures.go`, функция `GetWebServiceUrl`

```276:331:db/procedures.go
// GetWebServiceUrl получает адрес Crystal Reports через pcsystem.PKG_EMAIL.GET_SOAP_ADDRESS()
func (d *DBConnection) GetWebServiceUrl() (string, error) {
	if !d.CheckConnection() {
		return "", fmt.Errorf("соединение с БД недоступно")
	}

	queryCtx, queryCancel := context.WithTimeout(context.Background(), QueryTimeout)
	defer queryCancel()

	var url sql.NullString

	err := d.WithDBTx(queryCtx, func(tx *sql.Tx) error {
		if err := d.ensureWebServiceUrlPackageExistsTx(tx, queryCtx); err != nil {
			return fmt.Errorf("ошибка создания пакета: %w", err)
		}

		plsql := `
			BEGIN
				temp_webservice_url_pkg.g_url := pcsystem.PKG_EMAIL.GET_SOAP_ADDRESS();
			END;
		`

		_, err := tx.ExecContext(queryCtx, plsql)
		if err != nil {
			if logger.Log != nil {
				logger.Log.Error("Ошибка выполнения PL/SQL для pcsystem.PKG_EMAIL.GET_SOAP_ADDRESS()", zap.Error(err))
			}
			return fmt.Errorf("ошибка выполнения PL/SQL: %w", err)
		}

		query := "SELECT temp_webservice_url_pkg.get_url() FROM DUAL"
		err = tx.QueryRowContext(queryCtx, query).Scan(&url)
		if err != nil {
			if logger.Log != nil {
				logger.Log.Error("Ошибка выполнения SELECT для temp_webservice_url_pkg.get_url()", zap.Error(err))
			}
			return fmt.Errorf("ошибка получения URL: %w", err)
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	if !url.Valid || url.String == "" {
		return "", fmt.Errorf("ошибка получения URL: URL пуст")
	}

	if logger.Log != nil {
		logger.Log.Debug("pcsystem.PKG_EMAIL.GET_SOAP_ADDRESS() result",
			zap.String("url", url.String))
	}
	return url.String, nil
}
```

**Процесс аналогичен `GetEmailReportClob`**:
1. Создается временный Oracle пакет `temp_webservice_url_pkg`
2. Вызывается `pcsystem.PKG_EMAIL.GET_SOAP_ADDRESS()` и результат сохраняется в пакет
3. URL извлекается через `SELECT temp_webservice_url_pkg.get_url() FROM DUAL`

---

## Этап 3: SOAP клиент - создание и настройка

### Файл: `email/crystal_reports.go`

```83:99:email/crystal_reports.go
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
		namespace: CrystalReportsNamespace,
	}
}
```

**Строка 84-88**: Структура клиента:
- `baseURL` - базовый URL SOAP сервиса
- `httpClient` - HTTP клиент с таймаутом 60 секунд
- `namespace` - SOAP namespace (`http://webservices.crystal.progcom/`)

**Строка 93**: Удаление завершающего слеша из URL (если есть).

---

## Этап 4: Получение информации об отчете (getReportInfo)

### Файл: `email/crystal_reports.go`, функция `GetReportInfo`

```202:234:email/crystal_reports.go
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

**Строка 205-213**: Сериализация структуры `ReportRequest` в XML строку.

**Строка 216**: Выполнение SOAP запроса через `callSOAP` с действием `"getReportInfo"`.

**Строка 222**: Извлечение XML из SOAP ответа через `parseSOAPResponse`.

**Строка 228-231**: Десериализация XML в структуру `ReportInfoResponse`, которая содержит список параметров отчета.

**Результат**: Структура `ReportInfoResponse` с массивом `Params`, где каждый параметр содержит:
- `Name` - имя параметра
- `Value` - значение по умолчанию
- `FieldType` - тип поля
- `Multi` - множественный ли параметр
- `Prompt` - подсказка для пользователя
- `RangeKind` - тип диапазона
- `ValueType` - тип значения

---

## Этап 5: Формирование SOAP запроса

### Файл: `email/crystal_reports.go`, функция `buildSOAPEnvelope`

```101:113:email/crystal_reports.go
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
```

**Строка 103-110**: Формирование SOAP конверта:
- `action` - имя SOAP действия (`getReportInfo` или `getReport`)
- `bodyXML` - XML тело запроса (сериализованный `ReportRequest` или `ReportWithParams`)
- XML оборачивается в CDATA секцию внутри `<XMLString>`

**Пример SOAP запроса для getReportInfo:**
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

---

## Этап 6: Выполнение SOAP запроса

### Файл: `email/crystal_reports.go`, функция `callSOAP`

```127:165:email/crystal_reports.go
// callSOAP выполняет SOAP запрос с обработкой ошибок
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

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("ошибка чтения ответа: %w", err)
	}

	bodyStr := string(bodyBytes)

	// Проверяем на SOAP Fault
	if strings.Contains(bodyStr, "soap:Fault") || strings.Contains(bodyStr, "<Fault>") {
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

**Строка 129**: Построение SOAP конверта.

**Строка 131**: Создание HTTP POST запроса с контекстом (поддержка отмены).

**Строка 136-137**: Установка заголовков:
- `Content-Type: text/xml; charset=utf-8` - тип контента для SOAP
- `SOAPAction: ""` - пустой SOAPAction (для Document/Literal стиля)

**Строка 139**: Выполнение HTTP запроса.

**Строка 145**: Чтение тела ответа.

**Строка 153-158**: Проверка на SOAP Fault (ошибки SOAP сервиса).

**Строка 160-162**: Проверка HTTP статус кода (должен быть 200 OK).

**Строка 164**: Возврат SOAP ответа как строки.

---

## Этап 7: Парсинг SOAP ответа

### Файл: `email/crystal_reports.go`, функция `parseSOAPResponse`

```167:200:email/crystal_reports.go
// parseSOAPResponse извлекает содержимое из SOAP ответа
func (c *CrystalReportsClient) parseSOAPResponse(soapResponse, action string) (string, error) {
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

**Строка 169**: Создание XML декодера для парсинга SOAP ответа.

**Строка 171-172**: Флаги для отслеживания, находимся ли мы внутри элемента `<return>`.

**Строка 174-197**: Итерация по токенам XML:
- При входе в `<return>` устанавливается `inReturn = true`
- При выходе из `<return>` устанавливается `inReturn = false`
- Текст внутри `<return>` записывается в результат

**Строка 199**: Возврат извлеченного содержимого (XML для `getReportInfo` или Base64 строка для `getReport`).

**Пример SOAP ответа для getReportInfo:**
```xml
<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
    <soap:Body>
        <getReportInfoResponse xmlns="http://webservices.crystal.progcom/">
            <return>
                <ReportInfo>
                    <param name="P_CH_LIST" value="" field_type="3" .../>
                    <param name="P_DATE_FROM" value="" field_type="1" .../>
                </ReportInfo>
            </return>
        </getReportInfoResponse>
    </soap:Body>
</soap:Envelope>
```

---

## Этап 8: Генерация отчета (getReport)

### Файл: `email/crystal_reports.go`, функция `GetReport`

```236:262:email/crystal_reports.go
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

**Строка 239-247**: Сериализация `ReportWithParams` в XML (включая параметры отчета).

**Строка 250**: Выполнение SOAP запроса с действием `"getReport"`.

**Строка 256**: Извлечение Base64 строки из SOAP ответа.

**Строка 261**: Возврат Base64 строки (без пробелов).

**Пример SOAP запроса для getReport:**
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

**Пример SOAP ответа для getReport:**
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

---

## Этап 9: Декодирование Base64 и возврат результата

Вернемся к `processCrystalReport`:

```143:162:email/attachments.go
	// Шаг 5: Декодируем из Base64
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, fmt.Errorf("ошибка декодирования Base64: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("пустой отчет")
	}

	if logger.Log != nil {
		logger.Log.Debug("Crystal Reports отчет успешно сгенерирован",
			zap.Int64("taskID", taskID),
			zap.Int("size", len(data)))
	}

	return &AttachmentData{
		FileName: attach.FileName,
		Data:     data,
	}, nil
```

**Строка 144**: Декодирование Base64 строки в бинарные данные (`[]byte`).

**Строка 149-151**: Проверка, что отчет не пустой.

**Строка 159-162**: Возврат `AttachmentData` с именем файла и бинарными данными PDF отчета.

---

## Схема полного процесса

```
1. processCrystalReport вызывается с attach (тип 1)
   ↓
2. GetWebServiceUrl() → получение URL из Oracle БД
   ├─ pcsystem.PKG_EMAIL.GET_SOAP_ADDRESS()
   └─ Возврат URL (например, "http://server:8080/CrystalReports/Service.asmx")
   ↓
3. NewCrystalReportsClient(url) → создание SOAP клиента
   ↓
4. Формирование ReportRequest:
   ├─ ApplicationName (из attach.Catalog)
   ├─ DBInstance (из конфигурации)
   ├─ DBUser, DBPass (из attach)
   ├─ ExportFormat = 5 (PDF)
   └─ ReportName (из attach.File)
   ↓
5. GetReportInfo(ctx, reportRequest) → получение информации об отчете
   ├─ Сериализация ReportRequest в XML
   ├─ buildSOAPEnvelope("getReportInfo", xml)
   ├─ callSOAP() → HTTP POST запрос
   ├─ parseSOAPResponse() → извлечение XML из ответа
   └─ Десериализация в ReportInfoResponse (список параметров)
   ↓
6. Формирование ReportWithParams:
   ├─ Копирование Main из ReportRequest
   ├─ Применение параметров из attach.AttachParams
   └─ Обновление значений параметров из reportInfo.Params
   ↓
7. GetReport(ctx, reportWithParams) → генерация отчета
   ├─ Сериализация ReportWithParams в XML
   ├─ buildSOAPEnvelope("getReport", xml)
   ├─ callSOAP() → HTTP POST запрос
   ├─ parseSOAPResponse() → извлечение Base64 строки
   └─ Возврат Base64 строки
   ↓
8. base64.StdEncoding.DecodeString(base64Data)
   → Декодирование в []byte (PDF данные)
   ↓
9. Возврат AttachmentData{FileName, Data: []byte}
   → Готово для отправки в email
```

---

## Важные детали реализации

1. **Двухэтапный процесс**: Сначала получаем информацию об отчете (`getReportInfo`), затем генерируем отчет с параметрами (`getReport`).

2. **Динамический URL**: URL SOAP сервиса получается из Oracle БД, что позволяет изменять адрес без перекомпиляции.

3. **Параметры отчета**: Параметры из XML (`attach.AttachParams`) применяются к структуре параметров из `getReportInfo`, что обеспечивает правильные типы и форматы.

4. **SOAP формат**: Используется SOAP 1.1, Document/Literal стиль с CDATA секцией для XML тела.

5. **Обработка ошибок**: Проверка SOAP Fault и HTTP статус кодов для корректной обработки ошибок сервиса.

6. **Таймауты**: HTTP клиент имеет таймаут 60 секунд для генерации отчетов.

7. **Base64 кодирование**: Crystal Reports возвращает отчет в Base64, который декодируется перед отправкой в email.

8. **Формат экспорта**: Всегда используется PDF (ExportFormat = 5).

---

## Структуры данных

### ReportRequest (для getReportInfo)
```go
type ReportRequest struct {
    XMLName xml.Name `xml:"Report"`
    Main    MainInfo `xml:"Main"`
}

type MainInfo struct {
    ApplicationName string `xml:"Application_Name,attr"`
    DBInstance     string `xml:"DB_Instance,attr"`
    DBPass         string `xml:"DB_Pass,attr"`
    DBUser         string `xml:"DB_User,attr"`
    ExportFormat   int    `xml:"ExportFormat,attr"`
    ReportName     string `xml:"Report_Name,attr"`
}
```

### ReportWithParams (для getReport)
```go
type ReportWithParams struct {
    XMLName    xml.Name    `xml:"Report"`
    Main       MainInfo    `xml:"Main"`
    MainReport *MainReport `xml:"main_report,omitempty"`
}

type MainReport struct {
    ReportParams ReportParams `xml:"report_params"`
}

type ReportParams struct {
    Params []Param `xml:"param"`
}

type Param struct {
    Name      string `xml:"name,attr"`
    Value     string `xml:"value,attr"`
    FieldType int    `xml:"field_type,attr"`
    Multi     bool   `xml:"multi,attr"`
    Prompt    string `xml:"prompt,attr"`
    RangeKind string `xml:"range_kind,attr"`
    ValueType int    `xml:"value_type,attr"`
}
```

---

## Итог

Обработка Crystal Reports полностью реализована и работает следующим образом:

1. Получает URL SOAP сервиса из Oracle БД
2. Создает SOAP клиент
3. Получает информацию об отчете и его параметрах через `getReportInfo`
4. Применяет параметры из XML к структуре параметров отчета
5. Генерирует отчет через `getReport` с примененными параметрами
6. Декодирует Base64 ответ в бинарные данные PDF
7. Возвращает готовое вложение для отправки в email

Все этапы имеют обработку ошибок и логирование для отладки.

