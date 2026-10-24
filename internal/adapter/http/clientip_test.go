package http

import (
	"net/netip"
	"testing"
)

func TestClientIPResolver(t *testing.T) {
	resolver := NewClientIPResolver([]netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("2001:db8::/32"),
	})
	tests := []struct {
		name      string
		remote    string
		forwarded []string
		want      string // "" means the zero Addr
	}{
		{"direct client", "203.0.113.9:5000", nil, "203.0.113.9"},
		{"untrusted peer cannot choose its address", "203.0.113.9:5000", []string{"1.2.3.4"}, "203.0.113.9"},
		{"trusted proxy forwards the client", "10.0.0.5:4000", []string{"198.51.100.7"}, "198.51.100.7"},
		{"trusted proxy chain", "10.0.0.5:4000", []string{"198.51.100.7, 10.0.0.9"}, "198.51.100.7"},
		{"forged left-most entry is ignored", "10.0.0.5:4000", []string{"6.6.6.6, 198.51.100.7"}, "198.51.100.7"},
		{"several header lines", "10.0.0.5:4000", []string{"6.6.6.6", "198.51.100.7"}, "198.51.100.7"},
		{"every hop trusted", "10.0.0.5:4000", []string{"10.0.0.8, 10.0.0.9"}, "10.0.0.5"},
		{"trusted proxy without header", "10.0.0.5:4000", nil, "10.0.0.5"},
		{"unreadable hop", "10.0.0.5:4000", []string{"garbage"}, "10.0.0.5"},
		{"unreadable hop next to the proxy", "10.0.0.5:4000", []string{"198.51.100.7, garbage"}, "10.0.0.5"},
		{"hop with a port", "10.0.0.5:4000", []string{"198.51.100.7:1234"}, "10.0.0.5"},
		{"empty hop", "10.0.0.5:4000", []string{"198.51.100.7,"}, "10.0.0.5"},
		{"ipv6 client", "[2001:4860::1]:443", nil, "2001:4860::1"},
		{"ipv6 trusted proxy", "[2001:db8::1]:443", []string{"2001:4860::7"}, "2001:4860::7"},
		{"ipv4 in ipv6 notation", "[::ffff:203.0.113.9]:80", nil, "203.0.113.9"},
		{"ipv4-mapped proxy hop", "10.0.0.5:4000", []string{"::ffff:198.51.100.7"}, "198.51.100.7"},
		{"unreadable remote", "garbage", []string{"198.51.100.7"}, ""},
		{"empty remote", "", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolver.Resolve(tt.remote, tt.forwarded)
			want := netip.Addr{}
			if tt.want != "" {
				want = netip.MustParseAddr(tt.want)
			}
			if got != want {
				t.Errorf("Resolve(%q, %q) = %v, want %v", tt.remote, tt.forwarded, got, want)
			}
		})
	}
}

func TestClientIPResolverWithoutTrustedProxiesIgnoresHeaders(t *testing.T) {
	for _, resolver := range []*ClientIPResolver{NewClientIPResolver(nil), NewClientIPResolver([]netip.Prefix{})} {
		got := resolver.Resolve("10.0.0.5:4000", []string{"198.51.100.7"})
		if want := netip.MustParseAddr("10.0.0.5"); got != want {
			t.Errorf("Resolve() = %v, want the peer %v: with no trusted proxy no header is believed", got, want)
		}
	}
}
