package email

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"email-service/settings"
)

// POP3Client представляет POP3 клиент для получения статусов доставки
type POP3Client struct {
	cfg            *settings.SMTPConfig
	lastStatusTime time.Time
	mu             sync.Mutex
}

// NewPOP3Client создает новый POP3 клиент
func NewPOP3Client(cfg *settings.SMTPConfig) *POP3Client {
	return &POP3Client{
		cfg:            cfg,
		lastStatusTime: time.Now().Add(-24 * time.Hour), // Начинаем с вчерашнего дня
	}
}


// CheckEmailStatus проверяет статус письма по Message-ID в POP3 почтовом ящике
// POP3 не поддерживает папки, поэтому проверяем все сообщения
// Возвращает status (0 - не найдено, 3 - ошибка, 4 - отправлено), описание и ошибку
func (c *POP3Client) CheckEmailStatus(ctx context.Context, messageID string) (int, string, error) {
	if c.cfg.POPHost == "" {
		return 0, "", fmt.Errorf("POP3 не настроен")
	}

	// Создаем POP3 клиент
	client := &pop3Client{
		host: c.cfg.POPHost,
		port: c.cfg.POPPort,
		user: c.cfg.User,
		pass: c.cfg.Password,
	}

	// Подключаемся
	if err := client.connect(ctx); err != nil {
		return 0, "", fmt.Errorf("ошибка подключения к POP3: %w", err)
	}
	defer client.quit()

	// Аутентификация
	if err := client.auth(); err != nil {
		return 0, "", fmt.Errorf("ошибка аутентификации POP3: %w", err)
	}

	// Получаем количество сообщений
	total, err := client.stat()
	if err != nil {
		return 0, "", fmt.Errorf("ошибка получения статуса POP3: %w", err)
	}

	// Ищем письмо по Message-ID во всех сообщениях
	for i := total; i > 0; i-- {
		select {
		case <-ctx.Done():
			return 0, "", ctx.Err()
		default:
		}

		// Получаем сообщение
		messageData, err := client.retr(i)
		if err != nil {
			continue
		}

		// Ищем Message-ID в заголовках
		messageStr := string(messageData)
		if strings.Contains(messageStr, "Message-ID:") {
			// Извлекаем Message-ID из заголовков
			lines := strings.Split(messageStr, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(strings.ToLower(line), "message-id:") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 {
						foundMessageID := strings.TrimSpace(parts[1])
						// Убираем угловые скобки если есть
						foundMessageID = strings.Trim(foundMessageID, "<>")
						messageIDClean := strings.Trim(messageID, "<>")
						
						if strings.Contains(foundMessageID, messageIDClean) || strings.Contains(messageIDClean, foundMessageID) {
							// Найдено письмо
							// Проверяем наличие ошибок в теле сообщения
							bodyStr := strings.ToLower(messageStr)
							if strings.Contains(bodyStr, "error") || strings.Contains(bodyStr, "ошибка") ||
								strings.Contains(bodyStr, "failed") || strings.Contains(bodyStr, "не удалось") {
								return 3, "Письмо найдено с ошибкой отправки", nil
							}
							// Письмо найдено без ошибок - считаем отправленным
							return 4, "Письмо найдено в почтовом ящике", nil
						}
					}
				}
			}
		}
	}

	// Не найдено
	return 0, "", nil
}

// pop3Client представляет простой POP3 клиент
type pop3Client struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
	host   string
	port   int
	user   string
	pass   string
}

// connect подключается к POP3 серверу
func (c *pop3Client) connect(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", c.host, c.port)

	var conn net.Conn
	var err error

	if c.port > 110 {
		// SSL/TLS соединение
		dialer := &tls.Dialer{
			Config: &tls.Config{
				ServerName:         c.host,
				InsecureSkipVerify: false,
			},
		}
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	} else {
		// Обычное соединение
		d := net.Dialer{Timeout: 30 * time.Second}
		conn, err = d.DialContext(ctx, "tcp", addr)
	}

	if err != nil {
		return err
	}

	c.conn = conn
	c.reader = bufio.NewReader(conn)
	c.writer = bufio.NewWriter(conn)

	// Читаем приветствие
	line, err := c.reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return fmt.Errorf("ошибка чтения приветствия: %w", err)
	}

	if !strings.HasPrefix(line, "+OK") {
		conn.Close()
		return fmt.Errorf("неожиданный ответ сервера: %s", strings.TrimSpace(line))
	}

	return nil
}

// auth выполняет аутентификацию
func (c *pop3Client) auth() error {
	// USER команда
	if err := c.command("USER %s", c.user); err != nil {
		return fmt.Errorf("ошибка отправки команды USER: %w", err)
	}

	// Читаем ответ на USER
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("ошибка чтения ответа на USER: %w", err)
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "+OK") {
		return fmt.Errorf("ошибка аутентификации USER: %s", line)
	}

	// PASS команда
	if err := c.command("PASS %s", c.pass); err != nil {
		return fmt.Errorf("ошибка отправки команды PASS: %w", err)
	}

	// Читаем ответ на PASS
	line, err = c.reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("ошибка чтения ответа на PASS: %w", err)
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "+OK") {
		return fmt.Errorf("ошибка аутентификации PASS: %s", line)
	}

	return nil
}

// stat получает количество сообщений
func (c *pop3Client) stat() (int, error) {
	line, err := c.commandRead("STAT")
	if err != nil {
		return 0, fmt.Errorf("ошибка чтения ответа STAT: %w", err)
	}

	// Проверяем, что ответ начинается с +OK
	if !strings.HasPrefix(line, "+OK") {
		return 0, fmt.Errorf("сервер вернул ошибку на команду STAT: %s", line)
	}

	// Формат: +OK count size
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0, fmt.Errorf("неверный формат ответа STAT (ожидается '+OK count size'): %s", line)
	}

	count, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("ошибка парсинга количества сообщений из '%s': %w", line, err)
	}

	return count, nil
}

// retr получает сообщение по индексу
func (c *pop3Client) retr(index int) ([]byte, error) {
	line, err := c.commandRead("RETR %d", index)
	if err != nil {
		return nil, err
	}

	if !strings.HasPrefix(line, "+OK") {
		return nil, fmt.Errorf("ошибка RETR: %s", line)
	}

	// Читаем сообщение до точки на отдельной строке
	var messageData []byte
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("ошибка чтения сообщения: %w", err)
		}

		// Конец сообщения - точка на отдельной строке
		if strings.TrimSpace(line) == "." {
			break
		}

		// Убираем точку-префикс если есть
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}

		messageData = append(messageData, []byte(line)...)
	}

	return messageData, nil
}

// deleteMsgId удаляет сообщение по индексу
func (c *pop3Client) deleteMsgId(index int) error {
	_, err := c.commandRead("DELETE %d", index)
	return err
}

// quit закрывает соединение
func (c *pop3Client) quit() error {
	if c.conn != nil {
		c.command("QUIT")
		c.conn.Close()
	}
	return nil
}

// command отправляет команду
func (c *pop3Client) command(format string, args ...interface{}) error {
	cmd := fmt.Sprintf(format, args...)
	if _, err := c.writer.WriteString(cmd + "\r\n"); err != nil {
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}
	return nil
}

// commandRead отправляет команду и читает ответ
func (c *pop3Client) commandRead(format string, args ...interface{}) (string, error) {
	if err := c.command(format, args...); err != nil {
		return "", err
	}

	line, err := c.reader.ReadString('\n')
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(line), nil
}
