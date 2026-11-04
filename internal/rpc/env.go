// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package rpc

// UI 子进程与业务进程之间的通道参数。业务进程把监听地址、token 与
// 传输协议名通过环境变量下发给独立 lgdm-ui 或自我复刻的兼容 UI 进程；
// 子进程靠这几个变量认出「我是 UI 子进程」，并知道该去哪个地址、用哪种
// 传输连回业务进程。
//
// 这些名字是业务进程与 UI 子进程之间的约定，改动要同时改两处。
const (
	// EnvUIChild 标记当前进程是 UI 子进程。独立 lgdm-ui 由业务进程设置；
	// 自我复刻的兼容路径也靠它进入 UI 角色。值为 "1" 时表示是。
	EnvUIChild = "LDM_UI_CHILD"
	// EnvUIListen 是业务进程的监听地址，格式见 addr.Parse：
	// unix:<path> 或 tcp:<host:port>。
	EnvUIListen = "LDM_UI_LISTEN"
	// EnvUIToken 是握手 token，UI 子进程用它鉴权。
	EnvUIToken = "LDM_UI_TOKEN"
	// EnvUITransport 是传输协议名：grpc 或 http。
	EnvUITransport = "LDM_UI_TRANSPORT"
)
