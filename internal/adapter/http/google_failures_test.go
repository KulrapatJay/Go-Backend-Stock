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
