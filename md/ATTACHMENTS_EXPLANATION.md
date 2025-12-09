# Подробное объяснение работы отправки вложений в email сообщении

## Обзор процесса

Процесс отправки email с вложениями состоит из следующих этапов:
1. Парсинг XML сообщения из очереди Oracle
2. Извлечение информации о вложениях из XML
3. Обработка каждого вложения (загрузка данных)
4. Формирование MIME сообщения с вложениями
5. Отправка через SMTP

---

## Этап 1: Парсинг XML и извлечение вложений

### Файл: `service/service.go`, функция `sendMessage`

```325:329:service/service.go
	// Парсим вложения
	attachments, err := email.ParseAttachments(msg.XMLPayload, emailMsg.TaskID)
	if err != nil {
		logger.Log.Warn("Ошибка парсинга вложений", zap.Error(err), zap.Int64("taskID", emailMsg.TaskID))
	}
```

**Строка 326**: Вызывается функция `ParseAttachments`, которая парсит XML из `msg.XMLPayload` и извлекает информацию о вложениях. XML содержит элементы `<attach>` с атрибутами:
- `report_type` - тип вложения (1, 2 или 3)
- `email_attach_id` - ID для типа 2 (CLOB)
- `email_attach_name` - имя файла
- `report_file` - путь к файлу для типа 3
- `email_attach_catalog`, `email_attach_file` - параметры для типа 1 (Crystal Reports)
- `db_login`, `db_pass` - учетные данные для типа 1
- Внутренние элементы `<attach_param>` - параметры для Crystal Reports

**Строка 327-329**: Если парсинг не удался, логируется предупреждение, но обработка продолжается (вложения будут пустыми).

---

## Этап 2: Обработка вложений (загрузка данных)

### Файл: `service/service.go`, функция `sendMessage`

```331:345:service/service.go
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
```

**Строка 332**: Создается слайс `attachmentData` для хранения обработанных вложений с предварительным выделением памяти на количество вложений.

**Строка 333**: Цикл по всем вложениям из XML.

**Строка 334**: Вызывается `ProcessAttachment`, которая обрабатывает вложение в зависимости от его типа:
- **Тип 1** (Crystal Reports): загрузка через SOAP Web Service (пока не реализовано)
- **Тип 2** (CLOB): загрузка из Oracle БД через процедуру `pcsystem.pkg_email.get_email_report_clob()`
- **Тип 3** (Файл): чтение файла с диска

**Строка 335-343**: Если обработка вложения не удалась, логируется ошибка, но обработка остальных вложений продолжается (не блокируем отправку из-за одного плохого вложения).

**Строка 344**: Успешно обработанное вложение добавляется в массив `attachmentData`.

---

## Этап 3: Обработка вложений по типам

### Файл: `email/attachments.go`, функция `ProcessAttachment`

```29:44:email/attachments.go
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
```

**Строка 31**: Используется `switch` для выбора обработчика в зависимости от типа вложения.

**Строка 32-34**: **Тип 1** - Crystal Reports через Web Service (пока возвращает ошибку).

**Строка 35-37**: **Тип 2** - CLOB из Oracle БД.

**Строка 38-40**: **Тип 3** - файл с диска.

**Строка 41-43**: Неизвестный тип возвращает ошибку.

### Обработка типа 2 (CLOB)

```68:91:email/attachments.go
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
```

**Строка 70-72**: Проверка наличия `ClobAttachID` (обязателен для типа 2).

**Строка 81**: Вызов `GetEmailReportClob`, который:
1. Вызывает Oracle процедуру `pcsystem.pkg_email.get_email_report_clob(p_email_attach_id => clobID)`
2. Получает CLOB данные (в Base64)
3. Декодирует из Base64 в бинарные данные

**Строка 87-90**: Возвращается структура `AttachmentData` с именем файла и бинарными данными.

### Обработка типа 3 (Файл)

```93:149:email/attachments.go
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
```

**Строка 95-97**: Проверка наличия пути к файлу.

**Строка 105-107**: Проверка, что путь абсолютный (безопасность - предотвращает доступ к файлам вне разрешенных директорий).

**Строка 110-113**: Проверка существования файла через `os.Stat`.

**Строка 115-117**: Проверка, что путь не является директорией.

**Строка 120-124**: Открытие файла и гарантированное закрытие через `defer`.

**Строка 127-134**: Асинхронное чтение файла в горутине с поддержкой отмены через контекст.

**Строка 136-143**: Ожидание завершения чтения или отмены контекста.

**Строка 145-148**: Возврат данных вложения.

---

## Этап 4: Формирование email сообщения с вложениями

### Файл: `service/service.go`, функция `sendMessage`

```347:355:service/service.go
	// Отправляем email
	emailMsgForSend := &email.EmailMessage{
		TaskID:       emailMsg.TaskID,
		SmtpID:       emailMsg.SmtpID,
		EmailAddress: emailMsg.EmailAddress,
		Title:        emailMsg.Title,
		Text:         emailMsg.Text,
		Attachments:  attachmentData,
	}
```

**Строка 348-355**: Создается структура `EmailMessage` с обработанными вложениями для отправки.

### Файл: `email/smtp.go`, функция `buildEmailMessage`

```142:210:email/smtp.go
// buildEmailMessage формирует тело email сообщения с поддержкой вложений
func (c *SMTPClient) buildEmailMessage(msg *EmailMessage, recipientEmails []string, isBodyHTML bool, sendHiddenCopyToSelf bool) string {
	// Формируем основные заголовки
	headers := fmt.Sprintf("From: %s <%s>\r\n", c.cfg.DisplayName, c.cfg.User)

	// To: адреса
	toHeader := strings.Join(recipientEmails, ", ")
	headers += fmt.Sprintf("To: %s\r\n", toHeader)

	// BCC: скрытая копия себе (если включено)
	if sendHiddenCopyToSelf {
		headers += fmt.Sprintf("Bcc: %s\r\n", c.cfg.User)
	}

	headers += fmt.Sprintf("Subject: %s\r\n", msg.Title)
	headers += fmt.Sprintf("Message-ID: <%s@%s>\r\n", fmt.Sprintf("askemailsender%d", msg.TaskID), c.cfg.Host)
	headers += fmt.Sprintf("X-Envelope-ID: askemailsender%d\r\n", msg.TaskID) // Для DSN
	headers += "MIME-Version: 1.0\r\n"

	// Определяем Content-Type для тела сообщения
	textContentType := "text/plain; charset=UTF-8"
	if isBodyHTML {
		textContentType = "text/html; charset=UTF-8"
	}

	// Если есть вложения, используем multipart/mixed
	if len(msg.Attachments) > 0 {
		boundary := fmt.Sprintf("boundary_%d_%d", msg.TaskID, time.Now().Unix())
		headers += fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary)
		headers += "\r\n"

		// Тело сообщения
		body := fmt.Sprintf("--%s\r\n", boundary)
		body += fmt.Sprintf("Content-Type: %s\r\n", textContentType)
		body += "Content-Transfer-Encoding: 8bit\r\n"
		body += "\r\n"
		body += msg.Text
		body += "\r\n\r\n"

		// Добавляем вложения
		for _, attach := range msg.Attachments {
			// Определяем MIME тип по расширению файла
			ext := filepath.Ext(attach.FileName)
			mimeType := mime.TypeByExtension(ext)
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}

			body += fmt.Sprintf("--%s\r\n", boundary)
			body += fmt.Sprintf("Content-Type: %s\r\n", mimeType)
			body += fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", attach.FileName)
			body += "Content-Transfer-Encoding: base64\r\n"
			body += "\r\n"

			// Кодируем вложение в Base64
			encoded := base64.StdEncoding.EncodeToString(attach.Data)
			// Разбиваем на строки по 76 символов (RFC 2045)
			for i := 0; i < len(encoded); i += 76 {
				end := i + 76
				if end > len(encoded) {
					end = len(encoded)
				}
				body += encoded[i:end] + "\r\n"
			}
			body += "\r\n"
		}

		body += fmt.Sprintf("--%s--\r\n", boundary)
		return headers + body
	}

	// Без вложений - простое сообщение
	headers += fmt.Sprintf("Content-Type: %s\r\n", textContentType)
	headers += "\r\n"
	body := headers + msg.Text

	return body
}
```

**Строка 145**: Формируется заголовок `From` с именем отправителя и email адресом.

**Строка 148**: Формируется заголовок `To` со списком получателей через запятую.

**Строка 151-153**: Если включена скрытая копия себе, добавляется заголовок `Bcc`.

**Строка 156**: Заголовок `Subject` с темой письма.

**Строка 157**: `Message-ID` - уникальный идентификатор сообщения для отслеживания.

**Строка 158**: `X-Envelope-ID` - для DSN (Delivery Status Notification).

**Строка 159**: `MIME-Version: 1.0` - указывает, что используется MIME стандарт.

**Строка 162-165**: Определяется Content-Type для тела сообщения (text/plain или text/html).

**Строка 168**: Проверка наличия вложений. Если есть - используется формат `multipart/mixed`.

**Строка 169**: Генерируется уникальный boundary (разделитель) для multipart сообщения на основе TaskID и текущего времени.

**Строка 170**: Устанавливается `Content-Type: multipart/mixed` с указанием boundary.

**Строка 174**: Начало первой части (boundary с `--`).

**Строка 175**: Content-Type для текста письма.

**Строка 176**: `Content-Transfer-Encoding: 8bit` - кодировка для текста.

**Строка 178**: Добавляется текст письма.

**Строка 182**: Цикл по всем вложениям.

**Строка 184**: Извлекается расширение файла (например, `.pdf`, `.docx`).

**Строка 185**: Определяется MIME тип по расширению через `mime.TypeByExtension` (например, `.pdf` → `application/pdf`).

**Строка 186-188**: Если MIME тип не определен, используется `application/octet-stream` (универсальный бинарный тип).

**Строка 190**: Начало новой части вложения (boundary с `--`).

**Строка 191**: Content-Type для вложения (определенный ранее MIME тип).

**Строка 192**: `Content-Disposition: attachment` указывает, что это вложение, и задает имя файла.

**Строка 193**: `Content-Transfer-Encoding: base64` - вложение кодируется в Base64 (стандарт для email).

**Строка 197**: Кодирование бинарных данных вложения в Base64 строку.

**Строка 199-205**: Разбивка Base64 строки на строки по 76 символов (требование RFC 2045 для email). Каждая строка заканчивается `\r\n`.

**Строка 209**: Закрывающий boundary с `--` (указывает конец всех частей).

**Строка 210**: Возврат полного сообщения (заголовки + тело с вложениями).

**Строка 214-217**: Если вложений нет, формируется простое сообщение без multipart.

---

## Этап 5: Отправка через SMTP

### Файл: `email/smtp.go`, функция `sendWithTLS`

```221:297:email/smtp.go
// sendWithTLS отправляет email с поддержкой TLS
func (c *SMTPClient) sendWithTLS(ctx context.Context, addr string, auth smtp.Auth, tlsConfig *tls.Config, msg *EmailMessage, recipientEmails []string, body string) error {
	// Создаем канал для результата
	done := make(chan error, 1)

	go func() {
		// Подключаемся к SMTP серверу
		client, err := smtp.Dial(addr)
		if err != nil {
			done <- fmt.Errorf("ошибка подключения к SMTP: %w", err)
			return
		}
		defer client.Close()

		// Проверяем поддержку STARTTLS
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				done <- fmt.Errorf("ошибка STARTTLS: %w", err)
				return
			}
		} else if c.cfg.EnableSSL {
			// Если требуется SSL, но STARTTLS не поддерживается, используем прямое TLS соединение
			// Это требует использования tls.Dial вместо smtp.Dial
			done <- fmt.Errorf("сервер не поддерживает STARTTLS, но требуется SSL")
			return
		}

		// Аутентификация
		if auth != nil {
			if err := client.Auth(auth); err != nil {
				done <- fmt.Errorf("ошибка аутентификации: %w", err)
				return
			}
		}

		// Устанавливаем отправителя
		if err := client.Mail(c.cfg.User); err != nil {
			done <- fmt.Errorf("ошибка установки отправителя: %w", err)
			return
		}

		// Устанавливаем получателей (To и BCC)
		for _, recipientEmail := range recipientEmails {
			if err := client.Rcpt(recipientEmail); err != nil {
				done <- fmt.Errorf("ошибка установки получателя %s: %w", recipientEmail, err)
				return
			}
		}

		// Отправляем данные
		writer, err := client.Data()
		if err != nil {
			done <- fmt.Errorf("ошибка начала передачи данных: %w", err)
			return
		}

		// Записываем тело сообщения
		if _, err := writer.Write([]byte(body)); err != nil {
			writer.Close()
			done <- fmt.Errorf("ошибка записи данных: %w", err)
			return
		}

		// Закрываем writer
		if err := writer.Close(); err != nil {
			done <- fmt.Errorf("ошибка закрытия writer: %w", err)
			return
		}

		// Отправляем QUIT
		if err := client.Quit(); err != nil {
			done <- fmt.Errorf("ошибка QUIT: %w", err)
			return
		}

		done <- nil
	}()

	// Ждем завершения или отмены контекста
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}
```

**Строка 224**: Создается канал для асинхронной отправки с поддержкой отмены через контекст.

**Строка 226**: Запускается горутина для выполнения SMTP операций.

**Строка 228**: Подключение к SMTP серверу через `smtp.Dial(addr)` (например, `smtp.gmail.com:587`).

**Строка 236-240**: Проверка поддержки STARTTLS и установка TLS соединения для шифрования.

**Строка 249-253**: Аутентификация на SMTP сервере (если требуется).

**Строка 257**: Команда `MAIL FROM` - установка адреса отправителя.

**Строка 263-268**: Команды `RCPT TO` - установка адресов получателей (для каждого адреса отдельная команда).

**Строка 271**: Команда `DATA` - начало передачи данных сообщения.

**Строка 278**: Запись полного тела сообщения (включая заголовки и вложения в Base64) в SMTP соединение.

**Строка 285**: Закрытие writer завершает передачу данных (отправляет точку и перевод строки).

**Строка 291**: Команда `QUIT` - завершение SMTP сессии.

**Строка 300-305**: Ожидание завершения отправки или отмены через контекст.

---

## Формат MIME сообщения с вложениями

Пример сформированного сообщения:

```
From: Отправитель <sender@example.com>
To: recipient@example.com
Subject: Тема письма
Message-ID: <askemailsender123@example.com>
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="boundary_123_1234567890"

--boundary_123_1234567890
Content-Type: text/html; charset=UTF-8
Content-Transfer-Encoding: 8bit

Текст письма здесь

--boundary_123_1234567890
Content-Type: application/pdf
Content-Disposition: attachment; filename="document.pdf"
Content-Transfer-Encoding: base64

JVBERi0xLjQKJeLjz9MKMyAwIG9iago8PC9MZW5ndGgIDQgMCBSL0ZpbHRlci9GbGF0ZURlY29k
ZT4+CnN0cmVhbQp4nDPQM1Qo5ypUMFAwAHM9IM1QoVChKDU5Myc5VaE4Mz0vJ1WhJDU5Myc5Va
...
(базовые64 данные разбиты по 76 символов)

--boundary_123_1234567890--
```

---

## Итоговая схема процесса

```
XML из очереди Oracle
    ↓
Парсинг вложений (ParseAttachments)
    ↓
Для каждого вложения:
    ├─ Тип 1: Crystal Reports (не реализовано)
    ├─ Тип 2: CLOB из БД → GetEmailReportClob → Base64 декодирование
    └─ Тип 3: Файл с диска → os.Open → io.ReadAll
    ↓
Формирование EmailMessage с AttachmentData[]
    ↓
buildEmailMessage:
    ├─ Заголовки (From, To, Subject, MIME-Version)
    ├─ multipart/mixed с boundary
    ├─ Часть 1: Текст письма
    └─ Часть 2+: Вложения (Base64, MIME тип по расширению)
    ↓
sendWithTLS:
    ├─ Подключение к SMTP
    ├─ STARTTLS (шифрование)
    ├─ Аутентификация
    ├─ MAIL FROM
    ├─ RCPT TO (для каждого получателя)
    ├─ DATA (отправка тела сообщения)
    └─ QUIT
    ↓
Email отправлен с вложениями
```

---

## Важные детали реализации

1. **MIME типы**: Определяются автоматически по расширению файла через `mime.TypeByExtension()`. Если тип не определен, используется `application/octet-stream`.

2. **Base64 кодирование**: Все вложения кодируются в Base64 и разбиваются на строки по 76 символов (RFC 2045).

3. **Обработка ошибок**: Если одно вложение не удалось обработать, остальные все равно обрабатываются и отправляются.

4. **Безопасность**: Для файлов проверяется, что путь абсолютный (предотвращает доступ к файлам вне разрешенных директорий).

5. **Контекст**: Все операции поддерживают отмену через контекст (graceful shutdown).

6. **Multipart/mixed**: Используется стандартный формат MIME для сообщений с вложениями.

