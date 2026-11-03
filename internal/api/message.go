// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package api

// MessageKind 区分消息流里的消息类别。
type MessageKind string

const (
	MessageKindEvent       MessageKind = "event"         // 进度/状态事件
	MessageKindShow        MessageKind = "show"          // 业务托盘「显示窗口」
	MessageKindShowAddTask MessageKind = "show_add_task" // lgom:// 转发后弹对话框
	MessageKindLanguage    MessageKind = "language"      // 权威语言变更
	MessageKindClose       MessageKind = "close"         // 业务侧要求 UI 退出
)

// Message 是服务端推送的一条消息。事件与控制消息合并在同一条流里，
// 用 Kind 区分。
type Message struct {
	Kind        MessageKind        `json:"kind"`
	Event       *Event             `json:"event,omitempty"`
	ShowAddTask *ShowAddTaskParams `json:"show_add_task,omitempty"`
	Language    string             `json:"language,omitempty"`
}

// ShowAddTaskParams 描述一次「请 UI 弹出新建下载任务对话框」的请求。
type ShowAddTaskParams struct {
	URL     string `json:"url"`
	Name    string `json:"name,omitempty"`
	UA      string `json:"ua,omitempty"`
	Cookies string `json:"cookies,omitempty"`
}
