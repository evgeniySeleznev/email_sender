package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"email-service/db"
	"email-service/email"
	"email-service/logger"
	"email-service/settings"
)

const (
	portion = 20 // Количество сообщений для обработки за цикл
)

// Service представляет основной сервис обработки сообщений
type Service struct {
	cfg          *settings.Config
	dbConn       *db.DBConnection
	queueReader  *db.QueueReader
	emailService *email.Service

	// Внутренняя очередь сообщений (requestDir)
	requestDir   map[string]*db.QueueMessage // Ключ - taskID (string)
	requestDirMu sync.RWMutex

	// Очередь результатов (responseQueue)
	responseQueue   chan db.SaveEmailResponseParams
	responseQueueWg sync.WaitGroup

	// Ограничение частоты отправки на email адрес (sendEmail)
	sendEmailMap map[string]time.Time // Ключ - email адрес
	sendEmailMu  sync.RWMutex

	// Автоматический рестарт
	criticalErrorCount atomic.Int32
	needRestart        atomic.Bool

	// Периодическая выборка всех сообщений
	nextDequeueAll time.Time
	dequeueAllMu   sync.Mutex

	// Получение статусов доставки
	nextLoadStatuses time.Time
	loadStatusesMu   sync.Mutex
}

// NewService создает новый сервис
func NewService(cfg *settings.Config, dbConn *db.DBConnection, queueReader *db.QueueReader, emailService *email.Service) *Service {
	s := &Service{
		cfg:          cfg,
		dbConn:       dbConn,
		queueReader:  queueReader,
		emailService: emailService,

		requestDir:       make(map[string]*db.QueueMessage),
		responseQueue:    make(chan db.SaveEmailResponseParams, 10000), // Буферизованный канал
		sendEmailMap:     make(map[string]time.Time),
		nextDequeueAll:   time.Now(),                       // Сразу при запуске
		nextLoadStatuses: time.Now().Add(60 * time.Minute), // Через 60 минут
	}

	// Запускаем горутину для записи результатов в БД
	s.responseQueueWg.Add(1)
	go s.responseQueueWriter(context.Background())

	return s
}

// Run запускает основной цикл обработки сообщений
func (s *Service) Run(ctx context.Context, wg *sync.WaitGroup) {
	logger.Log.Info("Запуск основного цикла обработки сообщений")

	// Отмечаем начало работы горутины
	wg.Add(1)
	defer wg.Done()

	// Сбрасываем счетчик критических ошибок
	s.criticalErrorCount.Store(0)
	s.needRestart.Store(false)

	// Основной цикл обработки (jobLoop)
	for {
		// Проверяем контекст
		if ctx.Err() != nil {
			logger.Log.Info("Получен сигнал остановки, прекращаем обработку...")
			break
		}

		// Проверяем необходимость рестарта из-за критических ошибок
		if s.criticalErrorCount.Load() > int32(s.cfg.Mode.MaxErrorCountForAutoRestart) {
			logger.Log.Warn("Превышение числа критических ошибок, инициируется рестарт",
				zap.Int32("criticalErrorCount", s.criticalErrorCount.Load()),
				zap.Int("maxErrorCount", s.cfg.Mode.MaxErrorCountForAutoRestart))
			s.criticalErrorCount.Store(0)
			s.needRestart.Store(true)
		}

		// 1. Выбираем из очереди неотправленное если (запуск сервиса) или (каждые 60 минут при пустой очереди на отправку)
		if s.shouldDequeueAll() {
			if s.dbConn.CheckConnection() {
				s.dequeueAllMessages(ctx)
			}
		}

		// 2. Отправляем сообщения провайдеру
		s.processRequestQueue(ctx)

		// 3. Записываем статусы отправки в базу (через канал responseQueue)

		// 4. Получение статусов доставки сообщений
		if s.shouldLoadStatuses() {
			s.loadDeliveryStatuses(ctx)
		}

		// 5. Записываем подтверждения отправки в базу (через канал responseQueue)

		// Перезапустить сервис если все отправлено и записано в базу
		if s.needRestart.Load() && s.isRequestQueueEmpty() {
			logger.Log.Warn("Перезапуск цикла обработки")
			break
		}

		// Пауза между итерациями
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Логируем статистику при завершении
	s.logStatistics()
	logger.Log.Info("Цикл обработки остановлен")
}

// shouldDequeueAll проверяет, нужно ли выполнить выборку всех сообщений
func (s *Service) shouldDequeueAll() bool {
	s.dequeueAllMu.Lock()
	defer s.dequeueAllMu.Unlock()

	if s.needRestart.Load() {
		return false
	}

	if time.Now().After(s.nextDequeueAll) {
		// Проверяем, пуста ли внутренняя очередь
		if s.isRequestQueueEmpty() {
			s.nextDequeueAll = time.Now().Add(60 * time.Minute)
			return true
		}
		// Если очередь не пуста, откладываем выборку
		s.nextDequeueAll = time.Now().Add(60 * time.Minute)
	}
	return false
}

// dequeueAllMessages выбирает все сообщения из Oracle очереди
func (s *Service) dequeueAllMessages(ctx context.Context) {
	logger.Log.Info("Выборка всех сообщений из очереди")

	messages, err := s.queueReader.DequeueMany(ctx, portion)
	if err != nil {
		if ctx.Err() == context.Canceled {
			return
		}
		logger.Log.Error("Ошибка выборки всех сообщений", zap.Error(err))
		return
	}

	if len(messages) == 0 {
		return
	}

	logger.Log.Info("Прочитано сообщений из очереди", zap.Int("count", len(messages)))

	// Добавляем сообщения во внутреннюю очередь
	for _, msg := range messages {
		s.enqueueRequest(msg)
	}
}

// enqueueRequest добавляет сообщение во внутреннюю очередь с проверкой дубликатов
func (s *Service) enqueueRequest(msg *db.QueueMessage) {
	if msg == nil {
		return
	}

	// Парсим taskID из XML
	parsed, err := s.queueReader.ParseXMLMessage(msg)
	if err != nil {
		logger.Log.Error("Ошибка парсинга XML при добавлении в очередь", zap.Error(err))
		return
	}

	taskIDStr, ok := parsed["email_task_id"].(string)
	if !ok || taskIDStr == "" {
		logger.Log.Error("TaskId не распарсен при добавлении в очередь")
		return
	}

	taskIDStr = strings.TrimSpace(taskIDStr)

	s.requestDirMu.Lock()
	defer s.requestDirMu.Unlock()

	// Проверяем на дубликаты
	if _, exists := s.requestDir[taskIDStr]; exists {
		logger.Log.Error("Новое сообщение в очереди. Уже есть в очереди на отправку",
			zap.String("taskID", taskIDStr),
			zap.Int("queueSize", len(s.requestDir)))
		return
	}

	s.requestDir[taskIDStr] = msg
	logger.Log.Info("Новое сообщение в очереди",
		zap.String("taskID", taskIDStr),
		zap.Int("queueSize", len(s.requestDir)))
}

// isRequestQueueEmpty проверяет, пуста ли внутренняя очередь
func (s *Service) isRequestQueueEmpty() bool {
	s.requestDirMu.RLock()
	defer s.requestDirMu.RUnlock()
	return len(s.requestDir) == 0
}

// processRequestQueue обрабатывает сообщения из внутренней очереди
func (s *Service) processRequestQueue(ctx context.Context) {
	for i := 0; i < portion; i++ {
		// Получаем сообщение из очереди
		msg := s.dequeueRequest()
		if msg == nil {
			break
		}

		// Обрабатываем сообщение
		s.sendMessage(ctx, msg)
	}
}

// dequeueRequest извлекает одно сообщение из внутренней очереди
func (s *Service) dequeueRequest() *db.QueueMessage {
	s.requestDirMu.Lock()
	defer s.requestDirMu.Unlock()

	if len(s.requestDir) == 0 {
		return nil
	}

	// Берем первое сообщение (FIFO)
	for taskID, msg := range s.requestDir {
		delete(s.requestDir, taskID)
		return msg
	}

	return nil
}

// sendMessage отправляет одно сообщение
func (s *Service) sendMessage(ctx context.Context, msg *db.QueueMessage) {
	var status int = 2 // Sended по умолчанию
	var statusDesc string

	taskID := int64(-1)
	defer func() {
		// Сохраняем результат в очередь результатов
		if taskID > 0 {
			s.enqueueResponse(taskID, status, statusDesc)
		}
	}()

	if msg == nil {
		logger.Log.Error("Пустое сообщение во внутренней очереди")
		status = 3 // Failed
		statusDesc = "Пустое сообщение"
		return
	}

	// Парсим XML сообщение
	parsed, err := s.queueReader.ParseXMLMessage(msg)
	if err != nil {
		logger.Log.Error("Ошибка парсинга XML", zap.Error(err))
		status = 3 // Failed
		statusDesc = fmt.Sprintf("Ошибка парсинга XML: %v", err)
		return
	}

	// Преобразуем в ParsedEmailMessage
	emailMsg, err := email.ParseEmailMessage(parsed)
	if err != nil {
		logger.Log.Error("Ошибка преобразования в ParsedEmailMessage", zap.Error(err))
		status = 3 // Failed
		statusDesc = fmt.Sprintf("Ошибка преобразования: %v", err)
		return
	}

	taskID = emailMsg.TaskID

	// Проверяем частоту отправки на email адреса
	if err := s.checkAndUpdateRateLimits(emailMsg); err != nil {
		logger.Log.Warn("Ошибка проверки частоты отправки", zap.Error(err), zap.Int64("taskID", taskID))
		// Продолжаем отправку, но логируем предупреждение
	}

	// Проверяем расписание отправки
	if emailMsg.Schedule {
		if err := s.checkSchedule(emailMsg); err != nil {
			status = 3 // Failed
			statusDesc = err.Error()
			logger.Log.Warn("Попытка отправки вне графика",
				zap.Int64("taskID", taskID),
				zap.String("reason", statusDesc))
			return
		}
	}

	// Парсим вложения
	attachments, err := email.ParseAttachments(msg.XMLPayload, emailMsg.TaskID)
	if err != nil {
		logger.Log.Warn("Ошибка парсинга вложений", zap.Error(err), zap.Int64("taskID", emailMsg.TaskID))
	}

	// Обрабатываем вложения
	attachmentData := make([]email.AttachmentData, 0, len(attachments))
	for _, attach := range attachments {
		attachData, err := s.emailService.ProcessAttachment(ctx, &attach, emailMsg.TaskID)
		if err != nil {
			logger.Log.Error("Ошибка обработки вложения",
				zap.Error(err),
				zap.Int64("taskID", emailMsg.TaskID),
				zap.Int("reportType", attach.ReportType),
				zap.String("fileName", attach.FileName))
			// Продолжаем обработку остальных вложений
			continue
		}
		attachmentData = append(attachmentData, *attachData)
	}

	// Отправляем email
	emailMsgForSend := &email.EmailMessage{
		TaskID:       emailMsg.TaskID,
		SmtpID:       emailMsg.SmtpID,
		EmailAddress: emailMsg.EmailAddress,
		Title:        emailMsg.Title,
		Text:         emailMsg.Text,
		Attachments:  attachmentData,
	}

	err = s.emailService.SendEmail(ctx, emailMsgForSend)
	if err != nil {
		status = 3 // Failed
		statusDesc = err.Error()
		logger.Log.Error("Ошибка отправки email", zap.Error(err), zap.Int64("taskID", taskID))

		// Проверяем на критические ошибки
		if s.isCriticalError(err) {
			s.criticalErrorCount.Add(1)
		}
	} else {
		status = 2 // Sended
		statusDesc = fmt.Sprintf("Успешно отправлено SMTP [%d] [%s]", emailMsg.SmtpID, emailMsg.EmailAddress)
		logger.Log.Info("Email успешно отправлен", zap.Int64("taskID", taskID))
	}
}

// checkSchedule проверяет, соответствует ли время отправки расписанию
func (s *Service) checkSchedule(emailMsg *email.ParsedEmailMessage) error {
	if !emailMsg.Schedule {
		return nil
	}

	// Парсим date_active_from
	var activeDate time.Time
	var err error
	if emailMsg.DateActiveFrom != "" {
		// Пробуем разные форматы даты
		formats := []string{
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05",
			"2006-01-02",
		}
		for _, format := range formats {
			activeDate, err = time.Parse(format, emailMsg.DateActiveFrom)
			if err == nil {
				break
			}
		}
		if err != nil {
			// Если не удалось распарсить, используем текущее время
			activeDate = time.Now()
		}
	} else {
		activeDate = time.Now()
	}

	// Получаем время начала и окончания из конфигурации
	now := time.Now()
	timeStart := s.cfg.Schedule.TimeStart
	timeEnd := s.cfg.Schedule.TimeEnd

	// Обновляем дату для timeStart и timeEnd на текущую дату
	todayStart := time.Date(now.Year(), now.Month(), now.Day(),
		timeStart.Hour(), timeStart.Minute(), timeStart.Second(), 0, now.Location())
	todayEnd := time.Date(now.Year(), now.Month(), now.Day(),
		timeEnd.Hour(), timeEnd.Minute(), timeEnd.Second(), 0, now.Location())

	// Если время окончания раньше времени начала, это может означать, что окончание на следующий день
	if todayEnd.Before(todayStart) {
		todayEnd = todayEnd.Add(24 * time.Hour)
	}

	// Проверяем, что activeDate находится в пределах расписания
	activeTime := time.Date(activeDate.Year(), activeDate.Month(), activeDate.Day(),
		activeDate.Hour(), activeDate.Minute(), activeDate.Second(), 0, activeDate.Location())

	if activeTime.Before(todayStart) || activeTime.After(todayEnd) {
		return fmt.Errorf("попытка отправки вне графика %s [%s - %s]",
			activeTime.Format("2006-01-02 15:04:05"),
			todayStart.Format("15:04"),
			todayEnd.Format("15:04"))
	}

	return nil
}

// checkAndUpdateRateLimits проверяет и обновляет ограничения частоты отправки
func (s *Service) checkAndUpdateRateLimits(emailMsg *email.ParsedEmailMessage) error {
	// Очищаем устаревшие записи
	now := time.Now()
	s.sendEmailMu.Lock()
	for address, lastTime := range s.sendEmailMap {
		if lastTime.Before(now) {
			delete(s.sendEmailMap, address)
		}
	}
	s.sendEmailMu.Unlock()

	// Обрабатываем множественные адреса (разделители ; и ,)
	emailAddresses := strings.ReplaceAll(emailMsg.EmailAddress, ",", ";")
	addresses := strings.Split(emailAddresses, ";")

	smtpIndex := emailMsg.SmtpID
	if smtpIndex < 0 || smtpIndex >= len(s.cfg.SMTP) {
		smtpIndex = 0
	}
	smtpCfg := &s.cfg.SMTP[smtpIndex]

	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}

		s.sendEmailMu.Lock()
		lastTime, exists := s.sendEmailMap[address]
		if exists {
			// Проверяем, не превышен ли лимит
			interval := time.Duration(smtpCfg.SMTPMinSendEmailIntervalMsec) * time.Millisecond
			if now.Before(lastTime.Add(interval)) {
				// Ждем, пока не пройдет интервал (максимум 300 итераций по 50 мс = 15 секунд)
				waitUntil := lastTime.Add(interval)
				s.sendEmailMu.Unlock()

				waitCount := 0
				for time.Now().Before(waitUntil) && waitCount < 300 {
					time.Sleep(50 * time.Millisecond)
					waitCount++
				}
				s.sendEmailMu.Lock()
			}
			// Обновляем время последней отправки
			s.sendEmailMap[address] = now.Add(time.Duration(smtpCfg.SMTPMinSendEmailIntervalMsec) * time.Millisecond)
		} else {
			// Добавляем новую запись
			s.sendEmailMap[address] = now.Add(time.Duration(smtpCfg.SMTPMinSendEmailIntervalMsec) * time.Millisecond)
		}
		s.sendEmailMu.Unlock()
	}

	return nil
}

// isCriticalError проверяет, является ли ошибка критической
func (s *Service) isCriticalError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	// ORA-25263 - ошибка Oracle очереди
	if strings.Contains(errStr, "25263") || strings.Contains(errStr, "ora-25263") {
		return true
	}
	// SMTP "4.3.2 Please try again later"
	if strings.Contains(errStr, "4.3.2") && strings.Contains(errStr, "please try again later") {
		return true
	}

	return false
}

// enqueueResponse добавляет результат в очередь результатов
func (s *Service) enqueueResponse(taskID int64, statusID int, errorText string) {
	params := db.SaveEmailResponseParams{
		TaskID:       taskID,
		StatusID:     statusID,
		ResponseDate: time.Now(),
		ErrorText:    errorText,
	}

	select {
	case s.responseQueue <- params:
		// Успешно добавлено в очередь
	default:
		// Очередь переполнена - логируем предупреждение
		logger.Log.Warn("Очередь результатов переполнена, результат может быть потерян",
			zap.Int64("taskID", taskID))
	}
}

// responseQueueWriter записывает результаты из очереди в БД
func (s *Service) responseQueueWriter(ctx context.Context) {
	defer s.responseQueueWg.Done()

	batch := make([]db.SaveEmailResponseParams, 0, 10000)
	ticker := time.NewTicker(1 * time.Second) // Записываем батч каждую секунду
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Записываем оставшиеся результаты перед завершением
			if len(batch) > 0 {
				s.writeResponseBatch(batch)
			}
			return

		case params := <-s.responseQueue:
			batch = append(batch, params)
			// Если батч заполнен, записываем сразу
			if len(batch) >= 10000 {
				s.writeResponseBatch(batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			// Периодически записываем накопленные результаты
			if len(batch) > 0 {
				s.writeResponseBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

// writeResponseBatch записывает батч результатов в БД
func (s *Service) writeResponseBatch(batch []db.SaveEmailResponseParams) {
	for _, params := range batch {
		success, err := s.dbConn.SaveEmailResponse(context.Background(), params)
		if !success {
			if err != nil {
				logger.Log.Error("Ошибка сохранения результата email в БД",
					zap.Int64("taskID", params.TaskID),
					zap.Error(err))
			}
		}
	}
}

// shouldLoadStatuses проверяет, нужно ли загрузить статусы доставки
func (s *Service) shouldLoadStatuses() bool {
	s.loadStatusesMu.Lock()
	defer s.loadStatusesMu.Unlock()

	if s.needRestart.Load() {
		return false
	}

	if time.Now().After(s.nextLoadStatuses) {
		s.nextLoadStatuses = time.Now().Add(60 * time.Minute)
		return true
	}
	return false
}

// loadDeliveryStatuses загружает статусы доставки через POP3
func (s *Service) loadDeliveryStatuses(ctx context.Context) {
	// Обрабатываем каждый SMTP сервер с POP3
	for i, smtpCfg := range s.cfg.SMTP {
		if smtpCfg.POPHost == "" {
			continue // POP3 не настроен для этого SMTP
		}

		pop3Client := email.NewPOP3Client(&smtpCfg)
		sourceEmail := smtpCfg.User

		// Обрабатываем статусы доставки
		err := pop3Client.GetMessagesStatus(ctx, sourceEmail, func(taskID int64, status int, statusDesc string) {
			// Сохраняем статус в очередь результатов
			s.enqueueResponse(taskID, status, statusDesc)
		})

		if err != nil {
			logger.Log.Error("Ошибка получения статусов доставки",
				zap.Int("smtpIndex", i),
				zap.String("popHost", smtpCfg.POPHost),
				zap.Error(err))
		}
	}
}

// logStatistics логирует статистику при завершении
func (s *Service) logStatistics() {
	s.requestDirMu.RLock()
	queueSize := len(s.requestDir)
	s.requestDirMu.RUnlock()

	logger.Log.Info("Статистика при завершении",
		zap.Int("неотправленных Email", queueSize),
		zap.Int32("критических ошибок", s.criticalErrorCount.Load()))
}
