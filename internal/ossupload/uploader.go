// Package ossupload handles file uploads to Alibaba Cloud OSS via the Qwen
// STS token workflow. Files uploaded through this package can be referenced
// in conversations so the model can read their contents.
package ossupload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	oss "github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// MaxFileSize is the maximum upload size (100MB).
const MaxFileSize = 100 * 1024 * 1024

// AllowedExtensions lists permitted file types.
var AllowedExtensions = map[string]bool{
	".txt": true, ".md": true, ".py": true, ".js": true, ".ts": true,
	".go": true, ".rs": true, ".java": true, ".c": true, ".cpp": true,
	".h": true, ".hpp": true, ".cs": true, ".rb": true, ".php": true,
	".sh": true, ".bash": true, ".zsh": true, ".yml": true, ".yaml": true,
	".json": true, ".xml": true, ".html": true, ".css": true, ".sql": true,
	".r": true, ".swift": true, ".kt": true, ".scala": true, ".toml": true,
	".ini": true, ".cfg": true, ".conf": true, ".log": true, ".csv": true,
	".pdf": true, ".doc": true, ".docx": true,
	// Image types — for vision-capable models.
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".bmp": true,
}

// STSCredentials holds temporary OSS credentials.
type STSCredentials struct {
	AccessKeyID     string `json:"access_key_id"`
	AccessKeySecret string `json:"access_key_secret"`
	SecurityToken   string `json:"security_token"`
}

// FileInfo holds the upload target information.
type FileInfo struct {
	URL      string `json:"url"`
	Path     string `json:"path"`
	Bucket   string `json:"bucket"`
	Endpoint string `json:"endpoint"`
	ID       string `json:"id"`
}

// STSResponse combines credentials and file info from the STS token endpoint.
type STSResponse struct {
	Credentials STSCredentials `json:"credentials"`
	FileInfo    FileInfo       `json:"file_info"`
}

// UploadResult represents a successful file upload.
type UploadResult struct {
	FileID  string `json:"file_id"`
	FileURL string `json:"file_url"`
	Status  string `json:"status"`
}

// Uploader handles file uploads to Qwen's OSS.
type Uploader struct {
	baseURL   string
	client    *http.Client
	logger    *slog.Logger
	userAgent string
}

// NewUploader creates an Uploader.
func NewUploader(baseURL, userAgent string, logger *slog.Logger) *Uploader {
	if baseURL == "" {
		baseURL = "https://chat.qwen.ai"
	}
	return &Uploader{
		baseURL:   strings.TrimRight(baseURL, "/"),
		client:    &http.Client{Timeout: 30 * time.Second},
		logger:    logger,
		userAgent: userAgent,
	}
}

// RequestSTS obtains temporary STS credentials for uploading a file.
func (u *Uploader) RequestSTS(ctx context.Context, token, filename string, filesize int64) (*STSResponse, error) {
	ext := filepath.Ext(filename)
	simpleType := "file"
	mainType := mime.TypeByExtension(ext)
	if mainType != "" {
		parts := strings.SplitN(mainType, "/", 2)
		if len(parts) > 0 {
			switch parts[0] {
			case "image", "video", "audio":
				simpleType = parts[0]
			}
		}
	}

	body, _ := json.Marshal(map[string]any{
		"filename": filename,
		"filesize": filesize,
		"filetype": simpleType,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		u.baseURL+"/api/v1/files/getstsToken", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", u.userAgent)

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sts request: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read sts response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sts failed (%d): %s", resp.StatusCode, truncate(string(payload), 256))
	}

	var raw struct {
		AccessKeyID     string `json:"access_key_id"`
		AccessKeySecret string `json:"access_key_secret"`
		SecurityToken   string `json:"security_token"`
		FileURL         string `json:"file_url"`
		FilePath        string `json:"file_path"`
		BucketName      string `json:"bucketname"`
		Region          string `json:"region"`
		FileID          string `json:"file_id"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("decode sts response: %w", err)
	}
	if raw.AccessKeyID == "" || raw.AccessKeySecret == "" {
		return nil, errors.New("incomplete STS credentials")
	}

	return &STSResponse{
		Credentials: STSCredentials{
			AccessKeyID:     raw.AccessKeyID,
			AccessKeySecret: raw.AccessKeySecret,
			SecurityToken:   raw.SecurityToken,
		},
		FileInfo: FileInfo{
			URL:      raw.FileURL,
			Path:     raw.FilePath,
			Bucket:   raw.BucketName,
			Endpoint: raw.Region + ".aliyuncs.com",
			ID:       raw.FileID,
		},
	}, nil
}

// UploadToOSS uploads file content to Alibaba Cloud OSS using STS credentials.
// Uses the official ali-oss SDK to handle OSS V4 (HMAC-SHA256) signing, which
// is required by chat.qwen.ai's OSS bucket policy.
func (u *Uploader) UploadToOSS(ctx context.Context, sts *STSResponse, content []byte, mimeType string) error {
	endpoint := "https://" + sts.FileInfo.Endpoint
	client, err := oss.New(endpoint,
		sts.Credentials.AccessKeyID,
		sts.Credentials.AccessKeySecret,
		oss.SecurityToken(sts.Credentials.SecurityToken),
		oss.AuthVersion(oss.AuthV4),
		oss.Region(regionFromEndpoint(sts.FileInfo.Endpoint)),
	)
	if err != nil {
		return fmt.Errorf("oss client: %w", err)
	}
	bucket, err := client.Bucket(sts.FileInfo.Bucket)
	if err != nil {
		return fmt.Errorf("oss bucket: %w", err)
	}
	opts := []oss.Option{oss.ContentLength(int64(len(content)))}
	if mimeType != "" {
		opts = append(opts, oss.ContentType(mimeType))
	}
	if err := bucket.PutObject(sts.FileInfo.Path, bytes.NewReader(content), opts...); err != nil {
		return fmt.Errorf("oss upload: %w", err)
	}
	u.logger.Info("oss upload success", "file_id", sts.FileInfo.ID, "path", sts.FileInfo.Path)
	_ = ctx // SDK manages its own timeouts; keep ctx for signature compatibility.
	return nil
}

// regionFromEndpoint extracts the OSS region code from an endpoint like
// "ap-southeast-1.aliyuncs.com" or "oss-accelerate.aliyuncs.com".
func regionFromEndpoint(endpoint string) string {
	host := strings.TrimSuffix(endpoint, ".aliyuncs.com")
	host = strings.TrimPrefix(host, "oss-")
	return host
}

// Upload performs the full upload workflow: STS token → OSS upload.
func (u *Uploader) Upload(ctx context.Context, token, filename string, content []byte) (*UploadResult, error) {
	if int64(len(content)) > MaxFileSize {
		return nil, fmt.Errorf("file too large: %d bytes (max %d)", len(content), MaxFileSize)
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !AllowedExtensions[ext] {
		return nil, fmt.Errorf("file type not allowed: %s", ext)
	}

	sts, err := u.RequestSTS(ctx, token, filename, int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("request sts: %w", err)
	}

	mimeType := mime.TypeByExtension(ext)
	if err := u.UploadToOSS(ctx, sts, content, mimeType); err != nil {
		return nil, fmt.Errorf("upload to oss: %w", err)
	}

	return &UploadResult{
		FileID:  sts.FileInfo.ID,
		FileURL: sts.FileInfo.URL,
		Status:  "uploaded",
	}, nil
}

// ValidateExtension checks if a file extension is allowed.
func ValidateExtension(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return AllowedExtensions[ext]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
