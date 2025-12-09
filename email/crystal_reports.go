package email

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"email-service/logger"

	"go.uber.org/zap"
)

const (
	// CrystalReportsNamespace namespace для SOAP API Crystal Reports
	CrystalReportsNamespace = "http://webservices.crystal.progcom/"
	// ExportFormatPDF формат экспорта PDF
	ExportFormatPDF = 5
)

// ReportRequest представляет XML запрос для Crystal Reports getReportInfo
type ReportRequest struct {
	XMLName xml.Name `xml:"Report"`
	Main    MainInfo `xml:"Main"`
}

// MainInfo содержит основную информацию об отчете
type MainInfo struct {
	XMLName         xml.Name `xml:"Main"`
	ApplicationName string   `xml:"Application_Name,attr"`
	DBInstance      string   `xml:"DB_Instance,attr"`
	DBPass          string   `xml:"DB_Pass,attr"`
	DBUser          string   `xml:"DB_User,attr"`
	ExportFormat    int      `xml:"ExportFormat,attr"`
	ReportName      string   `xml:"Report_Name,attr"`
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
	XMLName    xml.Name    `xml:"Report"`
	Main       MainInfo    `xml:"Main"`
	MainReport *MainReport `xml:"main_report,omitempty"`
}

// MainReport содержит параметры отчета
type MainReport struct {
	XMLName      xml.Name     `xml:"main_report"`
	ReportParams ReportParams `xml:"report_params"`
}

// ReportParams содержит список параметров
type ReportParams struct {
	XMLName xml.Name `xml:"report_params"`
	Params  []Param  `xml:"param"`
}

// SOAPFault представляет SOAP ошибку
type SOAPFault struct {
	XMLName xml.Name `xml:"Fault"`
	Code    string   `xml:"faultcode"`
	String  string   `xml:"faultstring"`
	Detail  string   `xml:"detail"`
}

// CrystalReportsClient клиент для работы с SOAP API Crystal Reports
type CrystalReportsClient struct {
	baseURL    string
	httpClient *http.Client
	namespace  string
}

// NewCrystalReportsClient создает новый клиент
func NewCrystalReportsClient(baseURL string, timeout time.Duration) *CrystalReportsClient {
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &CrystalReportsClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
		namespace: CrystalReportsNamespace,
	}
}

// buildSOAPEnvelope создает SOAP конверт
func (c *CrystalReportsClient) buildSOAPEnvelope(action string, bodyXML string) string {
	// Используем префикс ns для action, чтобы вложенные элементы (XMLString) были без namespace (unqualified)
	// Это соответствует поведению C# клиента и требованиям сервера
	envelope := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:ns="%s">
    <soap:Body>
        <ns:%s>
            <XMLString><![CDATA[%s]]></XMLString>
        </ns:%s>
    </soap:Body>
</soap:Envelope>`, c.namespace, action, bodyXML, action)

	return envelope
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

// callSOAP выполняет SOAP запрос с обработкой ошибок
func (c *CrystalReportsClient) callSOAP(ctx context.Context, action string, bodyXML string) (string, error) {
	soapEnvelope := c.buildSOAPEnvelope(action, bodyXML)

	// Логируем SOAP запрос для отладки (только для getReportInfo, чтобы не засорять логи)
	if logger.Log != nil && action == "getReportInfo" {
		// Маскируем пароли в логах
		soapForLog := soapEnvelope
		// Ищем и маскируем DB_Pass в XML
		if idx := strings.Index(soapForLog, `DB_Pass="`); idx != -1 {
			start := idx + len(`DB_Pass="`)
			if endIdx := strings.Index(soapForLog[start:], `"`); endIdx != -1 {
				soapForLog = soapForLog[:start] + "***" + soapForLog[start+endIdx:]
			}
		}
		logger.Log.Debug("SOAP запрос",
			zap.String("action", action),
			zap.String("url", c.baseURL),
			zap.String("soapEnvelope", truncateString(soapForLog, 2000)))
	}

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

	// Проверяем на SOAP Fault (различные варианты написания)
	if strings.Contains(bodyStr, "soap:Fault") ||
		strings.Contains(bodyStr, "<Fault>") ||
		strings.Contains(bodyStr, "faultcode") ||
		strings.Contains(bodyStr, "faultstring") ||
		strings.Contains(bodyStr, "Fault") {
		// Логируем полный ответ для диагностики
		if logger.Log != nil {
			logger.Log.Error("SOAP Fault обнаружен",
				zap.String("response", truncateString(bodyStr, 3000)))
		}

		// Пытаемся извлечь faultstring различными способами
		var faultStr string

		// Вариант 1: <faultstring>...</faultstring> или <ns0:faultstring>...</ns0:faultstring>
		patterns := []string{
			"<faultstring>",
			"<ns0:faultstring>",
			"<ns1:faultstring>",
			"faultstring>",
		}

		for _, pattern := range patterns {
			startIdx := strings.Index(bodyStr, pattern)
			if startIdx != -1 {
				startIdx += len(pattern)
				// Ищем закрывающий тег
				endIdx := strings.Index(bodyStr[startIdx:], "<")
				if endIdx != -1 {
					faultStr = strings.TrimSpace(bodyStr[startIdx : startIdx+endIdx])
					// Убираем возможные префиксы типа ">"
					faultStr = strings.TrimPrefix(faultStr, ">")
					faultStr = strings.TrimSpace(faultStr)
					if faultStr != "" {
						break
					}
				}
			}
		}

		// Если не удалось извлечь через теги, пытаемся найти по ключевым словам
		if faultStr == "" {
			// Простой поиск по ключевым словам
			if idx := strings.Index(bodyStr, "NullPointerException"); idx != -1 {
				// Извлекаем контекст вокруг ошибки
				start := idx - 50
				if start < 0 {
					start = 0
				}
				end := idx + 100
				if end > len(bodyStr) {
					end = len(bodyStr)
				}
				context := bodyStr[start:end]
				// Извлекаем только саму ошибку
				if npeIdx := strings.Index(context, "NullPointerException"); npeIdx != -1 {
					faultStr = strings.TrimSpace(context[npeIdx:])
					// Обрезаем до первого < или конца строки
					if endIdx := strings.Index(faultStr, "<"); endIdx != -1 {
						faultStr = faultStr[:endIdx]
					}
					if endIdx := strings.Index(faultStr, "\n"); endIdx != -1 {
						faultStr = faultStr[:endIdx]
					}
				}
			}
		}

		if faultStr != "" {
			return "", fmt.Errorf("SOAP Fault: %s", faultStr)
		}

		// Если не удалось извлечь, возвращаем общую ошибку
		return "", fmt.Errorf("SOAP Fault (детали не извлечены, проверьте логи)")
	}

	if resp.StatusCode != http.StatusOK {
		// Для HTTP 500 с SOAP Fault уже обработано выше
		if resp.StatusCode == http.StatusInternalServerError &&
			(strings.Contains(bodyStr, "Fault") || strings.Contains(bodyStr, "faultstring")) {
			// Пытаемся извлечь информацию об ошибке
			if strings.Contains(bodyStr, "NullPointerException") {
				return "", fmt.Errorf("HTTP ошибка 500: NullPointerException на сервере Crystal Reports (возможно, не указаны обязательные параметры DBUser/DBPass)")
			}
		}
		return "", fmt.Errorf("HTTP ошибка %d: %s", resp.StatusCode, bodyStr)
	}

	return bodyStr, nil
}

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

// GetReportInfo получает информацию об отчете
func (c *CrystalReportsClient) GetReportInfo(ctx context.Context, req *ReportRequest) (*ReportWithParams, error) {
	// Сериализуем запрос в XML
	var buf bytes.Buffer
	encoder := xml.NewEncoder(&buf)
	encoder.Indent("", "")

	if err := encoder.Encode(req); err != nil {
		return nil, fmt.Errorf("ошибка сериализации запроса: %w", err)
	}

	requestXML := buf.String()

	// Валидация обязательных полей перед отправкой
	if req.Main.ApplicationName == "" {
		return nil, fmt.Errorf("ApplicationName не может быть пустым")
	}
	if req.Main.ReportName == "" {
		return nil, fmt.Errorf("ReportName не может быть пустым")
	}
	if req.Main.DBInstance == "" {
		return nil, fmt.Errorf("DBInstance не может быть пустым")
	}
	if req.Main.DBUser == "" {
		return nil, fmt.Errorf("DBUser не может быть пустым")
	}
	if req.Main.DBPass == "" {
		return nil, fmt.Errorf("DBPass не может быть пустым")
	}

	// Логируем XML запрос для отладки (без пароля)
	if logger.Log != nil {
		requestXMLForLog := strings.ReplaceAll(requestXML, req.Main.DBPass, "***")
		logger.Log.Debug("XML запрос getReportInfo",
			zap.String("xml", requestXMLForLog),
			zap.String("applicationName", req.Main.ApplicationName),
			zap.String("reportName", req.Main.ReportName),
			zap.String("dbInstance", req.Main.DBInstance),
			zap.String("dbUser", req.Main.DBUser))
	}

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
	// Ответ приходит в формате <Report>...</Report>, а не <ReportInfo>
	var reportInfo ReportWithParams
	if err := xml.Unmarshal([]byte(resultXML), &reportInfo); err != nil {
		return nil, fmt.Errorf("ошибка десериализации ответа: %w", err)
	}

	return &reportInfo, nil
}

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
