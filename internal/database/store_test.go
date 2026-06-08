package database

import (
	"log/slog"
	"os"
	"testing"
)

func TestStore_CreateAndGetConversation(t *testing.T) {
	// Create a temporary database for testing
	tmpFile, err := os.CreateTemp("", "qwen2api-test-*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	db, err := New(Config{Type: "sqlite", Path: tmpFile.Name()}, logger)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	defer db.Close()

	store := NewStore(db, logger)

	// Test CreateConversation
	conv, err := store.CreateConversation("Test Conversation", "qwen3.7-plus")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if conv.ID == "" {
		t.Fatal("conversation ID should not be empty")
	}
	if conv.Title != "Test Conversation" {
		t.Errorf("expected title 'Test Conversation', got '%s'", conv.Title)
	}
	if conv.Model != "qwen3.7-plus" {
		t.Errorf("expected model 'qwen3.7-plus', got '%s'", conv.Model)
	}

	// Test GetConversation
	fetched, err := store.GetConversation(conv.ID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if fetched == nil {
		t.Fatal("conversation should exist")
	}
	if fetched.ID != conv.ID {
		t.Errorf("expected ID '%s', got '%s'", conv.ID, fetched.ID)
	}
	if fetched.Title != conv.Title {
		t.Errorf("expected title '%s', got '%s'", conv.Title, fetched.Title)
	}
}

func TestStore_AddAndGetMessages(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "qwen2api-test-*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	db, err := New(Config{Type: "sqlite", Path: tmpFile.Name()}, logger)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	defer db.Close()

	store := NewStore(db, logger)

	// Create a conversation
	conv, err := store.CreateConversation("Test", "qwen3.7-plus")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	// Add messages
	msg1, err := store.AddMessage(conv.ID, "user", "Hello, how are you?")
	if err != nil {
		t.Fatalf("add message 1: %v", err)
	}
	if msg1.Role != "user" {
		t.Errorf("expected role 'user', got '%s'", msg1.Role)
	}
	if msg1.Content != "Hello, how are you?" {
		t.Errorf("expected content 'Hello, how are you?', got '%s'", msg1.Content)
	}

	msg2, err := store.AddMessage(conv.ID, "assistant", "I'm doing well, thank you!")
	if err != nil {
		t.Fatalf("add message 2: %v", err)
	}
	if msg2.Role != "assistant" {
		t.Errorf("expected role 'assistant', got '%s'", msg2.Role)
	}

	// Get messages
	messages, err := store.GetMessages(conv.ID)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].Content != "Hello, how are you?" {
		t.Errorf("expected first message content 'Hello, how are you?', got '%s'", messages[0].Content)
	}
	if messages[1].Content != "I'm doing well, thank you!" {
		t.Errorf("expected second message content 'I'm doing well, thank you!', got '%s'", messages[1].Content)
	}

	// Test GetMessageCount
	count, err := store.GetMessageCount(conv.ID)
	if err != nil {
		t.Fatalf("get message count: %v", err)
	}
	if count != 2 {
		t.Errorf("expected message count 2, got %d", count)
	}
}

func TestStore_ListConversations(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "qwen2api-test-*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	db, err := New(Config{Type: "sqlite", Path: tmpFile.Name()}, logger)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	defer db.Close()

	store := NewStore(db, logger)

	// Create multiple conversations
	for i := 0; i < 5; i++ {
		_, err := store.CreateConversation("Conversation "+string(rune('A'+i)), "qwen3.7-plus")
		if err != nil {
			t.Fatalf("create conversation %d: %v", i, err)
		}
	}

	// List conversations
	conversations, err := store.ListConversations(10, 0)
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(conversations) != 5 {
		t.Errorf("expected 5 conversations, got %d", len(conversations))
	}

	// Test with limit
	conversations, err = store.ListConversations(2, 0)
	if err != nil {
		t.Fatalf("list conversations with limit: %v", err)
	}
	if len(conversations) != 2 {
		t.Errorf("expected 2 conversations, got %d", len(conversations))
	}
}

func TestStore_DeleteConversation(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "qwen2api-test-*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	db, err := New(Config{Type: "sqlite", Path: tmpFile.Name()}, logger)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	defer db.Close()

	store := NewStore(db, logger)

	// Create a conversation
	conv, err := store.CreateConversation("To Delete", "qwen3.7-plus")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	// Add a message
	_, err = store.AddMessage(conv.ID, "user", "Test message")
	if err != nil {
		t.Fatalf("add message: %v", err)
	}

	// Delete conversation
	err = store.DeleteConversation(conv.ID)
	if err != nil {
		t.Fatalf("delete conversation: %v", err)
	}

	// Verify conversation is deleted
	fetched, err := store.GetConversation(conv.ID)
	if err != nil {
		t.Fatalf("get conversation after delete: %v", err)
	}
	if fetched != nil {
		t.Error("conversation should be deleted")
	}

	// Verify messages are deleted
	messages, err := store.GetMessages(conv.ID)
	if err != nil {
		t.Fatalf("get messages after delete: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("expected 0 messages after delete, got %d", len(messages))
	}
}

func TestStore_ConversationExists(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "qwen2api-test-*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	db, err := New(Config{Type: "sqlite", Path: tmpFile.Name()}, logger)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	defer db.Close()

	store := NewStore(db, logger)

	// Create a conversation
	conv, err := store.CreateConversation("Exists Test", "qwen3.7-plus")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	// Test exists
	exists, err := store.ConversationExists(conv.ID)
	if err != nil {
		t.Fatalf("check conversation exists: %v", err)
	}
	if !exists {
		t.Error("conversation should exist")
	}

	// Test not exists
	exists, err = store.ConversationExists("non-existent-id")
	if err != nil {
		t.Fatalf("check conversation not exists: %v", err)
	}
	if exists {
		t.Error("conversation should not exist")
	}
}

func TestStore_UpdateConversation(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "qwen2api-test-*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	db, err := New(Config{Type: "sqlite", Path: tmpFile.Name()}, logger)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	defer db.Close()

	store := NewStore(db, logger)

	// Create a conversation
	conv, err := store.CreateConversation("Original Title", "qwen3.7-plus")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	// Update title
	err = store.UpdateConversationTitle(conv.ID, "New Title")
	if err != nil {
		t.Fatalf("update conversation title: %v", err)
	}

	// Update model
	err = store.UpdateConversationModel(conv.ID, "qwen3-max")
	if err != nil {
		t.Fatalf("update conversation model: %v", err)
	}

	// Verify updates
	fetched, err := store.GetConversation(conv.ID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if fetched.Title != "New Title" {
		t.Errorf("expected title 'New Title', got '%s'", fetched.Title)
	}
	if fetched.Model != "qwen3-max" {
		t.Errorf("expected model 'qwen3-max', got '%s'", fetched.Model)
	}
}