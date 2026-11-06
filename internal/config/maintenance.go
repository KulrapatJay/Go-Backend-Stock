package config

import (
	"errors"
	"log/slog"
	"strings"
	"time"
)

// Audit events are kept 90 days unless AUDIT_RETENTION_DAYS says otherwise, and never fewer than
// 30 so a typo cannot erase the history the security review relies on.
const (
	defaultAuditRetentionDays = 90
	minAuditRetentionDays     = 30
	maxAuditRetentionDays     = 365
)

// MaintenanceConfig is the validated configuration of the retention cleanup job. It reads nothing
// of the API's secrets: the job only needs the maintenance database role.
type MaintenanceConfig struct {
	Env            Environment
	DatabaseURL    string
	LogLevel       slog.Level
	AuditRetention time.Duration
	Timeout        time.Duration
}

// LoadMaintenance reads the job's settings through getenv and returns all validation errors at
// once. Error messages never include configured values because they may contain secrets.
func LoadMaintenance(getenv func(string) string) (MaintenanceConfig, error) {
	var cfg MaintenanceConfig
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	switch env := Environment(strings.TrimSpace(getenv("APP_ENV"))); env {
	case EnvDevelopment, EnvTest, EnvProduction:
		cfg.Env = env
	default:
		add(errors.New("APP_ENV must be one of development, test, production"))
	}
	prod := cfg.Env == EnvProduction

	var err error
	cfg.DatabaseURL, err = parseDatabaseURL("MAINTENANCE_DATABASE_URL", getenv("MAINTENANCE_DATABASE_URL"), prod)
	add(err)
	cfg.LogLevel, err = parseLogLevel(getenv("LOG_LEVEL"), prod)
	add(err)
	days, err := parseIntInRange("AUDIT_RETENTION_DAYS", getenv("AUDIT_RETENTION_DAYS"),
		defaultAuditRetentionDays, minAuditRetentionDays, maxAuditRetentionDays)
	add(err)
	cfg.AuditRetention = time.Duration(days) * 24 * time.Hour
	cfg.Timeout, err = parseDurationInRange("MAINTENANCE_TIMEOUT", getenv("MAINTENANCE_TIMEOUT"), 10*time.Minute, time.Minute, time.Hour)
	add(err)

	if len(errs) > 0 {
		return MaintenanceConfig{}, errors.Join(errs...)
	}
	return cfg, nil
}
