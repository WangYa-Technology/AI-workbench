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
