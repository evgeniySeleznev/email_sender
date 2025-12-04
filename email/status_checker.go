package email

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"email-service/logger"
	"email-service/settings"
)

// SentEmailInfo хранит информацию об отправленном письме для последующей проверки статуса
type SentEmailInfo struct {
	TaskID    int64
	SmtpID    int
	MessageID string
	SendTime  time.Time
}

// StatusUpdateCallback функция для обновления статуса письма
type StatusUpdateCallback func(taskID int64, status int, statusDesc string)

// StatusChecker отвечает за проверку статуса отправленных писем через IMAP/POP3
type StatusChecker struct {
	cfg                 *settings.Config
	statusCheckChan     chan *SentEmailInfo
	statusUpdateCallback StatusUpdateCallback
	sentEmails          map[int64]*SentEmailInfo // Ключ - taskID
	sentEmailsMu        sync.RWMutex
}

// NewStatusChecker создает новый checker статусов
func NewStatusChecker(cfg *settings.Config, statusCallback StatusUpdateCallback) *StatusChecker {
	return &StatusChecker{
		cfg:                  cfg,
		statusCheckChan:      make(chan *SentEmailInfo, 1000),
		statusUpdateCallback: statusCallback,
		sentEmails:           make(map[int64]*SentEmailInfo),
	}
}

// Start запускает горутину для проверки статусов
func (sc *StatusChecker) Start(ctx context.Context) {
	go sc.statusChecker(ctx)
}

// ScheduleCheck планирует проверку статуса письма через 5 минут после отправки
func (sc *StatusChecker) ScheduleCheck(sentInfo *SentEmailInfo) {
	if sentInfo == nil {
		return
	}

	sc.sentEmailsMu.Lock()
	sc.sentEmails[sentInfo.TaskID] = sentInfo
	sc.sentEmailsMu.Unlock()

	// Отправляем в канал для проверки статуса через 5 минут
	select {
	case sc.statusCheckChan <- sentInfo:
	default:
		// Канал переполнен - логируем предупреждение
		if logger.Log != nil {
			logger.Log.Warn("Канал проверки статусов переполнен",
				zap.Int64("taskID", sentInfo.TaskID))
		}
	}
}

// statusChecker проверяет статусы отправленных писем через 5 минут после отправки
func (sc *StatusChecker) statusChecker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case sentInfo := <-sc.statusCheckChan:
			// Запускаем проверку через 5 минут
			go func(info *SentEmailInfo) {
				// Ждем 5 минут
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Minute):
					// Проверяем статус письма
					sc.checkEmailStatus(ctx, info)
				}
			}(sentInfo)
		}
	}
}

// checkEmailStatus проверяет статус письма через IMAP или POP3
func (sc *StatusChecker) checkEmailStatus(ctx context.Context, sentInfo *SentEmailInfo) {
	if sentInfo == nil {
		return
	}

	// Получаем конфигурацию SMTP сервера
	if sentInfo.SmtpID < 0 || sentInfo.SmtpID >= len(sc.cfg.SMTP) {
		return
	}
	smtpCfg := &sc.cfg.SMTP[sentInfo.SmtpID]

	// Пробуем проверить через IMAP
	if smtpCfg.IMAPHost != "" {
		imapClient := NewIMAPClient(smtpCfg)
		status, statusDesc, err := imapClient.CheckEmailStatus(ctx, sentInfo.MessageID)
		if err == nil {
			// Успешно проверили через IMAP
			if status > 0 {
				sc.updateEmailStatus(sentInfo.TaskID, status, statusDesc)
			}
			// Удаляем из отслеживания после проверки
			sc.sentEmailsMu.Lock()
			delete(sc.sentEmails, sentInfo.TaskID)
			sc.sentEmailsMu.Unlock()
			return
		}
		// Если IMAP недоступен, пробуем POP3
		if logger.Log != nil {
			logger.Log.Warn("IMAP недоступен, пробуем POP3",
				zap.Int64("taskID", sentInfo.TaskID),
				zap.Error(err))
		}
	}

	// Пробуем проверить через POP3
	if smtpCfg.POPHost != "" {
		pop3Client := NewPOP3Client(smtpCfg)
		status, statusDesc, err := pop3Client.CheckEmailStatus(ctx, sentInfo.MessageID)
		if err == nil && status > 0 {
			sc.updateEmailStatus(sentInfo.TaskID, status, statusDesc)
		}
		if err != nil && logger.Log != nil {
			logger.Log.Warn("Ошибка проверки статуса через POP3",
				zap.Int64("taskID", sentInfo.TaskID),
				zap.Error(err))
		}
	}

	// Удаляем из отслеживания после проверки
	sc.sentEmailsMu.Lock()
	delete(sc.sentEmails, sentInfo.TaskID)
	sc.sentEmailsMu.Unlock()
}

// updateEmailStatus обновляет статус письма в БД через callback
func (sc *StatusChecker) updateEmailStatus(taskID int64, status int, statusDesc string) {
	if sc.statusUpdateCallback != nil {
		sc.statusUpdateCallback(taskID, status, statusDesc)
	}
	if logger.Log != nil {
		logger.Log.Info("Обновление статуса письма",
			zap.Int64("taskID", taskID),
			zap.Int("status", status),
			zap.String("description", statusDesc))
	}
}

