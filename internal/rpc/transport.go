// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package rpc 是业务进程与 UI 子进程之间的传输抽象层：定义与协议无关的
// Transport / Handler / Client 三组契约，两种协议实现（gRPC、net/http）
// 都挂在 Transport 上，UI 只认 Client。
package rpc

import (
	"context"

	"lgo_download_manager/internal/api"
)

// Transport 是协议实现需要满足的最小能力面。新增一种协议只需实现这两个
// 方法，调用语义（超时、错误包装、事件扇出）由本包的共享实现统一处理。
type Transport interface {
	// Call 执行一次调用。method 取 api.Method* 常量；in / out 是 api 层的
	// DTO 指针，类型由 api.Spec(method) 决定，由传输实现按自己的线格式编解码。
	Call(ctx context.Context, method string, in, out any) error

	// Stream 打开服务端推送的消息流（进度事件与控制消息合并流）。
	// 返回的 channel 在流结束或 ctx 取消后关闭。
	Stream(ctx context.Context) (<-chan api.Message, error)
}
