// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package api 是业务主进程与 UI 子进程之间的契约层：只包含与传输
// 无关的词汇表（枚举取值）与 DTO（请求/响应数据结构）。
//
// 本包是叶子包，只依赖 internal/urlkind 与标准库，因此可以被
// 传输层（internal/rpc 及其 httpx/grpcx 实现）与业务侧
// （internal/appservice）同时引用，而不会形成依赖环。契约的
// 序列化形态（JSON / protobuf）由各传输实现自行决定。
package api

import "lgo_download_manager/internal/urlkind"

// ProtocolKind 是任务所属的传输协议。类型别名让 api 与 internal/protocol
// 共用同一个类型，跨层转换时不需要任何映射。
type ProtocolKind = urlkind.ProtocolKind

const (
	ProtoHTTP   = urlkind.ProtoHTTP
	ProtoHTTPS  = urlkind.ProtoHTTPS
	ProtoFTP    = urlkind.ProtoFTP
	ProtoWebDAV = urlkind.ProtoWebDAV
	ProtoBT     = urlkind.ProtoBT
)

// Status 反映 Task 的生命周期状态。取值与 internal/store 完全一致
// （首字母大写），appservice 的转换测试锁定两侧取值集合相同。
type Status string

const (
	StatusPending     Status = "Pending"
	StatusDownloading Status = "Downloading"
	StatusPaused      Status = "Paused"
	StatusCompleted   Status = "Completed"
	StatusFailed      Status = "Failed"
	StatusFileLost    Status = "FileLost"
)

// StatusFilter 是 UI 侧边栏的任务筛选条件。
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

// TaskSort 是任务列表的排序方式。
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

// ProxyMode 是代理模式。internal/protocol 那边是 iota 整数枚举，
// 这里用它的稳定字符串表示：字符串在 JSON 与 proto 枚举之间更好映射。
type ProxyMode string

const (
	ProxyModeSystem   ProxyMode = "system"
	ProxyModeDisabled ProxyMode = "disabled"
	ProxyModeManual   ProxyMode = "manual"
)
