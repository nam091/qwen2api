package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/keaume34/qwen2api/internal/claude"
	"github.com/keaume34/qwen2api/internal/openai"
)

// ErrorHandler provides enhanced error handling with detailed logging
type ErrorHandler struct {
	logger *slog.Logger
}

// NewErrorHandler creates a new ErrorHandler instance
func NewErrorHandler(logger *slog.Logger) *ErrorHandler {
	return &ErrorHandler{logger: logger}
}

// ErrorContext holds context information for error logging
type ErrorContext struct {
	RequestID   string
	Method      string
	Path        string
	ClientIP    string
	UserAgent   string
	StatusCode  int
	ErrorCode   string
	ErrorType   string
	Message     string
	Stack       string
	Timestamp   time.Time
	Duration    time.Duration
	UpstreamErr error
}

// LogAndWriteError logs the error with full context and writes appropriate response
func (eh *ErrorHandler) LogAndWriteError(w http.ResponseWriter, r *http.Request, errCtx ErrorContext) {
	// Capture stack trace if not provided
	if errCtx.Stack == "" {
		errCtx.Stack = string(debug.Stack())
	}
	errCtx.Timestamp = time.Now()

	// Log with structured fields
	logFields := []any{
		"request_id", errCtx.RequestID,
		"method", errCtx.Method,
		"path", errCtx.Path,
		"client_ip", errCtx.ClientIP,
		"user_agent", errCtx.UserAgent,
		"status_code", errCtx.StatusCode,
		"error_code", errCtx.ErrorCode,
		"error_type", errCtx.ErrorType,
		"message", errCtx.Message,
		"timestamp", errCtx.Timestamp.Format(time.RFC3339),
		"duration_ms", errCtx.Duration.Milliseconds(),
	}

	if errCtx.UpstreamErr != nil {
		logFields = append(logFields, "upstream_error", errCtx.UpstreamErr.Error())
	}

	// Log based on severity
	switch {
	case errCtx.StatusCode >= 500:
		eh.logger.Error("server error occurred", logFields...)
		eh.logger.Debug("error stack trace", "stack", errCtx.Stack)
	case errCtx.StatusCode >= 400:
		eh.logger.Warn("client error occurred", logFields...)
	default:
		eh.logger.Info("request completed with error", logFields...)
	}

	// Write appropriate response format
	if isClaudeRequest(r) {
		eh.writeClaudeErrorResponse(w, errCtx)
	} else {
		eh.writeOpenAIErrorResponse(w, errCtx)
	}
}

// writeClaudeErrorResponse writes Claude-format error response
func (eh *ErrorHandler) writeClaudeErrorResponse(w http.ResponseWriter, errCtx ErrorContext) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(errCtx.StatusCode)

	resp := claude.ErrorResponse{
		Type: "error",
		Error: claude.ErrorBody{
			Type:    errCtx.ErrorType,
			Message: errCtx.Message,
		},
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		eh.logger.Error("failed to encode Claude error response",
			"error", err.Error(),
			"request_id", errCtx.RequestID,
		)
	}
}

// writeOpenAIErrorResponse writes OpenAI-format error response
func (eh *ErrorHandler) writeOpenAIErrorResponse(w http.ResponseWriter, errCtx ErrorContext) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(errCtx.StatusCode)

	resp := openai.ErrorEnvelope{
		Error: openai.ErrorBody{
			Message: errCtx.Message,
			Type:    errCtx.ErrorType,
			Code:    errCtx.ErrorCode,
		},
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		eh.logger.Error("failed to encode OpenAI error response",
			"error", err.Error(),
			"request_id", errCtx.RequestID,
		)
	}
}

// isClaudeRequest checks if the request is for Claude API
func isClaudeRequest(r *http.Request) bool {
	return r.URL.Path == "/v1/messages" || r.URL.Path == "/v1/messages/count_tokens"
}

// RecoverPanic recovers from panics and logs them properly
func (eh *ErrorHandler) RecoverPanic(r *http.Request, requestID string) {
	if rec := recover(); rec != nil {
		stack := string(debug.Stack())
		eh.logger.Error("panic recovered",
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"panic", fmt.Sprintf("%v", rec),
			"stack", stack,
		)
	}
}

// WrapHandler wraps an HTTP handler with panic recovery and error logging
func (eh *ErrorHandler) WrapHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = fmt.Sprintf("req_%d", time.Now().UnixNano())
		}

		defer func() {
			if rec := recover(); rec != nil {
				stack := string(debug.Stack())
				eh.logger.Error("handler panic recovered",
					"request_id", requestID,
					"method", r.Method,
					"path", r.URL.Path,
					"panic", fmt.Sprintf("%v", rec),
					"stack", stack,
					"duration_ms", time.Since(start).Milliseconds(),
				)

				// Write 500 error response
				errCtx := ErrorContext{
					RequestID:  requestID,
					Method:     r.Method,
					Path:       r.URL.Path,
					ClientIP:   r.RemoteAddr,
					UserAgent:  r.UserAgent(),
					StatusCode: http.StatusInternalServerError,
					ErrorCode:  "internal_error",
					ErrorType:  "server_error",
					Message:    "An internal server error occurred",
					Stack:      stack,
					Duration:   time.Since(start),
				}
				eh.writeOpenAIErrorResponse(w, errCtx)
			}
		}()

		next(w, r)
	}
}
