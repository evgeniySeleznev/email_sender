package db

import (
	"context"
	"database/sql"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"email-service/logger"
)

// QueueMessage представляет сообщение из очереди Oracle AQ
type QueueMessage struct {
	MessageID   string
	XMLPayload  string
	RawPayload  []byte
	DequeueTime time.Time
}

// QueueReader инкапсулирует работу с очередью Oracle AQ
type QueueReader struct {
	dbConn       *DBConnection
	queueName    string
	consumerName string
	waitTimeout  int // в секундах
	mu           sync.Mutex
}

// NewQueueReader создает новый экземпляр QueueReader
func NewQueueReader(dbConn *DBConnection) (*QueueReader, error) {
	cfg := dbConn.GetConfig()
	if cfg == nil {
		return nil, errors.New("конфигурация не загружена")
	}

	// Проверяем секцию [queue] или используем значения по умолчанию
	var queueName, consumerName string
	if cfg.File.HasSection("queue") {
		queueSec := cfg.File.Section("queue")
		queueName = queueSec.Key("queue_name").String()
		consumerName = queueSec.Key("consumer_name").String()
	}

	if queueName == "" {
		queueName = "askaq.aq_ask" // Значение по умолчанию
	}
	if consumerName == "" {
		consumerName = "SUB_EMAIL_SENDER" // Значение по умолчанию
	}

	return &QueueReader{
		dbConn:       dbConn,
		queueName:    queueName,
		consumerName: consumerName,
		waitTimeout:  2, // 2 секунды по умолчанию
	}, nil
}

// DequeueMany извлекает несколько сообщений из очереди
// Возвращает слайс сообщений, может быть пустым если очередь пуста
func (qr *QueueReader) DequeueMany(ctx context.Context, count int) ([]*QueueMessage, error) {
	qr.mu.Lock()
	defer qr.mu.Unlock()

	if count <= 0 {
		count = 1
	}

	consumerName := qr.consumerName
	if consumerName == "" {
		consumerName = "NULL"
	}
	if logger.Log != nil {
		logger.Log.Debug("Попытка извлечения сообщений из очереди",
			zap.Int("count", count),
			zap.String("queue", qr.queueName),
			zap.String("consumer", consumerName),
			zap.Int("timeout", qr.waitTimeout))
	}

	// Создаем контекст с таймаутом для операций
	opCtx, cancel := context.WithTimeout(ctx, ExecTimeout)
	defer cancel()

	// Создаем пакет один раз перед извлечением всех сообщений
	if err := qr.ensurePackageExists(opCtx); err != nil {
		return nil, fmt.Errorf("ошибка создания пакета: %w", err)
	}

	var messages []*QueueMessage

	// Извлекаем сообщения по одному
	// Для первого сообщения используем полный waitTimeout, для последующих - минимальный (50 мс)
	for i := 0; i < count; i++ {
		// Проверяем контекст перед каждой итерацией
		select {
		case <-ctx.Done():
			if logger.Log != nil {
				logger.Log.Info("Операция чтения из очереди прервана",
					zap.Int("received", len(messages)))
			}
			return messages, ctx.Err()
		default:
		}

		// Для первого сообщения используем полный timeout, для остальных - минимальный
		var timeout float64
		if i == 0 {
			timeout = float64(qr.waitTimeout)
		} else {
			// Для последующих сообщений используем минимальный timeout (50 миллисекунд = 0.05 секунды)
			// чтобы быстро определить, что очередь пуста
			timeout = 0.05
		}

		msg, err := qr.dequeueOneMessageWithTimeout(ctx, timeout)
		if err != nil {
			if ctx.Err() != nil {
				if logger.Log != nil {
					logger.Log.Info("Операция чтения из очереди прервана из-за отмены контекста",
						zap.Int("received", len(messages)))
				}
				return messages, ctx.Err()
			}
			return messages, err
		}
		if msg == nil {
			// Очередь пуста
			break
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

// ensurePackageExists создает пакет Oracle для работы с очередью, если он еще не существует
func (qr *QueueReader) ensurePackageExists(ctx context.Context) error {
	createPackageSQL := `
		CREATE OR REPLACE PACKAGE temp_queue_pkg AS
			g_msgid RAW(16);
			g_payload XMLType;
			g_success NUMBER := 0;
			g_error_code NUMBER := 0;
			g_error_msg VARCHAR2(4000);
			
			FUNCTION get_success RETURN NUMBER;
			FUNCTION get_error_code RETURN NUMBER;
			FUNCTION get_error_msg RETURN VARCHAR2;
			FUNCTION get_msgid RETURN RAW;
			FUNCTION get_payload RETURN XMLType;
		END temp_queue_pkg;
	`

	err := qr.dbConn.WithDB(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, createPackageSQL)
		if err != nil {
			return fmt.Errorf("не удалось создать пакет: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	createPackageBodySQL := `
		CREATE OR REPLACE PACKAGE BODY temp_queue_pkg AS
			FUNCTION get_success RETURN NUMBER IS
			BEGIN
				RETURN g_success;
			END;
			
			FUNCTION get_error_code RETURN NUMBER IS
			BEGIN
				RETURN g_error_code;
			END;
			
			FUNCTION get_error_msg RETURN VARCHAR2 IS
			BEGIN
				RETURN g_error_msg;
			END;
			
			FUNCTION get_msgid RETURN RAW IS
			BEGIN
				RETURN g_msgid;
			END;
			
			FUNCTION get_payload RETURN XMLType IS
			BEGIN
				RETURN g_payload;
			END;
		END temp_queue_pkg;
	`

	err = qr.dbConn.WithDB(func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, createPackageBodySQL)
		if err != nil {
			return fmt.Errorf("не удалось создать тело пакета: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// dequeueOneMessageWithTimeout извлекает одно сообщение из очереди с указанным timeout (в секундах)
func (qr *QueueReader) dequeueOneMessageWithTimeout(ctx context.Context, timeout float64) (*QueueMessage, error) {
	plsql := `
		DECLARE
			v_dequeue_options DBMS_AQ.dequeue_options_t;
			v_message_properties DBMS_AQ.message_properties_t;
			v_consumer_name VARCHAR2(128);
		BEGIN
			-- Инициализируем переменные пакета
			temp_queue_pkg.g_success := 0;
			temp_queue_pkg.g_error_code := 0;
			temp_queue_pkg.g_error_msg := NULL;
			
			-- Настраиваем опции dequeue
			v_dequeue_options.dequeue_mode := DBMS_AQ.REMOVE;
			v_dequeue_options.wait := :1;
			v_dequeue_options.navigation := DBMS_AQ.FIRST_MESSAGE;
			
			-- Устанавливаем consumer_name только если он не пустой
			IF :2 IS NOT NULL THEN
				v_consumer_name := :2;
				IF LENGTH(TRIM(v_consumer_name)) > 0 THEN
					v_dequeue_options.consumer_name := TRIM(v_consumer_name);
				END IF;
			END IF;
			
			-- Выполняем dequeue и сохраняем результат в пакетные переменные
			DBMS_AQ.DEQUEUE(
				queue_name => :3,
				dequeue_options => v_dequeue_options,
				message_properties => v_message_properties,
				payload => temp_queue_pkg.g_payload,
				msgid => temp_queue_pkg.g_msgid
			);
			
			-- Устанавливаем флаг успеха
			temp_queue_pkg.g_success := 1;
			temp_queue_pkg.g_error_code := 0;
			temp_queue_pkg.g_error_msg := NULL;
		EXCEPTION
			WHEN OTHERS THEN
				temp_queue_pkg.g_error_code := SQLCODE;
				temp_queue_pkg.g_error_msg := SUBSTR(SQLERRM, 1, 4000);
				IF SQLCODE = -25228 THEN
					-- Очередь пуста - это нормально
					temp_queue_pkg.g_success := 0;
				ELSE
					-- Другая ошибка - поднимаем исключение
					temp_queue_pkg.g_success := 0;
					RAISE;
				END IF;
		END;
	`

	var consumerParam interface{}
	if qr.consumerName == "" {
		consumerParam = nil
	} else {
		consumerParam = strings.TrimSpace(qr.consumerName)
	}

	txTimeout := time.Duration(timeout)*time.Second + 5*time.Second
	if txTimeout > ExecTimeout {
		txTimeout = ExecTimeout
	}
	txCtx, txCancel := context.WithTimeout(ctx, txTimeout)
	defer txCancel()

	var msg *QueueMessage
	err := qr.dbConn.WithDBTx(txCtx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(txCtx, plsql,
			timeout,
			consumerParam,
			qr.queueName,
		)

		if err != nil {
			isContextCanceled := ctx.Err() == context.Canceled || txCtx.Err() == context.Canceled

			errStr := err.Error()
			if strings.Contains(errStr, "25228") || strings.Contains(errStr, "-25228") {
				if logger.Log != nil {
					logger.Log.Debug("Очередь пуста (код ошибки 25228)")
				}
				return nil
			}

			if isContextCanceled {
				if logger.Log != nil {
					logger.Log.Info("Операция dequeue отменена из-за graceful shutdown",
						zap.String("consumer", qr.consumerName),
						zap.String("queue", qr.queueName))
				}
				return fmt.Errorf("операция отменена: %w", ctx.Err())
			}

			if logger.Log != nil {
				logger.Log.Error("Ошибка выполнения PL/SQL для dequeue",
					zap.Error(err),
					zap.String("consumer", qr.consumerName),
					zap.String("queue", qr.queueName),
					zap.Float64("timeout", timeout))
			}
			return fmt.Errorf("ошибка выполнения PL/SQL: %w", err)
		}

		checkSuccessSQL := `SELECT temp_queue_pkg.get_success(), temp_queue_pkg.get_error_code(), temp_queue_pkg.get_error_msg() FROM DUAL`
		var successFlag, errorCode sql.NullInt64
		var errorMsg sql.NullString
		err = tx.QueryRowContext(txCtx, checkSuccessSQL).Scan(&successFlag, &errorCode, &errorMsg)
		if err != nil {
			isContextCanceled := ctx.Err() == context.Canceled || txCtx.Err() == context.Canceled
			if isContextCanceled {
				if logger.Log != nil {
					logger.Log.Info("Проверка результата dequeue отменена из-за graceful shutdown")
				}
				return fmt.Errorf("операция отменена: %w", ctx.Err())
			}
			return fmt.Errorf("ошибка проверки результата dequeue: %w", err)
		}

		if !successFlag.Valid || successFlag.Int64 == 0 {
			if errorCode.Valid && errorCode.Int64 == -25228 {
				return nil
			}
			errText := "неизвестная ошибка"
			if errorMsg.Valid && errorMsg.String != "" {
				errText = errorMsg.String
			}
			return fmt.Errorf("ошибка Oracle (код %d): %s", errorCode.Int64, errText)
		}

		query := `SELECT RAWTOHEX(temp_queue_pkg.get_msgid()) as msgid, 
		             XMLSerialize(DOCUMENT temp_queue_pkg.get_payload() AS CLOB) as payload 
		          FROM DUAL`

		rows, err := tx.QueryContext(txCtx, query)
		if err != nil {
			if logger.Log != nil {
				logger.Log.Error("Ошибка выполнения SELECT с XMLSerialize", zap.Error(err))
			}
			return fmt.Errorf("ошибка выполнения SELECT с XMLSerialize: %w", err)
		}
		defer rows.Close()

		if !rows.Next() {
			return nil
		}

		var msgid, payload sql.NullString
		if err := rows.Scan(&msgid, &payload); err != nil {
			return fmt.Errorf("ошибка чтения данных: %w", err)
		}

		if !payload.Valid || payload.String == "" {
			return nil
		}

		xmlString := payload.String

		msgidStr := ""
		if msgid.Valid {
			msgidStr = msgid.String
		}

		msg = &QueueMessage{
			MessageID:   msgidStr,
			XMLPayload:  xmlString,
			RawPayload:  []byte(xmlString),
			DequeueTime: time.Now(),
		}

		if logger.Log != nil {
			logger.Log.Debug("Получено сообщение из очереди",
				zap.String("messageID", msg.MessageID),
				zap.Int("size", len(msg.RawPayload)))
		}

		return nil
	})

	if err != nil {
		if ctx.Err() == context.Canceled {
			if logger.Log != nil {
				logger.Log.Info("Операция dequeue отменена из-за graceful shutdown")
			}
			return nil, fmt.Errorf("операция отменена: %w", ctx.Err())
		}
		return nil, err
	}

	return msg, nil
}

// ParseXMLMessage парсит XML сообщение из очереди
func (qr *QueueReader) ParseXMLMessage(msg *QueueMessage) (map[string]interface{}, error) {
	if msg == nil || msg.XMLPayload == "" {
		return nil, errors.New("сообщение пусто или не содержит XML")
	}

	// Парсим корневой элемент root
	type Root struct {
		XMLName xml.Name `xml:"root"`
		Head    struct {
			DateActiveFrom string `xml:"date_active_from"`
		} `xml:"head"`
		Body struct {
			InnerXML string `xml:",innerxml"`
		} `xml:"body"`
	}

	var root Root
	xmlBytes := []byte(msg.XMLPayload)
	if err := xml.Unmarshal(xmlBytes, &root); err != nil {
		return nil, fmt.Errorf("ошибка парсинга корневого XML: %w, XML: %s", err, truncateString(msg.XMLPayload, 500))
	}

	// Парсим внутренний XML из body
	type EmailData struct {
		EmailTaskID     string `xml:"email_task_id,attr"`
		SmtpID          string `xml:"smtp_id,attr"`
		EmailAddress    string `xml:"email_address,attr"`
		EmailTitle      string `xml:"email_title,attr"`
		EmailText       string `xml:"email_text,attr"`
		SendingSchedule string `xml:"sending_schedule,attr"`
	}

	var emailData EmailData
	bodyXML := strings.TrimSpace(root.Body.InnerXML)

	if bodyXML == "" {
		return nil, fmt.Errorf("body пуст, XML: %s", truncateString(msg.XMLPayload, 500))
	}

	// Извлекаем содержимое из CDATA, если оно там есть
	bodyXML = extractCDATAContent(bodyXML)

	// Парсим внутренний XML из body
	if err := xml.Unmarshal([]byte(bodyXML), &emailData); err != nil {
		return nil, fmt.Errorf("ошибка парсинга внутреннего XML из body: %w, body content: %s", err, truncateString(bodyXML, 500))
	}

	result := map[string]interface{}{
		"message_id":       msg.MessageID,
		"dequeue_time":     msg.DequeueTime,
		"date_active_from": root.Head.DateActiveFrom,
		"email_task_id":    emailData.EmailTaskID,
		"smtp_id":          emailData.SmtpID,
		"email_address":    emailData.EmailAddress,
		"email_title":      emailData.EmailTitle,
		"email_text":       emailData.EmailText,
		"sending_schedule": emailData.SendingSchedule,
	}

	return result, nil
}

// extractCDATAContent извлекает содержимое из CDATA секции
func extractCDATAContent(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	cdataStart := "<![CDATA["
	cdataEnd := "]]>"

	startIdx := strings.Index(s, cdataStart)
	if startIdx != -1 {
		endIdx := strings.Index(s[startIdx+len(cdataStart):], cdataEnd)
		if endIdx != -1 {
			contentStart := startIdx + len(cdataStart)
			contentEnd := startIdx + len(cdataStart) + endIdx
			content := s[contentStart:contentEnd]
			return strings.TrimSpace(content)
		}
	}

	if strings.HasPrefix(strings.TrimSpace(s), "<") {
		return s
	}

	return s
}

// truncateString обрезает строку до указанной длины
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// GetQueueName возвращает имя очереди
func (qr *QueueReader) GetQueueName() string {
	return qr.queueName
}

// GetConsumerName возвращает имя consumer
func (qr *QueueReader) GetConsumerName() string {
	return qr.consumerName
}

// SetWaitTimeout устанавливает таймаут ожидания сообщений (в секундах)
func (qr *QueueReader) SetWaitTimeout(seconds int) {
	qr.mu.Lock()
	defer qr.mu.Unlock()
	qr.waitTimeout = seconds
}
