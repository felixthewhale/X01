package db

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"

	_ "modernc.org/sqlite"

	"X01/internal/logger"
)

var DB *sql.DB

const dbName = "x01.db"

// InitDB initializes the SQLite database and creates necessary tables.
func InitDB() error {
	var err error
	DB, err = sql.Open("sqlite", dbName)
	if err != nil {
		return err
	}

	// Enable WAL mode for better performance and disk longevity
	if _, err := DB.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return fmt.Errorf("failed to set WAL mode: %v", err)
	}

	// Create messages table
	query := `
	CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		role TEXT NOT NULL,
		content TEXT,
		tool_calls TEXT,
		tool_call_id TEXT,
		name TEXT,
		reasoning TEXT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
	);`

	_, err = DB.Exec(query)
	if err != nil {
		return err
	}

	// Create memories table
	query = `
	CREATE TABLE IF NOT EXISTS memories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		content TEXT NOT NULL,
		importance INTEGER DEFAULT 1,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = DB.Exec(query)
	if err != nil {
		return err
	}

	// Create state table
	query = `
	CREATE TABLE IF NOT EXISTS state (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);`
	_, err = DB.Exec(query)
	if err != nil {
		return err
	}

	// Create pending_messages table
	query = `
	CREATE TABLE IF NOT EXISTS pending_messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		content TEXT NOT NULL,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = DB.Exec(query)
	if err != nil {
		return err
	}

	// Migration: Add reasoning column if it doesn't exist
	_, _ = DB.Exec("ALTER TABLE messages ADD COLUMN reasoning TEXT;")

	return nil
}

func CloseDB() error {
	if DB != nil {
		return DB.Close()
	}
	return nil
}

type Message struct {
	ID         int64           `json:"-"`
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Reasoning  string          `json:"reasoning,omitempty"`
	Timestamp  string          `json:"-"`
}

type Memory struct {
	ID         int64  `json:"id"`
	Content    string `json:"content"`
	Importance int    `json:"importance"`
	Timestamp  string `json:"timestamp"`
}

func SaveMessage(m Message) error {
	toolCallsStr := ""
	if m.ToolCalls != nil {
		toolCallsStr = string(m.ToolCalls)
	}
	query := "INSERT INTO messages (role, content, tool_calls, tool_call_id, name, reasoning) VALUES (?, ?, ?, ?, ?, ?)"
	_, err := DB.Exec(query, m.Role, m.Content, toolCallsStr, m.ToolCallID, m.Name, m.Reasoning)
	return err
}

// SaveMessages saves multiple messages in a single atomic transaction.
func SaveMessages(msgs []Message) error {
	if len(msgs) == 0 {
		logger.LogInfo("No messages to save this turn.")
		return nil
	}

	logger.LogInfo("Beginning transaction to save %d messages...", len(msgs))
	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	query := "INSERT INTO messages (role, content, tool_calls, tool_call_id, name, reasoning) VALUES (?, ?, ?, ?, ?, ?)"
	for _, m := range msgs {
		toolCallsStr := ""
		if m.ToolCalls != nil {
			toolCallsStr = string(m.ToolCalls)
		}
		if _, err := tx.Exec(query, m.Role, m.Content, toolCallsStr, m.ToolCallID, m.Name, m.Reasoning); err != nil {
			logger.LogError("Transaction exec failed: %v", err)
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		logger.LogError("Transaction commit failed: %v", err)
		return err
	}
	logger.LogSuccess("Transaction committed successfully.")
	return nil
}

func GetHistory(limit int) ([]Message, error) {
	query := "SELECT id, role, content, tool_calls, tool_call_id, name, reasoning, timestamp FROM messages ORDER BY id DESC LIMIT ?"
	rows, err := DB.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []Message
	for rows.Next() {
		var msg Message
		var toolCallsStr string
		if err := rows.Scan(&msg.ID, &msg.Role, &msg.Content, &toolCallsStr, &msg.ToolCallID, &msg.Name, &msg.Reasoning, &msg.Timestamp); err != nil {
			return nil, err
		}
		if toolCallsStr != "" {
			msg.ToolCalls = json.RawMessage(toolCallsStr)
		}
		history = append(history, msg)
	}

	// Reverse to get chronological order
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}

	return history, nil
}

// GetContextWindow fetches the last `limit` messages but ensures we don't slice a turn in half.
// If the oldest message is a "tool" message, it keeps fetching backwards until it finds the
// start of the turn (the Assistant message that called the tool).
func GetContextWindow(limit int) ([]Message, error) {
	// 1. Fetch initial batch
	history, err := GetHistory(limit)
	if err != nil {
		return nil, err
	}

	if len(history) == 0 {
		return history, nil
	}

	// History is returned Newest -> Oldest by GetHistory? 
	// Wait, GetHistory sorts it Oldest -> Newest before returning!
	// Let's check GetHistory implementation...
	// It fetches DESC, then Reverses. So index 0 is Oldest.

	// 2. Check strict turn boundary at the start (history[0])
	// If history[0] is a Tool, we are missing its context (the Assistant call).
	// We must fetch backwards.
	
	for {
		if len(history) == 0 {
			break
		}
		
		oldest := history[0]
		if oldest.Role == "tool" {
			// Fetch the message immediately preceding this one
			// (ID < oldest.ID) ORDER BY ID DESC LIMIT 1
			var prevMsg Message
			var toolCallsStr string
			
			query := "SELECT id, role, content, tool_calls, tool_call_id, name, reasoning, timestamp FROM messages WHERE id < ? ORDER BY id DESC LIMIT 1"
			err := DB.QueryRow(query, oldest.ID).Scan(&prevMsg.ID, &prevMsg.Role, &prevMsg.Content, &toolCallsStr, &prevMsg.ToolCallID, &prevMsg.Name, &prevMsg.Reasoning, &prevMsg.Timestamp)
			
			if err == sql.ErrNoRows {
				// No more history? Then this tool message is truly an orphan (data corruption/cleanup).
				// We can't fix it. Stop.
				break
			} else if err != nil {
				return nil, err
			}

			if toolCallsStr != "" {
				prevMsg.ToolCalls = json.RawMessage(toolCallsStr)
			}

			// Prepend to history
			history = append([]Message{prevMsg}, history...)
			
		} else {
			break
		}
	}

	return history, nil
}

func SearchMessages(keyword string, limit int) ([]Message, error) {
	query := `SELECT id, role, content, tool_calls, tool_call_id, name, reasoning, timestamp 
	          FROM messages 
	          WHERE content LIKE ? OR reasoning LIKE ? 
	          ORDER BY id DESC LIMIT ?`
	
	pattern := "%" + keyword + "%"
	rows, err := DB.Query(query, pattern, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Message
	for rows.Next() {
		var msg Message
		var toolCallsStr string
		if err := rows.Scan(&msg.ID, &msg.Role, &msg.Content, &toolCallsStr, &msg.ToolCallID, &msg.Name, &msg.Reasoning, &msg.Timestamp); err != nil {
			return nil, err
		}
		if toolCallsStr != "" {
			msg.ToolCalls = json.RawMessage(toolCallsStr)
		}
		results = append(results, msg)
	}
	return results, nil
}

func ClearHistory() error {
	_, err := DB.Exec("DELETE FROM messages")
	return err
}

func GetState(key string) (string, error) {
	var value string
	err := DB.QueryRow("SELECT value FROM state WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func SetState(key string, value string) error {
	query := `INSERT INTO state (key, value) VALUES (?, ?)
	          ON CONFLICT(key) DO UPDATE SET value = excluded.value`
	_, err := DB.Exec(query, key, value)
	return err
}

func AddPendingMessage(content string) error {
	_, err := DB.Exec("INSERT INTO pending_messages (content) VALUES (?)", content)
	return err
}

func GetPendingCount() (int, error) {
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM pending_messages").Scan(&count)
	return count, err
}

func FetchAndClearPending() ([]string, error) {
	rows, err := DB.Query("SELECT content FROM pending_messages ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return nil, err
		}
		messages = append(messages, content)
	}

	_, err = DB.Exec("DELETE FROM pending_messages")
	return messages, err
}

func AddMemory(content string) (int64, error) {
	query := "INSERT INTO memories (content) VALUES (?)"
	res, err := DB.Exec(query, content)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func DeleteMemory(id int64) error {
	query := "DELETE FROM memories WHERE id = ?"
	_, err := DB.Exec(query, id)
	return err
}

func GetMemories(limit int) ([]Memory, error) {
	query := "SELECT id, content, importance, timestamp FROM memories ORDER BY id DESC LIMIT ?"
	rows, err := DB.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Content, &m.Importance, &m.Timestamp); err != nil {
			return nil, err
		}
		memories = append(memories, m)
	}
	return memories, nil
}
