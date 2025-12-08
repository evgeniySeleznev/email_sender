package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/godror/godror"
	"go.uber.org/zap"

	"email-service/logger"
	"email-service/settings"
)

const (
	// Таймауты для операций с БД
	pingTimeout       = 5 * time.Second  // Таймаут для проверки соединения
	QueryTimeout      = 30 * time.Second // Таймаут для запросов (экспортируется для использования в procedures.go)
	ExecTimeout       = 30 * time.Second // Таймаут для выполнения команд (экспортируется для использования в queue.go)
	connectionTimeout = 10 * time.Second // Таймаут для подключения
)

// DBConnection инкапсулирует соединение и операции с БД
type DBConnection struct {
	cfg               *settings.Config
	db                *sql.DB
	ctx               context.Context
	cancel            context.CancelFunc
	mu                sync.RWMutex // Блокировка для потокобезопасного доступа к БД
	reconnectTicker   *time.Ticker
	reconnectStop     chan struct{}
	reconnectWg       sync.WaitGroup
	lastReconnect     time.Time
	reconnectInterval time.Duration // Интервал переподключения (30 минут)
	activeOps         atomic.Int32  // Счетчик активных операций с БД
}

// NewDBConnection создает новое подключение к БД
func NewDBConnection(cfg *settings.Config) (*DBConnection, error) {
	ctx, cancel := context.WithCancel(context.Background())
	return &DBConnection{
		cfg:               cfg,
		ctx:               ctx,
		cancel:            cancel,
		reconnectInterval: 30 * time.Minute, // 30 минут по умолчанию
		reconnectStop:     make(chan struct{}),
		lastReconnect:     time.Now(),
	}, nil
}

// OpenConnection открывает подключение к Oracle через драйвер godror
func (d *DBConnection) OpenConnection() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.openConnectionInternal()
}

// CloseConnection закрывает соединение
func (d *DBConnection) CloseConnection() {
	// Останавливаем периодическое переподключение
	d.StopPeriodicReconnect()

	if d.cancel != nil {
		d.cancel()
	}
	if d.db != nil {
		_ = d.db.Close()
		d.db = nil
	}
	if logger.Log != nil {
		logger.Log.Info("Database connection closed")
	}
}

// CheckConnection проверяет соединение с БД
func (d *DBConnection) CheckConnection() bool {
	d.mu.RLock()
	db := d.db
	d.mu.RUnlock()

	if db == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		if logger.Log != nil {
			logger.Log.Debug("Ошибка проверки соединения", zap.Error(err))
		}
		return false
	}
	return true
}

// Reconnect переподключается к базе данных с пересозданием пула соединений
// Проверяет наличие активных операций перед переподключением
// Использует безопасную блокировку без гонок
func (d *DBConnection) Reconnect() error {
	// Первая проверка: ждем завершения активных операций БЕЗ блокировки
	// Это позволяет операциям завершиться естественным образом
	if err := d.waitForActiveOperations(); err != nil {
		return fmt.Errorf("не удалось дождаться завершения активных операций: %w", err)
	}

	// Блокируем доступ к пулу для предотвращения новых операций
	// Используем цикл с проверкой для предотвращения гонок
	const maxRetries = 3
	const retryDelay = 50 * time.Millisecond

	// Пытаемся получить блокировку с проверкой активных операций
	for retry := 0; retry < maxRetries; retry++ {
		// Проверяем активные операции БЕЗ блокировки (быстрая проверка)
		activeCount := d.activeOps.Load()
		if activeCount == 0 {
			// Нет активных операций - получаем блокировку и переподключаемся
			break
		}

		if retry < maxRetries-1 {
			if logger.Log != nil {
				logger.Log.Debug("Обнаружены активные операции, ожидание",
					zap.Int32("activeOps", activeCount),
					zap.Int("retry", retry+1),
					zap.Int("maxRetries", maxRetries))
			}
			time.Sleep(retryDelay)
		} else {
			// Последняя попытка - ждем завершения операций
			if logger.Log != nil {
				logger.Log.Debug("Последняя попытка: ожидание завершения активных операций",
					zap.Int32("activeOps", activeCount))
			}
			if err := d.waitForActiveOperations(); err != nil {
				return fmt.Errorf("не удалось дождаться завершения активных операций: %w", err)
			}
			// Финальная проверка
			activeCount = d.activeOps.Load()
			if activeCount > 0 && logger.Log != nil {
				logger.Log.Warn("Переподключение выполняется при наличии активных операций",
					zap.Int32("activeOps", activeCount))
			}
			break
		}
	}

	// Получаем блокировку для переподключения
	// К этому моменту активные операции должны быть завершены
	d.mu.Lock()
	defer d.mu.Unlock()

	// Финальная проверка под блокировкой (на случай гонки)
	finalActiveCount := d.activeOps.Load()
	if finalActiveCount > 0 && logger.Log != nil {
		logger.Log.Warn("Обнаружены активные операции под блокировкой, продолжаем переподключение",
			zap.Int32("activeOps", finalActiveCount))
	}

	if logger.Log != nil {
		logger.Log.Info("Начало переподключения к базе данных...")
	}

	// Закрываем текущий пул соединений
	if d.db != nil {
		_ = d.db.Close()
		d.db = nil
		if logger.Log != nil {
			logger.Log.Debug("Текущий пул соединений закрыт")
		}
	}

	// Пересоздаем подключение (открываем новый пул)
	if err := d.openConnectionInternal(); err != nil {
		return fmt.Errorf("ошибка переподключения: %w", err)
	}

	if logger.Log != nil {
		logger.Log.Info("Переподключение к базе данных выполнено успешно. Пул соединений пересоздан")
	}
	return nil
}

// waitForActiveOperations ждет завершения активных операций перед переподключением
// Максимальное время ожидания - 35 секунд
func (d *DBConnection) waitForActiveOperations() error {
	const maxWaitTime = 35 * time.Second
	const checkInterval = 100 * time.Millisecond
	const logInterval = 1 * time.Second

	startTime := time.Now()
	lastLogTime := time.Time{}
	lastActiveCount := int32(-1)

	for {
		activeCount := d.activeOps.Load()
		if activeCount == 0 {
			return nil
		}

		if time.Since(startTime) > maxWaitTime {
			if logger.Log != nil {
				logger.Log.Warn("Превышено время ожидания завершения активных операций",
					zap.Int32("activeOps", activeCount))
			}
			return nil
		}

		now := time.Now()
		if now.Sub(lastLogTime) >= logInterval || activeCount != lastActiveCount {
			if logger.Log != nil {
				logger.Log.Debug("Ожидание завершения активных операций перед переподключением",
					zap.Int32("activeOps", activeCount))
			}
			lastLogTime = now
			lastActiveCount = activeCount
		}

		time.Sleep(checkInterval)
	}
}

// BeginOperation отмечает начало операции с БД
func (d *DBConnection) BeginOperation() {
	d.activeOps.Add(1)
}

// EndOperation отмечает завершение операции с БД
func (d *DBConnection) EndOperation() {
	d.activeOps.Add(-1)
}

// GetActiveOperationsCount возвращает количество активных операций
func (d *DBConnection) GetActiveOperationsCount() int32 {
	return d.activeOps.Load()
}

// openConnectionInternal внутренний метод открытия соединения без блокировки
func (d *DBConnection) openConnectionInternal() error {
	if logger.Log != nil {
		logger.Log.Info("openConnectionInternal: начало открытия соединения")
	}
	// Получаем параметры подключения из конфигурации
	// Для совместимости с существующим форматом используем секцию [ORACLE]
	oracleSec := d.cfg.File.Section("ORACLE")
	instance := oracleSec.Key("Instance").String()

	// Также проверяем секцию [main] для user/password (как в smsSender)
	mainSec := d.cfg.File.Section("main")
	user := mainSec.Key("username").String()
	password := mainSec.Key("password").String()
	if password == "" {
		password = mainSec.Key("passwword").String() // Совместимость с опечаткой
	}
	dsn := mainSec.Key("dsn").String()

	// Формируем строку подключения для godror
	var connString string
	if user != "" && password != "" && dsn != "" {
		connString = fmt.Sprintf("%s/%s@%s", user, password, dsn)
	} else if instance != "" {
		// Используем только instance (требует настройки переменных окружения или tnsnames.ora)
		connString = instance
	} else {
		return fmt.Errorf("не указаны параметры подключения к БД")
	}

	db, err := sql.Open("godror", connString)
	if err != nil {
		if logger.Log != nil {
			logger.Log.Error("Ошибка sql.Open", zap.Error(err))
		}
		return fmt.Errorf("ошибка sql.Open: %w", err)
	}

	// Настройки пула
	db.SetMaxOpenConns(200)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	// Проверка соединения с таймаутом
	// Используем горутину для гарантированного таймаута, т.к. PingContext может зависать
	// на уровне ОС (DNS, TCP) до того, как контекст сможет отменить операцию
	if logger.Log != nil {
		logger.Log.Info("openConnectionInternal: выполнение PingContext с таймаутом",
			zap.Duration("timeout", connectionTimeout))
	}

	// Создаем таймер для гарантированного таймаута
	timeoutTimer := time.NewTimer(connectionTimeout)
	defer timeoutTimer.Stop()

	// Канал для результата ping
	pingDone := make(chan error, 1)

	// Запускаем PingContext в горутине
	// Используем background context, т.к. реальный таймаут контролируется таймером
	go func() {
		// Создаем контекст с таймаутом для PingContext (на случай если он все же поддерживает отмену)
		pingCtx, pingCancel := context.WithTimeout(context.Background(), connectionTimeout+time.Second)
		defer pingCancel()

		err := db.PingContext(pingCtx)
		select {
		case pingDone <- err:
			// Результат отправлен
		default:
			// Канал уже закрыт (таймаут), игнорируем результат
		}
	}()

	select {
	case err := <-pingDone:
		// Ping завершился (успешно или с ошибкой)
		if err != nil {
			_ = db.Close()
			if logger.Log != nil {
				logger.Log.Error("openConnectionInternal: ошибка ping", zap.Error(err))
			}
			return fmt.Errorf("ошибка ping: %w", err)
		}
		if logger.Log != nil {
			logger.Log.Info("openConnectionInternal: PingContext успешно выполнен")
		}
	case <-timeoutTimer.C:
		// Таймаут истек - принудительно закрываем соединение
		_ = db.Close()
		if logger.Log != nil {
			logger.Log.Error("openConnectionInternal: таймаут PingContext истек",
				zap.Duration("timeout", connectionTimeout))
		}
		// Ждем немного, чтобы горутина могла завершиться (но не блокируемся навсегда)
		select {
		case <-pingDone:
			// Горутина завершилась, игнорируем результат
		case <-time.After(100 * time.Millisecond):
			// Не ждем дольше 100мс
		}
		return fmt.Errorf("таймаут подключения к БД (%v): операция не завершилась в течение указанного времени", connectionTimeout)
	}
	d.db = db
	d.lastReconnect = time.Now()
	if logger.Log != nil {
		logger.Log.Info("Database connection opened (using Oracle Instant Client via godror)")
	}
	return nil
}

// StartPeriodicReconnect запускает горутину для периодического переподключения к БД
func (d *DBConnection) StartPeriodicReconnect() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.reconnectTicker != nil {
		return
	}

	d.reconnectTicker = time.NewTicker(d.reconnectInterval)
	d.reconnectWg.Add(1)

	go func() {
		defer d.reconnectWg.Done()
		defer d.reconnectTicker.Stop()

		if logger.Log != nil {
			logger.Log.Info("Запущен механизм периодического переподключения к БД",
				zap.Duration("interval", d.reconnectInterval))
		}

		for {
			select {
			case <-d.reconnectTicker.C:
				if logger.Log != nil {
					logger.Log.Info("Периодическое переподключение к БД",
						zap.Duration("sinceLastReconnect", time.Since(d.lastReconnect)))
				}
				if err := d.Reconnect(); err != nil {
					if logger.Log != nil {
						logger.Log.Error("Ошибка периодического переподключения", zap.Error(err))
					}
				}

			case <-d.reconnectStop:
				if logger.Log != nil {
					logger.Log.Info("Остановка механизма периодического переподключения к БД")
				}
				return

			case <-d.ctx.Done():
				if logger.Log != nil {
					logger.Log.Info("Контекст отменен, остановка механизма периодического переподключения к БД")
				}
				return
			}
		}
	}()
}

// StopPeriodicReconnect останавливает механизм периодического переподключения
func (d *DBConnection) StopPeriodicReconnect() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.reconnectTicker != nil {
		close(d.reconnectStop)
		d.reconnectTicker.Stop()
		d.reconnectTicker = nil
		d.reconnectWg.Wait()
		d.reconnectStop = make(chan struct{})
	}
}

// GetConfig возвращает конфигурацию
func (d *DBConnection) GetConfig() *settings.Config {
	return d.cfg
}

// WithDB выполняет функцию с безопасным доступом к соединению БД
// Использует RWMutex для параллельного чтения
func (d *DBConnection) WithDB(fn func(*sql.DB) error) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.db == nil {
		return fmt.Errorf("соединение с БД не открыто")
	}

	return fn(d.db)
}

// WithDBTx выполняет функцию с транзакцией и безопасным доступом к соединению
// Отмечает начало операции с БД для предотвращения переподключения во время транзакции
func (d *DBConnection) WithDBTx(ctx context.Context, fn func(*sql.Tx) error) error {
	// Отмечаем начало операции ПОД блокировкой
	d.mu.Lock()
	if d.db == nil {
		d.mu.Unlock()
		return fmt.Errorf("соединение с БД не открыто")
	}
	d.BeginOperation()
	db := d.db
	d.mu.Unlock()

	defer d.EndOperation()

	// Создаем транзакцию БЕЗ блокировки
	txTimeout := ExecTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining < ExecTimeout {
			txTimeout = remaining
		}
	}
	txCtx, txCancel := context.WithTimeout(ctx, txTimeout)
	defer txCancel()

	tx, err := db.BeginTx(txCtx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer tx.Rollback()

	// Выполняем функцию с транзакцией
	if err := fn(tx); err != nil {
		return err
	}

	// Коммитим транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка коммита транзакции: %w", err)
	}

	return nil
}
