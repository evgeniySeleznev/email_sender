package email

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"email-service/db"
	"email-service/logger"
	"email-service/settings"
)

// Service представляет email сервис
type Service struct {
	cfg                 *settings.Config
	dbConn              *db.DBConnection
	smtpClients         []*SMTPClient
	imapClients         []*IMAPClient // IMAP клиенты для сохранения в Sent
	attachmentProcessor *AttachmentProcessor
	testEmail           string
	testEmailCacheTime  time.Time
	testEmailMu         sync.RWMutex
	testEmailCacheTTL   time.Duration

	// Проверка статуса отправленных писем
	statusChecker *StatusChecker
}

// NewService создает новый email сервис
func NewService(cfg *settings.Config, dbConn *db.DBConnection, statusCallback StatusUpdateCallback) (*Service, error) {
	// Создаем SMTP клиенты для каждого SMTP сервера
	smtpClients := make([]*SMTPClient, 0, len(cfg.SMTP))
	imapClients := make([]*IMAPClient, 0, len(cfg.SMTP))
	for i := range cfg.SMTP {
		smtpClient := NewSMTPClient(&cfg.SMTP[i])
		smtpClients = append(smtpClients, smtpClient)
		// Создаем IMAP клиент для сохранения в Sent (если IMAP настроен)
		if cfg.SMTP[i].IMAPHost != "" {
			imapClient := NewIMAPClient(&cfg.SMTP[i])
			imapClients = append(imapClients, imapClient)
		} else {
			imapClients = append(imapClients, nil)
		}
	}

	service := &Service{
		cfg:                 cfg,
		dbConn:              dbConn,
		smtpClients:         smtpClients,
		imapClients:         imapClients,
		attachmentProcessor: NewAttachmentProcessor(dbConn),
		testEmailCacheTTL:   5 * time.Minute, // Кеш тестового email на 5 минут
		statusChecker:       NewStatusChecker(cfg, statusCallback),
	}

	// Запускаем горутину для проверки статусов отправленных писем
	// Контекст будет передан при вызове Close или через отдельный механизм
	service.statusChecker.Start(context.Background())

	return service, nil
}

// Close закрывает сервис
func (s *Service) Close() error {
	// SMTP клиенты не требуют явного закрытия (используют стандартный net/smtp)
	// POP3 клиенты создаются по требованию и закрываются автоматически
	if logger.Log != nil {
		logger.Log.Info("Email сервис закрыт")
	}
	return nil
}

// SendEmail отправляет email
func (s *Service) SendEmail(ctx context.Context, msg *EmailMessage) error {
	// Получаем тестовый email, если включен Debug режим
	var testEmail string
	if s.cfg.Mode.Debug {
		testEmail = s.getTestEmail(ctx)
		if testEmail == "" {
			if logger.Log != nil {
				logger.Log.Warn("Debug режим включен, но тестовый email не получен, используем оригинальный адрес",
					zap.Int64("taskID", msg.TaskID))
			}
		}
	}

	// Выбираем SMTP клиент по SmtpID (индекс в массиве)
	smtpIndex := msg.SmtpID
	if smtpIndex < 0 || smtpIndex >= len(s.smtpClients) {
		smtpIndex = 0 // Используем первый SMTP сервер по умолчанию
	}

	smtpClient := s.smtpClients[smtpIndex]

	// Получаем тело письма для сохранения в папку Sent
	recipientEmails := smtpClient.parseEmailAddresses(msg.EmailAddress, testEmail)
	emailBody := smtpClient.GetEmailBody(msg, recipientEmails, s.cfg.Mode.IsBodyHTML, s.cfg.Mode.SendHiddenCopyToSelf)

	// Отправляем email с параметрами из конфигурации
	if err := smtpClient.SendEmail(ctx, msg, testEmail, s.cfg.Mode.IsBodyHTML, s.cfg.Mode.SendHiddenCopyToSelf); err != nil {
		return fmt.Errorf("ошибка отправки через SMTP: %w", err)
	}

	// Сохраняем письмо в папку "Sent" через IMAP (если IMAP настроен)
	if smtpIndex < len(s.imapClients) && s.imapClients[smtpIndex] != nil {
		// Сохраняем асинхронно, чтобы не блокировать отправку
		go func(imapClient *IMAPClient, body string) {
			if err := imapClient.SaveToSentFolder(context.Background(), body); err != nil {
				if logger.Log != nil {
					logger.Log.Warn("Ошибка сохранения письма в папку Sent",
						zap.Int64("taskID", msg.TaskID),
						zap.Error(err))
				}
			} else {
				if logger.Log != nil {
					logger.Log.Debug("Письмо сохранено в папку Sent",
						zap.Int64("taskID", msg.TaskID))
				}
			}
		}(s.imapClients[smtpIndex], emailBody)
	}

	// Сохраняем информацию об отправленном письме для последующей проверки статуса
	smtpCfg := &s.cfg.SMTP[smtpIndex]
	// Message-ID должен совпадать с тем, что в заголовке письма
	// В smtp.go он формируется как: <askemailsender%d@%s>
	// Поэтому здесь тоже используем тот же формат (без угловых скобок, они добавятся при поиске)
	messageID := fmt.Sprintf("askemailsender%d@%s", msg.TaskID, smtpCfg.Host)
	sentInfo := &SentEmailInfo{
		TaskID:    msg.TaskID,
		SmtpID:    msg.SmtpID,
		MessageID: messageID,
		SendTime:  time.Now(),
	}

	if logger.Log != nil {
		logger.Log.Info("Планирование проверки статуса после отправки письма",
			zap.Int64("taskID", msg.TaskID),
			zap.String("messageID", messageID))
	}

	// Планируем проверку статуса через 30 секунд
	s.statusChecker.ScheduleCheck(sentInfo)

	return nil
}

// getTestEmail получает тестовый email из БД с кешированием
func (s *Service) getTestEmail(ctx context.Context) string {
	s.testEmailMu.RLock()
	// Проверяем кеш
	if s.testEmail != "" && time.Since(s.testEmailCacheTime) < s.testEmailCacheTTL {
		cachedEmail := s.testEmail
		s.testEmailMu.RUnlock()
		return cachedEmail
	}
	s.testEmailMu.RUnlock()

	// Получаем тестовый email из БД
	testEmail, err := s.dbConn.GetTestEmail()
	if err != nil {
		if logger.Log != nil {
			logger.Log.Warn("Ошибка получения тестового email из БД",
				zap.Error(err))
		}
		return ""
	}

	// Обновляем кеш
	s.testEmailMu.Lock()
	s.testEmail = testEmail
	s.testEmailCacheTime = time.Now()
	s.testEmailMu.Unlock()

	return testEmail
}

// ProcessAttachment обрабатывает вложение и возвращает данные для отправки
func (s *Service) ProcessAttachment(ctx context.Context, attach *Attachment, taskID int64) (*AttachmentData, error) {
	return s.attachmentProcessor.ProcessAttachment(ctx, attach, taskID)
}

// EmailMessage представляет email сообщение для отправки
type EmailMessage struct {
	TaskID       int64
	SmtpID       int
	EmailAddress string
	Title        string
	Text         string
	Attachments  []AttachmentData
}

// AttachmentData представляет данные вложения
type AttachmentData struct {
	FileName string
	Data     []byte
}
