package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"sync"
	"time"

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

// CheckEmailStatus проверяет статус письма по Message-ID в папках "исходящие" и "отправленные"
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

	// Список возможных названий папок для исходящих и отправленных
	outboxFolders := []string{"Исходящие", "Outbox"}
	sentFolders := []string{"Отправленные", "Sent"}

	// Проверяем папку "исходящие" (Outbox)
	for _, folderName := range outboxFolders {
		status, err := c.checkFolderForMessage(imapClient, folderName, messageID)
		if err == nil && status > 0 {
			if status == 3 {
				// Найдено в исходящих с ошибкой
				return 3, fmt.Sprintf("Письмо найдено в папке '%s' с ошибкой отправки", folderName), nil
			}
			// Найдено в исходящих без ошибки - еще не отправлено
			return 0, "", nil
		}
	}

	// Проверяем папку "отправленные" (Sent)
	for _, folderName := range sentFolders {
		status, err := c.checkFolderForMessage(imapClient, folderName, messageID)
		if err == nil && status > 0 {
			// Найдено в отправленных - успешно отправлено
			return 4, fmt.Sprintf("Письмо найдено в папке '%s'", folderName), nil
		}
	}

	// Не найдено ни в одной папке
	return 0, "", nil
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
	messageIDClean := strings.Trim(messageID, "<>")
	criteria := imap.NewSearchCriteria()
	criteria.Header.Add("Message-ID", messageIDClean)

	ids, err := imapClient.Search(criteria)
	if err != nil {
		return 0, err
	}

	if len(ids) == 0 {
		return 0, fmt.Errorf("письмо не найдено")
	}

	// Получаем письмо для проверки на наличие ошибок
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(ids...)

	section := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, section.FetchItem()}

	messages := make(chan *imap.Message, 1)
	done := make(chan error, 1)

	go func() {
		done <- imapClient.Fetch(seqSet, items, messages)
	}()

	select {
	case err := <-done:
		if err != nil {
			return 0, err
		}
	case msg := <-messages:
		if msg == nil {
			return 0, fmt.Errorf("письмо не получено")
		}

		// Проверяем наличие ошибок в заголовках или теле письма
		// Если есть ошибки отправки, возвращаем статус 3
		// Пока что просто возвращаем 1 (найдено)
		return 1, nil
	}

	return 0, fmt.Errorf("письмо не найдено")
}
