# Walkthrough: Reliability Improvements

## Changes Implemented

### 1. Configuration (`settings.ini`)
Added new parameters to `settings.ini` to control reliability features:

```ini
[main]
# ... existing params ...
# Retry connection to DB on startup
DBConnectRetryAttempts = 10
DBConnectRetryIntervalSec = 5

[Mode]
# ... existing params ...
# Limit attachment size (MB)
MaxAttachmentSizeMB = 100
# Timeout for Crystal Reports generation (seconds)
CrystalReportsTimeoutSec = 60
```

### 2. Attachment Size Limit
- **File Attachments**: Before reading any file (local or CIFS), the application now checks its size.
- **Limit**: If the file size exceeds `MaxAttachmentSizeMB` (default 100MB), the attachment is skipped, and an error is logged. This prevents Out-Of-Memory (OOM) crashes.

### 3. Crystal Reports Timeout
- **Configurable Timeout**: The SOAP client for Crystal Reports now uses `CrystalReportsTimeoutSec` (default 60s).
- **Benefit**: Prevents the application from hanging indefinitely if the reporting service is slow or unresponsive.

### 4. Database Startup Retry
- **Retry Logic**: On startup, if the database is unavailable, the application will retry connecting `DBConnectRetryAttempts` times with `DBConnectRetryIntervalSec` delay.
- **Benefit**: Allows the service to recover automatically after a server restart where the DB might come up slower than the application (e.g., in Docker Compose).

## Verification Steps

### 1. Verify Configuration
- Check `settings.ini` and ensure the new parameters are present (or add them).
- Run the application and check logs for successful startup.

### 2. Test Attachment Limit
- Set `MaxAttachmentSizeMB = 1` in `settings.ini`.
- Try to send an email with an attachment larger than 1MB.
- **Expected Result**: Error in logs "размер файла ... превышает лимит 1 МБ", email not sent (or sent without attachment depending on logic).

### 3. Test Startup Retry
- Stop the Oracle database (or change `dsn` in `settings.ini` to an invalid value).
- Start the application.
- **Expected Result**: Logs showing "Ошибка подключения к БД, повторная попытка...".
- Start the Oracle database (or fix `dsn`).
- **Expected Result**: Application connects and starts successfully.
