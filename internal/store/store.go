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

	"lgo_download_manager/internal/protocol"

	_ "modernc.org/sqlite" // 纯 Go 实现的 sqlite 驱动
)

// Status 反映 Task 的生命周期状态。数据库中以纯文本存储。
type Status string

// 内部常量：禁止外部包直接引用，只能通过 TaskStatus 枚举类访问。
const (
	_pending     Status = "Pending"
	_downloading Status = "Downloading"
	_paused      Status = "Paused"
	_completed   Status = "Completed"
	_failed      Status = "Failed"
	_fileLost    Status = "FileLost"
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
	FilterAll         StatusFilter = "all"
	FilterPending     StatusFilter = "pending"
	FilterDownloading StatusFilter = "downloading"
	FilterPaused      StatusFilter = "paused"
	FilterCompleted   StatusFilter = "completed"
	FilterFailed      StatusFilter = "failed"
	FilterFileLost    StatusFilter = "filelost"
)

// filterStatus 把 StatusFilter 映射到对应的 DB Status 值。
// 返回的 bool 表示该筛选器是否需要在 SQL WHERE 中加 status 条件。
func (f StatusFilter) filterStatus() (Status, bool) {
	switch f {
	case FilterPending:
		return _pending, true
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
	case SortCreatedDesc:
		// 必须按 created_at 排序：曾经这里用的是 updated_at，导致
		// 默认视图会随着每 2 秒的进度刷盘不断重新排序（行跳动），
		// 也与「按添加时间倒序」的语义不符。
		return "ORDER BY created_at DESC, id ASC"
	case SortNameAsc:
		return "ORDER BY save_path ASC, id ASC"
	case SortNameDesc:
		return "ORDER BY save_path DESC, id ASC"
	default:
		return "ORDER BY created_at DESC, id ASC"
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
	AuthData      string    `json:"auth_data"` // JSON：{username,password,...}
	ErrorMessage  string    `json:"error_message"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CompletedAt   time.Time `json:"completed_at"` // 零值表示尚未完成
}

// Store 封装 *sql.DB，并缓存进度刷盘用的 prepared statement。
// 它是并发安全的；底层 sqlite 驱动在进程级别加锁，
// 因此写入通过 mu 串行化。
type Store struct {
	mu sync.Mutex
	db *sql.DB

	// 高频刷盘语句的缓存：UpdateTaskProgress 每 2 秒会被每个下载中的
	// 任务调用一次，缓存在此避免每次都让驱动重新编译 SQL。
	stmtProgress  *sql.Stmt
	stmtCompleted *sql.Stmt
}

// 进度刷盘语句（UpdateTaskProgress / UpdateTasksProgress 共用同一份 SQL：
// Open 会把它们预编译成 prepared statement，避免每次 Exec 重新编译）。
const (
	sqlProgress = `UPDATE tasks SET
		downloaded=?, chunk_progress=?, status=?, error_message=?, updated_at=?
		WHERE id=?`
	sqlProgressCompleted = `UPDATE tasks SET
		downloaded=?, chunk_progress=?, status=?, error_message=?, updated_at=?,
		completed_at=COALESCE(completed_at, ?)
		WHERE id=?`
)

// maxOpenConns 限制连接池大小。SQLite 只有单个写者，写入已由 mu 串行化；
// 读可以并发（WAL 下读不阻塞写），4 条连接足够覆盖 UI 列表 + 状态刷盘，
// 同时避免连接与 page cache 无上限增长。
const maxOpenConns = 4

// Open 打开 path 指定的数据库，必要时创建父目录。
// DSN 启用 WAL、synchronous=NORMAL（WAL 下的推荐组合：提交不做 fsync，
// 仅 checkpoint 时落盘）与 5 秒 busy_timeout。
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store mkdir: %w", err)
		}
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store open: %w", err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store ping: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store migrate: %w", err)
	}
	if s.stmtProgress, err = db.Prepare(sqlProgress); err != nil {
		db.Close()
		return nil, fmt.Errorf("store prepare progress: %w", err)
	}
	if s.stmtCompleted, err = db.Prepare(sqlProgressCompleted); err != nil {
		s.stmtProgress.Close()
		db.Close()
		return nil, fmt.Errorf("store prepare completed: %w", err)
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
		min_chunk_size  INTEGER NOT NULL DEFAULT 1048576,
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
	prealloc            INTEGER NOT NULL DEFAULT 1,
	max_concurrent      INTEGER NOT NULL DEFAULT 0
);
-- 索引与 ListTasks 的筛选/排序形状对齐（见 TaskSort.orderByClause）：
-- status 服务侧边栏筛选，另外两条让默认排序与文件名排序免于全表排序。
CREATE INDEX IF NOT EXISTS idx_tasks_status       ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_created      ON tasks(created_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS idx_tasks_save_path    ON tasks(save_path ASC, id ASC);
`
	if _, err := s.db.Exec(ddl); err != nil {
		return err
	}
	// 幂等的列迁移：向已存在的 tasks 表新增列，且不丢数据。
	if err := s.addColumnIfMissing("tasks", "min_chunk_size", "INTEGER NOT NULL DEFAULT 1048576"); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("tasks", "chunk_ranges", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("tasks", "completed_at", "DATETIME"); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("settings", "task_sort", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("settings", "proxy_url", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("settings", "proxy_bypass", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("settings", "proxy_mode", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("settings", "light_mode", `INTEGER NOT NULL DEFAULT 1`); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("settings", "max_concurrent", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("settings", "auto_start", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	return nil
}

// columnExists 报告 table 是否已有 col 列。
//
// 注意：必须在返回前关闭 rows。曾经这里直接在 rows 未关闭的情况下返回，
// 导致一条连接连同其读语句被永久 pin 住——后果是 WAL 无法 checkpoint
// （实测 WAL 增长到 27MB 且不回收）且 Store.Close() 之后数据库文件仍被
// 占用（Windows 上表现为文件无法删除）。
func (s *Store) columnExists(table, col string) (bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}

// addColumnIfMissing 是幂等的列迁移：列已存在时不做任何事。
// 先关闭探测用的 rows 再执行 ALTER，避免在同一连接上嵌套语句。
func (s *Store) addColumnIfMissing(table, col, decl string) error {
	found, err := s.columnExists(table, col)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", table, err)
	}
	if found {
		return nil
	}
	if _, err := s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + col + ` ` + decl); err != nil {
		return fmt.Errorf("alter %s add %s: %w", table, col, err)
	}
	return nil
}

// Close 释放数据库句柄。engine 在关闭时应通过 defer 调用。
func (s *Store) Close() error {
	if s.stmtProgress != nil {
		_ = s.stmtProgress.Close()
	}
	if s.stmtCompleted != nil {
		_ = s.stmtCompleted.Close()
	}
	return s.db.Close()
}

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
		is_allocated,chunk_count,min_chunk_size,chunk_progress,chunk_ranges,status,
		auth_data,error_message,created_at,updated_at,completed_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.URL, t.SavePath, t.Protocol, t.TotalSize, t.Downloaded,
		boolToInt(t.SupportRange), boolToInt(t.IsAllocated), t.ChunkCount,
		t.MinChunkSize,
		string(cpJSON), string(rgJSON), string(t.Status), t.AuthData,
		t.ErrorMessage, t.CreatedAt, t.UpdatedAt, nil,
	)
	if err != nil {
		return fmt.Errorf("store insert: %w", err)
	}
	return nil
}

// ProgressUpdate 是一次进度刷盘的内容。
type ProgressUpdate struct {
	ID            string
	Downloaded    int64
	ChunkProgress []int64
	Status        Status
	ErrMsg        string
}

// progressArgs 把一次进度更新编码为 SQL 参数。
func progressArgs(u ProgressUpdate, now time.Time) ([]any, error) {
	if u.ChunkProgress == nil {
		u.ChunkProgress = []int64{}
	}
	cpJSON, err := json.Marshal(u.ChunkProgress)
	if err != nil {
		return nil, fmt.Errorf("store marshal chunk progress: %w", err)
	}
	args := []any{u.Downloaded, string(cpJSON), string(u.Status), u.ErrMsg, now}
	if u.Status == _completed {
		args = append(args, now, u.ID)
	} else {
		args = append(args, u.ID)
	}
	return args, nil
}

// writeProgress 用 prepared statement 写一条进度（单条路径）。
func (s *Store) writeProgress(u ProgressUpdate, now time.Time) error {
	args, err := progressArgs(u, now)
	if err != nil {
		return err
	}
	var stmt *sql.Stmt
	if u.Status == _completed {
		stmt = s.stmtCompleted
	} else {
		stmt = s.stmtProgress
	}
	_, err = stmt.Exec(args...)
	return err
}

// UpdateTaskProgress 是 engine 使用的高频刷盘路径。仅更新易变列
// （downloaded、chunk_progress、status）。当状态变为 Completed 时，
// 自动写入 completed_at；其余情况保持原值。
func (s *Store) UpdateTaskProgress(id string, downloaded int64, chunkProgress []int64, status Status, errMsg string) error {
	u := ProgressUpdate{ID: id, Downloaded: downloaded, ChunkProgress: chunkProgress, Status: status, ErrMsg: errMsg}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeProgress(u, time.Now().UTC())
}

// UpdateTasksProgress 在一个事务里批量落盘多条进度。
// scheduler 的 2 秒 tick 用它把本周期所有 dirty 任务合并为一次提交，
// 取代逐任务一次 Exec/一次提交的写法。
func (s *Store) UpdateTasksProgress(updates []ProgressUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store begin: %w", err)
	}
	now := time.Now().UTC()
	for _, u := range updates {
		args, aerr := progressArgs(u, now)
		if aerr != nil {
			_ = tx.Rollback()
			return aerr
		}
		query := sqlProgress
		if u.Status == _completed {
			query = sqlProgressCompleted
		}
		if _, err := tx.Exec(query, args...); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
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
		is_allocated,chunk_count,min_chunk_size,chunk_progress,chunk_ranges,status,
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
		is_allocated,chunk_count,min_chunk_size,chunk_progress,chunk_ranges,status,
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
		&support, &alloc, &t.ChunkCount, &t.MinChunkSize, &cp, &rg, &t.Status, &t.AuthData,
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

type Settings struct {
	DefaultSaveDir string
	DefaultThreads int
	MinChunkSize   int64
	UserAgent      string
	Cookies        string
	FTPPassive     bool
	Prealloc       bool
	TaskSort       TaskSort // 任务列表排序方式
	ProxyMode      protocol.ProxyMode

	// ProxyURL 是 Manual 模式下使用的代理地址；其他模式忽略。
	ProxyURL    string
	ProxyBypass string

	// LightMode 为 true 时，关闭主窗口会同时销毁窗口内的 widget 树，
	// 释放任务行、磁盘条等占用的内存，仅保留调度器和系统托盘。
	// 重新打开主窗口时按需重建。
	LightMode bool

	// MaxConcurrent 是同时运行的最大下载任务数。<=0 表示使用默认值
	// (DefaultMaxConcurrent)。设置被改小不会自动暂停正在运行的下载,
	// 但超出限额后新加入的任务会保持 Pending 直到有 slot 释放。
	MaxConcurrent int

	// AutoStart 控制操作系统登录后是否自动启动业务主进程。
	// 启用时,业务进程会以静默(隐藏主窗口)方式拉起,
	// 只保留调度器与系统托盘;用户在托盘菜单「显示窗口」恢复 UI。
	// 持久化与实际注册表/.desktop/LaunchAgent 是否一致由
	// settings.ApplyAutoStart 在 Save/启动时调和。
	AutoStart bool
}

// DefaultMaxConcurrent 是 MaxConcurrent 为 0/负数时的兜底默认值;
// 同时也是首次运行 / 列缺省时落盘的初始值。设 3 是为了与主流下载器
// (aria2c 默认 5、IDM 默认 4) 的轻量级场景对齐。
const DefaultMaxConcurrent = 3

// EffectiveMaxConcurrent 返回 MaxConcurrent 的有效值:<=0 视为默认。
func (s Settings) EffectiveMaxConcurrent() int {
	if s.MaxConcurrent <= 0 {
		return DefaultMaxConcurrent
	}
	return s.MaxConcurrent
}

// 则插入一条全零的记录（由调用方负责套用默认值），
// 并返回零值的 Settings。
func (s *Store) LoadSettings() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		saveDir     string
		threads     int
		minChunk    int64
		ua          string
		cookies     string
		passive     int
		prealloc    int
		taskSort    string
		proxyMode   string
		proxyURL    string
		proxyBypass string
		lightMode   int
		maxConc     int
		autoStart   int
	)
	err := s.db.QueryRow(`SELECT default_save_dir, default_threads, min_chunk_size,
		user_agent, cookies, ftp_passive, prealloc, task_sort,
		COALESCE(proxy_mode, ''), COALESCE(proxy_url, ''), COALESCE(proxy_bypass, ''),
		COALESCE(light_mode, 1),
		COALESCE(max_concurrent, 0),
		COALESCE(auto_start, 0)
		FROM settings WHERE id=1`,
	).Scan(&saveDir, &threads, &minChunk, &ua, &cookies, &passive, &prealloc, &taskSort,
		&proxyMode, &proxyURL, &proxyBypass, &lightMode, &maxConc, &autoStart)
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
	// 兼容升级：未持久化 proxy_mode 时根据 proxy_url 是否非空推断。
	// - 空 URL ⇒ System（默认，与"未启用"语义一致）
	// - 非空 URL ⇒ Manual（旧版的"启用 HTTP 代理"）
	mode := protocol.ParseProxyMode(proxyMode)
	if proxyMode == "" {
		if proxyURL != "" {
			mode = protocol.ProxyModeManual
		} else {
			mode = protocol.ProxyModeSystem
		}
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
		ProxyMode:      mode,
		// light_mode 列缺省值为 1（轻量模式为新用户的默认）；
		// 老数据库在迁移后会得到这个缺省，无需特别处理。
		LightMode: lightMode != 0,
		// max_concurrent 列缺省值为 0(<DefaultMaxConcurrent 视为未配置);
		// 调用方 (settings.Load) 会套用首次运行默认值。
		MaxConcurrent: maxConc,
		// auto_start 列缺省值为 0:首次运行不自动开机自启,符合
		// 「默认不打扰用户」原则;用户在「设置」里显式开启才会注册。
		AutoStart: autoStart != 0,
	}, nil
}

// SaveSettings upsert 已持久化的 settings 行。
func (s *Store) SaveSettings(s2 Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO settings (id, default_save_dir, default_threads,
		min_chunk_size, user_agent, cookies, ftp_passive, prealloc, task_sort,
		proxy_mode, proxy_url, proxy_bypass, light_mode, max_concurrent, auto_start)
	VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
			default_save_dir=excluded.default_save_dir,
			default_threads=excluded.default_threads,
			min_chunk_size=excluded.min_chunk_size,
			user_agent=excluded.user_agent,
			cookies=excluded.cookies,
			ftp_passive=excluded.ftp_passive,
			prealloc=excluded.prealloc,
			task_sort=excluded.task_sort,
			proxy_mode=excluded.proxy_mode,
			proxy_url=excluded.proxy_url,
			proxy_bypass=excluded.proxy_bypass,
			light_mode=excluded.light_mode,
			max_concurrent=excluded.max_concurrent,
			auto_start=excluded.auto_start`,
		s2.DefaultSaveDir, s2.DefaultThreads, s2.MinChunkSize,
		s2.UserAgent, s2.Cookies, boolToInt(s2.FTPPassive), boolToInt(s2.Prealloc),
		string(s2.TaskSort),
		string(protocol.ProxyMode(s2.ProxyMode).String()), s2.ProxyURL, s2.ProxyBypass,
		boolToInt(s2.LightMode),
		s2.MaxConcurrent,
		boolToInt(s2.AutoStart),
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
