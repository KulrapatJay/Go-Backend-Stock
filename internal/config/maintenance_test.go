package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

const maintenanceURL = "postgres://app_maintenance:Sup3rS3cretPw@postgres:5432/stock?sslmode=disable"

func maintenanceEnv() map[string]string {
	return map[string]string{"APP_ENV": "development", "MAINTENANCE_DATABASE_URL": maintenanceURL}
}

func TestLoadMaintenanceDefaults(t *testing.T) {
	cfg, err := LoadMaintenance(envFrom(maintenanceEnv()))
	if err != nil {
		t.Fatalf("LoadMaintenance() error = %v", err)
	}
	if cfg.Env != EnvDevelopment || cfg.DatabaseURL != maintenanceURL || cfg.LogLevel != slog.LevelInfo ||
		cfg.AuditRetention != 90*24*time.Hour || cfg.Timeout != 10*time.Minute {
		t.Errorf("cfg = %+v, want development, the URL, info, 90 days and 10 minutes", cfg)
	}
}

func TestLoadMaintenanceReadsRetentionInDays(t *testing.T) {
	env := maintenanceEnv()
	env["AUDIT_RETENTION_DAYS"] = "30"
	env["MAINTENANCE_TIMEOUT"] = "2m"
	cfg, err := LoadMaintenance(envFrom(env))
	if err != nil {
		t.Fatalf("LoadMaintenance() error = %v", err)
	}
	if cfg.AuditRetention != 30*24*time.Hour || cfg.Timeout != 2*time.Minute {
		t.Errorf("AuditRetention = %s, Timeout = %s", cfg.AuditRetention, cfg.Timeout)
	}
}

func TestLoadMaintenanceRanges(t *testing.T) {
	tests := []struct{ name, key, value string }{
		{"days too few", "AUDIT_RETENTION_DAYS", "29"},
		{"days too many", "AUDIT_RETENTION_DAYS", "366"},
		{"days not a number", "AUDIT_RETENTION_DAYS", "abc"},
		{"days as a duration", "AUDIT_RETENTION_DAYS", "90d"},
		{"timeout too short", "MAINTENANCE_TIMEOUT", "30s"},
		{"timeout too long", "MAINTENANCE_TIMEOUT", "2h"},
		{"log level", "LOG_LEVEL", "verbose"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := maintenanceEnv()
			env[tt.key] = tt.value
			_, err := LoadMaintenance(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("LoadMaintenance() error = %v, want it to name %s", err, tt.key)
			}
		})
	}
}

func TestLoadMaintenanceRequiresTheDatabaseURL(t *testing.T) {
	env := maintenanceEnv()
	delete(env, "MAINTENANCE_DATABASE_URL")
	if _, err := LoadMaintenance(envFrom(env)); err == nil || !strings.Contains(err.Error(), "MAINTENANCE_DATABASE_URL") {
		t.Fatalf("LoadMaintenance() error = %v, want it to name MAINTENANCE_DATABASE_URL", err)
	}

	env = maintenanceEnv()
	env["MAINTENANCE_DATABASE_URL"] = "mysql://app_maintenance:Sup3rS3cretPw@db/stock"
	_, err := LoadMaintenance(envFrom(env))
	if err == nil || !strings.Contains(err.Error(), "MAINTENANCE_DATABASE_URL") || strings.Contains(err.Error(), "Sup3rS3cretPw") {
		t.Fatalf("LoadMaintenance() error = %v, want it to name the variable and not leak the password", err)
	}
}

func TestLoadMaintenanceDemandsVerifiedTLSInProduction(t *testing.T) {
	env := maintenanceEnv()
	env["APP_ENV"] = "production"
	_, err := LoadMaintenance(envFrom(env))
	if err == nil || !strings.Contains(err.Error(), "MAINTENANCE_DATABASE_URL") || !strings.Contains(err.Error(), "verify-full") ||
		strings.Contains(err.Error(), "Sup3rS3cretPw") {
		t.Fatalf("LoadMaintenance() error = %v, want verify-full named without the password", err)
	}

	env["MAINTENANCE_DATABASE_URL"] = "postgres://app_maintenance:Sup3rS3cretPw@db.example.com:5432/stock?sslmode=verify-full"
	if _, err := LoadMaintenance(envFrom(env)); err != nil {
		t.Fatalf("LoadMaintenance() error = %v", err)
	}
}

func TestLoadMaintenanceRejectsAnUnknownEnvironment(t *testing.T) {
	env := maintenanceEnv()
	env["APP_ENV"] = "staging"
	if _, err := LoadMaintenance(envFrom(env)); err == nil || !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("LoadMaintenance() error = %v, want it to name APP_ENV", err)
	}
}
