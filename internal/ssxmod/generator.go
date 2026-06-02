// Package ssxmod generates ssxmod_itna and ssxmod_itna2 browser fingerprint
// cookies used by chat.qwen.ai for anti-bot detection. The cookies are
// automatically refreshed at a configurable interval.
package ssxmod

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"
)

// DefaultRefreshInterval is how often cookies are regenerated.
const DefaultRefreshInterval = 15 * time.Minute

// customBase64 is the non-standard alphabet used by the Qwen fingerprint.
const customBase64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// Cookies holds the generated ssxmod values.
type Cookies struct {
	SsxmodItna  string
	SsxmodItna2 string
	Timestamp   int64
}

// Manager generates and periodically refreshes SSXMOD cookies.
type Manager struct {
	mu      sync.RWMutex
	current Cookies
	logger  *slog.Logger
	stopCh  chan struct{}
}

// NewManager creates a Manager and generates initial cookies.
func NewManager(logger *slog.Logger) *Manager {
	m := &Manager{
		logger: logger,
		stopCh: make(chan struct{}),
	}
	m.refresh()
	return m
}

// Start begins periodic cookie refresh.
func (m *Manager) Start(interval time.Duration) {
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.logger.Error("ssxmod refresh loop panic recovered", "panic", r)
			}
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.refresh()
			case <-m.stopCh:
				return
			}
		}
	}()
}

// Stop halts the refresh loop.
func (m *Manager) Stop() {
	select {
	case m.stopCh <- struct{}{}:
	default:
	}
}

// Get returns the current cookies.
func (m *Manager) Get() Cookies {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

func (m *Manager) refresh() {
	cookies := generate()
	m.mu.Lock()
	m.current = cookies
	m.mu.Unlock()
	m.logger.Debug("ssxmod cookies refreshed")
}

// generate creates new ssxmod cookie values using a fingerprint-like algorithm.
func generate() Cookies {
	now := time.Now()
	ts := now.UnixMilli()

	// Generate ssxmod_itna: LZW-compressed fingerprint data
	raw1 := generateFingerprintData(ts)
	itna := lzwCompress(raw1)

	// Generate ssxmod_itna2: secondary fingerprint with different seed
	raw2 := generateFingerprintData(ts + 1)
	itna2 := lzwCompress(raw2)

	return Cookies{
		SsxmodItna:  itna,
		SsxmodItna2: itna2,
		Timestamp:   ts,
	}
}

// generateFingerprintData creates pseudo-random fingerprint payload.
func generateFingerprintData(seed int64) string {
	// Simulate browser environment data points
	var sb strings.Builder

	// Screen dimensions
	screens := []string{"1920x1080", "2560x1440", "1366x768", "1536x864", "1440x900"}
	sb.WriteString(pickRandom(screens))
	sb.WriteByte('|')

	// Color depth
	sb.WriteString("24")
	sb.WriteByte('|')

	// Timezone offset
	offsets := []string{"-480", "-420", "-360", "-300", "-240", "0", "60", "120", "480", "540"}
	sb.WriteString(pickRandom(offsets))
	sb.WriteByte('|')

	// Platform
	platforms := []string{"Win32", "Win64", "Linux x86_64", "MacIntel"}
	sb.WriteString(pickRandom(platforms))
	sb.WriteByte('|')

	// Language
	langs := []string{"en-US", "zh-CN", "en-GB", "ja-JP", "ko-KR"}
	sb.WriteString(pickRandom(langs))
	sb.WriteByte('|')

	// Canvas fingerprint hash (32 hex chars)
	sb.WriteString(randomHex(32))
	sb.WriteByte('|')

	// WebGL renderer hash
	sb.WriteString(randomHex(16))
	sb.WriteByte('|')

	// Timestamp
	sb.WriteString(fmt.Sprintf("%d", seed))
	sb.WriteByte('|')

	// Random session component
	sb.WriteString(randomHex(8))

	return sb.String()
}

// lzwCompress implements LZW compression and encodes the result as
// custom base64, matching the Qwen fingerprint format.
func lzwCompress(input string) string {
	if input == "" {
		return ""
	}

	// Initialize dictionary with single chars
	dict := make(map[string]int)
	for i := 0; i < 256; i++ {
		dict[string(rune(i))] = i
	}

	nextCode := 256
	var result []int
	w := ""

	for _, c := range input {
		wc := w + string(c)
		if _, ok := dict[wc]; ok {
			w = wc
		} else {
			result = append(result, dict[w])
			dict[wc] = nextCode
			nextCode++
			w = string(c)
		}
	}
	if w != "" {
		result = append(result, dict[w])
	}

	// Encode as bytes then base64
	var buf []byte
	for _, code := range result {
		buf = append(buf, byte(code>>8), byte(code&0xFF))
	}
	return base64.RawStdEncoding.EncodeToString(buf)
}

func pickRandom(choices []string) string {
	if len(choices) == 0 {
		return ""
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(choices))))
	if err != nil {
		return choices[0]
	}
	return choices[n.Int64()]
}

func randomHex(length int) string {
	b := make([]byte, (length+1)/2)
	_, _ = rand.Read(b)
	hex := fmt.Sprintf("%x", b)
	if len(hex) > length {
		hex = hex[:length]
	}
	return hex
}
