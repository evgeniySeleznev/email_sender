package email

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"email-service/db"
	"email-service/logger"
	"email-service/settings"
	"email-service/storage"
)

// AttachmentProcessor обрабатывает вложения разных типов
type AttachmentProcessor struct {
	dbConn      *db.DBConnection
	cifsManager *storage.CIFSManager
	cfg         *settings.Config
}

// NewAttachmentProcessor создает новый процессор вложений
func NewAttachmentProcessor(dbConn *db.DBConnection, cfg *settings.Config) *AttachmentProcessor {
	var cifsManager *storage.CIFSManager
	if cfg != nil {
		cifsManager = storage.NewCIFSManager(&cfg.Share)
	}
	return &AttachmentProcessor{
		dbConn:      dbConn,
		cifsManager: cifsManager,
		cfg:         cfg,
	}
}

// ProcessAttachment обрабатывает вложение и возвращает данные для отправки
func (p *AttachmentProcessor) ProcessAttachment(ctx context.Context, attach *Attachment, taskID int64) (*AttachmentData, error) {
	switch attach.ReportType {
	case 1:
		// Тип 1: Crystal Reports
		return p.processCrystalReport(ctx, attach, taskID)
	case 2:
		// Тип 2: CLOB из БД
		return p.processCLOB(ctx, attach, taskID)
	case 3:
		// Тип 3: Готовый файл (поддерживает локальные пути и UNC пути через CIFS/SMB)
		return p.processFile(ctx, attach)
	default:
		return nil, fmt.Errorf("неизвестный тип вложения: %d", attach.ReportType)
	}
}

// processCrystalReport обрабатывает Crystal Reports вложение через Web Service
func (p *AttachmentProcessor) processCrystalReport(ctx context.Context, attach *Attachment, taskID int64) (*AttachmentData, error) {
	// Получаем URL Web Service из БД
	url, err := p.dbConn.GetWebServiceUrl()
	if err != nil {
		return nil, fmt.Errorf("ошибка получения URL Web Service: %w", err)
	}

	// Получаем DBInstance из конфигурации
	cfg := p.dbConn.GetConfig()
	if cfg == nil {
		return nil, fmt.Errorf("конфигурация не загружена")
	}

	dbInstance := cfg.Oracle.Instance
	if dbInstance == "" {
		dbInstance = cfg.Oracle.DSN
	}
	if dbInstance == "" {
		return nil, fmt.Errorf("не указан DBInstance в конфигурации")
	}

	// Валидация и установка DBUser и DBPass
	// Если не указаны в XML, используем значения из конфигурации
	dbUser := strings.TrimSpace(attach.DbLogin)
	dbPass := strings.TrimSpace(attach.DbPass)

	if dbUser == "" {
		// Используем значение из конфигурации как fallback
		dbUser = strings.TrimSpace(cfg.Oracle.User)
		if dbUser == "" {
			return nil, fmt.Errorf("не указан db_login для Crystal Reports вложения и отсутствует значение по умолчанию в конфигурации (Oracle.User)")
		}
		if logger.Log != nil {
			logger.Log.Warn("db_login не указан в XML, используется значение из конфигурации",
				zap.Int64("taskID", taskID),
				zap.String("dbUser", dbUser))
		}
	}

	if dbPass == "" {
		// Используем значение из конфигурации как fallback
		dbPass = strings.TrimSpace(cfg.Oracle.Password)
		if dbPass == "" {
			return nil, fmt.Errorf("не указан db_pass для Crystal Reports вложения и отсутствует значение по умолчанию в конфигурации (Oracle.Password)")
		}
		if logger.Log != nil {
			logger.Log.Warn("db_pass не указан в XML, используется значение из конфигурации",
				zap.Int64("taskID", taskID))
		}
	}

	if logger.Log != nil {
		logger.Log.Debug("Обработка Crystal Reports вложения",
			zap.Int64("taskID", taskID),
			zap.String("catalog", attach.Catalog),
			zap.String("file", attach.File),
			zap.String("url", url),
			zap.String("dbInstance", dbInstance),
			zap.String("dbUser", dbUser),
			zap.Bool("dbPassProvided", attach.DbPass != ""))
	}

	// Создаем SOAP клиент с таймаутом из конфигурации
	timeout := 60 * time.Second
	if p.cfg != nil && p.cfg.Mode.CrystalReportsTimeoutSec > 0 {
		timeout = time.Duration(p.cfg.Mode.CrystalReportsTimeoutSec) * time.Second
	}
	client := NewCrystalReportsClient(url, timeout)

	// Шаг 1: Создаем запрос для getReportInfo
	reportRequest := &ReportRequest{
		Main: MainInfo{
			ApplicationName: attach.Catalog,
			DBInstance:      dbInstance,
			DBPass:          dbPass,
			DBUser:          dbUser,
			ExportFormat:    ExportFormatPDF,
			ReportName:      attach.File,
		},
	}

	// Шаг 2: Получаем информацию об отчете
	reportInfo, err := client.GetReportInfo(ctx, reportRequest)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения информации об отчете: %w", err)
	}

	var reportParams []Param
	if reportInfo.MainReport != nil {
		reportParams = reportInfo.MainReport.ReportParams.Params
	}

	if logger.Log != nil {
		logger.Log.Debug("Получена информация об отчете",
			zap.Int64("taskID", taskID),
			zap.Int("paramsCount", len(reportParams)),
			zap.Any("receivedParams", reportParams))
	}

	// Шаг 3: Создаем запрос с параметрами для getReport
	reportWithParams := &ReportWithParams{
		Main: reportRequest.Main,
	}

	// Применяем параметры отчета
	if len(attach.AttachParams) > 0 && len(reportParams) > 0 {
		params := make([]Param, 0, len(reportParams))

		// Создаем карту параметров из attach.AttachParams для быстрого поиска
		paramValues := make(map[string]string)
		for k, v := range attach.AttachParams {
			paramValues[k] = v
		}

		// Обновляем параметры из reportInfo значениями из attach.AttachParams
		for _, infoParam := range reportParams {
			param := infoParam
			if value, ok := paramValues[infoParam.Name]; ok {
				param.Value = value
			}
			params = append(params, param)
		}

		if len(params) > 0 {
			reportWithParams.MainReport = &MainReport{
				ReportParams: ReportParams{
					Params: params,
				},
			}
		}
	}

	if logger.Log != nil {
		var paramsToLog []Param
		if reportWithParams.MainReport != nil {
			paramsToLog = reportWithParams.MainReport.ReportParams.Params
		}
		logger.Log.Debug("Параметры для генерации отчета",
			zap.Int64("taskID", taskID),
			zap.Any("sentParams", paramsToLog))
	}

	// Шаг 4: Генерируем отчет
	base64Data, err := client.GetReport(ctx, reportWithParams)
	if err != nil {
		return nil, fmt.Errorf("ошибка генерации отчета: %w", err)
	}

	// Шаг 5: Декодируем из Base64
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, fmt.Errorf("ошибка декодирования Base64: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("пустой отчет")
	}

	// Проверяем, что данные являются валидным PDF файлом (проверка магических байтов)
	if len(data) < 4 || string(data[0:4]) != "%PDF" {
		return nil, fmt.Errorf("полученные данные не являются валидным PDF файлом (ожидается магический байт %%PDF)")
	}

	// Формируем имя файла: если не указано, используем имя отчета с расширением .pdf
	fileName := attach.FileName
	if fileName == "" {
		// Используем имя отчета (attach.File) как основу
		reportName := attach.File
		// Убираем расширение .rpt, если есть
		if ext := filepath.Ext(reportName); ext == ".rpt" {
			reportName = reportName[:len(reportName)-len(ext)]
		}
		// Добавляем расширение .pdf
		fileName = reportName + ".pdf"
	} else {
		// Если имя указано, но нет расширения .pdf, добавляем его
		if ext := filepath.Ext(fileName); ext != ".pdf" {
			fileName = fileName + ".pdf"
		}
	}

	if logger.Log != nil {
		logger.Log.Debug("Crystal Reports отчет успешно сгенерирован",
			zap.Int64("taskID", taskID),
			zap.String("fileName", fileName),
			zap.Int("size", len(data)))
	}

	return &AttachmentData{
		FileName: fileName,
		Data:     data,
	}, nil
}

// processCLOB обрабатывает CLOB вложение из БД
func (p *AttachmentProcessor) processCLOB(ctx context.Context, attach *Attachment, taskID int64) (*AttachmentData, error) {
	if attach.ClobAttachID == nil {
		return nil, fmt.Errorf("не указан ClobAttachID для типа 2")
	}

	if logger.Log != nil {
		logger.Log.Debug("Обработка CLOB вложения",
			zap.Int64("taskID", taskID),
			zap.Int64("clobID", *attach.ClobAttachID))
	}

	// Получаем CLOB из БД
	clobData, err := p.dbConn.GetEmailReportClob(taskID, *attach.ClobAttachID)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения CLOB: %w", err)
	}

	// Проверяем, что CLOB не пустой
	if len(clobData) == 0 {
		return nil, fmt.Errorf("CLOB вложение пустое (размер 0 байт)")
	}

	if logger.Log != nil {
		logger.Log.Debug("CLOB вложение успешно получено",
			zap.Int64("taskID", taskID),
			zap.Int64("clobID", *attach.ClobAttachID),
			zap.Int("size", len(clobData)))
	}

	// CLOB уже декодирован из Base64 в GetEmailReportClob
	return &AttachmentData{
		FileName: attach.FileName,
		Data:     clobData,
	}, nil
}

// processFile обрабатывает готовый файл (поддерживает локальные пути и UNC пути)
func (p *AttachmentProcessor) processFile(ctx context.Context, attach *Attachment) (*AttachmentData, error) {
	if attach.ReportFile == "" {
		return nil, fmt.Errorf("не указан ReportFile для типа 3")
	}

	if logger.Log != nil {
		logger.Log.Debug("Обработка файла вложения",
			zap.String("file", attach.ReportFile))
	}

	// Нормализация пути (специфичный фикс для 192.168.87.31)
	attach.ReportFile = normalizeReportPath(attach.ReportFile)
	if logger.Log != nil {
		logger.Log.Debug("Нормализованный путь",
			zap.String("file", attach.ReportFile))
	}

	// Проверяем, является ли путь UNC путем (начинается с \\ или //)
	isUNCPath := strings.HasPrefix(attach.ReportFile, `\\`) || strings.HasPrefix(attach.ReportFile, `//`)

	if isUNCPath {
		// Обрабатываем UNC путь через CIFS/SMB
		return p.processUNCFile(ctx, attach)
	}

	// Обрабатываем локальный путь
	// Валидация пути для безопасности
	if !filepath.IsAbs(attach.ReportFile) {
		return nil, fmt.Errorf("путь к файлу должен быть абсолютным: %s", attach.ReportFile)
	}

	// Проверяем, что файл существует
	info, err := os.Stat(attach.ReportFile)
	if err != nil {
		return nil, fmt.Errorf("ошибка проверки файла: %w", err)
	}

	if info.IsDir() {
		return nil, fmt.Errorf("указанный путь является директорией: %s", attach.ReportFile)
	}

	// Проверка размера файла
	maxSizeMB := 100
	if p.cfg != nil && p.cfg.Mode.MaxAttachmentSizeMB > 0 {
		maxSizeMB = p.cfg.Mode.MaxAttachmentSizeMB
	}
	maxSizeBytes := int64(maxSizeMB) * 1024 * 1024

	if info.Size() > maxSizeBytes {
		return nil, fmt.Errorf("размер файла %s (%d байт) превышает лимит %d МБ",
			attach.ReportFile, info.Size(), maxSizeMB)
	}

	// Читаем файл
	file, err := os.Open(attach.ReportFile)
	if err != nil {
		return nil, fmt.Errorf("ошибка открытия файла: %w", err)
	}
	defer file.Close()

	// Читаем данные с контекстом
	done := make(chan error, 1)
	var data []byte

	go func() {
		var readErr error
		data, readErr = io.ReadAll(file)
		done <- readErr
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("ошибка чтения файла: %w", err)
		}
	}

	// Проверяем, что файл не пустой
	if len(data) == 0 {
		return nil, fmt.Errorf("файл вложения пустой (размер 0 байт): %s", attach.ReportFile)
	}

	if logger.Log != nil {
		logger.Log.Debug("Файл вложения успешно прочитан",
			zap.String("file", attach.ReportFile),
			zap.Int("size", len(data)))
	}

	return &AttachmentData{
		FileName: attach.FileName,
		Data:     data,
	}, nil
}

// processUNCFile обрабатывает файл по UNC пути через CIFS/SMB
func (p *AttachmentProcessor) processUNCFile(ctx context.Context, attach *Attachment) (*AttachmentData, error) {
	if p.cifsManager == nil {
		return nil, fmt.Errorf("CIFS менеджер не инициализирован, проверьте настройки [share] в конфигурации")
	}

	// Парсим UNC путь: \\192.168.87.31\shares$\esig_docs\OBN\SEMD\2021\08\ASK_6365065_1.XML
	server, share, relPath, err := storage.ParseUNCPath(attach.ReportFile)
	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга UNC пути %s: %w", attach.ReportFile, err)
	}

	if logger.Log != nil {
		logger.Log.Debug("Обработка файла через CIFS",
			zap.String("server", server),
			zap.String("share", share),
			zap.String("relPath", relPath))
	}

	// Получаем клиент для подключения к шаре
	// Используем relPath как sharePath для правильной работы с пулом подключений
	client, err := p.cifsManager.GetClient(ctx, server, share, relPath)
	if err != nil {
		return nil, fmt.Errorf("ошибка подключения к CIFS шаре %s\\%s: %w", server, share, err)
	}

	// Проверяем существование файла
	exists, err := client.FileExists(relPath)
	if err != nil {
		return nil, fmt.Errorf("ошибка проверки существования файла %s: %w", attach.ReportFile, err)
	}
	if !exists {
		return nil, fmt.Errorf("файл не найден на шаре: %s", attach.ReportFile)
	}

	// Проверка размера файла
	maxSizeMB := 100
	if p.cfg != nil && p.cfg.Mode.MaxAttachmentSizeMB > 0 {
		maxSizeMB = p.cfg.Mode.MaxAttachmentSizeMB
	}
	maxSizeBytes := int64(maxSizeMB) * 1024 * 1024

	fileSize, err := client.GetFileSize(relPath)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения размера файла %s: %w", attach.ReportFile, err)
	}

	if fileSize > maxSizeBytes {
		return nil, fmt.Errorf("размер файла %s (%d байт) превышает лимит %d МБ",
			attach.ReportFile, fileSize, maxSizeMB)
	}

	// Читаем файл с шары
	data, err := client.ReadFileContent(relPath)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения файла с шары %s: %w", attach.ReportFile, err)
	}

	// Проверяем, что файл не пустой
	if len(data) == 0 {
		return nil, fmt.Errorf("файл вложения пустой (размер 0 байт): %s", attach.ReportFile)
	}

	if logger.Log != nil {
		logger.Log.Debug("Файл успешно прочитан с CIFS шары",
			zap.String("file", attach.ReportFile),
			zap.Int("size", len(data)))
	}

	return &AttachmentData{
		FileName: attach.FileName,
		Data:     data,
	}, nil
}

// DecodeBase64 декодирует Base64 строку
func DecodeBase64(encoded string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(encoded)
}

// normalizeReportPath нормализует путь к файлу, применяя специфичные замены
func normalizeReportPath(path string) string {
	// Специфичная логика для замены старого пути 192.168.87.31:shares$:esig_docs
	// Целевой путь: \\192.168.7.120\Applic\Xchange\...
	if strings.Contains(path, "192.168.87.31") || strings.Contains(path, "sto-s") {
		// 1. Заменяем хост
		path = strings.ReplaceAll(path, "192.168.87.31", "192.168.7.120")
		path = strings.ReplaceAll(path, "sto-s", "192.168.7.120")

		// 2. Заменяем шару и путь
		// shares$ -> Applic
		// esig_docs -> Xchange\EDS
		path = strings.ReplaceAll(path, "shares$", "Applic")
		path = strings.ReplaceAll(path, "esig_docs", `Xchange\EDS`)
	}

	// Если путь в формате host:share:path, преобразуем в UNC
	// Пример: 192.168.7.120:Applic:Xchange... -> \\192.168.7.120\Applic\Xchange...
	if strings.Contains(path, ":") && !strings.HasPrefix(path, `\\`) && !strings.HasPrefix(path, `//`) && !strings.Contains(path, `:\`) {
		// Это не диск C:\, а скорее всего host:share format
		parts := strings.SplitN(path, ":", 3)
		if len(parts) >= 2 {
			// Собираем UNC путь
			newPath := `\\` + parts[0] + `\` + parts[1]
			if len(parts) > 2 {
				newPath += `\` + parts[2]
			}
			// Исправляем возможные двойные слэши или смешанные слэши, если они возникли
			newPath = strings.ReplaceAll(newPath, `\\`, `\`)
			newPath = `\\` + strings.TrimPrefix(newPath, `\`) // Возвращаем ведущие \\
			return newPath
		}
	}

	return path
}
