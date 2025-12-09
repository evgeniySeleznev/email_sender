package email

import (
	"context"
	"fmt"
	"strings"

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
	// В raw string: \\\\ = \\ (два обратных слэша), \\ = \ (один обратный слэш)
	// Результат: \\192.168.3.79\build\SMS_Sender\25.7\installDir\tester_instruction.md
	testFilePath := `\\\\192.168.3.79\\build\\SMS_Sender\\25.7\\installDir\\tester_instruction.md`

	if logger.Log != nil {
		logger.Log.Info("Добавление тестового вложения для типа 3",
			zap.String("file", testFilePath))
	}

	// Нормализуем путь: в raw string \\\\ становится \\, а \\ становится \
	// Нужно нормализовать множественные обратные слэши в середине пути
	// но сохранить \\ в начале (UNC префикс)
	normalizedPath := testFilePath
	// Заменяем множественные обратные слэши (3+) на одинарные, но сохраняем \\ в начале
	if strings.HasPrefix(normalizedPath, `\\`) {
		// Сохраняем префикс \\
		rest := normalizedPath[2:]
		// Заменяем множественные \ на одинарные
		for strings.Contains(rest, `\\`) {
			rest = strings.ReplaceAll(rest, `\\`, `\`)
		}
		normalizedPath = `\\` + rest
	} else {
		// Если нет префикса, добавляем и нормализуем
		trimmed := strings.TrimLeft(normalizedPath, `\/`)
		for strings.Contains(trimmed, `\\`) {
			trimmed = strings.ReplaceAll(trimmed, `\\`, `\`)
		}
		normalizedPath = `\\` + trimmed
	}

	// Парсим UNC путь
	server, share, relPath, err := storage.ParseUNCPath(normalizedPath)
	if err != nil {
		if logger.Log != nil {
			logger.Log.Error("Ошибка парсинга UNC пути тестового файла",
				zap.Error(err),
				zap.String("file", testFilePath))
		}
		return attachments, fmt.Errorf("ошибка парсинга UNC пути: %w", err)
	}

	// Получаем клиент для подключения к шаре
	// Используем relPath как sharePath для правильной работы с пулом подключений
	client, err := p.cifsManager.GetClient(ctx, server, share, relPath)
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
	data, err := client.ReadFileContent(relPath)
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
