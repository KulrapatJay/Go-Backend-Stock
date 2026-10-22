package http

import (
	"net/netip"
	"slices"
	"strings"
)

// ClientIPResolver finds the address of the client for rate limiting and audit. It believes
// X-Forwarded-For only when the connection comes from a configured trusted proxy, and then reads
// it from the right: the entry the trusted proxy itself appended is the last one, and anything to
// its left was written by whoever sent the request and can be forged.
type ClientIPResolver struct{ trusted []netip.Prefix }

// NewClientIPResolver returns a resolver that trusts the given proxy networks (none = trust no header).
func NewClientIPResolver(trusted []netip.Prefix) *ClientIPResolver {
	return &ClientIPResolver{trusted: slices.Clone(trusted)}
}

// Resolve returns the client address, or the zero Addr when remoteAddr cannot be read.
// remoteAddr is the connection's "host:port"; forwardedFor holds the value of every
// X-Forwarded-For header line. A hop that cannot be read, or a header made only of trusted
// proxies, makes Resolve return the proxy's own address: everyone behind it then shares one
// quota, which is safer than guessing.
func (r *ClientIPResolver) Resolve(remoteAddr string, forwardedFor []string) netip.Addr {
	remote, ok := parseRemote(remoteAddr)
	if !ok {
		return netip.Addr{}
	}
	if !r.isTrusted(remote) {
		return remote
	}
	var hops []string
	for _, line := range forwardedFor {
		hops = append(hops, strings.Split(line, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return remote
		}
		hop = hop.Unmap().WithZone("")
		if !r.isTrusted(hop) {
			return hop
		}
	}
	return remote
}

func (r *ClientIPResolver) isTrusted(addr netip.Addr) bool {
	return slices.ContainsFunc(r.trusted, func(p netip.Prefix) bool { return p.Contains(addr) })
}

func parseRemote(remoteAddr string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(remoteAddr); err == nil {
		return ap.Addr().Unmap().WithZone(""), true
	}
	if addr, err := netip.ParseAddr(remoteAddr); err == nil {
		return addr.Unmap().WithZone(""), true
	}
	return netip.Addr{}, false
}
