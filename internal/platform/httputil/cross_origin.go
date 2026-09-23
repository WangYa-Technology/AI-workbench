package httputil

import (
	"net/http"
	"strings"
)

// ProtectBrowserWrites rejects unsafe cross-origin browser requests before
// they reach business handlers. CORS controls response access, not execution.
// Callers may exempt exact, method-qualified server-to-server endpoints only
// when those endpoints independently authenticate every incoming request.
func ProtectBrowserWrites(allowedOrigin string, bypassPatterns ...string) func(http.Handler) http.Handler {
	protection := http.NewCrossOriginProtection()
	if origin := strings.TrimRight(allowedOrigin, "/"); origin != "" {
		if err := protection.AddTrustedOrigin(origin); err != nil {
			panic("WEB_ORIGIN must be a valid origin for browser request protection")
		}
	}
	for _, pattern := range bypassPatterns {
		protection.AddInsecureBypassPattern(pattern)
	}
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		WriteError(w, r, http.StatusForbidden, "cross_origin_request", "Open this action from the site's own page and try again.", false)
	}))
	return protection.Handler
}
