# Milestone 1 — Backend Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** สร้างโครง Go API ที่รันได้จริงผ่าน Docker Compose พร้อม config validation, structured log, request ID, error envelope, `/healthz`, `/readyz`, Swagger UI แบบ offline, DB bootstrap + goose baseline และ CI ที่ตรวจทั้งหมดนี้

**Architecture:** `cmd/api/main.go` เป็น composition root เดียวที่สร้าง config → logger → pgx pool → Fiber server; HTTP adapter (`internal/adapter/http`) มี middleware chain `RequestID → AccessLog → Recover → SecurityHeaders` และ error handler ที่ AccessLog เรียกเองเพื่อ log final status; PostgreSQL ถูก bootstrap roles + schema `app` ก่อน goose แล้ว goose เก็บ version table ใน `app`; Swagger 2.0 generate จาก annotations แล้ว embed ลง binary พร้อม Swagger UI assets

**Tech Stack:** Go 1.27.1, Fiber v3.5.0, pgx v5.11.0, PostgreSQL 17.11, goose v3.28.0, swaggo/swag v1.16.6, go-swagger v0.34.1, Swagger UI v5.33.1, golangci-lint v2.14.0, Docker Compose, GitHub Actions

**Spec:** [docs/superpowers/specs/2026-10-05-phase-1-design.md](../specs/2026-10-05-phase-1-design.md) (หัวข้อ 3–5, 7 Roles และ Supabase, 13) และ [AGENTS.md](../../../AGENTS.md); งานนี้คือ milestone 1 ใน [roadmap](2026-10-05-phase-1-roadmap.md)

## Global Constraints

- Go `1.27.1`; Fiber `github.com/gofiber/fiber/v3 v3.5.0`; pgx `github.com/jackc/pgx/v5 v5.11.0`; PostgreSQL `17.11` ทั้ง local และ CI
- goose `v3.28.0`; swaggo `github.com/swaggo/swag v1.16.6` (output Swagger 2.0); go-swagger `v0.34.1`; Swagger UI `5.33.1`; golangci-lint `v2.14.0`
- ไม่ใช้ `latest` ใน Dockerfile, compose, CI หรือคำสั่งติดตั้งที่ commit; image ต้อง pin tag + digest
- Module path: `go-backend-stock` (เปลี่ยนได้ด้วย `go mod edit -module` + แทนที่ import ก่อนสร้าง GitHub repo)
- Core (`internal/core`) ยังไม่มีใน milestone นี้; ห้ามสร้าง empty interfaces หรือ handlers ล่วงหน้า
- ประกอบ dependency ที่ `cmd/*/main.go` เท่านั้น ไม่ใช้ global logger/state
- Request ID เป็น UUIDv7 ที่ server สร้างใหม่ทุกคำขอ ตอบใน `X-Request-ID`; ไม่ใช้ค่าจาก client
- Access log มี method, route template, final status, duration, request_id; ห้าม log raw URL/query/body/cookies/Authorization
- Error ทุกจุดใช้ envelope `{"error":{"code","message","request_id"}}`; ไม่ส่ง stack trace หรือรายละเอียด DB ให้ client
- Body limit 64 KiB; JSON ตอบ `Cache-Control: no-store` และ `X-Content-Type-Options: nosniff`
- `/docs` redirect ไป `docs/`; spec ที่ `/swagger.json`, `/swagger.yaml`; UI assets bundle ใน binary ไม่โหลด CDN; ปิด online validator; Try it out เปิด/ปิดด้วย config
- Production config ปฏิเสธ `LOG_LEVEL=debug`, `PUBLIC_ORIGIN` ที่ไม่ใช่ https และ `DATABASE_URL` ที่ไม่ใช่ `sslmode=verify-full`; error ของ config ห้ามมีค่า secret
- Roles: `app_migrator` เป็นเจ้าของ schema `app`; `app_runtime`/`app_maintenance` ไม่เป็น superuser/owner และไม่มี BYPASSRLS; goose ใช้ `-table app.goose_db_version`; ไม่มีอะไรถูกสร้างใน `public`
- Migrations ใช้เลขลำดับต่อเนื่อง (`goose create -s`) และห้ามแก้ไฟล์ที่รันแล้ว
- Compose: `migrate` รอ `postgres` healthy; `api` รอ `migrate` จบด้วย exit 0; API image รันเป็น non-root
- Git: `git init` ใน Task 1 หลังมีโครงโค้ดแรก แล้ว commit ทุกท้าย task

## Review Focus

1. **Client ส่ง `X-Request-ID` ของตัวเองมา** — server ต้องไม่ใช้ค่านั้นและต้องตอบ UUIDv7 ใหม่ (test ใน Task 3: `TestRequestIDIgnoresClientValue`)
2. **URL query, cookie หรือ Authorization มีค่าลับ** — ต้องไม่ปรากฏใน log ใดเลย รวมถึง path parameter ดิบ (Task 3: `TestAccessLogUsesRouteTemplateAndOmitsSecrets`)
3. **Request body เกิน 64 KiB** — Fiber ตัดก่อนเข้า middleware chain แต่ client ยังต้องได้ 413 แบบ JSON envelope ที่มี request_id (Task 4: `TestBodyLimitReturns413Envelope`)
4. **เปิด `/docs` โดยไม่มี slash ท้าย หรือผ่าน reverse proxy ที่มี prefix** — ต้อง redirect แบบ relative ไป `docs/` และไม่วนซ้ำ (Task 6: `TestDocsServesSpecAndUI`)
5. **Migration ล้มเหลว** — API ต้องไม่เริ่มทำงานกับ schema ที่ไม่ครบ (Task 8 Step 8: ตรวจด้วย broken migration)

---

## File Structure

| ไฟล์ | หน้าที่ |
| --- | --- |
| `go.mod`, `go.sum` | module `go-backend-stock`, Go 1.27.1 |
| `Makefile` | คำสั่ง test/lint/swagger/migrate/check และติดตั้ง tools ตามเวอร์ชันที่ pin |
| `.gitignore`, `.dockerignore` | ไม่ commit `.env`, `bin/`, `tmp/`; ไม่ส่งไฟล์ไม่จำเป็นเข้า Docker build |
| `internal/config/config.go` (+test) | โหลดและ validate env แยก dev/test/production |
| `internal/logging/logger.go` (+test) | สร้าง `*slog.Logger` JSON, เวลา UTC, field `service` |
| `internal/adapter/http/middleware/requestid.go` | UUIDv7 request ID + accessors |
| `internal/adapter/http/middleware/accesslog.go` | access log ที่เรียก error handler ก่อน log final status |
| `internal/adapter/http/middleware/recover.go` | แปลง panic เป็น error และ log stack ฝั่ง server |
| `internal/adapter/http/middleware/security.go` | security headers ของทุก response |
| `internal/adapter/http/middleware/notfound.go` | catch-all 404 ที่ทำเครื่องหมาย route `unmatched` |
| `internal/adapter/http/errors.go` | `APIError`, `ErrorResponse`, `ErrorHandler` |
| `internal/adapter/http/health.go` | `/healthz`, `/readyz` + annotations |
| `internal/adapter/http/server.go` | ประกอบ Fiber app, middleware order, routes |
| `internal/adapter/http/docs.go` | เสิร์ฟ spec และ Swagger UI พร้อม CSP |
| `internal/adapter/http/assets/` | embed Swagger UI assets + `index.html` |
| `internal/adapter/postgres/pool.go` (+tests) | สร้าง pgx pool; integration tests ของ bootstrap/roles |
| `cmd/api/main.go`, `cmd/api/healthcheck.go` | composition root, graceful shutdown, Swagger general info, subcommand `healthcheck` |
| `docs/api/docs.go`, `swagger.json`, `swagger.yaml` | generated โดย swag ห้ามแก้มือ |
| `docs/api/embed.go` | เขียนเอง: embed `swagger.json`/`swagger.yaml` ให้ composition root |
| `scripts/fetch-swagger-ui.sh` | ดาวน์โหลด Swagger UI 5.33.1 พร้อมตรวจ integrity |
| `deploy/db/bootstrap.sql` | สร้าง roles + schema `app` (local/CI) |
| `deploy/local/initdb/10-bootstrap.sh` | ให้ postgres image รัน bootstrap ตอน volume ว่าง |
| `migrations/00001_baseline.sql` | baseline migration |
| `Dockerfile` | targets `base`, `dev`, `build`, `goose-build`, `migrate`, `runtime` |
| `docker-compose.yml`, `docker-compose.override.yml`, `.air.toml`, `.env.example` | local stack และ dev hot reload |
| `.golangci.yml`, `.github/workflows/ci.yml` | lint และ CI |
| `README.md` | วิธีเปิดระบบ, URLs, logs, tests |

---

### Task 1: ตรวจเวอร์ชัน, Go module และ config validation

**Files:**
- Create: `go.mod`, `.gitignore`, `Makefile`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: ไม่มี
- Produces:
  - `type config.Environment string` ค่า `config.EnvDevelopment`, `config.EnvTest`, `config.EnvProduction`
  - `type config.Config struct { Env Environment; HTTPAddr string; PublicOrigin string; DatabaseURL string; DatabaseMaxConns int; LogLevel slog.Level; DocsEnabled bool; DocsTryItOutEnabled bool; ShutdownTimeout time.Duration }`
  - `func (c config.Config) IsProduction() bool`
  - `func config.Load(getenv func(string) string) (config.Config, error)`

- [ ] **Step 1: ตรวจว่าเวอร์ชันที่ pin มีอยู่จริง**

```bash
go version
go list -m github.com/gofiber/fiber/v3@v3.5.0 github.com/jackc/pgx/v5@v5.11.0 github.com/swaggo/swag@v1.16.6 github.com/pressly/goose/v3@v3.28.0 github.com/go-swagger/go-swagger@v0.34.1
go list -m github.com/golangci/golangci-lint/v2@v2.14.0
curl -fsSL https://registry.npmjs.org/swagger-ui-dist/5.33.1 | head -c 200
```

Expected: `go version go1.27.1 ...` และทุก module แสดงชื่อพร้อมเวอร์ชันโดยไม่มี error; ถ้า Go ในเครื่องไม่ใช่ 1.27.1 ให้ติดตั้งจาก https://go.dev/dl/ ก่อน ถ้าเวอร์ชันใดไม่มีจริง ให้หยุดและแจ้งผู้ใช้พร้อมเวอร์ชันที่มีอยู่ เพราะ pins ถูกยืนยันใน spec แล้ว

- [ ] **Step 2: สร้าง module และ `.gitignore`**

```bash
go mod init go-backend-stock
go mod edit -go=1.27.1
```

`.gitignore`:

```gitignore
# Local secrets and environment
.env

# Build and tool output
/bin/
/tmp/
*.test
coverage.out

# OS/editor
.DS_Store
.idea/
.vscode/
```

- [ ] **Step 3: เขียน failing tests ของ config**

`internal/config/config_test.go`:

```go
package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func envFrom(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func validDevEnv() map[string]string {
	return map[string]string{
		"APP_ENV":       "development",
		"PUBLIC_ORIGIN": "http://localhost:8080",
		"DATABASE_URL":  "postgres://app_runtime:devpass@postgres:5432/stock?sslmode=disable",
	}
}

func TestLoadDevelopmentDefaults(t *testing.T) {
	cfg, err := Load(envFrom(validDevEnv()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Env != EnvDevelopment {
		t.Errorf("Env = %q, want %q", cfg.Env, EnvDevelopment)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.PublicOrigin != "http://localhost:8080" {
		t.Errorf("PublicOrigin = %q", cfg.PublicOrigin)
	}
	if cfg.DatabaseMaxConns != 5 {
		t.Errorf("DatabaseMaxConns = %d, want 5", cfg.DatabaseMaxConns)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want INFO", cfg.LogLevel)
	}
	if !cfg.DocsEnabled || !cfg.DocsTryItOutEnabled {
		t.Errorf("docs flags = %v/%v, want true/true in development", cfg.DocsEnabled, cfg.DocsTryItOutEnabled)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
}

func TestLoadProductionDefaultsDisableDocs(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{
		"APP_ENV":       "production",
		"PUBLIC_ORIGIN": "https://stock.example.com",
		"DATABASE_URL":  "postgres://app_runtime:prodpass@db.example.com:5432/postgres?sslmode=verify-full",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DocsEnabled || cfg.DocsTryItOutEnabled {
		t.Errorf("docs flags = %v/%v, want false/false by default in production", cfg.DocsEnabled, cfg.DocsTryItOutEnabled)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	production := func(e map[string]string) {
		e["APP_ENV"] = "production"
		e["PUBLIC_ORIGIN"] = "https://stock.example.com"
		e["DATABASE_URL"] = "postgres://u:p@db.example.com:5432/postgres?sslmode=verify-full"
	}
	tests := []struct {
		name    string
		mutate  func(map[string]string)
		wantErr string
	}{
		{"missing app env", func(e map[string]string) { delete(e, "APP_ENV") }, "APP_ENV"},
		{"unknown app env", func(e map[string]string) { e["APP_ENV"] = "staging" }, "APP_ENV"},
		{"missing origin", func(e map[string]string) { delete(e, "PUBLIC_ORIGIN") }, "PUBLIC_ORIGIN is required"},
		{"origin with path", func(e map[string]string) { e["PUBLIC_ORIGIN"] = "http://localhost:8080/app" }, "PUBLIC_ORIGIN must be an origin"},
		{"production http origin", func(e map[string]string) {
			production(e)
			e["PUBLIC_ORIGIN"] = "http://stock.example.com"
		}, "must use https in production"},
		{"production debug log", func(e map[string]string) {
			production(e)
			e["LOG_LEVEL"] = "debug"
		}, "LOG_LEVEL=debug"},
		{"production without verify-full", func(e map[string]string) {
			production(e)
			e["DATABASE_URL"] = "postgres://u:p@db.example.com:5432/postgres?sslmode=require"
		}, "sslmode=verify-full"},
		{"non postgres url", func(e map[string]string) { e["DATABASE_URL"] = "mysql://u:p@db:3306/stock" }, "postgres:// URL"},
		{"unknown log level", func(e map[string]string) { e["LOG_LEVEL"] = "verbose" }, "LOG_LEVEL must be one of"},
		{"max conns zero", func(e map[string]string) { e["DB_MAX_CONNS"] = "0" }, "DB_MAX_CONNS"},
		{"max conns text", func(e map[string]string) { e["DB_MAX_CONNS"] = "abc" }, "DB_MAX_CONNS"},
		{"bad bool", func(e map[string]string) { e["DOCS_ENABLED"] = "yes please" }, "DOCS_ENABLED must be true or false"},
		{"try it out without docs", func(e map[string]string) {
			e["DOCS_ENABLED"] = "false"
			e["DOCS_TRY_IT_OUT_ENABLED"] = "true"
		}, "requires DOCS_ENABLED=true"},
		{"shutdown too long", func(e map[string]string) { e["SHUTDOWN_TIMEOUT"] = "5m" }, "SHUTDOWN_TIMEOUT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validDevEnv()
			tt.mutate(env)
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadErrorsNeverEchoDatabasePassword(t *testing.T) {
	_, err := Load(envFrom(map[string]string{
		"APP_ENV":       "production",
		"PUBLIC_ORIGIN": "https://stock.example.com",
		"DATABASE_URL":  "postgres://app_runtime:S3cretPassw0rd@db.example.com:5432/postgres?sslmode=disable",
	}))
	if err == nil {
		t.Fatal("Load() error = nil, want sslmode error")
	}
	if strings.Contains(err.Error(), "S3cretPassw0rd") {
		t.Fatalf("error leaked the database password: %v", err)
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	_, err := Load(envFrom(map[string]string{}))
	if err == nil {
		t.Fatal("Load() error = nil, want errors")
	}
	for _, want := range []string{"APP_ENV", "PUBLIC_ORIGIN", "DATABASE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}
```

- [ ] **Step 4: รัน test ให้เห็นว่าล้มเหลว**

Run: `go test ./internal/config/...`
Expected: FAIL ด้วย `undefined: Load` และ `undefined: EnvDevelopment`

- [ ] **Step 5: เขียน `internal/config/config.go`**

```go
// Package config loads and validates process configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Environment selects the validation rules applied to the configuration.
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvTest        Environment = "test"
	EnvProduction  Environment = "production"
)

// Config is the validated configuration of the API process.
type Config struct {
	Env                 Environment
	HTTPAddr            string
	PublicOrigin        string
	DatabaseURL         string
	DatabaseMaxConns    int
	LogLevel            slog.Level
	DocsEnabled         bool
	DocsTryItOutEnabled bool
	ShutdownTimeout     time.Duration
}

// IsProduction reports whether production-only rules apply.
func (c Config) IsProduction() bool { return c.Env == EnvProduction }

// Load reads every setting through getenv and returns all validation errors at once.
// Error messages never include configured values because they may contain secrets.
func Load(getenv func(string) string) (Config, error) {
	var cfg Config
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
	prod := cfg.IsProduction()

	cfg.HTTPAddr = valueOr(getenv("HTTP_ADDR"), ":8080")

	var err error
	cfg.PublicOrigin, err = parseOrigin(getenv("PUBLIC_ORIGIN"), prod)
	add(err)
	cfg.DatabaseURL, err = parseDatabaseURL(getenv("DATABASE_URL"), prod)
	add(err)
	cfg.DatabaseMaxConns, err = parseIntInRange("DB_MAX_CONNS", getenv("DB_MAX_CONNS"), 5, 1, 50)
	add(err)
	cfg.LogLevel, err = parseLogLevel(getenv("LOG_LEVEL"), prod)
	add(err)
	cfg.DocsEnabled, err = parseBool("DOCS_ENABLED", getenv("DOCS_ENABLED"), !prod)
	add(err)
	cfg.DocsTryItOutEnabled, err = parseBool("DOCS_TRY_IT_OUT_ENABLED", getenv("DOCS_TRY_IT_OUT_ENABLED"), !prod)
	add(err)
	if cfg.DocsTryItOutEnabled && !cfg.DocsEnabled {
		add(errors.New("DOCS_TRY_IT_OUT_ENABLED requires DOCS_ENABLED=true"))
	}
	cfg.ShutdownTimeout, err = parseDurationInRange("SHUTDOWN_TIMEOUT", getenv("SHUTDOWN_TIMEOUT"), 10*time.Second, time.Second, time.Minute)
	add(err)

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

func valueOr(v, fallback string) string {
	if v = strings.TrimSpace(v); v == "" {
		return fallback
	}
	return v
}

func parseOrigin(raw string, prod bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("PUBLIC_ORIGIN is required")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("PUBLIC_ORIGIN must be an origin such as https://example.com")
	}
	if prod && u.Scheme != "https" {
		return "", errors.New("PUBLIC_ORIGIN must use https in production")
	}
	return u.Scheme + "://" + u.Host, nil
}

func parseDatabaseURL(raw string, prod bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("DATABASE_URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		// Never wrap err or echo raw: both can contain the password.
		return "", errors.New("DATABASE_URL must be a postgres:// URL")
	}
	if prod && u.Query().Get("sslmode") != "verify-full" {
		return "", errors.New("DATABASE_URL must set sslmode=verify-full in production")
	}
	return raw, nil
}

func parseIntInRange(name, raw string, fallback, minValue, maxValue int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < minValue || v > maxValue {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, minValue, maxValue)
	}
	return v, nil
}

func parseLogLevel(raw string, prod bool) (slog.Level, error) {
	var level slog.Level
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		level = slog.LevelInfo
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return 0, errors.New("LOG_LEVEL must be one of debug, info, warn, error")
	}
	if prod && level < slog.LevelInfo {
		return 0, errors.New("LOG_LEVEL=debug is not allowed in production")
	}
	return level, nil
}

func parseBool(name, raw string, fallback bool) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", name)
	}
	return v, nil
}

func parseDurationInRange(name, raw string, fallback, minValue, maxValue time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v < minValue || v > maxValue {
		return 0, fmt.Errorf("%s must be a duration between %s and %s", name, minValue, maxValue)
	}
	return v, nil
}
```

- [ ] **Step 6: รัน test ให้ผ่าน**

Run: `go test ./internal/config/...`
Expected: `ok  	go-backend-stock/internal/config`

- [ ] **Step 7: สร้าง `Makefile` เริ่มต้น**

```make
-include .env
export

BIN := $(CURDIR)/bin

.PHONY: test vet tidy-check

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

tidy-check:
	go mod tidy -diff
```

บรรทัดคำสั่งใต้ target ใน Makefile ต้องขึ้นต้นด้วย TAB จริง ไม่ใช่ช่องว่าง

Run: `make test vet`
Expected: test ผ่าน และ `go vet` ไม่มี output

- [ ] **Step 8: `git init` และ commit แรก**

```bash
git init
git add .gitignore go.mod Makefile internal/config AGENTS.md CLAUDE.md docs
git commit -m "chore: initialize Go module with validated configuration"
```

---

### Task 2: Structured JSON logger

**Files:**
- Create: `internal/logging/logger.go`
- Test: `internal/logging/logger_test.go`

**Interfaces:**
- Consumes: ไม่มี
- Produces: `func logging.New(w io.Writer, level slog.Leveler, service string) *slog.Logger` — JSON handler, `time` เป็น UTC, ทุก record มี `service`

- [ ] **Step 1: เขียน failing tests**

`internal/logging/logger_test.go`:

```go
package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNewWritesJSONWithUTCTimeAndService(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo, "stock-api")

	log.Info("server started", slog.String("request_id", "req-1"))

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}
	ts, _ := entry["time"].(string)
	if !strings.HasSuffix(ts, "Z") {
		t.Errorf("time = %q, want UTC with Z suffix", ts)
	}
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("time %q is not RFC3339: %v", ts, err)
	}
	for key, want := range map[string]string{
		"service":    "stock-api",
		"level":      "INFO",
		"msg":        "server started",
		"request_id": "req-1",
	} {
		if got, _ := entry[key].(string); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestNewRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo, "stock-api")

	log.Debug("hidden")

	if buf.Len() != 0 {
		t.Fatalf("debug record written at info level: %q", buf.String())
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้มเหลว**

Run: `go test ./internal/logging/...`
Expected: FAIL ด้วย `undefined: New`

- [ ] **Step 3: เขียน `internal/logging/logger.go`**

```go
// Package logging builds the application logger injected from the composition root.
package logging

import (
	"io"
	"log/slog"
)

// New returns a JSON logger whose timestamps are UTC and whose records carry the service name.
func New(w io.Writer, level slog.Leveler, service string) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey && a.Value.Kind() == slog.KindTime {
				a.Value = slog.TimeValue(a.Value.Time().UTC())
			}
			return a
		},
	})
	return slog.New(handler).With(slog.String("service", service))
}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `go test ./internal/logging/...`
Expected: `ok  	go-backend-stock/internal/logging`

- [ ] **Step 5: Commit**

```bash
git add internal/logging
git commit -m "feat: add structured JSON logger with UTC timestamps"
```

---

### Task 3: Middleware — request ID, access log, recover, security headers, not found

**Files:**
- Create: `internal/adapter/http/middleware/requestid.go`
- Create: `internal/adapter/http/middleware/accesslog.go`
- Create: `internal/adapter/http/middleware/recover.go`
- Create: `internal/adapter/http/middleware/security.go`
- Create: `internal/adapter/http/middleware/notfound.go`
- Test: `internal/adapter/http/middleware/helpers_test.go`, `internal/adapter/http/middleware/middleware_test.go`

**Interfaces:**
- Consumes: ไม่มี (เป็นแพ็กเกจล่างสุดของ HTTP adapter ห้าม import `internal/adapter/http`)
- Produces:
  - `const middleware.HeaderRequestID = "X-Request-ID"`
  - `func middleware.RequestID() fiber.Handler`
  - `func middleware.NewRequestID() (string, error)` — UUIDv7 string
  - `func middleware.GetRequestID(c fiber.Ctx) string` — คืน `""` ถ้ายังไม่มี
  - `func middleware.SetRequestID(c fiber.Ctx, id string)`
  - `func middleware.AccessLog(log *slog.Logger, handleError fiber.ErrorHandler) fiber.Handler`
  - `func middleware.Recover(log *slog.Logger) fiber.Handler`
  - `var middleware.ErrPanic error`
  - `func middleware.SecurityHeaders() fiber.Handler`
  - `func middleware.NotFound() fiber.Handler`
  - `func middleware.MarkRoute(c fiber.Ctx, route string)`

- [ ] **Step 1: เพิ่ม dependencies**

```bash
go get github.com/gofiber/fiber/v3@v3.5.0
go get github.com/google/uuid@latest
```

Expected: `go.mod` มี `github.com/gofiber/fiber/v3 v3.5.0` และ `github.com/google/uuid` เวอร์ชันที่ระบุแน่นอน (go.mod/go.sum เป็นตัว lock; `@latest` ใช้เฉพาะคำสั่งเพิ่ม dependency ครั้งแรก ไม่อยู่ในไฟล์ที่ commit)

- [ ] **Step 2: เขียน test helpers**

`internal/adapter/http/middleware/helpers_test.go`:

```go
package middleware

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func logEntries(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(buf.Bytes()))
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("log line is not JSON: %v (%q)", err, scanner.Text())
		}
		entries = append(entries, entry)
	}
	return entries
}

func findEntry(t *testing.T, entries []map[string]any, msg string) map[string]any {
	t.Helper()
	for _, e := range entries {
		if e["msg"] == msg {
			return e
		}
	}
	t.Fatalf("no log entry with msg %q in %v", msg, entries)
	return nil
}

// doRequest runs the request through the app and always closes the body.
func doRequest(t *testing.T, app *fiber.App, req *http.Request) (*http.Response, []byte) {
	t.Helper()
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, body
}

// testErrorHandler stands in for the production envelope handler, which lives in a parent package.
func testErrorHandler(c fiber.Ctx, err error) error {
	status := fiber.StatusInternalServerError
	var fe *fiber.Error
	if errors.As(err, &fe) {
		status = fe.Code
	}
	return c.Status(status).SendString(http.StatusText(status))
}

func newChainApp(log *slog.Logger) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler, StrictRouting: true, CaseSensitive: true})
	app.Use(RequestID())
	app.Use(AccessLog(log, testErrorHandler))
	app.Use(Recover(log))
	return app
}
```

- [ ] **Step 3: เขียน failing tests**

`internal/adapter/http/middleware/middleware_test.go`:

```go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func TestRequestIDIgnoresClientValue(t *testing.T) {
	log, _ := newTestLogger()
	app := newChainApp(log)
	app.Get("/ok", func(c fiber.Ctx) error { return c.SendString(GetRequestID(c)) })

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set(HeaderRequestID, "client-supplied")
	resp, body := doRequest(t, app, req)

	got := resp.Header.Get(HeaderRequestID)
	if got == "client-supplied" {
		t.Fatal("server reused the client request ID")
	}
	parsed, err := uuid.Parse(got)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("X-Request-ID = %q, want UUIDv7 (err %v)", got, err)
	}
	if string(body) != got {
		t.Errorf("GetRequestID() = %q, header = %q", body, got)
	}
}

func TestRequestIDIsUniquePerRequest(t *testing.T) {
	log, _ := newTestLogger()
	app := newChainApp(log)
	app.Get("/ok", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })

	first, _ := doRequest(t, app, httptest.NewRequest(http.MethodGet, "/ok", nil))
	second, _ := doRequest(t, app, httptest.NewRequest(http.MethodGet, "/ok", nil))

	if first.Header.Get(HeaderRequestID) == second.Header.Get(HeaderRequestID) {
		t.Fatal("two requests received the same request ID")
	}
}

func TestAccessLogRecordsFinalStatus(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		handler    fiber.Handler
		wantStatus int
		wantLevel  string
	}{
		{"success", "/ok", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) }, 200, "INFO"},
		{"client error", "/bad", func(c fiber.Ctx) error { return fiber.ErrBadRequest }, 400, "WARN"},
		{"conflict", "/conflict", func(c fiber.Ctx) error { return fiber.NewError(fiber.StatusConflict, "conflict") }, 409, "WARN"},
		{"panic", "/panic", func(c fiber.Ctx) error { panic("boom") }, 500, "ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, buf := newTestLogger()
			app := newChainApp(log)
			app.Get(tt.path, tt.handler)

			resp, _ := doRequest(t, app, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			entry := findEntry(t, logEntries(t, buf), "http request")
			if got := int(entry["status"].(float64)); got != tt.wantStatus {
				t.Errorf("logged status = %d, want %d", got, tt.wantStatus)
			}
			if entry["level"] != tt.wantLevel {
				t.Errorf("level = %v, want %s", entry["level"], tt.wantLevel)
			}
			if entry["method"] != http.MethodGet || entry["route"] != tt.path || entry["event"] != "http.access" {
				t.Errorf("unexpected access entry: %v", entry)
			}
			if entry["request_id"] != resp.Header.Get(HeaderRequestID) {
				t.Errorf("logged request_id = %v, header = %s", entry["request_id"], resp.Header.Get(HeaderRequestID))
			}
		})
	}
}

func TestRecoverLogsPanicWithRequestID(t *testing.T) {
	log, buf := newTestLogger()
	app := newChainApp(log)
	app.Get("/panic", func(c fiber.Ctx) error { panic("boom") })

	resp, body := doRequest(t, app, httptest.NewRequest(http.MethodGet, "/panic", nil))

	entry := findEntry(t, logEntries(t, buf), "panic recovered")
	if entry["level"] != "ERROR" || entry["panic"] != "boom" {
		t.Errorf("unexpected panic entry: %v", entry)
	}
	if stack, _ := entry["stack"].(string); !strings.Contains(stack, "goroutine") {
		t.Errorf("panic entry has no stack trace: %v", entry)
	}
	if entry["request_id"] != resp.Header.Get(HeaderRequestID) {
		t.Errorf("panic request_id = %v, header = %s", entry["request_id"], resp.Header.Get(HeaderRequestID))
	}
	if strings.Contains(string(body), "goroutine") {
		t.Errorf("response exposed a stack trace: %s", body)
	}
}

func TestAccessLogUsesRouteTemplateAndOmitsSecrets(t *testing.T) {
	log, buf := newTestLogger()
	app := newChainApp(log)
	app.Get("/items/:id", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/items/98765?token=topsecret", nil)
	req.Header.Set("Cookie", "session=cookiesecret")
	req.Header.Set("Authorization", "Bearer authsecret")
	doRequest(t, app, req)

	raw := buf.String()
	for _, secret := range []string{"topsecret", "cookiesecret", "authsecret", "98765"} {
		if strings.Contains(raw, secret) {
			t.Errorf("log contains %q: %s", secret, raw)
		}
	}
	entry := findEntry(t, logEntries(t, buf), "http request")
	if entry["route"] != "/items/:id" {
		t.Errorf("route = %v, want /items/:id", entry["route"])
	}
}

func TestNotFoundMarksUnmatchedRoute(t *testing.T) {
	log, buf := newTestLogger()
	app := newChainApp(log)
	app.Use(NotFound())

	resp, _ := doRequest(t, app, httptest.NewRequest(http.MethodGet, "/nope/secret-path", nil))

	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	entry := findEntry(t, logEntries(t, buf), "http request")
	if entry["route"] != "unmatched" {
		t.Errorf("route = %v, want unmatched", entry["route"])
	}
	if strings.Contains(buf.String(), "secret-path") {
		t.Errorf("raw unmatched path leaked into log: %s", buf.String())
	}
}

func TestSecurityHeaders(t *testing.T) {
	app := fiber.New()
	app.Use(SecurityHeaders())
	app.Get("/ok", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	resp, _ := doRequest(t, app, httptest.NewRequest(http.MethodGet, "/ok", nil))

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}
```

- [ ] **Step 4: รัน test ให้เห็นว่าล้มเหลว**

Run: `go test ./internal/adapter/http/middleware/...`
Expected: FAIL ด้วย `undefined: RequestID`, `undefined: AccessLog` ฯลฯ

- [ ] **Step 5: เขียน `requestid.go`**

```go
// Package middleware contains the Fiber middleware chain shared by every route.
package middleware

import (
	"fmt"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// HeaderRequestID carries the server-generated request ID on every response.
const HeaderRequestID = "X-Request-ID"

type requestIDKey struct{}

// RequestID assigns a fresh UUIDv7 to each request. Client-supplied IDs are ignored.
func RequestID() fiber.Handler {
	return func(c fiber.Ctx) error {
		id, err := NewRequestID()
		if err != nil {
			return err
		}
		SetRequestID(c, id)
		return c.Next()
	}
}

// NewRequestID returns a new UUIDv7 string.
func NewRequestID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate request id: %w", err)
	}
	return id.String(), nil
}

// GetRequestID returns the request ID stored on c, or "" when none was assigned.
func GetRequestID(c fiber.Ctx) string {
	id, _ := c.Locals(requestIDKey{}).(string)
	return id
}

// SetRequestID stores id on c and echoes it in the response header.
func SetRequestID(c fiber.Ctx, id string) {
	c.Locals(requestIDKey{}, id)
	c.Set(HeaderRequestID, id)
}
```

- [ ] **Step 6: เขียน `accesslog.go`**

```go
package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"
)

type routeKey struct{}

// MarkRoute overrides the route template recorded by AccessLog.
func MarkRoute(c fiber.Ctx, route string) {
	c.Locals(routeKey{}, route)
}

func routeTemplate(c fiber.Ctx) string {
	if route, ok := c.Locals(routeKey{}).(string); ok && route != "" {
		return route
	}
	return c.Route().Path
}

// AccessLog records one entry per request after the error handler has produced the
// final status, so failed and panicking requests are never logged as 200.
// It logs the route template only: never the raw path, query string, headers or body.
func AccessLog(log *slog.Logger, handleError fiber.ErrorHandler) fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()
		if chainErr := c.Next(); chainErr != nil {
			if err := handleError(c, chainErr); err != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
			}
		}

		status := c.Response().StatusCode()
		level := slog.LevelInfo
		switch {
		case status >= fiber.StatusInternalServerError:
			level = slog.LevelError
		case status >= fiber.StatusBadRequest:
			level = slog.LevelWarn
		}
		log.LogAttrs(context.Background(), level, "http request",
			slog.String("event", "http.access"),
			slog.String("request_id", GetRequestID(c)),
			slog.String("method", c.Method()),
			slog.String("route", routeTemplate(c)),
			slog.Int("status", status),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
		)
		// The error was already rendered by handleError; returning it would render it twice.
		return nil
	}
}
```

- [ ] **Step 7: เขียน `recover.go`**

```go
package middleware

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/gofiber/fiber/v3"
)

// ErrPanic is returned to the error handler after a panic has been recovered.
var ErrPanic = errors.New("panic recovered")

const maxPanicValueLen = 512

// Recover turns a panic into ErrPanic and logs the panic value and stack server-side only.
func Recover(log *slog.Logger) fiber.Handler {
	return func(c fiber.Ctx) (err error) {
		defer func() {
			if r := recover(); r != nil {
				value := fmt.Sprint(r)
				if len(value) > maxPanicValueLen {
					value = value[:maxPanicValueLen]
				}
				log.Error("panic recovered",
					slog.String("event", "http.panic"),
					slog.String("request_id", GetRequestID(c)),
					slog.String("panic", value),
					slog.String("stack", string(debug.Stack())),
				)
				err = ErrPanic
			}
		}()
		return c.Next()
	}
}
```

- [ ] **Step 8: เขียน `security.go` และ `notfound.go`**

`security.go`:

```go
package middleware

import "github.com/gofiber/fiber/v3"

// SecurityHeaders sets defaults for API responses. Docs handlers override Cache-Control and add a CSP.
func SecurityHeaders() fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("Cache-Control", "no-store")
		c.Set("Referrer-Policy", "no-referrer")
		c.Set("X-Frame-Options", "DENY")
		return c.Next()
	}
}
```

`notfound.go`:

```go
package middleware

import "github.com/gofiber/fiber/v3"

// NotFound must be registered last. It marks the route as "unmatched" so the access log
// never records the raw path of an unknown URL.
func NotFound() fiber.Handler {
	return func(c fiber.Ctx) error {
		MarkRoute(c, "unmatched")
		return fiber.ErrNotFound
	}
}
```

- [ ] **Step 9: รัน test ให้ผ่าน**

Run: `go test -race ./internal/adapter/http/middleware/...`
Expected: `ok  	go-backend-stock/internal/adapter/http/middleware`

- [ ] **Step 10: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/adapter/http/middleware
git commit -m "feat: add request ID, access log, recover and security middleware"
```

---

### Task 4: Error envelope, server assembly และ health endpoints

**Files:**
- Create: `internal/adapter/http/errors.go`
- Create: `internal/adapter/http/health.go`
- Create: `internal/adapter/http/server.go`
- Test: `internal/adapter/http/helpers_test.go`, `internal/adapter/http/server_test.go`

**Interfaces:**
- Consumes: ทุก export ของ `middleware` จาก Task 3
- Produces:
  - `type http.APIError struct { Status int; Code string; Message string }` (implements `error`)
  - `type http.ErrorResponse struct { Error ErrorBody }`, `type http.ErrorBody struct { Code, Message, RequestID string }`
  - `func http.ErrorHandler(log *slog.Logger) fiber.ErrorHandler`
  - `type http.ReadinessChecker interface { Ping(ctx context.Context) error }`
  - `type http.HealthResponse struct { Status string }`, `type http.ReadyResponse struct { Status string }`
  - `type http.Options struct { Logger *slog.Logger; Readiness ReadinessChecker }` (Task 6 เพิ่ม field `Docs`)
  - `func http.NewServer(opts Options) *fiber.App`
  - unexported สำหรับ tests: `newApp(log *slog.Logger) *fiber.App`, `registerRoutes(app *fiber.App, opts Options)`

แพ็กเกจนี้ชื่อ `http` ตามโฟลเดอร์ ไฟล์ที่ต้องใช้ `net/http` ให้ import เป็น `nethttp`

- [ ] **Step 1: เขียน test helpers**

`internal/adapter/http/helpers_test.go`:

```go
package http

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	nethttp "net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
)

type fakeReadiness struct{ err error }

func (f fakeReadiness) Ping(context.Context) error { return f.err }

func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func newTestServer(t *testing.T, readiness ReadinessChecker) (*fiber.App, *bytes.Buffer) {
	t.Helper()
	log, buf := newTestLogger()
	return NewServer(Options{Logger: log, Readiness: readiness}), buf
}

func doRequest(t *testing.T, app *fiber.App, req *nethttp.Request) (*nethttp.Response, []byte) {
	t.Helper()
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, body
}

func decodeError(t *testing.T, body []byte) ErrorBody {
	t.Helper()
	var envelope ErrorResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("body is not an error envelope: %v (%s)", err, body)
	}
	return envelope.Error
}

func accessEntries(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(buf.Bytes()))
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("log line is not JSON: %v", err)
		}
		if entry["msg"] == "http request" {
			entries = append(entries, entry)
		}
	}
	return entries
}
```

- [ ] **Step 2: เขียน failing tests**

`internal/adapter/http/server_test.go`:

```go
package http

import (
	"bytes"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
)

func TestHealthz(t *testing.T) {
	app, _ := newTestServer(t, fakeReadiness{})

	resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/healthz", nil))

	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got HealthResponse
	if err := json.Unmarshal(body, &got); err != nil || got.Status != "ok" {
		t.Fatalf("body = %s, want {\"status\":\"ok\"}", body)
	}
	if resp.Header.Get(middleware.HeaderRequestID) == "" {
		t.Error("missing X-Request-ID")
	}
	if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("missing security headers: %v", resp.Header)
	}
}

func TestReadyzReady(t *testing.T) {
	app, _ := newTestServer(t, fakeReadiness{})

	resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/readyz", nil))

	var got ReadyResponse
	if resp.StatusCode != nethttp.StatusOK || json.Unmarshal(body, &got) != nil || got.Status != "ready" {
		t.Fatalf("status = %d body = %s, want 200 ready", resp.StatusCode, body)
	}
}

func TestReadyzNotReadyHidesCause(t *testing.T) {
	app, logs := newTestServer(t, fakeReadiness{err: errors.New("dial tcp 10.0.0.5:5432: connection refused")})

	resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/readyz", nil))

	if resp.StatusCode != nethttp.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	got := decodeError(t, body)
	if got.Code != "not_ready" || got.RequestID != resp.Header.Get(middleware.HeaderRequestID) {
		t.Errorf("error = %+v", got)
	}
	if strings.Contains(string(body), "10.0.0.5") {
		t.Errorf("response leaked connection details: %s", body)
	}
	if !strings.Contains(logs.String(), "connection refused") {
		t.Error("readiness failure cause was not logged server-side")
	}
}

func TestUnknownRouteReturnsEnvelope404(t *testing.T) {
	app, _ := newTestServer(t, fakeReadiness{})

	resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/nope", nil))

	if resp.StatusCode != nethttp.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	got := decodeError(t, body)
	if got.Code != "not_found" || got.RequestID == "" || got.RequestID != resp.Header.Get(middleware.HeaderRequestID) {
		t.Errorf("error = %+v", got)
	}
}

func TestAPIErrorIsRenderedAndLoggedWithItsStatus(t *testing.T) {
	log, logs := newTestLogger()
	app := newApp(log)
	app.Get("/conflict", func(c fiber.Ctx) error {
		return &APIError{Status: fiber.StatusConflict, Code: "portfolio_name_conflict", Message: "Portfolio name already exists"}
	})
	app.Use(middleware.NotFound())

	resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/conflict", nil))

	if resp.StatusCode != nethttp.StatusConflict || decodeError(t, body).Code != "portfolio_name_conflict" {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	entries := accessEntries(t, logs)
	if len(entries) != 1 || int(entries[0]["status"].(float64)) != nethttp.StatusConflict {
		t.Errorf("access log = %v, want one entry with status 409", entries)
	}
}

func TestPanicReturnsGeneric500AndLogsFinalStatus(t *testing.T) {
	log, logs := newTestLogger()
	app := newApp(log)
	app.Get("/boom", func(c fiber.Ctx) error { panic("db password=hunter2 leaked in panic") })
	app.Use(middleware.NotFound())

	resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/boom", nil))

	if resp.StatusCode != nethttp.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if got := decodeError(t, body); got.Code != "internal" {
		t.Errorf("code = %q, want internal", got.Code)
	}
	for _, leak := range []string{"hunter2", "goroutine", "panic"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("response contains %q: %s", leak, body)
		}
	}
	entries := accessEntries(t, logs)
	if len(entries) != 1 || int(entries[0]["status"].(float64)) != nethttp.StatusInternalServerError {
		t.Errorf("access log = %v, want one entry with status 500", entries)
	}
}

func TestBodyLimitReturns413Envelope(t *testing.T) {
	app, _ := newTestServer(t, fakeReadiness{})
	req := httptest.NewRequest(nethttp.MethodPost, "/healthz", bytes.NewReader(make([]byte, 64*1024+1)))
	req.Header.Set("Content-Type", "application/json")

	resp, body := doRequest(t, app, req)

	if resp.StatusCode != nethttp.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	got := decodeError(t, body)
	if got.Code != "payload_too_large" || got.RequestID == "" {
		t.Errorf("error = %+v, want payload_too_large with request_id", got)
	}
}
```

- [ ] **Step 3: รัน test ให้เห็นว่าล้มเหลว**

Run: `go test ./internal/adapter/http/`
Expected: FAIL ด้วย `undefined: NewServer`, `undefined: APIError` ฯลฯ

- [ ] **Step 4: เขียน `errors.go`**

```go
// Package http is the primary adapter: Fiber server, middleware chain, handlers and DTOs.
package http

import (
	"errors"
	"log/slog"
	nethttp "net/http"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
)

// APIError is an HTTP-level error with a stable machine-readable code.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// ErrorResponse is the single error envelope used by every endpoint.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody carries the error code, a human-readable message and the request ID.
type ErrorBody struct {
	Code      string `json:"code" example:"not_found"`
	Message   string `json:"message" example:"Not Found"`
	RequestID string `json:"request_id" example:"0192f0c1-7a8e-7cc4-9d2b-3f6c1a2b4c5d"`
}

// ErrorHandler renders every error as ErrorResponse. Server errors are logged with the
// request ID; their details never reach the client.
func ErrorHandler(log *slog.Logger) fiber.ErrorHandler {
	return func(c fiber.Ctx, err error) error {
		apiErr := toAPIError(err)

		// Errors raised before the middleware chain (e.g. body too large) have no ID yet.
		requestID := middleware.GetRequestID(c)
		if requestID == "" {
			if id, idErr := middleware.NewRequestID(); idErr == nil {
				middleware.SetRequestID(c, id)
				requestID = id
			}
		}

		if apiErr.Status >= nethttp.StatusInternalServerError {
			log.Error("request failed",
				slog.String("event", "http.error"),
				slog.String("request_id", requestID),
				slog.String("error_code", apiErr.Code),
				slog.String("error", err.Error()),
			)
		}

		c.Set("Cache-Control", "no-store")
		c.Set("X-Content-Type-Options", "nosniff")
		return c.Status(apiErr.Status).JSON(ErrorResponse{Error: ErrorBody{
			Code:      apiErr.Code,
			Message:   apiErr.Message,
			RequestID: requestID,
		}})
	}
}

func toAPIError(err error) *APIError {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return &APIError{Status: fiberErr.Code, Code: codeForStatus(fiberErr.Code), Message: nethttp.StatusText(fiberErr.Code)}
	}
	return &APIError{Status: nethttp.StatusInternalServerError, Code: "internal", Message: "Internal server error"}
}

func codeForStatus(status int) string {
	switch status {
	case nethttp.StatusNotFound:
		return "not_found"
	case nethttp.StatusMethodNotAllowed:
		return "method_not_allowed"
	case nethttp.StatusRequestTimeout:
		return "request_timeout"
	case nethttp.StatusRequestEntityTooLarge:
		return "payload_too_large"
	case nethttp.StatusTooManyRequests:
		return "rate_limited"
	}
	if status >= nethttp.StatusInternalServerError {
		return "internal"
	}
	return "invalid_input"
}
```

- [ ] **Step 5: เขียน `health.go`**

```go
package http

import (
	"context"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
)

// ReadinessChecker reports whether a dependency required to serve traffic is reachable.
type ReadinessChecker interface {
	Ping(ctx context.Context) error
}

// HealthResponse is returned by /healthz.
type HealthResponse struct {
	Status string `json:"status" example:"ok"`
}

// ReadyResponse is returned by /readyz when the API can serve traffic.
type ReadyResponse struct {
	Status string `json:"status" example:"ready"`
}

// HealthHandler serves liveness and readiness probes.
type HealthHandler struct {
	readiness ReadinessChecker
	log       *slog.Logger
	budget    time.Duration
}

// Healthz godoc
// @Summary      Liveness probe
// @Description  Returns 200 while the process is running. Does not check the database.
// @Tags         Health
// @ID           getHealthz
// @Produce      json
// @Success      200  {object}  HealthResponse
// @Header       200  {string}  X-Request-ID  "Server-generated UUIDv7 for this request"
// @Router       /healthz [get]
func (h HealthHandler) Healthz(c fiber.Ctx) error {
	return c.JSON(HealthResponse{Status: "ok"})
}

// Readyz godoc
// @Summary      Readiness probe
// @Description  Pings the database within a 1 second budget. Returns 503 not_ready without connection details when it is unreachable.
// @Tags         Health
// @ID           getReadyz
// @Produce      json
// @Success      200  {object}  ReadyResponse
// @Failure      503  {object}  ErrorResponse
// @Header       200  {string}  X-Request-ID  "Server-generated UUIDv7 for this request"
// @Router       /readyz [get]
func (h HealthHandler) Readyz(c fiber.Ctx) error {
	// Detached from fiber.Ctx on purpose: the context is bounded by budget and never outlives this call.
	ctx, cancel := context.WithTimeout(context.Background(), h.budget)
	defer cancel()
	if err := h.readiness.Ping(ctx); err != nil {
		h.log.Warn("readiness check failed",
			slog.String("event", "health.not_ready"),
			slog.String("request_id", middleware.GetRequestID(c)),
			slog.String("error", err.Error()),
		)
		return &APIError{Status: fiber.StatusServiceUnavailable, Code: "not_ready", Message: "Service is not ready"}
	}
	return c.JSON(ReadyResponse{Status: "ready"})
}
```

- [ ] **Step 6: เขียน `server.go`**

```go
package http

import (
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
)

const (
	bodyLimitBytes  = 64 * 1024
	readTimeout     = 10 * time.Second
	writeTimeout    = 15 * time.Second
	idleTimeout     = 60 * time.Second
	readinessBudget = time.Second
)

// Options are the dependencies of the HTTP server, assembled in cmd/api/main.go.
type Options struct {
	Logger    *slog.Logger
	Readiness ReadinessChecker
}

// NewServer builds the Fiber app with the full middleware chain and every route.
func NewServer(opts Options) *fiber.App {
	app := newApp(opts.Logger)
	registerRoutes(app, opts)
	app.Use(middleware.NotFound())
	return app
}

func newApp(log *slog.Logger) *fiber.App {
	errorHandler := ErrorHandler(log)
	app := fiber.New(fiber.Config{
		AppName:      "stock-api",
		ErrorHandler: errorHandler,
		BodyLimit:    bodyLimitBytes,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
		// StrictRouting keeps /docs and /docs/ distinct so the docs redirect cannot loop.
		StrictRouting: true,
		CaseSensitive: true,
	})
	// Order matters: the request ID exists before anything logs, and AccessLog wraps
	// Recover so a recovered panic is logged with its final 500 status.
	app.Use(middleware.RequestID())
	app.Use(middleware.AccessLog(log, errorHandler))
	app.Use(middleware.Recover(log))
	app.Use(middleware.SecurityHeaders())
	return app
}

func registerRoutes(app *fiber.App, opts Options) {
	health := HealthHandler{readiness: opts.Readiness, log: opts.Logger, budget: readinessBudget}
	app.Get("/healthz", health.Healthz)
	app.Get("/readyz", health.Readyz)
}
```

- [ ] **Step 7: รัน test ให้ผ่าน**

Run: `go test -race ./internal/adapter/http/...`
Expected: `ok` ทั้ง `internal/adapter/http` และ `internal/adapter/http/middleware`

ถ้า `TestBodyLimitReturns413Envelope` ได้ status 413 แต่ body ไม่ใช่ JSON แปลว่า Fiber v3.5.0 ไม่ส่ง body-limit error ผ่าน `ErrorHandler` ให้หยุดและรายงานผู้ใช้พร้อม body ที่ได้ ห้ามลบ test เพื่อให้ผ่าน

- [ ] **Step 8: Commit**

```bash
git add internal/adapter/http
git commit -m "feat: add error envelope, health endpoints and server assembly"
```

---

### Task 5: PostgreSQL pool และ composition root

**Files:**
- Create: `internal/adapter/postgres/pool.go`
- Create: `cmd/api/main.go`, `cmd/api/healthcheck.go`
- Test: `internal/adapter/postgres/testdb_test.go`, `internal/adapter/postgres/pool_test.go`

**Interfaces:**
- Consumes: `config.Load`, `config.Config` (Task 1); `logging.New` (Task 2); `http.NewServer`, `http.Options` (Task 4)
- Produces:
  - `func postgres.NewPool(ctx context.Context, databaseURL string, maxConns int32) (*pgxpool.Pool, error)`
  - test helper `integrationURL(t *testing.T, key string) string` ใน package `postgres` (Task 7 ใช้ต่อ)
  - binary `api` ที่รับ subcommand `healthcheck`

- [ ] **Step 1: เพิ่ม pgx**

```bash
go get github.com/jackc/pgx/v5@v5.11.0
```

- [ ] **Step 2: เขียน failing tests**

`internal/adapter/postgres/testdb_test.go`:

```go
package postgres

import (
	"os"
	"testing"
)

// integrationURL returns a database URL from the environment. Integration tests are skipped
// locally when it is unset, but fail in CI where REQUIRE_INTEGRATION=1.
func integrationURL(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		if os.Getenv("REQUIRE_INTEGRATION") == "1" {
			t.Fatalf("%s is required when REQUIRE_INTEGRATION=1", key)
		}
		t.Skipf("%s not set; skipping integration test", key)
	}
	return v
}
```

`internal/adapter/postgres/pool_test.go`:

```go
package postgres

import (
	"strings"
	"testing"
)

func TestNewPoolErrorDoesNotLeakPassword(t *testing.T) {
	_, err := NewPool(t.Context(), "postgres://app_runtime:S3cretPassw0rd@localhost:5432/stock?sslmode=bogus", 2)
	if err == nil {
		t.Fatal("NewPool() error = nil, want invalid sslmode error")
	}
	if strings.Contains(err.Error(), "S3cretPassw0rd") {
		t.Fatalf("error leaked the password: %v", err)
	}
}

func TestNewPoolPingsDatabase(t *testing.T) {
	pool, err := NewPool(t.Context(), integrationURL(t, "TEST_RUNTIME_DATABASE_URL"), 2)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}
```

- [ ] **Step 3: รัน test ให้เห็นว่าล้มเหลว**

Run: `go test ./internal/adapter/postgres/...`
Expected: FAIL ด้วย `undefined: NewPool`

- [ ] **Step 4: เขียน `pool.go`**

```go
// Package postgres is the secondary adapter for PostgreSQL (local Docker and Supabase).
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool parses databaseURL and creates a lazily-connecting pool limited to maxConns.
// pgx redacts the password in parse errors; TestNewPoolErrorDoesNotLeakPassword guards that.
func NewPool(ctx context.Context, databaseURL string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	return pool, nil
}
```

- [ ] **Step 5: รัน test ให้ผ่าน**

Run: `go test ./internal/adapter/postgres/...`
Expected: `ok` โดย `TestNewPoolPingsDatabase` แสดง SKIP เพราะยังไม่ได้ตั้ง `TEST_RUNTIME_DATABASE_URL`

ถ้า `TestNewPoolErrorDoesNotLeakPassword` ล้มเพราะ error มีรหัสผ่าน ให้เปลี่ยนบรรทัด return แรกเป็น `return nil, errors.New("parse database config: invalid DATABASE_URL")` (ไม่ wrap err) แล้วรัน test ซ้ำ

- [ ] **Step 6: เขียน `cmd/api/healthcheck.go`**

```go
package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"
)

// runHealthcheck lets the distroless image probe itself without curl: `api healthcheck`.
func runHealthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
```

- [ ] **Step 7: เขียน `cmd/api/main.go`**

```go
// Command api is the composition root of the stock portfolio tracker API.
//
// @title           Stock Portfolio Tracker API
// @version         1.0
// @description     Records US stock trades and tracks portfolio cost and P&L. It does not place real orders.
// @description     Milestone 1 exposes only the Health endpoints; every other endpoint is not implemented yet.
// @BasePath        /
// @tag.name        Health
// @tag.description Liveness and readiness probes
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v3"

	httpadapter "go-backend-stock/internal/adapter/http"
	"go-backend-stock/internal/adapter/postgres"
	"go-backend-stock/internal/config"
	"go-backend-stock/internal/logging"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logging.New(os.Stdout, cfg.LogLevel, "stock-api")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	//nolint:gosec // DatabaseMaxConns is validated to 1..50 by config.Load.
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, int32(cfg.DatabaseMaxConns))
	if err != nil {
		return err
	}
	defer pool.Close()

	app := httpadapter.NewServer(httpadapter.Options{
		Logger:    log,
		Readiness: pool,
	})

	listenErr := make(chan error, 1)
	go func() {
		listenErr <- app.Listen(cfg.HTTPAddr, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	log.Info("server started", "event", "server.start", "addr", cfg.HTTPAddr, "env", string(cfg.Env))

	select {
	case err := <-listenErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("listen: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	log.Info("shutting down", "event", "server.stop", "timeout", cfg.ShutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	log.Info("server stopped", "event", "server.stopped")
	return nil
}
```

- [ ] **Step 8: ตรวจการรันจริงโดยไม่มี database**

```bash
go build ./...
APP_ENV=development PUBLIC_ORIGIN=http://localhost:8080 \
  DATABASE_URL='postgres://nobody:nothing@127.0.0.1:1/none?sslmode=disable' \
  go run ./cmd/api
```

ในอีกหน้าต่าง terminal:

```bash
curl -i http://localhost:8080/healthz
curl -i http://localhost:8080/readyz
curl -i 'http://localhost:8080/nope?token=secret'
```

Expected:
- `/healthz` → `200` และ `{"status":"ok"}` พร้อม header `X-Request-ID`
- `/readyz` → `503` และ `{"error":{"code":"not_ready",...}}` โดยไม่มี `127.0.0.1:1` ใน body
- `/nope` → `404` envelope; หน้าต่างแรกมี JSON log `"route":"unmatched"` และไม่มีคำว่า `secret`
- กด Ctrl+C ในหน้าต่างแรก → log `server.stop` แล้ว `server.stopped` และ process จบด้วย exit 0

- [ ] **Step 9: ตรวจ config ที่ผิดทำให้เริ่มไม่ได้**

```bash
APP_ENV=production PUBLIC_ORIGIN=http://x DATABASE_URL='postgres://u:hunter2@db:5432/x?sslmode=disable' go run ./cmd/api; echo "exit=$?"
```

Expected: stderr มี `load config:` พร้อมข้อความเรื่อง https และ sslmode, ไม่มีคำว่า `hunter2`, `exit=1`

- [ ] **Step 10: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/adapter/postgres cmd/api
git commit -m "feat: add composition root with pgx pool and graceful shutdown"
```

---

### Task 6: Swagger generation และ Swagger UI แบบ offline

**Files:**
- Create: `scripts/fetch-swagger-ui.sh`
- Create: `internal/adapter/http/assets/assets.go`, `internal/adapter/http/assets/swagger-ui/index.html`
- Create (จาก script): `internal/adapter/http/assets/swagger-ui/{swagger-ui.css,swagger-ui-bundle.js,swagger-ui-standalone-preset.js,favicon-32x32.png,LICENSE,NOTICE,VERSION}`
- Create: `internal/adapter/http/docs.go`
- Create (generated): `docs/api/docs.go`, `docs/api/swagger.json`, `docs/api/swagger.yaml`
- Create: `docs/api/embed.go`
- Modify: `internal/adapter/http/server.go` (เพิ่ม `Docs` ใน `Options` และเรียก `registerDocs`)
- Modify: `cmd/api/main.go` (ส่ง spec และ flags เข้า `Options.Docs`)
- Modify: `Makefile`
- Test: `internal/adapter/http/docs_test.go`, `internal/adapter/http/contract_test.go`

**Interfaces:**
- Consumes: `http.Options`, `newApp`, `registerRoutes`, `doRequest`, `newTestServer`, `fakeReadiness` (Task 4); `config.Config.DocsEnabled`, `DocsTryItOutEnabled` (Task 1)
- Produces:
  - `type http.DocsOptions struct { Enabled bool; TryItOutEnabled bool; SpecJSON []byte; SpecYAML []byte }`
  - field `Options.Docs DocsOptions`
  - `var assets.SwaggerUI embed.FS`
  - `var api.SwaggerJSON []byte`, `var api.SwaggerYAML []byte` ใน package `go-backend-stock/docs/api`
  - Make targets `tools`, `swagger`

- [ ] **Step 1: เขียน `scripts/fetch-swagger-ui.sh` และดาวน์โหลด assets**

```bash
#!/usr/bin/env bash
# Downloads Swagger UI from the npm registry, verifies its integrity hash and copies the
# files the API embeds. The copied files are committed; CI does not re-download them.
set -euo pipefail

VERSION="5.33.1"
DEST="internal/adapter/http/assets/swagger-ui"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

expected="$(curl -fsSL "https://registry.npmjs.org/swagger-ui-dist/${VERSION}" \
  | python3 -c 'import json, sys; print(json.load(sys.stdin)["dist"]["integrity"])')"
curl -fsSL -o "$WORK/package.tgz" "https://registry.npmjs.org/swagger-ui-dist/-/swagger-ui-dist-${VERSION}.tgz"
actual="sha512-$(openssl dgst -sha512 -binary "$WORK/package.tgz" | openssl base64 -A)"
if [ "$expected" != "$actual" ]; then
  echo "integrity mismatch: expected $expected got $actual" >&2
  exit 1
fi

mkdir -p "$WORK/extract" "$DEST"
tar -xzf "$WORK/package.tgz" -C "$WORK/extract"
for file in swagger-ui.css swagger-ui-bundle.js swagger-ui-standalone-preset.js favicon-32x32.png LICENSE NOTICE; do
  cp "$WORK/extract/package/$file" "$DEST/$file"
done
echo "$VERSION" > "$DEST/VERSION"
echo "Swagger UI ${VERSION} copied to ${DEST}"
```

```bash
chmod +x scripts/fetch-swagger-ui.sh
./scripts/fetch-swagger-ui.sh
ls internal/adapter/http/assets/swagger-ui
```

Expected: `Swagger UI 5.33.1 copied to ...` และมีไฟล์ครบ 7 ไฟล์ (`swagger-ui.css`, `swagger-ui-bundle.js`, `swagger-ui-standalone-preset.js`, `favicon-32x32.png`, `LICENSE`, `NOTICE`, `VERSION`)

- [ ] **Step 2: เขียน `index.html` และ embed package**

`internal/adapter/http/assets/swagger-ui/index.html`:

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Stock Portfolio API Docs</title>
  <link rel="stylesheet" href="swagger-ui.css">
  <link rel="icon" type="image/png" href="favicon-32x32.png">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="swagger-ui-bundle.js"></script>
  <script src="swagger-ui-standalone-preset.js"></script>
  <script src="swagger-initializer.js"></script>
</body>
</html>
```

`internal/adapter/http/assets/assets.go`:

```go
// Package assets embeds the Swagger UI files served under /docs/.
package assets

import "embed"

// SwaggerUI holds Swagger UI 5.33.1 (Apache-2.0, see swagger-ui/LICENSE and NOTICE) and index.html.
//
//go:embed swagger-ui
var SwaggerUI embed.FS
```

- [ ] **Step 3: เขียน failing tests ของ docs**

`internal/adapter/http/docs_test.go`:

```go
package http

import (
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func newDocsServer(t *testing.T, docs DocsOptions) *fiber.App {
	t.Helper()
	log, _ := newTestLogger()
	return NewServer(Options{Logger: log, Readiness: fakeReadiness{}, Docs: docs})
}

func enabledDocs(tryItOut bool) DocsOptions {
	return DocsOptions{
		Enabled:         true,
		TryItOutEnabled: tryItOut,
		SpecJSON:        []byte(`{"swagger":"2.0"}`),
		SpecYAML:        []byte("swagger: \"2.0\"\n"),
	}
}

func TestDocsDisabledReturns404(t *testing.T) {
	app := newDocsServer(t, DocsOptions{Enabled: false})
	for _, path := range []string{"/swagger.json", "/swagger.yaml", "/docs", "/docs/"} {
		resp, _ := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, path, nil))
		if resp.StatusCode != nethttp.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404 when docs are disabled", path, resp.StatusCode)
		}
	}
}

func TestDocsServesSpecAndUI(t *testing.T) {
	app := newDocsServer(t, enabledDocs(false))

	resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/swagger.json", nil))
	if resp.StatusCode != nethttp.StatusOK || string(body) != `{"swagger":"2.0"}` ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Errorf("swagger.json: status=%d type=%q body=%s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}

	resp, body = doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/swagger.yaml", nil))
	if resp.StatusCode != nethttp.StatusOK || !strings.Contains(string(body), "swagger:") ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), "application/yaml") {
		t.Errorf("swagger.yaml: status=%d type=%q body=%s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}

	resp, _ = doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/docs", nil))
	if resp.StatusCode != nethttp.StatusMovedPermanently || resp.Header.Get("Location") != "docs/" {
		t.Errorf("/docs: status=%d location=%q, want 301 to relative docs/", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp, body = doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/docs/", nil))
	if resp.StatusCode != nethttp.StatusOK || !strings.Contains(string(body), "swagger-initializer.js") {
		t.Errorf("/docs/: status=%d body=%s", resp.StatusCode, body)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "http") {
		t.Errorf("CSP = %q, want self-only scripts without remote origins", csp)
	}

	resp, _ = doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/docs/swagger-ui-bundle.js", nil))
	if resp.StatusCode != nethttp.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") {
		t.Errorf("bundle: status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	for _, path := range []string{"/docs/unknown.js", "/docs/LICENSE", "/docs/index.html"} {
		resp, _ = doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, path, nil))
		if resp.StatusCode != nethttp.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404 for non-allowlisted file", path, resp.StatusCode)
		}
	}
}

func TestDocsInitializerRespectsTryItOut(t *testing.T) {
	tests := []struct {
		tryItOut bool
		want     string
	}{
		{false, "supportedSubmitMethods: []"},
		{true, `supportedSubmitMethods: ["get", "post", "put", "patch", "delete"]`},
	}
	for _, tt := range tests {
		app := newDocsServer(t, enabledDocs(tt.tryItOut))
		resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, "/docs/swagger-initializer.js", nil))
		if resp.StatusCode != nethttp.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		for _, want := range []string{tt.want, "validatorUrl: null", `url: "../swagger.json"`} {
			if !strings.Contains(string(body), want) {
				t.Errorf("tryItOut=%v initializer missing %q:\n%s", tt.tryItOut, want, body)
			}
		}
	}
}
```

- [ ] **Step 4: รัน test ให้เห็นว่าล้มเหลว**

Run: `go test ./internal/adapter/http/`
Expected: FAIL ด้วย `unknown field Docs in struct literal` และ `undefined: DocsOptions`

- [ ] **Step 5: เขียน `docs.go`**

```go
package http

import (
	"io/fs"

	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/assets"
)

// DocsOptions controls the Swagger UI and spec endpoints. Disabled docs register no routes.
type DocsOptions struct {
	Enabled         bool
	TryItOutEnabled bool
	SpecJSON        []byte
	SpecYAML        []byte
}

const docsCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; " +
	"base-uri 'none'; form-action 'self'"

// docsAssets is the allowlist of files served under /docs/; anything else is 404.
var docsAssets = map[string]string{
	"swagger-ui.css":                  "text/css; charset=utf-8",
	"swagger-ui-bundle.js":            "text/javascript; charset=utf-8",
	"swagger-ui-standalone-preset.js": "text/javascript; charset=utf-8",
	"favicon-32x32.png":               "image/png",
}

func registerDocs(app *fiber.App, opts DocsOptions) {
	if !opts.Enabled {
		return
	}
	app.Get("/swagger.json", sendBytes("application/json", opts.SpecJSON))
	app.Get("/swagger.yaml", sendBytes("application/yaml", opts.SpecYAML))
	app.Get("/docs", func(c fiber.Ctx) error {
		// Relative so the redirect also works behind a reverse proxy path prefix.
		c.Set(fiber.HeaderLocation, "docs/")
		return c.SendStatus(fiber.StatusMovedPermanently)
	})
	app.Get("/docs/", func(c fiber.Ctx) error {
		return sendAsset(c, "index.html", "text/html; charset=utf-8")
	})
	app.Get("/docs/swagger-initializer.js", sendDocsBytes("text/javascript; charset=utf-8", swaggerInitializer(opts.TryItOutEnabled)))
	app.Get("/docs/:file", func(c fiber.Ctx) error {
		name := c.Params("file")
		contentType, ok := docsAssets[name]
		if !ok {
			return fiber.ErrNotFound
		}
		return sendAsset(c, name, contentType)
	})
}

func sendBytes(contentType string, body []byte) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, contentType)
		return c.Send(body)
	}
}

func sendDocsBytes(contentType string, body []byte) fiber.Handler {
	return func(c fiber.Ctx) error {
		setDocsHeaders(c)
		c.Set(fiber.HeaderContentType, contentType)
		return c.Send(body)
	}
}

func sendAsset(c fiber.Ctx, name, contentType string) error {
	body, err := fs.ReadFile(assets.SwaggerUI, "swagger-ui/"+name)
	if err != nil {
		return fiber.ErrNotFound
	}
	setDocsHeaders(c)
	c.Set(fiber.HeaderContentType, contentType)
	return c.Send(body)
}

func setDocsHeaders(c fiber.Ctx) {
	c.Set("Content-Security-Policy", docsCSP)
	c.Set("Cache-Control", "no-cache")
}

func swaggerInitializer(tryItOut bool) []byte {
	methods := "[]"
	if tryItOut {
		methods = `["get", "post", "put", "patch", "delete"]`
	}
	return []byte(`window.addEventListener("load", function () {
  window.ui = SwaggerUIBundle({
    url: "../swagger.json",
    dom_id: "#swagger-ui",
    deepLinking: true,
    validatorUrl: null,
    supportedSubmitMethods: ` + methods + `,
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
    layout: "StandaloneLayout"
  });
});
`)
}
```

- [ ] **Step 6: แก้ `server.go` ให้ลงทะเบียน docs**

แทนที่ `Options` และ `registerRoutes` ใน `internal/adapter/http/server.go` ด้วย:

```go
// Options are the dependencies of the HTTP server, assembled in cmd/api/main.go.
type Options struct {
	Logger    *slog.Logger
	Readiness ReadinessChecker
	Docs      DocsOptions
}
```

```go
func registerRoutes(app *fiber.App, opts Options) {
	health := HealthHandler{readiness: opts.Readiness, log: opts.Logger, budget: readinessBudget}
	app.Get("/healthz", health.Healthz)
	app.Get("/readyz", health.Readyz)
	registerDocs(app, opts.Docs)
}
```

- [ ] **Step 7: รัน docs tests ให้ผ่าน**

Run: `go test -race ./internal/adapter/http/...`
Expected: `ok` ทั้งสอง package

- [ ] **Step 8: เพิ่ม Makefile targets สำหรับ tools และ swagger**

ต่อท้าย `Makefile`:

```make
SWAG_VERSION := v1.16.6
GO_SWAGGER_VERSION := v0.34.1
GOOSE_VERSION := v3.28.0
GOLANGCI_LINT_VERSION := v2.14.0
GOOSE_TAGS := no_clickhouse no_duckdb no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb

SWAG := $(BIN)/swag
SWAGGER := $(BIN)/swagger
GOOSE := $(BIN)/goose
GOLANGCI_LINT := $(BIN)/golangci-lint

.PHONY: tools swagger

tools:
	GOBIN=$(BIN) go install github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION)
	GOBIN=$(BIN) go install github.com/go-swagger/go-swagger/cmd/swagger@$(GO_SWAGGER_VERSION)
	CGO_ENABLED=0 GOBIN=$(BIN) go install -tags '$(GOOSE_TAGS)' github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)

swagger:
	$(SWAG) init -g cmd/api/main.go -d . --parseInternal -o docs/api --outputTypes go,json,yaml
	$(SWAGGER) validate docs/api/swagger.yaml
```

golangci-lint ไม่อยู่ใน `tools` เพราะ CI ใช้ action ทางการ; ติดตั้งใช้ในเครื่องใน Task 9

- [ ] **Step 9: Generate spec**

```bash
go get github.com/swaggo/swag@v1.16.6
make tools
make swagger
head -3 docs/api/docs.go
```

Expected:
- `docs/api/docs.go`, `swagger.json`, `swagger.yaml` ถูกสร้าง
- `head` แสดง `package api` (ถ้าแสดงชื่ออื่น ให้ใช้ชื่อนั้นใน `embed.go` ของ Step 10 แทน)
- `swagger validate` แสดง `The swagger spec at "docs/api/swagger.yaml" is valid against swagger specification 2.0`

- [ ] **Step 10: เขียน `docs/api/embed.go` (ไฟล์เขียนเอง ไม่ใช่ generated)**

```go
package api

import _ "embed"

// SwaggerJSON is the generated Swagger 2.0 spec served at /swagger.json.
//
//go:embed swagger.json
var SwaggerJSON []byte

// SwaggerYAML is the generated Swagger 2.0 spec served at /swagger.yaml.
//
//go:embed swagger.yaml
var SwaggerYAML []byte
```

ไฟล์นี้อยู่ข้าง generated files เพราะ `go:embed` อ่านได้เฉพาะไฟล์ในโฟลเดอร์ของ package; composition root ใช้เฉพาะสองตัวแปรนี้ ไม่ใช้ registry ของ `docs.go`

- [ ] **Step 11: ส่ง docs เข้า server ใน `cmd/api/main.go`**

เพิ่ม import `apidocs "go-backend-stock/docs/api"` และแทนที่การสร้าง app ด้วย:

```go
	app := httpadapter.NewServer(httpadapter.Options{
		Logger:    log,
		Readiness: pool,
		Docs: httpadapter.DocsOptions{
			Enabled:         cfg.DocsEnabled,
			TryItOutEnabled: cfg.DocsTryItOutEnabled,
			SpecJSON:        apidocs.SwaggerJSON,
			SpecYAML:        apidocs.SwaggerYAML,
		},
	})
```

- [ ] **Step 12: เขียน contract test ที่เทียบ spec กับ response จริง**

`internal/adapter/http/contract_test.go`:

```go
package http

import (
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type swaggerSpec struct {
	Paths       map[string]map[string]swaggerOperation `json:"paths"`
	Definitions map[string]swaggerSchema               `json:"definitions"`
}

type swaggerOperation struct {
	OperationID string                     `json:"operationId"`
	Responses   map[string]swaggerResponse `json:"responses"`
}

type swaggerResponse struct {
	Schema *swaggerSchema `json:"schema"`
}

type swaggerSchema struct {
	Ref        string                   `json:"$ref"`
	Properties map[string]swaggerSchema `json:"properties"`
}

func loadSpec(t *testing.T) swaggerSpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "api", "swagger.json"))
	if err != nil {
		t.Fatalf("read swagger.json (run `make swagger` first): %v", err)
	}
	var spec swaggerSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse swagger.json: %v", err)
	}
	return spec
}

func TestSwaggerOperationIDsAreUnique(t *testing.T) {
	spec := loadSpec(t)
	seen := map[string]string{}
	for path, ops := range spec.Paths {
		for method, op := range ops {
			where := strings.ToUpper(method) + " " + path
			if op.OperationID == "" {
				t.Errorf("%s has no operationId", where)
				continue
			}
			if prev, dup := seen[op.OperationID]; dup {
				t.Errorf("operationId %q used by %s and %s", op.OperationID, prev, where)
			}
			seen[op.OperationID] = where
		}
	}
}

func TestSwaggerContractMatchesResponses(t *testing.T) {
	spec := loadSpec(t)
	tests := []struct {
		name       string
		path       string
		readiness  error
		wantStatus int
	}{
		{"healthz", "/healthz", nil, nethttp.StatusOK},
		{"readyz ready", "/readyz", nil, nethttp.StatusOK},
		{"readyz not ready", "/readyz", errors.New("db down"), nethttp.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, _ := newTestServer(t, fakeReadiness{err: tt.readiness})
			resp, body := doRequest(t, app, httptest.NewRequest(nethttp.MethodGet, tt.path, nil))
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}

			op, ok := spec.Paths[tt.path]["get"]
			if !ok {
				t.Fatalf("GET %s is not documented", tt.path)
			}
			declared, ok := op.Responses[strconv.Itoa(resp.StatusCode)]
			if !ok || declared.Schema == nil {
				t.Fatalf("GET %s returned %d which is not documented", tt.path, resp.StatusCode)
			}
			def, ok := spec.Definitions[strings.TrimPrefix(declared.Schema.Ref, "#/definitions/")]
			if !ok {
				t.Fatalf("schema %q not found in definitions", declared.Schema.Ref)
			}

			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("response is not JSON: %v", err)
			}
			for prop := range def.Properties {
				if _, ok := got[prop]; !ok {
					t.Errorf("response missing documented property %q: %s", prop, body)
				}
			}
			for prop := range got {
				if _, ok := def.Properties[prop]; !ok {
					t.Errorf("response has undocumented property %q", prop)
				}
			}
		})
	}
}
```

- [ ] **Step 13: รัน test ทั้งหมดให้ผ่าน**

Run: `make test`
Expected: ทุก package `ok` (integration tests แสดง SKIP)

- [ ] **Step 14: ตรวจใน browser จริง**

รัน API แบบ Task 5 Step 8 แล้วเปิด `http://localhost:8080/docs`
Expected: redirect ไป `/docs/`, เห็นกลุ่ม Health สอง operations, ใน DevTools > Network ทุกไฟล์โหลดจาก `localhost` เท่านั้น, Console ไม่มี CSP error, กด Try it out ที่ `/healthz` แล้วได้ 200 จากนั้นรันใหม่ด้วย `DOCS_TRY_IT_OUT_ENABLED=false` แล้วปุ่ม Try it out ต้องหายไป

- [ ] **Step 15: Commit**

```bash
go mod tidy
git add Makefile go.mod go.sum scripts internal/adapter/http docs/api cmd/api
git commit -m "feat: serve generated Swagger spec and offline Swagger UI"
```

---

### Task 7: DB bootstrap, goose baseline และ role checks

**Files:**
- Create: `deploy/db/bootstrap.sql`
- Create: `deploy/local/initdb/10-bootstrap.sh`
- Create: `migrations/00001_baseline.sql`
- Modify: `Makefile`
- Test: `internal/adapter/postgres/bootstrap_test.go`

**Interfaces:**
- Consumes: `integrationURL` (Task 5); Make variables `GOOSE`, `GOOSE_TAGS` (Task 6)
- Produces:
  - roles `app_migrator`, `app_runtime`, `app_maintenance` และ schema `app` (owner `app_migrator`)
  - psql variables ของ bootstrap: `migrator_password`, `runtime_password`, `maintenance_password`
  - Make targets `migrate-up`, `migrate-status`, `test-integration`
  - env ที่ tests ใช้: `TEST_ADMIN_DATABASE_URL`, `TEST_RUNTIME_DATABASE_URL`, `REQUIRE_INTEGRATION`

- [ ] **Step 1: เขียน `deploy/db/bootstrap.sql`**

```sql
-- Bootstraps roles and the app schema before goose runs for the first time.
-- Local Docker and CI only. Supabase gets its own runbook in milestone 11:
-- do not run this file against Supabase as-is.
--
-- Usage (as a superuser):
--   psql "$ADMIN_DATABASE_URL" -v ON_ERROR_STOP=1 \
--     -v migrator_password=... -v runtime_password=... -v maintenance_password=... \
--     -f deploy/db/bootstrap.sql

\set ON_ERROR_STOP on

CREATE ROLE app_migrator LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD :'migrator_password';
CREATE ROLE app_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD :'runtime_password';
CREATE ROLE app_maintenance LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD :'maintenance_password';

SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', current_database()) \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO app_migrator, app_runtime, app_maintenance', current_database()) \gexec

REVOKE CREATE ON SCHEMA public FROM PUBLIC;

CREATE SCHEMA app AUTHORIZATION app_migrator;
REVOKE ALL ON SCHEMA app FROM PUBLIC;
GRANT USAGE ON SCHEMA app TO app_runtime, app_maintenance;
```

- [ ] **Step 2: เขียน `deploy/local/initdb/10-bootstrap.sh`**

```sh
#!/bin/sh
# Run by the postgres image only when the data volume is empty.
set -eu
psql -v ON_ERROR_STOP=1 \
  --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v migrator_password="$APP_MIGRATOR_PASSWORD" \
  -v runtime_password="$APP_RUNTIME_PASSWORD" \
  -v maintenance_password="$APP_MAINTENANCE_PASSWORD" \
  -f /bootstrap/bootstrap.sql
```

```bash
chmod +x deploy/local/initdb/10-bootstrap.sh
```

- [ ] **Step 3: สร้าง baseline migration**

```bash
mkdir -p migrations
./bin/goose -dir migrations -s create baseline sql
```

Expected: ได้ไฟล์ `migrations/00001_baseline.sql` แล้วแทนที่เนื้อหาด้วย:

```sql
-- +goose Up
COMMENT ON SCHEMA app IS 'Stock portfolio tracker application schema (managed by goose)';

-- +goose Down
COMMENT ON SCHEMA app IS NULL;
```

`COMMENT ON SCHEMA` ทำได้เฉพาะเจ้าของ schema จึงพิสูจน์ด้วยว่า migration รันด้วย `app_migrator` ที่เป็น owner

- [ ] **Step 4: เขียน failing integration test**

`internal/adapter/postgres/bootstrap_test.go`:

```go
package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const insufficientPrivilege = "42501"

func openPool(t *testing.T, key string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), integrationURL(t, key))
	if err != nil {
		t.Fatalf("connect %s: %v", key, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestBootstrapAndBaselineMigration(t *testing.T) {
	admin := openPool(t, "TEST_ADMIN_DATABASE_URL")
	ctx := t.Context()

	var appTable, publicTable *string
	if err := admin.QueryRow(ctx,
		`SELECT to_regclass('app.goose_db_version')::text, to_regclass('public.goose_db_version')::text`,
	).Scan(&appTable, &publicTable); err != nil {
		t.Fatalf("query goose tables: %v", err)
	}
	if appTable == nil {
		t.Error("app.goose_db_version does not exist: goose did not use the schema-qualified table")
	}
	if publicTable != nil {
		t.Error("public.goose_db_version exists: nothing may be created in public")
	}

	var schemaOwner string
	if err := admin.QueryRow(ctx,
		`SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'app'`,
	).Scan(&schemaOwner); err != nil {
		t.Fatalf("query schema owner: %v", err)
	}
	if schemaOwner != "app_migrator" {
		t.Errorf("schema app owner = %q, want app_migrator", schemaOwner)
	}

	var baselineApplied bool
	if err := admin.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM app.goose_db_version WHERE version_id = 1 AND is_applied)`,
	).Scan(&baselineApplied); err != nil {
		t.Fatalf("query baseline version: %v", err)
	}
	if !baselineApplied {
		t.Error("baseline migration 00001 is not recorded as applied")
	}

	var publicObjects int
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public'
		  AND pg_get_userbyid(c.relowner) IN ('app_migrator', 'app_runtime', 'app_maintenance')`,
	).Scan(&publicObjects); err != nil {
		t.Fatalf("query public objects: %v", err)
	}
	if publicObjects != 0 {
		t.Errorf("app roles own %d objects in public, want 0", publicObjects)
	}

	for _, role := range []string{"app_runtime", "app_maintenance"} {
		var super, bypass, createDB, createRole bool
		if err := admin.QueryRow(ctx,
			`SELECT rolsuper, rolbypassrls, rolcreatedb, rolcreaterole FROM pg_roles WHERE rolname = $1`, role,
		).Scan(&super, &bypass, &createDB, &createRole); err != nil {
			t.Fatalf("query role %s: %v", role, err)
		}
		if super || bypass || createDB || createRole {
			t.Errorf("%s has elevated attributes: super=%v bypassrls=%v createdb=%v createrole=%v",
				role, super, bypass, createDB, createRole)
		}
	}
}

func TestRuntimeRoleCannotChangeSchema(t *testing.T) {
	runtime := openPool(t, "TEST_RUNTIME_DATABASE_URL")
	statements := map[string]string{
		"create in app":      `CREATE TABLE app.should_not_exist (id int)`,
		"create in public":   `CREATE TABLE public.should_not_exist (id int)`,
		"read goose history": `SELECT count(*) FROM app.goose_db_version`,
	}
	for name, sql := range statements {
		t.Run(name, func(t *testing.T) {
			_, err := runtime.Exec(t.Context(), sql)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != insufficientPrivilege {
				t.Fatalf("Exec(%q) error = %v, want SQLSTATE %s", sql, err, insufficientPrivilege)
			}
		})
	}
}
```

- [ ] **Step 5: เพิ่ม Makefile targets ของ database**

ต่อท้าย `Makefile`:

```make
POSTGRES_HOST_PORT ?= 55432
LOCAL_DB_HOST := 127.0.0.1:$(POSTGRES_HOST_PORT)
MIGRATION_DATABASE_URL ?= postgres://app_migrator:$(APP_MIGRATOR_PASSWORD)@$(LOCAL_DB_HOST)/$(POSTGRES_DB)?sslmode=disable
TEST_ADMIN_DATABASE_URL ?= postgres://$(POSTGRES_ADMIN_USER):$(POSTGRES_ADMIN_PASSWORD)@$(LOCAL_DB_HOST)/$(POSTGRES_DB)?sslmode=disable
TEST_RUNTIME_DATABASE_URL ?= postgres://app_runtime:$(APP_RUNTIME_PASSWORD)@$(LOCAL_DB_HOST)/$(POSTGRES_DB)?sslmode=disable

.PHONY: migrate-up migrate-status test-integration

migrate-up:
	$(GOOSE) -dir migrations -table app.goose_db_version postgres "$(MIGRATION_DATABASE_URL)" up

migrate-status:
	$(GOOSE) -dir migrations -table app.goose_db_version postgres "$(MIGRATION_DATABASE_URL)" status

test-integration:
	REQUIRE_INTEGRATION=1 \
	TEST_ADMIN_DATABASE_URL="$(TEST_ADMIN_DATABASE_URL)" \
	TEST_RUNTIME_DATABASE_URL="$(TEST_RUNTIME_DATABASE_URL)" \
	go test -race -count=1 ./internal/adapter/postgres/...
```

ค่า `?=` ทำให้ CI ส่ง URL ของตัวเองผ่าน env ได้ ส่วนในเครื่องจะใช้ค่าจาก `.env` (Task 8)

- [ ] **Step 6: ทดสอบกับ PostgreSQL ชั่วคราว**

```bash
docker run -d --name stock-pg-test -e POSTGRES_PASSWORD=admin -e POSTGRES_DB=stock \
  -p 127.0.0.1:55432:5432 postgres:17.11-bookworm
until docker exec stock-pg-test pg_isready -U postgres -d stock; do sleep 1; done
psql "postgres://postgres:admin@127.0.0.1:55432/stock?sslmode=disable" -v ON_ERROR_STOP=1 \
  -v migrator_password=m -v runtime_password=r -v maintenance_password=x \
  -f deploy/db/bootstrap.sql
make migrate-up MIGRATION_DATABASE_URL='postgres://app_migrator:m@127.0.0.1:55432/stock?sslmode=disable'
make test-integration \
  TEST_ADMIN_DATABASE_URL='postgres://postgres:admin@127.0.0.1:55432/stock?sslmode=disable' \
  TEST_RUNTIME_DATABASE_URL='postgres://app_runtime:r@127.0.0.1:55432/stock?sslmode=disable'
```

Expected: `goose` แสดง `OK   00001_baseline.sql` และ `goose: successfully migrated database to version: 1`; integration tests `ok` โดยไม่มี SKIP

ถ้า goose error ว่าหา/สร้าง `app.goose_db_version` ไม่ได้ หรือ test แจ้งว่า table อยู่ใน `public` แปลว่า goose v3.28.0 ไม่รองรับชื่อ table แบบมี schema — **หยุดและแจ้งผู้ใช้** พร้อมข้อเสนอสำรอง: `ALTER ROLE app_migrator IN DATABASE stock SET search_path = app` แล้วใช้ชื่อ table ปกติ ซึ่งต้องแก้ spec และ ADR-0004 ก่อนทำต่อ (ตามข้อกำหนดใน spec หัวข้อ 7)

ลบ container หลังตรวจเสร็จ:

```bash
docker rm -f stock-pg-test
```

- [ ] **Step 7: Commit**

```bash
git add deploy migrations internal/adapter/postgres Makefile
git commit -m "feat: bootstrap database roles and goose baseline in app schema"
```

---

### Task 8: Docker image และ Compose stack

**Files:**
- Create: `Dockerfile`, `.dockerignore`
- Create: `docker-compose.yml`, `docker-compose.override.yml`
- Create: `.air.toml`, `.env.example`

**Interfaces:**
- Consumes: binary `api` + subcommand `healthcheck` (Task 5); `deploy/db/bootstrap.sql`, `deploy/local/initdb/10-bootstrap.sh`, `migrations/` (Task 7); env names จาก `config.Load` (Task 1)
- Produces:
  - Docker targets `dev`, `migrate`, `runtime` (default)
  - Compose services `postgres`, `migrate`, `api`, `mailpit`; image `stock-portfolio-api:local`
  - `.env` keys: `APP_ENV`, `PUBLIC_ORIGIN`, `LOG_LEVEL`, `DOCS_ENABLED`, `DOCS_TRY_IT_OUT_ENABLED`, `POSTGRES_DB`, `POSTGRES_ADMIN_USER`, `POSTGRES_ADMIN_PASSWORD`, `POSTGRES_HOST_PORT`, `APP_MIGRATOR_PASSWORD`, `APP_RUNTIME_PASSWORD`, `APP_MAINTENANCE_PASSWORD`

- [ ] **Step 1: หาเวอร์ชันและ digest ของ images/tools ที่ยังไม่มีใน spec**

```bash
docker buildx imagetools inspect golang:1.27.1-bookworm | head -3
docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot | head -3
docker buildx imagetools inspect postgres:17.11-bookworm | head -3
go list -m -versions github.com/air-verse/air | tr ' ' '\n' | tail -1
```

แล้วเปิด https://github.com/axllent/mailpit/releases เพื่อเลือก release ล่าสุดที่ไม่ใช่ pre-release และรัน `docker buildx imagetools inspect axllent/mailpit:<tag ที่เลือก> | head -3`

Expected: แต่ละ image แสดง `Digest: sha256:...` และได้ tag ของ air (เช่น `v1.xx.y`)
จดค่าทั้งหมดไว้ใช้ใน Step 2–4: ทุก `FROM`/`image:` ต้องเป็นรูป `name:tag@sha256:<digest>` และ air ใช้ tag ที่ได้ ห้ามใช้ `latest`

- [ ] **Step 2: เขียน `Dockerfile`**

ใส่ digest จาก Step 1 ต่อท้าย tag ของทุก `FROM` และใส่ tag ของ air ใน `ARG AIR_VERSION`:

```dockerfile
# syntax=docker/dockerfile:1

FROM golang:1.27.1-bookworm AS base
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Dev: source is bind-mounted by docker-compose.override.yml and rebuilt by air.
FROM base AS dev
ARG AIR_VERSION
RUN go install github.com/air-verse/air@${AIR_VERSION}
CMD ["air", "-c", ".air.toml"]

FROM base AS build
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM base AS goose-build
ARG GOOSE_VERSION=v3.28.0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go install \
      -tags 'no_clickhouse no_duckdb no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb' \
      github.com/pressly/goose/v3/cmd/goose@${GOOSE_VERSION}

FROM gcr.io/distroless/static-debian12:nonroot AS migrate
COPY --from=goose-build /go/bin/goose /usr/local/bin/goose
COPY migrations /migrations
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/goose"]
CMD ["-dir", "/migrations", "-table", "app.goose_db_version", "up"]

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/api /usr/local/bin/api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]
```

`.dockerignore`:

```gitignore
.git
.github
.env
bin
tmp
web
docs/superpowers
docs/adr
docs/database
**/*.md
docker-compose*.yml
```

- [ ] **Step 3: เขียน `.env.example`, `docker-compose.yml` และ `docker-compose.override.yml`**

`.env.example`:

```dotenv
# Local development only. Production never uses these values or this compose file.
APP_ENV=development
PUBLIC_ORIGIN=http://localhost:8080
LOG_LEVEL=info
DOCS_ENABLED=true
DOCS_TRY_IT_OUT_ENABLED=true

POSTGRES_DB=stock
POSTGRES_ADMIN_USER=postgres
POSTGRES_ADMIN_PASSWORD=dev_admin_password
POSTGRES_HOST_PORT=55432

# Letters, digits and underscores only: these are embedded in connection URLs unescaped.
APP_MIGRATOR_PASSWORD=dev_migrator_password
APP_RUNTIME_PASSWORD=dev_runtime_password
APP_MAINTENANCE_PASSWORD=dev_maintenance_password
```

`docker-compose.yml` (ใส่ digest จาก Step 1 ใน `image:` ของ postgres และ mailpit):

```yaml
name: stock-portfolio

services:
  postgres:
    image: postgres:17.11-bookworm
    environment:
      POSTGRES_USER: ${POSTGRES_ADMIN_USER}
      POSTGRES_PASSWORD: ${POSTGRES_ADMIN_PASSWORD}
      POSTGRES_DB: ${POSTGRES_DB}
      APP_MIGRATOR_PASSWORD: ${APP_MIGRATOR_PASSWORD}
      APP_RUNTIME_PASSWORD: ${APP_RUNTIME_PASSWORD}
      APP_MAINTENANCE_PASSWORD: ${APP_MAINTENANCE_PASSWORD}
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ./deploy/local/initdb:/docker-entrypoint-initdb.d:ro
      - ./deploy/db:/bootstrap:ro
    ports:
      - "127.0.0.1:${POSTGRES_HOST_PORT:-55432}:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U $${POSTGRES_USER} -d $${POSTGRES_DB}"]
      interval: 5s
      timeout: 5s
      retries: 20

  migrate:
    build:
      context: .
      target: migrate
    environment:
      GOOSE_DRIVER: postgres
      GOOSE_DBSTRING: postgres://app_migrator:${APP_MIGRATOR_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable
    depends_on:
      postgres:
        condition: service_healthy
    restart: "no"

  api:
    build:
      context: .
      target: runtime
    image: stock-portfolio-api:local
    environment:
      APP_ENV: ${APP_ENV}
      HTTP_ADDR: ":8080"
      PUBLIC_ORIGIN: ${PUBLIC_ORIGIN}
      DATABASE_URL: postgres://app_runtime:${APP_RUNTIME_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable
      LOG_LEVEL: ${LOG_LEVEL}
      DOCS_ENABLED: ${DOCS_ENABLED}
      DOCS_TRY_IT_OUT_ENABLED: ${DOCS_TRY_IT_OUT_ENABLED}
      SHUTDOWN_TIMEOUT: 10s
    ports:
      - "127.0.0.1:8080:8080"
    depends_on:
      migrate:
        condition: service_completed_successfully
    healthcheck:
      test: ["CMD", "/usr/local/bin/api", "healthcheck"]
      interval: 10s
      timeout: 3s
      retries: 3
      start_period: 5s
    stop_grace_period: 15s
    restart: unless-stopped

  mailpit:
    image: axllent/mailpit
    ports:
      - "127.0.0.1:8025:8025"
      - "127.0.0.1:1025:1025"

volumes:
  pgdata:
```

`docker-compose.override.yml` (ใส่ tag ของ air จาก Step 1):

```yaml
# Loaded automatically by `docker compose up`. Development only: source mount + hot reload.
services:
  api:
    build:
      context: .
      target: dev
      args:
        AIR_VERSION: ""
    image: stock-portfolio-api:dev
    working_dir: /src
    volumes:
      - .:/src
      - gomodcache:/go/pkg/mod
    healthcheck:
      test: ["CMD", "/src/tmp/api", "healthcheck"]
      start_period: 60s

volumes:
  gomodcache:
```

แก้ `AIR_VERSION: ""` เป็น tag ที่ได้จาก Step 1 ก่อนบันทึก และแก้ `image: axllent/mailpit` ใน `docker-compose.yml` เป็น `axllent/mailpit:<tag>@sha256:<digest>` ที่ได้จาก Step 1

- [ ] **Step 4: เขียน `.air.toml`**

```toml
root = "."
tmp_dir = "tmp"

[build]
  cmd = "go build -o ./tmp/api ./cmd/api"
  bin = "./tmp/api"
  include_ext = ["go", "json", "yaml", "html", "js", "css"]
  exclude_dir = ["tmp", "bin", "web", "docs/superpowers", "docs/adr", "docs/database", "migrations"]
  delay = 500
  kill_delay = "2s"
  send_interrupt = true

[log]
  time = false
```

- [ ] **Step 5: เปิด stack แบบ production-like (ไม่โหลด override)**

```bash
cp .env.example .env
docker compose -f docker-compose.yml up --build -d
docker compose -f docker-compose.yml ps
```

Expected: `migrate` สถานะ `exited (0)`, `api` สถานะ `healthy`, `postgres` `healthy`, `mailpit` `running`

- [ ] **Step 6: ตรวจ endpoints, logs และ non-root**

```bash
curl -i http://localhost:8080/healthz
curl -i http://localhost:8080/readyz
curl -sI http://localhost:8080/docs/ | head -1
docker compose -f docker-compose.yml logs api | tail -5
docker image inspect stock-portfolio-api:local --format '{{.Config.User}}'
```

Expected: `/healthz` 200, `/readyz` 200 `{"status":"ready"}`, `/docs/` `HTTP/1.1 200`, logs เป็น JSON บรรทัดละ record มี `"service":"stock-api"` และ `"event":"http.access"`, user เป็น `nonroot:nonroot`; เปิด http://localhost:8025 แล้วเห็นหน้า Mailpit

- [ ] **Step 7: ตรวจ graceful shutdown**

```bash
docker compose -f docker-compose.yml stop api
docker compose -f docker-compose.yml logs api | tail -3
```

Expected: มี log `"event":"server.stop"` ตามด้วย `"event":"server.stopped"`

- [ ] **Step 8: ตรวจว่า migration ที่ล้มเหลวทำให้ API ไม่เริ่ม**

```bash
docker compose -f docker-compose.yml down
printf -- '-- +goose Up\nSELECT * FROM app.table_that_does_not_exist;\n\n-- +goose Down\nSELECT 1;\n' > migrations/00002_broken_check.sql
docker compose -f docker-compose.yml up --build -d; echo "exit=$?"
docker compose -f docker-compose.yml ps -a
```

Expected: `docker compose up` คืน exit ไม่เป็น 0, `migrate` สถานะ `exited (1)`, `api` ไม่ได้ถูกเริ่ม (ไม่อยู่ในสถานะ running) และ `docker compose -f docker-compose.yml logs migrate` มีข้อความว่า relation ไม่มีอยู่

ล้างกลับ:

```bash
rm migrations/00002_broken_check.sql
docker compose -f docker-compose.yml down -v
```

- [ ] **Step 9: ตรวจ dev mode พร้อม hot reload**

```bash
docker compose up --build -d
docker compose logs -f api
```

แก้ข้อความ `"server started"` ใน `cmd/api/main.go` ชั่วคราว แล้วบันทึกไฟล์
Expected: log ของ air แสดงการ build ใหม่และ API เริ่มใหม่ภายในไม่กี่วินาที จากนั้นแก้ข้อความกลับเป็นเดิม แล้วรัน `docker compose down`

- [ ] **Step 10: Commit**

```bash
git add Dockerfile .dockerignore docker-compose.yml docker-compose.override.yml .air.toml .env.example
git commit -m "feat: add multi-stage Dockerfile and local compose stack"
```

---

### Task 9: CI, lint และ README

**Files:**
- Create: `.golangci.yml`
- Create: `.github/workflows/ci.yml`
- Create: `README.md`
- Modify: `Makefile`

**Interfaces:**
- Consumes: Make targets `tools`, `swagger`, `migrate-up`, `test` (Tasks 1, 6, 7); `deploy/db/bootstrap.sql` (Task 7); Docker targets `runtime`, `migrate` (Task 8)
- Produces: workflow `CI` job `backend`; Make targets `lint`, `check`

- [ ] **Step 1: เขียน `.golangci.yml`**

```yaml
version: "2"

run:
  timeout: 5m

linters:
  default: standard
  enable:
    - bodyclose
    - errorlint
    - gosec
    - misspell
    - noctx
  exclusions:
    presets:
      - std-error-handling
      - common-false-positives
    paths:
      - docs/api
    rules:
      - path: _test\.go
        linters:
          - gosec

formatters:
  enable:
    - gofmt
    - goimports
  settings:
    goimports:
      local-prefixes:
        - go-backend-stock
```

- [ ] **Step 2: เพิ่ม `lint` และ `check` ใน Makefile แล้วรัน**

ต่อท้าย `Makefile`:

```make
.PHONY: lint-install lint check

lint-install:
	GOBIN=$(BIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

lint:
	$(GOLANGCI_LINT) run ./...

check: tidy-check vet lint swagger test
	git diff --exit-code -- docs/api
```

```bash
make lint-install
make check
```

Expected: lint ไม่มี issue, swagger valid, tests ผ่าน และ `git diff` ของ `docs/api` ว่าง ถ้า lint พบ issue ให้แก้โค้ดตามข้อความ ห้ามเพิ่ม `//nolint` โดยไม่มีเหตุผลกำกับ

- [ ] **Step 3: ตรวจเวอร์ชัน GitHub Actions**

เปิดหน้า releases ของ `actions/checkout`, `actions/setup-go` และ `golangci/golangci-lint-action`
Expected: ได้ major version ล่าสุดของแต่ละตัว ใช้ค่าเหล่านั้นแทน `@v5`, `@v6`, `@v8` ใน Step 4 ถ้าต่างกัน และ golangci-lint-action ต้องรองรับ golangci-lint v2

- [ ] **Step 4: เขียน `.github/workflows/ci.yml`**

```yaml
name: CI

on:
  pull_request:
  push:
    branches: [main]

permissions:
  contents: read

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true

jobs:
  backend:
    runs-on: ubuntu-24.04
    timeout-minutes: 20
    services:
      postgres:
        image: postgres:17.11-bookworm
        env:
          POSTGRES_USER: postgres
          POSTGRES_PASSWORD: ci_admin_password
          POSTGRES_DB: stock
        ports:
          - 5432:5432
        options: >-
          --health-cmd "pg_isready -U postgres -d stock"
          --health-interval 5s
          --health-timeout 5s
          --health-retries 20
    env:
      ADMIN_DATABASE_URL: postgres://postgres:ci_admin_password@localhost:5432/stock?sslmode=disable
      MIGRATION_DATABASE_URL: postgres://app_migrator:ci_migrator_password@localhost:5432/stock?sslmode=disable
      TEST_ADMIN_DATABASE_URL: postgres://postgres:ci_admin_password@localhost:5432/stock?sslmode=disable
      TEST_RUNTIME_DATABASE_URL: postgres://app_runtime:ci_runtime_password@localhost:5432/stock?sslmode=disable
      REQUIRE_INTEGRATION: "1"
    steps:
      - uses: actions/checkout@v5

      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true

      - name: go.mod is tidy
        run: go mod tidy -diff

      - name: Install pinned tools
        run: make tools

      - name: go vet
        run: go vet ./...

      - name: golangci-lint
        uses: golangci/golangci-lint-action@v8
        with:
          version: v2.14.0

      - name: Swagger is regenerated, valid and committed
        run: |
          make swagger
          git diff --exit-code -- docs/api

      - name: Bootstrap database roles
        run: |
          psql "$ADMIN_DATABASE_URL" -v ON_ERROR_STOP=1 \
            -v migrator_password=ci_migrator_password \
            -v runtime_password=ci_runtime_password \
            -v maintenance_password=ci_maintenance_password \
            -f deploy/db/bootstrap.sql

      - name: Migrations apply to an empty database
        run: make migrate-up

      - name: Unit, HTTP, contract and integration tests
        run: go test -race -count=1 ./...

      - name: Build API image
        run: docker build --target runtime -t stock-portfolio-api:ci .

      - name: Build migrate image
        run: docker build --target migrate -t stock-portfolio-migrate:ci .
```

แก้ `image: postgres:17.11-bookworm` ของ service container ให้มี digest เดียวกับ Task 8 Step 1 (`postgres:17.11-bookworm@sha256:<digest>`) และแทน tag ของ actions ด้วย commit SHA ของ release ที่เลือกใน Step 3 พร้อม comment ชื่อเวอร์ชันท้ายบรรทัด

CI ใช้ PostgreSQL ใน service container เท่านั้น ไม่มี production credentials และไม่มี web jobs

- [ ] **Step 5: เขียน `README.md`**

````markdown
# Stock Portfolio Tracker — Backend

Go API สำหรับบันทึกรายการซื้อขายหุ้นสหรัฐฯ และติดตามต้นทุน/กำไรขาดทุนของหลายพอร์ต
ระบบนี้บันทึกการลงทุนที่ทำผ่านช่องทางอื่น ไม่ใช่ระบบส่งคำสั่งซื้อขายจริง

> **สถานะ:** Milestone 1 — มีเฉพาะ `/healthz`, `/readyz` และเอกสาร API; auth, พอร์ต และรายการซื้อขายยังไม่เปิดใช้งาน

## เริ่มใช้งาน (dev พร้อม hot reload)

```bash
cp .env.example .env
docker compose up --build
```

`docker compose up` โหลด `docker-compose.override.yml` อัตโนมัติ: mount source และ rebuild ด้วย air

## เปิดแบบ production-like ในเครื่อง (ไม่โหลด dev override)

```bash
docker compose -f docker-compose.yml up --build
```

ใช้ image ที่ build แล้วและรันเป็น non-root; production จริงใช้ Supabase และ deployment config แยก (milestone 11)

## URLs

| URL | ใช้ทำอะไร |
| --- | --- |
| http://localhost:8080/healthz | process ทำงานอยู่ |
| http://localhost:8080/readyz | เชื่อม database ได้ |
| http://localhost:8080/docs/ | Swagger UI (bundle ใน image ไม่โหลด CDN) |
| http://localhost:8080/swagger.json, /swagger.yaml | Swagger 2.0 spec |
| http://localhost:8025 | Mailpit กล่องจดหมายปลอมสำหรับ dev |

ทุก response มี `X-Request-ID` ใช้ค้น log ของคำขอนั้นได้

## Application log

```bash
docker compose logs -f api
```

Log เป็น JSON บรรทัดละ record (`time` UTC, `level`, `service`, `msg`, `event`, `request_id`)
Access log เก็บ method, route template, status และ duration โดยไม่เก็บ query string, cookies, headers หรือ body

## Database

- `deploy/db/bootstrap.sql` สร้าง roles `app_migrator` (เจ้าของ schema `app`), `app_runtime` และ `app_maintenance` ตอน volume ว่าง
- service `migrate` รัน goose ด้วย `app_migrator` และเก็บประวัติใน `app.goose_db_version`; `api` จะเริ่มเมื่อ migrate สำเร็จเท่านั้น
- PostgreSQL เปิดให้เครื่อง host ที่ `127.0.0.1:55432` (เปลี่ยนได้ด้วย `POSTGRES_HOST_PORT`)
- ล้างข้อมูลทั้งหมดแล้วเริ่มใหม่: `docker compose down -v`

## พัฒนาและทดสอบ

```bash
make tools            # ติดตั้ง swag, go-swagger, goose ตามเวอร์ชันที่ pin ลง ./bin
make lint-install     # ติดตั้ง golangci-lint ลง ./bin
make test             # unit/HTTP/contract tests (integration tests จะ skip)
make test-integration # integration tests กับ PostgreSQL ใน compose (ต้องเปิด stack ก่อน)
make swagger          # generate docs/api จาก annotations แล้ว validate
make check            # tidy + vet + lint + swagger + tests + ตรวจว่า docs/api commit แล้ว
```

ห้ามแก้ไฟล์ใน `docs/api/` ที่ generate แล้วด้วยมือ ให้แก้ annotations แล้วรัน `make swagger`

## เอกสารออกแบบ

- [AGENTS.md](AGENTS.md) — ข้อกำหนดของโปรเจกต์
- [Design spec](docs/superpowers/specs/2026-10-05-phase-1-design.md) และ [roadmap](docs/superpowers/plans/2026-10-05-phase-1-roadmap.md)
- [ADRs](docs/adr/README.md) — เหตุผลของการตัดสินใจสำคัญ
- [ER diagram (DBML)](docs/database/schema.dbml)
````

- [ ] **Step 6: ตรวจ workflow syntax ในเครื่อง**

```bash
docker run --rm -v "$PWD":/repo -w /repo rhysd/actionlint:latest -color
```

Expected: ไม่มี error (คำสั่งนี้ใช้ตรวจในเครื่องเท่านั้น ไม่ได้ commit จึงใช้ tag `latest` ได้)

- [ ] **Step 7: Commit**

```bash
git add .golangci.yml .github README.md Makefile
git commit -m "ci: add backend CI with lint, swagger checks, migrations and image builds"
```

- [ ] **Step 8: อัปเดต roadmap**

ใน `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md` milestone 1 ติ๊กรายการที่ทำเสร็จ และระบุว่า CI บน GitHub จะตรวจจริงเมื่อสร้าง remote repository แล้ว จากนั้น commit:

```bash
git add docs/superpowers/plans/2026-10-05-phase-1-roadmap.md
git commit -m "docs: mark milestone 1 tasks complete"
```

---

## เกณฑ์ผ่าน milestone 1

- `make check` ผ่านในเครื่อง และ `make test-integration` ผ่านกับ compose stack
- `docker compose -f docker-compose.yml up --build` เปิด postgres → migrate (exit 0) → api (healthy) → mailpit ได้
- broken migration ทำให้ API ไม่เริ่ม (Task 8 Step 8)
- `/docs/` โหลดจาก origin เดียวโดยไม่มี CSP error และ Try it out เปิด/ปิดตาม config
- `app.goose_db_version` อยู่ใน schema `app` และไม่มี object ของ app roles ใน `public`
- ยังไม่อ้างว่า auth/พอร์ตใช้งานได้; CI บน GitHub จะยืนยันเมื่อมี remote repository
