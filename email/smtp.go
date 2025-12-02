package email

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net/smtp"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"email-service/logger"
	"email-service/settings"
)

// SMTPClient представляет SMTP клиент для отправки email
type SMTPClient struct {
	cfg           *settings.SMTPConfig
	lastSendTime  time.Time
	lastEmailTime map[string]time.Time // Ключ - email адрес
	mu            sync.Mutex
}

// NewSMTPClient создает новый SMTP клиент
func NewSMTPClient(cfg *settings.SMTPConfig) *SMTPClient {
	return &SMTPClient{
		cfg:           cfg,
		lastEmailTime: make(map[string]time.Time),
	}
}

// SendEmail отправляет email через SMTP
func (c *SMTPClient) SendEmail(ctx context.Context, msg *EmailMessage, testEmail string, isBodyHTML bool, sendHiddenCopyToSelf bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Проверяем ограничение частоты отправки
	now := time.Now()
	if c.cfg.MinSendIntervalMsec > 0 {
		waitCount := 0
		for c.lastSendTime.After(now) && waitCount < 100 {
			time.Sleep(50 * time.Millisecond)
			now = time.Now()
			waitCount++
		}
	}

	// Определяем адреса получателей (тестовый режим или оригинальные)
	recipientEmails := c.parseEmailAddresses(msg.EmailAddress, testEmail)

	// Формируем сообщение
	emailBody := c.buildEmailMessage(msg, recipientEmails, isBodyHTML, sendHiddenCopyToSelf)

	// Подключаемся к SMTP серверу с reconnect логикой
	addr := fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)
	var auth smtp.Auth
	if c.cfg.User != "" && c.cfg.Password != "" {
		auth = smtp.PlainAuth("", c.cfg.User, c.cfg.Password, c.cfg.Host)
	}

	// Создаем TLS конфигурацию
	tlsConfig := &tls.Config{
		ServerName:         c.cfg.Host,
		InsecureSkipVerify: false,
	}

	// Отправляем email с повторной попыткой при таймауте
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		err = c.sendWithTLS(ctx, addr, auth, tlsConfig, msg, recipientEmails, emailBody)
		if err == nil {
			break
		}

		// Проверяем на таймаут SMTP команды
		if strings.Contains(strings.ToLower(err.Error()), "smtp command timeout") {
			if logger.Log != nil {
				logger.Log.Warn("SMTP таймаут, повторная попытка",
					zap.Int64("taskID", msg.TaskID),
					zap.Int("attempt", attempt+1))
			}
			// Повторная попытка
			continue
		}
		// Другие ошибки - не повторяем
		break
	}

	if err != nil {
		return fmt.Errorf("ошибка отправки email: %w", err)
	}

	// Обновляем время последней отправки
	if c.cfg.MinSendIntervalMsec > 0 {
		c.lastSendTime = time.Now().Add(time.Duration(c.cfg.MinSendIntervalMsec) * time.Millisecond)
	}

	// Обновляем время последней отправки для каждого адреса
	for _, emailAddr := range recipientEmails {
		c.lastEmailTime[emailAddr] = time.Now()
	}

	if logger.Log != nil {
		logger.Log.Info("Email успешно отправлен",
			zap.Int64("taskID", msg.TaskID),
			zap.Strings("to", recipientEmails),
			zap.String("subject", msg.Title))
	}

	return nil
}

// parseEmailAddresses парсит email адреса с поддержкой разделителей ; и ,
func (c *SMTPClient) parseEmailAddresses(emailAddresses string, testEmail string) []string {
	if testEmail != "" {
		// В тестовом режиме используем только тестовый email
		return []string{testEmail}
	}

	// Заменяем запятые на точки с запятой
	emailAddresses = strings.ReplaceAll(emailAddresses, ",", ";")

	// Разбиваем по точкам с запятой
	addresses := strings.Split(emailAddresses, ";")

	result := make([]string, 0, len(addresses))
	for _, addr := range addresses {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			result = append(result, addr)
		}
	}

	return result
}

// encodeHeader кодирует заголовок по RFC 2047 для не-ASCII символов
func encodeHeader(text string) string {
	if text == "" {
		return text
	}
	
	// Проверяем, есть ли не-ASCII символы
	needsEncoding := false
	for _, r := range text {
		if r > 127 {
			needsEncoding = true
			break
		}
	}
	
	if !needsEncoding {
		return text
	}
	
	// Используем Q-encoding для заголовков (RFC 2047)
	return mime.QEncoding.Encode("UTF-8", text)
}

// buildEmailMessage формирует тело email сообщения с поддержкой вложений
func (c *SMTPClient) buildEmailMessage(msg *EmailMessage, recipientEmails []string, isBodyHTML bool, sendHiddenCopyToSelf bool) string {
	// Формируем основные заголовки
	// Кодируем DisplayName если он не пустой
	fromHeader := c.cfg.User
	if c.cfg.DisplayName != "" {
		encodedDisplayName := encodeHeader(c.cfg.DisplayName)
		fromHeader = fmt.Sprintf("%s <%s>", encodedDisplayName, c.cfg.User)
	}
	headers := fmt.Sprintf("From: %s\r\n", fromHeader)

	// To: адреса
	toHeader := strings.Join(recipientEmails, ", ")
	headers += fmt.Sprintf("To: %s\r\n", toHeader)

	// BCC: скрытая копия себе (если включено)
	if sendHiddenCopyToSelf {
		headers += fmt.Sprintf("Bcc: %s\r\n", c.cfg.User)
	}

	// Кодируем Subject если он не пустой
	subject := msg.Title
	if subject == "" {
		subject = "(без темы)" // Значение по умолчанию
	}
	encodedSubject := encodeHeader(subject)
	headers += fmt.Sprintf("Subject: %s\r\n", encodedSubject)
	headers += fmt.Sprintf("Message-ID: <%s@%s>\r\n", fmt.Sprintf("askemailsender%d", msg.TaskID), c.cfg.Host)
	headers += fmt.Sprintf("X-Envelope-ID: askemailsender%d\r\n", msg.TaskID) // Для DSN
	// Return-Path для корректной обработки DSN уведомлений
	headers += fmt.Sprintf("Return-Path: <%s>\r\n", c.cfg.User)
	headers += "MIME-Version: 1.0\r\n"

	// Определяем Content-Type для тела сообщения
	textContentType := "text/plain; charset=UTF-8"
	if isBodyHTML {
		textContentType = "text/html; charset=UTF-8"
	}

	// Если есть вложения, используем multipart/mixed
	if len(msg.Attachments) > 0 {
		boundary := fmt.Sprintf("boundary_%d_%d", msg.TaskID, time.Now().Unix())
		headers += fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary)
		headers += "\r\n"

		// Тело сообщения
		body := fmt.Sprintf("--%s\r\n", boundary)
		body += fmt.Sprintf("Content-Type: %s\r\n", textContentType)
		body += "Content-Transfer-Encoding: 8bit\r\n"
		body += "\r\n"
		body += msg.Text
		body += "\r\n\r\n"

		// Добавляем вложения
		for _, attach := range msg.Attachments {
			// Определяем MIME тип по расширению файла
			ext := filepath.Ext(attach.FileName)
			mimeType := mime.TypeByExtension(ext)
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}

			body += fmt.Sprintf("--%s\r\n", boundary)
			body += fmt.Sprintf("Content-Type: %s\r\n", mimeType)
			body += fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", attach.FileName)
			body += "Content-Transfer-Encoding: base64\r\n"
			body += "\r\n"

			// Кодируем вложение в Base64
			encoded := base64.StdEncoding.EncodeToString(attach.Data)
			// Разбиваем на строки по 76 символов (RFC 2045)
			for i := 0; i < len(encoded); i += 76 {
				end := i + 76
				if end > len(encoded) {
					end = len(encoded)
				}
				body += encoded[i:end] + "\r\n"
			}
			body += "\r\n"
		}

		body += fmt.Sprintf("--%s--\r\n", boundary)
		return headers + body
	}

	// Без вложений - простое сообщение
	headers += fmt.Sprintf("Content-Type: %s\r\n", textContentType)
	headers += "\r\n"
	body := headers + msg.Text

	return body
}

// sendWithTLS отправляет email с поддержкой TLS
func (c *SMTPClient) sendWithTLS(ctx context.Context, addr string, auth smtp.Auth, tlsConfig *tls.Config, msg *EmailMessage, recipientEmails []string, body string) error {
	// Создаем канал для результата
	done := make(chan error, 1)

	go func() {
		// Подключаемся к SMTP серверу
		client, err := smtp.Dial(addr)
		if err != nil {
			done <- fmt.Errorf("ошибка подключения к SMTP: %w", err)
			return
		}
		defer client.Close()

		// Проверяем поддержку STARTTLS
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				done <- fmt.Errorf("ошибка STARTTLS: %w", err)
				return
			}
		} else if c.cfg.EnableSSL {
			// Если требуется SSL, но STARTTLS не поддерживается, используем прямое TLS соединение
			// Это требует использования tls.Dial вместо smtp.Dial
			done <- fmt.Errorf("сервер не поддерживает STARTTLS, но требуется SSL")
			return
		}

		// Аутентификация
		if auth != nil {
			if err := client.Auth(auth); err != nil {
				done <- fmt.Errorf("ошибка аутентификации: %w", err)
				return
			}
		}

		// Устанавливаем отправителя
		if err := client.Mail(c.cfg.User); err != nil {
			done <- fmt.Errorf("ошибка установки отправителя: %w", err)
			return
		}

		// Устанавливаем получателей (To и BCC)
		for _, recipientEmail := range recipientEmails {
			if err := client.Rcpt(recipientEmail); err != nil {
				done <- fmt.Errorf("ошибка установки получателя %s: %w", recipientEmail, err)
				return
			}
		}

		// Отправляем данные
		writer, err := client.Data()
		if err != nil {
			done <- fmt.Errorf("ошибка начала передачи данных: %w", err)
			return
		}

		// Записываем тело сообщения
		if _, err := writer.Write([]byte(body)); err != nil {
			writer.Close()
			done <- fmt.Errorf("ошибка записи данных: %w", err)
			return
		}

		// Закрываем writer
		if err := writer.Close(); err != nil {
			done <- fmt.Errorf("ошибка закрытия writer: %w", err)
			return
		}

		// Отправляем QUIT
		if err := client.Quit(); err != nil {
			done <- fmt.Errorf("ошибка QUIT: %w", err)
			return
		}

		done <- nil
	}()

	// Ждем завершения или отмены контекста
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}
