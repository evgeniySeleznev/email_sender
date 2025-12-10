package email

import (
	"context"
	"fmt"
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
// statusDesc - описание статуса для логирования
// errorText - текст ошибки для записи в error_text (может быть пустым)
type StatusUpdateCallback func(taskID int64, status int, statusDesc string, errorText string)

// StatusChecker отвечает за проверку статуса отправленных писем через IMAP
type StatusChecker struct {
	cfg                  *settings.Config
	statusCheckChan      chan *SentEmailInfo
	statusUpdateCallback StatusUpdateCallback
	sentEmails           map[int64]*SentEmailInfo // Ключ - taskID
	sentEmailsMu         sync.RWMutex
}

// NewStatusChecker создает новый checker статусов
func NewStatusChecker(cfg *settings.Config, statusCallback StatusUpdateCallback) *StatusChecker {
	return &StatusChecker{
		cfg:                  cfg,
		statusCheckChan:      make(chan *SentEmailInfo, 2000),
		statusUpdateCallback: statusCallback,
		sentEmails:           make(map[int64]*SentEmailInfo),
	}
}

// Start запускает горутину для проверки статусов
func (sc *StatusChecker) Start(ctx context.Context) {
	go sc.statusChecker(ctx)
}

// ScheduleCheck планирует проверку статуса письма через 30 секунд после отправки
func (sc *StatusChecker) ScheduleCheck(sentInfo *SentEmailInfo) {
	if sentInfo == nil {
		return
	}

	sc.sentEmailsMu.Lock()
	sc.sentEmails[sentInfo.TaskID] = sentInfo
	sc.sentEmailsMu.Unlock()

	select {
	case sc.statusCheckChan <- sentInfo:
	default:
	}
}

// statusChecker проверяет статусы отправленных писем через 30 секунд после отправки
func (sc *StatusChecker) statusChecker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case sentInfo := <-sc.statusCheckChan:
			if sentInfo == nil {
				continue
			}

			if logger.Log != nil {
				logger.Log.Debug("Запланирована проверка статуса письма",
					zap.Int64("taskID", sentInfo.TaskID),
					zap.String("messageID", sentInfo.MessageID),
					zap.Duration("delay", 30*time.Second))
			}

			go func(info *SentEmailInfo) {
				select {
				case <-ctx.Done():
					return
				case <-time.After(30 * time.Second):
					if logger.Log != nil {
						logger.Log.Debug("Начало проверки статуса письма",
							zap.Int64("taskID", info.TaskID),
							zap.String("messageID", info.MessageID))
					}
					sc.checkEmailStatus(ctx, info)
				}
			}(sentInfo)
		}
	}
}

// checkEmailStatus проверяет статус письма через IMAP
func (sc *StatusChecker) checkEmailStatus(ctx context.Context, sentInfo *SentEmailInfo) {
	if sentInfo == nil {
		if logger.Log != nil {
			logger.Log.Warn("checkEmailStatus вызван с nil sentInfo")
		}
		return
	}

	if sentInfo.SmtpID < 0 || sentInfo.SmtpID >= len(sc.cfg.SMTP) {
		if logger.Log != nil {
			logger.Log.Warn("Некорректный SmtpID для проверки статуса",
				zap.Int64("taskID", sentInfo.TaskID),
				zap.Int("smtpID", sentInfo.SmtpID),
				zap.Int("smtpCount", len(sc.cfg.SMTP)))
		}
		sc.updateEmailStatus(sentInfo.TaskID, 3, "Некорректный SmtpID", "Некорректный SmtpID")
		return
	}
	smtpCfg := &sc.cfg.SMTP[sentInfo.SmtpID]

	if smtpCfg.IMAPHost == "" {
		if logger.Log != nil {
			logger.Log.Debug("IMAP не настроен для проверки статуса",
				zap.Int64("taskID", sentInfo.TaskID),
				zap.Int("smtpID", sentInfo.SmtpID))
		}
		// Если IMAP не настроен, считаем письмо успешно отправленным (статус 4)
		sc.updateEmailStatus(sentInfo.TaskID, 4, "IMAP не настроен, статус не проверяется", "")
		return
	}

	imapClient := NewIMAPClient(smtpCfg)
	status, statusDesc, err := imapClient.CheckEmailStatus(ctx, sentInfo.MessageID)
	if err != nil {
		// Проверяем, является ли ошибка таймаутом
		if err == context.DeadlineExceeded || err == context.Canceled {
			// При таймауте оставляем статус 2 (отправлено), но записываем сообщение в error_text
			timeoutMsg := fmt.Sprintf("Не уложились в таймаут проверки статуса через IMAP сервер (35 секунд): %v", err)
			if logger.Log != nil {
				logger.Log.Warn("Таймаут проверки статуса через IMAP",
					zap.Int64("taskID", sentInfo.TaskID),
					zap.String("messageID", sentInfo.MessageID),
					zap.Error(err))
			}
			sc.updateEmailStatus(sentInfo.TaskID, 2, "Проверка статуса не завершена из-за таймаута", timeoutMsg)
			return
		}

		// Для других ошибок устанавливаем статус 3 (ошибка)
		if logger.Log != nil {
			logger.Log.Error("Ошибка проверки статуса через IMAP",
				zap.Int64("taskID", sentInfo.TaskID),
				zap.String("messageID", sentInfo.MessageID),
				zap.Error(err))
		}
		sc.updateEmailStatus(sentInfo.TaskID, 3, fmt.Sprintf("Ошибка проверки статуса через IMAP: %v", err), fmt.Sprintf("Ошибка проверки статуса через IMAP: %v", err))
		return
	}

	if logger.Log != nil {
		logger.Log.Info("Статус письма проверен",
			zap.Int64("taskID", sentInfo.TaskID),
			zap.Int("status", status),
			zap.String("statusDesc", statusDesc))
	}

	// Для успешных проверок errorText пустой (заполняется только при статусе 3)
	errorText := ""
	if status == 3 {
		errorText = statusDesc
	}
	sc.updateEmailStatus(sentInfo.TaskID, status, statusDesc, errorText)
}

// updateEmailStatus обновляет статус письма в БД через callback
func (sc *StatusChecker) updateEmailStatus(taskID int64, status int, statusDesc string, errorText string) {
	if sc.statusUpdateCallback != nil {
		sc.statusUpdateCallback(taskID, status, statusDesc, errorText)
	}
}
