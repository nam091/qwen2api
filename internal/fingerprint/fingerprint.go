// Package fingerprint generates browser-like fingerprint data for HTTP
// requests to chat.qwen.ai. This helps bypass simple bot detection by
// making requests appear to come from a real browser.
package fingerprint

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

// Profile represents a complete browser fingerprint profile.
type Profile struct {
	UserAgent      string
	Platform       string
	Language       string
	ScreenRes      string
	ColorDepth     int
	TimezoneOffset int
	CanvasHash     string
	WebGLHash      string
	AudioHash      string
	FontHash       string
	PluginHash     string
	CreatedAt      time.Time
}

var (
	userAgents = []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:138.0) Gecko/20100101 Firefox/138.0",
	}
	platforms = []string{"Win32", "Win64", "MacIntel", "Linux x86_64"}
	languages = []string{"en-US,en;q=0.9", "zh-CN,zh;q=0.9,en;q=0.8", "en-GB,en;q=0.9", "ja-JP,ja;q=0.9"}
	screens   = []string{"1920x1080", "2560x1440", "1366x768", "1536x864", "3840x2160"}
)

// Generator creates and caches browser fingerprint profiles.
type Generator struct {
	mu      sync.RWMutex
	current *Profile
}

// NewGenerator creates a fingerprint generator with an initial profile.
func NewGenerator() *Generator {
	g := &Generator{}
	g.current = g.generate()
	return g
}

// Get returns the current fingerprint profile.
func (g *Generator) Get() *Profile {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.current
}

// Rotate generates a new fingerprint profile.
func (g *Generator) Rotate() *Profile {
	p := g.generate()
	g.mu.Lock()
	g.current = p
	g.mu.Unlock()
	return p
}

func (g *Generator) generate() *Profile {
	seed := fmt.Sprintf("%d-%s", time.Now().UnixNano(), randomBytes(16))
	return &Profile{
		UserAgent:      pick(userAgents),
		Platform:       pick(platforms),
		Language:       pick(languages),
		ScreenRes:      pick(screens),
		ColorDepth:     24,
		TimezoneOffset: pickTimezone(),
		CanvasHash:     hashStr(seed + ":canvas"),
		WebGLHash:      hashStr(seed + ":webgl"),
		AudioHash:      hashStr(seed + ":audio"),
		FontHash:       hashStr(seed + ":fonts"),
		PluginHash:     hashStr(seed + ":plugins"),
		CreatedAt:      time.Now(),
	}
}

// ApplyHeaders sets fingerprint-derived headers on an HTTP request.
func (p *Profile) ApplyHeaders(headers map[string]string) {
	headers["User-Agent"] = p.UserAgent
	headers["Accept-Language"] = p.Language
	headers["Sec-Ch-Ua-Platform"] = fmt.Sprintf(`"%s"`, platformToChUA(p.Platform))
	headers["Sec-Ch-Ua-Mobile"] = "?0"
}

func platformToChUA(platform string) string {
	switch {
	case strings.Contains(platform, "Win"):
		return "Windows"
	case strings.Contains(platform, "Mac"):
		return "macOS"
	case strings.Contains(platform, "Linux"):
		return "Linux"
	default:
		return "Unknown"
	}
}

func pick(choices []string) string {
	if len(choices) == 0 {
		return ""
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(choices))))
	if err != nil {
		return choices[0]
	}
	return choices[n.Int64()]
}

func pickTimezone() int {
	offsets := []int{-480, -420, -360, -300, -240, 0, 60, 120, 480, 540}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(offsets))))
	if err != nil {
		return 0
	}
	return offsets[n.Int64()]
}

func hashStr(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}

func randomBytes(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
