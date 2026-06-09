package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// ErrorLoggingMiddleware provides comprehensive error logging for all requests
func ErrorLoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Generate request ID if not present
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = fmt.Sprintf("req_%d", time.Now().UnixNano())
			}

			// Add request ID to response headers
			w.Header().Set("X-Request-ID", requestID)

			// Create response wrapper to capture status code
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Log request start
			logger.Debug("request started",
				"request_id", requestID,
				"method", r.Method,
				"path", r.URL.Path,
				"query", r.URL.RawQuery,
				"client_ip", r.RemoteAddr,
				"user_agent", r.UserAgent(),
				"content_length", r.ContentLength,
				"content_type", r.Header.Get("Content-Type"),
			)

			// Recover from panics
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("request panic recovered",
						"request_id", requestID,
						"method", r.Method,
						"path", r.URL.Path,
						"panic", fmt.Sprintf("%v", rec),
						"duration_ms", time.Since(start).Milliseconds(),
					)

					// Write 500 error if response hasn't been written
					if rw.statusCode == http.StatusOK {
						http.Error(w, `{"error":{"message":"Internal server error","type":"server_error"}}`, http.StatusInternalServerError)
					}
				}
			}()

			// Process request
			next.ServeHTTP(rw, r)

			// Log request completion
			duration := time.Since(start)
			logFields := []any{
				"request_id", requestID,
				"method", r.Method,
				"path", r.URL.Path,
				"status_code", rw.statusCode,
				"duration_ms", duration.Milliseconds(),
				"client_ip", r.RemoteAddr,
			}

			if rw.statusCode >= 500 {
				logger.Error("request completed with server error", logFields...)
			} else if rw.statusCode >= 400 {
				logger.Warn("request completed with client error", logFields...)
			} else {
				logger.Debug("request completed", logFields...)
			}
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}
