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
		statusCheckChan:      make(chan *SentEmailInfo, 1000),
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

	// Отправляем в канал для проверки статуса через 30 секунд
	if logger.Log != nil {
		logger.Log.Info("Планирование проверки статуса письма",
			zap.Int64("taskID", sentInfo.TaskID),
			zap.String("messageID", sentInfo.MessageID),
			zap.Time("sendTime", sentInfo.SendTime))
	}

	select {
	case sc.statusCheckChan <- sentInfo:
		if logger.Log != nil {
			logger.Log.Debug("Письмо добавлено в очередь проверки статуса",
				zap.Int64("taskID", sentInfo.TaskID))
		}
	default:
		// Канал переполнен - логируем предупреждение
		if logger.Log != nil {
			logger.Log.Warn("Канал проверки статусов переполнен",
				zap.Int64("taskID", sentInfo.TaskID))
		}
	}
}

// statusChecker проверяет статусы отправленных писем через 30 секунд после отправки
func (sc *StatusChecker) statusChecker(ctx context.Context) {
	if logger.Log != nil {
		logger.Log.Info("Запущена горутина проверки статусов писем")
	}
	for {
		select {
		case <-ctx.Done():
			if logger.Log != nil {
				logger.Log.Info("Остановка горутины проверки статусов")
			}
			return
		case sentInfo := <-sc.statusCheckChan:
			if logger.Log != nil {
				logger.Log.Info("Получено письмо для проверки статуса, запуск таймера на 30 секунд",
					zap.Int64("taskID", sentInfo.TaskID),
					zap.String("messageID", sentInfo.MessageID))
			}
			// Запускаем проверку через 30 секунд
			go func(info *SentEmailInfo) {
				// Ждем 30 секунд
				select {
				case <-ctx.Done():
					if logger.Log != nil {
						logger.Log.Debug("Отмена проверки статуса из-за отмены контекста",
							zap.Int64("taskID", info.TaskID))
					}
					return
				case <-time.After(30 * time.Second):
					if logger.Log != nil {
						logger.Log.Info("Таймер истек, начинаем проверку статуса письма",
							zap.Int64("taskID", info.TaskID),
							zap.String("messageID", info.MessageID))
					}
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
		if logger.Log != nil {
			logger.Log.Warn("checkEmailStatus вызван с nil sentInfo")
		}
		return
	}

	if logger.Log != nil {
		logger.Log.Info("Начало проверки статуса письма",
			zap.Int64("taskID", sentInfo.TaskID),
			zap.Int("smtpID", sentInfo.SmtpID),
			zap.String("messageID", sentInfo.MessageID))
	}

	// Получаем конфигурацию SMTP сервера
	if sentInfo.SmtpID < 0 || sentInfo.SmtpID >= len(sc.cfg.SMTP) {
		if logger.Log != nil {
			logger.Log.Warn("Неверный SmtpID",
				zap.Int64("taskID", sentInfo.TaskID),
				zap.Int("smtpID", sentInfo.SmtpID),
				zap.Int("smtpServersCount", len(sc.cfg.SMTP)))
		}
		return
	}
	smtpCfg := &sc.cfg.SMTP[sentInfo.SmtpID]

	// Пробуем проверить через IMAP
	if smtpCfg.IMAPHost != "" {
		if logger.Log != nil {
			logger.Log.Info("Проверка статуса через IMAP",
				zap.Int64("taskID", sentInfo.TaskID),
				zap.String("imapHost", smtpCfg.IMAPHost))
		}
		imapClient := NewIMAPClient(smtpCfg)
		status, statusDesc, err := imapClient.CheckEmailStatus(ctx, sentInfo.MessageID)
		if err == nil {
			// Успешно проверили через IMAP
			if logger.Log != nil {
				logger.Log.Info("Проверка через IMAP завершена",
					zap.Int64("taskID", sentInfo.TaskID),
					zap.Int("status", status),
					zap.String("statusDesc", statusDesc))
			}
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
	} else {
		if logger.Log != nil {
			logger.Log.Debug("IMAP не настроен для SMTP сервера",
				zap.Int64("taskID", sentInfo.TaskID),
				zap.Int("smtpID", sentInfo.SmtpID))
		}
	}

	// Пробуем проверить через POP3
	if smtpCfg.POPHost != "" {
		if logger.Log != nil {
			logger.Log.Info("Проверка статуса через POP3",
				zap.Int64("taskID", sentInfo.TaskID),
				zap.String("popHost", smtpCfg.POPHost))
		}
		pop3Client := NewPOP3Client(smtpCfg)
		status, statusDesc, err := pop3Client.CheckEmailStatus(ctx, sentInfo.MessageID)
		if err == nil && status > 0 {
			if logger.Log != nil {
				logger.Log.Info("Проверка через POP3 завершена",
					zap.Int64("taskID", sentInfo.TaskID),
					zap.Int("status", status),
					zap.String("statusDesc", statusDesc))
			}
			sc.updateEmailStatus(sentInfo.TaskID, status, statusDesc)
		}
		if err != nil && logger.Log != nil {
			logger.Log.Warn("Ошибка проверки статуса через POP3",
				zap.Int64("taskID", sentInfo.TaskID),
				zap.Error(err))
		}
	} else {
		if logger.Log != nil {
			logger.Log.Debug("POP3 не настроен для SMTP сервера",
				zap.Int64("taskID", sentInfo.TaskID),
				zap.Int("smtpID", sentInfo.SmtpID))
		}
	}

	// Удаляем из отслеживания после проверки
	sc.sentEmailsMu.Lock()
	delete(sc.sentEmails, sentInfo.TaskID)
	sc.sentEmailsMu.Unlock()

	if logger.Log != nil {
		logger.Log.Info("Завершена проверка статуса письма",
			zap.Int64("taskID", sentInfo.TaskID))
	}
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
