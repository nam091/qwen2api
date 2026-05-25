package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/keaume34/qwen2api/internal/ossupload"
)

const token = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpZCI6IjY3OGM5NzQ2LTUyZjQtNGQ4YS1hNTJiLWI5YmRhN2QwOGEzMyIsImxhc3RfcGFzc3dvcmRfY2hhbmdlIjoxNzc4NzcyMjM5LCJleHAiOjE3ODEzNjQyNDF9.0XQvB0n-DTgczi3UJklORODHEJXzKp2etJHt9cKqtX4"
const UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

func main() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	content, err := os.ReadFile("/home/ubuntu/attachments/c5d7c553-61d2-4899-bf6a-e3ac128fd51d/image.png")
	if err != nil {
		panic(err)
	}
	fmt.Println("image bytes:", len(content))

	up := ossupload.NewUploader("", UA, logger)

	// Try uploading despite .png not being in AllowedExtensions; we'll call internal methods.
	sts, err := up.RequestSTS(ctx, token, "image.png", int64(len(content)))
	if err != nil {
		fmt.Println("RequestSTS error:", err)
		return
	}
	fmt.Println("STS file_url:", sts.FileInfo.URL, "file_id:", sts.FileInfo.ID)
	if err := up.UploadToOSS(ctx, sts, content, "image/png"); err != nil {
		fmt.Println("UploadToOSS error:", err)
		return
	}
	fmt.Println("Upload OK")

	clientHTTP := &http.Client{Timeout: 60 * time.Second}

	// /chats/new
	newReq, _ := http.NewRequestWithContext(ctx, "POST", "https://chat.qwen.ai/api/v2/chats/new",
		bytes.NewReader([]byte(`{"title":"img","models":["qwen3-max"],"chat_mode":"normal","chat_type":"t2t","timestamp":1000}`)))
	newReq.Header.Set("Content-Type", "application/json")
	newReq.Header.Set("Authorization", "Bearer "+token)
	newReq.Header.Set("User-Agent", UA)
	newReq.Header.Set("Source", "web")
	nr, err := clientHTTP.Do(newReq)
	if err != nil {
		panic(err)
	}
	body, _ := io.ReadAll(nr.Body)
	nr.Body.Close()
	fmt.Println("chats/new", nr.StatusCode, truncate(string(body), 200))
	var nresp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(body, &nresp)
	chatID := nresp.Data.ID

	// chat.qwen.ai web client format: content is an array of multimodal parts.
	// For images: {type:'image', image: <https URL on Qwen OSS>}
	contentArr := []map[string]any{
		{"type": "text", "text": "Đây là logo của ứng dụng/extension nào? Mô tả ngắn gọn tiếng Việt 1 câu."},
		{"type": "image", "image": sts.FileInfo.URL},
	}
	for _, model := range []string{"qwen3-max", "qwen3-vl-plus"} {
		// New chat for each model to avoid reuse failures.
		nr2, _ := http.NewRequestWithContext(ctx, "POST", "https://chat.qwen.ai/api/v2/chats/new",
			bytes.NewReader([]byte(fmt.Sprintf(`{"title":"img-%s","models":["%s"],"chat_mode":"normal","chat_type":"t2t","timestamp":1000}`, model, model))))
		nr2.Header.Set("Content-Type", "application/json")
		nr2.Header.Set("Authorization", "Bearer "+token)
		nr2.Header.Set("User-Agent", UA)
		nr2.Header.Set("Source", "web")
		ncR, err := clientHTTP.Do(nr2)
		if err != nil {
			fmt.Println("chats/new err:", err)
			continue
		}
		nb, _ := io.ReadAll(ncR.Body)
		ncR.Body.Close()
		var nrr struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		json.Unmarshal(nb, &nrr)
		thisChat := nrr.Data.ID
		_ = chatID

		fmt.Println("\n=== model:", model, "chat:", thisChat, "===")
		payload := map[string]any{
			"stream":             true,
			"incremental_output": true,
			"chat_id":            thisChat,
			"chat_type":          "t2t",
			"sub_chat_type":      "t2t",
			"chat_mode":          "normal",
			"model":              model,
			"session_id":         "sess-" + model,
			"id":                 "msg-" + model,
			"messages": []map[string]any{{
				"role":           "user",
				"content":        contentArr,
				"chat_type":      "t2t",
				"extra":          map[string]any{},
				"feature_config": map[string]any{"output_schema": "phase", "thinking_enabled": false},
			}},
		}
		pb, _ := json.Marshal(payload)
		cr, _ := http.NewRequestWithContext(ctx, "POST", "https://chat.qwen.ai/api/v2/chat/completions?chat_id="+thisChat, bytes.NewReader(pb))
		cr.Header.Set("Content-Type", "application/json")
		cr.Header.Set("Authorization", "Bearer "+token)
		cr.Header.Set("User-Agent", UA)
		cr.Header.Set("Origin", "https://chat.qwen.ai")
		cr.Header.Set("Referer", "https://chat.qwen.ai/c/guest")
		cr.Header.Set("source", "web")
		cr.Header.Set("Version", "0.2.50")
		cr.Header.Set("bx-v", "2.5.36")
		cr.Header.Set("Sec-Fetch-Site", "same-origin")
		cr.Header.Set("Sec-Fetch-Mode", "cors")
		cr.Header.Set("Sec-Fetch-Dest", "empty")
		cr.Header.Set("Accept", "*/*")
		cr.Header.Set("Accept-Language", "en-US,en;q=0.9")
		cresp, err := clientHTTP.Do(cr)
		if err != nil {
			fmt.Println("err:", err)
			continue
		}
		raw, _ := io.ReadAll(cresp.Body)
		cresp.Body.Close()
		// Parse SSE and extract just the assistant content.
		text := ""
		for _, line := range bytesSplitLines(raw) {
			if !bytes.HasPrefix(line, []byte("data: ")) {
				continue
			}
			body := bytes.TrimPrefix(line, []byte("data: "))
			if bytes.Equal(body, []byte("[DONE]")) {
				break
			}
			var obj struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(body, &obj); err == nil {
				for _, ch := range obj.Choices {
					text += ch.Delta.Content
				}
			}
		}
		fmt.Println("status:", cresp.StatusCode, "raw_len:", len(raw))
		fmt.Println("ANSWER:", text)
		_ = bufio.NewScanner
	}
}

func jsonStr(v any) string { b, _ := json.Marshal(v); return string(b) }

func bytesSplitLines(b []byte) [][]byte { return bytes.Split(b, []byte("\n")) }
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
