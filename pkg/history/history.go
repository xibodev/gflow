package history

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	sqlite "modernc.org/sqlite"
)

// Entry records details of a generated asset.
type Entry struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"` // "image" or "video"
	Prompt    string    `json:"prompt"`
	LocalPath string    `json:"local_path"`
	URL       string    `json:"url,omitempty"`
	Aspect    string    `json:"aspect,omitempty"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

var (
	mu           sync.Mutex
	pathOverride string
	db           *sql.DB
)

// SetPathOverride injects the history file path in tests.
func SetPathOverride(p string) {
	mu.Lock()
	defer mu.Unlock()
	if db != nil {
		_ = db.Close()
		db = nil
	}
	pathOverride = p
}

// getDB returns the SQLite database connection, initializing it if needed.
func getDB() (*sql.DB, error) {
	mu.Lock()
	defer mu.Unlock()
	if db != nil {
		return db, nil
	}
	path, err := getHistoryPathLocked()
	if err != nil {
		return nil, err
	}
	if err := migrateLegacyJSONIfNeeded(path); err != nil {
		return nil, fmt.Errorf("migrate history: %w", err)
	}
	d, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	if _, err := d.Exec("PRAGMA journal_mode=WAL"); err != nil {
		d.Close()
		return nil, fmt.Errorf("failed to set WAL mode: %w", err)
	}
	if _, err := d.Exec("PRAGMA busy_timeout=5000"); err != nil {
		d.Close()
		return nil, fmt.Errorf("failed to set busy_timeout: %w", err)
	}

	if err := checkIntegrity(d); err != nil {
		d.Close()
		return nil, err
	}
	if err := ensureSchema(d); err != nil {
		d.Close()
		return nil, fmt.Errorf("failed to create schema: %w", err)
	}

	db = d
	return d, nil
}

// checkIntegrity runs PRAGMA integrity_check on the given db connection.
func checkIntegrity(d *sql.DB) error {
	var result string
	err := d.QueryRow("PRAGMA integrity_check").Scan(&result)
	if err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity check failed: %s", result)
	}
	return nil
}

// CheckIntegrity runs PRAGMA integrity_check on the database.
func CheckIntegrity() error {
	d, err := getDB()
	if err != nil {
		return err
	}
	var result string
	err = d.QueryRow("PRAGMA integrity_check").Scan(&result)
	if err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity check failed: %s", result)
	}
	return nil
}

// Recover backs up the corrupted DB and recreates a fresh one.
func Recover() error {
	mu.Lock()
	defer mu.Unlock()
	path, err := getHistoryPathLocked()
	if err != nil {
		return err
	}
	return recoverDBByPath(path)
}

func recoverDBByPath(path string) error {
	if db != nil {
		_ = db.Close()
		db = nil
	}
	backupPath := path + ".corrupt." + fmt.Sprintf("%d", time.Now().Unix())
	// Rename the file; if it does not exist, just recreate fresh.
	if err := os.Rename(path, backupPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to backup corrupt db: %w", err)
	}
	// Also clean up WAL/SHM sidecars if present.
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	d2, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("failed to recreate database: %w", err)
	}
	d2.SetMaxOpenConns(1)
	if _, err := d2.Exec("PRAGMA journal_mode=WAL"); err != nil {
		d2.Close()
		return err
	}
	if _, err := d2.Exec("PRAGMA busy_timeout=5000"); err != nil {
		d2.Close()
		return err
	}
	if _, err := d2.Exec("CREATE TABLE IF NOT EXISTS history_entries (id TEXT PRIMARY KEY, type TEXT, prompt TEXT, local_path TEXT, url TEXT, aspect TEXT, model TEXT, created_at TEXT)"); err != nil {
		d2.Close()
		return fmt.Errorf("failed to create schema: %w", err)
	}
	db = d2
	return nil
}

// retryBusy executes fn, retrying on sqlite.ErrBusy up to 3 times.
func retryBusy(fn func() error) error {
	const maxRetries = 3
	var lastErr error
	for i := 0; i <= maxRetries; i++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		var se *sqlite.Error
		if errors.As(lastErr, &se) && se.Code() == 5 { // SQLITE_BUSY = 5
			if i < maxRetries {
				time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
				continue
			}
		}
		return lastErr
	}
	return lastErr
}

// withImmediateTx wraps fn in a transaction, retrying on busy.
func withImmediateTx(db *sql.DB, fn func(*sql.Tx) error) error {
	return retryBusy(func() error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if err := fn(tx); err != nil {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	})
}

func ensureSchema(db *sql.DB) error {
	_, err := db.Exec("CREATE TABLE IF NOT EXISTS history_entries (id TEXT PRIMARY KEY, type TEXT, prompt TEXT, local_path TEXT, url TEXT, aspect TEXT, model TEXT, created_at TEXT)")
	return err
}

func migrateLegacyJSONIfNeeded(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	trim := bytes.TrimSpace(data)
	if len(trim) == 0 || trim[0] != '[' {
		// Not legacy JSON array — let sqlite handle it (preserves corrupt-case behavior).
		return nil
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil // preserve corrupt file behavior
	}
	backup := path + ".json.bak." + fmt.Sprintf("%d", time.Now().Unix())
	_ = os.Rename(path, backup)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	d, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer d.Close()
	d.SetMaxOpenConns(1)
	if _, err := d.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return err
	}
	if _, err := d.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return err
	}
	if err := ensureSchema(d); err != nil {
		return err
	}
	for _, e := range entries {
		if e.CreatedAt.IsZero() {
			e.CreatedAt = time.Now()
		}
		if _, err := d.Exec("INSERT OR REPLACE INTO history_entries (id, type, prompt, local_path, url, aspect, model, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			e.ID, e.Type, e.Prompt, e.LocalPath, e.URL, e.Aspect, e.Model, e.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	// Enforce cap after migration
	_, _ = d.Exec("DELETE FROM history_entries WHERE id NOT IN (SELECT id FROM history_entries ORDER BY created_at DESC, rowid DESC LIMIT 500)")
	return nil
}

// Add appends a new generation entry atomically.
func Add(entry Entry) error {
	d, err := getDB()
	if err != nil {
		return err
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	return withImmediateTx(d, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT OR REPLACE INTO history_entries (id, type, prompt, local_path, url, aspect, model, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			entry.ID, entry.Type, entry.Prompt, entry.LocalPath, entry.URL, entry.Aspect, entry.Model, entry.CreatedAt.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM history_entries WHERE id NOT IN (SELECT id FROM history_entries ORDER BY created_at DESC, rowid DESC LIMIT 500)")
		return err
	})
}

// List returns the most recent history entries up to limit.
func List(limit int) ([]Entry, error) {
	d, err := getDB()
	if err != nil {
		return nil, err
	}
	query := "SELECT id, type, prompt, local_path, url, aspect, model, created_at FROM history_entries ORDER BY created_at DESC, rowid DESC"
	if limit > 0 {
		query += " LIMIT ?"
		rows, err := d.Query(query, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanEntries(rows)
	}
	rows, err := d.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEntries(rows)
}

func scanEntries(rows *sql.Rows) ([]Entry, error) {
	var entries []Entry
	for rows.Next() {
		var e Entry
		var createdAtStr string
		if err := rows.Scan(&e.ID, &e.Type, &e.Prompt, &e.LocalPath, &e.URL, &e.Aspect, &e.Model, &createdAtStr); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
		if e.CreatedAt.IsZero() {
			e.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func getHistoryPathLocked() (string, error) {
	ov := pathOverride
	if ov != "" {
		if err := os.MkdirAll(filepath.Dir(ov), 0755); err != nil {
			return "", err
		}
		return ov, nil
	}
	if h := os.Getenv("GFLOW_HISTORY_PATH"); h != "" {
		if err := os.MkdirAll(filepath.Dir(h), 0755); err != nil {
			return "", err
		}
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".gflow")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "history.json"), nil
}
