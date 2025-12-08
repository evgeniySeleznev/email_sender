package email

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"email-service/logger"
	"email-service/storage"
)

// AddTestAttachmentForType3 добавляет тестовое вложение к письмам типа 3 с ЭЦП
// ВАЖНО: Этот метод создан для тестирования и должен быть удален после тестов
func (p *AttachmentProcessor) AddTestAttachmentForType3(ctx context.Context, attachments []AttachmentData, hasType3 bool) ([]AttachmentData, error) {
	// Добавляем тестовое вложение только если есть вложения типа 3
	if !hasType3 {
		return attachments, nil
	}

	if p.cifsManager == nil {
		if logger.Log != nil {
			logger.Log.Warn("CIFS менеджер не инициализирован, пропускаем добавление тестового вложения")
		}
		return attachments, nil
	}

	// UNC путь к тестовому файлу
	testFilePath := `\\assdocker\build\SMS_Sender\25.7\installDir\tester_instruction.md`

	if logger.Log != nil {
		logger.Log.Info("Добавление тестового вложения для типа 3",
			zap.String("file", testFilePath))
	}

	// Парсим UNC путь
	server, share, relPath, err := storage.ParseUNCPath(testFilePath)
	if err != nil {
		if logger.Log != nil {
			logger.Log.Error("Ошибка парсинга UNC пути тестового файла",
				zap.Error(err),
				zap.String("file", testFilePath))
		}
		return attachments, fmt.Errorf("ошибка парсинга UNC пути: %w", err)
	}

	// Получаем клиент для подключения к шаре
	client, err := p.cifsManager.GetClient(ctx, server, share)
	if err != nil {
		if logger.Log != nil {
			logger.Log.Error("Ошибка подключения к CIFS шаре для тестового файла",
				zap.Error(err),
				zap.String("server", server),
				zap.String("share", share))
		}
		return attachments, fmt.Errorf("ошибка подключения к CIFS шаре: %w", err)
	}

	// Проверяем существование файла
	exists, err := client.FileExists(relPath)
	if err != nil {
		if logger.Log != nil {
			logger.Log.Error("Ошибка проверки существования тестового файла",
				zap.Error(err),
				zap.String("file", testFilePath))
		}
		return attachments, fmt.Errorf("ошибка проверки файла: %w", err)
	}
	if !exists {
		if logger.Log != nil {
			logger.Log.Warn("Тестовый файл не найден на шаре",
				zap.String("file", testFilePath))
		}
		return attachments, nil // Не добавляем вложение, если файл не найден
	}

	// Читаем файл с шары
	data, err := client.ReadFile(relPath)
	if err != nil {
		if logger.Log != nil {
			logger.Log.Error("Ошибка чтения тестового файла с шары",
				zap.Error(err),
				zap.String("file", testFilePath))
		}
		return attachments, fmt.Errorf("ошибка чтения файла: %w", err)
	}

	if len(data) == 0 {
		if logger.Log != nil {
			logger.Log.Warn("Тестовый файл пустой",
				zap.String("file", testFilePath))
		}
		return attachments, nil
	}

	// Добавляем тестовое вложение
	testAttachment := AttachmentData{
		FileName: "tester_instruction.md",
		Data:     data,
	}

	if logger.Log != nil {
		logger.Log.Info("Тестовое вложение успешно добавлено",
			zap.String("fileName", testAttachment.FileName),
			zap.Int("size", len(data)))
	}

	// Добавляем в начало списка вложений (или в конец - как удобнее)
	attachments = append(attachments, testAttachment)

	return attachments, nil
}
