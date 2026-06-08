package database

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Conversation represents a chat conversation.
type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Message represents a single message in a conversation.
type Message struct {
	ID             int64     `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

// Store provides conversation and message storage backed by a database.
type Store struct {
	db     *DB
	logger *slog.Logger
}

// NewStore creates a new database-backed store.
func NewStore(db *DB, logger *slog.Logger) *Store {
	return &Store{
		db:     db,
		logger: logger,
	}
}

// CreateConversation creates a new conversation and returns it.
func (s *Store) CreateConversation(title, model string) (*Conversation, error) {
	id := uuid.NewString()
	now := time.Now()

	query := `INSERT INTO conversations (id, title, model, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.conn.Exec(query, id, title, model, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert conversation: %w", err)
	}

	return &Conversation{
		ID:        id,
		Title:     title,
		Model:     model,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// GetConversation retrieves a conversation by ID.
func (s *Store) GetConversation(id string) (*Conversation, error) {
	query := `SELECT id, title, model, created_at, updated_at FROM conversations WHERE id = ?`
	row := s.db.conn.QueryRow(query, id)

	var conv Conversation
	err := row.Scan(&conv.ID, &conv.Title, &conv.Model, &conv.CreatedAt, &conv.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan conversation: %w", err)
	}

	return &conv, nil
}

// ListConversations returns all conversations, ordered by most recent.
func (s *Store) ListConversations(limit, offset int) ([]*Conversation, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, title, model, created_at, updated_at FROM conversations ORDER BY updated_at DESC LIMIT ? OFFSET ?`
	rows, err := s.db.conn.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query conversations: %w", err)
	}
	defer rows.Close()

	var conversations []*Conversation
	for rows.Next() {
		var conv Conversation
		if err := rows.Scan(&conv.ID, &conv.Title, &conv.Model, &conv.CreatedAt, &conv.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		conversations = append(conversations, &conv)
	}

	return conversations, nil
}

// DeleteConversation deletes a conversation and all its messages.
func (s *Store) DeleteConversation(id string) error {
	query := `DELETE FROM conversations WHERE id = ?`
	result, err := s.db.conn.Exec(query, id)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("conversation not found: %s", id)
	}

	return nil
}

// AddMessage adds a message to a conversation.
func (s *Store) AddMessage(conversationID, role, content string) (*Message, error) {
	query := `INSERT INTO messages (conversation_id, role, content, created_at) VALUES (?, ?, ?, ?)`
	now := time.Now()
	result, err := s.db.conn.Exec(query, conversationID, role, content, now)
	if err != nil {
		return nil, fmt.Errorf("insert message: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("get last insert id: %w", err)
	}

	return &Message{
		ID:             id,
		ConversationID: conversationID,
		Role:           role,
		Content:        content,
		CreatedAt:      now,
	}, nil
}

// GetMessages returns all messages for a conversation, ordered by creation time.
func (s *Store) GetMessages(conversationID string) ([]*Message, error) {
	query := `SELECT id, conversation_id, role, content, created_at FROM messages WHERE conversation_id = ? ORDER BY created_at ASC`
	rows, err := s.db.conn.Query(query, conversationID)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var messages []*Message
	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.ID, &msg.ConversationID, &msg.Role, &msg.Content, &msg.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		messages = append(messages, &msg)
	}

	return messages, nil
}

// GetMessageCount returns the number of messages in a conversation.
func (s *Store) GetMessageCount(conversationID string) (int, error) {
	query := `SELECT COUNT(*) FROM messages WHERE conversation_id = ?`
	var count int
	err := s.db.conn.QueryRow(query, conversationID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count messages: %w", err)
	}
	return count, nil
}

// UpdateConversationTitle updates the title of a conversation.
func (s *Store) UpdateConversationTitle(id, title string) error {
	query := `UPDATE conversations SET title = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.conn.Exec(query, title, time.Now(), id)
	if err != nil {
		return fmt.Errorf("update conversation title: %w", err)
	}
	return nil
}

// UpdateConversationModel updates the model of a conversation.
func (s *Store) UpdateConversationModel(id, model string) error {
	query := `UPDATE conversations SET model = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.conn.Exec(query, model, time.Now(), id)
	if err != nil {
		return fmt.Errorf("update conversation model: %w", err)
	}
	return nil
}

// ConversationExists checks if a conversation exists.
func (s *Store) ConversationExists(id string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM conversations WHERE id = ?)`
	var exists bool
	err := s.db.conn.QueryRow(query, id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check conversation exists: %w", err)
	}
	return exists, nil
}