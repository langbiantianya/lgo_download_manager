// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Package ipc 定义业务主进程与 Fyne UI 子进程之间的线协议与通道：
// Unix socket + 长度前缀 JSON 帧。UI 子进程可随时销毁，由操作系统
// 回收其全部内存；业务进程不中断，UI 可在托盘菜单中随时重建。
package ipc

import "encoding/json"

// UI 子进程的启动环境变量（单二进制自我复刻角色切换）。

const (
	EnvUIChild  = "LDM_UI_CHILD"  // "1" 表示当前进程以 UI 子进程角色运行
	EnvUISocket = "LDM_UI_SOCKET" // Unix socket 路径
	EnvUIToken  = "LDM_UI_TOKEN"  // 握手 token
)

// 消息类型：业务 → UI。
const (
	// MsgInit 携带初始状态（权威设置快照），在 UI 握手后由业务推送。
	MsgInit = "biz.init"
	// MsgEvent 转发一条 scheduler 事件（进度/状态变更…）。
	MsgEvent = "biz.event"
	// MsgResult 是 UI 调用的应答；ID 与对应的 ui.call 一致。
	MsgResult = "biz.result"
	// MsgShow 请求 UI 显示/恢复主窗口（托盘「显示窗口」）。
	MsgShow = "biz.show"
	// MsgClose 请求 UI 进程优雅退出（托盘「退出」/业务进程关停）。
	MsgClose = "biz.close"
)

// 消息类型：UI → 业务。
const (
	// MsgHello 是 UI 连接后的第一条消息，携带握手 token。
	MsgHello = "ui.hello"
	// MsgCall 是 UI 发起的远程调用（list/add/start/…），带自增 ID。
	MsgCall = "ui.call"
)

// Message 是 socket 上的信封。Data 为 json.RawMessage，具体结构由 Type 决定。
type Message struct {
	ID   string          `json:"id,omitempty"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// HelloData 是 ui.hello 的负载。
type HelloData struct {
	Token string `json:"token"`
}

// CallData 是 ui.call 的负载。
type CallData struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// ResultData 是 biz.result 的负载。
type ResultData struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}
