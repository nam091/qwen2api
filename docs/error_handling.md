# Error Handling and Logging Documentation

This document describes the enhanced error handling and logging mechanisms added to qwen2api.

## Overview

The error handling system provides:
- **Comprehensive error tracking** with persistent storage
- **Real-time error monitoring** via dashboard
- **Structured logging** with request context
- **Automatic error categorization** (4xx vs 5xx)
- **Error statistics and analytics**

## Components

### 1. ErrorHandler (`internal/server/error_handler.go`)

Centralized error handling with structured logging.

**Features:**
- Captures stack traces automatically
- Logs errors with full request context
- Supports both OpenAI and Claude error formats
- Provides panic recovery middleware

**Usage:**
```go
errorHandler := server.NewErrorHandler(logger)

// In handlers
errCtx := ErrorContext{
    RequestID:  requestID,
    Method:     r.Method,
    Path:       r.URL.Path,
    StatusCode: http.StatusInternalServerError,
    ErrorCode:  "internal_error",
    ErrorType:  "server_error",
    Message:    "An internal server error occurred",
}
errorHandler.LogAndWriteError(w, r, errCtx)
```

### 2. ErrorTracker (`internal/server/error_tracker.go`)

Persistent error tracking with analytics capabilities.

**Features:**
- Stores errors in JSON format
- Configurable maximum error history (default: 1000)
- Query errors by path, status code, or time range
- Automatic cleanup of old errors
- Thread-safe operations

**API Endpoints:**

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/admin/errors` | GET | Get error list with filters |
| `/admin/errors/dashboard` | GET | Error tracking dashboard (HTML) |
| `/admin/errors/stats` | GET | Error statistics |
| `/admin/errors` | DELETE | Clear all errors |
| `/admin/errors/cleanup` | POST | Remove errors older than 24h |

**Query Parameters:**
- `limit`: Number of errors to return (default: 50)
- `path`: Filter by request path
- `status`: Filter by HTTP status code

### 3. ErrorLoggingMiddleware (`internal/server/error_middleware.go`)

HTTP middleware for automatic error logging.

**Features:**
- Generates unique request IDs
- Captures response status codes
- Logs request/response details
- Panic recovery with error responses

**Configuration:**
```go
// Add to router
r.Use(ErrorLoggingMiddleware(logger))
```

## Integration

### Main Application (`cmd/qwen2api/main.go`)

The error handling system is initialized in the main function:

```go
// Initialize error handling and tracking
errorHandler := server.NewErrorHandler(logger)
errorTracker, err := server.NewErrorTracker(logger, cfg.Logging.Path)
if err != nil {
    logger.Warn("error tracker init failed", "err", err)
}

// Add to dependencies
srv := server.New(server.Deps{
    // ... other deps
    ErrorHandler: errorHandler,
    ErrorTracker: errorTracker,
})
```

### Router Registration (`internal/server/server.go`)

Error routes are registered in the admin section:

```go
r.Group(func(r chi.Router) {
    r.Use(h.adminMiddleware)
    // ... other admin routes
    registerErrorRoutes(r, h)
})
```

## Error Context Structure

```go
type ErrorContext struct {
    RequestID   string        // Unique request identifier
    Method      string        // HTTP method
    Path        string        // Request path
    ClientIP    string        // Client IP address
    UserAgent   string        // User agent string
    StatusCode  int           // HTTP status code
    ErrorCode   string        // Application error code
    ErrorType   string        // Error type category
    Message     string        // Human-readable error message
    Stack       string        // Stack trace (auto-captured if empty)
    Timestamp   time.Time     // Error timestamp
    Duration    time.Duration // Request duration
    UpstreamErr error         // Upstream error (if applicable)
}
```

## Error Entry Structure

```go
type ErrorEntry struct {
    Timestamp   time.Time `json:"timestamp"`
    RequestID   string    `json:"request_id"`
    Method      string    `json:"method"`
    Path        string    `json:"path"`
    StatusCode  int       `json:"status_code"`
    ErrorCode   string    `json:"error_code"`
    ErrorType   string    `json:"error_type"`
    Message     string    `json:"message"`
    ClientIP    string    `json:"client_ip"`
    UserAgent   string    `json:"user_agent"`
    Stack       string    `json:"stack,omitempty"`
    UpstreamErr string    `json:"upstream_error,omitempty"`
    Duration    int64     `json:"duration_ms"`
}
```

## Dashboard

Access the error tracking dashboard at:
```
http://localhost:PORT/admin/errors/dashboard
```

**Features:**
- Real-time error statistics
- Filterable error list
- Auto-refresh every 30 seconds
- Manual refresh and cleanup options
- Visual distinction between 4xx and 5xx errors

## Error Tracking in Upstream Failures

The `handleUpstreamFailure` function in `chat.go` now integrates with the error tracker:

```go
func (h *handlers) handleUpstreamFailure(w http.ResponseWriter, token string, err error, action string) {
    var upstream *qwen.UpstreamError
    if errors.As(err, &upstream) {
        // ... existing logic ...

        // Track error if tracker is available
        if h.deps.ErrorTracker != nil {
            h.deps.ErrorTracker.Track(ErrorEntry{
                Timestamp:   time.Now(),
                RequestID:   fmt.Sprintf("req_%d", time.Now().UnixNano()),
                StatusCode:  http.StatusBadGateway,
                ErrorCode:   "upstream_error",
                ErrorType:   "server_error",
                Message:     fmt.Sprintf("qwen %s failed: %d", action, upstream.Status),
                UpstreamErr: truncate(upstream.Body, 256),
            })
        }

        writeError(w, http.StatusBadGateway, "upstream_error", ...)
        return
    }
    // ... similar for transport errors ...
}
```

## Best Practices

### 1. Always Include Request Context
When logging errors, include as much context as possible:
```go
logger.Error("operation failed",
    "request_id", requestID,
    "method", r.Method,
    "path", r.URL.Path,
    "error", err.Error(),
)
```

### 2. Use Structured Logging
Prefer structured fields over string concatenation:
```go
// Good
logger.Error("request failed", "status", 500, "error", err)

// Avoid
logger.Error("request failed with status 500: " + err.Error())
```

### 3. Track Upstream Errors
Always track upstream failures for debugging:
```go
if h.deps.ErrorTracker != nil {
    h.deps.ErrorTracker.Track(ErrorEntry{
        StatusCode: http.StatusBadGateway,
        ErrorCode:  "upstream_error",
        Message:    err.Error(),
    })
}
```

### 4. Clean Up Old Errors
Periodically clean up old errors to prevent storage bloat:
```go
// Run daily
go func() {
    ticker := time.NewTicker(24 * time.Hour)
    for range ticker.C {
        errorTracker.CleanupOldErrors(7 * 24 * time.Hour) // Keep 7 days
    }
}()
```

## Troubleshooting

### Error Tracker Not Working
1. Check if error tracker is initialized in `main.go`
2. Verify log directory is writable
3. Check logger output for initialization errors

### Dashboard Not Loading
1. Ensure admin middleware is configured
2. Verify `/admin/errors/dashboard` route is registered
3. Check browser console for JavaScript errors

### Missing Errors
1. Check if error tracking is enabled
2. Verify error handler is being used in handlers
3. Check log file permissions

## Performance Considerations

- Error tracking adds minimal overhead (< 1ms per error)
- JSON file storage is efficient for moderate error volumes
- Consider implementing database storage for high-traffic deployments
- Regular cleanup prevents unbounded storage growth

## Security Notes

- Error dashboard requires admin authentication
- Stack traces are logged but not exposed to clients
- Sensitive information should be sanitized before logging
- Error messages should not expose internal implementation details
