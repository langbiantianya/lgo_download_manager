// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package api

import "time"

// Task 是任务的对外视图，字段与 internal/store.Task 一一对应。
type Task struct {
	ID              string    `json:"id"`
	URL             string    `json:"url"`
	SavePath        string    `json:"save_path"`
	Protocol        string    `json:"protocol"`
	TotalSize       int64     `json:"total_size"`
	Downloaded      int64     `json:"downloaded"`
	SupportRange    bool      `json:"support_range"`
	ETag            string    `json:"etag"`
	LastModified    string    `json:"last_modified"`
	DigestAlgorithm string    `json:"digest_algorithm,omitempty"`
	Digest          string    `json:"digest,omitempty"`
	IsAllocated     bool      `json:"is_allocated"`
	ChunkCount      int       `json:"chunk_count"`
	MinChunkSize    int64     `json:"min_chunk_size"`
	ChunkProgress   []int64   `json:"chunk_progress"`
	ChunkRanges     []int64   `json:"chunk_ranges"`
	Status          Status    `json:"status"`
	AuthData        string    `json:"auth_data"`
	ErrorMessage    string    `json:"error_message"`
	ErrorKind       string    `json:"error_kind,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	CompletedAt     time.Time `json:"completed_at"`
	BTFile          string    `json:"bt_file,omitempty"`
}

// Settings 是权威设置快照，字段与 internal/store.Settings 一一对应。
type Settings struct {
	DefaultSaveDir     string    `json:"default_save_dir"`
	DefaultThreads     int       `json:"default_threads"`
	MinChunkSize       int64     `json:"min_chunk_size"`
	UserAgent          string    `json:"user_agent"`
	Cookies            string    `json:"cookies"`
	FTPPassive         bool      `json:"ftp_passive"`
	Prealloc           bool      `json:"prealloc"`
	TaskSort           TaskSort  `json:"task_sort"`
	ProxyMode          ProxyMode `json:"proxy_mode"`
	ProxyURL           string    `json:"proxy_url"`
	ProxyBypass        string    `json:"proxy_bypass"`
	ProxyAutoConfigURL string    `json:"proxy_auto_config_url"`
	DownloadLimitKiB   int       `json:"download_limit_kib"`
	LightMode          bool      `json:"light_mode"`
	MaxConcurrent      int       `json:"max_concurrent"`
	AutoStart          bool      `json:"auto_start"`
	Language           string    `json:"language"`
	BTPort             int       `json:"bt_port"`
	BTMaxPeers         int       `json:"bt_max_peers"`
	BTDownloadLimitKiB int       `json:"bt_download_limit_kib"`
	BTUploadLimitKiB   int       `json:"bt_upload_limit_kib"`
	BTDHT              bool      `json:"bt_dht"`
	BTPEX              bool      `json:"bt_pex"`
	BTSeedRatio        float64   `json:"bt_seed_ratio"`
	BTMaxSeeding       int       `json:"bt_max_seeding"`
	BTMaxConcurrent    int       `json:"bt_max_concurrent"`
	BTTrackers         string    `json:"bt_trackers"`
}

// AuthOptions 是随任务一起传递的凭据。字段与 internal/protocol.AuthOptions
// 对齐；ProxyMode 在契约层是字符串，见 vocab.go。
type AuthOptions struct {
	Username           string    `json:"username"`
	Password           string    `json:"password"`
	UserAgent          string    `json:"user_agent"`
	Cookies            string    `json:"cookies"`
	Referer            string    `json:"referer"`
	FTPPassive         bool      `json:"ftp_passive"`
	ProxyMode          ProxyMode `json:"proxy_mode"`
	ProxyURL           string    `json:"proxy_url"`
	ProxyBypass        string    `json:"proxy_bypass"`
	ProxyAutoConfigURL string    `json:"proxy_auto_config_url"`
}

// Event 是一条任务事件，语义与 internal/scheduler.Event 一致。
type Event struct {
	Why      string  `json:"why"`
	Task     *Task   `json:"task"`
	SpeedBPS float64 `json:"speed_bps"`
}

// FileProgress 是种子内单个文件的进度。
type FileProgress struct {
	Path      string `json:"path"`
	Length    int64  `json:"length"`
	Completed int64  `json:"completed"`
}

// BTDetail 是 BT 任务的实时详情快照。字段与 internal/bittorrent.Detail 对齐。
type BTDetail struct {
	Name         string         `json:"name"`
	InfoHash     string         `json:"info_hash"`
	Total        int64          `json:"total"`
	Downloaded   int64          `json:"downloaded"`
	Uploaded     int64          `json:"uploaded"`
	SpeedBPS     float64        `json:"speed_bps"`
	UploadBPS    float64        `json:"upload_bps"`
	Peers        int            `json:"peers"`
	TotalPeers   int            `json:"total_peers"`
	Seeders      int            `json:"seeders"`
	Leechers     int            `json:"leechers"`
	PieceLength  int64          `json:"piece_length"`
	Pieces       int            `json:"pieces"`
	PiecesDone   int            `json:"pieces_done"`
	PieceBuckets []uint8        `json:"piece_buckets"`
	Seeding      bool           `json:"seeding"`
	Complete     bool           `json:"complete"`
	Files        []FileProgress `json:"files"`
	Trackers     []string       `json:"trackers"`
}

// AddTaskInput 描述一次「添加下载任务」。
type AddTaskInput struct {
	URL          string       `json:"url"`
	SavePath     string       `json:"save_path"`
	Protocol     ProtocolKind `json:"protocol"`
	Auth         AuthOptions  `json:"auth"`
	ChunkCount   int          `json:"chunk_count"`
	MinChunkSize int64        `json:"min_chunk_size"`
}

// ListParams 是列表查询参数，语义与 store.ListTasks 的筛选一致。
type ListParams struct {
	Filter StatusFilter `json:"filter"`
	Sort   TaskSort     `json:"sort"`
}

// ListResult 是列表查询的结果。
type ListResult struct {
	Tasks []*Task `json:"tasks"`
}

// TaskRef 引用单个任务。
type TaskRef struct {
	ID string `json:"id"`
}

// ProbeParams 是体积探测的请求。
type ProbeParams struct {
	URL string `json:"url"`
}

// ProbeResult 是体积探测的结果。
type ProbeResult struct {
	TotalSize int64  `json:"total_size"`
	Filename  string `json:"filename,omitempty"`
}

// BoolResult 是布尔结果。
type BoolResult struct {
	Value bool `json:"value"`
}

// BTInfoParams 是 BT 详情查询的请求。
type BTInfoParams struct {
	TaskID string `json:"task_id"`
}

// Empty 是无负载请求/响应的占位类型。
type Empty struct{}
