package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/keaume34/qwen2api/internal/openai"
	"github.com/keaume34/qwen2api/internal/ossupload"
)

// imageUploadCache memoizes (token, image-content-hash) -> OSS file URL so the
// same image isn't re-uploaded across requests. Qwen's signed OSS URLs are
// valid for ~24h after upload, so we cap entries to 12h to stay safely fresh.
type imageUploadCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]imageUploadEntry
}

type imageUploadEntry struct {
	url     string
	expires time.Time
}

func newImageUploadCache() *imageUploadCache {
	return &imageUploadCache{
		ttl:     12 * time.Hour,
		entries: make(map[string]imageUploadEntry, 64),
	}
}

func (c *imageUploadCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return "", false
	}
	if time.Now().After(e.expires) {
		delete(c.entries, key)
		return "", false
	}
	return e.url, true
}

func (c *imageUploadCache) put(key, url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = imageUploadEntry{url: url, expires: time.Now().Add(c.ttl)}
}

// resolveImageURL converts an OpenAI-style image URL into a Qwen-OSS HTTPS URL
// that the vision model can read. Data URIs are decoded and uploaded; existing
// HTTPS URLs (e.g. already on Qwen OSS or a public image host) are returned
// as-is.
func (h *handlers) resolveImageURL(ctx context.Context, token, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty image url")
	}
	if !strings.HasPrefix(raw, "data:") {
		return raw, nil
	}
	mimeType, body, err := decodeDataURI(raw)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	// Cache by content hash only: OSS URLs are signed but accessible to any
	// vision request regardless of which qwen account uploaded the file.
	cacheKey := hash

	if h.imageCache != nil {
		if url, ok := h.imageCache.get(cacheKey); ok {
			return url, nil
		}
	}

	uploader := h.imageUploader
	if uploader == nil {
		return "", fmt.Errorf("image uploader not configured")
	}

	filename := filenameForMime(mimeType, hash)
	res, err := uploader.Upload(ctx, token, filename, body)
	if err != nil {
		return "", fmt.Errorf("upload image to oss: %w", err)
	}
	if h.imageCache != nil {
		h.imageCache.put(cacheKey, res.FileURL)
	}
	return res.FileURL, nil
}

// resolveImageURLs maps a list of OpenAI-style image URLs to Qwen-OSS HTTPS
// URLs. Returns the first error encountered; other images are dropped.
func (h *handlers) resolveImageURLs(ctx context.Context, token string, urls []string) ([]string, error) {
	if len(urls) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		resolved, err := h.resolveImageURL(ctx, token, u)
		if err != nil {
			return out, err
		}
		out = append(out, resolved)
	}
	return out, nil
}

// uploadDataURIsInPlace walks the parsed content of every message and replaces
// any `data:` image URI with the OSS HTTPS URL produced by an upload. The
// rewritten parts are re-marshalled back into the message's Content RawMessage
// so downstream code that calls Images()/Parts()/Text() observes the upgraded
// URLs.
func (h *handlers) uploadDataURIsInPlace(ctx context.Context, token string, msgs []openai.ChatMessage) error {
	for i := range msgs {
		parts := msgs[i].Parts()
		if len(parts) == 0 {
			continue
		}
		modified := false
		for j := range parts {
			url := parts[j].ImageRef()
			if url == "" {
				continue
			}
			if !strings.HasPrefix(url, "data:") {
				continue
			}
			resolved, err := h.resolveImageURL(ctx, token, url)
			if err != nil {
				return err
			}
			// Normalise to a single string-form image_url so downstream
			// emitters pick the resolved URL regardless of which input
			// form was used.
			parts[j].Type = "image_url"
			parts[j].Image = ""
			parts[j].InputImage = openai.ContentImageRef{}
			parts[j].ImageURL = openai.ContentImageRef{URL: resolved}
			modified = true
		}
		if !modified {
			continue
		}
		out, err := json.Marshal(parts)
		if err != nil {
			return err
		}
		msgs[i].Content = out
	}
	return nil
}

// decodeDataURI parses a `data:<mime>;base64,<payload>` URI.
func decodeDataURI(uri string) (mimeType string, body []byte, err error) {
	if !strings.HasPrefix(uri, "data:") {
		return "", nil, fmt.Errorf("not a data URI")
	}
	rest := strings.TrimPrefix(uri, "data:")
	idx := strings.Index(rest, ",")
	if idx < 0 {
		return "", nil, fmt.Errorf("malformed data URI: missing comma")
	}
	header := rest[:idx]
	payload := rest[idx+1:]

	isB64 := false
	parts := strings.Split(header, ";")
	if len(parts) > 0 {
		mimeType = parts[0]
	}
	for _, p := range parts[1:] {
		if strings.EqualFold(p, "base64") {
			isB64 = true
		}
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if !isB64 {
		return mimeType, []byte(payload), nil
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		// Try URL-safe variant.
		decoded, err = base64.URLEncoding.DecodeString(payload)
		if err != nil {
			return "", nil, fmt.Errorf("decode base64: %w", err)
		}
	}
	return mimeType, decoded, nil
}

// filenameForMime picks a stable filename from the image content hash + MIME type.
func filenameForMime(mimeType, hash string) string {
	ext := ".png"
	switch strings.ToLower(mimeType) {
	case "image/jpeg", "image/jpg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	case "image/bmp":
		ext = ".bmp"
	case "image/png":
		ext = ".png"
	}
	short := hash
	if len(short) > 12 {
		short = short[:12]
	}
	return short + ext
}

// downloadImage fetches an external HTTPS image into memory. Used when we want
// to upload a non-data-URI image to Qwen OSS for guaranteed vision access.
func (h *handlers) downloadImage(ctx context.Context, url string) (mimeType string, body []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, ossupload.MaxFileSize))
	if err != nil {
		return "", nil, err
	}
	return resp.Header.Get("Content-Type"), body, nil
}
