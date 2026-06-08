package database

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps the database connection and provides helper methods.
type DB struct {
	conn   *sql.DB
	logger *slog.Logger
}

// Config holds database configuration.
type Config struct {
	Type string `json:"type"` // "sqlite" or "postgres"
	Path string `json:"path"` // For SQLite: "./data/qwen2api.db"
	URL  string `json:"url"`  // For PostgreSQL connection string
}

// New creates a new database connection.
func New(cfg Config, logger *slog.Logger) (*DB, error) {
	switch cfg.Type {
	case "sqlite", "":
		return newSQLite(cfg.Path, logger)
	case "postgres":
		return nil, fmt.Errorf("postgres not yet supported")
	default:
		return nil, fmt.Errorf("unsupported database type: %s", cfg.Type)
	}
}

func newSQLite(dbPath string, logger *slog.Logger) (*DB, error) {
	if dbPath == "" {
		dbPath = "./data/qwen2api.db"
	}

	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	conn, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Test connection
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	db := &DB{
		conn:   conn,
		logger: logger,
	}

	// Run migrations
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	logger.Info("database initialized", "path", dbPath)
	return db, nil
}

func (db *DB) migrate() error {
	// Create tables if they don't exist
	queries := []string{
		`CREATE TABLE IF NOT EXISTS conversations (
			id TEXT PRIMARY KEY,
			title TEXT,
			model TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id TEXT NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
			content TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation_id ON messages(conversation_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(conversation_id, created_at)`,
		`CREATE TRIGGER IF NOT EXISTS update_conversation_timestamp
			AFTER INSERT ON messages
			BEGIN
				UPDATE conversations SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.conversation_id;
			END`,
	}

	for _, query := range queries {
		if _, err := db.conn.Exec(query); err != nil {
			return fmt.Errorf("execute migration: %w", err)
		}
	}

	db.logger.Info("database migrations completed")
	return nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// Conn returns the underlying sql.DB connection.
func (db *DB) Conn() *sql.DB {
	return db.conn
}