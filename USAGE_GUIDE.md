# Hướng dẫn sử dụng Conversation Memory

## Cách hoạt động

### 1. Tự động tạo Conversation (Claude Code)
Khi phát hiện User-Agent là `claude-code`, hệ thống tự động tạo conversation dựa trên IP + User-Agent.

```bash
# Request 1 - tự động tạo conversation
curl -X POST http://localhost:5001/v1/chat/completions \
  -H "User-Agent: claude-code/1.0.0" \
  -d '{"model": "qwen3.7-plus", "messages": [{"role": "user", "content": "Hello"}]}'

# Request 2 - tự động reuse conversation (cùng IP + User-Agent)
curl -X POST http://localhost:5001/v1/chat/completions \
  -H "User-Agent: claude-code/1.0.0" \
  -d '{"model": "qwen3.7-plus", "messages": [
    {"role": "user", "content": "Hello"},
    {"role": "assistant", "content": "Hi!"},
    {"role": "user", "content": "What did I say?"}
  ]}'
```

### 2. Chỉ định Conversation ID (Chrome DevTools, curl, etc.)
Đối với các client khác, bạn cần tạo conversation trước và gửi `conversation_id`:

```bash
# Bước 1: Tạo conversation
curl -X POST http://localhost:5001/v1/conversations \
  -H "Content-Type: application/json" \
  -d '{"title": "My Chat", "model": "qwen3.7-plus"}'

# Response: {"id": "abc123...", ...}

# Bước 2: Gửi message với conversation_id
curl -X POST http://localhost:5001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3.7-plus",
    "conversation_id": "abc123...",
    "messages": [{"role": "user", "content": "Hello"}]
  }'

# Bước 3: Gửi message tiếp theo (AI sẽ nhớ)
curl -X POST http://localhost:5001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3.7-plus",
    "conversation_id": "abc123...",
    "messages": [
      {"role": "user", "content": "Hello"},
      {"role": "assistant", "content": "Hi!"},
      {"role": "user", "content": "What did I say?"}
    ]
  }'
```

## Test bằng Chrome DevTools

1. Mở Chrome DevTools (F12)
2. Vào tab Console
3. Chạy code JavaScript:

```javascript
// Tạo conversation
const convResponse = await fetch('http://localhost:5001/v1/conversations', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({title: 'Test', model: 'qwen3.7-plus'})
});
const conv = await convResponse.json();
console.log('Conversation ID:', conv.id);

// Gửi message 1
const response1 = await fetch('http://localhost:5001/v1/chat/completions', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({
    model: 'qwen3.7-plus',
    conversation_id: conv.id,
    messages: [{role: 'user', content: 'My name is Alice'}]
  })
});
const data1 = await response1.json();
console.log('Response 1:', data1.choices[0].message.content);

// Gửi message 2 - AI sẽ nhớ
const response2 = await fetch('http://localhost:5001/v1/chat/completions', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({
    model: 'qwen3.7-plus',
    conversation_id: conv.id,
    messages: [
      {role: 'user', content: 'My name is Alice'},
      {role: 'assistant', content: 'Hi Alice!'},
      {role: 'user', content: 'What is my name?'}
    ]
  })
});
const data2 = await response2.json();
console.log('Response 2:', data2.choices[0].message.content);
// Expected: "Your name is Alice"
```

## Lưu ý quan trọng

1. **Claude Code**: Tự động tạo conversation, không cần conversation_id
2. **Chrome DevTools**: Phải tạo conversation trước và gửi conversation_id
3. **Cùng conversation**: AI sẽ nhớ tất cả messages trong cùng conversation
4. **Khác conversation**: AI không nhớ messages từ conversation khác

## Kiểm tra conversations

```bash
# Liệt kê tất cả conversations
curl http://localhost:5001/v1/conversations

# Xem messages trong conversation
curl http://localhost:5001/v1/conversations/{id}/messages
```