package email

import (
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

// SaveToSentFolder сохраняет письмо в папку "Sent" через IMAP APPEND
func (c *IMAPClient) SaveToSentFolder(ctx context.Context, emailBody string) error {
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

	// Пробуем найти папку "Sent" (разные варианты названий)
	sentFolders := []string{"Sent", "Отправленные", "Sent Items"}
	var selectedFolder string
	
	for _, folderName := range sentFolders {
		_, err := imapClient.Select(folderName, false)
		if err == nil {
			selectedFolder = folderName
			break
		}
	}

	if selectedFolder == "" {
		// Если папка не найдена, пробуем создать или использовать INBOX
		if logger.Log != nil {
			logger.Log.Warn("Папка Sent не найдена, пробуем использовать INBOX")
		}
		selectedFolder = "INBOX"
	}

	// Сохраняем письмо в папку через APPEND
	// emailBody уже содержит полное письмо с заголовками и телом
	messageLiteral := imap.NewLiteral([]byte(emailBody))
	
	// Добавляем флаги (например, \Seen - прочитано, но для Sent обычно не нужно)
	flags := []string{}
	
	// Добавляем дату отправки (текущее время)
	date := time.Now()
	
	if err := imapClient.Append(selectedFolder, flags, date, messageLiteral); err != nil {
		return fmt.Errorf("ошибка сохранения письма в папку '%s': %w", selectedFolder, err)
	}

	if logger.Log != nil {
		logger.Log.Debug("Письмо сохранено в папку Sent",
			zap.String("folder", selectedFolder))
	}

	return nil
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
	// Для Yandex обычно используются: "Отправленные" (Sent), "Исходящие" (Outbox)
	// Но на английском могут быть: "Sent", "Outbox", "Sent Items"
	outboxFolders := []string{"Outbox", "Исходящие"}
	sentFolders := []string{"Sent", "Отправленные", "Sent Items"}

	// Логируем Message-ID для отладки
	if logger.Log != nil {
		logger.Log.Debug("Поиск письма по Message-ID",
			zap.String("messageID", messageID),
			zap.String("messageIDClean", strings.Trim(messageID, "<>")),
			zap.String("messageIDForSearch", "<"+strings.Trim(messageID, "<>")+">"))
	}

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
		// Логируем ошибки для отладки (кроме "папка не существует")
		if err != nil && logger.Log != nil {
			if !strings.Contains(err.Error(), "не существует") && !strings.Contains(err.Error(), "not found") {
				logger.Log.Debug("Ошибка проверки папки исходящих",
					zap.String("folder", folderName),
					zap.Error(err))
			}
		}
	}

	// Проверяем папку "отправленные" (Sent)
	for _, folderName := range sentFolders {
		status, err := c.checkFolderForMessage(imapClient, folderName, messageID)
		if err == nil && status > 0 {
			// Найдено в отправленных - успешно отправлено
			return 4, fmt.Sprintf("Письмо найдено в папке '%s'", folderName), nil
		}
		// Логируем ошибки для отладки (кроме "папка не существует")
		if err != nil && logger.Log != nil {
			if !strings.Contains(err.Error(), "не существует") && !strings.Contains(err.Error(), "not found") {
				logger.Log.Debug("Ошибка проверки папки отправленных",
					zap.String("folder", folderName),
					zap.Error(err))
			}
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
	// В заголовке письма он хранится как <askemailsender123@host>
	messageIDClean := strings.Trim(messageID, "<>")

	// Формируем полный Message-ID с угловыми скобками для поиска
	// IMAP SEARCH HEADER ищет точное совпадение в заголовке
	messageIDForSearch := "<" + messageIDClean + ">"

	// Логируем для отладки
	if logger.Log != nil {
		logger.Log.Debug("Поиск письма в папке",
			zap.String("folder", folderName),
			zap.String("messageIDOriginal", messageID),
			zap.String("messageIDClean", messageIDClean),
			zap.String("messageIDForSearch", messageIDForSearch))
	}

	// Пробуем поиск через SEARCH команду
	// Если не работает (например, "SEARCH Backend error" от Yandex), используем ручную проверку
	var ids []uint32
	var searchErr error

	// Вариант 1: Поиск по HEADER с угловыми скобками
	criteria := imap.NewSearchCriteria()
	criteria.Header.Add("Message-ID", messageIDForSearch)
	ids, searchErr = imapClient.Search(criteria)

	// Если поиск не удался из-за ошибки сервера, переходим к ручной проверке
	if searchErr != nil || len(ids) == 0 {
		if searchErr != nil {
			if logger.Log != nil {
				logger.Log.Debug("SEARCH команда не работает, используем ручную проверку",
					zap.String("folder", folderName),
					zap.Error(searchErr))
			}
		}

		// Ручная проверка: получаем последние письма и проверяем их Message-ID
		mailboxStatus, err := imapClient.Status(folderName, []imap.StatusItem{imap.StatusMessages})
		if err != nil {
			return 0, fmt.Errorf("ошибка получения статуса папки: %w", err)
		}

		if mailboxStatus.Messages == 0 {
			return 0, fmt.Errorf("папка пуста")
		}

		// Получаем последние 100 писем (достаточно для недавно отправленных)
		maxMessages := uint32(100)
		if mailboxStatus.Messages < maxMessages {
			maxMessages = mailboxStatus.Messages
		}

		seqSet := new(imap.SeqSet)
		seqSet.AddRange(mailboxStatus.Messages-maxMessages+1, mailboxStatus.Messages)

		// Получаем только Envelope (быстрее, чем полное тело)
		items := []imap.FetchItem{imap.FetchEnvelope}

		messages := make(chan *imap.Message, 10)
		done := make(chan error, 1)

		go func() {
			done <- imapClient.Fetch(seqSet, items, messages)
		}()

		// Проверяем каждое письмо
		for {
			select {
			case err := <-done:
				if err != nil {
					return 0, fmt.Errorf("ошибка получения писем: %w", err)
				}
				// Все письма проверены, не найдено
				return 0, fmt.Errorf("письмо не найдено в папке '%s' по Message-ID '%s'", folderName, messageIDForSearch)
			case msg := <-messages:
				if msg == nil {
					continue
				}

				// Проверяем Message-ID из Envelope
				if msg.Envelope != nil && msg.Envelope.MessageId != "" {
					envelopeMsgID := strings.Trim(msg.Envelope.MessageId, "<>")
					if envelopeMsgID == messageIDClean {
						if logger.Log != nil {
							logger.Log.Debug("Письмо найдено через проверку Envelope",
								zap.String("folder", folderName),
								zap.String("messageID", messageIDClean))
						}
						return 1, nil
					}
				}
			}
		}
	}

	// SEARCH сработал, письмо найдено
	if logger.Log != nil {
		logger.Log.Debug("Письмо найдено через SEARCH",
			zap.String("folder", folderName),
			zap.String("messageID", messageIDForSearch),
			zap.Int("foundCount", len(ids)))
	}

	// Письмо найдено через SEARCH, возвращаем успех
	return 1, nil
}
