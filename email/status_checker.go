package email

import (
	"context"
	"sync"
	"time"

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
			go func(info *SentEmailInfo) {
				select {
				case <-ctx.Done():
					return
				case <-time.After(30 * time.Second):
					sc.checkEmailStatus(ctx, info)
				}
			}(sentInfo)
		}
	}
}

// checkEmailStatus проверяет статус письма через IMAP
func (sc *StatusChecker) checkEmailStatus(ctx context.Context, sentInfo *SentEmailInfo) {
	if sentInfo == nil {
		return
	}

	if sentInfo.SmtpID < 0 || sentInfo.SmtpID >= len(sc.cfg.SMTP) {
		return
	}
	smtpCfg := &sc.cfg.SMTP[sentInfo.SmtpID]

	if smtpCfg.IMAPHost != "" {
		imapClient := NewIMAPClient(smtpCfg)
		status, statusDesc, err := imapClient.CheckEmailStatus(ctx, sentInfo.MessageID)
		if err == nil {
			sc.updateEmailStatus(sentInfo.TaskID, status, statusDesc)
			return
		}
	}

	sc.updateEmailStatus(sentInfo.TaskID, 3, "Ошибка проверки статуса через IMAP")
}

// updateEmailStatus обновляет статус письма в БД через callback
func (sc *StatusChecker) updateEmailStatus(taskID int64, status int, statusDesc string) {
	if sc.statusUpdateCallback != nil {
		sc.statusUpdateCallback(taskID, status, statusDesc)
	}
}
