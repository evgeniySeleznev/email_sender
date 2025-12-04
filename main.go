package main

import (
	"context"
	"email-service/db"
	"email-service/email"
	"email-service/logger"
	"email-service/service"
	"email-service/settings"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
)

const shutdownTimeout = 10 * time.Second

func main() {
	cfg := initializeConfig()
	defer logger.Log.Sync()

	logger.Log.Info("Запуск email сервиса")

	dbConn := initializeDatabase(cfg)
	defer dbConn.CloseConnection()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shutdownRequested := setupSignalHandling()

	queueReader := initializeQueueReader(dbConn)
	mainService := service.NewService(cfg, dbConn, queueReader)
	emailService := initializeEmailService(cfg, dbConn, mainService.GetStatusUpdateCallback())
	mainService.SetEmailService(emailService)

	var allHandlersWg sync.WaitGroup
	startMainService(ctx, mainService, &allHandlersWg)

	<-shutdownRequested
	shutdown(ctx, cancel, mainService, emailService, dbConn, &allHandlersWg)
}

// initializeConfig загружает конфигурацию и инициализирует логгер
func initializeConfig() *settings.Config {
	cfg, err := settings.LoadConfig("settings/settings.ini")
	if err != nil {
		os.Stderr.WriteString("Ошибка загрузки конфигурации: " + err.Error() + "\n")
		os.Exit(1)
	}

	if err := logger.InitLogger(cfg.File); err != nil {
		os.Stderr.WriteString("Ошибка инициализации логгера: " + err.Error() + "\n")
		os.Exit(1)
	}

	return cfg
}

// initializeDatabase создает и настраивает подключение к БД
func initializeDatabase(cfg *settings.Config) *db.DBConnection {
	dbConn, err := db.NewDBConnection(cfg)
	if err != nil {
		logger.Log.Fatal("Ошибка создания подключения к БД", zap.Error(err))
	}

	if err := dbConn.OpenConnection(); err != nil {
		logger.Log.Fatal("Ошибка открытия соединения", zap.Error(err))
	}

	logger.Log.Info("Успешно подключено к Oracle базе данных")
	dbConn.StartPeriodicReconnect()

	return dbConn
}

// setupSignalHandling настраивает обработку сигналов для graceful shutdown
func setupSignalHandling() chan struct{} {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	if runtime.GOOS != "windows" {
		signal.Notify(sigChan, syscall.SIGHUP)
	}

	shutdownRequested := make(chan struct{})
	go func() {
		sig := <-sigChan
		logger.Log.Info("Получен сигнал, инициируется graceful shutdown",
			zap.String("signal", sig.String()),
			zap.Duration("shutdownTimeout", shutdownTimeout))
		close(shutdownRequested)
	}()

	return shutdownRequested
}

// initializeQueueReader создает и настраивает QueueReader
func initializeQueueReader(dbConn *db.DBConnection) *db.QueueReader {
	queueReader, err := db.NewQueueReader(dbConn)
	if err != nil {
		logger.Log.Fatal("Ошибка создания QueueReader", zap.Error(err))
	}

	logger.Log.Info("Настройки очереди",
		zap.String("queue", queueReader.GetQueueName()),
		zap.String("consumer", queueReader.GetConsumerName()))

	queueReader.SetWaitTimeout(10) // 10 секунд (аналогично smsSender)

	return queueReader
}

// initializeEmailService создает email сервис
func initializeEmailService(cfg *settings.Config, dbConn *db.DBConnection, statusCallback email.StatusUpdateCallback) *email.Service {
	emailService, err := email.NewService(cfg, dbConn, statusCallback)
	if err != nil {
		logger.Log.Fatal("Ошибка создания email сервиса", zap.Error(err))
	}
	return emailService
}

// startMainService запускает основной цикл обработки сообщений
func startMainService(ctx context.Context, mainService *service.Service, wg *sync.WaitGroup) {
	go func() {
		mainService.Run(ctx, wg)
	}()
}

// shutdown выполняет graceful shutdown приложения
func shutdown(
	ctx context.Context,
	cancel context.CancelFunc,
	mainService *service.Service,
	emailService *email.Service,
	dbConn *db.DBConnection,
	allHandlersWg *sync.WaitGroup,
) {
	logger.Log.Info("Начало graceful shutdown с таймаутом",
		zap.Duration("timeout", shutdownTimeout))

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	cancel()

	waitForOperationsCompletion(shutdownCtx, allHandlersWg, dbConn)
	performGracefulShutdown(shutdownCtx, mainService, emailService, dbConn, allHandlersWg)
}

// waitForOperationsCompletion ждет завершения всех операций с таймаутом
func waitForOperationsCompletion(
	shutdownCtx context.Context,
	allHandlersWg *sync.WaitGroup,
	dbConn *db.DBConnection,
) {
	operationsDone := make(chan struct{})
	go func() {
		allHandlersWg.Wait()
		close(operationsDone)
	}()

	select {
	case <-operationsDone:
		logger.Log.Info("Все операции завершены до истечения таймаута")
	case <-shutdownCtx.Done():
		logger.Log.Warn("Таймаут graceful shutdown истек, принудительное завершение",
			zap.Duration("timeout", shutdownTimeout),
			zap.Int32("activeOperations", dbConn.GetActiveOperationsCount()))
	}
}

// performGracefulShutdown выполняет корректное завершение всех операций
func performGracefulShutdown(
	ctx context.Context,
	mainService *service.Service,
	emailService *email.Service,
	dbConn *db.DBConnection,
	allHandlersWg *sync.WaitGroup,
) {
	logger.Log.Info("Завершение graceful shutdown...")

	waitForActiveDatabaseOperations(ctx, dbConn)
	waitForMessageHandlers(ctx, allHandlersWg)
	stopServices(emailService, dbConn)

	logger.Log.Info("Graceful shutdown завершен успешно")
}

// waitForActiveDatabaseOperations ждет завершения активных операций с БД
func waitForActiveDatabaseOperations(ctx context.Context, dbConn *db.DBConnection) {
	activeOps := dbConn.GetActiveOperationsCount()
	if activeOps == 0 {
		return
	}

	logger.Log.Info("Ожидание завершения активных операций с БД",
		zap.Int32("activeOperations", activeOps))

	checkCtx, checkCancel := context.WithTimeout(ctx, shutdownTimeout)
	defer checkCancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-checkCtx.Done():
			logger.Log.Warn("Таймаут ожидания активных операций истек",
				zap.Int32("remainingOperations", dbConn.GetActiveOperationsCount()))
			return
		case <-ticker.C:
			if dbConn.GetActiveOperationsCount() == 0 {
				logger.Log.Info("Все активные операции с БД завершены")
				return
			}
		}
	}
}

// waitForMessageHandlers ждет завершения всех обработчиков сообщений
func waitForMessageHandlers(ctx context.Context, allHandlersWg *sync.WaitGroup) {
	logger.Log.Info("Ожидание завершения всех обработчиков сообщений...")
	done := make(chan struct{})
	go func() {
		allHandlersWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Log.Info("Все обработчики сообщений завершены")
	case <-ctx.Done():
		logger.Log.Warn("Таймаут ожидания обработчиков истек, принудительное завершение")
	}
}

// stopServices останавливает все сервисы
func stopServices(emailService *email.Service, dbConn *db.DBConnection) {
	logger.Log.Info("Остановка механизма периодического переподключения к БД...")
	dbConn.StopPeriodicReconnect()

	logger.Log.Info("Закрытие email сервиса...")
	if err := emailService.Close(); err != nil {
		logger.Log.Error("Ошибка при закрытии email сервиса", zap.Error(err))
	}
}
