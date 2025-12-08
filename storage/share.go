package storage

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hirochachacha/go-smb2"
	"go.uber.org/zap"

	"email-service/logger"
	"email-service/settings"
)

// CIFSManager управляет пулом подключений к SMB-шарам
type CIFSManager struct {
	clients map[string]*CIFSClient // key: "server:share"
	mu      sync.RWMutex
	cfg     *settings.ShareConfig
}

// NewCIFSManager создает новый менеджер подключений
func NewCIFSManager(cfg *settings.ShareConfig) *CIFSManager {
	return &CIFSManager{
		clients: make(map[string]*CIFSClient),
		cfg:     cfg,
	}
}

// CIFSClient предоставляет методы для подключения к SMB/CIFS шаре и работы с файлами
type CIFSClient struct {
	Address   string
	Username  string
	Password  string
	ShareName string
	Port      string
	Domain    string

	conn     net.Conn
	session  *smb2.Session
	fs       *smb2.Share
	lastUsed time.Time
	useCount int
}

// NewCIFSClient — инициализация клиента (конструктор)
func NewCIFSClient(address string, shareName string, cfg *settings.ShareConfig) *CIFSClient {
	return &CIFSClient{
		Address:   address,
		Username:  cfg.Username,
		Password:  cfg.Password,
		ShareName: shareName,
		Port:      cfg.Port,
		Domain:    cfg.Domain,
		lastUsed:  time.Now(),
	}
}

// ==================== CIFSManager методы ====================

// GetClient возвращает клиент для указанного сервера и шары
func (m *CIFSManager) GetClient(ctx context.Context, server, share string) (*CIFSClient, error) {
	key := server + ":" + share

	if logger.Log != nil {
		logger.Log.Debug("Получение CIFS клиента",
			zap.String("server", server),
			zap.String("share", share))
	}

	// Пытаемся получить существующее подключение
	m.mu.RLock()
	client, exists := m.clients[key]
	m.mu.RUnlock()

	if exists && client != nil {
		if err := client.EnsureConnected(); err == nil {
			client.MarkUsed()
			return client, nil
		}
		// Соединение мертво, удаляем из кэша
		m.mu.Lock()
		delete(m.clients, key)
		m.mu.Unlock()
		client.Disconnect()
	}

	// Создаем новое соединение
	m.mu.Lock()
	defer m.mu.Unlock()

	// Двойная проверка на случай конкуренции
	if client, exists := m.clients[key]; exists && client != nil {
		if err := client.EnsureConnected(); err == nil {
			client.MarkUsed()
			return client, nil
		}
		delete(m.clients, key)
	}

	client = NewCIFSClient(server, share, m.cfg)
	if err := client.ConnectWithRetry(3); err != nil {
		return nil, fmt.Errorf("не удалось подключиться к %s: %w", key, err)
	}

	m.clients[key] = client
	return client, nil
}

// Close закрывает все подключения
func (m *CIFSManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, client := range m.clients {
		if client != nil {
			client.Disconnect()
		}
		delete(m.clients, key)
	}
	if logger.Log != nil {
		logger.Log.Info("CIFSManager: все подключения закрыты")
	}
}

// CleanupIdleConnections очищает неиспользуемые подключения
func (m *CIFSManager) CleanupIdleConnections(maxIdleTime time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, client := range m.clients {
		if client != nil && time.Since(client.lastUsed) > maxIdleTime {
			if logger.Log != nil {
				logger.Log.Debug("CIFSManager: очистка неиспользуемого подключения",
					zap.String("key", key))
			}
			client.Disconnect()
			delete(m.clients, key)
		}
	}
}

// ==================== CIFSClient методы ====================

// MarkUsed обновляет время последнего использования
func (c *CIFSClient) MarkUsed() {
	c.lastUsed = time.Now()
	c.useCount++
	if logger.Log != nil {
		logger.Log.Debug("CIFSClient: обновлено время использования",
			zap.String("address", c.Address),
			zap.Int("useCount", c.useCount))
	}
}

// Connect — подключение к серверу и монтирование шары
func (c *CIFSClient) Connect() error {
	// Поддержка случаев, когда в Address приходит UNC вида "\\server\share".
	host := c.Address
	shareUNC := c.ShareName

	// Если Address начинается с UNC, пытаемся распарсить "\\server\share" -> host="server", share="\\server\share"
	if strings.HasPrefix(host, `\\`) || strings.HasPrefix(host, `//`) {
		trimmed := strings.TrimLeft(host, `\/`)
		parts := strings.SplitN(trimmed, `\`, 3)
		if len(parts) >= 1 && parts[0] != "" {
			host = parts[0]
		}
		// Если в Address был указан share, а ShareName пуст или похож на домен — используем Address как UNC для монтирования
		if len(parts) >= 2 {
			share := parts[1]
			shareUNC = `\\` + parts[0] + `\` + share
		}
	}

	// Нормализуем хост для TCP-диала: убираем ведущие слэши, оставляем только имя/адрес хоста
	host = strings.TrimLeft(host, `\/`)
	if host == "" {
		return fmt.Errorf("не указан хост SMB-сервера")
	}

	// Устанавливаем соединение с сервером
	addr := net.JoinHostPort(host, c.Port)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("ошибка подключения к серверу %s: %w", host, err)
	}
	c.conn = conn

	// Настраиваем аутентификацию (учитываем домен, если задан)
	d := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			Domain:   c.Domain,
			User:     c.Username,
			Password: c.Password,
		},
	}

	// Аутентифицируемся на сервере
	s, err := d.Dial(conn)
	if err != nil {
		_ = conn.Close()
		c.conn = nil
		return fmt.Errorf("ошибка аутентификации: %w", err)
	}
	c.session = s

	// Готовим путь шары:
	// - Если shareUNC уже в формате UNC (начинается с "\\") — используем как есть.
	// - Если нет — считаем, что это имя шары и собираем UNC на основе host.
	if shareUNC == "" {
		return fmt.Errorf("не указано имя шары/UNC для монтирования")
	}
	if !(strings.HasPrefix(shareUNC, `\\`) || strings.HasPrefix(shareUNC, `//`)) {
		shareUNC = `\\` + host + `\` + strings.TrimLeft(shareUNC, `\/`)
	}

	// Монтируем шару
	fs, err := s.Mount(shareUNC)
	if err != nil {
		_ = s.Logoff()
		c.session = nil
		_ = conn.Close()
		c.conn = nil
		return fmt.Errorf("ошибка монтирования общей папки %s: %w", shareUNC, err)
	}
	c.fs = fs

	if logger.Log != nil {
		logger.Log.Info("Успешно подключено к CIFS шаре",
			zap.String("shareUNC", shareUNC),
			zap.String("domain", c.Domain),
			zap.String("username", c.Username))
	}
	return nil
}

// EnsureConnected проверяет и восстанавливает подключение при необходимости
func (c *CIFSClient) EnsureConnected() error {
	if c.fs == nil {
		return c.ConnectWithRetry(3)
	}

	// Простая проверка - пытаемся прочитать корень
	_, err := c.fs.ReadDir(".")
	if err != nil {
		if logger.Log != nil {
			logger.Log.Warn("Соединение разорвано, переподключаемся...")
		}
		c.Disconnect()
		return c.ConnectWithRetry(3)
	}

	return nil
}

func (c *CIFSClient) readyFS() (*smb2.Share, error) {
	if c.fs != nil {
		// быстрая проверка «живости» – читаем корень
		if _, err := c.fs.ReadDir("."); err == nil {
			return c.fs, nil
		}
	}
	// соединение мертво – переподключаемся
	if logger.Log != nil {
		logger.Log.Warn("readyFS: соединение разорвано, переподключаемся...")
	}
	c.Disconnect()
	if err := c.ConnectWithRetry(5); err != nil {
		return nil, fmt.Errorf("readyFS: не удалось восстановить соединение: %w", err)
	}
	return c.fs, nil
}

// ConnectWithRetry подключается с повторными попытками
func (c *CIFSClient) ConnectWithRetry(maxRetries int) error {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if err := c.Connect(); err != nil {
			lastErr = err
			if logger.Log != nil {
				logger.Log.Warn("Попытка подключения к CIFS не удалась",
					zap.Int("attempt", i+1),
					zap.Int("maxRetries", maxRetries),
					zap.Error(err))
			}

			// Если это EOF ошибка, ждем и пробуем снова
			if strings.Contains(err.Error(), "EOF") && i < maxRetries-1 {
				waitTime := time.Duration(i+1) * time.Second
				if logger.Log != nil {
					logger.Log.Info("Ждем перед повторной попыткой подключения",
						zap.Duration("waitTime", waitTime))
				}
				time.Sleep(waitTime)
				continue
			}
			return err
		}
		if logger.Log != nil {
			logger.Log.Info("Успешное подключение к CIFS шаре",
				zap.String("shareName", c.ShareName))
		}
		return nil
	}
	return fmt.Errorf("не удалось подключиться после %d попыток: %w", maxRetries, lastErr)
}

// Disconnect — размонтирование шары и закрытие соединений
func (c *CIFSClient) Disconnect() {
	if c.fs != nil {
		_ = c.fs.Umount()
		c.fs = nil
	}
	if c.session != nil {
		_ = c.session.Logoff()
		c.session = nil
	}
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	if logger.Log != nil {
		logger.Log.Debug("Подключение к CIFS шаре закрыто",
			zap.String("shareName", c.ShareName))
	}
}

// ==================== Файловые операции ====================

// OpenFile открывает файл на шаре для чтения
func (c *CIFSClient) OpenFile(filePath string) (io.ReadCloser, error) {
	const maxTries = 2
	for attempt := 0; attempt < maxTries; attempt++ {
		file, err := c.openFileOnce(filePath)
		if err != nil && attempt == 0 {
			if _, rerr := c.readyFS(); rerr != nil {
				return nil, rerr
			}
			continue
		}
		return file, err
	}
	return nil, fmt.Errorf("не удалось открыть файл после %d попыток", maxTries)
}

func (c *CIFSClient) openFileOnce(filePath string) (io.ReadCloser, error) {
	if c.fs == nil {
		return nil, fmt.Errorf("шара не смонтирована: вызовите Connect() перед OpenFile")
	}

	file, err := c.fs.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("ошибка открытия файла %s: %w", filePath, err)
	}

	return file, nil
}

// FileExists проверяет существует ли файл на шаре
func (c *CIFSClient) FileExists(filePath string) (bool, error) {
	if c.fs == nil {
		return false, fmt.Errorf("шара не смонтирована: вызовите Connect() перед FileExists")
	}

	_, err := c.fs.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("ошибка проверки файла %s: %w", filePath, err)
	}

	return true, nil
}

// ReadFile читает файл с шары полностью
func (c *CIFSClient) ReadFile(filePath string) ([]byte, error) {
	file, err := c.OpenFile(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения файла %s: %w", filePath, err)
	}

	return data, nil
}

// ParseUNCPath парсит UNC путь и возвращает сервер, шару и относительный путь
// Пример: \\192.168.87.31\shares$\esig_docs\OBN\SEMD\2021\08\ASK_6365065_1.XML
// Возвращает: server="192.168.87.31", share="shares$", path="esig_docs\OBN\SEMD\2021\08\ASK_6365065_1.XML"
func ParseUNCPath(uncPath string) (server, share, relPath string, err error) {
	// Нормализуем путь: убираем ведущие слэши
	trimmed := strings.TrimLeft(uncPath, `\/`)
	if trimmed == "" {
		return "", "", "", fmt.Errorf("пустой UNC путь")
	}

	// Разбиваем на части
	parts := strings.SplitN(trimmed, `\`, 3)
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf("неверный формат UNC пути: %s", uncPath)
	}

	server = parts[0]
	share = parts[1]
	if len(parts) >= 3 {
		relPath = parts[2]
	}

	return server, share, relPath, nil
}
