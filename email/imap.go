package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"go.uber.org/zap"

	"email-service/logger"
	"email-service/settings"
)

// IMAPClient представляет IMAP клиент для получения статусов доставки
type IMAPClient struct {
	cfg            *settings.SMTPConfig
	lastStatusTime time.Time
	mu             sync.Mutex
}

// NewIMAPClient создает новый IMAP клиент
func NewIMAPClient(cfg *settings.SMTPConfig) *IMAPClient {
	return &IMAPClient{
		cfg:            cfg,
		lastStatusTime: time.Now().Add(-24 * time.Hour), // Начинаем с вчерашнего дня
	}
}

// GetMessagesStatus получает статусы доставки из IMAP почтового ящика
func (c *IMAPClient) GetMessagesStatus(ctx context.Context, sourceEmail string, processDSN func(taskID int64, status int, statusDesc string)) error {
	if c.cfg.IMAPHost == "" {
		return nil // IMAP не настроен
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	startTime := time.Now()

	logger.Log.Info("Получение статусов доставки через IMAP",
		zap.String("imapHost", c.cfg.IMAPHost),
		zap.Int("imapPort", c.cfg.IMAPPort))

	// Подключаемся к IMAP серверу
	addr := fmt.Sprintf("%s:%d", c.cfg.IMAPHost, c.cfg.IMAPPort)
	var imapClient *client.Client
	var err error

	if c.cfg.IMAPPort == 993 {
		// SSL/TLS соединение
		imapClient, err = client.DialTLS(addr, &tls.Config{
			ServerName:         c.cfg.IMAPHost,
			InsecureSkipVerify: false,
		})
	} else {
		// Обычное соединение с STARTTLS
		imapClient, err = client.Dial(addr)
		if err == nil {
			// Пробуем STARTTLS
			if err := imapClient.StartTLS(&tls.Config{
				ServerName:         c.cfg.IMAPHost,
				InsecureSkipVerify: false,
			}); err != nil {
				imapClient.Logout()
				return fmt.Errorf("ошибка STARTTLS: %w", err)
			}
		}
	}

	if err != nil {
		return fmt.Errorf("ошибка подключения к IMAP: %w", err)
	}
	defer imapClient.Logout()

	// Аутентификация
	if err := imapClient.Login(c.cfg.User, c.cfg.Password); err != nil {
		return fmt.Errorf("ошибка аутентификации IMAP: %w", err)
	}

	// Выбираем папку INBOX
	_, err = imapClient.Select("INBOX", false)
	if err != nil {
		return fmt.Errorf("ошибка выбора папки INBOX: %w", err)
	}

	// Ищем непрочитанные сообщения (или все сообщения после lastStatusTime)
	criteria := imap.NewSearchCriteria()
	criteria.Since = c.lastStatusTime
	// Также ищем по заголовку X-Envelope-ID для DSN
	criteria.Header.Add("X-Envelope-ID", "")

	ids, err := imapClient.Search(criteria)
	if err != nil {
		// Если поиск по заголовку не работает, ищем все сообщения после даты
		criteria = imap.NewSearchCriteria()
		criteria.Since = c.lastStatusTime
		ids, err = imapClient.Search(criteria)
		if err != nil {
			return fmt.Errorf("ошибка поиска сообщений: %w", err)
		}
	}

	if len(ids) == 0 {
		logger.Log.Info("Нет новых сообщений в IMAP ящике",
			zap.String("imapHost", c.cfg.IMAPHost),
			zap.Time("lastStatusTime", c.lastStatusTime))
		c.lastStatusTime = startTime
		return nil
	}

	logger.Log.Info("Найдено сообщений в IMAP ящике",
		zap.String("imapHost", c.cfg.IMAPHost),
		zap.Int("totalMessages", len(ids)),
		zap.Time("lastStatusTime", c.lastStatusTime))

	// Ограничение на количество обрабатываемых сообщений
	maxMessages := 1000
	if len(ids) > maxMessages {
		ids = ids[:maxMessages]
		logger.Log.Debug("Ограничение количества сообщений", zap.Int("max", maxMessages))
	}

	// Получаем сообщения
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(ids...)

	section := &imap.BodySectionName{}
	items := []imap.FetchItem{section.FetchItem(), imap.FetchEnvelope}

	messages := make(chan *imap.Message, 10)
	done := make(chan error, 1)

	go func() {
		done <- imapClient.Fetch(seqSet, items, messages)
	}()

	processed := 0

	for {
		select {
		case <-ctx.Done():
			logger.Log.Info("Прерывание получения статусов из-за отмены контекста")
			return ctx.Err()
		case err := <-done:
			if err != nil {
				return fmt.Errorf("ошибка получения сообщений: %w", err)
			}
			// Все сообщения получены
			goto done
		case msg := <-messages:
			if msg == nil {
				continue
			}

			// Проверяем дату сообщения
			if msg.Envelope != nil && msg.Envelope.Date.Before(c.lastStatusTime) {
				logger.Log.Debug("Пропускаем сообщение, дата раньше lastStatusTime",
					zap.Time("messageDate", msg.Envelope.Date),
					zap.Time("lastStatusTime", c.lastStatusTime))
				continue
			}

			// Получаем тело сообщения
			if msg.Body == nil {
				logger.Log.Debug("Сообщение не содержит тела")
				continue
			}

			// Читаем тело сообщения
			bodyData, err := io.ReadAll(msg.Body[section])
			if err != nil {
				logger.Log.Debug("Ошибка чтения тела сообщения", zap.Error(err))
				continue
			}

			// Парсим MIME сообщение
			mimeMsg, err := ParseMIMEMessage(bodyData)
			if err != nil {
				logger.Log.Debug("Ошибка парсинга MIME сообщения", zap.Error(err))
				continue
			}

			// Логируем информацию о сообщении для отладки
			var msgDate time.Time
			if mimeMsg.Date != nil {
				msgDate = *mimeMsg.Date
			} else if msg.Envelope != nil {
				msgDate = msg.Envelope.Date
			}
			logger.Log.Debug("Обработка сообщения из IMAP",
				zap.Uint32("seqNum", msg.SeqNum),
				zap.Int("partsCount", len(mimeMsg.Parts)),
				zap.Int("bodySize", len(mimeMsg.Body)),
				zap.Time("date", msgDate))

			// Обрабатываем DSN
			taskID, status, statusDesc := ProcessDeliveryStatusNotification(sourceEmail, mimeMsg)
			if taskID > 0 && status > 0 {
				logger.Log.Info("Получен статус доставки через IMAP",
					zap.Int64("taskID", taskID),
					zap.Int("status", status),
					zap.String("description", statusDesc))

				// Вызываем callback для обработки статуса
				processDSN(taskID, status, statusDesc)

				processed++

				// Помечаем сообщение как прочитанное (не удаляем, как в POP3)
				seqSet := new(imap.SeqSet)
				seqSet.AddNum(msg.SeqNum)
				item := imap.FormatFlagsOp(imap.AddFlags, true)
				flags := []interface{}{imap.SeenFlag}
				if err := imapClient.Store(seqSet, item, flags, nil); err != nil {
					logger.Log.Warn("Ошибка пометки сообщения как прочитанного", zap.Uint32("seqNum", msg.SeqNum), zap.Error(err))
				}
			} else {
				// Логируем подробную информацию о сообщении для диагностики
				bodyPreview := ""
				if len(mimeMsg.Body) > 0 {
					bodyStr := string(mimeMsg.Body)
					if len(bodyStr) > 500 {
						bodyPreview = bodyStr[:500] + "..."
					} else {
						bodyPreview = bodyStr
					}
				}

				// Проверяем, содержит ли сообщение ключевые слова DSN
				isDSNCandidate := false
				if len(mimeMsg.Body) > 0 {
					bodyStr := string(mimeMsg.Body)
					isDSNCandidate = strings.Contains(bodyStr, "delivery-status") ||
						strings.Contains(bodyStr, "Original-Envelope-Id") ||
						strings.Contains(bodyStr, "X-Envelope-ID") ||
						strings.Contains(bodyStr, "askemailsender")
				}

				for _, part := range mimeMsg.Parts {
					if strings.Contains(part.ContentType, "delivery-status") {
						isDSNCandidate = true
						break
					}
				}

				if isDSNCandidate {
					logger.Log.Info("Найдено потенциальное DSN сообщение через IMAP, но оно не было обработано",
						zap.Uint32("seqNum", msg.SeqNum),
						zap.Int("partsCount", len(mimeMsg.Parts)),
						zap.String("bodyPreview", bodyPreview),
						zap.Int64("taskID", taskID),
						zap.Int("status", status))
				} else {
					logger.Log.Debug("Сообщение не является DSN",
						zap.Uint32("seqNum", msg.SeqNum),
						zap.Int("partsCount", len(mimeMsg.Parts)))
				}
			}
		}
	}

done:
	c.lastStatusTime = startTime

	logger.Log.Info("Завершена проверка статусов доставки через IMAP",
		zap.String("imapHost", c.cfg.IMAPHost),
		zap.Int("processedDSN", processed),
		zap.Int("totalMessages", len(ids)),
		zap.Duration("duration", time.Since(startTime)))

	return nil
}
