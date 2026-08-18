package main

import "testing"

func TestPublicHTTPSOrigin(t *testing.T) {
	for _, value := range []string{"https://staging.example.test", "https://app.example.test:443"} {
		if !publicHTTPSOrigin(value) {
			t.Fatalf("expected public HTTPS origin: %s", value)
		}
	}
	for _, value := range []string{"http://localhost:5173", "https://example.test/path", "https://example.test/?q=1", "example.test"} {
		if publicHTTPSOrigin(value) {
			t.Fatalf("unsafe origin accepted: %s", value)
		}
	}
}
