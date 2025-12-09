package storage

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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
func (m *CIFSManager) GetClient(ctx context.Context, server, share string, sharePath string) (*CIFSClient, error) {
	key := server + ":" + share + ":" + sharePath + ":" + uuid.New().String()

	if logger.Log != nil {
		logger.Log.Info("GetClient",
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
				logger.Log.Info("CIFSManager: очистка неиспользуемого подключения",
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
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("ошибка подключения к серверу %s: %w", host, err)
	}
	c.conn = conn

	// Настраиваем аутентификацию (учитываем домен, если задан)
	// Используем настройки из секции [share] в settings.ini:
	// CIFSUSERNAME -> c.Username
	// CIFSPASSWORD -> c.Password
	// CIFSDOMEN -> c.Domain
	// CIFSPORT -> c.Port (уже использован выше для TCP подключения)
	if logger.Log != nil {
		logger.Log.Debug("Использование учетных данных для CIFS подключения",
			zap.String("host", host),
			zap.String("username", c.Username),
			zap.String("domain", c.Domain),
			zap.String("port", c.Port),
			zap.Bool("hasPassword", c.Password != ""))
	}
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
func (c *CIFSClient) ReadFile(dirPath string) ([]string, error) {
	const maxTries = 2
	for attempt := 0; attempt < maxTries; attempt++ {
		paths, err := c.readFileOnce(dirPath)
		if err != nil && attempt == 0 {
			if _, rerr := c.readyFS(); rerr != nil {
				return nil, rerr
			}
			continue
		}
		return paths, err
	}
	return nil, nil
}

// ReadFile читает файлы из директории на шаре
func (c *CIFSClient) readFileOnce(dirPath string) ([]string, error) {
	if c.fs == nil {
		return nil, fmt.Errorf("шара не смонтирована: вызовите Connect() перед ReadFile")
	}

	entries, err := c.fs.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения папки %s на шаре: %w", dirPath, err)
	}

	const workers = 64
	sem := make(chan struct{}, workers)
	type result struct {
		path string
	}

	results := make([]result, 0, len(entries))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		wg.Add(1)

		go func(n string) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			full := filepath.Join(dirPath, n)
			st, err := c.fs.Stat(full)
			if err != nil || st.IsDir() {
				return
			}

			mu.Lock()
			results = append(results, result{path: full})
			mu.Unlock()
		}(name)
	}
	wg.Wait()

	paths := make([]string, 0, len(results))
	for _, r := range results {
		paths = append(paths, r.path)
	}

	if logger.Log != nil {
		logger.Log.Info("Найдено файлов в папке",
			zap.Int("count", len(paths)),
			zap.String("dirPath", dirPath))
	}
	return paths, nil
}
func (c *CIFSClient) FindFileBySuffix(dirPath, suffix string) (string, bool, error) {
	if c.fs == nil {
		return "", false, fmt.Errorf("шара не смонтирована")
	}

	entries, err := c.fs.ReadDir(dirPath)
	if err != nil {
		return "", false, fmt.Errorf("read dir: %w", err)
	}

	for _, e := range entries {
		name := e.Name()
		if logger.Log != nil {
			logger.Log.Debug("Проверка файла",
				zap.String("name", name),
				zap.Bool("isDir", e.IsDir()))
		}
		if !e.IsDir() && strings.HasSuffix(name, suffix) {
			full := path.Join(dirPath, name)
			return full, true, nil
		}
	}
	return "", false, nil
}

var writeSema = make(chan struct{}, 50)

// WriteStream записывает данные из io.Reader на шару
func (c *CIFSClient) WriteStream(destPath string, src io.Reader) (int64, error) {
	const maxTries = 2
	for attempt := 0; attempt < maxTries; attempt++ {
		written, err := c.writeStreamOnce(destPath, src)
		if err != nil {
			if attempt == 0 {
				if logger.Log != nil {
					logger.Log.Warn("WriteStream: ошибка ввода-вывода, пробуем восстановить соединение")
				}
				if _, rerr := c.readyFS(); rerr != nil {
					return 0, rerr
				}
				continue
			}
			return 0, err
		}
		return written, nil
	}
	return 0, nil
}

// writeStreamOnce потоковая запись из io.Reader
func (c *CIFSClient) writeStreamOnce(destPath string, src io.Reader) (int64, error) {
	writeSema <- struct{}{}
	defer func() { <-writeSema }()

	if c.fs == nil {
		return 0, fmt.Errorf("шара не смонтирована: вызовите Connect() перед WriteStream")
	}

	dir := parentDirFromSMBPath(destPath)
	if err := c.ensureRemoteDirAll(dir); err != nil {
		return 0, err
	}

	cleanPath := strings.ReplaceAll(destPath, `\`, `/`)
	remoteDir := path.Dir(cleanPath)
	fileName := path.Base(cleanPath)

	suffix := fileName
	if idx := strings.LastIndex(fileName, "_"); idx != -1 {
		suffix = fileName[idx+1:]
	}

	if logger.Log != nil {
		logger.Log.Info("Запись файла на шару",
			zap.String("remoteDir", remoteDir))
	}
	if existing, found, err := c.FindFileBySuffix(remoteDir, suffix); err != nil {
		return 0, fmt.Errorf("ошибка поиска существующего файла: %w", err)
	} else if found {
		if logger.Log != nil {
			logger.Log.Warn("Файл уже существует, пропускаем",
				zap.String("file", existing))
		}
		return 0, nil
	}

	dst, err := c.fs.OpenFile(destPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return 0, fmt.Errorf("не удалось создать файл на шаре %s: %w", destPath, err)
	}
	defer dst.Close()

	buf := make([]byte, 32*1024)
	written, err := io.CopyBuffer(dst, src, buf)
	if err != nil {
		return 0, fmt.Errorf("ошибка потоковой записи на шару: %w", err)
	}
	time.Sleep(50 * time.Millisecond)
	return written, nil
}

func (c *CIFSClient) OpenFile(filePath string) (io.ReadCloser, error) {
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

// GetFileSize возвращает размер файла на шаре
func (c *CIFSClient) GetFileSize(filePath string) (int64, error) {
	if c.fs == nil {
		return 0, fmt.Errorf("шара не смонтирована: вызовите Connect() перед GetFileSize")
	}

	fi, err := c.fs.Stat(filePath)
	if err != nil {
		return 0, fmt.Errorf("ошибка получения информации о файле %s: %w", filePath, err)
	}

	return fi.Size(), nil
}

// ReadFileContent читает файл с шары полностью (добавлено для текущего проекта)
func (c *CIFSClient) ReadFileContent(filePath string) ([]byte, error) {
	const maxTries = 2
	for attempt := 0; attempt < maxTries; attempt++ {
		data, err := c.readFileContentOnce(filePath)
		if err != nil && attempt == 0 {
			if _, rerr := c.readyFS(); rerr != nil {
				return nil, rerr
			}
			continue
		}
		return data, err
	}
	return nil, fmt.Errorf("не удалось прочитать файл после %d попыток", maxTries)
}

func (c *CIFSClient) readFileContentOnce(filePath string) ([]byte, error) {
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

// ListRoot — выводит и возвращает список элементов в корне смонтированной шары
func (c *CIFSClient) ListRoot(relativePath string) ([]string, error) {
	if c.fs == nil {
		return nil, fmt.Errorf("шара не смонтирована: вызовите Connect() перед ListRoot")
	}

	entries, err := c.fs.ReadDir(relativePath)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать директорию %s: %w", relativePath, err)
	}

	names := make([]string, 0, len(entries))
	for _, fi := range entries {
		names = append(names, fi.Name())
	}
	if logger.Log != nil {
		logger.Log.Debug("Содержимое директории",
			zap.String("path", relativePath),
			zap.Strings("names", names))
	}
	return names, nil
}

// ==================== Вспомогательные методы ====================

func parentDirFromSMBPath(p string) string {
	if p == "" {
		return ""
	}
	idx := strings.LastIndexAny(p, "/\\")
	if idx <= 0 {
		return ""
	}
	return p[:idx]
}

func (c *CIFSClient) ensureRemoteDirAll(dir string) error {
	if c.fs == nil {
		return fmt.Errorf("шара не смонтирована: вызовите Connect() перед ensureRemoteDirAll")
	}
	trimmed := strings.Trim(dir, "\\/")
	if trimmed == "" {
		return nil
	}
	parts := strings.FieldsFunc(trimmed, func(r rune) bool { return r == '/' || r == '\\' })
	accum := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if accum == "" {
			accum = part
		} else {
			accum = accum + "\\" + part
		}
		if err := c.fs.Mkdir(accum, 0755); err != nil {
			if fi, statErr := c.fs.Stat(accum); statErr == nil {
				if fi.IsDir() {
					continue
				}
				return fmt.Errorf("путь %s существует, но это не директория", accum)
			}
			return fmt.Errorf("не удалось создать директорию %s: %w", accum, err)
		}
	}
	return nil
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

	// Применяем замену имени сервера
	server = resolveServerName(server)

	if len(parts) >= 3 {
		relPath = parts[2]
	}

	return server, share, relPath, nil
}

// resolveServerName заменяет IP на имя сервера при необходимости
func resolveServerName(server string) string {
	if server == "192.168.87.31" {
		return "crys4"
	}
	return server
}
