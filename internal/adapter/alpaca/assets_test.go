package alpaca

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-backend-stock/internal/core/domain"
)

const (
	testKeyID  = "PKTESTKEYID0123456789"
	testSecret = "alpaca-test-secret-ZQXJ-0123456789"

	aaplAsset = `{"id":"b0b6dd9d-8b9b-48a9-ba46-b9d54906e415","class":"us_equity","exchange":"NASDAQ","symbol":"AAPL","name":"Apple Inc. Common Stock","status":"active","tradable":true}`
)

// fakeAlpaca is an httptest.Server that records what it was asked and answers with answer.
type fakeAlpaca struct {
	*httptest.Server
	hits   atomic.Int32
	method atomic.Value // string
	path   atomic.Value // string, the escaped path
	keyID  atomic.Value // string
	secret atomic.Value // string
	answer func(w http.ResponseWriter, r *http.Request)
}

func newFakeAlpaca(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) *fakeAlpaca {
	t.Helper()
	f := &fakeAlpaca{answer: answer}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.method.Store(r.Method)
		f.path.Store(r.URL.EscapedPath())
		f.keyID.Store(r.Header.Get("APCA-API-KEY-ID"))
		f.secret.Store(r.Header.Get("APCA-API-SECRET-KEY"))
		f.answer(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func answerJSON(status int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// clock is a settable time source for the negative cache.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestAssets(t *testing.T, f *fakeAlpaca, mutate func(*AssetsConfig)) (*Assets, *clock) {
	t.Helper()
	clk := &clock{now: time.Date(2026, time.November, 4, 9, 0, 0, 0, time.UTC)}
	cfg := AssetsConfig{BaseURL: f.URL, KeyID: testKeyID, Secret: testSecret, Timeout: DefaultTimeout,
		NegativeTTL: DefaultNegativeTTL, NegativeMax: DefaultNegativeMax, Now: clk.Now}
	if mutate != nil {
		mutate(&cfg)
	}
	a, err := NewAssets(cfg)
	if err != nil {
		t.Fatalf("NewAssets() error = %v", err)
	}
	return a, clk
}

func mustSymbol(t *testing.T, s string) domain.Symbol {
	t.Helper()
	sym, err := domain.ParseSymbol(s)
	if err != nil {
		t.Fatal(err)
	}
	return sym
}

func TestLookupFindsAnActiveUSEquity(t *testing.T) {
	f := newFakeAlpaca(t, answerJSON(200, aaplAsset))
	a, _ := newTestAssets(t, f, nil)

	got, err := a.Lookup(t.Context(), mustSymbol(t, "AAPL"))
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if f.method.Load() != "GET" || f.path.Load() != "/v2/assets/AAPL" ||
		f.keyID.Load() != testKeyID || f.secret.Load() != testSecret {
		t.Errorf("request = %v %v key=%v secret=%v", f.method.Load(), f.path.Load(), f.keyID.Load(), f.secret.Load())
	}
	if got.Symbol.String() != "AAPL" || got.ProviderAssetID != "b0b6dd9d-8b9b-48a9-ba46-b9d54906e415" ||
		got.Name != "Apple Inc. Common Stock" || got.Exchange != "NASDAQ" || got.AssetClass != domain.AssetClassUSEquity ||
		got.Status != domain.InstrumentStatusActive || got.Source != domain.InstrumentSourceAlpaca {
		t.Errorf("Lookup() = %+v", got)
	}
	if !got.ID.IsZero() || !got.VerifiedAt.IsZero() || !got.CreatedAt.IsZero() {
		t.Errorf("Lookup() filled ID or times: %+v (the service adds them)", got)
	}
	// What the service does next must accept the answer.
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	got.ID, got.VerifiedAt, got.CreatedAt, got.UpdatedAt = id, time.Now().UTC(), time.Now().UTC(), time.Now().UTC()
	if _, err := domain.NewInstrument(got); err != nil {
		t.Errorf("domain.NewInstrument(Lookup()) error = %v", err)
	}
}

func TestLookupKeepsInactiveAssets(t *testing.T) {
	f := newFakeAlpaca(t, answerJSON(200, strings.Replace(aaplAsset, `"active"`, `"inactive"`, 1)))
	a, _ := newTestAssets(t, f, nil)
	got, err := a.Lookup(t.Context(), mustSymbol(t, "AAPL"))
	if err != nil || got.Status != domain.InstrumentStatusInactive {
		t.Fatalf("Lookup() = %+v, %v; want an inactive instrument", got, err)
	}
}

func TestLookupEscapesTheSymbolInThePath(t *testing.T) {
	for _, symbol := range []string{"BRK.B", "BF-B"} {
		t.Run(symbol, func(t *testing.T) {
			f := newFakeAlpaca(t, answerJSON(200, strings.NewReplacer(`"AAPL"`, `"`+symbol+`"`).Replace(aaplAsset)))
			a, _ := newTestAssets(t, f, nil)
			got, err := a.Lookup(t.Context(), mustSymbol(t, symbol))
			if err != nil || got.Symbol.String() != symbol {
				t.Fatalf("Lookup() = %+v, %v", got, err)
			}
			if f.path.Load() != "/v2/assets/"+symbol {
				t.Errorf("path = %v, want /v2/assets/%s", f.path.Load(), symbol)
			}
		})
	}
}

func TestNotFoundAndOtherClassesAreUnknown(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"404 with a JSON body":   {404, `{"code":40410000,"message":"asset not found"}`},
		"404 with no body":       {404, ``},
		"crypto":                 {200, strings.Replace(aaplAsset, `"us_equity"`, `"crypto"`, 1)},
		"unparsable symbol":      {200, strings.Replace(aaplAsset, `"AAPL"`, `"AA PL"`, 1)},
		"symbol with a bad lead": {200, strings.Replace(aaplAsset, `"AAPL"`, `"1AAPL"`, 1)},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeAlpaca(t, answerJSON(tt.status, tt.body))
			a, _ := newTestAssets(t, f, nil)
			_, err := a.Lookup(t.Context(), mustSymbol(t, "AAPL"))
			if !errors.Is(err, domain.ErrUnknownSymbol) {
				t.Fatalf("Lookup() error = %v, want unknown_symbol", err)
			}
		})
	}
}

func TestUnknownSymbolsAreCachedForTenMinutes(t *testing.T) {
	f := newFakeAlpaca(t, answerJSON(404, ``))
	a, clk := newTestAssets(t, f, nil)
	zzzz := mustSymbol(t, "ZZZZ")

	for i := range 2 {
		if _, err := a.Lookup(t.Context(), zzzz); !errors.Is(err, domain.ErrUnknownSymbol) {
			t.Fatalf("lookup %d error = %v", i, err)
		}
		clk.advance(4 * time.Minute)
	}
	if got := f.hits.Load(); got != 1 {
		t.Fatalf("Alpaca was asked %d times within 8 minutes, want 1", got)
	}
	clk.advance(2*time.Minute - time.Nanosecond) // 9m59.999999999s after the first answer
	_, _ = a.Lookup(t.Context(), zzzz)
	if got := f.hits.Load(); got != 1 {
		t.Fatalf("asked %d times just before 10 minutes, want 1", got)
	}
	clk.advance(time.Nanosecond) // exactly 10 minutes
	_, _ = a.Lookup(t.Context(), zzzz)
	if got := f.hits.Load(); got != 2 {
		t.Fatalf("asked %d times at 10 minutes, want a fresh question (2)", got)
	}
}

func TestOnlyUnknownAnswersAreCached(t *testing.T) {
	f := newFakeAlpaca(t, answerJSON(500, `{"message":"boom"}`))
	a, _ := newTestAssets(t, f, nil)
	for range 2 {
		if _, err := a.Lookup(t.Context(), mustSymbol(t, "AAPL")); err == nil || errors.Is(err, domain.ErrUnknownSymbol) {
			t.Fatalf("Lookup() error = %v, want a retryable error", err)
		}
	}
	if got := f.hits.Load(); got != 2 {
		t.Errorf("a 500 was cached: %d requests, want 2", got)
	}
	// A symbol that was found is not remembered either: the database keeps it.
	g := newFakeAlpaca(t, answerJSON(200, aaplAsset))
	b, _ := newTestAssets(t, g, nil)
	for range 2 {
		if _, err := b.Lookup(t.Context(), mustSymbol(t, "AAPL")); err != nil {
			t.Fatal(err)
		}
	}
	if got := g.hits.Load(); got != 2 {
		t.Errorf("a found asset was cached: %d requests, want 2", got)
	}
}

func TestNegativeCacheIsBounded(t *testing.T) {
	f := newFakeAlpaca(t, answerJSON(404, ``))
	a, clk := newTestAssets(t, f, func(c *AssetsConfig) { c.NegativeMax = 2 })
	for _, s := range []string{"AAA", "BBB", "CCC"} {
		clk.advance(time.Second)
		_, _ = a.Lookup(t.Context(), mustSymbol(t, s))
	}
	if got := f.hits.Load(); got != 3 {
		t.Fatalf("hits = %d, want 3", got)
	}
	_, _ = a.Lookup(t.Context(), mustSymbol(t, "CCC")) // newest: still remembered
	if got := f.hits.Load(); got != 3 {
		t.Errorf("CCC was asked again (hits = %d), want it cached", got)
	}
	_, _ = a.Lookup(t.Context(), mustSymbol(t, "AAA")) // oldest: evicted to make room
	if got := f.hits.Load(); got != 4 {
		t.Errorf("AAA was not asked again (hits = %d), want the oldest entry evicted", got)
	}
}

func TestRejectedCredentialsAreNotCachedAndNotLeaked(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			// A hostile or sloppy server may repeat the credentials in its body.
			f := newFakeAlpaca(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"bad key ` + r.Header.Get("APCA-API-KEY-ID") + ` / ` + r.Header.Get("APCA-API-SECRET-KEY") + `"}`))
			})
			a, _ := newTestAssets(t, f, nil)
			var err error
			for range 2 {
				_, err = a.Lookup(t.Context(), mustSymbol(t, "AAPL"))
				if err == nil || errors.Is(err, domain.ErrUnknownSymbol) {
					t.Fatalf("Lookup() error = %v, want a retryable error", err)
				}
			}
			if got := f.hits.Load(); got != 2 {
				t.Errorf("requests = %d, want 2 (not cached)", got)
			}
			msg := err.Error()
			if strings.Contains(msg, testKeyID) || strings.Contains(msg, testSecret) {
				t.Errorf("error %q leaks a credential", msg)
			}
			if !strings.Contains(msg, strconv.Itoa(status)) {
				t.Errorf("error %q hides the HTTP status %d", msg, status)
			}
		})
	}
}

func TestLookupGivesUpAfterTheTimeout(t *testing.T) {
	f := newFakeAlpaca(t, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	a, _ := newTestAssets(t, f, func(c *AssetsConfig) { c.Timeout = 50 * time.Millisecond })

	start := time.Now()
	_, err := a.Lookup(t.Context(), mustSymbol(t, "AAPL"))
	if err == nil || errors.Is(err, domain.ErrUnknownSymbol) {
		t.Fatalf("Lookup() error = %v, want a retryable error", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("Lookup() took %v, want it to stop at the 50ms timeout", took)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before := f.hits.Load()
	if _, err := a.Lookup(ctx, mustSymbol(t, "MSFT")); err == nil || f.hits.Load() != before {
		t.Errorf("cancelled context: error = %v, extra requests = %d, want an error and no request", err, f.hits.Load()-before)
	}
}

func TestMalformedAnswersAreErrors(t *testing.T) {
	without := func(field string) string {
		return strings.Replace(aaplAsset, `"`+field+`"`, `"x_`+field+`"`, 1)
	}
	bodies := map[string]string{
		"not JSON":       `<html>bad gateway</html>`,
		"empty":          ``,
		"an array":       `[]`,
		"no id":          without("id"),
		"no symbol":      without("symbol"),
		"no name":        without("name"),
		"no exchange":    without("exchange"),
		"unknown status": strings.Replace(aaplAsset, `"active"`, `"delisted"`, 1),
		"empty name":     strings.Replace(aaplAsset, `"Apple Inc. Common Stock"`, `""`, 1),
		// A complete, valid asset that is only too big: the size limit alone must refuse it.
		"oversized": strings.Replace(aaplAsset, `"tradable":true`, `"padding":"`+strings.Repeat("x", 70<<10)+`"`, 1),
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			f := newFakeAlpaca(t, answerJSON(200, body))
			a, _ := newTestAssets(t, f, nil)
			_, err := a.Lookup(t.Context(), mustSymbol(t, "AAPL"))
			if err == nil || errors.Is(err, domain.ErrUnknownSymbol) {
				t.Fatalf("Lookup() error = %v, want an error that is not unknown_symbol", err)
			}
		})
	}
}

func TestLongNamesAreCutTo200Characters(t *testing.T) {
	long := strings.Repeat("ก", 250)
	f := newFakeAlpaca(t, answerJSON(200, strings.Replace(aaplAsset, `"Apple Inc. Common Stock"`, `"`+long+`"`, 1)))
	a, _ := newTestAssets(t, f, nil)
	got, err := a.Lookup(t.Context(), mustSymbol(t, "AAPL"))
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if n := len([]rune(got.Name)); n != 200 {
		t.Errorf("name has %d characters, want 200", n)
	}
	id, _ := domain.NewID()
	got.ID, got.VerifiedAt, got.CreatedAt, got.UpdatedAt = id, time.Now().UTC(), time.Now().UTC(), time.Now().UTC()
	if _, err := domain.NewInstrument(got); err != nil {
		t.Errorf("domain.NewInstrument() error = %v", err)
	}
}

func TestNewAssetsValidatesItsConfig(t *testing.T) {
	ok := AssetsConfig{BaseURL: "https://paper-api.alpaca.markets", KeyID: testKeyID, Secret: testSecret,
		Timeout: time.Second, NegativeTTL: time.Minute, NegativeMax: 10}
	if _, err := NewAssets(ok); err != nil {
		t.Fatalf("NewAssets(valid) error = %v", err)
	}
	mutations := map[string]func(*AssetsConfig){
		"no base URL": func(c *AssetsConfig) { c.BaseURL = "" },
		"no key id":   func(c *AssetsConfig) { c.KeyID = "" },
		"no secret":   func(c *AssetsConfig) { c.Secret = "" },
		"no timeout":  func(c *AssetsConfig) { c.Timeout = 0 },
		"no ttl":      func(c *AssetsConfig) { c.NegativeTTL = 0 },
		"no max":      func(c *AssetsConfig) { c.NegativeMax = 0 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			cfg := ok
			mutate(&cfg)
			_, err := NewAssets(cfg)
			if err == nil {
				t.Fatal("NewAssets() error = nil")
			}
			if strings.Contains(err.Error(), testKeyID) || strings.Contains(err.Error(), testSecret) {
				t.Errorf("error %q leaks a credential", err)
			}
		})
	}
}

// A redirect must never carry the key pair to another host: Go copies custom headers such as
// APCA-API-SECRET-KEY to the new location, so the adapter must not follow redirects at all.
func TestRedirectsAreNotFollowedAndNeverCarryTheSecret(t *testing.T) {
	elsewhere := newFakeAlpaca(t, answerJSON(200, aaplAsset))
	f := newFakeAlpaca(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/v2/assets/AAPL", http.StatusFound)
	})
	a, _ := newTestAssets(t, f, nil)

	for range 2 {
		_, err := a.Lookup(t.Context(), mustSymbol(t, "AAPL"))
		if err == nil || errors.Is(err, domain.ErrUnknownSymbol) {
			t.Fatalf("Lookup() error = %v, want a retryable error", err)
		}
		if msg := err.Error(); strings.Contains(msg, testSecret) || strings.Contains(msg, testKeyID) || !strings.Contains(msg, "302") {
			t.Errorf("error %q leaks a credential or hides the HTTP status 302", msg)
		}
	}
	if got := elsewhere.hits.Load(); got != 0 {
		t.Errorf("the redirect target received %d requests (key %v, secret %v), want none", got, elsewhere.keyID.Load(), elsewhere.secret.Load())
	}
	if got := f.hits.Load(); got != 2 {
		t.Errorf("requests = %d, want 2 (a redirect is not cached)", got)
	}
}

// domain.NewInstrument refuses these, and the service turns its refusal into a 500: the adapter must
// refuse them first, as an error the caller can retry (503).
func TestAnswersThatTheInstrumentRulesRefuseAreRetryableErrors(t *testing.T) {
	bodies := map[string]string{
		"exchange longer than 20":       strings.Replace(aaplAsset, `"NASDAQ"`, `"AVERYLONGEXCHANGENAME123"`, 1),
		"asset id longer than 64":       strings.Replace(aaplAsset, `"b0b6dd9d-8b9b-48a9-ba46-b9d54906e415"`, `"`+strings.Repeat("a", 65)+`"`, 1),
		"control character in the name": strings.Replace(aaplAsset, `"Apple Inc. Common Stock"`, `"Apple\u0000Inc"`, 1),
		"newline in the exchange":       strings.Replace(aaplAsset, `"NASDAQ"`, `"NAS\nDAQ"`, 1),
		"control character in the id":   strings.Replace(aaplAsset, `"b0b6dd9d-8b9b-48a9-ba46-b9d54906e415"`, `"b0b6\u0007dd9d"`, 1),
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			f := newFakeAlpaca(t, answerJSON(200, body))
			a, _ := newTestAssets(t, f, nil)
			got, err := a.Lookup(t.Context(), mustSymbol(t, "AAPL"))
			if err == nil || errors.Is(err, domain.ErrUnknownSymbol) {
				t.Fatalf("Lookup() = %+v, %v; want an error that is not unknown_symbol", got, err)
			}
			if strings.Contains(err.Error(), "AVERYLONG") || strings.Contains(err.Error(), "Apple") {
				t.Errorf("error %q repeats what Alpaca sent", err)
			}
			if _, again := a.Lookup(t.Context(), mustSymbol(t, "AAPL")); again == nil || f.hits.Load() != 2 {
				t.Errorf("hits = %d after two lookups, want 2 (not cached)", f.hits.Load())
			}
		})
	}
}

// Whatever Lookup returns must be accepted by domain.NewInstrument once the service adds the ID and
// times: that is what keeps a well-formed answer from turning into a 500.
func TestEveryAnswerLookupAcceptsPassesTheInstrumentRules(t *testing.T) {
	for _, exchange := range []string{"NASDAQ", "NYSEARCA", strings.Repeat("X", 20)} {
		f := newFakeAlpaca(t, answerJSON(200, strings.Replace(aaplAsset, `"NASDAQ"`, `"`+exchange+`"`, 1)))
		a, _ := newTestAssets(t, f, nil)
		got, err := a.Lookup(t.Context(), mustSymbol(t, "AAPL"))
		if err != nil {
			t.Fatalf("Lookup(%s) error = %v", exchange, err)
		}
		id, _ := domain.NewID()
		got.ID, got.VerifiedAt, got.CreatedAt, got.UpdatedAt = id, time.Now().UTC(), time.Now().UTC(), time.Now().UTC()
		if _, err := domain.NewInstrument(got); err != nil {
			t.Errorf("domain.NewInstrument(Lookup() with exchange %q) error = %v", exchange, err)
		}
	}
}
