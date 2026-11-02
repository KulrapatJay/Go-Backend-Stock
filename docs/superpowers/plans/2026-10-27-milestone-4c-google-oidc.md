# Milestone 4c — Google OpenID Connect (login และเชื่อมบัญชี) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** เพิ่ม Google sign-in ผ่าน OpenID Connect Authorization Code Flow + PKCE ทั้ง login, สร้างบัญชีใหม่จาก Google และเชื่อม Google กับบัญชีที่ login อยู่ (`GET /auth/config`, `POST /auth/google/start`, `POST /auth/google/link`, `GET /auth/google/callback`) โดยปิดอย่างชัดเจนเมื่อ config ไม่ครบ และทดสอบได้ครบกับ OIDC server ปลอมโดยไม่เรียก Google จริง

**Architecture:** core ได้ชนิดข้อมูลของ flow (`domain/oidc.go`), port `IdentityProvider` และ use case `StartGoogle`/`CompleteGoogle` ใน `service.Auth`; state, nonce และ browser binding สุ่มใน service แล้วเก็บเฉพาะ hash ใน `app.oauth_flows` ส่วน PKCE verifier คำนวณใหม่ด้วย HMAC จาก `OIDC_SECRET` จึงไม่ต้องเก็บ adapter `internal/adapter/identity` ใช้ `go-oidc` + `oauth2` ทำ discovery แบบ lazy, แลก code และตรวจลายเซ็น/issuer/audience/expiry ของ ID token; HTTP adapter ตั้ง cookie binding และ redirect 303 กลับไปที่ `/docs/?google=<outcome>`; ประกอบทุกอย่างใน `cmd/api`

**Tech Stack:** Go 1.27 (go.mod `go 1.27.1`), Fiber v3.5.0, pgx v5.11.0, PostgreSQL 17.11, `github.com/coreos/go-oidc/v3` v3.21.0, `golang.org/x/oauth2` v0.37.0 (ดึง `github.com/go-jose/go-jose/v4` v4.1.4 ตาม go.mod ของ go-oidc), swaggo/swag v1.16.6 (Swagger 2.0), golangci-lint v2.14.0

**Spec:** [docs/superpowers/specs/2026-10-22-milestone-4-auth-design.md](../specs/2026-10-22-milestone-4-auth-design.md) หัวข้อ 1, 2, 3, 4.10, 6, 7 และ 8 ซึ่งอ้าง [phase 1 spec](../specs/2026-10-05-phase-1-design.md) หัวข้อ 8, 10 และ 11 กับ [AGENTS.md](../../../AGENTS.md) (หัวข้อ Auth, Session และ Google identity)

## Global Constraints

- core (`internal/core/**` รวม `_test.go`) import ได้เฉพาะ standard library, `github.com/shopspring/decimal`, `github.com/google/uuid`, `golang.org/x/crypto/argon2` และ `go-backend-stock/internal/core/...`; `go-oidc`, `oauth2` และ `go-jose` อยู่ใน `internal/adapter/identity` เท่านั้น (รวม package ช่วยทดสอบ `identitytest`)
- Libraries ใหม่ pin ตามนี้: `github.com/coreos/go-oidc/v3@v3.21.0` (ออก 2026-09-01) และ `golang.org/x/oauth2@v0.37.0` (ออก 2026-08-25); go-jose เป็นไปตาม MVS (v4.1.4 จาก go.mod ของ go-oidc) ห้ามอัปเกรดเป็น v4.1.6 ที่ออกไม่ถึง 2 สัปดาห์; license: go-oidc Apache-2.0, oauth2 BSD-3-Clause, go-jose Apache-2.0
- Endpoints (ใต้ `/api/v1`): `GET /auth/config` (public) → 200 `{google_enabled}`; `POST /auth/google/start` (session + CSRF, rate limit `google_start_ip` 20/ชม.) → 200 `{authorization_url}` + cookie binding; `POST /auth/google/link` (session → require auth → CSRF, rate limit เดียวกัน) → 200 `{authorization_url}` + cookie binding โดย flow ผูก user ปัจจุบัน; `GET /auth/google/callback?code&state` (session, ไม่มี app CSRF) → 303 `Location: /docs/?google=success|linked|link_required|failed`
- เมื่อ Google ปิด (config ว่าง): `GET /auth/config` ตอบ `{"google_enabled":false}` และ start/link/callback ตอบ 503 `feature_disabled`; route ยังลงทะเบียน (ไม่ใช่ 404)
- Start: สุ่ม state, nonce และ browser binding อย่างละ 32 bytes (base64url ไม่มี padding 43 ตัว) → `AuthorizationURL` ก่อน แล้วจึง insert `oauth_flows` (SHA-256 ของทั้งสามค่า, อายุ 10 นาที, purpose `login`/`link`, `user_id` เฉพาะ link) เพื่อไม่ให้เหลือแถวกำพร้าเมื่อ provider ล่ม → scope `openid email`, `code_challenge = base64url(SHA-256(verifier))`, `code_challenge_method=S256`
- PKCE: `verifier = base64url(HMAC-SHA-256(OIDC_SECRET, "pkce" ‖ 0x00 ‖ state ‖ 0x00 ‖ binding))` (43 ตัว) คำนวณใหม่ตอน callback ไม่เก็บใน DB หรือ cookie
- Cookie binding: `oidc_binding` (`__Host-oidc_binding` เมื่อ `PUBLIC_ORIGIN` เป็น https) HttpOnly, SameSite=Lax, Path=/, ไม่มี Domain, หมดอายุพร้อม flow; callback ลบ cookie นี้ทุกครั้ง
- Callback: ตรวจรูปแบบ state/binding → `OIDCFlows().Consume(stateHash, bindingHash, now)` (statement เดียว นอก transaction) → error จาก provider/ไม่มี code → link ต้องมี session ของ user ที่เริ่ม flow → `Exchange` นอก transaction → nonce ต้องตรงกับ hash ใน flow (constant-time) → login หรือ link
- Login: `BySubject(google, sub)` เจอ → transaction: lock user → insert session → audit `auth.login_succeeded` (method google); ไม่เจอ → ต้อง `email_verified` และ `ParseEmail` ผ่าน → transaction: insert user (ยืนยันแล้ว ไม่มีรหัสผ่าน) + identity + session + audit `auth.registered` และ `auth.login_succeeded` (method google); อีเมลชนบัญชีเดิม → ไม่ link อัตโนมัติ → audit `auth.login_failed` reason `link_required` → outcome `link_required`
- Link: transaction: lock user → `Identities().Insert`; ชน (sub ของคนอื่นหรือ user มี Google แล้ว) → audit `auth.identity_link_failed` reason `identity_conflict` ใน transaction เดิม; สำเร็จ → audit `auth.identity_linked`; ไม่สร้าง session ใหม่
- Audit ของ Google: event login ใช้ metadata `method: "google"`; event identity ใช้ `provider: "google"`; actor ของ login ที่ล้มเหลวเป็น NULL; actor ของ link เป็น user เฉพาะเมื่อ session ตรงกับ flow; failure audit เขียนใน transaction ของตัวเองและ target เป็น `oidc_flow` + ID ของ flow
- Reasons ที่ใช้: `provider_error`, `session_mismatch`, `exchange_rejected`, `nonce_mismatch`, `token_invalid`, `email_not_verified`, `email_invalid`, `link_required`, `identity_conflict`
- Callback ที่ไม่มี flow ตรง (รูปแบบผิด, ไม่รู้จัก, หมดอายุ, ใช้แล้ว, คนละ browser) → outcome `failed` โดย**ไม่เขียน audit** (มีแค่ application log ระดับ info `auth.oidc_flow_not_found`) เพราะ GET ที่ไม่ต้อง login ต้องไม่ทำให้ `audit_events` โตจาก traffic โจมตี
- provider ล่ม/timeout → 503 `dependency_unavailable` ทั้ง start และ callback; adapter ทำ discovery ครั้งแรกที่ใช้และ cache ไว้ ถ้าล้มเหลวให้ลองใหม่ครั้งถัดไป API ยังเริ่มได้; ทุกการเรียก provider มี timeout 10 วินาที
- ห้าม log หรือส่งออก code, state, nonce, binding, ID token, access token, client secret หรือ session token; ห้าม echo ค่า `error` ของ provider ใน response; ไม่มี return URL ที่ผู้ใช้กำหนดเอง
- Config: `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URI` ต้องครบทั้งสามหรือว่างทั้งสาม; ไม่ครบ → API ไม่เริ่มและบอกชื่อตัวที่ขาด (ไม่บอกค่า); `GOOGLE_REDIRECT_URI` ต้องเท่ากับ `PUBLIC_ORIGIN` + `/api/v1/auth/google/callback` พอดี
- Tests และ CI ไม่เรียก Google จริง: service/HTTP ใช้ fake ของ `port.IdentityProvider`; adapter และ test end-to-end ใช้ OIDC server ปลอมใน `httptest` (discovery, JWKS ด้วย RSA key ที่สร้างใน test, token endpoint ที่ตรวจ client credentials, code และ `code_verifier`)
- ห้ามแก้ `.env.example` และ `.env` (ผู้ใช้เป็นเจ้าของ); `docker-compose.yml` เพิ่ม `GOOGLE_*` แบบ `${VAR:-}` ได้
- ต้องเห็น test ล้มก่อนแล้วจึงเขียนโค้ดทุก task; จบ task ด้วย `gofmt -l` ที่ไม่พิมพ์อะไร, `go vet ./...` และ `make lint` ผ่าน
- task ที่แก้ annotation ของ handler หรือ DTO ต้องรัน `make swagger` แล้ว commit `docs/api/` ในชุดเดียวกัน ห้ามแก้ไฟล์ generated ด้วยมือ
- integration tests ใช้ PostgreSQL จริงผ่าน `make -s test-integration` เท่านั้น (`-s` กัน Makefile echo รหัสผ่าน DB) และต้องไม่ SKIP; ใช้อีเมล/IP/subject สุ่มต่อ test
- Docker: ใช้ได้เฉพาะ `docker compose ps`; ถ้า postgres ไม่ได้รันให้หยุดแล้วแจ้ง controller (ผู้ใช้เปิด stack เอง) ห้าม `docker compose up/down/stop/restart` และห้ามเรียก API ที่ผู้ใช้รันอยู่
- commit message ไม่มี `Co-Authored-By` หรือชื่อ Claude; commit ในเครื่องเท่านั้น ห้าม push; `git add` เฉพาะไฟล์ของ task ห้าม `git add -A`/`git add .`
- รันทุกคำสั่งที่ root ของ repo บน branch `feature/create_login_and_register_3`; `go` คือ `/usr/local/go/bin/go` (1.27.2)

## การตัดสินใจที่แผนนี้เสนอ (รอผู้ใช้ยืนยันตอนรีวิวแผน)

spec กำหนด flow หลักไว้แล้ว ข้อต่อไปนี้เป็นรายละเอียดที่ spec ไม่ได้ระบุหรือที่แผนปรับจากรายการ field ใน phase 1 spec หัวข้อ 8:

1. **callback ที่ไม่มี flow ตรงไม่เขียน audit** (ดู Global Constraints) — ทางเลือกอื่นคือ audit ทุกครั้ง ซึ่งเปิดให้คนนอกเติม `audit_events` ได้ไม่จำกัดผ่าน GET ที่ไม่ต้อง login
2. **service สุ่ม browser binding เอง** — `OIDCStart` จึงมีแค่ Purpose + Principal และ binding กลับมาใน `OIDCRedirect.BrowserBinding`; phase 1 spec เขียนว่า `OIDCStart` มี BrowserBinding แต่ design หัวข้อ 6 ให้สุ่มใน start
3. **redirect URI อยู่ใน adapter** — `AuthorizationRequest`/`CodeExchange` ไม่มี RedirectURI เพราะ core ไม่ต้องรู้; `AuthorizationURL` รับ `context.Context` เพิ่มเพราะ discovery แบบ lazy ต้องใช้ network
4. **ใส่ 0x00 คั่นใน input ของ PKCE HMAC** ให้เหมือน OTP/CSRF (state และ binding ยาวคงที่จึงไม่มีความกำกวมอยู่แล้ว)
5. **error ภายใน (DB/provider ล่ม, เขียน audit ไม่ได้) ตอบ JSON 5xx แทน redirect** เพื่อไม่ซ่อนการล่มเป็น `failed`; cookie binding ยังถูกลบ
6. **`GOOGLE_REDIRECT_URI` ต้องเป็น `PUBLIC_ORIGIN` + `/api/v1/auth/google/callback` พอดี** เพราะ cookie binding เป็นของ host นี้เท่านั้น ค่าอื่นทำให้ทุก callback ล้มเหลว
7. **บัญชี local ที่ยังไม่ยืนยันแต่อีเมลตรงกับ Google ได้ `link_required`** ตาม spec (ไม่ link อัตโนมัติ) แม้ผู้ใช้จะยืนยันอีเมลกับ Google แล้ว — สอดคล้องกับความเสี่ยงการสมัครชิงอีเมลที่ยอมรับไว้ใน roadmap (ทางแก้คือ forgot-password ใน milestone 5)
8. **สองแท็บสร้างบัญชี Google ใหม่พร้อมกัน**: แท็บที่ insert user ชนให้ตรวจ `BySubject` ใน transaction เดิมอีกครั้ง ถ้าแท็บแรกสร้าง identity เดียวกันแล้วให้ login เป็นบัญชีนั้นแทนการตอบ `link_required`

## Review Focus

1. **callback ซ้ำ (replay)** — กด back แล้ว callback เดิมถูกเรียกซ้ำ ต้องได้ `failed` โดยไม่สร้าง session ใหม่ ไม่เรียก provider ซ้ำ และไม่เขียน audit ซ้ำ — Task 6 `TestGoogleCallbackRejectsAReplay`
2. **callback จากคนละ browser** — ลิงก์ callback ถูกเปิดใน browser อื่น ต้อง `failed` และ flow ของ browser เดิมยังทำต่อได้ — Task 6 `TestGoogleCallbackNeedsTheBrowserThatStartedIt`
3. **สองแท็บสร้างบัญชี Google ใหม่พร้อมกัน** — ต้องได้บัญชีเดียว identity เดียว และทั้งสองแท็บ login ได้ ไม่มี 500 — Task 6 `TestGoogleSignInFromTwoTabsMakesOneAccount`
4. **link หลัง session ที่เริ่ม flow ถูก logout ไปแล้ว** — ต้อง `failed` reason `session_mismatch` และไม่สร้าง identity — Task 7 `TestGoogleLinkNeedsTheSessionThatStartedIt`
5. **Google ล่มตอน discovery แล้วกลับมา** — start ครั้งแรกได้ 503 และครั้งถัดไปทำงานโดยไม่ต้อง restart API — Task 4 `TestDiscoveryFailureIsUnavailableAndRetried`

---

## File Structure

| ไฟล์ | หน้าที่ | Task |
| --- | --- | --- |
| `internal/config/google.go` (+test), `config.go`, `redact.go` | `GoogleConfig`, ตรวจ `GOOGLE_*`, ไม่พิมพ์ secret | 1 |
| `internal/core/domain/oidc.go` (+test), `errors.go`, `internal/core/port/identity.go`, `port/repositories.go` | ชนิดข้อมูลของ flow, `ErrFeatureDisabled`/`ErrIdentityRejected`, port `IdentityProvider`, `Repositories.Identities/OIDCFlows` | 2 |
| `internal/core/service/oidc.go` (+test) | ค่าสุ่มของ flow, hash, PKCE verifier/challenge | 3 |
| `go.mod`, `go.sum`, `internal/adapter/identity/google.go` (+test), `internal/adapter/identity/identitytest/provider.go` | adapter `go-oidc` + OIDC server ปลอมสำหรับ test | 4 |
| `internal/core/port/auth_service.go`, `internal/core/service/auth.go`, `google.go` (+test), `internal/adapter/http/google.go`, `auth.go`, `auth_dto.go`, `cookies.go`, `auth_harness_test.go`, `google_fake_test.go`, `google_start_test.go`, `auth_contract_test.go`, `docs/api/` | `GoogleEnabled`, `StartGoogle`, `GET /auth/config`, start/link | 5 |
| `internal/core/port/auth_service.go`, `internal/core/service/google.go`, `session_grant.go`, `login.go`, `audit.go`, `internal/adapter/http/google.go`, `auth.go`, `google_callback_test.go`, `auth_contract_test.go`, `docs/api/` | `CompleteGoogle` (login + link), callback | 6 |
| `internal/adapter/http/google_link_test.go`, `google_failures_test.go` | link, audit rollback และ log ของ Google | 7 |
| `cmd/api/wire.go` (+test), `cmd/api/main.go`, `internal/adapter/http/google_e2e_test.go`, `docker-compose.yml`, `docs/api/` | ประกอบ dependency และ test ครบเส้นทางกับ OIDC server ปลอม | 8 |
| `README.md`, roadmap, spec | คู่มือตั้งค่า Google และลองบน browser, สถานะ, ตรวจรับ | 9 |

---

### Task 1: config ของ Google

**Files:**
- Create: `internal/config/google.go`, `internal/config/google_test.go`
- Modify: `internal/config/config.go`, `internal/config/redact.go`

**Interfaces:**
- Consumes: `config.Secret`, `parseOrigin` (ผลอยู่ใน `cfg.PublicOrigin`), test helpers `validDevEnv`, `validProdEnv`, `envFrom`
- Produces:
  - `config.GoogleCallbackPath = "/api/v1/auth/google/callback"`
  - `config.GoogleConfig{ClientID string; ClientSecret Secret; RedirectURI string}` และ `(GoogleConfig).Enabled() bool`
  - `config.Config.Google GoogleConfig`; `Config.String()`/`LogValue()` บอก `google_enabled` เท่านั้น

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/config/google_test.go`:

```go
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func googleEnv(env map[string]string) map[string]string {
	env["GOOGLE_CLIENT_ID"] = "1234-abc.apps.googleusercontent.com"
	env["GOOGLE_CLIENT_SECRET"] = "GOCSPX-test-secret-ZQXJ"
	env["GOOGLE_REDIRECT_URI"] = env["PUBLIC_ORIGIN"] + GoogleCallbackPath
	return env
}

func TestLoadGoogleIsOffByDefault(t *testing.T) {
	cfg, err := Load(envFrom(validDevEnv()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Google.Enabled() || cfg.Google != (GoogleConfig{}) {
		t.Errorf("Google = %+v, want off and empty", cfg.Google)
	}
}

func TestLoadGoogle(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"development": googleEnv(validDevEnv()),
		"production":  googleEnv(validProdEnv()),
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(envFrom(env))
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			g := cfg.Google
			if !g.Enabled() || g.ClientID != env["GOOGLE_CLIENT_ID"] || g.ClientSecret.Reveal() != env["GOOGLE_CLIENT_SECRET"] ||
				g.RedirectURI != env["PUBLIC_ORIGIN"]+GoogleCallbackPath {
				t.Errorf("Google = %+v", g)
			}
		})
	}
}

func TestLoadGoogleNeedsAllThreeValues(t *testing.T) {
	for _, missing := range []string{"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_REDIRECT_URI"} {
		t.Run(missing, func(t *testing.T) {
			env := googleEnv(validDevEnv())
			delete(env, missing)
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), "missing "+missing) {
				t.Fatalf("Load() error = %v, want it to name %s", err, missing)
			}
			if strings.Contains(err.Error(), "ZQXJ") || strings.Contains(err.Error(), "apps.googleusercontent.com") {
				t.Errorf("error %q echoes a configured value", err)
			}
		})
	}
	env := validDevEnv()
	env["GOOGLE_CLIENT_SECRET"] = "   "
	if _, err := Load(envFrom(env)); err != nil {
		t.Errorf("only blank Google values = %v, want Google off without an error", err)
	}
}

func TestLoadGoogleRedirectMustBeTheCallbackOfThisOrigin(t *testing.T) {
	for name, uri := range map[string]string{
		"other host":     "http://evil.example" + GoogleCallbackPath,
		"other port":     "http://localhost:8081" + GoogleCallbackPath,
		"other path":     "http://localhost:8080/callback",
		"trailing slash": "http://localhost:8080" + GoogleCallbackPath + "/",
		"with a query":   "http://localhost:8080" + GoogleCallbackPath + "?next=/",
	} {
		t.Run(name, func(t *testing.T) {
			env := googleEnv(validDevEnv())
			env["GOOGLE_REDIRECT_URI"] = uri
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), "GOOGLE_REDIRECT_URI must be PUBLIC_ORIGIN followed by "+GoogleCallbackPath) {
				t.Fatalf("Load() error = %v, want the redirect URI rule", err)
			}
			if strings.Contains(err.Error(), uri) {
				t.Errorf("error %q echoes the configured value", err)
			}
		})
	}
}

func TestConfigNeverPrintsTheGoogleSecret(t *testing.T) {
	cfg, err := Load(envFrom(googleEnv(validDevEnv())))
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("config", slog.Any("config", cfg), slog.Any("google", cfg.Google))
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{
		"%v": fmt.Sprintf("%v", cfg), "%+v": fmt.Sprintf("%+v", cfg), "%#v": fmt.Sprintf("%#v", cfg),
		"google %+v": fmt.Sprintf("%+v", cfg.Google), "json": string(data), "slog": logs.String(),
	} {
		if strings.Contains(out, "ZQXJ") {
			t.Errorf("%s leaks the Google client secret: %s", name, out)
		}
	}
	if !strings.Contains(cfg.String(), "google=true") || !strings.Contains(logs.String(), `"google_enabled":true`) {
		t.Errorf("String() = %s, log = %s; want google=true and google_enabled", cfg.String(), logs.String())
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/config/ -run Google -count=1`
Expected: build fail — `undefined: GoogleCallbackPath`, `cfg.Google undefined`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/config/google.go`:

```go
package config

import (
	"errors"
	"fmt"
	"strings"
)

// GoogleCallbackPath is where Google sends the browser back. GOOGLE_REDIRECT_URI must be
// PUBLIC_ORIGIN followed by this path: the browser-binding cookie belongs to this host only, so
// any other redirect target would fail every sign-in.
const GoogleCallbackPath = "/api/v1/auth/google/callback"

// GoogleConfig is the OAuth client of Google sign-in. Either every value is set or none is, and an
// empty GoogleConfig turns Google sign-in off.
type GoogleConfig struct {
	ClientID     string
	ClientSecret Secret
	RedirectURI  string
}

// Enabled reports whether Google sign-in is configured.
func (g GoogleConfig) Enabled() bool { return g.ClientID != "" }

// loadGoogle reads GOOGLE_*. Its errors name the variables but never their values.
func loadGoogle(getenv func(string) string, publicOrigin string) (GoogleConfig, []error) {
	values := []struct {
		name  string
		value string
	}{
		{"GOOGLE_CLIENT_ID", strings.TrimSpace(getenv("GOOGLE_CLIENT_ID"))},
		{"GOOGLE_CLIENT_SECRET", strings.TrimSpace(getenv("GOOGLE_CLIENT_SECRET"))},
		{"GOOGLE_REDIRECT_URI", strings.TrimSpace(getenv("GOOGLE_REDIRECT_URI"))},
	}
	var missing []string
	for _, v := range values {
		if v.value == "" {
			missing = append(missing, v.name)
		}
	}
	switch len(missing) {
	case len(values):
		return GoogleConfig{}, nil
	case 0:
	default:
		return GoogleConfig{}, []error{fmt.Errorf(
			"GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET and GOOGLE_REDIRECT_URI must be set together; missing %s",
			strings.Join(missing, ", "))}
	}
	cfg := GoogleConfig{ClientID: values[0].value, ClientSecret: Secret(values[1].value), RedirectURI: values[2].value}
	// publicOrigin is empty when PUBLIC_ORIGIN itself was invalid; that error is reported already.
	if publicOrigin != "" && cfg.RedirectURI != publicOrigin+GoogleCallbackPath {
		return GoogleConfig{}, []error{errors.New("GOOGLE_REDIRECT_URI must be PUBLIC_ORIGIN followed by " + GoogleCallbackPath)}
	}
	return cfg, nil
}
```

แก้ `internal/config/config.go` ตามคู่ต่อไปนี้:

1. แทน

```go
	Auth                AuthConfig
	SMTP                SMTPConfig
}
```

   ด้วย

```go
	Auth                AuthConfig
	SMTP                SMTPConfig
	Google              GoogleConfig
}
```

2. แทน

```go
	var authErrs, smtpErrs []error
	cfg.Auth, authErrs = loadAuth(getenv, prod)
	cfg.SMTP, smtpErrs = loadSMTP(getenv, prod)
	errs = append(errs, authErrs...)
	errs = append(errs, smtpErrs...)
```

   ด้วย

```go
	var authErrs, smtpErrs, googleErrs []error
	cfg.Auth, authErrs = loadAuth(getenv, prod)
	cfg.SMTP, smtpErrs = loadSMTP(getenv, prod)
	cfg.Google, googleErrs = loadGoogle(getenv, cfg.PublicOrigin)
	errs = append(errs, authErrs...)
	errs = append(errs, smtpErrs...)
	errs = append(errs, googleErrs...)
```

แก้ `internal/config/redact.go` ตามคู่ต่อไปนี้:

1. แทน

```go
	return fmt.Sprintf("config{env=%s addr=%s origin=%s docs=%t try_it_out=%t smtp=%s:%d smtp_tls=%s}",
		c.Env, c.HTTPAddr, c.PublicOrigin, c.DocsEnabled, c.DocsTryItOutEnabled, c.SMTP.Host, c.SMTP.Port, c.SMTP.TLS)
```

   ด้วย

```go
	return fmt.Sprintf("config{env=%s addr=%s origin=%s docs=%t try_it_out=%t smtp=%s:%d smtp_tls=%s google=%t}",
		c.Env, c.HTTPAddr, c.PublicOrigin, c.DocsEnabled, c.DocsTryItOutEnabled, c.SMTP.Host, c.SMTP.Port, c.SMTP.TLS,
		c.Google.Enabled())
```

2. แทน

```go
		slog.String("smtp_tls", c.SMTP.TLS),
	)
```

   ด้วย

```go
		slog.String("smtp_tls", c.SMTP.TLS),
		slog.Bool("google_enabled", c.Google.Enabled()),
	)
```

(ถ้า test เดิมของ `Config.String()` เทียบข้อความทั้งบรรทัด ให้เพิ่ม ` google=false` ตามผลใหม่และระบุใน report)

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/config; go vet ./internal/config/ && go test ./internal/config/ -count=1 -race && make lint`
Expected: ผ่านทั้งหมด

- [ ] **Step 5: Commit**

```bash
git add internal/config/google.go internal/config/google_test.go internal/config/config.go internal/config/redact.go
git commit -m "feat(config): add the Google OAuth client settings, all or none"
```

---

### Task 2: ชนิดข้อมูลของ OIDC flow, sentinels และ port `IdentityProvider`

**Files:**
- Create: `internal/core/domain/oidc.go`, `internal/core/domain/oidc_test.go`, `internal/core/port/identity.go`
- Modify: `internal/core/domain/errors.go`, `internal/core/port/repositories.go`

**Interfaces:**
- Consumes: `domain.SecretText`, `domain.Principal`, `domain.SessionGrant`, `domain.OIDCPurpose`, `port.IdentityRepository`, `port.OIDCFlowRepository`
- Produces:
  - `domain.OIDCOutcome` และค่าคงที่ `OIDCOutcomeSuccess` (`success`), `OIDCOutcomeLinked` (`linked`), `OIDCOutcomeLinkRequired` (`link_required`), `OIDCOutcomeFailed` (`failed`)
  - `domain.OIDCStart{Purpose OIDCPurpose; Principal Principal}`
  - `domain.OIDCRedirect{URL string; BrowserBinding SecretText; ExpiresAt time.Time}`
  - `domain.OIDCCallback{Code SecretText; State string; ProviderError string; BrowserBinding SecretText; Principal Principal}`
  - `domain.OIDCResult{Outcome OIDCOutcome; Session SessionGrant}` (Session ใช้เฉพาะ outcome success)
  - `domain.AuthorizationRequest{State, Nonce, CodeChallenge string}`, `domain.CodeExchange{Code, CodeVerifier SecretText}`, `domain.VerifiedIdentity{Issuer, Subject, Email string; EmailVerified bool; Nonce string}`
  - sentinels `domain.ErrFeatureDisabled` (code `feature_disabled`), `domain.ErrIdentityRejected` (code `invalid_credentials`)
  - `port.IdentityProvider{AuthorizationURL(ctx, domain.AuthorizationRequest) (string, error); Exchange(ctx, domain.CodeExchange) (domain.VerifiedIdentity, error)}`
  - `port.Repositories` เพิ่ม `Identities() IdentityRepository` และ `OIDCFlows() OIDCFlowRepository` (`*postgres.Store` มีทั้งสอง method อยู่แล้ว)

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/core/domain/oidc_test.go`:

```go
package domain_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

func TestOIDCValuesNeverPrintTheirSecrets(t *testing.T) {
	values := map[string]any{
		"callback": domain.OIDCCallback{Code: domain.NewSecretText("code-ZQXJ"), State: "s", BrowserBinding: domain.NewSecretText("binding-ZQXJ")},
		"exchange": domain.CodeExchange{Code: domain.NewSecretText("code-ZQXJ"), CodeVerifier: domain.NewSecretText("verifier-ZQXJ")},
		"redirect": domain.OIDCRedirect{URL: "https://accounts.example/auth", BrowserBinding: domain.NewSecretText("binding-ZQXJ"), ExpiresAt: time.Now()},
		"result":   domain.OIDCResult{Outcome: domain.OIDCOutcomeSuccess, Session: domain.SessionGrant{Token: domain.NewSecretText("token-ZQXJ")}},
	}
	for name, v := range values {
		for _, verb := range []string{"%v", "%+v", "%#v"} {
			if out := fmt.Sprintf(verb, v); strings.Contains(out, "ZQXJ") {
				t.Errorf("%s %s leaks a secret: %s", name, verb, out)
			}
		}
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "ZQXJ") {
			t.Errorf("%s JSON leaks a secret: %s", name, data)
		}
	}
}

func TestOIDCOutcomesAreTheRedirectValues(t *testing.T) {
	got := []domain.OIDCOutcome{domain.OIDCOutcomeSuccess, domain.OIDCOutcomeLinked, domain.OIDCOutcomeLinkRequired, domain.OIDCOutcomeFailed}
	want := []string{"success", "linked", "link_required", "failed"}
	for i := range got {
		if string(got[i]) != want[i] {
			t.Errorf("outcome %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGoogleSentinels(t *testing.T) {
	for sentinel, code := range map[*domain.Error]domain.ErrorCode{
		domain.ErrFeatureDisabled:  domain.CodeFeatureDisabled,
		domain.ErrIdentityRejected: domain.CodeInvalidCredentials,
	} {
		if sentinel.Code != code || sentinel.Message == "" {
			t.Errorf("sentinel %v has code %s, want %s", sentinel, sentinel.Code, code)
		}
		if !errors.Is(fmt.Errorf("wrapped: %w", sentinel), sentinel) {
			t.Errorf("a wrapped %s must still match", code)
		}
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/domain/ -run 'OIDC|GoogleSentinels' -count=1`
Expected: build fail — `undefined: domain.OIDCCallback`, `domain.ErrFeatureDisabled`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/core/domain/oidc.go`:

```go
package domain

import "time"

// OIDCOutcome is where the Google callback sends the browser: /docs/?google=<outcome>.
type OIDCOutcome string

const (
	OIDCOutcomeSuccess      OIDCOutcome = "success"       // signed in; the session cookie is set
	OIDCOutcomeLinked       OIDCOutcome = "linked"        // Google added to the signed-in account
	OIDCOutcomeLinkRequired OIDCOutcome = "link_required" // the email has an account: sign in to it, then link
	OIDCOutcomeFailed       OIDCOutcome = "failed"
)

// OIDCStart begins a Google flow. Principal is the signed-in caller: required to link, ignored for
// login.
type OIDCStart struct {
	Purpose   OIDCPurpose
	Principal Principal
}

// OIDCRedirect is where to send the browser, and the browser binding the HTTP adapter keeps in an
// HttpOnly cookie until the callback. Only the binding's hash is stored.
type OIDCRedirect struct {
	URL            string
	BrowserBinding SecretText
	ExpiresAt      time.Time
}

// OIDCCallback is what came back: the callback query, the binding cookie, and the caller's current
// session (zero when there is none).
type OIDCCallback struct {
	Code           SecretText
	State          string
	ProviderError  string // the provider's "error" query value; never shown to the client
	BrowserBinding SecretText
	Principal      Principal
}

// OIDCResult ends a flow. Session is set only when Outcome is OIDCOutcomeSuccess.
type OIDCResult struct {
	Outcome OIDCOutcome
	Session SessionGrant
}

// AuthorizationRequest is what the identity adapter puts in the provider's sign-in URL. The redirect
// URI and client ID are adapter configuration.
type AuthorizationRequest struct {
	State         string
	Nonce         string
	CodeChallenge string // S256 of the PKCE verifier (RFC 7636)
}

// CodeExchange redeems an authorization code with its PKCE verifier.
type CodeExchange struct {
	Code         SecretText
	CodeVerifier SecretText
}

// VerifiedIdentity is an ID token whose signature, issuer, audience and expiry the adapter checked.
// The service still compares Nonce with the flow. Email is as the provider sent it: parse it with
// ParseEmail before use.
type VerifiedIdentity struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	Nonce         string
}
```

แก้ `internal/core/domain/errors.go` — แทน

```go
	ErrCSRFFailed          = NewError(CodeCSRFFailed, "CSRF token is missing, invalid or expired")
```

ด้วย

```go
	ErrCSRFFailed          = NewError(CodeCSRFFailed, "CSRF token is missing, invalid or expired")

	// Google sign-in. ErrIdentityRejected means the provider or the token check refused the
	// sign-in; the callback turns it into an audited "failed" outcome, never a client message.
	ErrFeatureDisabled  = NewError(CodeFeatureDisabled, "this feature is not configured on this server")
	ErrIdentityRejected = NewError(CodeInvalidCredentials, "the identity provider did not confirm the sign-in")
```

สร้าง `internal/core/port/identity.go`:

```go
package port

import (
	"context"

	"go-backend-stock/internal/core/domain"
)

// IdentityProvider is an external OpenID Connect provider (Google in phase 1). The adapter owns the
// client ID, client secret and redirect URI, and checks the ID token's signature, issuer, audience
// and expiry before it returns an identity.
type IdentityProvider interface {
	// AuthorizationURL returns the provider's sign-in URL for req. A provider that cannot be reached
	// returns dependency_unavailable.
	AuthorizationURL(ctx context.Context, req domain.AuthorizationRequest) (string, error)
	// Exchange redeems the code and verifies the ID token. A code, verifier or token that the
	// provider or the token check refuses returns domain.ErrIdentityRejected; a provider that cannot
	// be reached returns dependency_unavailable.
	Exchange(ctx context.Context, ex domain.CodeExchange) (domain.VerifiedIdentity, error)
}
```

แก้ `internal/core/port/repositories.go` — แทน

```go
	OTPChallenges() OTPChallengeRepository
	RateLimits() RateLimitRepository
}
```

ด้วย

```go
	OTPChallenges() OTPChallengeRepository
	RateLimits() RateLimitRepository
	Identities() IdentityRepository
	OIDCFlows() OIDCFlowRepository // Consume is one conditional UPDATE, so it needs no transaction
}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/core; go vet ./... && go test ./internal/... -count=1 -race && make lint`
Expected: ผ่านทั้งหมด; `internal/adapter/postgres/store.go` ยัง compile เพราะ `*Store` มี `Identities()` และ `OIDCFlows()` อยู่แล้ว (ถ้า signature ไม่ตรงกับ port ให้แก้ฝั่ง Store และระบุใน report)

- [ ] **Step 5: Commit**

```bash
git add internal/core/domain/oidc.go internal/core/domain/oidc_test.go internal/core/domain/errors.go internal/core/port/identity.go internal/core/port/repositories.go
git commit -m "feat(domain): add the Google sign-in flow types and the IdentityProvider port"
```

---

### Task 3: ค่าสุ่มของ flow และ PKCE

**Files:**
- Create: `internal/core/service/oidc.go`, `internal/core/service/oidc_test.go`

**Interfaces:**
- Consumes: `copySecret` (`internal/core/service/secret.go`)
- Produces:
  - `service.OIDCCodec`; `func NewOIDCCodec(secret []byte) (*OIDCCodec, error)` (secret อย่างน้อย 32 bytes)
  - `(*OIDCCodec).Verifier(state, binding string) string` — base64url(HMAC-SHA-256(secret, `"pkce"` ‖ 0x00 ‖ state ‖ 0x00 ‖ binding)) ยาว 43
  - `func CodeChallenge(verifier string) string` — base64url(SHA-256(verifier)) ตาม RFC 7636
  - `func NewOIDCValue() (string, error)` — 32 bytes สุ่ม เป็น base64url ไม่มี padding (43 ตัว)
  - `func ValidOIDCValue(s string) bool`
  - `func HashOIDCValue(s string) []byte` — SHA-256 ของ string (32 bytes) ที่เก็บใน `oauth_flows`
  - `oidcFlowLifetime = 10 * time.Minute` (ใช้ใน Task 5)

- [ ] **Step 1: เขียน failing tests**

สร้าง `internal/core/service/oidc_test.go`:

```go
package service

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
)

func TestNewOIDCValue(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		v, err := NewOIDCValue()
		if err != nil {
			t.Fatal(err)
		}
		if len(v) != 43 || !ValidOIDCValue(v) || strings.ContainsAny(v, "+/=") {
			t.Fatalf("value %q is not 43 unpadded base64url characters", v)
		}
		if seen[v] {
			t.Fatal("NewOIDCValue repeated a value")
		}
		seen[v] = true
	}
}

func TestValidOIDCValueRejectsOtherShapes(t *testing.T) {
	good, err := NewOIDCValue()
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{
		"empty": "", "short": good[:42], "long": good + "A", "padding": good[:42] + "=",
		"standard base64": strings.Repeat("+", 43), "space": " " + good[1:],
	} {
		if ValidOIDCValue(v) {
			t.Errorf("%s %q accepted", name, v)
		}
	}
}

func TestHashOIDCValue(t *testing.T) {
	sum := sha256.Sum256([]byte("state-value"))
	if got := HashOIDCValue("state-value"); !bytes.Equal(got, sum[:]) || len(got) != 32 {
		t.Errorf("HashOIDCValue = %x, want the SHA-256 of the string", got)
	}
}

// RFC 7636 appendix B.
func TestCodeChallengeMatchesRFC7636(t *testing.T) {
	if got := CodeChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Errorf("CodeChallenge = %q", got)
	}
}

func TestVerifierIsBoundToStateBindingAndSecret(t *testing.T) {
	c, err := NewOIDCCodec(bytes.Repeat([]byte("i"), 32))
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewOIDCCodec(bytes.Repeat([]byte("j"), 32))
	if err != nil {
		t.Fatal(err)
	}
	v := c.Verifier("state-a", "binding-a")
	if len(v) != 43 || v != c.Verifier("state-a", "binding-a") {
		t.Fatalf("Verifier = %q, want 43 characters and the same value each time", v)
	}
	for name, w := range map[string]string{
		"other state":   c.Verifier("state-b", "binding-a"),
		"other binding": c.Verifier("state-a", "binding-b"),
		"other secret":  other.Verifier("state-a", "binding-a"),
		"moved byte":    c.Verifier("state-ab", "inding-a"),
	} {
		if w == v {
			t.Errorf("%s gives the same verifier", name)
		}
	}
	if _, err := NewOIDCCodec(bytes.Repeat([]byte("i"), 31)); err == nil {
		t.Error("NewOIDCCodec accepted a 31-byte secret")
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/service/ -run 'OIDC|CodeChallenge|Verifier' -count=1`
Expected: build fail — `undefined: NewOIDCValue`, `NewOIDCCodec`

- [ ] **Step 3: เขียนโค้ด**

สร้าง `internal/core/service/oidc.go`:

```go
package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"
)

const (
	// oidcFlowLifetime is how long a Google flow may wait for its callback (design section 6).
	oidcFlowLifetime = 10 * time.Minute
	oidcValueBytes   = 32
)

// OIDCCodec derives the PKCE verifier of a Google flow (phase 1 spec section 10). The verifier is
// never stored: the callback recomputes it from the state in the query and the binding cookie, so
// rotating OIDC_SECRET only breaks the flows started in the last ten minutes. Safe for concurrent use.
type OIDCCodec struct{ secret []byte }

// NewOIDCCodec returns a codec keyed with secret (at least 32 bytes).
func NewOIDCCodec(secret []byte) (*OIDCCodec, error) {
	s, err := copySecret("OIDC secret", secret)
	if err != nil {
		return nil, err
	}
	return &OIDCCodec{secret: s}, nil
}

// Verifier returns base64url(HMAC-SHA-256(secret, "pkce" ‖ 0x00 ‖ state ‖ 0x00 ‖ binding)): 43
// characters, inside the 43–128 that RFC 7636 allows.
func (c *OIDCCodec) Verifier(state, binding string) string {
	mac := hmac.New(sha256.New, c.secret)
	for i, part := range []string{"pkce", state, binding} {
		if i > 0 {
			mac.Write([]byte{0})
		}
		mac.Write([]byte(part))
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// CodeChallenge is the S256 challenge of verifier (RFC 7636 section 4.2).
func CodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// NewOIDCValue returns 32 random bytes as 43 unpadded base64url characters: a state, a nonce or a
// browser binding.
func NewOIDCValue() (string, error) {
	raw := make([]byte, oidcValueBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate OIDC value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// ValidOIDCValue reports whether s has the shape NewOIDCValue produces. A callback value of any
// other shape cannot belong to a flow, so it never reaches the database.
func ValidOIDCValue(s string) bool {
	if len(s) != 43 {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return err == nil && len(raw) == oidcValueBytes
}

// HashOIDCValue is what oauth_flows stores instead of a state, nonce or binding.
func HashOIDCValue(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}
```

- [ ] **Step 4: รัน test ให้ผ่าน**

Run: `gofmt -l internal/core; go vet ./internal/core/... && go test ./internal/core/... -count=1 -race && make lint`
Expected: ผ่านทั้งหมด รวม `boundary_test` (ถ้า lint เตือนว่า `oidcFlowLifetime` ไม่ถูกใช้ ให้ใส่ `//nolint:unused // Task 5 uses it` ชั่วคราวและระบุใน report; Task 5 ต้องลบออก)

- [ ] **Step 5: Commit**

```bash
git add internal/core/service/oidc.go internal/core/service/oidc_test.go
git commit -m "feat(service): add the Google flow values and the derived PKCE verifier"
```

---

### Task 4: adapter Google ด้วย go-oidc และ OIDC server ปลอม

**Files:**
- Create: `internal/adapter/identity/google.go`, `internal/adapter/identity/google_test.go`, `internal/adapter/identity/identitytest/provider.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `port.IdentityProvider`, `domain.AuthorizationRequest`, `domain.CodeExchange`, `domain.VerifiedIdentity`, `domain.ErrIdentityRejected`, `domain.CodeDependencyUnavailable` (Task 2)
- Produces:
  - `identity.GoogleIssuer = "https://accounts.google.com"`
  - `identity.GoogleConfig{Issuer, ClientID, ClientSecret, RedirectURL string; Timeout time.Duration; HTTPClient *http.Client}` (พิมพ์/JSON/slog ไม่แสดง ClientSecret)
  - `func NewGoogle(cfg GoogleConfig) (*Google, error)`; `*Google` implement `port.IdentityProvider`; discovery แบบ lazy ที่ลองใหม่ได้เมื่อล้มเหลว
  - package `identitytest` (ใช้ใน test เท่านั้น): `ClientID`, `ClientSecret`, `type Grant`, `func New(t testing.TB) *Provider`, `(*Provider).Issuer() string`, `Authorize(t, Grant) string`, `SetDown(bool)`, `TokenCalls() int`, `func NewKey(t testing.TB) *rsa.PrivateKey`, `func ParamsFromURL(t testing.TB, authURL string) (state, nonce, challenge string)`, `func Challenge(verifier string) string`

- [ ] **Step 1: เพิ่ม dependencies**

Run:
```bash
go get github.com/coreos/go-oidc/v3@v3.21.0 golang.org/x/oauth2@v0.37.0
grep -E 'go-oidc|oauth2|go-jose' go.mod
```
Expected: go.mod มี `github.com/coreos/go-oidc/v3 v3.21.0`, `golang.org/x/oauth2 v0.37.0` และ `github.com/go-jose/go-jose/v4 v4.1.4` (indirect ตอนนี้; หลัง `go mod tidy` ใน Step 5 จะเป็น direct เพราะ `identitytest` import) ถ้า go-jose ได้เวอร์ชันอื่นที่ใหม่กว่า v4.1.5 ให้หยุดและรายงาน controller

- [ ] **Step 2: เขียน OIDC server ปลอมและ failing tests**

สร้าง `internal/adapter/identity/identitytest/provider.go`:

```go
// Package identitytest is a fake OpenID Connect provider for tests: discovery, a JWKS with an RSA
// key made at start-up, and a token endpoint that checks the client credentials, the code and the
// PKCE verifier before it signs an ID token. Only tests import it.
package identitytest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// The OAuth client the fake provider accepts.
const (
	ClientID     = "test-client.apps.example"
	ClientSecret = "test-client-secret-ZQXJ"
	keyID        = "test-key"
)

// Grant is what one authorization code redeems to. The zero values of the override fields give a
// valid token.
type Grant struct {
	Subject       string
	Email         string
	EmailVerified bool
	Nonce         string
	CodeChallenge string // the S256 challenge of the authorization request

	Audience string          // "" = ClientID
	Issuer   string          // "" = the server's URL
	Expiry   time.Duration   // 0 = five minutes from now; negative = already expired
	SignWith *rsa.PrivateKey // nil = the published key
}

// Provider is a running fake provider. Safe for concurrent use.
type Provider struct {
	Server *httptest.Server

	mu         sync.Mutex
	key        *rsa.PrivateKey
	codes      map[string]Grant
	down       bool
	tokenCalls int
}

// New starts a provider that stops when the test ends.
func New(t testing.TB) *Provider {
	t.Helper()
	p := &Provider{key: NewKey(t), codes: map[string]Grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /keys", p.keys)
	mux.HandleFunc("POST /token", p.token)
	p.Server = httptest.NewServer(p.guard(mux))
	t.Cleanup(p.Server.Close)
	return p
}

// NewKey returns an RSA key, for example one the provider does not publish.
func NewKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// Issuer is the provider's issuer URL.
func (p *Provider) Issuer() string { return p.Server.URL }

// Authorize registers a one-time code that redeems to g and returns it.
func (p *Provider) Authorize(t testing.TB, g Grant) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.codes[code] = g
	return code
}

// SetDown makes every endpoint answer 503 (down) or work again.
func (p *Provider) SetDown(down bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = down
}

// TokenCalls is how many times the token endpoint was called.
func (p *Provider) TokenCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tokenCalls
}

// Challenge is the S256 PKCE challenge of verifier (RFC 7636).
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ParamsFromURL reads the flow values of an authorization URL.
func ParamsFromURL(t testing.TB, authURL string) (state, nonce, challenge string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("authorization URL: %v", err)
	}
	q := u.Query()
	return q.Get("state"), q.Get("nonce"), q.Get("code_challenge")
}

func (p *Provider) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		down := p.down
		p.mu.Unlock()
		if down {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                p.Issuer(),
		"authorization_endpoint":                p.Issuer() + "/auth",
		"token_endpoint":                        p.Issuer() + "/token",
		"jwks_uri":                              p.Issuer() + "/keys",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (p *Provider) keys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: &p.key.PublicKey, KeyID: keyID, Algorithm: string(jose.RS256), Use: "sig",
	}}})
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.tokenCalls++
	p.mu.Unlock()
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id, secret, ok := r.BasicAuth()
	if ok { // oauth2 query-escapes basic credentials
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != ClientID || secret != ClientSecret {
		tokenError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	code := r.PostForm.Get("code")
	p.mu.Lock()
	g, found := p.codes[code]
	delete(p.codes, code) // a code works once
	p.mu.Unlock()
	if r.PostForm.Get("grant_type") != "authorization_code" || !found ||
		Challenge(r.PostForm.Get("code_verifier")) != g.CodeChallenge {
		tokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	raw, err := p.sign(g)
	if err != nil {
		http.Error(w, "signing failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": "fake-access-token", "token_type": "Bearer", "expires_in": 3600, "id_token": raw,
	})
}

func (p *Provider) sign(g Grant) (string, error) {
	key, aud, iss, life := p.key, g.Audience, g.Issuer, g.Expiry
	if g.SignWith != nil {
		key = g.SignWith
	}
	if aud == "" {
		aud = ClientID
	}
	if iss == "" {
		iss = p.Issuer()
	}
	if life == 0 {
		life = 5 * time.Minute
	}
	now := time.Now()
	payload, err := json.Marshal(map[string]any{
		"iss": iss, "aud": aud, "sub": g.Subject, "email": g.Email, "email_verified": g.EmailVerified,
		"nonce": g.Nonce, "iat": now.Unix(), "exp": now.Add(life).Unix(),
	})
	if err != nil {
		return "", err
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID))
	if err != nil {
		return "", err
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		return "", err
	}
	return obj.CompactSerialize()
}

func tokenError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
```

สร้าง `internal/adapter/identity/google_test.go`:

```go
package identity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/adapter/identity/identitytest"
	"go-backend-stock/internal/core/domain"
)

const testRedirect = "http://localhost:8080/api/v1/auth/google/callback"

func newTestGoogle(t *testing.T, p *identitytest.Provider) *Google {
	t.Helper()
	g, err := NewGoogle(GoogleConfig{
		Issuer: p.Issuer(), ClientID: identitytest.ClientID, ClientSecret: identitytest.ClientSecret,
		RedirectURL: testRedirect, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewGoogle() error = %v", err)
	}
	return g
}

func requireCode(t *testing.T, err error, want domain.ErrorCode) {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != want {
		t.Fatalf("error = %v, want code %s", err, want)
	}
}

const testVerifier = "vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv" // 43 characters

func exchange(code string) domain.CodeExchange {
	return domain.CodeExchange{Code: domain.NewSecretText(code), CodeVerifier: domain.NewSecretText(testVerifier)}
}

func TestAuthorizationURLCarriesTheFlowValues(t *testing.T) {
	p := identitytest.New(t)
	g := newTestGoogle(t, p)
	raw, err := g.AuthorizationURL(t.Context(), domain.AuthorizationRequest{State: "state-1", Nonce: "nonce-1", CodeChallenge: "challenge-1"})
	if err != nil {
		t.Fatalf("AuthorizationURL() error = %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != p.Issuer()+"/auth" {
		t.Errorf("endpoint = %s, want the discovered authorization endpoint", got)
	}
	want := map[string]string{
		"response_type": "code", "client_id": identitytest.ClientID, "redirect_uri": testRedirect,
		"scope": "openid email", "state": "state-1", "nonce": "nonce-1",
		"code_challenge": "challenge-1", "code_challenge_method": "S256",
	}
	q := u.Query()
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if q.Has("client_secret") || strings.Contains(raw, "ZQXJ") {
		t.Error("the authorization URL must not carry the client secret")
	}
}

func TestExchangeReturnsTheVerifiedIdentity(t *testing.T) {
	p := identitytest.New(t)
	g := newTestGoogle(t, p)
	code := p.Authorize(t, identitytest.Grant{
		Subject: "1029384756", Email: "user@example.com", EmailVerified: true, Nonce: "nonce-1",
		CodeChallenge: identitytest.Challenge(testVerifier),
	})
	id, err := g.Exchange(t.Context(), exchange(code))
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if id.Issuer != p.Issuer() || id.Subject != "1029384756" || id.Email != "user@example.com" || !id.EmailVerified || id.Nonce != "nonce-1" {
		t.Errorf("identity = %+v", id)
	}
}

func TestExchangeKeepsAnUnverifiedEmailUnverified(t *testing.T) {
	p := identitytest.New(t)
	g := newTestGoogle(t, p)
	code := p.Authorize(t, identitytest.Grant{Subject: "s", Email: "user@example.com", Nonce: "n", CodeChallenge: identitytest.Challenge(testVerifier)})
	id, err := g.Exchange(t.Context(), exchange(code))
	if err != nil || id.EmailVerified {
		t.Errorf("identity = %+v, %v; want email_verified false", id, err)
	}
}

func TestExchangeRejectsABadIDToken(t *testing.T) {
	p := identitytest.New(t)
	g := newTestGoogle(t, p)
	foreign := identitytest.NewKey(t)
	for name, grant := range map[string]identitytest.Grant{
		"other audience":    {Audience: "someone-else.apps.example"},
		"other issuer":      {Issuer: "https://accounts.evil.example"},
		"expired":           {Expiry: -10 * time.Minute},
		"foreign signature": {SignWith: foreign},
	} {
		t.Run(name, func(t *testing.T) {
			grant.Subject, grant.Email, grant.Nonce = "s", "user@example.com", "n"
			grant.CodeChallenge = identitytest.Challenge(testVerifier)
			_, err := g.Exchange(t.Context(), exchange(p.Authorize(t, grant)))
			if !errors.Is(err, domain.ErrIdentityRejected) {
				t.Errorf("Exchange() error = %v, want ErrIdentityRejected", err)
			}
		})
	}
}

func TestExchangeRejectsAWrongVerifierAndAReusedCode(t *testing.T) {
	p := identitytest.New(t)
	g := newTestGoogle(t, p)
	code := p.Authorize(t, identitytest.Grant{Subject: "s", Nonce: "n", CodeChallenge: identitytest.Challenge(testVerifier)})
	wrong := domain.CodeExchange{Code: domain.NewSecretText(code), CodeVerifier: domain.NewSecretText(strings.Repeat("w", 43))}
	if _, err := g.Exchange(t.Context(), wrong); !errors.Is(err, domain.ErrIdentityRejected) {
		t.Errorf("wrong verifier = %v, want ErrIdentityRejected", err)
	}
	code = p.Authorize(t, identitytest.Grant{Subject: "s", Nonce: "n", CodeChallenge: identitytest.Challenge(testVerifier)})
	if _, err := g.Exchange(t.Context(), exchange(code)); err != nil {
		t.Fatalf("first use = %v", err)
	}
	if _, err := g.Exchange(t.Context(), exchange(code)); !errors.Is(err, domain.ErrIdentityRejected) {
		t.Errorf("reused code = %v, want ErrIdentityRejected", err)
	}
}

func TestDiscoveryFailureIsUnavailableAndRetried(t *testing.T) {
	p := identitytest.New(t)
	g := newTestGoogle(t, p)
	req := domain.AuthorizationRequest{State: "s", Nonce: "n", CodeChallenge: "c"}
	p.SetDown(true)
	_, err := g.AuthorizationURL(t.Context(), req)
	requireCode(t, err, domain.CodeDependencyUnavailable)
	p.SetDown(false)
	if _, err := g.AuthorizationURL(t.Context(), req); err != nil {
		t.Errorf("after the provider came back = %v, want discovery to be retried", err)
	}
}

func TestExchangeWhenTheProviderIsDownIsUnavailable(t *testing.T) {
	p := identitytest.New(t)
	g := newTestGoogle(t, p)
	if _, err := g.AuthorizationURL(t.Context(), domain.AuthorizationRequest{State: "s", Nonce: "n", CodeChallenge: "c"}); err != nil {
		t.Fatal(err) // discovery succeeded and is cached
	}
	code := p.Authorize(t, identitytest.Grant{Subject: "s", Nonce: "n", CodeChallenge: identitytest.Challenge(testVerifier)})
	p.SetDown(true)
	_, err := g.Exchange(t.Context(), exchange(code))
	requireCode(t, err, domain.CodeDependencyUnavailable)
	if errors.Is(err, domain.ErrIdentityRejected) {
		t.Error("an outage must not look like a refused sign-in")
	}
}

func TestNewGoogleValidatesItsConfig(t *testing.T) {
	valid := GoogleConfig{Issuer: GoogleIssuer, ClientID: "id", ClientSecret: "secret", RedirectURL: testRedirect, Timeout: time.Second}
	for name, mutate := range map[string]func(*GoogleConfig){
		"no issuer":    func(c *GoogleConfig) { c.Issuer = "" },
		"no client id": func(c *GoogleConfig) { c.ClientID = "" },
		"no secret":    func(c *GoogleConfig) { c.ClientSecret = "" },
		"no redirect":  func(c *GoogleConfig) { c.RedirectURL = "" },
		"no timeout":   func(c *GoogleConfig) { c.Timeout = 0 },
	} {
		cfg := valid
		mutate(&cfg)
		if _, err := NewGoogle(cfg); err == nil {
			t.Errorf("%s: NewGoogle() accepted the config", name)
		}
	}
}

func TestGoogleNeverPrintsTheClientSecret(t *testing.T) {
	cfg := GoogleConfig{Issuer: GoogleIssuer, ClientID: "id", ClientSecret: "GOCSPX-ZQXJ", RedirectURL: testRedirect, Timeout: time.Second}
	g, err := NewGoogle(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("google", slog.Any("config", cfg), slog.Any("provider", g))
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{
		"%v": fmt.Sprintf("%v", cfg), "%+v": fmt.Sprintf("%+v", cfg), "%#v": fmt.Sprintf("%#v", cfg),
		"provider %v": fmt.Sprintf("%v", g), "provider %+v": fmt.Sprintf("%+v", g),
		"json": string(data), "slog": logs.String(),
	} {
		if strings.Contains(out, "ZQXJ") {
			t.Errorf("%s leaks the client secret: %s", name, out)
		}
	}
}
```

- [ ] **Step 3: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/adapter/identity/... -count=1`
Expected: build fail — `undefined: Google`, `NewGoogle`, `GoogleConfig`

- [ ] **Step 4: เขียนโค้ด**

สร้าง `internal/adapter/identity/google.go`:

```go
// Package identity holds the external identity providers (Google OpenID Connect in phase 1).
package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// GoogleIssuer is Google's OpenID Connect issuer.
const GoogleIssuer = "https://accounts.google.com"

// GoogleConfig configures NewGoogle. It never prints, marshals or logs ClientSecret.
type GoogleConfig struct {
	Issuer       string        // GoogleIssuer; a test server in tests
	ClientID     string
	ClientSecret string
	RedirectURL  string        // PUBLIC_ORIGIN + /api/v1/auth/google/callback
	Timeout      time.Duration // the limit of each call to the provider
	HTTPClient   *http.Client  // nil = a client with Timeout
}

func (c GoogleConfig) String() string {
	return "GoogleConfig{issuer=" + c.Issuer + " client_id=" + c.ClientID + " redirect_url=" + c.RedirectURL + "}"
}

// GoString makes %#v print the same safe summary.
func (c GoogleConfig) GoString() string { return c.String() }

// MarshalJSON marshals the same safe summary.
func (c GoogleConfig) MarshalJSON() ([]byte, error) { return json.Marshal(c.String()) }

// LogValue logs the safe fields only.
func (c GoogleConfig) LogValue() slog.Value {
	return slog.GroupValue(slog.String("issuer", c.Issuer), slog.String("client_id", c.ClientID), slog.String("redirect_url", c.RedirectURL))
}

// Google implements port.IdentityProvider with go-oidc and oauth2. It discovers the provider on
// first use and keeps the result; a failed discovery is retried by the next call, so the API starts
// even while Google is unreachable. Safe for concurrent use.
type Google struct {
	cfg    GoogleConfig
	client *http.Client

	mu       sync.Mutex
	provider *oidc.Provider
}

var _ port.IdentityProvider = (*Google)(nil)

// NewGoogle validates cfg. It makes no network call.
func NewGoogle(cfg GoogleConfig) (*Google, error) {
	switch {
	case cfg.Issuer == "", cfg.ClientID == "", cfg.ClientSecret == "", cfg.RedirectURL == "":
		return nil, errors.New("google identity provider is missing a setting")
	case cfg.Timeout <= 0:
		return nil, errors.New("google identity provider timeout must be positive")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	return &Google{cfg: cfg, client: client}, nil
}

func (g *Google) String() string { return "identity.Google{" + g.cfg.String() + "}" }

// GoString makes %#v print the same safe summary.
func (g *Google) GoString() string { return g.String() }

// LogValue logs the safe configuration only.
func (g *Google) LogValue() slog.Value { return g.cfg.LogValue() }

// AuthorizationURL implements port.IdentityProvider.
func (g *Google) AuthorizationURL(ctx context.Context, req domain.AuthorizationRequest) (string, error) {
	p, err := g.discover(ctx)
	if err != nil {
		return "", err
	}
	return g.oauth(p).AuthCodeURL(req.State,
		oauth2.SetAuthURLParam("nonce", req.Nonce),
		oauth2.SetAuthURLParam("code_challenge", req.CodeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	), nil
}

// Exchange implements port.IdentityProvider. go-oidc checks the signature against the provider's
// published keys, the issuer, the audience (the client ID) and the expiry.
func (g *Google) Exchange(ctx context.Context, ex domain.CodeExchange) (domain.VerifiedIdentity, error) {
	p, err := g.discover(ctx)
	if err != nil {
		return domain.VerifiedIdentity{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, g.cfg.Timeout)
	defer cancel()
	ctx = oidc.ClientContext(ctx, g.client)

	tok, err := g.oauth(p).Exchange(ctx, ex.Code.Reveal(), oauth2.VerifierOption(ex.CodeVerifier.Reveal()))
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode < http.StatusInternalServerError {
			return domain.VerifiedIdentity{}, fmt.Errorf("%w: token endpoint answered %d", domain.ErrIdentityRejected, re.Response.StatusCode)
		}
		return domain.VerifiedIdentity{}, unavailable(err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return domain.VerifiedIdentity{}, fmt.Errorf("%w: no ID token", domain.ErrIdentityRejected)
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: g.cfg.ClientID}).Verify(ctx, raw)
	if err != nil {
		if isNetwork(err) {
			return domain.VerifiedIdentity{}, unavailable(err)
		}
		return domain.VerifiedIdentity{}, fmt.Errorf("%w: %w", domain.ErrIdentityRejected, err)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idt.Claims(&claims); err != nil {
		return domain.VerifiedIdentity{}, fmt.Errorf("%w: unreadable claims", domain.ErrIdentityRejected)
	}
	return domain.VerifiedIdentity{
		Issuer: idt.Issuer, Subject: idt.Subject, Email: claims.Email, EmailVerified: claims.EmailVerified, Nonce: idt.Nonce,
	}, nil
}

// discover returns the cached provider, or runs discovery once more.
func (g *Google) discover(ctx context.Context) (*oidc.Provider, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.provider != nil {
		return g.provider, nil
	}
	ctx, cancel := context.WithTimeout(ctx, g.cfg.Timeout)
	defer cancel()
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, g.client), g.cfg.Issuer)
	if err != nil {
		return nil, unavailable(err)
	}
	g.provider = p
	return p, nil
}

func (g *Google) oauth(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID: g.cfg.ClientID, ClientSecret: g.cfg.ClientSecret, RedirectURL: g.cfg.RedirectURL,
		Endpoint: p.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email"},
	}
}

func unavailable(err error) error {
	return fmt.Errorf("%w: %w", domain.NewError(domain.CodeDependencyUnavailable, "identity provider is unavailable"), err)
}

func isNetwork(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &ne)
}
```

หมายเหตุสำหรับผู้ลงมือ: ถ้า `oidc.NewProvider` ของ v3.21.0 เก็บ context ของ discovery ไว้ใช้ดึง JWKS ภายหลัง (ทำให้ timeout ของ discovery ไปตัดการดึง key ครั้งต่อไป) ให้ดู source ใน `$(go env GOMODCACHE)/github.com/coreos/go-oidc/v3@v3.21.0/oidc/oidc.go` แล้วสร้าง provider ด้วย `oidc.ClientContext(context.Background(), g.client)` แทนและคุม timeout ด้วย `http.Client.Timeout`; ระบุสิ่งที่พบใน report

- [ ] **Step 5: รัน test ให้ผ่าน**

Run:
```bash
go mod tidy
gofmt -l internal; go vet ./... && go test ./internal/adapter/identity/... -count=1 -race && go test ./... -count=1 -race && make lint
grep -E 'go-oidc|oauth2|go-jose' go.mod
```
Expected: ผ่านทั้งหมด; go.mod มี go-oidc v3.21.0, oauth2 v0.37.0 และ go-jose v4.1.4 เป็น direct

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/adapter/identity
git commit -m "feat(identity): add the Google OpenID Connect adapter with a fake provider for tests"
```

---

### Task 5: `StartGoogle`, `GET /auth/config`, `POST /auth/google/start` และ `/link`

**Files:**
- Create: `internal/core/service/google.go`, `internal/core/service/google_test.go`, `internal/adapter/http/google.go`, `internal/adapter/http/google_fake_test.go`, `internal/adapter/http/google_start_test.go`
- Modify: `internal/core/port/auth_service.go`, `internal/core/service/auth.go`, `internal/core/service/oidc.go` (ลบ nolint ถ้ามี), `internal/adapter/http/auth.go`, `internal/adapter/http/auth_dto.go`, `internal/adapter/http/cookies.go`, `internal/adapter/http/auth_harness_test.go`, `internal/adapter/http/auth_contract_test.go`, `docs/api/` (generated)

**Interfaces:**
- Consumes: `OIDCCodec`, `NewOIDCValue`, `HashOIDCValue`, `CodeChallenge`, `oidcFlowLifetime` (Task 3); `port.IdentityProvider`, `domain.OIDCStart`, `domain.OIDCRedirect`, `domain.ErrFeatureDisabled`, `port.Repositories.OIDCFlows()` (Task 2); `RateLimitPolicy.GoogleStart`, `(*Auth).admit`, `NowForStorage`, `domain.NewOIDCFlow`; HTTP `cookieJar`, `requestMeta`, `middleware.GetPrincipal`, `middleware.RequireAuth`; harness `newAuthHarness`, `browser`, `signUpAndVerify`, `login`, `user`, `auditFor`, `decodeError`, `uniqueEmail`
- Produces:
  - `port.AuthService` เพิ่ม `GoogleEnabled() bool` และ `StartGoogle(ctx, in domain.OIDCStart, meta domain.RequestMeta) (domain.OIDCRedirect, error)`
  - `service.AuthDeps` เพิ่ม `Identity port.IdentityProvider` (nil = ปิด Google) และ `OIDC *OIDCCodec` (ต้องมีเมื่อ Identity ไม่ nil); `Auth` มี field `identity`, `oidc`
  - HTTP: `cookieJar.bindingName` (`oidc_binding` / `__Host-oidc_binding`); DTO `AuthConfigResponse{GoogleEnabled bool json:"google_enabled"}`, `GoogleStartResponse{AuthorizationURL string json:"authorization_url"}`; routes `GET /api/v1/auth/config`, `POST /api/v1/auth/google/start`, `POST /api/v1/auth/google/link`
  - harness: option `withIdentityProvider(p port.IdentityProvider)`, `testOIDCSecret`; `fakeIdentityProvider` (`newFakeIdentityProvider`, `setIdentity`, `setExchangeError`, `setURLError`, `forceNonce`, `lastRequest`, `exchanges`); browser helpers `startGoogle()`, `linkGoogle()`, `googleCallback(url.Values)`; `decodeStart`; `(*authHarness).flowByState(t, state) (flowRow, bool)` (ใช้ต่อใน Task 6–8)

- [ ] **Step 1: เขียน failing tests ของ service**

สร้าง `internal/core/service/google_test.go`:

```go
package service

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

type fakeFlows struct {
	port.OIDCFlowRepository
	inserted []domain.OIDCFlow
}

func (f *fakeFlows) Insert(_ context.Context, fl domain.OIDCFlow) error {
	f.inserted = append(f.inserted, fl)
	return nil
}

// googleRepos adds the OIDC flows to the fakes of newTestAuth.
type googleRepos struct {
	*fakeRepositories
	flows *fakeFlows
}

func (r *googleRepos) OIDCFlows() port.OIDCFlowRepository { return r.flows }

type fakeIdentity struct {
	requests []domain.AuthorizationRequest
	urlErr   error
}

func (f *fakeIdentity) AuthorizationURL(_ context.Context, req domain.AuthorizationRequest) (string, error) {
	f.requests = append(f.requests, req)
	if f.urlErr != nil {
		return "", f.urlErr
	}
	return "https://accounts.example/auth?state=" + req.State, nil
}

func (f *fakeIdentity) Exchange(context.Context, domain.CodeExchange) (domain.VerifiedIdentity, error) {
	return domain.VerifiedIdentity{}, errors.New("not used by these tests")
}

type googleTestAuth struct {
	testAuth
	identity *fakeIdentity
	flows    *fakeFlows
	codec    *OIDCCodec
}

func newGoogleTestAuth(t *testing.T) googleTestAuth {
	t.Helper()
	codec, err := NewOIDCCodec(bytes.Repeat([]byte("i"), 32))
	if err != nil {
		t.Fatal(err)
	}
	identity, flows := &fakeIdentity{}, &fakeFlows{}
	ta := newTestAuth(t, func(d *AuthDeps) {
		d.Store = &googleRepos{fakeRepositories: d.Store.(*fakeRepositories), flows: flows}
		d.Identity, d.OIDC = identity, codec
	})
	return googleTestAuth{testAuth: ta, identity: identity, flows: flows, codec: codec}
}

func TestStartGoogleWhenDisabled(t *testing.T) {
	ta := newTestAuth(t, nil)
	if ta.auth.GoogleEnabled() {
		t.Error("GoogleEnabled() = true without an identity provider")
	}
	_, err := ta.auth.StartGoogle(t.Context(), domain.OIDCStart{Purpose: domain.OIDCPurposeLogin}, testMeta(t))
	requireCode(t, err, domain.CodeFeatureDisabled)
}

func TestStartGoogleStoresOnlyHashesAndDerivesTheChallenge(t *testing.T) {
	g := newGoogleTestAuth(t)
	if !g.auth.GoogleEnabled() {
		t.Fatal("GoogleEnabled() = false with an identity provider")
	}
	r, err := g.auth.StartGoogle(t.Context(), domain.OIDCStart{Purpose: domain.OIDCPurposeLogin}, testMeta(t))
	if err != nil {
		t.Fatalf("StartGoogle() error = %v", err)
	}
	if len(g.identity.requests) != 1 || len(g.flows.inserted) != 1 {
		t.Fatalf("requests = %d, flows = %d; want one of each", len(g.identity.requests), len(g.flows.inserted))
	}
	req, flow, binding := g.identity.requests[0], g.flows.inserted[0], r.BrowserBinding.Reveal()
	for name, v := range map[string]string{"state": req.State, "nonce": req.Nonce, "binding": binding} {
		if !ValidOIDCValue(v) {
			t.Errorf("%s %q is not a 32-byte random value", name, v)
		}
	}
	if req.CodeChallenge != CodeChallenge(g.codec.Verifier(req.State, binding)) {
		t.Error("the challenge must be the S256 of the verifier derived from state and binding")
	}
	if !bytes.Equal(flow.StateHash, HashOIDCValue(req.State)) || !bytes.Equal(flow.NonceHash, HashOIDCValue(req.Nonce)) ||
		!bytes.Equal(flow.BrowserBindingHash, HashOIDCValue(binding)) {
		t.Error("the flow must store the hashes of state, nonce and binding")
	}
	now := NowForStorage(g.clock)
	if flow.Purpose != domain.OIDCPurposeLogin || !flow.UserID.IsZero() || !flow.CreatedAt.Equal(now) ||
		!flow.ExpiresAt.Equal(now.Add(10*time.Minute)) || !r.ExpiresAt.Equal(flow.ExpiresAt) {
		t.Errorf("flow = %+v, redirect expires %v", flow, r.ExpiresAt)
	}
	if r.URL != "https://accounts.example/auth?state="+req.State {
		t.Errorf("URL = %q, want the provider's", r.URL)
	}
	if len(g.uow.audit.events) != 0 {
		t.Error("starting a flow writes no audit")
	}
}

func TestStartGoogleLinkNeedsAndBindsTheCaller(t *testing.T) {
	g := newGoogleTestAuth(t)
	_, err := g.auth.StartGoogle(t.Context(), domain.OIDCStart{Purpose: domain.OIDCPurposeLink}, testMeta(t))
	requireCode(t, err, domain.CodeUnauthenticated)

	p := domain.Principal{UserID: mustID(t), SessionID: mustID(t)}
	if _, err := g.auth.StartGoogle(t.Context(), domain.OIDCStart{Purpose: domain.OIDCPurposeLink, Principal: p}, testMeta(t)); err != nil {
		t.Fatal(err)
	}
	if f := g.flows.inserted[len(g.flows.inserted)-1]; f.Purpose != domain.OIDCPurposeLink || f.UserID != p.UserID {
		t.Errorf("link flow = %+v, want it bound to %s", f, p.UserID)
	}
}

func TestStartGoogleWritesNoFlowWhenTheProviderIsDown(t *testing.T) {
	g := newGoogleTestAuth(t)
	g.identity.urlErr = domain.NewError(domain.CodeDependencyUnavailable, "identity provider is unavailable")
	_, err := g.auth.StartGoogle(t.Context(), domain.OIDCStart{Purpose: domain.OIDCPurposeLogin}, testMeta(t))
	requireCode(t, err, domain.CodeDependencyUnavailable)
	if len(g.flows.inserted) != 0 {
		t.Error("a flow must not be stored when the provider cannot build the URL")
	}
}

func TestStartGoogleIsRateLimitedBeforeAnyWork(t *testing.T) {
	g := newGoogleTestAuth(t)
	g.repos.limits.decision = domain.RateLimitDecision{RetryAfter: time.Minute, Scope: domain.RateLimitGoogleStartIP}
	_, err := g.auth.StartGoogle(t.Context(), domain.OIDCStart{Purpose: domain.OIDCPurposeLogin}, testMeta(t))
	requireCode(t, err, domain.CodeRateLimited)
	if len(g.identity.requests) != 0 || len(g.flows.inserted) != 0 {
		t.Error("a refused start must not call the provider or store a flow")
	}
}

func TestNewAuthNeedsTheOIDCCodecWithAProvider(t *testing.T) {
	deps := validAuthDeps(t)
	deps.Identity = &fakeIdentity{}
	if _, err := NewAuth(deps); err == nil {
		t.Error("NewAuth() accepted an identity provider without the OIDC codec")
	}
}
```

(ถ้า `requireCode` ในไฟล์ test เดิมคืนค่าแล้ว lint บังคับให้ใช้ `_ =` ให้ทำตามรูปแบบเดิมของไฟล์ และระบุใน report)

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/core/service/ -run 'Google' -count=1`
Expected: build fail — `unknown field Identity in struct literal of type AuthDeps`, `ta.auth.GoogleEnabled undefined`

- [ ] **Step 3: เขียนโค้ดฝั่ง core**

แก้ `internal/core/port/auth_service.go` — แทน

```go
	Logout(ctx context.Context, p domain.Principal, meta domain.RequestMeta) error
}
```

ด้วย

```go
	Logout(ctx context.Context, p domain.Principal, meta domain.RequestMeta) error
	// GoogleEnabled reports whether Google sign-in is configured.
	GoogleEnabled() bool
	// StartGoogle begins a Google login or, for a signed-in caller, a link. It returns the provider's
	// URL and the browser binding for the cookie. Without Google it returns domain.ErrFeatureDisabled.
	StartGoogle(ctx context.Context, in domain.OIDCStart, meta domain.RequestMeta) (domain.OIDCRedirect, error)
}
```

แก้ `internal/core/service/auth.go` ตามคู่ต่อไปนี้:

1. แทน

```go
	SessionTTL     time.Duration
	OTPMaxAttempts int
}

// Auth implements
```

   ด้วย

```go
	SessionTTL     time.Duration
	OTPMaxAttempts int
	Identity       port.IdentityProvider // nil turns Google sign-in off
	OIDC           *OIDCCodec            // required when Identity is set
}

// Auth implements
```

2. แทน

```go
	sessionTTL     time.Duration
	otpMaxAttempts int
}
```

   ด้วย

```go
	sessionTTL     time.Duration
	otpMaxAttempts int
	identity       port.IdentityProvider
	oidc           *OIDCCodec
}
```

3. แทน

```go
	case d.OTPMaxAttempts < 1 || d.OTPMaxAttempts > domain.MaxOTPFailedAttemptsCeiling:
		return nil, fmt.Errorf("OTP attempts must be between 1 and %d", domain.MaxOTPFailedAttemptsCeiling)
	}
```

   ด้วย

```go
	case d.OTPMaxAttempts < 1 || d.OTPMaxAttempts > domain.MaxOTPFailedAttemptsCeiling:
		return nil, fmt.Errorf("OTP attempts must be between 1 and %d", domain.MaxOTPFailedAttemptsCeiling)
	case d.Identity != nil && d.OIDC == nil:
		return nil, errors.New("google sign-in needs the OIDC codec")
	}
```

4. แทน

```go
		sessionTTL: d.SessionTTL, otpMaxAttempts: d.OTPMaxAttempts,
	}, nil
```

   ด้วย

```go
		sessionTTL: d.SessionTTL, otpMaxAttempts: d.OTPMaxAttempts,
		identity: d.Identity, oidc: d.OIDC,
	}, nil
```

สร้าง `internal/core/service/google.go`:

```go
package service

import (
	"context"

	"go-backend-stock/internal/core/domain"
)

// GoogleEnabled implements port.AuthService.
func (a *Auth) GoogleEnabled() bool { return a.identity != nil }

// StartGoogle implements port.AuthService (milestone 4 design section 6). It builds the provider URL
// before it stores the flow, so an unreachable provider leaves no row behind. Only the hashes of the
// state, the nonce and the browser binding are stored; the PKCE verifier is derived again at the
// callback. Starting a flow writes no audit event.
func (a *Auth) StartGoogle(ctx context.Context, in domain.OIDCStart, meta domain.RequestMeta) (domain.OIDCRedirect, error) {
	if a.identity == nil {
		return domain.OIDCRedirect{}, domain.ErrFeatureDisabled
	}
	var flowUser domain.ID
	switch in.Purpose {
	case domain.OIDCPurposeLogin:
	case domain.OIDCPurposeLink:
		if in.Principal.IsZero() {
			return domain.OIDCRedirect{}, domain.ErrUnauthenticated
		}
		flowUser = in.Principal.UserID
	default:
		return domain.OIDCRedirect{}, domain.NewError(domain.CodeInternal, "unknown Google flow purpose")
	}

	rules, err := a.limits.GoogleStart(meta.ClientIP)
	if err != nil {
		return domain.OIDCRedirect{}, err
	}
	if err := a.admit(ctx, rules, meta, flowUser); err != nil {
		return domain.OIDCRedirect{}, err
	}

	var values [3]string // state, nonce, browser binding
	for i := range values {
		if values[i], err = NewOIDCValue(); err != nil {
			return domain.OIDCRedirect{}, err
		}
	}
	state, nonce, binding := values[0], values[1], values[2]
	url, err := a.identity.AuthorizationURL(ctx, domain.AuthorizationRequest{
		State: state, Nonce: nonce, CodeChallenge: CodeChallenge(a.oidc.Verifier(state, binding)),
	})
	if err != nil {
		return domain.OIDCRedirect{}, err
	}

	id, err := domain.NewID()
	if err != nil {
		return domain.OIDCRedirect{}, err
	}
	now := NowForStorage(a.clock)
	flow, err := domain.NewOIDCFlow(domain.OIDCFlow{
		ID: id, Purpose: in.Purpose, UserID: flowUser,
		StateHash: HashOIDCValue(state), NonceHash: HashOIDCValue(nonce), BrowserBindingHash: HashOIDCValue(binding),
		CreatedAt: now, ExpiresAt: now.Add(oidcFlowLifetime),
	})
	if err != nil {
		return domain.OIDCRedirect{}, err
	}
	if err := a.store.OIDCFlows().Insert(ctx, flow); err != nil {
		return domain.OIDCRedirect{}, err
	}
	return domain.OIDCRedirect{URL: url, BrowserBinding: domain.NewSecretText(binding), ExpiresAt: flow.ExpiresAt}, nil
}
```

ถ้า Task 3 ใส่ `//nolint:unused` ไว้ที่ `oidcFlowLifetime` ใน `internal/core/service/oidc.go` ให้ลบออก

- [ ] **Step 4: รัน test ของ service ให้ผ่าน**

Run: `go test ./internal/core/service/ -count=1 -race`
Expected: PASS

- [ ] **Step 5: เขียน failing tests ฝั่ง HTTP**

แก้ `internal/adapter/http/auth_harness_test.go` ตามคู่ต่อไปนี้:

1. แทน

```go
type harnessConfig struct {
	hasher    service.Argon2Params
	floor     time.Duration
	failAudit bool
}
```

   ด้วย

```go
type harnessConfig struct {
	hasher    service.Argon2Params
	floor     time.Duration
	failAudit bool
	identity  port.IdentityProvider // nil = Google sign-in off
}

// testOIDCSecret keys the PKCE verifiers of every harness, so a flow started on one harness can be
// completed on another that shares the database.
const testOIDCSecret = "iiiiiiiiiiiiiiiiiiiiiiiiiiiiiiii"

func withIdentityProvider(p port.IdentityProvider) harnessOption {
	return func(c *harnessConfig) { c.identity = p }
}
```

2. แทน

```go
	auth, err := service.NewAuth(service.AuthDeps{
		Store: store, UnitOfWork: uow, Clock: clock, Hasher: hasher, OTP: otp, Limits: limits,
		Pacer: pacer, Mailer: mailer, Logger: log, SessionTTL: 7 * 24 * time.Hour, OTPMaxAttempts: 5,
	})
	must(err)
```

   ด้วย

```go
	oidc, err := service.NewOIDCCodec([]byte(testOIDCSecret))
	must(err)
	auth, err := service.NewAuth(service.AuthDeps{
		Store: store, UnitOfWork: uow, Clock: clock, Hasher: hasher, OTP: otp, Limits: limits,
		Pacer: pacer, Mailer: mailer, Logger: log, SessionTTL: 7 * 24 * time.Hour, OTPMaxAttempts: 5,
		Identity: cfg.identity, OIDC: oidc,
	})
	must(err)
```

(ถ้า `newAuthHarness` ปัจจุบันไม่ตรงกับข้อความข้างบนพอดี เช่น รอบแก้ของ 4b เปลี่ยนบรรทัด ให้เพิ่ม `Identity`/`OIDC` ใน `service.AuthDeps` แบบเดียวกันและระบุใน report)

สร้าง `internal/adapter/http/google_fake_test.go`:

```go
package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/service"
)

// fakeIdentityProvider stands in for Google at the port. Exchange accepts only a verifier whose
// S256 matches the challenge of a request it saw, and answers with that request's nonce unless a
// test forces another.
type fakeIdentityProvider struct {
	mu          sync.Mutex
	requests    []domain.AuthorizationRequest
	exchanged   []domain.CodeExchange
	identity    domain.VerifiedIdentity
	urlErr      error
	exchangeErr error
	nonce       string // forced nonce; "" = the nonce of the matching request
}

func newFakeIdentityProvider() *fakeIdentityProvider { return &fakeIdentityProvider{} }

func (f *fakeIdentityProvider) AuthorizationURL(_ context.Context, req domain.AuthorizationRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req) // recorded even on error, so a test can look up the state
	if f.urlErr != nil {
		return "", f.urlErr
	}
	q := url.Values{"state": {req.State}, "nonce": {req.Nonce}, "code_challenge": {req.CodeChallenge}, "code_challenge_method": {"S256"}}
	return "https://accounts.example.test/auth?" + q.Encode(), nil
}

func (f *fakeIdentityProvider) Exchange(_ context.Context, ex domain.CodeExchange) (domain.VerifiedIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchanged = append(f.exchanged, ex)
	if f.exchangeErr != nil {
		return domain.VerifiedIdentity{}, f.exchangeErr
	}
	challenge := service.CodeChallenge(ex.CodeVerifier.Reveal())
	for _, r := range f.requests {
		if r.CodeChallenge == challenge {
			id := f.identity
			id.Nonce = r.Nonce
			if f.nonce != "" {
				id.Nonce = f.nonce
			}
			return id, nil
		}
	}
	return domain.VerifiedIdentity{}, domain.ErrIdentityRejected
}

func (f *fakeIdentityProvider) setIdentity(subject, email string, verified bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identity = domain.VerifiedIdentity{Issuer: "https://accounts.example.test", Subject: subject, Email: email, EmailVerified: verified}
}

func (f *fakeIdentityProvider) setURLError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urlErr = err
}

func (f *fakeIdentityProvider) setExchangeError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchangeErr = err
}

func (f *fakeIdentityProvider) forceNonce(nonce string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nonce = nonce
}

func (f *fakeIdentityProvider) lastRequest(t *testing.T) domain.AuthorizationRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("the identity provider received no authorization request")
	}
	return f.requests[len(f.requests)-1]
}

func (f *fakeIdentityProvider) exchanges() []domain.CodeExchange {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.CodeExchange(nil), f.exchanged...)
}

func (b *browser) startGoogle() (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/google/start", nil, nil)
}

func (b *browser) linkGoogle() (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	return b.do(nethttp.MethodPost, "/api/v1/auth/google/link", nil, nil)
}

func (b *browser) googleCallback(q url.Values) (response, []byte) {
	b.t.Helper()
	return b.do(nethttp.MethodGet, "/api/v1/auth/google/callback?"+q.Encode(), nil, nil)
}

func decodeStart(t *testing.T, body []byte) GoogleStartResponse {
	t.Helper()
	var out GoogleStartResponse
	if err := json.Unmarshal(body, &out); err != nil || out.AuthorizationURL == "" {
		t.Fatalf("start body %s: %v", body, err)
	}
	return out
}

type flowRow struct {
	Purpose  string
	UserID   string // "" when NULL
	Lifetime time.Duration
	Consumed bool
}

func (h *authHarness) flowByState(t *testing.T, state string) (flowRow, bool) {
	t.Helper()
	var r flowRow
	var created, expires time.Time
	err := h.admin.QueryRow(context.Background(),
		`SELECT purpose, COALESCE(user_id::text, ''), created_at, expires_at, consumed_at IS NOT NULL
		 FROM app.oauth_flows WHERE state_hash = $1`, service.HashOIDCValue(state),
	).Scan(&r.Purpose, &r.UserID, &created, &expires, &r.Consumed)
	if err != nil {
		return flowRow{}, false
	}
	r.Lifetime = expires.Sub(created)
	return r, true
}
```

สร้าง `internal/adapter/http/google_start_test.go`:

```go
package http

import (
	"encoding/json"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/service"
)

func TestAuthConfigReportsWhetherGoogleIsOn(t *testing.T) {
	for name, h := range map[string]*authHarness{
		"off": newAuthHarness(t),
		"on":  newAuthHarness(t, withIdentityProvider(newFakeIdentityProvider())),
	} {
		resp, body := h.newBrowser(t).do(nethttp.MethodGet, "/api/v1/auth/config", nil, nil)
		var out AuthConfigResponse
		if err := json.Unmarshal(body, &out); err != nil || resp.StatusCode != nethttp.StatusOK {
			t.Fatalf("%s: %d %s", name, resp.StatusCode, body)
		}
		if out.GoogleEnabled != (name == "on") || !strings.Contains(string(body), `"google_enabled"`) {
			t.Errorf("%s: body %s", name, body)
		}
	}
}

func TestGoogleWhenDisabledIs503(t *testing.T) {
	h := newAuthHarness(t)
	b := h.newBrowser(t)
	resp, body := b.startGoogle()
	if resp.StatusCode != nethttp.StatusServiceUnavailable || decodeError(t, body).Code != "feature_disabled" {
		t.Errorf("start = %d %s, want 503 feature_disabled", resp.StatusCode, body)
	}
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	b.login(email, "Zq9!kVx1")
	resp, body = b.linkGoogle()
	if resp.StatusCode != nethttp.StatusServiceUnavailable || decodeError(t, body).Code != "feature_disabled" {
		t.Errorf("link = %d %s, want 503 feature_disabled", resp.StatusCode, body)
	}
}

func TestGoogleStartReturnsAnAuthorizationURLAndABindingCookie(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	b := h.newBrowser(t)

	resp, body := b.startGoogle()
	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("start = %d %s", resp.StatusCode, body)
	}
	out := decodeStart(t, body)
	req := f.lastRequest(t)
	if !strings.Contains(out.AuthorizationURL, "state="+req.State) || resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("url = %s, Cache-Control = %q", out.AuthorizationURL, resp.Header.Get("Cache-Control"))
	}
	raw := strings.ToLower(strings.Join(resp.Header.Values("Set-Cookie"), "\n"))
	for _, want := range []string{"oidc_binding=", "httponly", "samesite=lax", "path=/", "expires="} {
		if !strings.Contains(raw, want) {
			t.Errorf("Set-Cookie %q lacks %q", raw, want)
		}
	}
	binding := b.jar["oidc_binding"]
	codec, err := service.NewOIDCCodec([]byte(testOIDCSecret))
	if err != nil {
		t.Fatal(err)
	}
	if !service.ValidOIDCValue(binding) || req.CodeChallenge != service.CodeChallenge(codec.Verifier(req.State, binding)) {
		t.Error("the challenge must come from the state and the binding the browser holds")
	}
	flow, ok := h.flowByState(t, req.State)
	if !ok || flow.Purpose != "login" || flow.UserID != "" || flow.Lifetime != 10*time.Minute || flow.Consumed {
		t.Errorf("flow = %+v (found %v), want an open 10-minute login flow", flow, ok)
	}
	if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 0 {
		t.Errorf("starting a flow writes no audit, got %v", eventTypes(rows))
	}
}

func TestGoogleLinkNeedsASignedInUserAndBindsTheFlow(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	b := h.newBrowser(t)
	resp, body := b.linkGoogle()
	if resp.StatusCode != nethttp.StatusUnauthorized || decodeError(t, body).Code != "unauthenticated" {
		t.Fatalf("anonymous link = %d %s, want 401", resp.StatusCode, body)
	}
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	b.login(email, "Zq9!kVx1")
	u, _ := h.user(t, email)
	if resp, body := b.linkGoogle(); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("link = %d %s", resp.StatusCode, body)
	}
	if flow, ok := h.flowByState(t, f.lastRequest(t).State); !ok || flow.Purpose != "link" || flow.UserID != u.ID {
		t.Errorf("flow = %+v, want a link flow of the signed-in user", flow)
	}
}

func TestGoogleStartNeedsCSRF(t *testing.T) {
	h := newAuthHarness(t, withIdentityProvider(newFakeIdentityProvider()))
	b := h.newBrowser(t)
	b.fetchCSRF()
	resp, body := b.do(nethttp.MethodPost, "/api/v1/auth/google/start", nil, map[string]string{"X-CSRF-Token": ""})
	if resp.StatusCode != nethttp.StatusForbidden || decodeError(t, body).Code != "csrf_failed" {
		t.Errorf("start without a token = %d %s, want 403 csrf_failed", resp.StatusCode, body)
	}
}

func TestGoogleStartWhenTheProviderIsDownIs503(t *testing.T) {
	f := newFakeIdentityProvider()
	f.setURLError(domain.NewError(domain.CodeDependencyUnavailable, "identity provider is unavailable"))
	h := newAuthHarness(t, withIdentityProvider(f))
	resp, body := h.newBrowser(t).startGoogle()
	if resp.StatusCode != nethttp.StatusServiceUnavailable || decodeError(t, body).Code != "dependency_unavailable" {
		t.Fatalf("start = %d %s, want 503 dependency_unavailable", resp.StatusCode, body)
	}
	if _, ok := h.flowByState(t, f.lastRequest(t).State); ok {
		t.Error("no flow may be stored when the provider is down")
	}
}

func TestGoogleStartIsRateLimitedPerIP(t *testing.T) {
	h := newAuthHarness(t, withIdentityProvider(newFakeIdentityProvider()))
	b := h.newBrowser(t)
	for i := range 20 {
		if resp, body := b.startGoogle(); resp.StatusCode != nethttp.StatusOK {
			t.Fatalf("start %d = %d %s", i+1, resp.StatusCode, body)
		}
	}
	resp, body := b.startGoogle()
	if resp.StatusCode != nethttp.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("start 21 = %d %s, want 429 with Retry-After", resp.StatusCode, body)
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "security.rate_limited" || rows[0].Metadata["scope"] != "google_start_ip" {
		t.Errorf("audit = %+v, want security.rate_limited for google_start_ip", rows)
	}
}
```

แก้ `internal/adapter/http/auth_contract_test.go` — แทน

```go
	{"post", "/api/v1/auth/logout", []int{204, 403, 500, 503}},
}
```

ด้วย

```go
	{"post", "/api/v1/auth/logout", []int{204, 403, 500, 503}},
	{"get", "/api/v1/auth/config", []int{200}},
	{"post", "/api/v1/auth/google/start", []int{200, 403, 429, 500, 503}},
	{"post", "/api/v1/auth/google/link", []int{200, 401, 403, 429, 500, 503}},
}
```

และในตาราง `requiredAuthFields` ของไฟล์เดียวกัน แทน

```go
	"http.ErrorBody":           {"code", "message", "request_id"},
}
```

ด้วย

```go
	"http.ErrorBody":           {"code", "message", "request_id"},
	"http.AuthConfigResponse":  {"google_enabled"},
	"http.GoogleStartResponse": {"authorization_url"},
}
```

- [ ] **Step 6: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/adapter/http/ -count=1`
Expected: build fail — `undefined: AuthConfigResponse`, `GoogleStartResponse`

- [ ] **Step 7: เขียนโค้ดฝั่ง HTTP**

แก้ `internal/adapter/http/cookies.go` ตามคู่ต่อไปนี้:

1. แทน

```go
	sessionName string
	nonceName   string
}
```

   ด้วย

```go
	sessionName string
	nonceName   string
	bindingName string // the browser binding of a Google flow
}
```

2. แทน

```go
		return cookieJar{secure: true, sessionName: "__Host-session", nonceName: "__Host-csrf_nonce"}
	}
	return cookieJar{sessionName: "session", nonceName: "csrf_nonce"}
```

   ด้วย

```go
		return cookieJar{secure: true, sessionName: "__Host-session", nonceName: "__Host-csrf_nonce", bindingName: "__Host-oidc_binding"}
	}
	return cookieJar{sessionName: "session", nonceName: "csrf_nonce", bindingName: "oidc_binding"}
```

ต่อท้าย `internal/adapter/http/auth_dto.go` ด้วย:

```go

// AuthConfigResponse tells a client which sign-in methods this server offers.
type AuthConfigResponse struct {
	GoogleEnabled bool `json:"google_enabled" example:"true" validate:"required"`
}

// GoogleStartResponse is where to send the browser to sign in with Google.
type GoogleStartResponse struct {
	AuthorizationURL string `json:"authorization_url" example:"https://accounts.google.com/o/oauth2/v2/auth?client_id=...&code_challenge=...&code_challenge_method=S256&nonce=...&redirect_uri=...&response_type=code&scope=openid+email&state=..." validate:"required"`
}
```

แก้ `internal/adapter/http/auth.go` — แทน

```go
	app.Post("/api/v1/auth/logout", session, csrf, h.logout)
```

ด้วย

```go
	app.Post("/api/v1/auth/logout", session, csrf, h.logout)
	app.Get("/api/v1/auth/config", h.authConfig)
	app.Post("/api/v1/auth/google/start", session, csrf, h.googleStart)
	app.Post("/api/v1/auth/google/link", session, middleware.RequireAuth(), csrf, h.googleLink)
```

สร้าง `internal/adapter/http/google.go`:

```go
package http

import (
	"github.com/gofiber/fiber/v3"

	"go-backend-stock/internal/adapter/http/middleware"
	"go-backend-stock/internal/core/domain"
)

// AuthConfig godoc
// @Summary      Get the sign-in options
// @Description  Tells a client whether Google sign-in is configured. When google_enabled is false, the Google endpoints answer 503 feature_disabled.
// @Tags         Auth
// @ID           getAuthConfig
// @Produce      json
// @Success      200  {object}  AuthConfigResponse
// @Router       /api/v1/auth/config [get]
func (h *authHandlers) authConfig(c fiber.Ctx) error {
	return c.JSON(AuthConfigResponse{GoogleEnabled: h.auth.GoogleEnabled()})
}

// GoogleStart godoc
// @Summary      Start signing in with Google
// @Description  Returns the Google URL to open in the same browser, and sets the HttpOnly cookie oidc_binding (__Host-oidc_binding over HTTPS) that must come back with the callback; the flow lasts 10 minutes. Google sends the browser to GET /api/v1/auth/google/callback, which ends at /docs/?google=success|link_required|failed. A Google account whose email already belongs to an account is not linked automatically: sign in to that account and use POST /api/v1/auth/google/link. Needs X-CSRF-Token and the csrf_nonce cookie. Swagger UI and curl cannot complete the Google page; open authorization_url in the browser.
// @Tags         Auth
// @ID           startGoogleSignIn
// @Produce      json
// @Param        X-CSRF-Token  header  string  true  "Token from GET /api/v1/auth/csrf"
// @Success      200  {object}  GoogleStartResponse
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Failure      500  {object}  ErrorResponse  "internal"
// @Failure      503  {object}  ErrorResponse  "feature_disabled (Google is not configured) or dependency_unavailable (Google cannot be reached)"
// @Header       200  {string}   Set-Cookie   "oidc_binding=…; Path=/; Expires=…; HttpOnly; SameSite=Lax"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Router       /api/v1/auth/google/start [post]
func (h *authHandlers) googleStart(c fiber.Ctx) error {
	return h.startGoogle(c, domain.OIDCPurposeLogin)
}

// GoogleLink godoc
// @Summary      Link Google to the signed-in account
// @Description  Like POST /api/v1/auth/google/start, but the flow belongs to the signed-in user: after Google, the callback adds the Google account to this account (/docs/?google=linked) without opening a new session. The callback must come with the same session. Needs the session cookie and an X-CSRF-Token bound to it.
// @Tags         Auth
// @ID           linkGoogleAccount
// @Produce      json
// @Param        X-CSRF-Token  header  string  true  "Token from GET /api/v1/auth/csrf, fetched while signed in"
// @Success      200  {object}  GoogleStartResponse
// @Failure      401  {object}  ErrorResponse  "unauthenticated"
// @Failure      403  {object}  ErrorResponse  "csrf_failed"
// @Failure      429  {object}  ErrorResponse  "rate_limited; wait Retry-After seconds"
// @Failure      500  {object}  ErrorResponse  "internal"
// @Failure      503  {object}  ErrorResponse  "feature_disabled or dependency_unavailable"
// @Header       200  {string}   Set-Cookie   "oidc_binding=…; Path=/; Expires=…; HttpOnly; SameSite=Lax"
// @Header       429  {integer}  Retry-After  "Seconds to wait"
// @Router       /api/v1/auth/google/link [post]
func (h *authHandlers) googleLink(c fiber.Ctx) error {
	return h.startGoogle(c, domain.OIDCPurposeLink)
}

func (h *authHandlers) startGoogle(c fiber.Ctx, purpose domain.OIDCPurpose) error {
	p, _ := middleware.GetPrincipal(c)
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	r, err := h.auth.StartGoogle(c.Context(), domain.OIDCStart{Purpose: purpose, Principal: p}, meta)
	if err != nil {
		return err
	}
	h.cookies.set(c, h.cookies.bindingName, r.BrowserBinding.Reveal(), r.ExpiresAt)
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(GoogleStartResponse{AuthorizationURL: r.URL})
}
```

- [ ] **Step 8: รัน test ให้ผ่าน และ generate Swagger**

Run:
```bash
docker compose ps
gofmt -l internal; go vet ./... && make swagger && go test ./internal/... -count=1 -race && make -s test-integration && make lint
```
Expected: ผ่านทั้งหมด; test ใน `google_start_test.go` ทำงานจริง (ไม่ SKIP); ถ้า `docker compose ps` ไม่เห็น postgres ให้หยุดแล้วรายงาน

- [ ] **Step 9: Commit**

```bash
git add internal/core/port/auth_service.go internal/core/service/auth.go internal/core/service/oidc.go internal/core/service/google.go internal/core/service/google_test.go internal/adapter/http docs/api
git commit -m "feat(auth): start Google sign-in and linking, and report it in GET /auth/config"
```

---

### Task 6: `CompleteGoogle` และ `GET /auth/google/callback`

**Files:**
- Create: `internal/core/service/session_grant.go`, `internal/adapter/http/google_callback_test.go`
- Modify: `internal/core/port/auth_service.go`, `internal/core/service/google.go`, `internal/core/service/login.go`, `internal/core/service/audit.go`, `internal/adapter/http/google.go`, `internal/adapter/http/auth.go`, `internal/adapter/http/auth_contract_test.go`, `docs/api/` (generated)

**Interfaces:**
- Consumes: Task 2–5 ทั้งหมด; `(*Auth).recordFailure`, `appendAudit`, `auditSpec`; repositories `OIDCFlows().Consume`, `Identities().BySubject`/`Insert`, `Users().Insert`/`Lock`, `Sessions().Insert`; `domain.NewAuthProvider`, `domain.ParseEmail`; harness + `fakeIdentityProvider` + browser helpers (Task 5)
- Produces:
  - `port.AuthService` เพิ่ม `CompleteGoogle(ctx, in domain.OIDCCallback, meta domain.RequestMeta) (domain.OIDCResult, error)`
  - `(*Auth).issueSession(userID domain.ID, now time.Time) (domain.Session, domain.SecretText, error)` (Login ใช้ด้วย)
  - `auditSpec.Provider` และ metadata `provider` ใน allowlist
  - route `GET /api/v1/auth/google/callback` → 303 `Location: /docs/?google=<outcome>`; `googleResultPath = "/docs/?google="`
  - test helpers `signInWithGoogle`, `requireGoogleRedirect`, `identityOf`, `uniqueSubject` (ใช้ต่อใน Task 7–8)

- [ ] **Step 1: เขียน failing tests**

แก้ `internal/adapter/http/auth_contract_test.go` — แทน

```go
	{"post", "/api/v1/auth/google/link", []int{200, 401, 403, 429, 500, 503}},
}
```

ด้วย

```go
	{"post", "/api/v1/auth/google/link", []int{200, 401, 403, 429, 500, 503}},
	{"get", "/api/v1/auth/google/callback", []int{303, 500, 503}},
}
```

สร้าง `internal/adapter/http/google_callback_test.go`:

```go
package http

import (
	"context"
	nethttp "net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/service"
)

func uniqueSubject(t *testing.T) string {
	t.Helper()
	return "google-" + mustTestID(t).String()
}

// signInWithGoogle starts a flow in b and comes back from Google with a code, as Google would after
// the user agreed. f decides the identity.
func (b *browser) signInWithGoogle(f *fakeIdentityProvider) (response, []byte) {
	b.t.Helper()
	if resp, body := b.startGoogle(); resp.StatusCode != nethttp.StatusOK {
		b.t.Fatalf("start = %d %s", resp.StatusCode, body)
	}
	return b.googleCallback(url.Values{"code": {"code-" + mustTestID(b.t).String()}, "state": {f.lastRequest(b.t).State}})
}

func requireGoogleRedirect(t *testing.T, resp response, body []byte, outcome domain.OIDCOutcome) {
	t.Helper()
	if resp.StatusCode != nethttp.StatusSeeOther || resp.Header.Get("Location") != "/docs/?google="+string(outcome) {
		t.Fatalf("callback = %d Location %q (%s), want 303 to /docs/?google=%s", resp.StatusCode, resp.Header.Get("Location"), body, outcome)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", resp.Header.Get("Cache-Control"))
	}
}

// identityOf returns the user a Google subject is linked to.
func (h *authHarness) identityOf(t *testing.T, subject string) (string, bool) {
	t.Helper()
	var userID string
	err := h.admin.QueryRow(context.Background(),
		`SELECT user_id::text FROM app.auth_providers WHERE provider = 'google' AND provider_subject = $1`, subject).Scan(&userID)
	return userID, err == nil
}

func TestGoogleSignInCreatesAVerifiedAccount(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	b := h.newBrowser(t)
	email, sub := uniqueEmail(t), uniqueSubject(t)
	f.setIdentity(sub, strings.ToUpper(email), true)

	resp, body := b.signInWithGoogle(f)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeSuccess)
	if b.jar["session"] == "" || b.jar["oidc_binding"] != "" || b.jar["csrf_nonce"] != "" {
		t.Errorf("cookies = %v, want a session and neither the binding nor the nonce", b.jar)
	}
	u, ok := h.user(t, email)
	if !ok || !u.Verified || u.PasswordHash != "" {
		t.Fatalf("user = %+v (found %v), want a verified account without a password under the lower-cased email", u, ok)
	}
	if owner, ok := h.identityOf(t, sub); !ok || owner != u.ID {
		t.Errorf("identity owner = %q (found %v), want %s", owner, ok, u.ID)
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if got := eventTypes(rows); !slices.Equal(got, []string{"auth.registered", "auth.login_succeeded"}) {
		t.Fatalf("audit = %v", got)
	}
	for _, r := range rows {
		if r.Actor != u.ID || r.Metadata["method"] != "google" || r.Outcome != "success" {
			t.Errorf("audit row %+v: want actor the new user and method google", r)
		}
	}
	if rows[1].SessionID == "" {
		t.Error("auth.login_succeeded must name the session")
	}
	if resp, body := b.me(); resp.StatusCode != nethttp.StatusOK || decodeUser(t, body).ID != u.ID {
		t.Errorf("me = %d %s", resp.StatusCode, body)
	}
	if flow, _ := h.flowByState(t, f.lastRequest(t).State); !flow.Consumed {
		t.Error("the flow must be consumed")
	}
}

func TestGoogleSignInOfALinkedAccountOpensASession(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	email, sub := uniqueEmail(t), uniqueSubject(t)
	f.setIdentity(sub, email, true)
	first, body := h.newBrowser(t).signInWithGoogle(f)
	requireGoogleRedirect(t, first, body, domain.OIDCOutcomeSuccess)
	u, _ := h.user(t, email)

	f.setIdentity(sub, "changed-"+email, true) // Google sign-in follows the subject, not the email
	b := h.newBrowser(t)
	resp, body := b.signInWithGoogle(f)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeSuccess)
	if resp, body := b.me(); resp.StatusCode != nethttp.StatusOK || decodeUser(t, body).ID != u.ID {
		t.Errorf("me = %d %s, want the account linked to the subject", resp.StatusCode, body)
	}
	if _, ok := h.user(t, "changed-"+email); ok {
		t.Error("a second account must not be created")
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if !slices.Equal(eventTypes(rows), []string{"auth.login_succeeded"}) || rows[0].Actor != u.ID {
		t.Errorf("audit = %+v", rows)
	}
}

func TestGoogleSignInWithTheEmailOfAnAccountNeedsALink(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	email, sub := uniqueEmail(t), uniqueSubject(t)
	h.newBrowser(t).signUpAndVerify(email, "Zq9!kVx1")
	u, _ := h.user(t, email)
	f.setIdentity(sub, email, true)

	b := h.newBrowser(t)
	resp, body := b.signInWithGoogle(f)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeLinkRequired)
	if b.jar["session"] != "" || h.sessionsOf(t, u.ID) != 0 {
		t.Error("link_required must not open a session")
	}
	if _, ok := h.identityOf(t, sub); ok {
		t.Error("an email match must never link automatically")
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "auth.login_failed" || rows[0].Reason != "link_required" ||
		rows[0].Actor != "" || rows[0].Metadata["method"] != "google" {
		t.Errorf("audit = %+v, want login_failed link_required with actor NULL", rows)
	}
}

func TestGoogleSignInRefusesAnUnverifiedEmail(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	email := uniqueEmail(t)
	f.setIdentity(uniqueSubject(t), email, false)
	resp, body := h.newBrowser(t).signInWithGoogle(f)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
	if _, ok := h.user(t, email); ok {
		t.Error("an unverified Google email must not create an account")
	}
	if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 1 || rows[0].Reason != "email_not_verified" {
		t.Errorf("audit = %+v", rows)
	}
}

func TestGoogleCallbackRejectsAReplay(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	f.setIdentity(uniqueSubject(t), email, true)
	if resp, body := b.startGoogle(); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("start = %d %s", resp.StatusCode, body)
	}
	binding := b.jar["oidc_binding"]
	q := url.Values{"code": {"code-1"}, "state": {f.lastRequest(t).State}}
	first, body := b.googleCallback(q)
	requireGoogleRedirect(t, first, body, domain.OIDCOutcomeSuccess)
	u, _ := h.user(t, email)

	b.jar["oidc_binding"] = binding // the browser goes back in history
	again, body := b.googleCallback(q)
	requireGoogleRedirect(t, again, body, domain.OIDCOutcomeFailed)
	if len(f.exchanges()) != 1 || h.sessionsOf(t, u.ID) != 1 {
		t.Errorf("exchanges = %d, sessions = %d; a replay must not reach the provider or open a session", len(f.exchanges()), h.sessionsOf(t, u.ID))
	}
	if rows := h.auditFor(t, again.Header.Get("X-Request-ID")); len(rows) != 0 {
		t.Errorf("a replay is not audited, got %v", eventTypes(rows))
	}
}

func TestGoogleCallbackNeedsTheBrowserThatStartedIt(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	f.setIdentity(uniqueSubject(t), uniqueEmail(t), true)
	owner := h.newBrowser(t)
	if resp, body := owner.startGoogle(); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("start = %d %s", resp.StatusCode, body)
	}
	q := url.Values{"code": {"code-1"}, "state": {f.lastRequest(t).State}}

	other := h.newBrowser(t)
	resp, body := other.googleCallback(q)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
	other.jar["oidc_binding"] = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // a well-formed binding of nobody
	resp, body = other.googleCallback(q)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
	if len(f.exchanges()) != 0 {
		t.Error("a callback from another browser must not reach the provider")
	}

	resp, body = owner.googleCallback(q)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeSuccess) // the owner's flow is untouched
}

func TestGoogleCallbackAfterTheFlowExpired(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	b := h.newBrowser(t)
	f.setIdentity(uniqueSubject(t), uniqueEmail(t), true)
	if resp, body := b.startGoogle(); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("start = %d %s", resp.StatusCode, body)
	}
	h.clock.Advance(10 * time.Minute) // exactly at expires_at the flow is over
	resp, body := b.googleCallback(url.Values{"code": {"code-1"}, "state": {f.lastRequest(t).State}})
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
	if len(f.exchanges()) != 0 || b.jar["session"] != "" {
		t.Error("an expired flow must not reach the provider or open a session")
	}
}

func TestGoogleCallbackWithAProviderErrorEndsTheFlow(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	b := h.newBrowser(t)
	f.setIdentity(uniqueSubject(t), uniqueEmail(t), true)
	if resp, body := b.startGoogle(); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("start = %d %s", resp.StatusCode, body)
	}
	state := f.lastRequest(t).State
	binding := b.jar["oidc_binding"]
	resp, body := b.googleCallback(url.Values{"error": {"access_denied"}, "state": {state}})
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
	if strings.Contains(resp.Header.Get("Location"), "access_denied") || strings.Contains(string(body), "access_denied") {
		t.Error("the provider's error must not be echoed")
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "auth.login_failed" || rows[0].Reason != "provider_error" {
		t.Errorf("audit = %+v", rows)
	}
	b.jar["oidc_binding"] = binding
	resp, body = b.googleCallback(url.Values{"code": {"code-1"}, "state": {state}})
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
	if len(f.exchanges()) != 0 {
		t.Error("a flow ended by a provider error cannot be completed afterwards")
	}
}

func TestGoogleCallbackRejectsANonceMismatch(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	email := uniqueEmail(t)
	f.setIdentity(uniqueSubject(t), email, true)
	f.forceNonce("a-nonce-from-another-flow")
	b := h.newBrowser(t)
	resp, body := b.signInWithGoogle(f)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
	if _, ok := h.user(t, email); ok || b.jar["session"] != "" {
		t.Error("a token for another flow must not sign in")
	}
	if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 1 || rows[0].Reason != "nonce_mismatch" {
		t.Errorf("audit = %+v", rows)
	}
}

func TestGoogleCallbackRejectsAnExchangeTheProviderRefused(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	f.setExchangeError(domain.ErrIdentityRejected)
	resp, body := h.newBrowser(t).signInWithGoogle(f)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
	if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 1 || rows[0].Reason != "exchange_rejected" || rows[0].Actor != "" {
		t.Errorf("audit = %+v", rows)
	}
}

func TestGoogleCallbackSendsTheDerivedVerifier(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	b := h.newBrowser(t)
	f.setIdentity(uniqueSubject(t), uniqueEmail(t), true)
	if resp, body := b.startGoogle(); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("start = %d %s", resp.StatusCode, body)
	}
	state, binding := f.lastRequest(t).State, b.jar["oidc_binding"]
	resp, body := b.googleCallback(url.Values{"code": {"the-code"}, "state": {state}})
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeSuccess)
	codec, err := service.NewOIDCCodec([]byte(testOIDCSecret))
	if err != nil {
		t.Fatal(err)
	}
	ex := f.exchanges()
	if len(ex) != 1 || ex[0].Code.Reveal() != "the-code" || ex[0].CodeVerifier.Reveal() != codec.Verifier(state, binding) {
		t.Errorf("exchange = %d calls, want the code and the verifier derived from state and binding", len(ex))
	}
}

func TestGoogleCallbackWhenTheProviderIsDownIs503(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	f.setExchangeError(domain.NewError(domain.CodeDependencyUnavailable, "identity provider is unavailable"))
	b := h.newBrowser(t)
	resp, body := b.signInWithGoogle(f)
	if resp.StatusCode != nethttp.StatusServiceUnavailable || decodeError(t, body).Code != "dependency_unavailable" {
		t.Fatalf("callback = %d %s, want 503 dependency_unavailable", resp.StatusCode, body)
	}
	if b.jar["oidc_binding"] != "" {
		t.Error("the binding cookie is cleared on every outcome")
	}
}

func TestGoogleCallbackWithoutAFlowIsFailedWithoutAudit(t *testing.T) {
	h := newAuthHarness(t, withIdentityProvider(newFakeIdentityProvider()))
	for name, q := range map[string]url.Values{
		"nothing":       {},
		"junk state":    {"code": {"x"}, "state": {"<script>"}},
		"unknown state": {"code": {"x"}, "state": {"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}},
	} {
		resp, body := h.newBrowser(t).googleCallback(q)
		requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
		if rows := h.auditFor(t, resp.Header.Get("X-Request-ID")); len(rows) != 0 {
			t.Errorf("%s: audit = %v, want none", name, eventTypes(rows))
		}
	}
	if !strings.Contains(h.logs.String(), `"event":"auth.oidc_flow_not_found"`) {
		t.Error("an unmatched callback is logged")
	}
}

func TestGoogleCallbackWhenDisabledIs503(t *testing.T) {
	h := newAuthHarness(t)
	resp, body := h.newBrowser(t).googleCallback(url.Values{"code": {"x"}, "state": {"y"}})
	if resp.StatusCode != nethttp.StatusServiceUnavailable || decodeError(t, body).Code != "feature_disabled" {
		t.Errorf("callback = %d %s, want 503 feature_disabled", resp.StatusCode, body)
	}
}

func TestGoogleSignInFromTwoTabsMakesOneAccount(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	email, sub := uniqueEmail(t), uniqueSubject(t)
	f.setIdentity(sub, email, true)
	tabs := []*browser{h.newBrowser(t), h.newBrowser(t)}
	states := make([]string, len(tabs))
	for i, b := range tabs {
		if resp, body := b.startGoogle(); resp.StatusCode != nethttp.StatusOK {
			t.Fatalf("start %d = %d %s", i, resp.StatusCode, body)
		}
		states[i] = f.lastRequest(t).State
	}

	var wg sync.WaitGroup
	results := make([]response, len(tabs))
	errs := make([]error, len(tabs))
	for i, b := range tabs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := url.Values{"code": {"code"}, "state": {states[i]}}
			results[i], _, errs[i] = b.tryDo(nethttp.MethodGet, "/api/v1/auth/google/callback?"+q.Encode(), nil, nil)
		}()
	}
	wg.Wait()
	for i := range tabs {
		if errs[i] != nil {
			t.Fatalf("tab %d: %v", i, errs[i])
		}
		if results[i].StatusCode != nethttp.StatusSeeOther || results[i].Header.Get("Location") != "/docs/?google=success" {
			t.Errorf("tab %d = %d %q, want both tabs signed in", i, results[i].StatusCode, results[i].Header.Get("Location"))
		}
	}
	var users, links int
	if err := h.admin.QueryRow(t.Context(), `SELECT count(*) FROM app.users WHERE email = $1`, email).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := h.admin.QueryRow(t.Context(), `SELECT count(*) FROM app.auth_providers WHERE provider_subject = $1`, sub).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if users != 1 || links != 1 {
		t.Errorf("users = %d, identities = %d; want one of each", users, links)
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./internal/adapter/http/ -count=1`
Expected: test compile ผ่านแต่ `make -s test-integration` ล้มที่ callback (404 เพราะยังไม่มี route) และ `TestAuthOperationsAreDocumented` ล้มที่ `/api/v1/auth/google/callback`; รัน `make -s test-integration` เพื่อเห็นผลนั้น

- [ ] **Step 3: เขียนโค้ด**

แก้ `internal/core/port/auth_service.go` — แทน

```go
	StartGoogle(ctx context.Context, in domain.OIDCStart, meta domain.RequestMeta) (domain.OIDCRedirect, error)
}
```

ด้วย

```go
	StartGoogle(ctx context.Context, in domain.OIDCStart, meta domain.RequestMeta) (domain.OIDCRedirect, error)
	// CompleteGoogle ends a flow at the callback. Every refusal is an OIDCResult with outcome
	// failed (audited when a flow was found); only infrastructure failures return an error.
	CompleteGoogle(ctx context.Context, in domain.OIDCCallback, meta domain.RequestMeta) (domain.OIDCResult, error)
}
```

แก้ `internal/core/service/audit.go` ตามคู่ต่อไปนี้:

1. แทน

```go
	Method     string // "password" or "google" for login events
	Scope      domain.RateLimitScope
}
```

   ด้วย

```go
	Method     string // "password" or "google" for login events
	Provider   string // "google" for identity events
	Scope      domain.RateLimitScope
}
```

2. แทน

```go
	Method    string `json:"method,omitempty"`
	Scope     string `json:"scope,omitempty"`
}
```

   ด้วย

```go
	Method    string `json:"method,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Scope     string `json:"scope,omitempty"`
}
```

3. แทน

```go
	metadata, err := json.Marshal(auditMetadata{IP: meta.MaskedIP, UserAgent: meta.UserAgent, Method: s.Method, Scope: string(s.Scope)})
```

   ด้วย

```go
	metadata, err := json.Marshal(auditMetadata{IP: meta.MaskedIP, UserAgent: meta.UserAgent, Method: s.Method, Provider: s.Provider, Scope: string(s.Scope)})
```

(ถ้า `TestAuditMetadataKeepsOnlyTheAllowlist` ตรวจ key ทั้งหมดของ metadata จะยังผ่าน เพราะ `provider` ว่างแล้วถูกตัดด้วย `omitempty`)

สร้าง `internal/core/service/session_grant.go`:

```go
package service

import (
	"time"

	"go-backend-stock/internal/core/domain"
)

// issueSession builds a session of userID that starts at now and lasts the session lifetime. Only
// its SHA-256 is stored; the raw token goes into the cookie and nowhere else.
func (a *Auth) issueSession(userID domain.ID, now time.Time) (domain.Session, domain.SecretText, error) {
	token, hash, err := NewSessionToken()
	if err != nil {
		return domain.Session{}, domain.SecretText{}, err
	}
	id, err := domain.NewID()
	if err != nil {
		return domain.Session{}, domain.SecretText{}, err
	}
	s, err := domain.NewSession(domain.Session{ID: id, UserID: userID, TokenHash: hash, CreatedAt: now, ExpiresAt: now.Add(a.sessionTTL)})
	if err != nil {
		return domain.Session{}, domain.SecretText{}, err
	}
	return s, domain.NewSecretText(token), nil
}
```

แก้ `internal/core/service/login.go` ตามคู่ต่อไปนี้ (ให้ Login ใช้ `issueSession` แทนโค้ดชุดเดียวกัน):

1. แทน

```go
	token, tokenHash, err := NewSessionToken()
	if err != nil {
		return domain.SessionGrant{}, err
	}
	sessionID, err := domain.NewID()
	if err != nil {
		return domain.SessionGrant{}, err
	}
	now := NowForStorage(a.clock)
	session, err := domain.NewSession(domain.Session{
		ID: sessionID, UserID: creds.User.ID, TokenHash: tokenHash, CreatedAt: now, ExpiresAt: now.Add(a.sessionTTL),
	})
	if err != nil {
		return domain.SessionGrant{}, err
	}
```

   ด้วย

```go
	now := NowForStorage(a.clock)
	session, token, err := a.issueSession(creds.User.ID, now)
	if err != nil {
		return domain.SessionGrant{}, err
	}
```

2. แทน

```go
	return domain.SessionGrant{User: creds.User, Token: domain.NewSecretText(token), ExpiresAt: session.ExpiresAt}, nil
```

   ด้วย

```go
	return domain.SessionGrant{User: creds.User, Token: token, ExpiresAt: session.ExpiresAt}, nil
```

แทนเนื้อหาทั้งไฟล์ `internal/core/service/google.go` ด้วย:

```go
package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"

	"go-backend-stock/internal/core/domain"
	"go-backend-stock/internal/core/port"
)

// GoogleEnabled implements port.AuthService.
func (a *Auth) GoogleEnabled() bool { return a.identity != nil }

// StartGoogle implements port.AuthService (milestone 4 design section 6). It builds the provider URL
// before it stores the flow, so an unreachable provider leaves no row behind. Only the hashes of the
// state, the nonce and the browser binding are stored; the PKCE verifier is derived again at the
// callback. Starting a flow writes no audit event.
func (a *Auth) StartGoogle(ctx context.Context, in domain.OIDCStart, meta domain.RequestMeta) (domain.OIDCRedirect, error) {
	if a.identity == nil {
		return domain.OIDCRedirect{}, domain.ErrFeatureDisabled
	}
	var flowUser domain.ID
	switch in.Purpose {
	case domain.OIDCPurposeLogin:
	case domain.OIDCPurposeLink:
		if in.Principal.IsZero() {
			return domain.OIDCRedirect{}, domain.ErrUnauthenticated
		}
		flowUser = in.Principal.UserID
	default:
		return domain.OIDCRedirect{}, domain.NewError(domain.CodeInternal, "unknown Google flow purpose")
	}

	rules, err := a.limits.GoogleStart(meta.ClientIP)
	if err != nil {
		return domain.OIDCRedirect{}, err
	}
	if err := a.admit(ctx, rules, meta, flowUser); err != nil {
		return domain.OIDCRedirect{}, err
	}

	var values [3]string // state, nonce, browser binding
	for i := range values {
		if values[i], err = NewOIDCValue(); err != nil {
			return domain.OIDCRedirect{}, err
		}
	}
	state, nonce, binding := values[0], values[1], values[2]
	url, err := a.identity.AuthorizationURL(ctx, domain.AuthorizationRequest{
		State: state, Nonce: nonce, CodeChallenge: CodeChallenge(a.oidc.Verifier(state, binding)),
	})
	if err != nil {
		return domain.OIDCRedirect{}, err
	}

	id, err := domain.NewID()
	if err != nil {
		return domain.OIDCRedirect{}, err
	}
	now := NowForStorage(a.clock)
	flow, err := domain.NewOIDCFlow(domain.OIDCFlow{
		ID: id, Purpose: in.Purpose, UserID: flowUser,
		StateHash: HashOIDCValue(state), NonceHash: HashOIDCValue(nonce), BrowserBindingHash: HashOIDCValue(binding),
		CreatedAt: now, ExpiresAt: now.Add(oidcFlowLifetime),
	})
	if err != nil {
		return domain.OIDCRedirect{}, err
	}
	if err := a.store.OIDCFlows().Insert(ctx, flow); err != nil {
		return domain.OIDCRedirect{}, err
	}
	return domain.OIDCRedirect{URL: url, BrowserBinding: domain.NewSecretText(binding), ExpiresAt: flow.ExpiresAt}, nil
}

// CompleteGoogle implements port.AuthService (milestone 4 design section 6). The flow is consumed
// before the code is exchanged, so a replayed callback never reaches the provider twice, and the
// exchange runs outside any transaction.
func (a *Auth) CompleteGoogle(ctx context.Context, in domain.OIDCCallback, meta domain.RequestMeta) (domain.OIDCResult, error) {
	if a.identity == nil {
		return domain.OIDCResult{}, domain.ErrFeatureDisabled
	}
	binding := in.BrowserBinding.Reveal()
	if !ValidOIDCValue(in.State) || !ValidOIDCValue(binding) {
		return a.flowNotFound(meta), nil
	}
	flow, err := a.store.OIDCFlows().Consume(ctx, HashOIDCValue(in.State), HashOIDCValue(binding), NowForStorage(a.clock))
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return a.flowNotFound(meta), nil
	case err != nil:
		return domain.OIDCResult{}, err
	}

	if in.ProviderError != "" || in.Code.IsZero() {
		return a.googleFailed(ctx, meta, flow, in.Principal, "provider_error")
	}
	if flow.Purpose == domain.OIDCPurposeLink && (in.Principal.IsZero() || in.Principal.UserID != flow.UserID) {
		return a.googleFailed(ctx, meta, flow, in.Principal, "session_mismatch")
	}
	id, err := a.identity.Exchange(ctx, domain.CodeExchange{
		Code: in.Code, CodeVerifier: domain.NewSecretText(a.oidc.Verifier(in.State, binding)),
	})
	switch {
	case errors.Is(err, domain.ErrIdentityRejected):
		return a.googleFailed(ctx, meta, flow, in.Principal, "exchange_rejected")
	case err != nil:
		return domain.OIDCResult{}, err
	}
	if subtle.ConstantTimeCompare(HashOIDCValue(id.Nonce), flow.NonceHash) != 1 {
		return a.googleFailed(ctx, meta, flow, in.Principal, "nonce_mismatch")
	}
	if flow.Purpose == domain.OIDCPurposeLink {
		return a.linkGoogle(ctx, meta, flow, in.Principal, id)
	}
	return a.loginGoogle(ctx, meta, flow, id)
}

// loginGoogle signs in the account linked to the Google subject, or creates one for a new, verified
// email. An email that already has an account is never linked automatically (AGENTS.md).
func (a *Auth) loginGoogle(ctx context.Context, meta domain.RequestMeta, flow domain.OIDCFlow, id domain.VerifiedIdentity) (domain.OIDCResult, error) {
	link, err := a.store.Identities().BySubject(ctx, domain.IdentityProviderGoogle, id.Subject)
	switch {
	case err == nil:
		return a.openGoogleSession(ctx, meta, link.UserID)
	case !errors.Is(err, domain.ErrNotFound):
		return domain.OIDCResult{}, err
	}
	if !id.EmailVerified {
		return a.googleFailed(ctx, meta, flow, domain.Principal{}, "email_not_verified")
	}
	email, err := domain.ParseEmail(id.Email)
	if err != nil {
		return a.googleFailed(ctx, meta, flow, domain.Principal{}, "email_invalid")
	}
	return a.registerGoogle(ctx, meta, flow, id, email)
}

// registerGoogle creates a verified account without a password, its Google identity and a session in
// one transaction. If the email is taken it checks the subject again inside the transaction: a
// second tab may have just created this same account, and then this tab signs in to it.
func (a *Auth) registerGoogle(ctx context.Context, meta domain.RequestMeta, flow domain.OIDCFlow, id domain.VerifiedIdentity, email domain.Email) (domain.OIDCResult, error) {
	now := NowForStorage(a.clock)
	userID, err := domain.NewID()
	if err != nil {
		return domain.OIDCResult{}, err
	}
	linkID, err := domain.NewID()
	if err != nil {
		return domain.OIDCResult{}, err
	}
	link, err := domain.NewAuthProvider(domain.AuthProvider{
		ID: linkID, UserID: userID, Provider: domain.IdentityProviderGoogle, ProviderSubject: id.Subject, CreatedAt: now,
	})
	if err != nil {
		return a.googleFailed(ctx, meta, flow, domain.Principal{}, "token_invalid")
	}
	session, token, err := a.issueSession(userID, now)
	if err != nil {
		return domain.OIDCResult{}, err
	}
	user := domain.User{ID: userID, Email: email, EmailVerifiedAt: &now, CreatedAt: now, UpdatedAt: now}

	var linkRequired bool
	var existing domain.ID
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		linkRequired, existing = false, domain.ID{}
		if err := tx.Users().Insert(ctx, user); err != nil {
			if !errors.Is(err, domain.ErrIdentityConflict) {
				return err
			}
			other, err := tx.Identities().BySubject(ctx, domain.IdentityProviderGoogle, id.Subject)
			if err == nil {
				existing = other.UserID
				return nil
			}
			if !errors.Is(err, domain.ErrNotFound) {
				return err
			}
			linkRequired = true
			return a.appendAudit(ctx, tx, meta, googleFailure(flow, domain.Principal{}, "link_required"))
		}
		if err := tx.Identities().Insert(ctx, link); err != nil {
			return err
		}
		if err := tx.Sessions().Insert(ctx, session); err != nil {
			return err
		}
		if err := a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthRegistered, Outcome: domain.AuditOutcomeSuccess, Actor: userID,
			TargetType: "user", TargetID: userID.String(), Method: "google",
		}); err != nil {
			return err
		}
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthLoginSucceeded, Outcome: domain.AuditOutcomeSuccess, Actor: userID,
			SessionID: session.ID, TargetType: "user", TargetID: userID.String(), Method: "google",
		})
	})
	switch {
	case err != nil:
		return domain.OIDCResult{}, err
	case linkRequired:
		return domain.OIDCResult{Outcome: domain.OIDCOutcomeLinkRequired}, nil
	case !existing.IsZero():
		return a.openGoogleSession(ctx, meta, existing)
	}
	return domain.OIDCResult{Outcome: domain.OIDCOutcomeSuccess, Session: domain.SessionGrant{User: user, Token: token, ExpiresAt: session.ExpiresAt}}, nil
}

// openGoogleSession signs userID in: lock the user, insert the session and audit it together.
func (a *Auth) openGoogleSession(ctx context.Context, meta domain.RequestMeta, userID domain.ID) (domain.OIDCResult, error) {
	now := NowForStorage(a.clock)
	session, token, err := a.issueSession(userID, now)
	if err != nil {
		return domain.OIDCResult{}, err
	}
	var user domain.User
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		u, err := tx.Users().Lock(ctx, userID)
		if err != nil {
			return err
		}
		user = u
		if err := tx.Sessions().Insert(ctx, session); err != nil {
			return err
		}
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthLoginSucceeded, Outcome: domain.AuditOutcomeSuccess, Actor: userID,
			SessionID: session.ID, TargetType: "user", TargetID: userID.String(), Method: "google",
		})
	})
	if err != nil {
		return domain.OIDCResult{}, err
	}
	return domain.OIDCResult{Outcome: domain.OIDCOutcomeSuccess, Session: domain.SessionGrant{User: user, Token: token, ExpiresAt: session.ExpiresAt}}, nil
}

// linkGoogle adds the Google identity to the signed-in account that started the flow. It opens no
// session. A subject linked elsewhere, or a second Google account for this user, is refused and
// audited in the same transaction.
func (a *Auth) linkGoogle(ctx context.Context, meta domain.RequestMeta, flow domain.OIDCFlow, p domain.Principal, id domain.VerifiedIdentity) (domain.OIDCResult, error) {
	linkID, err := domain.NewID()
	if err != nil {
		return domain.OIDCResult{}, err
	}
	link, err := domain.NewAuthProvider(domain.AuthProvider{
		ID: linkID, UserID: p.UserID, Provider: domain.IdentityProviderGoogle, ProviderSubject: id.Subject, CreatedAt: NowForStorage(a.clock),
	})
	if err != nil {
		return a.googleFailed(ctx, meta, flow, p, "token_invalid")
	}
	var conflict bool
	err = a.uow.WithinTransaction(ctx, func(tx port.TxRepositories) error {
		conflict = false
		if _, err := tx.Users().Lock(ctx, p.UserID); err != nil {
			return err
		}
		if err := tx.Identities().Insert(ctx, link); err != nil {
			if !errors.Is(err, domain.ErrIdentityConflict) {
				return err
			}
			conflict = true
			return a.appendAudit(ctx, tx, meta, googleFailure(flow, p, "identity_conflict"))
		}
		return a.appendAudit(ctx, tx, meta, auditSpec{
			Type: domain.EventAuthIdentityLinked, Outcome: domain.AuditOutcomeSuccess, Actor: p.UserID,
			SessionID: p.SessionID, TargetType: "auth_provider", TargetID: link.ID.String(), Provider: "google",
		})
	})
	switch {
	case err != nil:
		return domain.OIDCResult{}, err
	case conflict:
		return domain.OIDCResult{Outcome: domain.OIDCOutcomeFailed}, nil
	}
	return domain.OIDCResult{Outcome: domain.OIDCOutcomeLinked}, nil
}

// googleFailed audits a refused callback in its own transaction and answers "failed".
func (a *Auth) googleFailed(ctx context.Context, meta domain.RequestMeta, flow domain.OIDCFlow, p domain.Principal, reason string) (domain.OIDCResult, error) {
	if err := a.recordFailure(ctx, meta, googleFailure(flow, p, reason)); err != nil {
		return domain.OIDCResult{}, err
	}
	return domain.OIDCResult{Outcome: domain.OIDCOutcomeFailed}, nil
}

// googleFailure is the audit event of a refused callback. A login flow has no proven caller. A link
// flow audits identity_link_failed, with the caller as actor only when its session is the one that
// started the flow.
func googleFailure(flow domain.OIDCFlow, p domain.Principal, reason string) auditSpec {
	if flow.Purpose != domain.OIDCPurposeLink {
		return auditSpec{
			Type: domain.EventAuthLoginFailed, Outcome: domain.AuditOutcomeFailure,
			TargetType: "oidc_flow", TargetID: flow.ID.String(), Reason: reason, Method: "google",
		}
	}
	s := auditSpec{
		Type: domain.EventAuthIdentityLinkFailed, Outcome: domain.AuditOutcomeFailure,
		TargetType: "oidc_flow", TargetID: flow.ID.String(), Reason: reason, Provider: "google",
	}
	if !p.IsZero() && p.UserID == flow.UserID {
		s.Actor, s.SessionID = p.UserID, p.SessionID
	}
	return s
}

// flowNotFound answers a callback that matches no open flow. It writes no audit: nothing proves the
// request belongs to a flow, and an unauthenticated GET must not fill audit_events.
func (a *Auth) flowNotFound(meta domain.RequestMeta) domain.OIDCResult {
	a.log.Info("google callback matched no open flow",
		slog.String("event", "auth.oidc_flow_not_found"),
		slog.String("request_id", meta.RequestID.String()))
	return domain.OIDCResult{Outcome: domain.OIDCOutcomeFailed}
}
```

แก้ `internal/adapter/http/auth.go` — แทน

```go
	app.Post("/api/v1/auth/google/link", session, middleware.RequireAuth(), csrf, h.googleLink)
```

ด้วย

```go
	app.Post("/api/v1/auth/google/link", session, middleware.RequireAuth(), csrf, h.googleLink)
	// No app CSRF: the browser arrives from Google. The state, the binding cookie and the nonce
	// protect the callback instead (phase 1 spec section 10).
	app.Get("/api/v1/auth/google/callback", session, h.googleCallback)
```

ต่อท้าย `internal/adapter/http/google.go` ด้วย (เพิ่ม `"strings"` ในบล็อก import):

```go

// googleResultPath is where every callback ends; the outcome follows. It is fixed (no return URL from
// the request) and same-origin. It changes when the frontend exists.
const googleResultPath = "/docs/?google="

// GoogleCallback godoc
// @Summary      Come back from Google
// @Description  Google sends the browser here after GET authorization_url. It is not called directly: it needs the state from that URL and the oidc_binding cookie of the browser that started the flow, and it takes no X-CSRF-Token (the state, the binding and the ID token's nonce protect it). It always clears oidc_binding and answers 303 to /docs/?google=success (signed in: the session cookie is set), linked (Google added to the signed-in account), link_required (the email belongs to an account: sign in to it and use POST /api/v1/auth/google/link) or failed (cancelled, expired, replayed, another browser, or refused). Infrastructure failures answer JSON instead.
// @Tags         Auth
// @ID           completeGoogleSignIn
// @Produce      json
// @Param        code   query  string  false  "Authorization code from Google"
// @Param        state  query  string  false  "The state of the flow"
// @Param        error  query  string  false  "Set by Google when the user cancelled"
// @Success      303  "Redirect to /docs/?google=success|linked|link_required|failed"
// @Failure      500  {object}  ErrorResponse  "audit_write_failed or internal"
// @Failure      503  {object}  ErrorResponse  "feature_disabled (Google is not configured) or dependency_unavailable"
// @Header       303  {string}  Location    "/docs/?google=success"
// @Header       303  {string}  Set-Cookie  "oidc_binding=; Expires=Tue, 10 Nov 2009 23:00:00 GMT — and session=… on success"
// @Router       /api/v1/auth/google/callback [get]
func (h *authHandlers) googleCallback(c fiber.Ctx) error {
	p, _ := middleware.GetPrincipal(c)
	in := domain.OIDCCallback{
		Code:           domain.NewSecretText(strings.Clone(c.Query("code"))),
		State:          strings.Clone(c.Query("state")),
		ProviderError:  strings.Clone(c.Query("error")),
		BrowserBinding: domain.NewSecretText(strings.Clone(c.Cookies(h.cookies.bindingName))),
		Principal:      p,
	}
	meta, err := h.requestMeta(c)
	if err != nil {
		return err
	}
	h.cookies.clear(c, h.cookies.bindingName) // the flow ends in this browser whatever happens
	res, err := h.auth.CompleteGoogle(c.Context(), in, meta)
	if err != nil {
		return err
	}
	if res.Outcome == domain.OIDCOutcomeSuccess {
		h.cookies.set(c, h.cookies.sessionName, res.Session.Token.Reveal(), res.Session.ExpiresAt)
		h.cookies.clear(c, h.cookies.nonceName)
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Redirect().Status(fiber.StatusSeeOther).To(googleResultPath + string(res.Outcome))
}
```

(ถ้า `c.Redirect().Status(...).To(...)` ของ Fiber v3.5.0 ทำให้ header `Location` ไม่ใช่ path ตรงตัว เช่นเติม host ให้ดู `$(go env GOMODCACHE)/github.com/gofiber/fiber/v3@v3.5.0/redirect.go` แล้วใช้ `c.Set(fiber.HeaderLocation, …)` กับ `c.SendStatus(fiber.StatusSeeOther)` แทน และระบุใน report)

- [ ] **Step 4: รัน test ให้ผ่าน และ generate Swagger**

Run:
```bash
docker compose ps
gofmt -l internal; go vet ./... && make swagger && go test ./internal/... -count=1 -race && make -s test-integration && make lint
```
Expected: ผ่านทั้งหมด; test ของ login ด้วยรหัสผ่าน (`login_test.go`) ยังผ่านหลังเปลี่ยนมาใช้ `issueSession`; test ใน `google_callback_test.go` ทำงานจริง

- [ ] **Step 5: Commit**

```bash
git add internal/core/port/auth_service.go internal/core/service internal/adapter/http docs/api
git commit -m "feat(auth): complete Google sign-in at the callback: log in, create a verified account or link"
```

---

### Task 7: test ของการ link, audit rollback และ log ของ Google

task นี้เพิ่มเฉพาะ test: ถ้า test ใดล้มแสดงว่าโค้ดของ Task 5–6 ผิด ให้หาสาเหตุแล้วแก้โค้ดตัวนั้น (ไม่แก้ test ให้ผ่าน) และระบุใน report

**Files:**
- Create: `internal/adapter/http/google_link_test.go`, `internal/adapter/http/google_failures_test.go`

**Interfaces:**
- Consumes: harness, `withIdentityProvider`, `withFailingAudit`, `fakeIdentityProvider`, `signInWithGoogle`, `requireGoogleRedirect`, `identityOf`, `uniqueSubject`, `requireAuditFailure`, `sessionsOf`, `signUpAndVerify`, `login`, `logout`, `me`, `decodeUser` (Task 5–6 และแผน 4b)
- Produces: ไม่มี

- [ ] **Step 1: เขียน tests**

สร้าง `internal/adapter/http/google_link_test.go`:

```go
package http

import (
	nethttp "net/http"
	"net/url"
	"testing"

	"go-backend-stock/internal/core/domain"
)

// signedIn returns a browser signed in to a new local account.
func signedIn(t *testing.T, h *authHarness) (*browser, userRow) {
	t.Helper()
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	b.signUpAndVerify(email, "Zq9!kVx1")
	if resp, body := b.login(email, "Zq9!kVx1"); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("login = %d %s", resp.StatusCode, body)
	}
	u, _ := h.user(t, email)
	return b, u
}

// linkWithGoogle starts a link in b and comes back from Google.
func (b *browser) linkWithGoogle(f *fakeIdentityProvider) (response, []byte) {
	b.t.Helper()
	if resp, body := b.linkGoogle(); resp.StatusCode != nethttp.StatusOK {
		b.t.Fatalf("link = %d %s", resp.StatusCode, body)
	}
	return b.googleCallback(url.Values{"code": {"code-" + mustTestID(b.t).String()}, "state": {f.lastRequest(b.t).State}})
}

func TestGoogleLinkAddsGoogleToTheSignedInAccount(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	b, u := signedIn(t, h)
	sub := uniqueSubject(t)
	f.setIdentity(sub, "another-address@example.com", true) // a link does not need the same email
	session := b.jar["session"]

	resp, body := b.linkWithGoogle(f)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeLinked)
	if owner, ok := h.identityOf(t, sub); !ok || owner != u.ID {
		t.Fatalf("identity owner = %q (found %v), want %s", owner, ok, u.ID)
	}
	if b.jar["session"] != session || h.sessionsOf(t, u.ID) != 1 {
		t.Error("linking must keep the current session and open no other")
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "auth.identity_linked" || rows[0].Actor != u.ID ||
		rows[0].SessionID == "" || rows[0].Metadata["provider"] != "google" {
		t.Errorf("audit = %+v, want identity_linked by the user with provider google", rows)
	}

	other := h.newBrowser(t)
	resp, body = other.signInWithGoogle(f)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeSuccess)
	if resp, body := other.me(); resp.StatusCode != nethttp.StatusOK || decodeUser(t, body).ID != u.ID {
		t.Errorf("Google sign-in after linking = %d %s, want the linked account", resp.StatusCode, body)
	}
}

func TestGoogleLinkOfASubjectThatBelongsToAnotherAccount(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	sub, googleEmail := uniqueSubject(t), uniqueEmail(t)
	f.setIdentity(sub, googleEmail, true)
	resp, body := h.newBrowser(t).signInWithGoogle(f) // the subject now owns its own account
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeSuccess)
	owner, _ := h.user(t, googleEmail)

	b, u := signedIn(t, h)
	resp, body = b.linkWithGoogle(f)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
	if got, _ := h.identityOf(t, sub); got != owner.ID {
		t.Errorf("identity owner = %s, want it unchanged (%s)", got, owner.ID)
	}
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "auth.identity_link_failed" || rows[0].Reason != "identity_conflict" ||
		rows[0].Actor != u.ID || rows[0].Metadata["provider"] != "google" {
		t.Errorf("audit = %+v, want identity_link_failed identity_conflict by the caller", rows)
	}
}

func TestGoogleLinkOfASecondGoogleAccount(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	b, u := signedIn(t, h)
	first := uniqueSubject(t)
	f.setIdentity(first, uniqueEmail(t), true)
	resp, body := b.linkWithGoogle(f)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeLinked)

	second := uniqueSubject(t)
	f.setIdentity(second, uniqueEmail(t), true)
	resp, body = b.linkWithGoogle(f)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
	if _, ok := h.identityOf(t, second); ok {
		t.Error("an account holds one Google identity")
	}
	if owner, _ := h.identityOf(t, first); owner != u.ID {
		t.Error("the first link must stay")
	}
}

func TestGoogleLinkNeedsTheSessionThatStartedIt(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	b, _ := signedIn(t, h)
	sub := uniqueSubject(t)
	f.setIdentity(sub, uniqueEmail(t), true)
	if resp, body := b.linkGoogle(); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("link = %d %s", resp.StatusCode, body)
	}
	state := f.lastRequest(t).State

	b.logout() // the binding cookie stays; the session that started the flow is gone
	resp, body := b.googleCallback(url.Values{"code": {"code-1"}, "state": {state}})
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].EventType != "auth.identity_link_failed" || rows[0].Reason != "session_mismatch" || rows[0].Actor != "" {
		t.Errorf("audit = %+v, want identity_link_failed session_mismatch with actor NULL", rows)
	}
	if _, ok := h.identityOf(t, sub); ok || len(f.exchanges()) != 0 {
		t.Error("a link without its session must not reach the provider or add an identity")
	}
}

func TestGoogleLinkCannotBeFinishedByAnotherAccount(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	owner, _ := signedIn(t, h)
	sub := uniqueSubject(t)
	f.setIdentity(sub, uniqueEmail(t), true)
	if resp, body := owner.linkGoogle(); resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("link = %d %s", resp.StatusCode, body)
	}
	state := f.lastRequest(t).State

	other, _ := signedIn(t, h)
	other.jar["oidc_binding"] = owner.jar["oidc_binding"] // the binding cookie was stolen
	resp, body := other.googleCallback(url.Values{"code": {"code-1"}, "state": {state}})
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
	rows := h.auditFor(t, resp.Header.Get("X-Request-ID"))
	if len(rows) != 1 || rows[0].Reason != "session_mismatch" || rows[0].Actor != "" {
		t.Errorf("audit = %+v, want session_mismatch with actor NULL", rows)
	}
	if _, ok := h.identityOf(t, sub); ok {
		t.Error("another account must not receive the identity")
	}
}
```

สร้าง `internal/adapter/http/google_failures_test.go`:

```go
package http

import (
	"net/url"
	"strings"
	"testing"

	"go-backend-stock/internal/core/service"
)

func TestGoogleSignInWithoutItsAuditCreatesNothing(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	failing := newAuthHarness(t, withIdentityProvider(f), withFailingAudit())
	email, sub := uniqueEmail(t), uniqueSubject(t)
	f.setIdentity(sub, email, true)

	b := failing.newBrowser(t)
	resp, body := b.signInWithGoogle(f)
	requireAuditFailure(t, "google sign-in", resp, body)
	if _, ok := h.user(t, email); ok {
		t.Error("the account must be rolled back with its audit")
	}
	if _, ok := h.identityOf(t, sub); ok || b.jar["session"] != "" {
		t.Error("no identity and no session may survive the rollback")
	}
}

func TestGoogleLinkWithoutItsAuditAddsNoIdentity(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	failing := newAuthHarness(t, withIdentityProvider(f), withFailingAudit())
	b, _ := signedIn(t, h)
	sub := uniqueSubject(t)
	f.setIdentity(sub, uniqueEmail(t), true)

	fb := failing.newBrowser(t)
	fb.jar["session"] = b.jar["session"] // the same signed-in browser, served by the failing twin
	resp, body := fb.linkWithGoogle(f)
	requireAuditFailure(t, "google link", resp, body)
	if _, ok := h.identityOf(t, sub); ok {
		t.Error("the identity must be rolled back with its audit")
	}
}

func TestGoogleLogsHoldNoSecrets(t *testing.T) {
	f := newFakeIdentityProvider()
	h := newAuthHarness(t, withIdentityProvider(f))
	b := h.newBrowser(t)
	f.setIdentity(uniqueSubject(t), uniqueEmail(t), true)
	if resp, body := b.startGoogle(); resp.StatusCode != 200 {
		t.Fatalf("start = %d %s", resp.StatusCode, body)
	}
	req := f.lastRequest(t)
	binding := b.jar["oidc_binding"]
	codec, err := service.NewOIDCCodec([]byte(testOIDCSecret))
	if err != nil {
		t.Fatal(err)
	}
	code := "authorization-code-ZQXJ-" + mustTestID(t).String()
	resp, body := b.googleCallback(url.Values{"code": {code}, "state": {req.State}})
	requireGoogleRedirect(t, resp, body, "success")
	b.googleCallback(url.Values{"error": {"access_denied"}, "state": {"junk"}})

	logs := h.logs.String()
	if !strings.Contains(logs, `"msg":"http request"`) || !strings.Contains(logs, `"event":"auth.oidc_flow_not_found"`) {
		t.Fatal("the access log and the unmatched-callback log must be captured, or this test proves nothing")
	}
	for name, secret := range map[string]string{
		"state": req.State, "nonce": req.Nonce, "binding": binding, "code": code,
		"verifier": codec.Verifier(req.State, binding), "session": b.jar["session"], "oidc secret": testOIDCSecret,
	} {
		if secret != "" && strings.Contains(logs, secret) {
			t.Errorf("logs contain the %s", name)
		}
	}
}
```

- [ ] **Step 2: รัน test**

Run: `docker compose ps && go vet ./internal/adapter/http/ && make -s test-integration`
Expected: ผ่านทั้งหมด (ถ้าล้ม ให้หาสาเหตุในโค้ดของ Task 5–6 ตาม systematic debugging แก้โค้ด แล้วรันซ้ำ)

- [ ] **Step 3: รันชุดเต็มและ lint**

Run: `gofmt -l internal; go test ./... -count=1 -race && make lint`
Expected: ผ่าน

- [ ] **Step 4: Commit**

```bash
git add internal/adapter/http/google_link_test.go internal/adapter/http/google_failures_test.go
git commit -m "test(auth): prove Google linking, audit rollback and log hygiene"
```

(ถ้าต้องแก้โค้ดของ task ก่อนหน้า ให้ `git add` ไฟล์นั้นใน commit แยก `fix(auth): …` ที่บอกสิ่งที่แก้)

---

### Task 8: ประกอบ Google ใน `cmd/api` และ test ครบเส้นทางกับ OIDC server ปลอม

**Files:**
- Create: `internal/adapter/http/google_e2e_test.go`
- Modify: `cmd/api/wire.go`, `cmd/api/wire_test.go`, `cmd/api/main.go`, `docker-compose.yml`, `docs/api/` (generated)

**Interfaces:**
- Consumes: `config.Config.Google` (Task 1), `identity.NewGoogle`, `identity.GoogleIssuer`, `identitytest` (Task 4), `service.NewOIDCCodec` (Task 3), `service.AuthDeps.Identity/OIDC` (Task 5), harness/browser helpers (Task 5–6)
- Produces: `buildAuth` ตั้ง `Identity` เฉพาะเมื่อ Google เปิด; `googleTimeout = 10 * time.Second`; `wireConfig(t, origin, extra ...map[string]string)`

- [ ] **Step 1: เขียน failing tests**

แก้ `cmd/api/wire_test.go` ตามคู่ต่อไปนี้:

1. แทน

```go
func wireConfig(t *testing.T, origin string) config.Config {
```

   ด้วย

```go
func wireConfig(t *testing.T, origin string, extra ...map[string]string) config.Config {
```

2. แทน

```go
	cfg, err := config.Load(func(k string) string { return env[k] })
```

   ด้วย

```go
	for _, more := range extra {
		for k, v := range more {
			env[k] = v
		}
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
```

3. ต่อท้ายไฟล์ด้วย

```go

func authConfigOf(t *testing.T, cfg config.Config) string {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	parts, err := buildAuth(cfg, pool, log)
	if err != nil {
		t.Fatalf("buildAuth() error = %v", err)
	}
	defer func() { _ = parts.mail.Close(context.Background()) }()
	app := httpadapter.NewServer(httpadapter.Options{Logger: log, Readiness: pool, Auth: parts.options})
	resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/v1/auth/config", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestBuildAuthLeavesGoogleOffByDefault(t *testing.T) {
	if got := authConfigOf(t, wireConfig(t, "http://localhost:8080")); got != `{"google_enabled":false}` {
		t.Errorf("GET /api/v1/auth/config = %s", got)
	}
}

// Building the Google adapter makes no network call (discovery is lazy), so this needs no Google.
func TestBuildAuthTurnsGoogleOnWithItsConfig(t *testing.T) {
	cfg := wireConfig(t, "http://localhost:8080", map[string]string{
		"GOOGLE_CLIENT_ID":     "1234-abc.apps.googleusercontent.com",
		"GOOGLE_CLIENT_SECRET": "GOCSPX-test",
		"GOOGLE_REDIRECT_URI":  "http://localhost:8080/api/v1/auth/google/callback",
	})
	if got := authConfigOf(t, cfg); got != `{"google_enabled":true}` {
		t.Errorf("GET /api/v1/auth/config = %s", got)
	}
}
```

(ตรวจ import ของ `wire_test.go`: ต้องมี `context`, `io`, `log/slog`, `nethttp "net/http"`, `net/http/httptest`, `pgxpool`, `httpadapter` และ `config` อยู่แล้วจากแผน 4b; ถ้าชื่อ alias ต่างไปให้ใช้ตามไฟล์จริง)

สร้าง `internal/adapter/http/google_e2e_test.go`:

```go
package http

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"go-backend-stock/internal/adapter/identity"
	"go-backend-stock/internal/adapter/identity/identitytest"
	"go-backend-stock/internal/core/domain"
)

// newRealGoogleHarness runs the real adapter against the fake provider, so the whole path is the
// production one: HTTP → service → go-oidc → token endpoint → signed ID token.
func newRealGoogleHarness(t *testing.T) (*authHarness, *identitytest.Provider) {
	t.Helper()
	p := identitytest.New(t)
	g, err := identity.NewGoogle(identity.GoogleConfig{
		Issuer: p.Issuer(), ClientID: identitytest.ClientID, ClientSecret: identitytest.ClientSecret,
		RedirectURL: testOrigin + "/api/v1/auth/google/callback", Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return newAuthHarness(t, withIdentityProvider(g)), p
}

// startAtFakeGoogle starts a flow and plays the Google page: it reads the flow values from the
// authorization URL and registers a code for grant.
func startAtFakeGoogle(t *testing.T, b *browser, p *identitytest.Provider, grant identitytest.Grant) url.Values {
	t.Helper()
	resp, body := b.startGoogle()
	out := decodeStart(t, body)
	if resp.StatusCode != 200 || !strings.HasPrefix(out.AuthorizationURL, p.Issuer()+"/auth?") {
		t.Fatalf("start = %d, url %s; want the fake provider's authorization endpoint", resp.StatusCode, out.AuthorizationURL)
	}
	state, nonce, challenge := identitytest.ParamsFromURL(t, out.AuthorizationURL)
	if grant.Nonce == "" {
		grant.Nonce = nonce
	}
	if grant.CodeChallenge == "" {
		grant.CodeChallenge = challenge
	}
	return url.Values{"code": {p.Authorize(t, grant)}, "state": {state}}
}

func TestGoogleSignInEndToEnd(t *testing.T) {
	h, p := newRealGoogleHarness(t)
	b := h.newBrowser(t)
	email := uniqueEmail(t)
	q := startAtFakeGoogle(t, b, p, identitytest.Grant{Subject: uniqueSubject(t), Email: email, EmailVerified: true})
	resp, body := b.googleCallback(q)
	requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeSuccess)
	if resp, body := b.me(); resp.StatusCode != 200 || decodeUser(t, body).Email != email {
		t.Errorf("me = %d %s", resp.StatusCode, body)
	}
}

func TestGoogleSignInEndToEndRefusesBadTokens(t *testing.T) {
	h, p := newRealGoogleHarness(t)
	for name, grant := range map[string]identitytest.Grant{
		"forged signature": {SignWith: identitytest.NewKey(t)},
		"other audience":   {Audience: "someone-else.apps.example"},
		"expired":          {Expiry: -10 * time.Minute},
		"wrong verifier":   {CodeChallenge: "a-challenge-of-another-verifier"},
		"other nonce":      {Nonce: "a-nonce-of-another-flow"},
	} {
		t.Run(name, func(t *testing.T) {
			b := h.newBrowser(t)
			email := uniqueEmail(t)
			grant.Subject, grant.Email, grant.EmailVerified = uniqueSubject(t), email, true
			resp, body := b.googleCallback(startAtFakeGoogle(t, b, p, grant))
			requireGoogleRedirect(t, resp, body, domain.OIDCOutcomeFailed)
			if _, ok := h.user(t, email); ok || b.jar["session"] != "" {
				t.Error("a refused token must not create an account or a session")
			}
		})
	}
}
```

- [ ] **Step 2: รัน test ให้เห็นว่าล้ม**

Run: `go test ./cmd/api/ -run Google -count=1`
Expected: FAIL — `TestBuildAuthTurnsGoogleOnWithItsConfig` ได้ `{"google_enabled":false}` เพราะ `buildAuth` ยังไม่ประกอบ Google (test end-to-end ผ่านตั้งแต่ตอนนี้ได้เพราะใช้ adapter จริงที่มีแล้ว ให้รัน `make -s test-integration` ยืนยัน)

- [ ] **Step 3: เขียนโค้ด**

แก้ `cmd/api/wire.go` ตามคู่ต่อไปนี้:

1. แทน

```go
	httpadapter "go-backend-stock/internal/adapter/http"
	"go-backend-stock/internal/adapter/mail"
```

   ด้วย

```go
	httpadapter "go-backend-stock/internal/adapter/http"
	"go-backend-stock/internal/adapter/identity"
	"go-backend-stock/internal/adapter/mail"
```

2. แทน

```go
	"go-backend-stock/internal/config"
	"go-backend-stock/internal/core/service"
)
```

   ด้วย

```go
	"go-backend-stock/internal/config"
	"go-backend-stock/internal/core/port"
	"go-backend-stock/internal/core/service"
)

// googleTimeout limits each call to Google (discovery, token, keys).
const googleTimeout = 10 * time.Second
```

3. แทน

```go
	tlsMode, err := smtpTLSMode(cfg.SMTP.TLS)
```

   ด้วย

```go
	oidc, err := service.NewOIDCCodec(cfg.Auth.OIDCSecret.Bytes())
	if err != nil {
		return fail("oidc codec", err)
	}
	// Set only when Google is configured: an interface that holds a nil *identity.Google is not nil
	// and would turn Google sign-in on.
	var google port.IdentityProvider
	if cfg.Google.Enabled() {
		g, err := identity.NewGoogle(identity.GoogleConfig{
			Issuer: identity.GoogleIssuer, ClientID: cfg.Google.ClientID, ClientSecret: cfg.Google.ClientSecret.Reveal(),
			RedirectURL: cfg.Google.RedirectURI, Timeout: googleTimeout,
		})
		if err != nil {
			return fail("google identity provider", err)
		}
		google = g
	}
	tlsMode, err := smtpTLSMode(cfg.SMTP.TLS)
```

4. แทน

```go
		Mailer: dispatcher, Logger: log, SessionTTL: cfg.Auth.SessionTTL, OTPMaxAttempts: cfg.Auth.OTPMaxAttempts,
	})
```

   ด้วย

```go
		Mailer: dispatcher, Logger: log, SessionTTL: cfg.Auth.SessionTTL, OTPMaxAttempts: cfg.Auth.OTPMaxAttempts,
		Identity: google, OIDC: oidc,
	})
```

แก้ `cmd/api/main.go` — แทน

```go
// @description     Milestone 4b exposes Health and the email/password Auth endpoints; Google sign-in, password reset, portfolios and transactions are not implemented yet.
```

ด้วย

```go
// @description     Milestone 4 exposes Health and the Auth endpoints (email and password, email verification, Google sign-in and linking); password reset, portfolios and transactions are not implemented yet.
```

แก้ `docker-compose.yml` — แทน

```yaml
      OIDC_SECRET: ${OIDC_SECRET}
```

ด้วย

```yaml
      OIDC_SECRET: ${OIDC_SECRET}
      GOOGLE_CLIENT_ID: ${GOOGLE_CLIENT_ID:-}
      GOOGLE_CLIENT_SECRET: ${GOOGLE_CLIENT_SECRET:-}
      GOOGLE_REDIRECT_URI: ${GOOGLE_REDIRECT_URI:-}
```

(ไม่ต้อง restart stack ของผู้ใช้; ค่าว่างทั้งสามแปลว่า Google ปิด)

- [ ] **Step 4: รัน test ให้ผ่าน**

Run:
```bash
docker compose ps
gofmt -l cmd internal; go vet ./... && make swagger && go test ./... -count=1 -race && make -s test-integration && make lint && go build -o /dev/null ./cmd/api
docker compose config --quiet
```
Expected: ผ่านทั้งหมด; `docker compose config --quiet` ไม่มี error (คำสั่งนี้อ่านไฟล์อย่างเดียว ไม่แตะ container)

- [ ] **Step 5: Commit**

```bash
git add cmd/api/wire.go cmd/api/wire_test.go cmd/api/main.go docker-compose.yml internal/adapter/http/google_e2e_test.go docs/api
git commit -m "feat(api): wire Google sign-in when its config is set and test it end to end"
```

---

### Task 9: คู่มือ Google, สถานะ และตรวจรับ

**Files:**
- Modify: `README.md`, `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md`, `docs/superpowers/specs/2026-10-22-milestone-4-auth-design.md`

**Interfaces:**
- Consumes: endpoints ของ Task 5–6, config ของ Task 1, wiring ของ Task 8
- Produces: README มีขั้นตอนสร้าง OAuth client ของ Google และลองบน browser; roadmap/spec บอกสถานะ 4c

- [ ] **Step 1: README**

แก้ `README.md` ตามคู่ต่อไปนี้:

1. แทน

```markdown
> **สถานะ:** Milestone 4b — สมัครด้วยอีเมล+รหัสผ่าน, ยืนยันอีเมลด้วย OTP, login/logout และ session ฝั่ง server ใช้งานผ่าน API ได้แล้ว; Google login (4c), ลืมรหัสผ่าน (milestone 5), พอร์ตและรายการซื้อขายยังไม่เปิดใช้งาน
```

   ด้วย

```markdown
> **สถานะ:** Milestone 4 — สมัครด้วยอีเมล+รหัสผ่าน, ยืนยันอีเมลด้วย OTP, login/logout, session ฝั่ง server และ Google sign-in/เชื่อมบัญชี (เปิดเมื่อตั้งค่า `GOOGLE_*`) ใช้งานผ่าน API ได้แล้ว; ลืมรหัสผ่าน (milestone 5), พอร์ตและรายการซื้อขายยังไม่เปิดใช้งาน
```

2. แทน

```markdown
## พัฒนาและทดสอบ
```

   ด้วย

````markdown
## Google sign-in (ไม่บังคับ)

ถ้าไม่ตั้งค่า Google ระบบยังใช้งานได้ปกติ: `GET /api/v1/auth/config` ตอบ `{"google_enabled":false}` และ endpoint ของ Google ตอบ 503 `feature_disabled`

สร้าง OAuth client (ฟรี ไม่ต้องผูกบัตร):

1. เปิด Google Cloud Console → APIs & Services → OAuth consent screen ตั้งเป็น External และโหมด Testing แล้วเพิ่มอีเมล Google ของตัวเองใน Test users
2. Credentials → Create credentials → OAuth client ID → Web application
3. Authorized redirect URIs ใส่ `http://localhost:8080/api/v1/auth/google/callback` (ต้องเท่ากับ `PUBLIC_ORIGIN` + `/api/v1/auth/google/callback` ทุกตัวอักษร)
4. ใส่ค่าใน `.env` ทั้งสามตัว (ตั้งไม่ครบ API จะไม่เริ่มและบอกว่าขาดตัวไหน) แล้ว `docker compose up -d --build api`

```bash
GOOGLE_CLIENT_ID=1234-abc.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=<client secret จาก Console — ห้าม commit>
GOOGLE_REDIRECT_URI=http://localhost:8080/api/v1/auth/google/callback
```

ลองบน browser (curl และ Swagger UI ทำหน้า login ของ Google แทนไม่ได้):

1. เปิด `http://localhost:8080/docs/` เรียก `GET /api/v1/auth/csrf` แล้วคัดลอก `csrf_token`
2. เรียก `POST /api/v1/auth/google/start` ใส่ token ในช่อง `X-CSRF-Token` แล้วเปิด `authorization_url` ที่ได้ใน**แท็บเดียวกันของ browser เดิม** (cookie `oidc_binding` อยู่ใน browser นี้)
3. login กับ Google แล้ว browser จะกลับมาที่ `/docs/?google=success` พร้อม cookie `session`; เรียก `GET /api/v1/auth/me` ดูได้
4. ผลอื่น: `link_required` = อีเมลนี้มีบัญชีอยู่แล้ว ให้ login บัญชีนั้นด้วยรหัสผ่าน แล้วขอ CSRF ใหม่และเรียก `POST /api/v1/auth/google/link` แทน (ได้ `/docs/?google=linked`); `failed` = ยกเลิก, หมดเวลา 10 นาที, เปิด callback ซ้ำหรือคนละ browser

ระบบไม่เชื่อมบัญชีให้อัตโนมัติเพียงเพราะอีเมลตรงกัน และบัญชีที่สร้างจาก Google ไม่มีรหัสผ่าน (login ด้วยรหัสผ่านไม่ได้จนกว่าจะมีลืมรหัสผ่านใน milestone 5)

## พัฒนาและทดสอบ
````

- [ ] **Step 2: roadmap และ spec**

แก้ `docs/superpowers/plans/2026-10-05-phase-1-roadmap.md` ตามคู่ต่อไปนี้:

1. แทน

```markdown
แผน 4b ลงมือครบแล้ว รอตรวจรับ ขั้นต่อไปคือแผน 4c
```

   ด้วย

```markdown
แผน 4b ลงมือครบแล้ว รอตรวจรับ แผน 4c ลงมือครบแล้ว รอตรวจรับ (ยังไม่ได้ทดสอบกับ Google จริงจนกว่าผู้ใช้จะสร้าง OAuth client) ขั้นต่อไปคือ milestone 5
```

2. แทน

```markdown
4c (Google OIDC) ยังไม่มีแผน
```

   ด้วย

```markdown
[4c](2026-10-27-milestone-4c-google-oidc.md) (Google OIDC; ลงมือครบ รอตรวจรับ)
```

3. แทนสามบรรทัดต่อไปนี้ (แต่ละบรรทัดเจอครั้งเดียว)

```markdown
- [ ] เพิ่ม Google OIDC, state/nonce validation, PKCE S256 และ explicit account linking; ปิด flow ชัดเจนเมื่อ config ไม่ครบ
```

```markdown
- [ ] ทดสอบ fake identity provider, replayed callback, invalid audience/issuer, session หมดอายุ/revoked และการ rollback เมื่อ audit insert ล้มเหลว
```

```markdown
- [ ] เพิ่ม auth annotations/examples แล้ว generate เอกสารใหม่ ตรวจ Swagger UI/browser cookie/CSRF โดยไม่ลดความปลอดภัย พร้อม curl cookie-jar examples และขั้นตอน Google login สำหรับ flow ที่ UI ไม่รองรับ ทั้งหมดทดสอบได้โดยไม่ใช้ Next.js
```

   ด้วยบรรทัดเดิมที่เปลี่ยน `- [ ]` เป็น `- [x]` และต่อท้ายด้วย ` (แผน 4c)` (บรรทัดที่สามต่อท้ายด้วย ` (แผน 4b/4c; ยังต้องลอง Swagger UI และ Google จริงบน browser)` แทน)

4. ในหัวข้อ "ข้อส่งต่อจากแผน 4b ไปแผนถัดไป" ต่อท้ายรายการด้วยบรรทัดนี้

```markdown
- ข้อส่งต่อจากแผน 4c: ทดสอบ Google จริงบน browser หลังผู้ใช้สร้าง OAuth client (ก่อนปล่อยระบบ); ปลายทาง `/docs/?google=…` เปลี่ยนเป็นหน้า frontend ใน milestone 10; บัญชีที่สร้างจาก Google ตั้งรหัสผ่านได้ผ่านลืมรหัสผ่านใน milestone 5
```

แก้ `docs/superpowers/specs/2026-10-22-milestone-4-auth-design.md` — แทน

```markdown
แผน 4b: [2026-10-24-milestone-4b-auth-endpoints.md](../plans/2026-10-24-milestone-4b-auth-endpoints.md) ลงมือครบ รอตรวจรับ
```

ด้วย

```markdown
แผน 4b: [2026-10-24-milestone-4b-auth-endpoints.md](../plans/2026-10-24-milestone-4b-auth-endpoints.md) ลงมือครบ รอตรวจรับ; แผน 4c: [2026-10-27-milestone-4c-google-oidc.md](../plans/2026-10-27-milestone-4c-google-oidc.md) ลงมือครบ รอตรวจรับ
```

- [ ] **Step 3: ตรวจรับในเครื่อง**

Run:
```bash
docker compose ps
make check
make -s test-integration
go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
```
Expected:
- `make check` ผ่าน (tidy, vet, lint, swagger ตรงกับที่ commit, tests)
- `make -s test-integration` ผ่าน ไม่มี SKIP
- `govulncheck`: รายงานผลใน report; ถ้าพบช่องโหว่ใน go-oidc, oauth2 หรือ go-jose ที่เส้นทางของเราเรียกถึง ให้หยุดและแจ้ง controller ห้ามอัปเกรดเอง (ช่องโหว่เดิมของ stdlib/`golang.org/x/text` จากแผน 4a ให้บันทึกว่ายังค้างอยู่) ถ้าไม่มี network ให้บันทึกว่าข้าม

ไม่ทดสอบกับ Google จริงใน task นี้: ต้องใช้ OAuth client ของผู้ใช้และ browser

- [ ] **Step 4: Commit**

```bash
git add README.md docs/superpowers/plans/2026-10-05-phase-1-roadmap.md docs/superpowers/specs/2026-10-22-milestone-4-auth-design.md
git commit -m "docs: add the Google sign-in guide and mark plan 4c done"
```

