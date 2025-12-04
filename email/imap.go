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

// SaveToOutboxFolder сохраняет письмо в папку "Outbox" (Исходящие) через IMAP APPEND
// Возвращает UID сохраненного письма для последующего перемещения
func (c *IMAPClient) SaveToOutboxFolder(ctx context.Context, emailBody string) (uint32, error) {
	if c.cfg.IMAPHost == "" {
		return 0, fmt.Errorf("IMAP не настроен")
	}

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
				return 0, fmt.Errorf("ошибка STARTTLS: %w", err)
			}
		}
	}

	if err != nil {
		return 0, fmt.Errorf("ошибка подключения к IMAP: %w", err)
	}
	defer imapClient.Logout()

	// Аутентификация
	if err := imapClient.Login(c.cfg.User, c.cfg.Password); err != nil {
		return 0, fmt.Errorf("ошибка аутентификации IMAP: %w", err)
	}

	// Пробуем найти папку "Outbox" (разные варианты названий)
	outboxFolders := []string{"Outbox", "Исходящие"}
	var selectedFolder string

	for _, folderName := range outboxFolders {
		_, err := imapClient.Select(folderName, false)
		if err == nil {
			selectedFolder = folderName
			break
		}
	}

	if selectedFolder == "" {
		// Если папка не найдена, пробуем использовать INBOX
		if logger.Log != nil {
			logger.Log.Warn("Папка Outbox не найдена, пробуем использовать INBOX")
		}
		selectedFolder = "INBOX"
	}

	// Сохраняем письмо в папку через APPEND
	// emailBody уже содержит полное письмо с заголовками и телом
	messageBytes := []byte(emailBody)

	// Добавляем флаги (письмо еще не отправлено)
	flags := []string{}

	// Добавляем дату создания (текущее время)
	date := time.Now()

	// APPEND сохраняет письмо в папку
	if err := imapClient.Append(selectedFolder, flags, date, bytes.NewReader(messageBytes)); err != nil {
		return 0, fmt.Errorf("ошибка сохранения письма в папку '%s': %w", selectedFolder, err)
	}

	// Получаем UID только что сохраненного письма по Message-ID
	messageID := extractMessageID(emailBody)
	if messageID == "" {
		return 0, nil
	}

	uid, err := c.findMessageUID(imapClient, selectedFolder, messageID)
	if err != nil {
		return 0, nil
	}

	return uid, nil
}

// MoveToSentFolder перемещает письмо из папки "Outbox" в папку "Sent" по UID
func (c *IMAPClient) MoveToSentFolder(ctx context.Context, outboxUID uint32, messageID string) error {
	if c.cfg.IMAPHost == "" {
		return fmt.Errorf("IMAP не настроен")
	}

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

	// Находим папки Outbox и Sent
	outboxFolders := []string{"Outbox", "Исходящие"}
	sentFolders := []string{"Sent", "Отправленные", "Sent Items"}

	var outboxFolder, sentFolder string

	for _, folderName := range outboxFolders {
		_, err := imapClient.Select(folderName, false)
		if err == nil {
			outboxFolder = folderName
			break
		}
	}

	for _, folderName := range sentFolders {
		_, err := imapClient.Select(folderName, false)
		if err == nil {
			sentFolder = folderName
			break
		}
	}

	if outboxFolder == "" {
		return fmt.Errorf("папка Outbox не найдена")
	}
	if sentFolder == "" {
		return fmt.Errorf("папка Sent не найдена")
	}

	// Если UID неизвестен (0), ищем письмо по Message-ID
	if outboxUID == 0 {
		uid, err := c.findMessageUID(imapClient, outboxFolder, messageID)
		if err != nil {
			return fmt.Errorf("не удалось найти письмо по Message-ID '%s': %w", messageID, err)
		}
		outboxUID = uid
	}

	// Выбираем папку Outbox
	_, err = imapClient.Select(outboxFolder, false)
	if err != nil {
		return fmt.Errorf("ошибка выбора папки Outbox: %w", err)
	}

	// Создаем SeqSet с UID письма
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(outboxUID)

	// Копируем письмо в Sent используя UID
	if err := imapClient.UidCopy(seqSet, sentFolder); err != nil {
		return fmt.Errorf("ошибка копирования письма в папку Sent: %w", err)
	}

	// Помечаем оригинал как удаленный используя UID
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	flags := []interface{}{imap.DeletedFlag}
	if err := imapClient.UidStore(seqSet, item, flags, nil); err != nil {
		return fmt.Errorf("ошибка пометки письма как удаленного: %w", err)
	}

	// Удаляем помеченные письма
	if err := imapClient.Expunge(nil); err != nil {
		return fmt.Errorf("ошибка удаления письма из Outbox: %w", err)
	}

	if logger.Log != nil {
		logger.Log.Debug("Письмо перемещено из Outbox в Sent",
			zap.String("outboxFolder", outboxFolder),
			zap.String("sentFolder", sentFolder),
			zap.Uint32("uid", outboxUID),
			zap.String("messageID", messageID))
	}

	return nil
}

// findMessageUID находит UID письма по Message-ID в указанной папке
func (c *IMAPClient) findMessageUID(imapClient *client.Client, folderName, messageID string) (uint32, error) {
	// Выбираем папку
	_, err := imapClient.Select(folderName, false)
	if err != nil {
		return 0, err
	}

	messageIDClean := strings.Trim(messageID, "<>")
	messageIDForSearch := "<" + messageIDClean + ">"

	// Пробуем поиск через SEARCH
	criteria := imap.NewSearchCriteria()
	criteria.Header.Add("Message-ID", messageIDForSearch)

	ids, err := imapClient.Search(criteria)
	if err == nil && len(ids) > 0 {
		// Возвращаем первый найденный UID
		return ids[0], nil
	}

	// Если SEARCH не сработал, используем ручную проверку последних писем
	mailboxStatus, err := imapClient.Status(folderName, []imap.StatusItem{imap.StatusMessages})
	if err != nil {
		return 0, err
	}

	if mailboxStatus.Messages == 0 {
		return 0, fmt.Errorf("папка пуста")
	}

	maxMessages := uint32(50) // Проверяем последние 50 писем
	if mailboxStatus.Messages < maxMessages {
		maxMessages = mailboxStatus.Messages
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddRange(mailboxStatus.Messages-maxMessages+1, mailboxStatus.Messages)

	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid}

	messages := make(chan *imap.Message, 10)
	done := make(chan error, 1)

	go func() {
		done <- imapClient.Fetch(seqSet, items, messages)
	}()

	for {
		select {
		case err := <-done:
			if err != nil {
				return 0, err
			}
			return 0, fmt.Errorf("письмо с Message-ID '%s' не найдено", messageIDForSearch)
		case msg := <-messages:
			if msg == nil {
				continue
			}

			if msg.Envelope != nil && msg.Envelope.MessageId != "" {
				envelopeMsgID := strings.Trim(msg.Envelope.MessageId, "<>")
				if envelopeMsgID == messageIDClean {
					return msg.Uid, nil
				}
			}
		}
	}
}

// extractMessageID извлекает Message-ID из тела письма
func extractMessageID(emailBody string) string {
	lines := strings.Split(emailBody, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "message-id:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

// CheckEmailStatus проверяет статус письма по Message-ID в папках "отправленные" и bounce messages во всех папках входящих
// Возвращает status (0 - не найдено, 3 - ошибка, 4 - отправлено), описание и ошибку
func (c *IMAPClient) CheckEmailStatus(ctx context.Context, messageID string) (int, string, error) {
	if c.cfg.IMAPHost == "" {
		return 0, "", fmt.Errorf("IMAP не настроен")
	}

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
				return 0, "", fmt.Errorf("ошибка STARTTLS: %w", err)
			}
		}
	}

	if err != nil {
		return 0, "", fmt.Errorf("ошибка подключения к IMAP: %w", err)
	}
	defer imapClient.Logout()

	// Аутентификация
	if err := imapClient.Login(c.cfg.User, c.cfg.Password); err != nil {
		return 0, "", fmt.Errorf("ошибка аутентификации IMAP: %w", err)
	}

	// Сначала проверяем bounce messages во всех папках входящих (включая INBOX)
	inboxFolders, err := c.listInboxFolders(imapClient)
	if err == nil {
		// Добавляем INBOX в начало списка, так как bounce messages обычно приходят туда
		inboxFolders = append([]string{"INBOX"}, inboxFolders...)

		for _, folderName := range inboxFolders {
			bounceStatus, bounceDesc, err := c.checkBounceMessages(imapClient, folderName, messageID)
			if err == nil && bounceStatus == 3 {
				// Найдено bounce message - письмо не доставлено
				return 3, bounceDesc, nil
			}
		}
	} else {
		// Если не удалось получить список папок, проверяем хотя бы INBOX
		bounceStatus, bounceDesc, err := c.checkBounceMessages(imapClient, "INBOX", messageID)
		if err == nil && bounceStatus == 3 {
			return 3, bounceDesc, nil
		}
	}

	// Проверяем папку "отправленные" (Sent)
	sentFolders := []string{"Sent", "Отправленные", "Sent Items"}

	for _, folderName := range sentFolders {
		status, err := c.checkFolderForMessage(imapClient, folderName, messageID)
		if err == nil && status > 0 {
			// Найдено в отправленных - успешно отправлено
			return 4, fmt.Sprintf("Письмо найдено в папке '%s'", folderName), nil
		}
	}

	// Не найдено в Sent и нет bounce messages - ошибка отправки
	return 3, "Письмо не найдено в папке Sent", nil
}

// checkFolderForMessage проверяет наличие письма в указанной папке по Message-ID
func (c *IMAPClient) checkFolderForMessage(imapClient *client.Client, folderName, messageID string) (int, error) {
	// Пробуем выбрать папку
	_, err := imapClient.Select(folderName, false)
	if err != nil {
		// Папка не существует или недоступна
		return 0, err
	}

	// Ищем письмо по Message-ID
	// Message-ID может быть с угловыми скобками или без них
	// В заголовке письма он хранится как <askemailsender123@host>
	messageIDClean := strings.Trim(messageID, "<>")

	// Формируем полный Message-ID с угловыми скобками для поиска
	// IMAP SEARCH HEADER ищет точное совпадение в заголовке
	messageIDForSearch := "<" + messageIDClean + ">"

	// Пробуем поиск через SEARCH команду
	criteria := imap.NewSearchCriteria()
	criteria.Header.Add("Message-ID", messageIDForSearch)
	ids, err := imapClient.Search(criteria)

	// Если поиск не удался, используем ручную проверку
	if err != nil || len(ids) == 0 {
		// Ручная проверка: получаем последние письма и проверяем их Message-ID
		mailboxStatus, err := imapClient.Status(folderName, []imap.StatusItem{imap.StatusMessages})
		if err != nil {
			return 0, fmt.Errorf("ошибка получения статуса папки: %w", err)
		}

		if mailboxStatus.Messages == 0 {
			return 0, fmt.Errorf("папка пуста")
		}

		// Получаем последние 100 писем
		maxMessages := uint32(100)
		if mailboxStatus.Messages < maxMessages {
			maxMessages = mailboxStatus.Messages
		}

		seqSet := new(imap.SeqSet)
		seqSet.AddRange(mailboxStatus.Messages-maxMessages+1, mailboxStatus.Messages)

		items := []imap.FetchItem{imap.FetchEnvelope}
		messages := make(chan *imap.Message, 10)
		done := make(chan error, 1)

		go func() {
			done <- imapClient.Fetch(seqSet, items, messages)
		}()

		for {
			select {
			case err := <-done:
				if err != nil {
					return 0, fmt.Errorf("ошибка получения писем: %w", err)
				}
				return 0, fmt.Errorf("письмо не найдено")
			case msg := <-messages:
				if msg == nil {
					continue
				}
				if msg.Envelope != nil && msg.Envelope.MessageId != "" {
					envelopeMsgID := strings.Trim(msg.Envelope.MessageId, "<>")
					if envelopeMsgID == messageIDClean {
						return 1, nil
					}
				}
			}
		}
	}

	// Письмо найдено через SEARCH
	return 1, nil
}

// listInboxFolders получает список всех папок входящих (INBOX|*, Spam, Trash и другие корневые папки входящих)
// INBOX добавляется отдельно в CheckEmailStatus, так как bounce messages обычно приходят туда
func (c *IMAPClient) listInboxFolders(imapClient *client.Client) ([]string, error) {
	mailboxes := make(chan *imap.MailboxInfo, 10)
	done := make(chan error, 1)

	go func() {
		done <- imapClient.List("", "*", mailboxes)
	}()

	var folders []string
	for {
		select {
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
func (c *IMAPClient) checkBounceMessages(imapClient *client.Client, folderName, messageID string) (int, string, error) {
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

	for {
		select {
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
				errorDesc, found := c.extractBounceError(msg, imapClient, folderName, messageIDClean)
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
func (c *IMAPClient) extractBounceError(msg *imap.Message, imapClient *client.Client, folderName, messageIDClean string) (string, bool) {
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

	select {
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
