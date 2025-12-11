package settings

import (
	"fmt"
	"time"

	"gopkg.in/ini.v1"
)

// Config представляет конфигурацию приложения
type Config struct {
	*ini.File
	Oracle       OracleConfig
	SMTP         []SMTPConfig
	Mode         ModeConfig
	Schedule     ScheduleConfig
	Log          LogConfig
	Share        ShareConfig
	scheduleStop chan struct{} // Канал для остановки горутины обновления расписания
}

// OracleConfig представляет конфигурацию Oracle
type OracleConfig struct {
	Instance                  string
	User                      string
	Password                  string
	DSN                       string
	DBConnectRetryAttempts    int
	DBConnectRetryIntervalSec int
}

// SMTPConfig представляет конфигурацию SMTP сервера
type SMTPConfig struct {
	Host                         string
	Port                         int
	User                         string
	Password                     string
	DisplayName                  string
	EnableSSL                    bool
	MinSendIntervalMsec          int
	SMTPMinSendEmailIntervalMsec int
	IMAPHost                     string // IMAP сервер для проверки bounce-сообщений
	IMAPPort                     int    // IMAP порт (обычно 993 для SSL)
}

// ModeConfig представляет режимы работы
type ModeConfig struct {
	Debug                       bool
	SendHiddenCopyToSelf        bool
	IsBodyHTML                  bool
	MaxErrorCountForAutoRestart int
	MaxAttachmentSizeMB         int
	CrystalReportsTimeoutSec    int
}

// ScheduleConfig представляет расписание отправки
type ScheduleConfig struct {
	TimeStart time.Time
	TimeEnd   time.Time
}

// LogConfig представляет конфигурацию логирования
type LogConfig struct {
	LogLevel        int
	MaxArchiveFiles int
}

// ShareConfig представляет конфигурацию для доступа к CIFS/SMB шарам
type ShareConfig struct {
	Username        string
	Password        string
	Domain          string
	Port            string
	PathReplaceFrom string // Строка для замены в пути (например: "192.168.87.31:shares$:esig_docs")
	PathReplaceTo   string // Замена на (например: "\\\\sto-s\\Applic\\Xchange\\EDS")
}

// LoadConfig загружает конфигурацию из INI файла
func LoadConfig(path string) (*Config, error) {
	cfg, err := ini.Load(path)
	if err != nil {
		return nil, fmt.Errorf("не удалось загрузить конфигурацию: %w", err)
	}

	config := &Config{
		File: cfg,
	}

	// Загружаем конфигурацию Oracle
	if err := config.loadOracleConfig(); err != nil {
		return nil, fmt.Errorf("ошибка загрузки конфигурации Oracle: %w", err)
	}

	// Загружаем конфигурацию SMTP
	if err := config.loadSMTPConfig(); err != nil {
		return nil, fmt.Errorf("ошибка загрузки конфигурации SMTP: %w", err)
	}

	// Загружаем режимы работы
	if err := config.loadModeConfig(); err != nil {
		return nil, fmt.Errorf("ошибка загрузки режимов работы: %w", err)
	}

	// Загружаем расписание
	if err := config.loadScheduleConfig(); err != nil {
		return nil, fmt.Errorf("ошибка загрузки расписания: %w", err)
	}

	// Загружаем конфигурацию логирования
	if err := config.loadLogConfig(); err != nil {
		return nil, fmt.Errorf("ошибка загрузки конфигурации логирования: %w", err)
	}

	// Загружаем конфигурацию CIFS/SMB шары
	if err := config.loadShareConfig(); err != nil {
		return nil, fmt.Errorf("ошибка загрузки конфигурации Share: %w", err)
	}

	return config, nil
}

func (c *Config) loadOracleConfig() error {
	// Проверяем секцию [main] для user/password/dsn (как в smsSender)
	if c.File.HasSection("main") {
		mainSec := c.File.Section("main")
		c.Oracle.User = mainSec.Key("username").String()
		c.Oracle.Password = mainSec.Key("password").String()
		if c.Oracle.Password == "" {
			c.Oracle.Password = mainSec.Key("passwword").String() // Совместимость с опечаткой
		}
		c.Oracle.DSN = mainSec.Key("dsn").String()

		// Параметры повторного подключения при старте
		c.Oracle.DBConnectRetryAttempts = mainSec.Key("DBConnectRetryAttempts").MustInt(10)
		c.Oracle.DBConnectRetryIntervalSec = mainSec.Key("DBConnectRetryIntervalSec").MustInt(5)
	}

	// Также проверяем секцию [ORACLE] для Instance (совместимость с C# версией)
	if c.File.HasSection("ORACLE") {
		sec := c.File.Section("ORACLE")
		c.Oracle.Instance = sec.Key("Instance").String()
	}

	// Если DSN не указан, используем Instance
	if c.Oracle.DSN == "" && c.Oracle.Instance != "" {
		c.Oracle.DSN = c.Oracle.Instance
	}

	if c.Oracle.DSN == "" {
		return fmt.Errorf("не указан DSN или Instance для подключения к Oracle")
	}

	return nil
}

func (c *Config) loadSMTPConfig() error {
	c.SMTP = make([]SMTPConfig, 0, 5)

	// Загружаем основную секцию [SMTP]
	smtpConfigs := []string{"SMTP", "SMTP1", "SMTP2", "SMTP3", "SMTP4"}

	for _, sectionName := range smtpConfigs {
		if !c.File.HasSection(sectionName) {
			continue
		}

		sec := c.File.Section(sectionName)
		host := sec.Key("Host").String()
		if host == "" {
			continue // Пропускаем пустые секции
		}

		port, err := sec.Key("Port").Int()
		if err != nil {
			port = 25 // По умолчанию
		}

		user := sec.Key("User").String()
		password := sec.Key("Password").String()
		displayName := sec.Key("DisplayName").String()
		enableSSL := sec.Key("EnableSSL").MustBool(true)

		minSendIntervalMsec := sec.Key("MinSendIntervalMsec").MustInt(1000)
		minSendEmailIntervalMsec := sec.Key("SMTPMinSendEmailIntervalMsec").MustInt(1000)

		imapHost := sec.Key("IMAPHost").String()
		imapPort := sec.Key("IMAPPort").MustInt(993) // По умолчанию 993 для SSL

		c.SMTP = append(c.SMTP, SMTPConfig{
			Host:                         host,
			Port:                         port,
			User:                         user,
			Password:                     password,
			DisplayName:                  displayName,
			EnableSSL:                    enableSSL,
			MinSendIntervalMsec:          minSendIntervalMsec,
			SMTPMinSendEmailIntervalMsec: minSendEmailIntervalMsec,
			IMAPHost:                     imapHost,
			IMAPPort:                     imapPort,
		})
	}

	if len(c.SMTP) == 0 {
		return fmt.Errorf("не найдено ни одной конфигурации SMTP")
	}

	return nil
}

func (c *Config) loadModeConfig() error {
	sec := c.File.Section("Mode")
	c.Mode.Debug = sec.Key("Debug").MustBool(false)
	c.Mode.SendHiddenCopyToSelf = sec.Key("SendHiddenCopyToSelf").MustBool(false)
	c.Mode.IsBodyHTML = sec.Key("IsBodyHTML").MustBool(false)
	c.Mode.MaxErrorCountForAutoRestart = sec.Key("MaxErrorCountForAutoRestart").MustInt(50)

	// Новые параметры надежности
	c.Mode.MaxAttachmentSizeMB = sec.Key("MaxAttachmentSizeMB").MustInt(100)
	c.Mode.CrystalReportsTimeoutSec = sec.Key("CrystalReportsTimeoutSec").MustInt(60)

	return nil
}

func (c *Config) loadScheduleConfig() error {
	sec := c.File.Section("Schedule")
	timeStartStr := sec.Key("TimeStart").String()
	timeEndStr := sec.Key("TimeEnd").String()

	// Парсим время в формате HH:MM
	now := time.Now()

	if timeStartStr != "" {
		timeStart, err := time.Parse("15:04", timeStartStr)
		if err != nil {
			return fmt.Errorf("неверный формат TimeStart: %w", err)
		}
		// Объединяем с текущей датой
		c.Schedule.TimeStart = time.Date(now.Year(), now.Month(), now.Day(),
			timeStart.Hour(), timeStart.Minute(), 0, 0, now.Location())
	} else {
		c.Schedule.TimeStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}

	if timeEndStr != "" {
		timeEnd, err := time.Parse("15:04", timeEndStr)
		if err != nil {
			return fmt.Errorf("неверный формат TimeEnd: %w", err)
		}
		// Объединяем с текущей датой
		c.Schedule.TimeEnd = time.Date(now.Year(), now.Month(), now.Day(),
			timeEnd.Hour(), timeEnd.Minute(), 0, 0, now.Location())
	} else {
		c.Schedule.TimeEnd = time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
	}

	// Обновляем время каждый день
	c.scheduleStop = make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-c.scheduleStop:
				return
			case <-ticker.C:
				now := time.Now()
				if timeStartStr != "" {
					timeStart, _ := time.Parse("15:04", timeStartStr)
					c.Schedule.TimeStart = time.Date(now.Year(), now.Month(), now.Day(),
						timeStart.Hour(), timeStart.Minute(), 0, 0, now.Location())
				}
				if timeEndStr != "" {
					timeEnd, _ := time.Parse("15:04", timeEndStr)
					c.Schedule.TimeEnd = time.Date(now.Year(), now.Month(), now.Day(),
						timeEnd.Hour(), timeEnd.Minute(), 0, 0, now.Location())
				}
			}
		}
	}()

	return nil
}

func (c *Config) loadLogConfig() error {
	sec := c.File.Section("Log")
	c.Log.LogLevel = sec.Key("LogLevel").MustInt(4) // По умолчанию Info
	c.Log.MaxArchiveFiles = sec.Key("MaxArchiveFiles").MustInt(10)

	return nil
}

func (c *Config) loadShareConfig() error {
	if !c.File.HasSection("share") {
		// Секция не обязательна, используем значения по умолчанию
		c.Share.Port = "445"
		return nil
	}

	sec := c.File.Section("share")
	c.Share.Username = sec.Key("CIFSUSERNAME").String()
	c.Share.Password = sec.Key("CIFSPASSWORD").String()
	c.Share.Domain = sec.Key("CIFSDOMEN").String()
	c.Share.Port = sec.Key("CIFSPORT").String()
	c.Share.PathReplaceFrom = sec.Key("PathReplaceFrom").String()
	c.Share.PathReplaceTo = sec.Key("PathReplaceTo").String()

	// Значение по умолчанию для порта
	if c.Share.Port == "" {
		c.Share.Port = "445"
	}

	return nil
}

// Stop останавливает фоновые горутины Config (для graceful shutdown)
func (c *Config) Stop() {
	if c.scheduleStop != nil {
		close(c.scheduleStop)
	}
}
