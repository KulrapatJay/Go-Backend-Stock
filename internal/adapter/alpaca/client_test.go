package alpaca

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sequence answers the n-th request with the n-th handler and repeats the last one.
func sequence(handlers ...func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	var n atomic.Int32
	return func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		if i >= len(handlers) {
			i = len(handlers) - 1
		}
		handlers[i](w, r)
	}
}

func status(code int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func ok(body string) func(http.ResponseWriter, *http.Request) { return answerJSON(200, body) }

func newTestClient(retries int) *apiClient {
	return newAPIClient(testKeyID, testSecret, 2*time.Second, nil, retries)
}

func TestClientRetriesTransientFailuresOnce(t *testing.T) {
	t.Run("a 503 then a 200", func(t *testing.T) {
		f := newFakeAlpaca(t, sequence(status(503), ok(`{"a":1}`)))
		code, body, err := newTestClient(1).get(t.Context(), f.URL+"/x", 1024)
		if err != nil || code != 200 || string(body) != `{"a":1}` || f.hits.Load() != 2 {
			t.Fatalf("get = %d %q %v after %d requests, want 200 after 2", code, body, err, f.hits.Load())
		}
	})
	t.Run("a 429 with Retry-After waits for it", func(t *testing.T) {
		f := newFakeAlpaca(t, sequence(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
		}, ok(`{}`)))
		start := time.Now()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		code, _, err := newTestClient(1).get(ctx, f.URL+"/x", 1024)
		if err != nil || code != 200 || f.hits.Load() != 2 {
			t.Fatalf("get = %d %v after %d requests, want 200 after 2", code, err, f.hits.Load())
		}
		if waited := time.Since(start); waited < time.Second {
			t.Errorf("waited %v, want at least the 1s Retry-After", waited)
		}
	})
	t.Run("a network error then a 200", func(t *testing.T) {
		f := newFakeAlpaca(t, sequence(func(w http.ResponseWriter, _ *http.Request) {
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			_ = conn.Close() // the connection drops without an answer
		}, ok(`{}`)))
		code, _, err := newTestClient(1).get(t.Context(), f.URL+"/x", 1024)
		if err != nil || code != 200 || f.hits.Load() != 2 {
			t.Fatalf("get = %d %v after %d requests, want 200 after 2", code, err, f.hits.Load())
		}
	})
	t.Run("two 500s stop at two requests", func(t *testing.T) {
		f := newFakeAlpaca(t, status(500))
		code, body, err := newTestClient(1).get(t.Context(), f.URL+"/x", 1024)
		if err != nil || code != 500 || body != nil || f.hits.Load() != 2 {
			t.Fatalf("get = %d %q %v after %d requests, want the 500 after exactly 2", code, body, err, f.hits.Load())
		}
		if msg := statusError("alpaca bars", code).Error(); !strings.Contains(msg, "500") {
			t.Errorf("statusError = %q, want the HTTP status in it", msg)
		}
	})
	t.Run("no retries configured", func(t *testing.T) {
		f := newFakeAlpaca(t, status(500))
		if code, _, _ := newTestClient(0).get(t.Context(), f.URL+"/x", 1024); code != 500 || f.hits.Load() != 1 {
			t.Fatalf("status %d after %d requests, want 500 after 1", code, f.hits.Load())
		}
	})
}

func TestClientDoesNotRetryWhatCannotSucceed(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404} {
		f := newFakeAlpaca(t, status(code))
		got, _, err := newTestClient(1).get(t.Context(), f.URL+"/x", 1024)
		if err != nil || got != code || f.hits.Load() != 1 {
			t.Errorf("status %d: get = %d %v after %d requests, want one request", code, got, err, f.hits.Load())
		}
	}
}

func TestClientDoesNotRetryWithoutTimeLeft(t *testing.T) {
	f := newFakeAlpaca(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(429)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	start := time.Now()

	code, _, err := newTestClient(1).get(ctx, f.URL+"/x", 1024)

	if err != nil || code != 429 || f.hits.Load() != 1 {
		t.Fatalf("get = %d %v after %d requests, want the 429 after one", code, err, f.hits.Load())
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("get took %v, want it to give up at once instead of waiting for a Retry-After it cannot afford", took)
	}
}

func TestClientNeverLeaksTheKeyPairOrFollowsRedirects(t *testing.T) {
	elsewhere := newFakeAlpaca(t, ok(`{}`))
	f := newFakeAlpaca(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/x", http.StatusFound)
	})
	code, _, err := newTestClient(1).get(t.Context(), f.URL+"/x", 1024)
	if err != nil || code != 302 || elsewhere.hits.Load() != 0 {
		t.Fatalf("get = %d %v, redirect target hit %d times; want the 302 and no request to the target", code, err, elsewhere.hits.Load())
	}

	down := newFakeAlpaca(t, status(500))
	url := down.URL + "/x"
	down.Close()
	_, _, err = newTestClient(1).get(t.Context(), url, 1024)
	if err == nil {
		t.Fatal("get on a closed server returned no error")
	}
	if msg := err.Error(); strings.Contains(msg, testKeyID) || strings.Contains(msg, testSecret) {
		t.Errorf("error %q leaks a credential", msg)
	}
}

func TestClientCapsTheBodyAndStopsOnCancel(t *testing.T) {
	f := newFakeAlpaca(t, ok(`{"padding":"`+strings.Repeat("x", 4096)+`"}`))
	if _, _, err := newTestClient(1).get(t.Context(), f.URL+"/x", 1024); err == nil {
		t.Error("a body larger than maxBytes returned no error")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before := f.hits.Load()
	if _, _, err := newTestClient(1).get(ctx, f.URL+"/x", 1024); err == nil || f.hits.Load() != before {
		t.Errorf("cancelled context: error = %v, extra requests = %d; want an error and no request", err, f.hits.Load()-before)
	}
}

func TestClientSendsTheKeyPairAsHeaders(t *testing.T) {
	f := newFakeAlpaca(t, ok(`{}`))
	if _, _, err := newTestClient(0).get(t.Context(), f.URL+"/x?a=1", 1024); err != nil {
		t.Fatal(err)
	}
	if f.keyID.Load() != testKeyID || f.secret.Load() != testSecret || f.method.Load() != "GET" {
		t.Errorf("request = %v key=%v secret=%v", f.method.Load(), f.keyID.Load(), f.secret.Load())
	}
}
