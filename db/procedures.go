package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"time"

	"go.uber.org/zap"

	"email-service/logger"
)

// SaveEmailResponseParams представляет параметры для вызова процедуры save_email_response
type SaveEmailResponseParams struct {
	TaskID       int64     // P_EMAIL_TASK_ID
	StatusID     int       // P_STATUS_ID
	ResponseDate time.Time // P_DATE_RESPONSE
	ErrorText    string    // P_ERROR_TEXT
}

// SaveEmailResponse вызывает процедуру pcsystem.pkg_email.save_email_response()
func (d *DBConnection) SaveEmailResponse(ctx context.Context, params SaveEmailResponseParams) (bool, error) {
	if !d.CheckConnection() {
		return false, fmt.Errorf("соединение с БД недоступно")
	}

	var queryCtx context.Context
	var queryCancel context.CancelFunc
	if ctx.Err() == context.Canceled {
		queryCtx, queryCancel = context.WithTimeout(context.Background(), ExecTimeout)
	} else {
		queryCtx, queryCancel = context.WithTimeout(ctx, ExecTimeout)
	}
	defer queryCancel()

	var errorText interface{}
	if params.ErrorText == "" {
		errorText = nil
	} else {
		errorText = params.ErrorText
	}

	var errCode sql.NullInt64
	var errDesc sql.NullString

	err := d.WithDBTx(queryCtx, func(tx *sql.Tx) error {
		if err := d.ensureEmailResponsePackageExistsTx(tx, queryCtx); err != nil {
			return fmt.Errorf("ошибка создания пакета: %w", err)
		}

		plsql := `
			DECLARE
				v_err_code NUMBER;
				v_err_desc VARCHAR2(4000);
			BEGIN
				temp_email_response_pkg.g_err_code := 0;
				temp_email_response_pkg.g_err_desc := NULL;
				
				pcsystem.pkg_email.save_email_response(
					P_EMAIL_TASK_ID => :1,
					P_STATUS_ID => :2,
					P_DATE_RESPONSE => :3,
					P_ERROR_TEXT => :4,
					p_err_code => v_err_code,
					p_err_desc => v_err_desc
				);
				
				temp_email_response_pkg.g_err_code := v_err_code;
				temp_email_response_pkg.g_err_desc := v_err_desc;
			END;`

		_, err := tx.ExecContext(queryCtx, plsql,
			params.TaskID,
			params.StatusID,
			params.ResponseDate,
			errorText,
		)

		if err != nil {
			if queryCtx.Err() != nil {
				if logger.Log != nil {
					if ctx.Err() == context.Canceled {
						logger.Log.Warn("Сохранение результата email отменено из-за graceful shutdown",
							zap.Int64("taskID", params.TaskID))
					}
				}
				return fmt.Errorf("операция отменена: %w", queryCtx.Err())
			}

			if logger.Log != nil {
				logger.Log.Error("Ошибка вызова pcsystem.pkg_email.save_email_response",
					zap.Int64("taskID", params.TaskID),
					zap.Int("statusID", params.StatusID),
					zap.Error(err))
			}
			return fmt.Errorf("ошибка вызова save_email_response: %w", err)
		}

		checkResultSQL := `SELECT temp_email_response_pkg.get_err_code(), temp_email_response_pkg.get_err_desc() FROM DUAL`
		err = tx.QueryRowContext(queryCtx, checkResultSQL).Scan(&errCode, &errDesc)
		if err != nil {
			if logger.Log != nil {
				logger.Log.Error("Ошибка чтения результата процедуры",
					zap.Int64("taskID", params.TaskID),
					zap.Error(err))
			}
			return fmt.Errorf("ошибка чтения результата процедуры: %w", err)
		}

		return nil
	})

	if err != nil {
		if queryCtx.Err() != nil {
			return false, err
		}
		return false, err
	}

	if errCode.Valid && errCode.Int64 != 0 {
		errMsg := ""
		if errDesc.Valid {
			errMsg = errDesc.String
		}
		if logger.Log != nil {
			logger.Log.Error("Ошибка выполнения pcsystem.pkg_email.save_email_response",
				zap.Int64("errCode", errCode.Int64),
				zap.String("errDesc", errMsg),
				zap.Int64("taskID", params.TaskID),
				zap.Int("statusID", params.StatusID))
		}
		return false, fmt.Errorf("ошибка БД: %d - %s", errCode.Int64, errMsg)
	}

	if logger.Log != nil {
		logger.Log.Info("Вызов pcsystem.pkg_email.save_email_response() успешно",
			zap.Int64("taskID", params.TaskID),
			zap.Int("statusID", params.StatusID),
			zap.Time("responseDate", params.ResponseDate),
			zap.String("errorText", params.ErrorText))
	}

	return true, nil
}

// ensureEmailResponsePackageExistsTx создает временный пакет Oracle для работы с OUT-параметрами
func (d *DBConnection) ensureEmailResponsePackageExistsTx(tx *sql.Tx, ctx context.Context) error {
	createPackageSQL := `
		CREATE OR REPLACE PACKAGE temp_email_response_pkg AS
			g_err_code NUMBER := 0;
			g_err_desc VARCHAR2(4000);
			
			FUNCTION get_err_code RETURN NUMBER;
			FUNCTION get_err_desc RETURN VARCHAR2;
		END temp_email_response_pkg;
	`
	_, err := tx.ExecContext(ctx, createPackageSQL)
	if err != nil {
		return fmt.Errorf("не удалось создать пакет temp_email_response_pkg: %w", err)
	}

	createPackageBodySQL := `
		CREATE OR REPLACE PACKAGE BODY temp_email_response_pkg AS
			FUNCTION get_err_code RETURN NUMBER IS
			BEGIN
				RETURN g_err_code;
			END;
			
			FUNCTION get_err_desc RETURN VARCHAR2 IS
			BEGIN
				RETURN g_err_desc;
			END;
		END temp_email_response_pkg;
	`
	_, err = tx.ExecContext(ctx, createPackageBodySQL)
	if err != nil {
		return fmt.Errorf("не удалось создать тело пакета temp_email_response_pkg: %w", err)
	}

	return nil
}

// GetTestEmail получает тестовый email через pcsystem.PKG_EMAIL.GET_TEST_EMAIL()
func (d *DBConnection) GetTestEmail() (string, error) {
	if !d.CheckConnection() {
		return "", fmt.Errorf("соединение с БД недоступно")
	}

	queryCtx, queryCancel := context.WithTimeout(context.Background(), QueryTimeout)
	defer queryCancel()

	var testEmail sql.NullString

	err := d.WithDBTx(queryCtx, func(tx *sql.Tx) error {
		if err := d.ensureTestEmailPackageExistsTx(tx, queryCtx); err != nil {
			return fmt.Errorf("ошибка создания пакета: %w", err)
		}

		plsql := `
			BEGIN
				temp_test_email_pkg.g_email := pcsystem.PKG_EMAIL.GET_TEST_EMAIL();
			END;
		`

		_, err := tx.ExecContext(queryCtx, plsql)
		if err != nil {
			if logger.Log != nil {
				logger.Log.Error("Ошибка выполнения PL/SQL для pcsystem.PKG_EMAIL.GET_TEST_EMAIL()", zap.Error(err))
			}
			return fmt.Errorf("ошибка выполнения PL/SQL: %w", err)
		}

		query := "SELECT temp_test_email_pkg.get_email() FROM DUAL"
		err = tx.QueryRowContext(queryCtx, query).Scan(&testEmail)
		if err != nil {
			if logger.Log != nil {
				logger.Log.Error("Ошибка выполнения SELECT для temp_test_email_pkg.get_email()", zap.Error(err))
			}
			return fmt.Errorf("ошибка получения тестового email: %w", err)
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	if !testEmail.Valid || testEmail.String == "" {
		errText := "Режим Debug: тестовый email отсутствует"
		if logger.Log != nil {
			logger.Log.Warn(errText)
		}
		return "", fmt.Errorf("ошибка получения тестового email: email пуст")
	}

	if logger.Log != nil {
		logger.Log.Debug("pcsystem.PKG_EMAIL.GET_TEST_EMAIL() result",
			zap.String("email", testEmail.String))
	}
	return testEmail.String, nil
}

// ensureTestEmailPackageExistsTx создает временный пакет Oracle для работы с функцией GET_TEST_EMAIL
func (d *DBConnection) ensureTestEmailPackageExistsTx(tx *sql.Tx, ctx context.Context) error {
	createPackageSQL := `
		CREATE OR REPLACE PACKAGE temp_test_email_pkg AS
			g_email VARCHAR2(500);
			
			FUNCTION get_email RETURN VARCHAR2;
		END temp_test_email_pkg;
	`
	_, err := tx.ExecContext(ctx, createPackageSQL)
	if err != nil {
		return fmt.Errorf("не удалось создать пакет temp_test_email_pkg: %w", err)
	}

	createPackageBodySQL := `
		CREATE OR REPLACE PACKAGE BODY temp_test_email_pkg AS
			FUNCTION get_email RETURN VARCHAR2 IS
			BEGIN
				RETURN g_email;
			END;
		END temp_test_email_pkg;
	`
	_, err = tx.ExecContext(ctx, createPackageBodySQL)
	if err != nil {
		return fmt.Errorf("не удалось создать тело пакета temp_test_email_pkg: %w", err)
	}

	return nil
}

// GetWebServiceUrl получает адрес Crystal Reports через pcsystem.PKG_EMAIL.GET_SOAP_ADDRESS()
func (d *DBConnection) GetWebServiceUrl() (string, error) {
	if !d.CheckConnection() {
		return "", fmt.Errorf("соединение с БД недоступно")
	}

	queryCtx, queryCancel := context.WithTimeout(context.Background(), QueryTimeout)
	defer queryCancel()

	var url sql.NullString

	err := d.WithDBTx(queryCtx, func(tx *sql.Tx) error {
		if err := d.ensureWebServiceUrlPackageExistsTx(tx, queryCtx); err != nil {
			return fmt.Errorf("ошибка создания пакета: %w", err)
		}

		plsql := `
			BEGIN
				temp_webservice_url_pkg.g_url := pcsystem.PKG_EMAIL.GET_SOAP_ADDRESS();
			END;
		`

		_, err := tx.ExecContext(queryCtx, plsql)
		if err != nil {
			if logger.Log != nil {
				logger.Log.Error("Ошибка выполнения PL/SQL для pcsystem.PKG_EMAIL.GET_SOAP_ADDRESS()", zap.Error(err))
			}
			return fmt.Errorf("ошибка выполнения PL/SQL: %w", err)
		}

		query := "SELECT temp_webservice_url_pkg.get_url() FROM DUAL"
		err = tx.QueryRowContext(queryCtx, query).Scan(&url)
		if err != nil {
			if logger.Log != nil {
				logger.Log.Error("Ошибка выполнения SELECT для temp_webservice_url_pkg.get_url()", zap.Error(err))
			}
			return fmt.Errorf("ошибка получения URL: %w", err)
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	if !url.Valid || url.String == "" {
		return "", fmt.Errorf("ошибка получения URL: URL пуст")
	}

	if logger.Log != nil {
		logger.Log.Debug("pcsystem.PKG_EMAIL.GET_SOAP_ADDRESS() result",
			zap.String("url", url.String))
	}
	return url.String, nil
}

// ensureWebServiceUrlPackageExistsTx создает временный пакет Oracle для работы с функцией GET_SOAP_ADDRESS
func (d *DBConnection) ensureWebServiceUrlPackageExistsTx(tx *sql.Tx, ctx context.Context) error {
	createPackageSQL := `
		CREATE OR REPLACE PACKAGE temp_webservice_url_pkg AS
			g_url VARCHAR2(500);
			
			FUNCTION get_url RETURN VARCHAR2;
		END temp_webservice_url_pkg;
	`
	_, err := tx.ExecContext(ctx, createPackageSQL)
	if err != nil {
		return fmt.Errorf("не удалось создать пакет temp_webservice_url_pkg: %w", err)
	}

	createPackageBodySQL := `
		CREATE OR REPLACE PACKAGE BODY temp_webservice_url_pkg AS
			FUNCTION get_url RETURN VARCHAR2 IS
			BEGIN
				RETURN g_url;
			END;
		END temp_webservice_url_pkg;
	`
	_, err = tx.ExecContext(ctx, createPackageBodySQL)
	if err != nil {
		return fmt.Errorf("не удалось создать тело пакета temp_webservice_url_pkg: %w", err)
	}

	return nil
}

// GetEmailReportClob получает CLOB вложения через pcsystem.pkg_email.get_email_report_clob()
func (d *DBConnection) GetEmailReportClob(taskID int64, clobID int64) ([]byte, error) {
	if !d.CheckConnection() {
		return nil, fmt.Errorf("соединение с БД недоступно")
	}

	queryCtx, queryCancel := context.WithTimeout(context.Background(), QueryTimeout)
	defer queryCancel()

	var clobData sql.NullString

	err := d.WithDBTx(queryCtx, func(tx *sql.Tx) error {
		if err := d.ensureEmailReportClobPackageExistsTx(tx, queryCtx); err != nil {
			return fmt.Errorf("ошибка создания пакета: %w", err)
		}

		plsql := `
			DECLARE
				v_clob CLOB;
			BEGIN
				v_clob := pcsystem.pkg_email.get_email_report_clob(p_email_attach_id => :1);
				temp_email_report_clob_pkg.g_clob := v_clob;
			END;
		`

		_, err := tx.ExecContext(queryCtx, plsql, clobID)
		if err != nil {
			if logger.Log != nil {
				logger.Log.Error("Ошибка выполнения PL/SQL для pcsystem.pkg_email.get_email_report_clob()",
					zap.Int64("taskID", taskID),
					zap.Int64("clobID", clobID),
					zap.Error(err))
			}
			return fmt.Errorf("ошибка выполнения PL/SQL: %w", err)
		}

		query := "SELECT temp_email_report_clob_pkg.get_clob() FROM DUAL"
		err = tx.QueryRowContext(queryCtx, query).Scan(&clobData)
		if err != nil {
			if logger.Log != nil {
				logger.Log.Error("Ошибка выполнения SELECT для temp_email_report_clob_pkg.get_clob()",
					zap.Int64("taskID", taskID),
					zap.Int64("clobID", clobID),
					zap.Error(err))
			}
			return fmt.Errorf("ошибка получения CLOB: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	if !clobData.Valid || clobData.String == "" {
		return nil, fmt.Errorf("CLOB пуст")
	}

	// Декодируем Base64
	decoded, err := base64.StdEncoding.DecodeString(clobData.String)
	if err != nil {
		if logger.Log != nil {
			logger.Log.Error("Ошибка декодирования Base64",
				zap.Int64("taskID", taskID),
				zap.Int64("clobID", clobID),
				zap.Error(err))
		}
		return nil, fmt.Errorf("ошибка декодирования Base64: %w", err)
	}

	if logger.Log != nil {
		logger.Log.Debug("pcsystem.pkg_email.get_email_report_clob() result",
			zap.Int64("taskID", taskID),
			zap.Int64("clobID", clobID),
			zap.Int("size", len(decoded)))
	}
	return decoded, nil
}

// ensureEmailReportClobPackageExistsTx создает временный пакет Oracle для работы с функцией get_email_report_clob
func (d *DBConnection) ensureEmailReportClobPackageExistsTx(tx *sql.Tx, ctx context.Context) error {
	createPackageSQL := `
		CREATE OR REPLACE PACKAGE temp_email_report_clob_pkg AS
			g_clob CLOB;
			
			FUNCTION get_clob RETURN CLOB;
		END temp_email_report_clob_pkg;
	`
	_, err := tx.ExecContext(ctx, createPackageSQL)
	if err != nil {
		return fmt.Errorf("не удалось создать пакет temp_email_report_clob_pkg: %w", err)
	}

	createPackageBodySQL := `
		CREATE OR REPLACE PACKAGE BODY temp_email_report_clob_pkg AS
			FUNCTION get_clob RETURN CLOB IS
			BEGIN
				RETURN g_clob;
			END;
		END temp_email_report_clob_pkg;
	`
	_, err = tx.ExecContext(ctx, createPackageBodySQL)
	if err != nil {
		return fmt.Errorf("не удалось создать тело пакета temp_email_report_clob_pkg: %w", err)
	}

	return nil
}
