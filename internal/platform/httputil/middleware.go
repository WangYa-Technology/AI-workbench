package httputil

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

type RequestObservation struct {
	RequestID     string
	Method        string
	Route         string
	Status        int
	Duration      time.Duration
	ResponseBytes int64
}

type RequestObserver func(context.Context, RequestObservation) error

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func Middleware(logger *slog.Logger, allowedOrigin string, observers ...RequestObserver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
			if !validRequestID.MatchString(requestID) {
				requestID = uuid.NewString()
			}
			w.Header().Set("X-Request-ID", requestID)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			if r.Header.Get("Origin") == allowedOrigin {
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Request-ID, Idempotency-Key")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			}
			ctx := WithRequestID(r.Context(), requestID)
			request := r.WithContext(ctx)
			wrapped := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Error("http panic", "request_id", requestID, "error", recovered, "stack", string(debug.Stack()))
					WriteError(wrapped, request, http.StatusInternalServerError, "internal_error", "The request could not be completed.", true)
				}
				duration := time.Since(started)
				status := wrapped.Status()
				if status == 0 {
					status = http.StatusOK
				}
				route := chi.RouteContext(request.Context()).RoutePattern()
				if route == "" {
					route = "unmatched"
				}
				observation := RequestObservation{RequestID: requestID, Method: r.Method, Route: route, Status: status, Duration: duration, ResponseBytes: int64(wrapped.BytesWritten())}
				logger.Info("http request", "request_id", requestID, "method", r.Method, "route", route, "status", status, "response_bytes", observation.ResponseBytes, "duration_ms", duration.Milliseconds())
				if len(observers) > 0 && strings.HasPrefix(r.URL.Path, "/api/v1") {
					recordContext, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), 750*time.Millisecond)
					defer cancel()
					for _, observer := range observers {
						if observer == nil {
							continue
						}
						if err := observer(recordContext, observation); err != nil {
							logger.Warn("record request observation", "request_id", requestID, "error", err)
						}
					}
				}
			}()
			if r.Method == http.MethodOptions {
				wrapped.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(wrapped, request)
		})
	}
}
