package email

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-message"
	"go.uber.org/zap"

	"email-service/logger"
)

const (
	EnvelopePrefix = "askemailsender"
)

// MIMEMessage представляет распарсенное MIME сообщение
type MIMEMessage struct {
	Date  *time.Time
	Body  []byte
	Parts []*MIMEPart
}

// MIMEPart представляет часть MIME сообщения
type MIMEPart struct {
	ContentType string
	Body        []byte
}

// ParseMIMEMessage парсит MIME сообщение из байтов
func ParseMIMEMessage(data []byte) (*MIMEMessage, error) {
	msg := &MIMEMessage{}

	// Парсим MIME сообщение
	m, err := message.Read(strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения MIME сообщения: %w", err)
	}

	// Получаем дату из заголовков
	if dateStr := m.Header.Get("Date"); dateStr != "" {
		// Парсим дату в формате RFC 5322
		if date, err := parseRFC5322Date(dateStr); err == nil {
			msg.Date = &date
		}
	}

	// Парсим тело сообщения
	mediaType, _, err := m.Header.ContentType()
	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга Content-Type: %w", err)
	}

	// Проверяем, является ли это multipart сообщением
	if strings.HasPrefix(mediaType, "multipart/") {
		mr := m.MultipartReader()
		if mr != nil {
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}

				partMediaType, _, err := p.Header.ContentType()
				if err != nil {
					continue
				}

				partBody, err := io.ReadAll(p.Body)
				if err != nil {
					continue
				}

				msg.Parts = append(msg.Parts, &MIMEPart{
					ContentType: partMediaType,
					Body:        partBody,
				})
			}
		}
	} else {
		// Простое сообщение
		body, err := io.ReadAll(m.Body)
		if err != nil {
			return nil, fmt.Errorf("ошибка чтения тела сообщения: %w", err)
		}
		msg.Body = body
	}

	return msg, nil
}

// parseRFC5322Date парсит дату в формате RFC 5322
func parseRFC5322Date(dateStr string) (time.Time, error) {
	// Пробуем разные форматы даты
	formats := []string{
		time.RFC1123Z, // Mon, 02 Jan 2006 15:04:05 -0700
		time.RFC1123,  // Mon, 02 Jan 2006 15:04:05 MST
		time.RFC822Z,  // 02 Jan 06 15:04 -0700
		time.RFC822,   // 02 Jan 06 15:04 MST
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05 MST",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("не удалось распарсить дату: %s", dateStr)
}

// ProcessDeliveryStatusNotification обрабатывает DSN (Delivery Status Notification) сообщение
// Возвращает taskID, status (2=Sended, 3=Failed, 4=Delivered), и описание статуса
func ProcessDeliveryStatusNotification(sourceEmail string, msg *MIMEMessage) (taskID int64, status int, statusDesc string) {
	if msg == nil {
		return 0, 0, ""
	}

	// Ищем multipart/report с report-type="delivery-status"
	for i, part := range msg.Parts {
		if part.ContentType == "message/delivery-status" || strings.Contains(part.ContentType, "delivery-status") {
			if logger.Log != nil {
				logger.Log.Debug("Найдена DSN часть",
					zap.Int("partIndex", i),
					zap.String("contentType", part.ContentType),
					zap.Int("bodySize", len(part.Body)))
			}
			return parseDSNPart(sourceEmail, part.Body)
		}
	}

	// Если не нашли в частях, проверяем основное тело
	if len(msg.Body) > 0 {
		// Пробуем найти DSN в теле сообщения
		bodyStr := string(msg.Body)
		if strings.Contains(bodyStr, "delivery-status") || strings.Contains(bodyStr, "Original-Envelope-Id") || strings.Contains(bodyStr, "X-Envelope-ID") {
			if logger.Log != nil {
				logger.Log.Debug("Найден DSN в основном теле сообщения",
					zap.Int("bodySize", len(msg.Body)))
			}
			return parseDSNBody(sourceEmail, msg.Body)
		}
	}

	return 0, 0, ""
}

// parseDSNPart парсит DSN из части сообщения
func parseDSNPart(sourceEmail string, body []byte) (taskID int64, status int, statusDesc string) {
	return parseDSNBody(sourceEmail, body)
}

// parseDSNBody парсит DSN из тела сообщения
func parseDSNBody(sourceEmail string, body []byte) (taskID int64, status int, statusDesc string) {
	bodyStr := string(body)

	// Ищем Original-Envelope-Id или X-Envelope-ID
	envelopeID := extractHeaderValue(bodyStr, "Original-Envelope-Id")
	if envelopeID == "" {
		envelopeID = extractHeaderValue(bodyStr, "X-Envelope-ID")
	}
	// Также пробуем найти в заголовках сообщения (не только в DSN части)
	if envelopeID == "" {
		// Ищем в начале тела (может быть в заголовках)
		lines := strings.Split(bodyStr, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(line), "x-envelope-id:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					envelopeID = strings.TrimSpace(parts[1])
					break
				}
			}
			if strings.HasPrefix(strings.ToLower(line), "original-envelope-id:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					envelopeID = strings.TrimSpace(parts[1])
					break
				}
			}
		}
	}
	if envelopeID == "" {
		if logger.Log != nil {
			bodyPreview := bodyStr
			if len(bodyStr) > 500 {
				bodyPreview = bodyStr[:500] + "..."
			}
			logger.Log.Info("DSN сообщение не содержит Original-Envelope-Id или X-Envelope-ID",
				zap.String("bodyPreview", bodyPreview),
				zap.Int("bodyLength", len(bodyStr)))
		}
		return 0, 0, ""
	}

	if logger.Log != nil {
		logger.Log.Debug("Найден envelope ID в DSN",
			zap.String("envelopeID", envelopeID))
	}

	// Проверяем, что это наш envelope ID
	if !strings.HasPrefix(envelopeID, EnvelopePrefix) {
		if logger.Log != nil {
			logger.Log.Debug("Envelope ID не соответствует нашему префиксу",
				zap.String("envelopeID", envelopeID),
				zap.String("expectedPrefix", EnvelopePrefix))
		}
		return 0, 0, ""
	}

	// Извлекаем taskID из envelope ID
	taskIDStr := strings.TrimPrefix(envelopeID, EnvelopePrefix)
	taskID, err := strconv.ParseInt(taskIDStr, 10, 64)
	if err != nil {
		if logger.Log != nil {
			logger.Log.Debug("Ошибка парсинга taskID из envelope ID",
				zap.String("envelopeID", envelopeID),
				zap.Error(err))
		}
		return 0, 0, ""
	}

	// Ищем Action в статусных группах
	// DSN формат: несколько статусных групп, каждая содержит Action и другие поля
	action := extractHeaderValue(bodyStr, "Action")
	if action == "" {
		// Пробуем найти в других статусных группах
		lines := strings.Split(bodyStr, "\n")
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "Action:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					action = strings.TrimSpace(parts[1])
					break
				}
			}
		}
	}

	// Ищем Final-Recipient или Original-Recipient
	recipient := extractHeaderValue(bodyStr, "Final-Recipient")
	if recipient == "" {
		recipient = extractHeaderValue(bodyStr, "Original-Recipient")
	}

	// Извлекаем email адрес из recipient (формат: "rfc822;user@domain.com")
	emailAddr := ""
	if recipient != "" {
		idx := strings.Index(recipient, ";")
		if idx >= 0 {
			emailAddr = strings.TrimSpace(recipient[idx+1:])
		}
	}

	// Пропускаем уведомления отправителю
	if emailAddr != "" && strings.EqualFold(emailAddr, sourceEmail) {
		return 0, 0, ""
	}

	// Ищем Diagnostic-Code
	diagnosticCode := extractHeaderValue(bodyStr, "Diagnostic-Code")

	// Определяем статус по Action
	action = strings.ToLower(strings.TrimSpace(action))
	if logger.Log != nil {
		logger.Log.Debug("Обработка DSN Action",
			zap.String("action", action),
			zap.String("emailAddr", emailAddr),
			zap.Int64("taskID", taskID))
	}

	switch action {
	case "failed":
		status = 3 // Failed
		statusDesc = fmt.Sprintf("Ошибка доставки для %s", emailAddr)
	case "delayed":
		status = 0 // Не обрабатываем delayed
		statusDesc = fmt.Sprintf("Задержка доставки %s", emailAddr)
		if logger.Log != nil {
			logger.Log.Debug("Пропускаем delayed статус")
		}
		return 0, 0, "" // Не возвращаем статус для delayed
	case "delivered":
		status = 4 // Delivered
		statusDesc = fmt.Sprintf("Доставлено для %s", emailAddr)
	case "relayed":
		status = 2 // Sended (релей обычно означает успешную передачу следующему серверу)
		statusDesc = fmt.Sprintf("Релей для %s", emailAddr)
	case "expanded":
		status = 4 // Delivered
		statusDesc = fmt.Sprintf("Доставлено для %s и релей для остальных", emailAddr)
	default:
		if logger.Log != nil {
			logger.Log.Debug("Неизвестный Action в DSN",
				zap.String("action", action),
				zap.Int64("taskID", taskID))
		}
		return 0, 0, ""
	}

	// Добавляем Diagnostic-Code если есть
	if diagnosticCode != "" {
		statusDesc += ", " + diagnosticCode
	}

	return taskID, status, statusDesc
}

// extractHeaderValue извлекает значение заголовка из тела сообщения
func extractHeaderValue(body, headerName string) string {
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, headerName+":") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}
