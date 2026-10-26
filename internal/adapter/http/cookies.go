package http

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
)

// cookieJar names and writes the auth cookies. Over HTTPS the names carry the __Host- prefix, which
// makes the browser insist on Secure, Path=/ and no Domain (phase 1 spec section 10).
type cookieJar struct {
	secure      bool
	sessionName string
	nonceName   string
}

func newCookieJar(secure bool) cookieJar {
	if secure {
		return cookieJar{secure: true, sessionName: "__Host-session", nonceName: "__Host-csrf_nonce"}
	}
	return cookieJar{sessionName: "session", nonceName: "csrf_nonce"}
}

// set writes an HttpOnly, SameSite=Lax cookie for the whole site that expires at expires. It sets no
// Domain, so the cookie belongs to this host only.
func (j cookieJar) set(c fiber.Ctx, name, value string, expires time.Time) {
	c.Cookie(&fiber.Cookie{
		Name: name, Value: value, Path: "/", Expires: expires.UTC(),
		Secure: j.secure, HTTPOnly: true, SameSite: fiber.CookieSameSiteLaxMode,
	})
}

// clear expires the cookie with the same attributes it was set with, so the browser accepts the
// deletion (a __Host- cookie must keep Secure and Path=/).
func (j cookieJar) clear(c fiber.Ctx, name string) { //nolint:unused // logout uses it in task 11.
	c.Cookie(&fiber.Cookie{
		Name: name, Value: "", Path: "/", Expires: fasthttp.CookieExpireDelete,
		Secure: j.secure, HTTPOnly: true, SameSite: fiber.CookieSameSiteLaxMode,
	})
}
