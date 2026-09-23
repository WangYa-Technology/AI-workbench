package httpapi

import (
	"net/http"
	"net/netip"
	"testing"
)

func TestClientAddressIgnoresForwardedHeadersFromUntrustedPeers(t *testing.T) {
	request, _ := http.NewRequest(http.MethodGet, "http://example.test", nil)
	request.RemoteAddr = "198.51.100.10:443"
	request.Header.Set("X-Forwarded-For", "203.0.113.8")
	if got := clientAddress(request, nil); got != "198.51.100.10" {
		t.Fatalf("untrusted forwarded address was accepted: %q", got)
	}
}

func TestClientAddressUsesForwardedClientOnlyFromConfiguredProxy(t *testing.T) {
	request, _ := http.NewRequest(http.MethodGet, "http://example.test", nil)
	request.RemoteAddr = "10.20.5.7:443"
	request.Header.Set("X-Forwarded-For", "203.0.113.8, 10.20.5.7")
	trusted := []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")}
	if got := clientAddress(request, trusted); got != "203.0.113.8" {
		t.Fatalf("configured proxy forwarded address was not used: %q", got)
	}
	request.Header.Set("X-Forwarded-For", "not-an-ip")
	if got := clientAddress(request, trusted); got != "10.20.5.7" {
		t.Fatalf("malformed forwarded address was accepted: %q", got)
	}
}

func TestClientAddressWalksOnlyTrustedForwardingHops(t *testing.T) {
	trusted := []netip.Prefix{
		netip.MustParsePrefix("10.20.0.0/16"),
		netip.MustParsePrefix("2001:db8:1234::/48"),
	}
	for _, tc := range []struct {
		name, peer string
		fields     []string
		want       string
	}{
		{"client prepends forged address", "10.20.5.7:443", []string{"203.0.113.8, 198.51.100.10"}, "198.51.100.10"},
		{"multiple trusted proxies", "10.20.5.7:443", []string{"203.0.113.8, 198.51.100.10, 10.20.1.2"}, "198.51.100.10"},
		{"stop at untrusted intermediary", "10.20.5.7:443", []string{"203.0.113.8, 192.0.2.44, 10.20.1.2"}, "192.0.2.44"},
		{"repeated header fields", "10.20.5.7:443", []string{"203.0.113.8", "198.51.100.10, 10.20.1.2"}, "198.51.100.10"},
		{"ignore malformed attacker prefix", "10.20.5.7:443", []string{"not-an-ip, , 198.51.100.10, 10.20.1.2"}, "198.51.100.10"},
		{"malformed trusted suffix", "10.20.5.7:443", []string{"203.0.113.8, not-an-ip, 10.20.1.2"}, "10.20.5.7"},
		{"empty trusted suffix", "10.20.5.7:443", []string{"203.0.113.8, , 10.20.1.2"}, "10.20.5.7"},
		{"zone scoped forwarded address", "10.20.5.7:443", []string{"203.0.113.8, fe80::1%eth0"}, "10.20.5.7"},
		{"IPv6 proxies and client", "[2001:db8:1234::2]:443", []string{"203.0.113.8, 2001:db8:5678::1, 2001:db8:1234::1"}, "2001:db8:5678::1"},
		{"mapped IPv4 proxy", "[::ffff:10.20.5.7]:443", []string{"203.0.113.8, 198.51.100.10"}, "198.51.100.10"},
		{"mapped IPv4 client", "10.20.5.7:443", []string{"203.0.113.8, ::ffff:198.51.100.10"}, "198.51.100.10"},
		{"untrusted direct peer", "198.51.100.10:443", []string{"203.0.113.8, 10.20.1.2"}, "198.51.100.10"},
		{"missing header", "10.20.5.7:443", nil, "10.20.5.7"},
		{"empty header", "10.20.5.7:443", []string{""}, "10.20.5.7"},
		{"single sanitized value", "10.20.5.7:443", []string{" 198.51.100.10 "}, "198.51.100.10"},
		{"all hops trusted", "10.20.5.7:443", []string{"10.20.2.3, 10.20.1.2"}, "10.20.2.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, _ := http.NewRequest(http.MethodGet, "http://example.test", nil)
			request.RemoteAddr = tc.peer
			for _, field := range tc.fields {
				request.Header.Add("X-Forwarded-For", field)
			}
			if got := clientAddress(request, trusted); got != tc.want {
				t.Fatalf("client address = %q, want %q", got, tc.want)
			}
		})
	}
}
