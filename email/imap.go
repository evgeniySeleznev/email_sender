package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"email-service/logger"
	"email-service/settings"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
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

// CheckEmailStatus проверяет наличие bounce messages по Message-ID во всех папках входящих
// Возвращает status (3 - bounce найден/ошибка, 4 - bounce не найден/доставлено), описание и ошибку
// Общий таймаут операции: 35 секунд
func (c *IMAPClient) CheckEmailStatus(ctx context.Context, messageID string) (int, string, error) {
	if c.cfg.IMAPHost == "" {
		return 4, "IMAP не настроен, считаем письмо доставленным", nil
	}

	// Устанавливаем общий таймаут для всей операции: 35 секунд
	// Это достаточно для проверки нескольких папок, но предотвращает зависание
	timeoutCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()

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
				return 4, "Ошибка STARTTLS, считаем письмо доставленным", fmt.Errorf("ошибка STARTTLS: %w", err)
			}
		}
	}

	if err != nil {
		return 4, "Ошибка подключения к IMAP, считаем письмо доставленным", fmt.Errorf("ошибка подключения к IMAP: %w", err)
	}
	defer imapClient.Logout()

	// Аутентификация
	if err := imapClient.Login(c.cfg.User, c.cfg.Password); err != nil {
		return 4, "Ошибка аутентификации IMAP, считаем письмо доставленным", fmt.Errorf("ошибка аутентификации IMAP: %w", err)
	}

	// Проверяем bounce messages во всех папках входящих (включая INBOX, Spam, Trash и другие)
	inboxFolders, err := c.listInboxFolders(timeoutCtx, imapClient)
	// Проверяем, является ли ошибка таймаутом
	if err != nil && (err == context.DeadlineExceeded || err == context.Canceled) {
		if logger.Log != nil {
			logger.Log.Warn("Таймаут получения списка папок IMAP",
				zap.String("messageID", messageID),
				zap.Error(err))
		}
		return 4, "Таймаут проверки статуса, считаем письмо доставленным", err
	}
	if err == nil {
		// Добавляем INBOX в начало списка, так как bounce messages обычно приходят туда
		inboxFolders = append([]string{"INBOX"}, inboxFolders...)

		for _, folderName := range inboxFolders {
			// Проверяем, не истек ли общий таймаут
			select {
			case <-timeoutCtx.Done():
				if logger.Log != nil {
					logger.Log.Warn("Таймаут проверки статуса письма",
						zap.String("messageID", messageID),
						zap.String("reason", "превышен общий таймаут 35 секунд"))
				}
				return 4, "Таймаут проверки статуса, считаем письмо доставленным", timeoutCtx.Err()
			default:
			}

			bounceStatus, bounceDesc, err := c.checkBounceMessages(timeoutCtx, imapClient, folderName, messageID)
			// Проверяем, является ли ошибка таймаутом
			if err != nil && (err == context.DeadlineExceeded || err == context.Canceled) {
				if logger.Log != nil {
					logger.Log.Warn("Таймаут проверки папки IMAP",
						zap.String("messageID", messageID),
						zap.String("folder", folderName),
						zap.Error(err))
				}
				return 4, "Таймаут проверки статуса, считаем письмо доставленным", err
			}
			if err == nil && bounceStatus == 3 {
				// Найдено bounce message - письмо не доставлено
				if logger.Log != nil {
					logger.Log.Info("Найден bounce message",
						zap.String("messageID", messageID),
						zap.String("folder", folderName),
						zap.String("description", bounceDesc))
				}
				return 3, bounceDesc, nil
			}
		}
	} else {
		// Если не удалось получить список папок (не таймаут), проверяем хотя бы INBOX
		bounceStatus, bounceDesc, err := c.checkBounceMessages(timeoutCtx, imapClient, "INBOX", messageID)
		// Проверяем, является ли ошибка таймаутом
		if err != nil && (err == context.DeadlineExceeded || err == context.Canceled) {
			if logger.Log != nil {
				logger.Log.Warn("Таймаут проверки INBOX IMAP",
					zap.String("messageID", messageID),
					zap.Error(err))
			}
			return 4, "Таймаут проверки статуса, считаем письмо доставленным", err
		}
		if err == nil && bounceStatus == 3 {
			if logger.Log != nil {
				logger.Log.Info("Найден bounce message в INBOX",
					zap.String("messageID", messageID),
					zap.String("description", bounceDesc))
			}
			return 3, bounceDesc, nil
		}
	}

	// Bounce messages не найдено - считаем письмо успешно доставленным
	if logger.Log != nil {
		logger.Log.Debug("Bounce messages не найдено, письмо считается доставленным",
			zap.String("messageID", messageID))
	}
	return 4, "Bounce messages не найдено, письмо доставлено", nil
}

// listInboxFolders получает список всех папок входящих (INBOX|*, Spam, Trash и другие корневые папки входящих)
// INBOX добавляется отдельно в CheckEmailStatus, так как bounce messages обычно приходят туда
// Таймаут: 15 секунд
func (c *IMAPClient) listInboxFolders(ctx context.Context, imapClient *client.Client) ([]string, error) {
	mailboxes := make(chan *imap.MailboxInfo, 10)
	done := make(chan error, 1)

	go func() {
		done <- imapClient.List("", "*", mailboxes)
	}()

	// Таймаут для получения списка папок: 15 секунд
	timeout := time.After(15 * time.Second)

	var folders []string
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			if logger.Log != nil {
				logger.Log.Warn("Таймаут получения списка папок IMAP", zap.Duration("timeout", 15*time.Second))
			}
			return folders, nil // Возвращаем то, что успели получить
		case err := <-done:
			if err != nil {
				return nil, err
			}
			return folders, nil
		case mbox := <-mailboxes:
			if mbox == nil {
				continue
			}
			// Добавляем все папки, которые могут содержать входящие письма
			// Включаем INBOX|*, Spam, Trash и другие корневые папки
			folderName := mbox.Name
			// Пропускаем саму папку INBOX (она добавляется отдельно в CheckEmailStatus)
			if folderName == "INBOX" {
				continue
			}
			// Включаем подпапки INBOX (INBOX|*)
			if strings.HasPrefix(folderName, "INBOX|") {
				folders = append(folders, folderName)
			}
			// Включаем корневые папки типа Spam, Trash
			if folderName == "Spam" || folderName == "Trash" {
				folders = append(folders, folderName)
			}
		}
	}
}

// checkBounceMessages проверяет наличие bounce messages в указанной папке
// Ищет письма о недоставке, которые ссылаются на указанный Message-ID
// Таймаут: 15 секунд на папку
func (c *IMAPClient) checkBounceMessages(ctx context.Context, imapClient *client.Client, folderName, messageID string) (int, string, error) {
	// Пробуем выбрать папку
	_, err := imapClient.Select(folderName, false)
	if err != nil {
		// Если папка недоступна, возвращаем что не найдено
		return 0, "", err
	}

	messageIDClean := strings.Trim(messageID, "<>")

	// Получаем статус папки
	mailboxStatus, err := imapClient.Status(folderName, []imap.StatusItem{imap.StatusMessages})
	if err != nil {
		return 0, "", err
	}

	if mailboxStatus.Messages == 0 {
		return 0, "", nil
	}

	// Проверяем последние 200 писем (bounce messages обычно приходят быстро)
	maxMessages := uint32(200)
	if mailboxStatus.Messages < maxMessages {
		maxMessages = mailboxStatus.Messages
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddRange(mailboxStatus.Messages-maxMessages+1, mailboxStatus.Messages)

	// Получаем заголовки и тело письма
	items := []imap.FetchItem{
		imap.FetchEnvelope,
		imap.FetchUid,
	}

	messages := make(chan *imap.Message, 10)
	done := make(chan error, 1)

	go func() {
		done <- imapClient.Fetch(seqSet, items, messages)
	}()

	// Таймаут для проверки папки: 15 секунд
	timeout := time.After(15 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return 0, "", ctx.Err()
		case <-timeout:
			if logger.Log != nil {
				logger.Log.Debug("Таймаут проверки папки IMAP",
					zap.String("folder", folderName),
					zap.Duration("timeout", 15*time.Second))
			}
			// Таймаут - считаем, что bounce messages не найдено в этой папке
			return 0, "", nil
		case err := <-done:
			if err != nil {
				return 0, "", err
			}
			// Проверка завершена, bounce messages не найдено
			return 0, "", nil

		case msg := <-messages:
			if msg == nil {
				continue
			}

			// Проверяем, является ли это bounce message
			if c.isBounceMessage(msg, messageIDClean) {
				// Получаем полное тело письма для извлечения деталей ошибки и проверки Message-ID
				errorDesc, found := c.extractBounceError(ctx, msg, imapClient, folderName, messageIDClean)
				if found {
					return 3, errorDesc, nil
				}
			}
		}
	}
}

// isBounceMessage проверяет, является ли письмо bounce message для указанного Message-ID
func (c *IMAPClient) isBounceMessage(msg *imap.Message, messageIDClean string) bool {
	if msg.Envelope == nil {
		return false
	}

	// Проверяем отправителя на типичные адреса bounce messages
	from := ""
	if len(msg.Envelope.From) > 0 {
		from = strings.ToLower(msg.Envelope.From[0].Address())
	}

	bounceFromKeywords := []string{
		"mailer-daemon",
		"postmaster",
		"mail delivery subsystem",
		"mailer@",
		"noreply@",
	}

	isBounceFrom := false
	for _, keyword := range bounceFromKeywords {
		if strings.Contains(from, keyword) {
			isBounceFrom = true
			break
		}
	}

	// Проверяем Subject на типичные паттерны bounce messages
	subject := strings.ToLower(msg.Envelope.Subject)
	bounceKeywords := []string{
		"delivery status notification",
		"mail delivery failed",
		"undelivered mail",
		"returned mail",
		"mail delivery subsystem",
		"delivery failure",
		"failure notice",
		"недоставленное сообщение",
		"недоставленное письмо",
		"ошибка доставки",
		"возврат письма",
		"не может быть отправлено",
	}

	isBounceSubject := false
	for _, keyword := range bounceKeywords {
		if strings.Contains(subject, keyword) {
			isBounceSubject = true
			break
		}
	}

	// Если это не bounce message по отправителю и теме, пропускаем
	if !isBounceFrom && !isBounceSubject {
		return false
	}

	// Проверяем заголовки на наличие Message-ID исходного письма
	// Проверяем Envelope.InReplyTo
	if msg.Envelope.InReplyTo != "" {
		inReplyToClean := strings.Trim(msg.Envelope.InReplyTo, "<>")
		if strings.Contains(inReplyToClean, messageIDClean) || strings.Contains(messageIDClean, inReplyToClean) {
			return true
		}
	}

	// Если это bounce message по отправителю и теме, считаем его релевантным
	// Проверка Message-ID в теле письма будет выполнена в extractBounceError
	return isBounceFrom && isBounceSubject
}

// extractBounceError извлекает описание ошибки из bounce message и проверяет наличие Message-ID в теле
// Возвращает описание ошибки и флаг, указывающий, найден ли Message-ID в теле письма
// Таймаут: 8 секунд
func (c *IMAPClient) extractBounceError(ctx context.Context, msg *imap.Message, imapClient *client.Client, folderName, messageIDClean string) (string, bool) {
	if msg.Uid == 0 {
		return "", false
	}

	// Получаем тело письма
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(msg.Uid)

	section := &imap.BodySectionName{}
	items := []imap.FetchItem{section.FetchItem()}

	messages := make(chan *imap.Message, 1)
	done := make(chan error, 1)

	go func() {
		done <- imapClient.UidFetch(seqSet, items, messages)
	}()

	// Таймаут для получения тела письма: 8 секунд
	timeout := time.After(8 * time.Second)

	select {
	case <-ctx.Done():
		return "", false
	case <-timeout:
		if logger.Log != nil {
			logger.Log.Debug("Таймаут получения тела письма IMAP",
				zap.String("folder", folderName),
				zap.Duration("timeout", 8*time.Second))
		}
		return "", false
	case err := <-done:
		if err != nil {
			return "", false
		}
	case msg := <-messages:
		if msg == nil {
			return "", false
		}

		// Извлекаем тело письма
		if body := msg.GetBody(section); body != nil {
			buf := new(bytes.Buffer)
			if _, err := buf.ReadFrom(body); err == nil {
				bodyText := buf.String()
				bodyLower := strings.ToLower(bodyText)

				// Проверяем наличие Message-ID в теле письма
				// Ищем Message-ID с угловыми скобками и без них
				messageIDInBody := strings.Contains(bodyText, messageIDClean) ||
					strings.Contains(bodyText, "<"+messageIDClean+">") ||
					strings.Contains(bodyText, "message-id:") && strings.Contains(bodyText, messageIDClean)

				if !messageIDInBody {
					// Message-ID не найден в теле - это не наш bounce message
					return "", false
				}

				// Ищем типичные сообщения об ошибках
				errorPatterns := []struct {
					pattern string
					desc    string
				}{
					{"550", "Адрес получателя не существует (550)"},
					{"551", "Пользователь не найден (551)"},
					{"552", "Превышен лимит почтового ящика (552)"},
					{"553", "Адрес получателя неверен (553)"},
					{"user unknown", "Пользователь не найден"},
					{"mailbox full", "Почтовый ящик переполнен"},
					{"address rejected", "Адрес отклонен"},
					{"relay denied", "Ретрансляция запрещена"},
					{"host or domain name not found", "Домен или хост не найден"},
					{"host not found", "Хост не найден"},
					{"name service error", "Ошибка службы имен"},
					{"не существует", "Адрес не существует"},
					{"не найден", "Пользователь не найден"},
					{"переполнен", "Почтовый ящик переполнен"},
					{"не может быть отправлено", "Письмо не может быть отправлено"},
				}

				for _, pattern := range errorPatterns {
					if strings.Contains(bodyLower, pattern.pattern) {
						return fmt.Sprintf("Bounce message в папке '%s': %s", folderName, pattern.desc), true
					}
				}

				// Если не нашли конкретную ошибку, возвращаем общее сообщение
				return fmt.Sprintf("Найдено bounce message о недоставке в папке '%s'", folderName), true
			}
		}
	}

	return "", false
}
