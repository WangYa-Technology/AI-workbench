package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Expire the ordinary response deadline before the app handles the request.
// Media must establish its own bounded transfer deadline after authorization;
// JSON endpoints must not gain that longer budget.
func shortMediaDeadlineServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		handler.ServeHTTP(w, r)
	}))
	server.Config.WriteTimeout = 5 * time.Millisecond
	server.Start()
	t.Cleanup(server.Close)
	return server
}
