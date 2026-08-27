// Package store is the SQLite persistence layer for download tasks.
//
// Schema (matches design doc section IV):
//
//	id              TEXT PK
//	url             TEXT
//	save_path       TEXT
//	protocol        TEXT (HTTP/HTTPS/FTP/WEBDAV)
//	total_size      INTEGER
//	downloaded      INTEGER
//	support_range   INTEGER (0/1)
//	is_allocated    INTEGER (0/1)
//	chunk_count     INTEGER
//	chunk_progress  TEXT  JSON array
//	status          TEXT  Pending/Downloading/Paused/Completed/Failed
//	auth_data       TEXT  JSON (kept plaintext for now; see comment)
//	error_message   TEXT
//	created_at      DATETIME
//	updated_at      DATETIME
//
// All writes go through (*Store).Flush: a Ticker in the engine calls
// Flush every 2 seconds. Lifecycle events (pause/complete/fail) call
// FlushImmediate to force a sync.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go sqlite driver
)

// Status reflects the lifecycle of a Task. The DB column is plain text.
type Status string

const (
	StatusPending    Status = "Pending"
	StatusDownloading Status = "Downloading"
	StatusPaused     Status = "Paused"
	StatusCompleted  Status = "Completed"
	StatusFailed     Status = "Failed"
)

// Task is the in-memory representation of a row. JSON-friendly.
type Task struct {
	ID            string    `json:"id"`
	URL           string    `json:"url"`
	SavePath      string    `json:"save_path"`
	Protocol      string    `json:"protocol"`
	TotalSize     int64     `json:"total_size"`
	Downloaded    int64     `json:"downloaded"`
	SupportRange  bool      `json:"support_range"`
	IsAllocated   bool      `json:"is_allocated"`
	ChunkCount    int       `json:"chunk_count"`
	ChunkProgress []int64   `json:"chunk_progress"` // per-chunk offset-from-start
	Status        Status    `json:"status"`
	AuthData      string    `json:"auth_data"`  // JSON: {username,password,...}
	ErrorMessage  string    `json:"error_message"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Store wraps an *sql.DB with prepared statements used by the engine.
// It is safe for concurrent use; the underlying sqlite driver is locked
// at the process level so writes are serialized through mu.
type Store struct {
	mu sync.Mutex
	db *sql.DB
}

// Open opens the database at path, creating the parent directory if
// needed. The provided DSN enables WAL + a busy timeout for safety
// against concurrent connections (the engine may have more than one
// writer if the user starts/stops tasks rapidly).
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store mkdir: %w", err)
		}
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store open: %w", err)
	}
	// Pragmas for safety: foreign keys off (none here), synchronous=NORMAL,
	// and a 5s busy timeout. modernc.org/sqlite sets these via DSN already.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store ping: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store migrate: %w", err)
	}
	return s, nil
}

// migrate creates the tasks table on a fresh DB. Idempotent.
func (s *Store) migrate() error {
	const ddl = `CREATE TABLE IF NOT EXISTS tasks (
		id              TEXT PRIMARY KEY,
		url             TEXT NOT NULL,
		save_path       TEXT NOT NULL,
		protocol        TEXT NOT NULL,
		total_size      INTEGER NOT NULL DEFAULT -1,
		downloaded      INTEGER NOT NULL DEFAULT 0,
		support_range   INTEGER NOT NULL DEFAULT 0,
		is_allocated    INTEGER NOT NULL DEFAULT 0,
		chunk_count     INTEGER NOT NULL DEFAULT 4,
		chunk_progress  TEXT NOT NULL DEFAULT '[]',
		status          TEXT NOT NULL DEFAULT 'Pending',
		auth_data       TEXT NOT NULL DEFAULT '{}',
		error_message   TEXT NOT NULL DEFAULT '',
		created_at      DATETIME NOT NULL,
		updated_at      DATETIME NOT NULL
	)`
	_, err := s.db.Exec(ddl)
	return err
}

// Close releases the DB handle. The engine should defer this on shutdown.
func (s *Store) Close() error { return s.db.Close() }

// CreateTask inserts a new task row. It returns the inserted Task (with
// CreatedAt/UpdatedAt populated) or an error.
func (s *Store) CreateTask(t *Task) error {
	if t.ID == "" {
		return errors.New("store: empty task id")
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	t.UpdatedAt = time.Now().UTC()
	if t.ChunkProgress == nil {
		t.ChunkProgress = []int64{}
	}
	cpJSON, _ := json.Marshal(t.ChunkProgress)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO tasks (
		id,url,save_path,protocol,total_size,downloaded,support_range,
		is_allocated,chunk_count,chunk_progress,status,auth_data,
		error_message,created_at,updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.URL, t.SavePath, t.Protocol, t.TotalSize, t.Downloaded,
		boolToInt(t.SupportRange), boolToInt(t.IsAllocated), t.ChunkCount,
		string(cpJSON), string(t.Status), t.AuthData, t.ErrorMessage,
		t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store insert: %w", err)
	}
	return nil
}

// UpdateTaskProgress is the hot-path flush used by the engine. It only
// touches the volatile columns (downloaded, chunk_progress, status).
func (s *Store) UpdateTaskProgress(id string, downloaded int64, chunkProgress []int64, status Status, errMsg string) error {
	if chunkProgress == nil {
		chunkProgress = []int64{}
	}
	cpJSON, _ := json.Marshal(chunkProgress)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE tasks SET
		downloaded=?, chunk_progress=?, status=?, error_message=?, updated_at=?
		WHERE id=?`,
		downloaded, string(cpJSON), string(status), errMsg, time.Now().UTC(), id)
	return err
}

// UpdateTaskMeta updates non-volatile fields after Probe completes.
func (s *Store) UpdateTaskMeta(id string, totalSize int64, supportRange, isAllocated bool, chunkCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE tasks SET
		total_size=?, support_range=?, is_allocated=?, chunk_count=?, updated_at=?
		WHERE id=?`,
		totalSize, boolToInt(supportRange), boolToInt(isAllocated), chunkCount,
		time.Now().UTC(), id)
	return err
}

// GetTask fetches a task by ID. Returns sql.ErrNoRows if not present.
func (s *Store) GetTask(id string) (*Task, error) {
	row := s.db.QueryRow(`SELECT
		id,url,save_path,protocol,total_size,downloaded,support_range,
		is_allocated,chunk_count,chunk_progress,status,auth_data,
		error_message,created_at,updated_at
		FROM tasks WHERE id=?`, id)
	return scanTask(row)
}

// ListTasks returns all tasks ordered by updated_at desc.
func (s *Store) ListTasks() ([]*Task, error) {
	rows, err := s.db.Query(`SELECT
		id,url,save_path,protocol,total_size,downloaded,support_range,
		is_allocated,chunk_count,chunk_progress,status,auth_data,
		error_message,created_at,updated_at
		FROM tasks ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("store list: %w", err)
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
// Clone deep-copies the task. Used when handing a Task to a UI consumer
// that might mutate it without affecting the running engine's view.
func (t *Task) Clone() *Task {
	if t == nil {
		return nil
	}
	cp := *t
	if t.ChunkProgress != nil {
		cp.ChunkProgress = append([]int64(nil), t.ChunkProgress...)
	}
	return &cp
}


// DeleteTask removes a task by ID.
func (s *Store) DeleteTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM tasks WHERE id=?`, id)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner) (*Task, error) {
	var (
		t          Task
		cp         string
		support    int
		alloc      int
		total, dwn int64
	)
	err := s.Scan(&t.ID, &t.URL, &t.SavePath, &t.Protocol, &total, &dwn,
		&support, &alloc, &t.ChunkCount, &cp, &t.Status, &t.AuthData,
		&t.ErrorMessage, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	t.TotalSize = total
	t.Downloaded = dwn
	t.SupportRange = support != 0
	t.IsAllocated = alloc != 0
	t.ChunkProgress = []int64{}
	if cp != "" {
		_ = json.Unmarshal([]byte(cp), &t.ChunkProgress)
	}
	return &t, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
