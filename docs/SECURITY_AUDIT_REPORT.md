# 🔒 BÁO CÁO KIỂM TOÁN BẢO MẬT - qwen2api

**Ngày:** 2026-06-09
**Branch kiểm tra:** v6 (HEAD), v3, v4, v5, main, devin/*
**Phạm vi:** Toàn bộ source code, git history, cấu hình, và binary committed

---

## 📊 TỔNG QUAN

| Mức độ | Số lượng | Mô tả |
|--------|---------|-------|
| 🔴 CRITICAL | 3 | Lỗ hổng nghiêm trọng cần fix ngay |
| 🟠 HIGH | 9 | Lỗ hổng cao, có thể bị khai thác trực tiếp |
| 🟡 MEDIUM | 10 | Lỗ hổng trung bình, cần khắc phục |
| 🟢 LOW | 4 | Vấn đề nhỏ, cải thiện code quality |
| **Tổng** | **26** | |

---

## 🔴 CRITICAL (Fix ngay lập tức)

### C1. Auth Bypass - API Key không được kiểm tra khi APIKeyRotation=false
- **File:** `internal/server/handlers.go:38`
- **Code:**
  ```go
  if !h.deps.Config.Features.APIKeyRotation || len(h.deps.Config.APIKeys) == 0 {
      next.ServeHTTP(w, r) // BYPASS AUTH!
      return
  }
  ```
- **Vấn đề:** Khi `APIKeyRotation` = `false` (mặc định), middleware bỏ qua hoàn toàn việc kiểm tra API key. Mọi endpoint đều truy cập được mà không cần xác thực.
- **Fix:**
  ```go
  if len(h.deps.Config.APIKeys) == 0 {
      next.ServeHTTP(w, r)
      return
  }
  ```

### C2. Hardcoded JWT Token trong source code committed
- **File:** `cmd/imgtest/main.go:18`
- **Code:**
  ```go
  const token = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpZCI6IjY3OGM5NzQ2LTUyZjQtNGQ4YS1hNTJiLWI5YmRhN2QwOGEzMy..."
  ```
- **Vấn đề:** JWT token thật của tài khoản Qwen được hardcode và committed vào git history. Token chứa user ID `678c9746-52f4-4d8a-a52b-95bda7d08a33`. File cũng expose server path `/home/ubuntu/attachments/...`
- **Fix:**
  1. Rotate token ngay lập tức trên Qwen
  2. Xóa file khỏi git history bằng BFG Repo-Cleaner
  3. Sử dụng environment variable thay vì hardcode

### C3. Admin Auth Bypass đã xảy ra trong history
- **Commit:** `8c2efde` - "feat: bypass admin authentication requirement completely"
- **Vấn đề:** Commit này đã thay `AuthorizedAdmin` thành `return true`, bypass toàn bộ admin auth. Được fix sau 15 ngày ở commit `94c2bfd`, nhưng mọi deployment trong giai đoạn đó đều bị lộ.
- **Fix:** Đã được fix. Cần audit lại xem có deployment nào vẫn chạy version cũ không.

---

## 🟠 HIGH

### H1. Dashboard/Data endpoint không yêu cầu authentication
- **File:** `internal/server/server.go:81-82`
- **Vấn đề:** `/dashboard` và `/dashboard/data` nằm ngoài auth middleware group. Endpoint trả về token pool status, API key names, feature flags, model aliases mà không cần auth.
- **Fix:** Chuyển vào admin middleware group hoặc thêm auth check riêng.

### H2. CORS Wildcard - cho phép mọi origin
- **File:** `internal/server/server.go:159`
- **Code:**
  ```go
  w.Header().Set("Access-Control-Allow-Origin", "*")
  w.Header().Set("Access-Control-Allow-Headers", "Authorization, ...")
  ```
- **Vấn đề:** Mọi website đều có thể gửi authenticated cross-origin requests. Kết hợp với API key qua query parameter, attacker có thể đánh cắp key.
- **Fix:** Thay `*` bằng configurable origin whitelist.

### H3. API Key chấp nhận qua URL query parameter
- **File:** `internal/server/handlers.go:52`
- **Code:**
  ```go
  if q := r.URL.Query().Get("api_key"); q != "" {
      return strings.TrimSpace(q)
  }
  ```
- **Vấn đề:** Query parameter bị log trong server logs, browser history, HTTP Referer headers. API key dễ bị leak.
- **Fix:** Chỉ chấp nhận qua `Authorization: Bearer` header hoặc `X-Api-Key` header.

### H4. Config endpoint trả về toàn bộ secrets
- **File:** `internal/server/config_api.go:29-32` + `internal/server/server.go:126`
- **Vấn đề:** `GET /admin/config` serialize toàn bộ Config struct bao gồm AdminToken, SsxmodItna tokens, và tất cả upstream token values.
- **Fix:** Sanitize response, redact AdminToken và token values.

### H5. SSRF qua fetchImageBase64 - không validate URL
- **File:** `internal/server/images_handler.go:212-213`
- **Code:**
  ```go
  resp, err := http.Get(url) // url từ upstream response, không validate
  ```
- **Vấn đề:** Attacker có thể manipulate upstream response để server gọi internal addresses (169.254.169.254 cloud metadata, localhost services).
- **Fix:** Validate URL scheme (https only), block private/link-local/loopback IPs.

### H6. SSRF qua downloadImage - không validate URL
- **File:** `internal/server/imageupload.go:232-251`
- **Vấn đề:** Tương tự H5, `downloadImage` gọi HTTP GET tới arbitrary URLs không có IP validation.
- **Fix:** Giống H5.

### H7. Không có Rate Limiting
- **File:** `internal/server/server.go` ( toàn bộ router)
- **Vấn đề:** Không có rate limiting middleware nào. Attacker có thể brute-force API keys, exhaust upstream token pools, hoặc DoS. Retry mechanism (10 attempts) làm vấn đề tệ hơn.
- **Fix:** Implement per-IP và per-API-key rate limiting.

### H8. SQLite Database committed với user data thật
- **File:** `data/qwen2api.db` (53KB, tracked by git)
- **Vấn đề:** Chứa 20 conversation records với tên user thật ("Nam"), 350 messages, metadata. Không có trong .gitignore.
- **Fix:** Thêm `data/` và `*.db` vào .gitignore, xóa khỏi git history.

### H9. Log file committed với token fragments
- **File:** `qwen2api.log` (546 lines, tracked by git)
- **Vấn đề:** Chứa partial JWT tokens (`eyJh...Vxtc`, `eyJh...qtX4`) và request metadata. Không có trong .gitignore.
- **Fix:** Thêm `qwen2api.log` vào .gitignore, rotate tất cả tokens đã xuất hiện trong log.

---

## 🟡 MEDIUM

### M1. Timing Attack trên Admin Token comparison
- **File:** `internal/config/config.go:362`
- **Code:**
  ```go
  return token != "" && token == c.AdminToken // vulnerable to timing attack
  ```
- **Fix:** Sử dụng `crypto/subtle.ConstantTimeCompare`.

### M2. Timing Attack trên API Key comparison
- **File:** `internal/config/config.go:345`
- **Code:**
  ```go
  if k.Value == "" || k.Value != key { // vulnerable to timing attack
  ```
- **Fix:** Sử dụng `crypto/subtle.ConstantTimeCompare`.

### M3. Arbitrary File Write qua debug environment variable
- **File:** `internal/server/responses_handler.go:36-38`
- **Vấn đề:** Nếu `QWEN2API_DEBUG_RESPONSES_DUMP` được set, handler viết raw request body tới path chỉ định. Có thể bị exploit nếu attacker control được env vars.
- **Fix:** Remove debug code path hoặc validate dump path.

### M4. Error messages lộ internal details
- **File:** `internal/server/chat.go:536`, `internal/server/claude_handler.go:227`
- **Vấn đề:** Raw upstream errors được trả về client, bao gồm internal network details, DNS failures, TLS info.
- **Fix:** Return generic errors, log chi tiết server-side only.

### M5. No Request Body Size Limit
- **File:** `internal/server/chat.go:36`, `internal/server/responses_handler.go:31`
- **Vấn đề:** `io.ReadAll(r.Body)` đọc toàn bộ body vào memory. Attacker có thể gửi payload cực lớn để exhaust memory.
- **Fix:** Wrap với `http.MaxBytesReader` (10MB limit).

### M6. Config file written với permissions 0644
- **File:** `internal/server/config_api.go:17-27`
- **Vấn đề:** Config chứa AdminToken, upstream tokens, API keys được write với 0644 (world-readable).
- **Fix:** Set permissions 0600.

### M7. CLI config files written với permissions 0644
- **File:** `internal/cliconfig/cliconfig.go` (multiple locations)
- **Vấn đề:** Files chứa API keys (`~/.claude/settings.json`, etc.) được write với 0644.
- **Fix:** Set permissions 0600.

### M8. Session Store Path Traversal potential
- **File:** `internal/session/store.go:192`
- **Vấn đề:** `sess.ID` (derived từ user content) dùng làm filename. Dù SHA-256 hash thường an toàn, cần validate.
- **Fix:** Validate session ID chỉ chứa alphanumeric characters.

### M9. No TLS - Plaintext HTTP
- **File:** `cmd/qwen2api/main.go:162`
- **Vấn đề:** Server dùng `httpServer.ListenAndServe()` (plain HTTP). Mọi data transmit cleartext.
- **Fix:** Thêm TLS config hoặc document yêu cầu reverse proxy.

### M10. node_modules/ committed (301 files)
- **File:** `node_modules/chrome-devtools-mcp/` (tracked by git)
- **Vấn đề:** Supply chain risk từ unvetted npm dependencies. Không có trong .gitignore (root level).
- **Fix:** Thêm `node_modules/` vào .gitignore, remove khỏi git.

---

## 🟢 LOW

### L1. WriteTimeout = 0 (Slowloris risk)
- **File:** `cmd/qwen2api/main.go:144`
- **Fix:** Implement connection limits và per-connection write deadlines.

### L2. External CDN dependencies trong dashboard
- **File:** `web/dashboard.html` - Google Fonts, Chart.js từ jsdelivr.net
- **Vấn đề:** CDN compromise có thể inject malicious JS vào admin dashboard.
- **Fix:** Thêm SRI hashes hoặc bundle locally.

### L3. TOML Injection trong DeepSeek config
- **File:** `internal/cliconfig/cliconfig.go:431-437`
- **Vấn đề:** `fmt.Sprintf` interpolation không escape TOML special chars.
- **Fix:** Dùng proper TOML library.

### L4. Backup files (.bak) committed
- **Files:** `main.go.bak`, `config.go.bak`, `client.go.bak`, `stream_pacer.go.bak`
- **Fix:** Thêm `*.bak` vào .gitignore.

---

## 📋 LỖ HỔNG TRONG GIT HISTORY

### Commits đáng ngờ cần audit:

| Commit | Message | Vấn đề |
|--------|---------|--------|
| `8c2efde` | "feat: bypass admin authentication requirement completely" | Admin auth bypass (đã fix) |
| `5d0d44c` | "new" | Thêm 12.5MB binary không giải thích |
| `18df364` | "@" | Xóa .env.example, thay đổi lớn không mô tả |
| `37c0ee4` | "add .gitignore" | Cũng xóa screenshots, zip files |

### Binary files committed:
- `qwen2api-linux-new` (12.5MB) - không có checksums hay build provenance
- `qwen2api-linux` (12.5MB)
- `qwen2api.exe~` (Windows backup binary)

### Sensitive data trong git history:
- JWT tokens trong `cmd/imgtest/main.go`
- Partial API keys trong `qwen2api.log` (49 instances of `sk-d...2f0a`)
- SQLite database với user data
- Zip archives với unknown content

---

## ✅ KHẮC PHỤC NGAY LẬP TỨC

### 1. Fix .gitignore
```gitignore
# Thêm vào .gitignore:
data/
*.db
*.bak
qwen2api.log
node_modules/
tmp/
*.exe~
cmd/imgtest/
history.txt
qwen_models_list.json
test-proxy-speed.ps1
test-vps-speed.sh
test_claude_code_optimization.sh
test_conversation_memory.sh
```

### 2. Fix Auth Bypass (Critical)
```go
// internal/server/handlers.go:38
// THAY:
if !h.deps.Config.Features.APIKeyRotation || len(h.deps.Config.APIKeys) == 0 {
// THÀNH:
if len(h.deps.Config.APIKeys) == 0 {
```

### 3. Fix Timing Attack
```go
// internal/config/config.go
import "crypto/subtle"

func (c Config) AuthorizedKey(key string) bool {
    if len(c.APIKeys) == 0 {
        return true
    }
    now := nowFunc()
    for _, k := range c.APIKeys {
        if k.Value == "" {
            continue
        }
        if subtle.ConstantTimeCompare([]byte(k.Value), []byte(key)) != 1 {
            continue
        }
        if k.ExpiresAt > 0 && k.ExpiresAt < now {
            continue
        }
        return true
    }
    return false
}

func (c Config) AuthorizedAdmin(token string) bool {
    if c.AdminToken == "" {
        return false
    }
    return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(c.AdminToken)) == 1
}
```

### 4. Fix CORS
```go
// internal/server/server.go:159
// Thay vì "*", dùng configurable origins:
allowedOrigins := h.deps.Config.CORSOrigins // new config field
origin := r.Header.Get("Origin")
if isAllowedOrigin(origin, allowedOrigins) {
    w.Header().Set("Access-Control-Allow-Origin", origin)
}
```

### 5. Fix SSRF
```go
// Thêm function validateURL:
func validateURL(rawURL string) error {
    u, err := url.Parse(rawURL)
    if err != nil { return err }
    if u.Scheme != "https" { return errors.New("only HTTPS allowed") }
    ip := net.ParseIP(u.Hostname())
    if ip != nil {
        if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
            return errors.New("private IPs blocked")
        }
    }
    return nil
}
```

### 6. Fix Request Body Size Limit
```go
// internal/server/chat.go
r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB limit
```

### 7. Rotate compromised credentials
- Rotate JWT token trong `cmd/imgtest/main.go`
- Rotate API key `sk-d...2f0a` xuất hiện trong logs
- Đổi admin token từ `admin123` sang strong random string

### 8. Clean git history
```bash
# Sử dụng BFG Repo-Cleaner
java -jar bfg.jar --delete-files "*.db" --delete-files "*.log" --delete-files "*.bak" --delete-files "qwen2api-linux*" --delete-files "*.exe~" --delete-files "node_modules" .
java -jar bfg.jar --replace-text passwords.txt .  # nếu có file chứa secrets
git reflog expire --expire=now --all && git gc --prune=now --aggressive
```

---

## 🔍 KIỂM TRA THEO BRANCH

### Branch v3 (main/HEAD)
- Auth bypass bug có thể tồn tại (cần verify version)
- Same CORS, SSRF issues
- Ít features hơn v6

### Branch v4
- Tương tự v3 với thêm streaming improvements
- Auth logic giống nhau

### Branch v5
- Token estimation improvements
- Auth logic giống nhau

### Branch v6 (current/HEAD)
- Tất cả issues được liệt kê ở trên
- Nhiều features mới nhất nhưng cũng nhiều attack surface hơn

### Devin branches
- `devin/1778772856-wire-protocol-fixes` - Wire protocol fixes
- `devin/1779378209-phase1-stability-features` - Stability features
- `devin/1779575391-fix-claude-responses-and-live-log` - Claude responses fix
- AI-generated code cần audit security kỹ hơn

---

*Báo cáo generated by security audit on 2026-06-09*
