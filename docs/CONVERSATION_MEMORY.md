# Conversation Memory Feature

This document describes the new Conversation Memory feature added to qwen2api, which enables persistent conversation storage similar to how qwen.chat.ai works.

## Overview

The Conversation Memory feature allows:
- **Persistent conversations**: Conversations are stored in a SQLite database and survive server restarts
- **Automatic history loading**: When you send a message with a `conversation_id`, the system automatically loads the conversation history
- **AI memory**: The AI remembers previous messages within the same conversation
- **Conversation management**: Full CRUD API for managing conversations

## Configuration

### Enable Conversation Memory

Add to your `config.json`:

```json
{
  "features": {
    "conversation_memory": true
  },
  "database": {
    "type": "sqlite",
    "path": "./data/qwen2api.db"
  }
}
```

Or use environment variables:

```bash
export QWEN2API_CONVERSATION_MEMORY=true
export QWEN2API_DATABASE_TYPE=sqlite
export QWEN2API_DATABASE_PATH=./data/qwen2api.db
```

### Database Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `type` | string | `"sqlite"` | Database type (`sqlite` or `postgres`) |
| `path` | string | `"./data/qwen2api.db"` | SQLite database file path |
| `url` | string | `""` | PostgreSQL connection URL |
| `max_conns` | int | `10` | Maximum database connections |

## API Endpoints

### Create a Conversation

```bash
curl -X POST http://localhost:5001/v1/conversations \
  -H "Authorization: Bearer sk-local" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "My Conversation",
    "model": "qwen3.7-plus"
  }'
```

Response:
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "title": "My Conversation",
  "model": "qwen3.7-plus",
  "created_at": "2024-01-15T10:30:00Z",
  "updated_at": "2024-01-15T10:30:00Z"
}
```

### List Conversations

```bash
curl http://localhost:5001/v1/conversations?limit=10&offset=0 \
  -H "Authorization: Bearer sk-local"
```

Response:
```json
[
  {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "title": "My Conversation",
    "model": "qwen3.7-plus",
    "created_at": "2024-01-15T10:30:00Z",
    "updated_at": "2024-01-15T10:35:00Z"
  }
]
```

### Get Conversation Details

```bash
curl http://localhost:5001/v1/conversations/550e8400-e29b-41d4-a716-446655440000 \
  -H "Authorization: Bearer sk-local"
```

### Update Conversation

```bash
curl -X PUT http://localhost:5001/v1/conversations/550e8400-e29b-41d4-a716-446655440000 \
  -H "Authorization: Bearer sk-local" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Updated Title",
    "model": "qwen3-max"
  }'
```

### Delete Conversation

```bash
curl -X DELETE http://localhost:5001/v1/conversations/550e8400-e29b-41d4-a716-446655440000 \
  -H "Authorization: Bearer sk-local"
```

### Get Conversation Messages

```bash
curl http://localhost:5001/v1/conversations/550e8400-e29b-41d4-a716-446655440000/messages \
  -H "Authorization: Bearer sk-local"
```

Response:
```json
[
  {
    "id": 1,
    "conversation_id": "550e8400-e29b-41d4-a716-446655440000",
    "role": "user",
    "content": "Hello, how are you?",
    "created_at": "2024-01-15T10:30:00Z"
  },
  {
    "id": 2,
    "conversation_id": "550e8400-e29b-41d4-a716-446655440000",
    "role": "assistant",
    "content": "I'm doing well, thank you!",
    "created_at": "2024-01-15T10:30:05Z"
  }
]
```

## Using Conversation Memory with Chat Completions

### Send Message to Existing Conversation

```bash
curl -X POST http://localhost:5001/v1/chat/completions \
  -H "Authorization: Bearer sk-local" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3.7-plus",
    "conversation_id": "550e8400-e29b-41d4-a716-446655440000",
    "messages": [
      {"role": "user", "content": "What is the secret code?"}
    ]
  }'
```

The system will:
1. Load all previous messages from the conversation
2. Append the new message
3. Send the full history to the AI
4. Save both the user message and AI response to the database
5. Return the response

### Example: Multi-turn Conversation

```bash
# First message - create conversation
curl -X POST http://localhost:5001/v1/chat/completions \
  -H "Authorization: Bearer sk-local" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3.7-plus",
    "conversation_id": "my-conv-123",
    "messages": [
      {"role": "user", "content": "The secret code is 14551"}
    ]
  }'

# Second message - AI remembers the secret
curl -X POST http://localhost:5001/v1/chat/completions \
  -H "Authorization: Bearer sk-local" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3.7-plus",
    "conversation_id": "my-conv-123",
    "messages": [
      {"role": "user", "content": "What is the secret code?"}
    ]
  }'
```

The AI will respond with "14551" because it remembers the previous message in the conversation.

## Database Schema

### Conversations Table

```sql
CREATE TABLE conversations (
    id TEXT PRIMARY KEY,
    title TEXT,
    model TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### Messages Table

```sql
CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
    content TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);

CREATE INDEX idx_messages_conversation_id ON messages(conversation_id);
CREATE INDEX idx_messages_created_at ON messages(conversation_id, created_at);
```

## Migration from Existing Sessions

If you have existing sessions stored in JSON files (from `session_persistence` feature), you can migrate them to the new database format. A migration script will be provided in future releases.

## Performance Considerations

- **Database size**: SQLite databases can grow large with many conversations. Consider periodic cleanup.
- **Query performance**: Indexes are created on `conversation_id` and `created_at` for fast queries.
- **Connection pooling**: The `max_conns` setting controls database connection pooling.

## Troubleshooting

### Database not created

Ensure the directory exists and is writable:
```bash
mkdir -p ./data
chmod 755 ./data
```

### Conversation not found

Check if the conversation ID exists:
```bash
curl http://localhost:5001/v1/conversations \
  -H "Authorization: Bearer sk-local"
```

### Performance issues

For large databases, consider:
1. Increasing `max_conns` in database config
2. Using PostgreSQL for better performance with many concurrent users
3. Implementing conversation cleanup policies

## Future Enhancements

- [ ] PostgreSQL support
- [ ] Conversation export/import
- [ ] Conversation sharing between users
- [ ] Advanced search and filtering
- [ ] Conversation analytics