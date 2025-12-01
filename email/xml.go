package email

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// ParsedEmailMessage представляет распарсенное email сообщение
type ParsedEmailMessage struct {
	TaskID         int64
	SmtpID         int
	EmailAddress   string
	Title          string
	Text           string
	Schedule       bool
	DateActiveFrom string
	Attachments    []Attachment
}

// Attachment представляет вложение
type Attachment struct {
	ReportType      int
	FileName        string
	ClobAttachID    *int64
	ReportFile      string
	Catalog         string
	File            string
	DbLogin         string
	DbPass          string
	AttachParams    map[string]string
}

// ParseEmailMessage парсит данные из map в ParsedEmailMessage
func ParseEmailMessage(data map[string]interface{}) (*ParsedEmailMessage, error) {
	msg := &ParsedEmailMessage{}

	// Парсим taskID
	if taskIDStr, ok := data["email_task_id"].(string); ok && taskIDStr != "" {
		taskID, err := strconv.ParseInt(strings.TrimSpace(taskIDStr), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("неверный формат email_task_id: %w", err)
		}
		msg.TaskID = taskID
	} else {
		return nil, fmt.Errorf("email_task_id не указан")
	}

	// Парсим smtpID
	if smtpIDStr, ok := data["smtp_id"].(string); ok && smtpIDStr != "" {
		smtpID, err := strconv.Atoi(strings.TrimSpace(smtpIDStr))
		if err == nil {
			msg.SmtpID = smtpID
		}
	}

	// Парсим email_address
	if emailAddress, ok := data["email_address"].(string); ok {
		msg.EmailAddress = strings.TrimSpace(emailAddress)
	} else {
		return nil, fmt.Errorf("email_address не указан")
	}

	// Парсим email_title
	if title, ok := data["email_title"].(string); ok {
		msg.Title = strings.TrimSpace(title)
	} else {
		return nil, fmt.Errorf("email_title не указан")
	}

	// Парсим email_text
	if text, ok := data["email_text"].(string); ok {
		msg.Text = strings.TrimSpace(text)
	} else {
		return nil, fmt.Errorf("email_text не указан")
	}

	// Парсим sending_schedule
	if scheduleStr, ok := data["sending_schedule"].(string); ok {
		msg.Schedule = scheduleStr == "1"
	}

	// Парсим date_active_from
	if dateActiveFrom, ok := data["date_active_from"].(string); ok {
		msg.DateActiveFrom = strings.TrimSpace(dateActiveFrom)
	}

	return msg, nil
}

// ParseAttachments парсит вложения из XML
// Вложения находятся внутри body элемента
func ParseAttachments(xmlPayload string, taskID int64) ([]Attachment, error) {
	type AttachElement struct {
		XMLName              xml.Name
		ReportType           string `xml:"report_type,attr"`
		EmailAttachID        string `xml:"email_attach_id,attr"`
		EmailAttachName      string `xml:"email_attach_name,attr"`
		ReportFile           string `xml:"report_file,attr"`
		EmailAttachCatalog   string `xml:"email_attach_catalog,attr"`
		EmailAttachFile      string `xml:"email_attach_file,attr"`
		DbLogin              string `xml:"db_login,attr"`
		DbPass               string `xml:"db_pass,attr"`
		InnerXML             string `xml:",innerxml"`
	}

	type Body struct {
		Attach []AttachElement `xml:"attach"`
	}

	type Root struct {
		XMLName xml.Name `xml:"root"`
		Body    Body     `xml:"body"`
	}

	var root Root
	if err := xml.Unmarshal([]byte(xmlPayload), &root); err != nil {
		return nil, fmt.Errorf("ошибка парсинга XML для вложений: %w", err)
	}

	var attachments []Attachment

	for _, attachElem := range root.Body.Attach {
		if attachElem.ReportType == "" {
			return nil, fmt.Errorf("не указан report_type вложения")
		}

		reportType, err := strconv.Atoi(attachElem.ReportType)
		if err != nil {
			return nil, fmt.Errorf("неверный формат report_type: %w", err)
		}

		attach := Attachment{
			ReportType: reportType,
			FileName:    attachElem.EmailAttachName,
		}

		switch reportType {
		case 2:
			// Тип 2: CLOB из БД
			if attachElem.EmailAttachID == "" {
				return nil, fmt.Errorf("не указан email_attach_id для типа 2")
			}
			clobID, err := strconv.ParseInt(attachElem.EmailAttachID, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("неверный формат email_attach_id: %w", err)
			}
			attach.ClobAttachID = &clobID
			attach.FileName = attachElem.EmailAttachName

		case 3:
			// Тип 3: Готовый файл
			if attachElem.ReportFile == "" {
				return nil, fmt.Errorf("не указан report_file для типа 3")
			}
			attach.ReportFile = attachElem.ReportFile
			attach.FileName = attachElem.EmailAttachName

		default:
			// Тип 1: Crystal Reports
			if attachElem.EmailAttachCatalog == "" || attachElem.EmailAttachFile == "" {
				return nil, fmt.Errorf("не указаны email_attach_catalog или email_attach_file для типа 1")
			}
			attach.Catalog = attachElem.EmailAttachCatalog
			attach.File = attachElem.EmailAttachFile
			attach.FileName = attachElem.EmailAttachName
			attach.DbLogin = attachElem.DbLogin
			attach.DbPass = attachElem.DbPass

			// Парсим параметры вложений
			if attachElem.InnerXML != "" {
				params, err := parseAttachParams(attachElem.InnerXML)
				if err != nil {
					return nil, fmt.Errorf("ошибка парсинга параметров вложений: %w", err)
				}
				attach.AttachParams = params
			}
		}

		attachments = append(attachments, attach)
	}

	return attachments, nil
}

// parseAttachParams парсит параметры вложений из XML
func parseAttachParams(xmlStr string) (map[string]string, error) {
	type AttachParam struct {
		XMLName              xml.Name
		EmailAttachParamName  string `xml:"email_attach_param_name,attr"`
		EmailAttachParamValue string `xml:"email_attach_param_value,attr"`
	}

	type Root struct {
		XMLName     xml.Name `xml:"attach"`
		AttachParam []AttachParam `xml:"attach_param"`
	}

	var root Root
	if err := xml.Unmarshal([]byte(xmlStr), &root); err != nil {
		return nil, fmt.Errorf("ошибка парсинга XML параметров: %w", err)
	}

	params := make(map[string]string)
	for _, param := range root.AttachParam {
		if param.EmailAttachParamName != "" {
			params[param.EmailAttachParamName] = param.EmailAttachParamValue
		}
	}

	return params, nil
}

