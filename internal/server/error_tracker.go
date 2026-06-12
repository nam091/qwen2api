package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrorEntry represents a single error occurrence
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

// ErrorTracker tracks and persists errors for debugging
type ErrorTracker struct {
	logger    *slog.Logger
	filePath  string
	mu        sync.Mutex
	errors    []ErrorEntry
	maxErrors int
}

// NewErrorTracker creates a new error tracker
func NewErrorTracker(logger *slog.Logger, logDir string) (*ErrorTracker, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	tracker := &ErrorTracker{
		logger:    logger,
		filePath:  filepath.Join(logDir, "errors.json"),
		maxErrors: 1000, // Keep last 1000 errors
	}

	// Load existing errors if file exists
	if err := tracker.load(); err != nil {
		logger.Warn("failed to load existing errors, starting fresh", "error", err.Error())
	}

	return tracker, nil
}

// Track records an error entry
func (et *ErrorTracker) Track(entry ErrorEntry) {
	et.mu.Lock()
	defer et.mu.Unlock()

	// Add to in-memory list
	et.errors = append(et.errors, entry)

	// Trim if exceeds max
	if len(et.errors) > et.maxErrors {
		et.errors = et.errors[len(et.errors)-et.maxErrors:]
	}

	// Log the error
	et.logger.Error("error tracked",
		"request_id", entry.RequestID,
		"method", entry.Method,
		"path", entry.Path,
		"status_code", entry.StatusCode,
		"error_code", entry.ErrorCode,
		"error_type", entry.ErrorType,
		"message", entry.Message,
		"client_ip", entry.ClientIP,
		"duration_ms", entry.Duration,
	)

	// Persist to file asynchronously (snapshot data under lock, write outside)
	go et.persistAsync()
}

// GetRecentErrors returns the most recent errors
func (et *ErrorTracker) GetRecentErrors(limit int) []ErrorEntry {
	et.mu.Lock()
	defer et.mu.Unlock()

	if limit <= 0 || limit > len(et.errors) {
		limit = len(et.errors)
	}

	start := len(et.errors) - limit
	if start < 0 {
		start = 0
	}

	result := make([]ErrorEntry, limit)
	copy(result, et.errors[start:])
	return result
}

// GetErrorsByPath returns errors for a specific path
func (et *ErrorTracker) GetErrorsByPath(path string, limit int) []ErrorEntry {
	et.mu.Lock()
	defer et.mu.Unlock()

	var filtered []ErrorEntry
	for i := len(et.errors) - 1; i >= 0 && len(filtered) < limit; i-- {
		if et.errors[i].Path == path {
			filtered = append(filtered, et.errors[i])
		}
	}
	return filtered
}

// GetErrorsByStatusCode returns errors with a specific status code
func (et *ErrorTracker) GetErrorsByStatusCode(statusCode int, limit int) []ErrorEntry {
	et.mu.Lock()
	defer et.mu.Unlock()

	var filtered []ErrorEntry
	for i := len(et.errors) - 1; i >= 0 && len(filtered) < limit; i-- {
		if et.errors[i].StatusCode == statusCode {
			filtered = append(filtered, et.errors[i])
		}
	}
	return filtered
}

// GetErrorStats returns error statistics
func (et *ErrorTracker) GetErrorStats() map[string]interface{} {
	et.mu.Lock()
	defer et.mu.Unlock()

	stats := map[string]interface{}{
		"total_errors": len(et.errors),
		"by_status":    make(map[int]int),
		"by_path":      make(map[string]int),
		"by_error_code": make(map[string]int),
	}

	byStatus := stats["by_status"].(map[int]int)
	byPath := stats["by_path"].(map[string]int)
	byErrorCode := stats["by_error_code"].(map[string]int)

	for _, entry := range et.errors {
		byStatus[entry.StatusCode]++
		byPath[entry.Path]++
		byErrorCode[entry.ErrorCode]++
	}

	return stats
}

// Clear removes all tracked errors
func (et *ErrorTracker) Clear() {
	et.mu.Lock()
	et.errors = nil
	et.persistLocked()
	et.mu.Unlock()
}

// load reads errors from file
func (et *ErrorTracker) load() error {
	data, err := os.ReadFile(et.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	return json.Unmarshal(data, &et.errors)
}

// persistAsync snapshots the error list under lock then writes to disk without holding the lock.
func (et *ErrorTracker) persistAsync() {
	et.mu.Lock()
	snapshot := make([]ErrorEntry, len(et.errors))
	copy(snapshot, et.errors)
	et.mu.Unlock()

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		et.logger.Error("failed to marshal errors", "error", err.Error())
		return
	}

	if err := os.WriteFile(et.filePath, data, 0644); err != nil {
		et.logger.Error("failed to write errors file", "error", err.Error())
	}
}

// persistLocked writes errors to file. Caller MUST hold et.mu.
func (et *ErrorTracker) persistLocked() {
	data, err := json.MarshalIndent(et.errors, "", "  ")
	if err != nil {
		et.logger.Error("failed to marshal errors", "error", err.Error())
		return
	}

	if err := os.WriteFile(et.filePath, data, 0644); err != nil {
		et.logger.Error("failed to write errors file", "error", err.Error())
	}
}

// CleanupOldErrors removes errors older than the specified duration
func (et *ErrorTracker) CleanupOldErrors(maxAge time.Duration) {
	et.mu.Lock()
	defer et.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	var cleaned []ErrorEntry

	for _, entry := range et.errors {
		if entry.Timestamp.After(cutoff) {
			cleaned = append(cleaned, entry)
		}
	}

	removed := len(et.errors) - len(cleaned)
	et.errors = cleaned

	if removed > 0 {
		et.logger.Info("cleaned up old errors", "removed", removed, "remaining", len(cleaned))
		et.persistLocked()
	}
}
