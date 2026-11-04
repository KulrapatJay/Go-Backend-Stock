package alpaca

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	// defaultRetryWait is how long a retry waits when Alpaca names no Retry-After.
	defaultRetryWait = 250 * time.Millisecond
	// retryMargin is the time a retry must still have left after the wait for its own request.
	retryMargin = 500 * time.Millisecond
)

// apiClient sends the GET requests of every Alpaca API this package uses: the key pair as headers, a
// timeout per attempt, no redirects (Go copies custom headers such as APCA-API-SECRET-KEY to the new
// location, even on another host), a capped body and, when retries is above zero, one more attempt
// after a failure that can pass by itself.
type apiClient struct {
	keyID, secret string
	timeout       time.Duration
	client        *http.Client
	retries       int
}

// newAPIClient returns a client. httpClient nil builds one that follows no redirect. retries is how
// many more attempts a transient failure gets (0 or 1).
func newAPIClient(keyID, secret string, timeout time.Duration, httpClient *http.Client, retries int) *apiClient {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return &apiClient{keyID: keyID, secret: secret, timeout: timeout, client: httpClient, retries: retries}
}

// get asks rawURL. It returns the HTTP status of the last attempt, and the body only for 200. Any HTTP
// answer is a result, not an error, so a caller decides what 404 or 403 means; err is for transport
// failures, a body above maxBytes and a cancelled context. A network error, a timeout, 429 and 5xx are
// tried once more when the context has time for the wait (Retry-After seconds, else 250 ms) and a
// request. Errors name the HTTP status or the kind of failure, never the key, the secret or a body.
func (c *apiClient) get(ctx context.Context, rawURL string, maxBytes int64) (int, []byte, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, nil, fmt.Errorf("alpaca: %w", err)
		}
		status, body, wait, err := c.once(ctx, rawURL, maxBytes)
		transient := err != nil && !errors.Is(err, errBodyTooLarge) && ctx.Err() == nil ||
			err == nil && (status == http.StatusTooManyRequests || status >= 500)
		if !transient || attempt >= c.retries || !hasTimeFor(ctx, wait) {
			return status, body, err
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return status, body, err
		}
	}
}

var errBodyTooLarge = errors.New("alpaca: answer is larger than the limit")

// once makes one attempt and says how long a retry would wait.
func (c *apiClient) once(ctx context.Context, rawURL string, maxBytes int64) (status int, body []byte, wait time.Duration, err error) {
	wait = defaultRetryWait
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return 0, nil, wait, errors.New("alpaca: could not build the request")
	}
	req.Header.Set("APCA-API-KEY-ID", c.keyID)
	req.Header.Set("APCA-API-SECRET-KEY", c.secret)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		// The error text of net/http holds the URL, which is public, but nothing else worth keeping.
		return 0, nil, wait, fmt.Errorf("alpaca: request failed: %w", unwrapTransport(err))
	}
	defer resp.Body.Close()
	if seconds, convErr := strconv.Atoi(resp.Header.Get("Retry-After")); convErr == nil && seconds >= 0 {
		wait = time.Duration(seconds) * time.Second
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil, wait, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return 0, nil, wait, fmt.Errorf("alpaca: read answer: %w", unwrapTransport(err))
	}
	if int64(len(raw)) > maxBytes {
		return 0, nil, wait, errBodyTooLarge
	}
	return resp.StatusCode, raw, wait, nil
}

// unwrapTransport keeps the cause (a timeout, a refused connection) without the request URL.
func unwrapTransport(err error) error {
	var urlErr interface{ Unwrap() error }
	if errors.As(err, &urlErr) {
		if inner := urlErr.Unwrap(); inner != nil {
			return inner
		}
	}
	return err
}

// hasTimeFor reports whether ctx still has the wait and room for one more attempt.
func hasTimeFor(ctx context.Context, wait time.Duration) bool {
	deadline, ok := ctx.Deadline()
	return !ok || time.Until(deadline) > wait+retryMargin
}

// statusError is the error for an HTTP status a caller does not accept. It names the status only.
func statusError(what string, status int) error {
	return fmt.Errorf("%s: unexpected HTTP status %d", what, status)
}
