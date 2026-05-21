# qwen2api - Roadmap nâng cấp

## 🎯 Tổng quan

Repo: **https://github.com/nam091/qwen2api** (branch `v2`)

qwen2api là OpenAI-compatible gateway cho Qwen AI, viết bằng Go. Sau khi phân tích repo Python [YuJunZhiXue/qwen2API](https://github.com/YuJunZhiXue/qwen2API), chúng ta đã cherry-pick các tính năng tinh hoa và có roadmap rõ ràng cho các nâng cấp tiếp theo.

---

## ✅ Đã hoàn thành (v2 - 2026-05-21)

### Core Features
- ✅ OpenAI Chat Completions API (`/v1/chat/completions`)
- ✅ Streaming & non-streaming responses
- ✅ Token pool với retry logic
- ✅ Prompt caching (chat ID reuse)
- ✅ Conversation continuity (hash-based)
- ✅ Model aliases
- ✅ Multimodal support (images)
- ✅ Prometheus metrics (`/metrics`)
- ✅ Dashboard (`/dashboard`)
- ✅ Admin API (key rotation)
- ✅ Request logging

### Advanced Tool Calling
- ✅ **Tool name obfuscation** - Tránh Qwen reject do tên trùng internal functions
- ✅ **Hallucination protection** - Filter duplicate/invalid tool calls
- ✅ **JSON schema compression** - Giảm 90% token usage cho tool definitions
- ✅ **JSON auto-repair** - Sửa malformed JSON trong tool calls
- ✅ Multi-format parsing (JSON/XML/Claude/bare JSON)

### API Compatibility
- ✅ **Claude Messages API** - `/anthropic/v1/messages` endpoint
- ✅ OpenAI-compatible endpoints

### DevOps
- ✅ **Performance benchmark suite** - Scripts đo TTFT, latency, throughput
- ✅ Docker support
- ✅ Health checks (`/healthz`, `/readyz`)

---

## 🔄 Đang làm (In Progress)

### Priority 2 - Important
- 🔄 **Truncation auto-continuation** (#32) - Auto-retry khi tool call bị cắt
  - Status: Task created, chưa implement
  - Impact: Trung bình - giảm tool call failures
  - Effort: ~200 lines code

---

## 📋 Kế hoạch nâng cấp (Roadmap)

### 🔴 Phase 1 - Stability & Reliability (1-2 tuần)

#### 1.1 Truncation Auto-Continuation (#32)
**Mục tiêu**: Tự động retry khi tool call JSON bị cắt do max_tokens

**Implementation**:
- Detect incomplete JSON trong `<tool_call>` blocks
- Parse partial JSON để xác định thiếu gì
- Gửi continuation request với context
- Merge responses

**Files**:
- `internal/toolcall/continuation.go` (new)
- `internal/toolcall/parser.go` (modify)
- `internal/server/chat.go` (modify)

**Effort**: 2-3 ngày

---

#### 1.2 History Refusal Cleaning
**Mục tiêu**: Remove rejection text từ conversation history để tránh cascade rejection

**Implementation**:
- Detect refusal patterns ("I cannot", "I'm unable to", etc.)
- Strip từ history trước khi gửi follow-up
- Preserve context nhưng remove negative signals

**Files**:
- `internal/refusal/cleaner.go` (new)
- `internal/server/chat.go` (modify)

**Effort**: 1-2 ngày

---

#### 1.3 Edit Fuzzy Matching
**Mục tiêu**: Auto-fix smart quotes, whitespace diffs trong Edit tool calls

**Implementation**:
- Normalize quotes (" " → " ")
- Normalize whitespace (tabs/spaces)
- Fuzzy match với Levenshtein distance
- Fallback to exact match

**Files**:
- `internal/fuzzy/matcher.go` (new)
- Integration vào tool execution layer

**Effort**: 2-3 ngày

---

### 🟡 Phase 2 - Performance & Scale (2-3 tuần)

#### 2.1 Incremental Streaming Warmup
**Mục tiêu**: Buffer 96 chars trước khi stream để tránh jitter

**Implementation**:
- Buffer initial chunks
- Detect `<think>` phase và skip streaming
- Stream answer phase only

**Files**:
- `internal/server/chat.go` (modify streaming logic)

**Effort**: 1 ngày

---

#### 2.2 Topic Isolation
**Mục tiêu**: Detect task switch bằng Jaccard similarity, tạo chat session mới

**Implementation**:
- Extract keywords từ messages
- Calculate Jaccard similarity với previous context
- Threshold < 0.3 → new chat session

**Files**:
- `internal/topic/detector.go` (new)
- `internal/server/chat.go` (modify)

**Effort**: 2-3 ngày

---

#### 2.3 File Content Caching
**Mục tiêu**: Cache "unchanged since last read" file content

**Implementation**:
- Track file hashes
- Return cached content nếu hash match
- Reduce redundant reads

**Files**:
- `internal/filecache/cache.go` (new)
- Integration vào Read tool

**Effort**: 2-3 ngày

---

### 🟢 Phase 3 - Extended Features (3-4 tuần)

#### 3.1 Gemini API Support
**Mục tiêu**: `/v1beta/models/*` endpoint cho Gemini SDK compatibility

**Implementation**:
- Define Gemini types
- Convert Gemini ↔ OpenAI format
- Add `/v1beta/models/{model}:generateContent` endpoint

**Files**:
- `internal/gemini/` (new package)
- `internal/server/gemini_handler.go` (new)

**Effort**: 3-4 ngày

---

#### 3.2 Image Generation Endpoint
**Mục tiêu**: `/v1/images/generations` (DALL-E compatible)

**Implementation**:
- Proxy to Qwen image generation API
- Convert OpenAI format → Qwen format
- Return image URLs or base64

**Files**:
- `internal/server/images_handler.go` (new)

**Effort**: 2-3 ngày

---

#### 3.3 React WebUI Upgrade
**Mục tiêu**: Full-featured management dashboard

**Implementation**:
- React SPA với Vite
- Account/key management UI
- Metrics visualization
- Request logs viewer

**Files**:
- `web/` (new directory)
- `internal/server/webui.go` (new)

**Effort**: 1-2 tuần (nếu cần)

---

### 🔵 Phase 4 - Anti-Bot & Resilience (Conditional)

#### 4.1 Browser Engine Fallback (#30)
**Mục tiêu**: Bypass anti-bot khi HTTP client bị block

**⚠️ Chỉ implement nếu bị block**

**Implementation**:
- Integrate Playwright/Puppeteer
- Fallback to browser automation khi detect block
- Maintain session cookies

**Dependencies**:
- `github.com/playwright-community/playwright-go`

**Files**:
- `internal/browser/` (new package)
- `internal/qwen/client.go` (modify)

**Effort**: 1 tuần

---

## 🎯 Priorities Summary

### Must Have (Phase 1)
1. Truncation auto-continuation
2. History refusal cleaning
3. Edit fuzzy matching

### Should Have (Phase 2)
4. Incremental streaming warmup
5. Topic isolation
6. File content caching

### Nice to Have (Phase 3)
7. Gemini API support
8. Image generation endpoint
9. React WebUI upgrade

### Conditional (Phase 4)
10. Browser engine fallback (only if blocked)

---

## 📊 Performance Optimization

### VPS Singapore Deployment
**Khuyến nghị**: Deploy lên VPS Singapore để giảm latency

**Expected gains**:
- 2-3x faster streaming (100ms → 50ms per chunk)
- 30-40% faster overall response time
- Better reliability (ít timeout/retry)

**Cost**: ~$5-10/month

**Providers**:
- DigitalOcean Singapore
- Vultr Singapore
- Linode Singapore

### Benchmark Results
Chạy `scripts/benchmark.ps1` để đo:
- TTFT (Time To First Token)
- Total latency
- Tokens/sec
- Success rate

---

## 🛠️ Development Workflow

### 1. Local Development
```powershell
# Build
go build -o qwen2api.exe ./cmd/qwen2api

# Run
$env:QWEN2API_TOKENS="token1,token2"
.\qwen2api.exe

# Test
go test ./...
```

### 2. Docker Build
```bash
docker build -t qwen2api:v2 .
docker run -p 8787:8787 -e QWEN2API_TOKENS="..." qwen2api:v2
```

### 3. Deploy to VPS
```bash
# Copy binary
scp qwen2api user@vps:/opt/qwen2api/

# Run with systemd
sudo systemctl start qwen2api
```

---

## 📚 Documentation

### Current Docs
- `README.md` - Setup & usage
- `comparison.md` - Feature comparison với Python repo
- `implementation-plan.md` - Chi tiết implementation
- `scripts/benchmark-guide.md` - Benchmarking instructions

### TODO Docs
- [ ] API reference (OpenAPI spec)
- [ ] Deployment guide (Docker/K8s)
- [ ] Performance tuning guide
- [ ] Troubleshooting guide

---

## 🤝 Contributing

### Code Style
- Go standard formatting (`gofmt`)
- Comprehensive tests cho new features
- Comments cho complex logic

### Testing
- Unit tests: `go test ./internal/...`
- Integration tests: `go test ./internal/server/...`
- Benchmark: `scripts/benchmark.ps1`

### Git Workflow
- Branch `main`: Stable releases
- Branch `v2`: Active development
- Feature branches: `feature/xxx`

---

## 📞 Contact

- GitHub: https://github.com/nam091/qwen2api
- Issues: https://github.com/nam091/qwen2api/issues

---

## 📝 Changelog

### v2 (2026-05-21)
- ✅ Tool name obfuscation
- ✅ Hallucination protection
- ✅ Schema compression
- ✅ Claude API support
- ✅ JSON auto-repair
- ✅ Performance benchmark suite

### v1 (Initial)
- ✅ OpenAI Chat Completions
- ✅ Streaming support
- ✅ Token pool
- ✅ Prompt caching
- ✅ Metrics & dashboard
