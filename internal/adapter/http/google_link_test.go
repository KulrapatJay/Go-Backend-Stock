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
