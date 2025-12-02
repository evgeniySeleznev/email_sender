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
	XMLName        xml.Name `xml:"Main"`
	ApplicationName string   `xml:"Application_Name,attr"`
	DBInstance     string   `xml:"DB_Instance,attr"`
	DBPass         string   `xml:"DB_Pass,attr"`
	DBUser         string   `xml:"DB_User,attr"`
	ExportFormat   int      `xml:"ExportFormat,attr"`
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
func NewCrystalReportsClient(baseURL string) *CrystalReportsClient {
	return &CrystalReportsClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		namespace: CrystalReportsNamespace,
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

