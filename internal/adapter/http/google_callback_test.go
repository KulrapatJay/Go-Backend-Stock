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
