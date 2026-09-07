// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// Service 是 UI 依赖的业务能力边界。生产环境由 IPC 客户端实现
// （UI 子进程角色）；测试可直接以真实 scheduler/store 实现本地版。
// 方法签名刻意贴近 scheduler.Store 的既有用法，把改造面收在 ui 包内。
type Service interface {
	// List 查询任务列表（filter/sort 语义与 store.ListTasks 一致）。
	List(filter store.StatusFilter, sort store.TaskSort) ([]*store.Task, error)

	// Subscribe 返回调度器事件流；unsub 取消订阅。
	Subscribe() (<-chan scheduler.Event, func())

	// AddTask 添加一个任务（不自动启动）。返回带 ID 的持久化任务。
	AddTask(in AddTaskInput) (*store.Task, error)

	// Start/Pause/Delete 控制单个任务。
	Start(taskID string) error
	Pause(taskID string) error
	Delete(taskID string) error

	// IsPreparing 报告任务是否处于「Start 已预留 slot 但 engine 尚未启动」
	// 的准备阶段。Pause 必须能在该阶段立即中止任务，UI 用此判断
	// 是否把按钮渲染为「暂停」而不是「开始」。
	IsPreparing(taskID string) bool
	// Probe 探测 URL 目标大小（新建任务对话框的尺寸预览）。
	Probe(url string) (int64, error)

	// Settings 返回当前权威设置快照。
	Settings() store.Settings

	// SaveSettings 持久化设置（业务侧同时应用代理配置）。
	SaveSettings(s store.Settings) error

	// Close 释放连接。
	Close() error
}
// AddTaskInput 描述一次“添加下载任务”。字段对齐 scheduler.AddTaskInput。
type AddTaskInput struct {
	URL          string
	SavePath     string
	ChunkCount   int
	MinChunkSize int64
	// Auth 由调用方预填：业务进程在收到 MethodAdd 后,直接把 Auth 透传给
	// scheduler.Add 的 AuthOptions,进而落到 store.Task.AuthData。
	// "新建下载任务"对话框用 GlobalSettings.UserAgent/Cookies 填充,
	// 使设置中的默认 UA 立即对所有新任务生效;命令行/URL 路径则用各自
	// 自带的凭据覆盖。
	Auth protocol.AuthOptions
}
