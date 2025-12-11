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
	reconnectPending  atomic.Bool   // Флаг ожидания переподключения
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
// Reconnect переподключается к базе данных с пересозданием пула соединений
// Использует Hot Swap механизм с принудительной подменой
func (d *DBConnection) Reconnect() error {
	// Используем Hot Swap с флагом force=true, так как этот метод обычно вызывается
	// при ошибках соединения, когда нужно восстановить работу как можно скорее.
	// При этом активные операции (если они есть) не будут прерваны мгновенно,
	// а завершатся на старом соединении перед его закрытием.
	return d.HotSwapReconnect(true)
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
func (d *DBConnection) BeginOperation() error {
	if d.reconnectPending.Load() {
		return fmt.Errorf("выполняется переподключение к БД")
	}
	d.activeOps.Add(1)
	return nil
}

// EndOperation отмечает завершение операции с БД
func (d *DBConnection) EndOperation() {
	d.activeOps.Add(-1)
}

// GetActiveOperationsCount возвращает количество активных операций
func (d *DBConnection) GetActiveOperationsCount() int32 {
	return d.activeOps.Load()
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
			logger.Log.Info("Запущен механизм периодического переподключения к БД (Hot Swap)",
				zap.Duration("interval", d.reconnectInterval))
		}

		postponeCount := 0
		const maxPostpones = 3
		const postponeInterval = 5 * time.Minute // 5 минут отсрочки
		const waitActiveOpsTimeout = 10 * time.Second

		for {
			select {
			case <-d.reconnectTicker.C:
				// 1. Сигнализируем о начале переподключения (блокируем новые операции)
				d.reconnectPending.Store(true)

				// 2. Ждем завершения активных операций (до 10 секунд)
				waitStart := time.Now()
				activeOps := d.GetActiveOperationsCount()

				if activeOps > 0 {
					if logger.Log != nil {
						logger.Log.Info("Ожидание завершения активных операций перед переподключением...",
							zap.Int32("activeOps", activeOps))
					}

					// Цикл ожидания
					for activeOps > 0 && time.Since(waitStart) < waitActiveOpsTimeout {
						time.Sleep(100 * time.Millisecond)
						activeOps = d.GetActiveOperationsCount()
					}
				}

				// 3. Проверяем результат ожидания
				if activeOps > 0 {
					// Если операции все еще есть - отменяем блокировку и откладываем
					d.reconnectPending.Store(false)

					if postponeCount < maxPostpones {
						postponeCount++
						if logger.Log != nil {
							logger.Log.Info("Периодическое переподключение отложено из-за активных операций",
								zap.Int32("activeOps", activeOps),
								zap.Int("postponeCount", postponeCount),
								zap.Duration("postponeInterval", postponeInterval))
						}
						d.reconnectTicker.Reset(postponeInterval)
						continue
					}

					// Если лимит откладываний исчерпан - идем на принудительное (Hot Swap)
					if logger.Log != nil {
						logger.Log.Warn("Выполняется принудительное переподключение (Hot Swap) после серии откладываний",
							zap.Int("postponeCount", postponeCount),
							zap.Int32("activeOps", activeOps))
					}
				} else {
					// Операций нет - можно переподключаться
					if logger.Log != nil {
						logger.Log.Info("Выполняется плановое переподключение (Hot Swap)",
							zap.Duration("sinceLastReconnect", time.Since(d.lastReconnect)))
					}
				}

				// Сбрасываем счетчик и возвращаем нормальный интервал
				postponeCount = 0
				d.reconnectTicker.Reset(d.reconnectInterval)

				// Выполняем Hot Swap
				if err := d.HotSwapReconnect(true); err != nil {
					if logger.Log != nil {
						logger.Log.Error("Ошибка Hot Swap переподключения", zap.Error(err))
					}
				}

				// Снимаем блокировку новых операций (для нового соединения)
				d.reconnectPending.Store(false)

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
		// Не останавливаем тикер здесь, так как он используется в горутине.
		d.reconnectTicker = nil
		d.reconnectWg.Wait()
		d.reconnectStop = make(chan struct{})
	}
}

// HotSwapReconnect выполняет переподключение с созданием нового соединения перед закрытием старого
func (d *DBConnection) HotSwapReconnect(force bool) error {
	if logger.Log != nil {
		logger.Log.Info("Начало Hot Swap переподключения...")
	}

	// 1. Создаем новое соединение (это может занять время, но не блокирует работу)
	newDB, err := d.createConnection()
	if err != nil {
		return fmt.Errorf("ошибка создания нового соединения для Hot Swap: %w", err)
	}

	// 2. Подменяем соединение под блокировкой
	d.mu.Lock()
	oldDB := d.db
	d.db = newDB
	d.lastReconnect = time.Now()
	d.mu.Unlock()

	if logger.Log != nil {
		logger.Log.Info("Hot Swap: пул соединений подменен на новый")
	}

	// 3. Обрабатываем старое соединение
	if oldDB != nil {
		if force {
			// Если принудительно (были активные операции), даем время на завершение
			// Запускаем в отдельной горутине, чтобы не блокировать текущий поток
			go func() {
				// Ждем чуть больше максимального таймаута запроса (QueryTimeout = 30s)
				// Дадим с запасом 2 минуты
				drainTimeout := 2 * time.Minute
				if logger.Log != nil {
					logger.Log.Info("Hot Swap: старое соединение будет закрыто через паузу (draining)",
						zap.Duration("timeout", drainTimeout))
				}
				time.Sleep(drainTimeout)

				if err := oldDB.Close(); err != nil {
					if logger.Log != nil {
						logger.Log.Error("Ошибка при отложенном закрытии старого соединения", zap.Error(err))
					}
				} else {
					if logger.Log != nil {
						logger.Log.Info("Hot Swap: старое соединение успешно закрыто после draining")
					}
				}
			}()
		} else {
			// Если операций не было, закрываем сразу
			if err := oldDB.Close(); err != nil {
				if logger.Log != nil {
					logger.Log.Error("Ошибка при закрытии старого соединения", zap.Error(err))
				}
			} else {
				if logger.Log != nil {
					logger.Log.Info("Hot Swap: старое соединение закрыто")
				}
			}
		}
	}

	return nil
}

// createConnection создает и настраивает новое подключение к БД
func (d *DBConnection) createConnection() (*sql.DB, error) {
	if logger.Log != nil {
		logger.Log.Info("createConnection: начало создания соединения")
	}

	// Получаем параметры подключения из конфигурации
	oracleSec := d.cfg.File.Section("ORACLE")
	instance := oracleSec.Key("Instance").String()

	mainSec := d.cfg.File.Section("main")
	user := mainSec.Key("username").String()
	password := mainSec.Key("password").String()
	if password == "" {
		password = mainSec.Key("passwword").String()
	}
	dsn := mainSec.Key("dsn").String()

	var connString string
	if user != "" && password != "" && dsn != "" {
		connString = fmt.Sprintf("%s/%s@%s", user, password, dsn)
	} else if instance != "" {
		connString = instance
	} else {
		return nil, fmt.Errorf("не указаны параметры подключения к БД")
	}

	db, err := sql.Open("godror", connString)
	if err != nil {
		return nil, fmt.Errorf("ошибка sql.Open: %w", err)
	}

	// Настройки пула
	db.SetMaxOpenConns(200)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	// Проверка соединения с таймаутом
	if logger.Log != nil {
		logger.Log.Info("createConnection: выполнение PingContext с таймаутом",
			zap.Duration("timeout", connectionTimeout))
	}

	timeoutTimer := time.NewTimer(connectionTimeout)
	defer timeoutTimer.Stop()

	pingDone := make(chan error, 1)

	go func() {
		pingCtx, pingCancel := context.WithTimeout(context.Background(), connectionTimeout+time.Second)
		defer pingCancel()

		err := db.PingContext(pingCtx)
		select {
		case pingDone <- err:
		default:
		}
	}()

	select {
	case err := <-pingDone:
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("ошибка ping: %w", err)
		}
	case <-timeoutTimer.C:
		_ = db.Close()
		return nil, fmt.Errorf("таймаут подключения к БД (%v)", connectionTimeout)
	}

	return db, nil
}

// openConnectionInternal использует createConnection для инициализации
func (d *DBConnection) openConnectionInternal() error {
	db, err := d.createConnection()
	if err != nil {
		return err
	}
	d.db = db
	d.lastReconnect = time.Now()
	if logger.Log != nil {
		logger.Log.Info("Database connection opened (using Oracle Instant Client via godror)")
	}
	return nil
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
	if err := d.BeginOperation(); err != nil {
		d.mu.Unlock()
		return err
	}
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

	var committed bool
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	// Выполняем функцию с транзакцией
	if err := fn(tx); err != nil {
		return err
	}

	// Коммитим транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка коммита транзакции: %w", err)
	}
	committed = true

	return nil
}
