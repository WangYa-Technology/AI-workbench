package httpapi

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) metricsEndpoint(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	body, err := s.metrics.Render(ctx, s.pool)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(body))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}
