// Package reqlog appends per-request audit lines to a rotating log file.
package reqlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one line in the request log.
type Entry struct {
	Time      string `json:"time"`
	RequestID string `json:"request_id"`
	APIKey    string `json:"api_key,omitempty"`
	Model     string `json:"model"`
	Token     string `json:"token,omitempty"`
	Status    int    `json:"status"`
	Latency   int64  `json:"latency_ms"`
	Stream    bool   `json:"stream"`
	HasTools  bool   `json:"has_tools"`
	CacheHit  bool   `json:"cache_hit"`
	Retries   int    `json:"retries,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Logger writes entries to a rotating file.
type Logger struct {
	mu          sync.Mutex
	file        *os.File
	path        string
	maxSize     int64
	maxBackups  int
	truncateLen int

	subMu       sync.RWMutex
	subscribers map[chan Entry]bool
}

// NewLogger opens or creates the log file at path.
// If path is empty, the logger is broadcast-only (no file I/O).
func NewLogger(path string, maxSizeMB, maxBackups, truncateLen int) (*Logger, error) {
	if maxSizeMB <= 0 {
		maxSizeMB = 50
	}
	if maxBackups <= 0 {
		maxBackups = 3
	}
	if truncateLen <= 0 {
		truncateLen = 2048
	}
	l := &Logger{
		path:        path,
		maxSize:     int64(maxSizeMB) * 1024 * 1024,
		maxBackups:  maxBackups,
		truncateLen: truncateLen,
		subscribers: make(map[chan Entry]bool),
	}
	if path != "" {
		if dir := filepath.Dir(path); dir != "." && dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		l.file = f
	}
	return l, nil
}

// Log writes one entry, optionally to file, and always broadcasts to subscribers.
func (l *Logger) Log(e Entry, writeToFile bool) {
	if l == nil {
		return
	}
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if writeToFile && l.file != nil {
		l.mu.Lock()
		if err := l.rotateIfNeeded(); err == nil {
			raw, err := json.Marshal(e)
			if err == nil {
				if l.truncateLen > 0 && len(raw) > l.truncateLen {
					raw = append(raw[:l.truncateLen], '"', '}')
				}
				_, _ = l.file.Write(raw)
				_, _ = l.file.WriteString("\n")
			}
		}
		l.mu.Unlock()
	}

	go l.Broadcast(e)
}

// Subscribe registers a new subscriber channel.
func (l *Logger) Subscribe() chan Entry {
	if l == nil {
		return nil
	}
	l.subMu.Lock()
	defer l.subMu.Unlock()
	ch := make(chan Entry, 128)
	l.subscribers[ch] = true
	return ch
}

// Unsubscribe safely deletes and drains the channel.
func (l *Logger) Unsubscribe(ch chan Entry) {
	if l == nil {
		return
	}
	l.subMu.Lock()
	defer l.subMu.Unlock()
	if _, exists := l.subscribers[ch]; exists {
		delete(l.subscribers, ch)
		close(ch)
	}
}

// Broadcast dispatches the entry to all subscribers.
func (l *Logger) Broadcast(e Entry) {
	if l == nil {
		return
	}
	l.subMu.RLock()
	defer l.subMu.RUnlock()
	for ch := range l.subscribers {
		select {
		case ch <- e:
		default:
			// Slow reader, drop to avoid blocking
		}
	}
}

// Close flushes and closes the underlying file.
func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

func (l *Logger) rotateIfNeeded() error {
	st, err := l.file.Stat()
	if err != nil {
		return err
	}
	if st.Size() < l.maxSize {
		return nil
	}
	if err := l.file.Close(); err != nil {
		return err
	}
	for i := l.maxBackups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", l.path, i)
		dst := fmt.Sprintf("%s.%d", l.path, i+1)
		if _, err := os.Stat(src); err == nil {
			_ = os.Rename(src, dst)
		}
	}
	_ = os.Rename(l.path, l.path+".1")
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	l.file = f
	return nil
}

// MaskKey returns a redacted version safe to log.
func MaskKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "..." + key[len(key)-4:]
}
