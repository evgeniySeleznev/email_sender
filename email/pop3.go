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

	"go.uber.org/zap"

	"email-service/logger"
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

// GetMessagesStatus получает статусы доставки из POP3 почтового ящика
func (c *POP3Client) GetMessagesStatus(ctx context.Context, sourceEmail string, processDSN func(taskID int64, status int, statusDesc string)) error {
	if c.cfg.POPHost == "" {
		return nil // POP3 не настроен
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	startTime := time.Now()

	logger.Log.Info("Получение статусов доставки",
		zap.String("popHost", c.cfg.POPHost),
		zap.Int("popPort", c.cfg.POPPort))

	// Создаем POP3 клиент
	client := &pop3Client{
		host: c.cfg.POPHost,
		port: c.cfg.POPPort,
		user: c.cfg.User,
		pass: c.cfg.Password,
	}

	// Подключаемся
	if err := client.connect(ctx); err != nil {
		return fmt.Errorf("ошибка подключения к POP3: %w", err)
	}
	defer client.quit()

	// Аутентификация
	if err := client.auth(); err != nil {
		return fmt.Errorf("ошибка аутентификации POP3: %w", err)
	}

	// Получаем количество сообщений
	total, err := client.stat()
	if err != nil {
		return fmt.Errorf("ошибка получения статуса POP3: %w", err)
	}

	processed := 0

	// Обрабатываем сообщения с конца (новые сначала)
	for i := total; i > 0; i-- {
		// Проверяем контекст
		select {
		case <-ctx.Done():
			logger.Log.Info("Прерывание получения статусов из-за отмены контекста")
			goto done
		default:
		}

		// Ограничение на количество обрабатываемых сообщений
		if total-i > 1000 {
			logger.Log.Debug("Достигнут лимит обработки сообщений (1000)")
			break
		}

		// Получаем сообщение
		messageData, err := client.retr(i)
		if err != nil {
			logger.Log.Debug("Ошибка получения сообщения", zap.Int("index", i), zap.Error(err))
			continue
		}

		// Парсим MIME сообщение
		msg, err := ParseMIMEMessage(messageData)
		if err != nil {
			logger.Log.Debug("Ошибка парсинга MIME сообщения", zap.Int("index", i), zap.Error(err))
			continue
		}

		// Проверяем дату сообщения
		if msg.Date != nil && msg.Date.Before(c.lastStatusTime) {
			logger.Log.Debug("Завершение чтения статусов. Дата сообщения раньше последней обработки",
				zap.Time("messageDate", *msg.Date),
				zap.Time("lastStatusTime", c.lastStatusTime))
			break
		}

		// Обрабатываем DSN
		taskID, status, statusDesc := ProcessDeliveryStatusNotification(sourceEmail, msg)
		if taskID > 0 && status > 0 {
			logger.Log.Debug("Получен статус доставки",
				zap.Int64("taskID", taskID),
				zap.Int("status", status),
				zap.String("description", statusDesc))

			// Вызываем callback для обработки статуса
			processDSN(taskID, status, statusDesc)

			processed++

			// Удаляем обработанное сообщение
			if err := client.deleteMsgId(i); err != nil {
				logger.Log.Warn("Ошибка удаления сообщения", zap.Int("index", i), zap.Error(err))
			}
		}
	}

done:
	c.lastStatusTime = startTime

	logger.Log.Info("Получено статусов доставки",
		zap.String("popHost", c.cfg.POPHost),
		zap.Int("processed", processed),
		zap.Int("total", total))

	return nil
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
		return err
	}

	// PASS команда
	if err := c.command("PASS %s", c.pass); err != nil {
		return err
	}

	return nil
}

// stat получает количество сообщений
func (c *pop3Client) stat() (int, error) {
	line, err := c.commandRead("STAT")
	if err != nil {
		return 0, err
	}

	// Формат: +OK count size
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0, fmt.Errorf("неверный формат ответа STAT: %s", line)
	}

	count, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("ошибка парсинга количества сообщений: %w", err)
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
