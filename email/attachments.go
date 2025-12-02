package email

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"email-service/db"
	"email-service/logger"
)

// AttachmentProcessor обрабатывает вложения разных типов
type AttachmentProcessor struct {
	dbConn *db.DBConnection
}

// NewAttachmentProcessor создает новый процессор вложений
func NewAttachmentProcessor(dbConn *db.DBConnection) *AttachmentProcessor {
	return &AttachmentProcessor{
		dbConn: dbConn,
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
		// Тип 3: Готовый файл
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

	if logger.Log != nil {
		logger.Log.Debug("Обработка Crystal Reports вложения",
			zap.Int64("taskID", taskID),
			zap.String("catalog", attach.Catalog),
			zap.String("file", attach.File),
			zap.String("url", url),
			zap.String("dbInstance", dbInstance))
	}

	// Создаем SOAP клиент
	client := NewCrystalReportsClient(url)

	// Шаг 1: Создаем запрос для getReportInfo
	reportRequest := &ReportRequest{
		Main: MainInfo{
			ApplicationName: attach.Catalog,
			DBInstance:     dbInstance,
			DBPass:         attach.DbPass,
			DBUser:         attach.DbLogin,
			ExportFormat:   ExportFormatPDF,
			ReportName:     attach.File,
		},
	}

	// Шаг 2: Получаем информацию об отчете
	reportInfo, err := client.GetReportInfo(ctx, reportRequest)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения информации об отчете: %w", err)
	}

	if logger.Log != nil {
		logger.Log.Debug("Получена информация об отчете",
			zap.Int64("taskID", taskID),
			zap.Int("paramsCount", len(reportInfo.Params)))
	}

	// Шаг 3: Создаем запрос с параметрами для getReport
	reportWithParams := &ReportWithParams{
		Main: reportRequest.Main,
	}

	// Применяем параметры отчета
	if len(attach.AttachParams) > 0 && len(reportInfo.Params) > 0 {
		params := make([]Param, 0, len(reportInfo.Params))

		// Создаем карту параметров из attach.AttachParams для быстрого поиска
		paramValues := make(map[string]string)
		for k, v := range attach.AttachParams {
			paramValues[k] = v
		}

		// Обновляем параметры из reportInfo значениями из attach.AttachParams
		for _, infoParam := range reportInfo.Params {
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

	if logger.Log != nil {
		logger.Log.Debug("Crystal Reports отчет успешно сгенерирован",
			zap.Int64("taskID", taskID),
			zap.Int("size", len(data)))
	}

	return &AttachmentData{
		FileName: attach.FileName,
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

	// CLOB уже декодирован из Base64 в GetEmailReportClob
	return &AttachmentData{
		FileName: attach.FileName,
		Data:     clobData,
	}, nil
}

// processFile обрабатывает готовый файл
func (p *AttachmentProcessor) processFile(ctx context.Context, attach *Attachment) (*AttachmentData, error) {
	if attach.ReportFile == "" {
		return nil, fmt.Errorf("не указан ReportFile для типа 3")
	}

	if logger.Log != nil {
		logger.Log.Debug("Обработка файла вложения",
			zap.String("file", attach.ReportFile))
	}

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

	return &AttachmentData{
		FileName: attach.FileName,
		Data:     data,
	}, nil
}

// DecodeBase64 декодирует Base64 строку
func DecodeBase64(encoded string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(encoded)
}
