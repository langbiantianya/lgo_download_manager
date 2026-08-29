// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package store 是下载任务的 SQLite 持久化层。
//
// 表结构（对应设计文档 IV 节）：
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
//	chunk_progress  TEXT  JSON 数组
//	status          TEXT  Pending/Downloading/Paused/Completed/Failed
//	auth_data       TEXT  JSON (目前以明文保存，详见代码内注释)
//	error_message   TEXT
//	created_at      DATETIME
//	updated_at      DATETIME
//
// 所有写入都通过 (*Store).Flush 完成：engine 中的 Ticker 每 2 秒调用
// Flush 一次。生命周期事件（暂停/完成/失败）通过 FlushImmediate 强制同步。
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

	_ "modernc.org/sqlite" // 纯 Go 实现的 sqlite 驱动
)

// Status 反映 Task 的生命周期状态。数据库中以纯文本存储。
type Status string

// 内部常量：禁止外部包直接引用，只能通过 TaskStatus 枚举类访问。
const (
	_pending      Status = "Pending"
	_downloading Status = "Downloading"
	_paused       Status = "Paused"
	_completed    Status = "Completed"
	_failed       Status = "Failed"
	_fileLost     Status = "FileLost"
)

// TaskStatus 是状态枚举类，提供所有合法的 Status 值。
// 外部包必须通过 TaskStatus 访问，禁止引用内部 _pending 等常量。
var TaskStatus = struct {
	Pending     Status
	Downloading Status
	Paused      Status
	Completed   Status
	Failed      Status
	FileLost    Status
}{
	Pending:     _pending,
	Downloading: _downloading,
	Paused:      _paused,
	Completed:   _completed,
	Failed:      _failed,
	FileLost:    _fileLost,
}

// String 返回状态的字符串表示。
func (s Status) String() string { return string(s) }

// IsTerminal 表示该状态为终态（已完成/失败/文件丢失）。
func (s Status) IsTerminal() bool { return s == _completed || s == _failed || s == _fileLost }

// IsActive 表示该状态为活跃状态（等待中/下载中）。
func (s Status) IsActive() bool { return s == _pending || s == _downloading }

// StatusFilter 用于 UI 侧边栏筛选任务。
type StatusFilter string

const (
	FilterAll        StatusFilter = "all"
	FilterDownloading StatusFilter = "downloading"
	FilterPaused    StatusFilter = "paused"
	FilterCompleted StatusFilter = "completed"
	FilterFailed   StatusFilter = "failed"
	FilterFileLost  StatusFilter = "filelost"
)
// filterStatus 把 StatusFilter 映射到对应的 DB Status 值。
// 返回的 bool 表示该筛选器是否需要在 SQL WHERE 中加 status 条件。
func (f StatusFilter) filterStatus() (Status, bool) {
	switch f {
	case FilterDownloading:
		return _downloading, true
	case FilterPaused:
		return _paused, true
	case FilterCompleted:
		return _completed, true
	case FilterFailed:
		return _failed, true
	case FilterFileLost:
		return _fileLost, true
	default:
		return "", false
	}
}

func (f StatusFilter) String() string { return string(f) }

// TaskSort 是任务列表的排序方式枚举类。
type TaskSort string

const (
	SortCreatedDesc TaskSort = "created_desc" // 按添加时间倒序（默认，最新的在前）
	SortCreatedAsc  TaskSort = "created_asc"  // 按添加时间正序（最老的在前）
	SortNameAsc     TaskSort = "name_asc"     // 按文件名正序（A-Z）
	SortNameDesc    TaskSort = "name_desc"    // 按文件名倒序（Z-A）
)

// AllTaskSorts 列出所有可选排序选项，用于 UI 渲染。
func AllTaskSorts() []TaskSort {
	return []TaskSort{SortCreatedDesc, SortCreatedAsc, SortNameAsc, SortNameDesc}
}

// orderByClause 把 TaskSort 翻译成 SQL ORDER BY 子句。
func (s TaskSort) orderByClause() string {
	switch s {
	case SortCreatedAsc:
		return "ORDER BY created_at ASC, id ASC"
	case SortNameAsc:
		return "ORDER BY save_path ASC, id ASC"
	case SortNameDesc:
		return "ORDER BY save_path DESC, id ASC"
	default:
		return "ORDER BY updated_at DESC, id ASC"
	}
}

func (s TaskSort) String() string { return string(s) }

// Task 是数据库行的内存表示，便于 JSON 序列化。
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
	MinChunkSize  int64     `json:"min_chunk_size"` // engine 分块下界（字节）
	ChunkProgress []int64   `json:"chunk_progress"` // 每个分块从起点开始的偏移量
	ChunkRanges   []int64   `json:"chunk_ranges"`   // [start0,end0,start1,end1,...] — 实际的 engine 分块布局
	Status        Status    `json:"status"`
	AuthData      string    `json:"auth_data"`  // JSON：{username,password,...}
	ErrorMessage  string    `json:"error_message"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CompletedAt   time.Time `json:"completed_at"` // 零值表示尚未完成
}

// Store 用 engine 使用的 prepared 语句封装 *sql.DB。
// 它是并发安全的；底层 sqlite 驱动在进程级别加锁，
// 因此写入通过 mu 串行化。
type Store struct {
	mu sync.Mutex
	db *sql.DB
}

// Open 打开 path 指定的数据库，必要时创建父目录。
// 所使用的 DSN 启用 WAL 与 busy_timeout，以应对并发连接
//（当用户频繁启停任务时，engine 可能有多个 writer）。
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
	// 出于安全考虑的 PRAGMA：关闭外键（此处未使用），synchronous=NORMAL，
	// 以及 5 秒的 busy_timeout。modernc.org/sqlite 已经通过 DSN 设置这些。
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

// migrate 在全新数据库上创建表，并对旧数据库应用幂等的列迁移。
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
	);
	CREATE TABLE IF NOT EXISTS settings (
		id              INTEGER PRIMARY KEY CHECK (id = 1),
		default_save_dir    TEXT NOT NULL DEFAULT '',
		default_threads     INTEGER NOT NULL DEFAULT 4,
		min_chunk_size      INTEGER NOT NULL DEFAULT 1048576,
		user_agent          TEXT NOT NULL DEFAULT '',
		cookies             TEXT NOT NULL DEFAULT '',
		ftp_passive         INTEGER NOT NULL DEFAULT 1,
		prealloc            INTEGER NOT NULL DEFAULT 1
	);`
	if _, err := s.db.Exec(ddl); err != nil {
		return err
	}
	// 幂等的列迁移：向已存在的 tasks 表新增列，且不丢数据。
	if err := s.addColumnIfMissing("tasks", "chunk_ranges", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("tasks", "completed_at", "DATETIME"); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("settings", "task_sort", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	return nil
}
// modernc.org/sqlite 通过 PRAGMA 暴露 table_info。
func (s *Store) addColumnIfMissing(table, col, decl string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == col {
			return nil
		}
	}
	if _, err := s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + col + ` ` + decl); err != nil {
		return fmt.Errorf("alter %s add %s: %w", table, col, err)
	}
	return nil
}

// Close 释放数据库句柄。engine 在关闭时应通过 defer 调用。
func (s *Store) Close() error { return s.db.Close() }

// CreateTask 插入一条新的任务记录。返回插入后的 Task（CreatedAt/UpdatedAt 已填充）或错误。
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
	if t.ChunkRanges == nil {
		t.ChunkRanges = []int64{}
	}
	cpJSON, _ := json.Marshal(t.ChunkProgress)
	rgJSON, _ := json.Marshal(t.ChunkRanges)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO tasks (
		id,url,save_path,protocol,total_size,downloaded,support_range,
		is_allocated,chunk_count,chunk_progress,chunk_ranges,status,
		auth_data,error_message,created_at,updated_at,completed_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.URL, t.SavePath, t.Protocol, t.TotalSize, t.Downloaded,
		boolToInt(t.SupportRange), boolToInt(t.IsAllocated), t.ChunkCount,
		string(cpJSON), string(rgJSON), string(t.Status), t.AuthData,
		t.ErrorMessage, t.CreatedAt, t.UpdatedAt, nil,
	)
	if err != nil {
		return fmt.Errorf("store insert: %w", err)
	}
	return nil
}

// UpdateTaskProgress 是 engine 使用的高频刷盘路径。仅更新易变列
// （downloaded、chunk_progress、status）。当状态变为 Completed 时，
// 自动写入 completed_at；其余情况保持原值。
func (s *Store) UpdateTaskProgress(id string, downloaded int64, chunkProgress []int64, status Status, errMsg string) error {
	if chunkProgress == nil {
		chunkProgress = []int64{}
	}
	cpJSON, _ := json.Marshal(chunkProgress)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if status == _completed {
		_, err := s.db.Exec(`UPDATE tasks SET
			downloaded=?, chunk_progress=?, status=?, error_message=?, updated_at=?,
			completed_at=COALESCE(completed_at, ?)
		WHERE id=?`,
			downloaded, string(cpJSON), string(status), errMsg, now, now, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE tasks SET
		downloaded=?, chunk_progress=?, status=?, error_message=?, updated_at=?
		WHERE id=?`,
		downloaded, string(cpJSON), string(status), errMsg, now, id)
	return err
}
// UpdateTaskMeta 在 Probe 完成后更新非易变字段。
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

// GetTask 按 ID 获取任务。若不存在则返回 sql.ErrNoRows。
func (s *Store) GetTask(id string) (*Task, error) {
	row := s.db.QueryRow(`SELECT
		id,url,save_path,protocol,total_size,downloaded,support_range,
		is_allocated,chunk_count,chunk_progress,chunk_ranges,status,
		auth_data,error_message,created_at,updated_at,completed_at
		FROM tasks WHERE id=?`, id)
	return scanTask(row)
}

// ListTasks 返回任务列表，按 sort 指定的排序方式在 SQL 层 ORDER BY。
// filter 控制 SQL 过滤：FilterAll 返回所有任务，其它值通过
// WHERE status = ? 在 DB 层过滤，避免把不匹配的行拉回内存。
func (s *Store) ListTasks(filter StatusFilter, sort TaskSort) ([]*Task, error) {
	statusVal, useWhere := filter.filterStatus()
	query := `SELECT
		id,url,save_path,protocol,total_size,downloaded,support_range,
		is_allocated,chunk_count,chunk_progress,chunk_ranges,status,
		auth_data,error_message,created_at,updated_at,completed_at
		FROM tasks`
	if useWhere {
		query += ` WHERE status = ?`
	}
	query += ` ` + sort.orderByClause()

	var (
		rows *sql.Rows
		err  error
	)
	if useWhere {
		rows, err = s.db.Query(query, string(statusVal))
	} else {
		rows, err = s.db.Query(query)
	}
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

// Clone 深拷贝任务。用于把 Task 交给可能就地修改它的 UI 消费者，
// 避免影响 engine 当前持有的视图。
func (t *Task) Clone() *Task {
	if t == nil {
		return nil
	}
	cp := *t
	if t.ChunkProgress != nil {
		cp.ChunkProgress = append([]int64(nil), t.ChunkProgress...)
	}
	if t.ChunkRanges != nil {
		cp.ChunkRanges = append([]int64(nil), t.ChunkRanges...)
	}
	return &cp
}

// DeleteTask 按 ID 删除任务。
func (s *Store) DeleteTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM tasks WHERE id=?`, id)
	return err
}

// UpdateTaskChunkRanges 记录 engine 为某个任务实际规划的分块布局。
// 每次 Start() 调用一次，位于 engine 完成分块规划之后
// （参见 engine.Options.ChunkCount + MinChunkSize）。持久化该布局后，
// UI 即可按真实的字节区间渲染分块详情，而不是按线程布局去猜测。
func (s *Store) UpdateTaskChunkRanges(id string, ranges []int64) error {
	if ranges == nil {
		ranges = []int64{}
	}
	rgJSON, _ := json.Marshal(ranges)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE tasks SET chunk_ranges=?, updated_at=? WHERE id=?`,
		string(rgJSON), time.Now().UTC(), id)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner) (*Task, error) {
	var (
		t             Task
		cp, rg        string
		support       int
		alloc         int
		total, dwn    int64
		completedAtNS sql.NullTime
	)
	err := s.Scan(&t.ID, &t.URL, &t.SavePath, &t.Protocol, &total, &dwn,
		&support, &alloc, &t.ChunkCount, &cp, &rg, &t.Status, &t.AuthData,
		&t.ErrorMessage, &t.CreatedAt, &t.UpdatedAt, &completedAtNS)
	if err != nil {
		return nil, err
	}
	if completedAtNS.Valid {
		t.CompletedAt = completedAtNS.Time
	}
	t.TotalSize = total
	t.Downloaded = dwn
	t.SupportRange = support != 0
	t.IsAllocated = alloc != 0
	t.ChunkProgress = []int64{}
	if cp != "" {
		_ = json.Unmarshal([]byte(cp), &t.ChunkProgress)
	}
	t.ChunkRanges = []int64{}
	if rg != "" {
		_ = json.Unmarshal([]byte(rg), &t.ChunkRanges)
	}
	return &t, nil
}

// Settings 是持久化的用户可调默认配置。以单行形式保存在 settings 表中
// （id = 1）。零值是合法的（首次加载时由表的 DEFAULT 覆盖）。
type Settings struct {
	DefaultSaveDir string
	DefaultThreads int
	MinChunkSize   int64
	UserAgent      string
	Cookies        string
	FTPPassive     bool
	Prealloc       bool
	TaskSort       TaskSort // 任务列表排序方式
}

// LoadSettings 返回已持久化的 settings 行。若尚不存在，
// 则插入一条全零的记录（由调用方负责套用默认值），
// 并返回零值的 Settings。
func (s *Store) LoadSettings() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		saveDir  string
		threads  int
		minChunk int64
		ua       string
		cookies  string
		passive  int
		prealloc int
		taskSort string
	)
	err := s.db.QueryRow(`SELECT default_save_dir, default_threads, min_chunk_size,
		user_agent, cookies, ftp_passive, prealloc, task_sort FROM settings WHERE id=1`,
	).Scan(&saveDir, &threads, &minChunk, &ua, &cookies, &passive, &prealloc, &taskSort)
	if errors.Is(err, sql.ErrNoRows) {
		// 首次运行：插入一条全零行，以便后续 LoadSettings 能读到。
		_, ierr := s.db.Exec(`INSERT OR IGNORE INTO settings (id) VALUES (1)`)
		if ierr != nil {
			return Settings{}, fmt.Errorf("settings init: %w", ierr)
		}
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("settings load: %w", err)
	}
	return Settings{
		DefaultSaveDir: saveDir,
		DefaultThreads: threads,
		MinChunkSize:   minChunk,
		UserAgent:      ua,
		Cookies:        cookies,
		FTPPassive:     passive != 0,
		Prealloc:       prealloc != 0,
		TaskSort:       TaskSort(taskSort),
	}, nil
}

// SaveSettings upsert 已持久化的 settings 行。
func (s *Store) SaveSettings(s2 Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO settings (id, default_save_dir, default_threads,
		min_chunk_size, user_agent, cookies, ftp_passive, prealloc, task_sort)
	VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
			default_save_dir=excluded.default_save_dir,
			default_threads=excluded.default_threads,
			min_chunk_size=excluded.min_chunk_size,
			user_agent=excluded.user_agent,
			cookies=excluded.cookies,
			ftp_passive=excluded.ftp_passive,
			prealloc=excluded.prealloc,
			task_sort=excluded.task_sort`,
		s2.DefaultSaveDir, s2.DefaultThreads, s2.MinChunkSize,
		s2.UserAgent, s2.Cookies, boolToInt(s2.FTPPassive), boolToInt(s2.Prealloc),
		string(s2.TaskSort),
	)
	if err != nil {
		return fmt.Errorf("settings save: %w", err)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
