package domain

import "net/netip"

// Principal is the caller of an authenticated request: the account and the session the request came
// with. It comes only from a verified session, never from the request body.
type Principal struct {
	UserID    ID
	SessionID ID
}

// IsZero reports whether p is the zero value (no authenticated caller).
func (p Principal) IsZero() bool { return p.UserID.IsZero() }

// RequestMeta is what a use case may know about the request beyond its input. ClientIP is the full
// address and is used for rate limiting only; audit metadata stores MaskedIP.
type RequestMeta struct {
	RequestID ID
	ClientIP  netip.Addr
	MaskedIP  string
	UserAgent string
}

// NewRequestMeta builds the metadata of one request: the client IP is unmapped and masked and the
// user agent is sanitized for storage.
func NewRequestMeta(requestID ID, clientIP netip.Addr, userAgent string) RequestMeta {
	if clientIP.IsValid() {
		clientIP = clientIP.Unmap().WithZone("")
	}
	return RequestMeta{
		RequestID: requestID,
		ClientIP:  clientIP,
		MaskedIP:  MaskIP(clientIP),
		UserAgent: SanitizeUserAgent(userAgent),
	}
}
