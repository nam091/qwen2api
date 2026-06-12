# Kế hoạch triển khai các tính năng tinh hoa

## Danh sách tính năng đã chọn (theo độ ưu tiên)

### 🔴 Priority 1 - Critical (Triển khai ngay)

1. **Tool name obfuscation** (#26)
   - Tránh Qwen reject tool calls do tên trùng với internal functions
   - Impact: Cao - giải quyết lỗi "Tool X does not exist"
   - Effort: Trung bình - ~200 lines code

2. **Hallucination protection** (#27)
   - Block duplicate/invalid tool calls
   - Impact: Cao - tăng reliability
   - Effort: Trung bình - ~150 lines code

3. **Performance benchmark** (#31)
   - Đo lường hiện trạng trước khi optimize
   - Impact: Cao - data-driven decisions
   - Effort: Thấp - ~100 lines script

### 🟡 Priority 2 - Important (Triển khai sau)

4. **Schema compression** (#28)
   - Giảm 90% token usage cho tool definitions
   - Impact: Trung bình - tiết kiệm cost
   - Effort: Trung bình - ~250 lines code

5. **Claude API support** (#29)
   - Mở rộng client compatibility
   - Impact: Trung bình - nhiều client dùng Claude SDK
   - Effort: Cao - ~400 lines code

### 🟢 Priority 3 - Nice to have (Cân nhắc sau)

6. **Browser engine fallback** (#30)
   - Bypass anti-bot khi bị block
   - Impact: Thấp - chỉ cần khi bị block
   - Effort: Rất cao - ~600 lines + dependencies

---

## VPS Location Impact Analysis

### Câu hỏi: Chạy trên VPS Singapore có nhanh hơn không?

**TL;DR: CÓ - nhanh hơn đáng kể nếu anh ở Việt Nam/Đông Nam Á**

### Phân tích chi tiết

#### 1. Qwen server location
- `chat.qwen.ai` hosted tại: **China mainland** (likely Beijing/Hangzhou)
- CDN: Alibaba Cloud CDN (có edge nodes ở Singapore)

#### 2. Network latency comparison

| Client Location | → Qwen Server | Estimated RTT | TTFT Impact |
|----------------|---------------|---------------|-------------|
| **Vietnam** | Direct | ~150-250ms | Cao |
| **Singapore VPS** | Direct | ~50-80ms | Thấp |
| **US/EU** | Direct | ~250-400ms | Rất cao |

**RTT (Round-Trip Time)**: Thời gian 1 packet đi về
**TTFT (Time To First Token)**: Thời gian từ request → token đầu tiên

#### 3. Performance gains với Singapore VPS

**Streaming mode** (quan trọng nhất):
- Mỗi SSE chunk phải qua network
- Vietnam: 150ms latency → chunk delay cộng dồn
- Singapore: 50ms latency → smooth streaming
- **Gain: ~100ms per chunk = 2-3x faster perceived speed**

**Non-streaming mode**:
- 1 request/response round-trip
- Vietnam: ~200ms overhead
- Singapore: ~60ms overhead
- **Gain: ~140ms per request**

**Tool calling** (nhiều round-trips):
- Tool call → execute → send result → next response
- Mỗi round-trip tiết kiệm ~100-150ms
- **Gain: ~300-500ms cho 3-4 tool calls**

#### 4. Bandwidth considerations

Singapore VPS thường có:
- Better international bandwidth
- Lower packet loss
- More stable connection
- **Gain: Ít bị timeout/retry**

#### 5. Cost-benefit analysis

**Singapore VPS cost**: ~$5-10/month (1GB RAM, 1 vCPU)
**Performance gain**: 
- 2-3x faster streaming
- 30-40% faster overall response time
- Better reliability

**Verdict: ĐÁng đầu tư nếu:**
- Anh dùng thường xuyên (>100 requests/day)
- Cần real-time streaming experience
- Dùng tool calling nhiều

---

## Benchmark Plan

### Test scenarios

1. **Simple chat** (no tools, no cache)
   - Measure: TTFT, total time, tokens/sec
   - Baseline performance

2. **Tool calling** (3-4 tool calls)
   - Measure: Round-trip latency per call
   - Network impact

3. **Cached requests** (repeat same prompt)
   - Measure: Cache hit speedup
   - Cache effectiveness

4. **Streaming vs non-streaming**
   - Measure: Perceived latency
   - User experience

### Test locations

1. **Local** (Vietnam)
   - Current setup
   - Baseline

2. **Singapore VPS** (if available)
   - Deploy same binary
   - Compare

3. **Python repo** (YuJunZhiXue)
   - Same hardware
   - Language comparison

### Metrics to collect

- **Latency**:
  - TTFT (time to first token)
  - Total response time
  - Per-chunk latency (streaming)

- **Throughput**:
  - Tokens per second
  - Requests per second

- **Resource usage**:
  - Memory (RSS)
  - CPU utilization
  - Network bandwidth

---

## Next Steps

1. ✅ Tạo task list (done)
2. 🔄 Implement tool name obfuscation (#26)
3. 🔄 Implement hallucination protection (#27)
4. 🔄 Create benchmark suite (#31)
5. ⏳ Run benchmarks (local + Singapore if available)
6. ⏳ Analyze results and decide on schema compression
7. ⏳ Consider Claude API support based on demand

---

## Estimated Timeline

- **Week 1**: Tool obfuscation + hallucination protection + benchmark
- **Week 2**: Schema compression (if benchmark shows token usage issue)
- **Week 3**: Claude API support (if needed)
- **Week 4**: Browser fallback (only if getting blocked)

**Total effort**: 2-4 weeks depending on priorities
