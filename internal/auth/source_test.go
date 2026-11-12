package auth

import (
	"net/netip"
	"testing"
)

func prefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	var out []netip.Prefix
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestSourceAddr(t *testing.T) {
	trusted := prefixes(t, "10.0.0.0/8", "fd00::/8")
	tests := []struct {
		name    string
		remote  string
		xff     []string
		trusted []netip.Prefix
		want    string
	}{
		{"connection address by default", "203.0.113.5:4000", nil, nil, "203.0.113.5"},
		{"no port is accepted", "203.0.113.5", nil, nil, "203.0.113.5"},
		{"untrusted connection ignores X-Forwarded-For", "203.0.113.5:4000", []string{"198.51.100.9"}, trusted, "203.0.113.5"},
		{"no trusted proxies ignores X-Forwarded-For", "10.0.0.2:4000", []string{"198.51.100.9"}, nil, "10.0.0.2"},
		{"trusted proxy uses the forwarded client", "10.0.0.2:4000", []string{"198.51.100.9"}, trusted, "198.51.100.9"},
		{"forged leftmost address is not used", "10.0.0.2:4000", []string{"1.2.3.4, 198.51.100.9"}, trusted, "198.51.100.9"},
		{"rightmost untrusted address wins over trusted hops", "10.0.0.2:4000", []string{"1.2.3.4, 198.51.100.9, 10.0.0.7"}, trusted, "198.51.100.9"},
		{"several header lines are joined in order", "10.0.0.2:4000", []string{"1.2.3.4", "198.51.100.9"}, trusted, "198.51.100.9"},
		{"missing header falls back to the connection", "10.0.0.2:4000", nil, trusted, "10.0.0.2"},
		{"malformed header falls back to the connection", "10.0.0.2:4000", []string{"not-an-ip"}, trusted, "10.0.0.2"},
		{"malformed hop before an untrusted one falls back", "10.0.0.2:4000", []string{"198.51.100.9, junk, 10.0.0.7"}, trusted, "10.0.0.2"},
		{"only trusted hops falls back to the connection", "10.0.0.2:4000", []string{"10.0.0.8, 10.0.0.7"}, trusted, "10.0.0.2"},
		{"ipv6 connection", "[2001:db8::1]:4000", nil, trusted, "2001:db8::1"},
		{"ipv6 trusted proxy and ipv6 client", "[fd00::2]:4000", []string{"2001:db8::9"}, trusted, "2001:db8::9"},
		{"ipv4-mapped ipv6 is unmapped", "[::ffff:10.0.0.2]:4000", []string{"::ffff:198.51.100.9"}, trusted, "198.51.100.9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SourceAddr(tt.remote, tt.xff, tt.trusted); got != tt.want {
				t.Errorf("SourceAddr(%q, %q) = %q, want %q", tt.remote, tt.xff, got, tt.want)
			}
		})
	}
}
