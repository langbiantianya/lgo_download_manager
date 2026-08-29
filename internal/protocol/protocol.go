// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package protocol 定义每个传输驱动（driver）必须实现的统一接口，
// 以及它们共享的能力/选项结构体。具体的驱动实现位于同包的
// protocol_*.go 文件中。
//
// 该接口有意与设计文档保持一致：Probe 用于资源探测，
// DownloadChunk 用于多线程区间下载，DownloadFallback 用于
// 不支持 Range 的服务器下的单流下载。
package protocol

import (
	"context"
	"os"
)

// DriverCapabilities 是 Probe 成功执行后的结果——包含足够多的元数据，
// 供引擎规划下载方式。
type DriverCapabilities struct {
	TotalSize    int64  // 总字节长度；未知时为 -1
	SupportRange bool   // 服务器是否支持 Range / FTP REST
	ServerInfo   string // 自由形式的 banner / 服务器响应头
}

// AuthOptions 封装凭据以及驱动能够遵循的请求修饰开关。
// 字段与具体协议无关；驱动会忽略其不理解的字段。
type AuthOptions struct {
	Username   string
	Password   string
	UserAgent  string
	Cookies    string
	Referer    string
	FTPPassive bool // 仅对 FTP 生效
}

// ProtocolDriver 是每个传输实现必须满足的接口。所有方法都必须
// 遵循传入 context 的取消/截止语义。
type ProtocolDriver interface {
	// Probe 检查资源并报告其能力。对于需要认证的 URL，
	// 这是执行握手的最合适位置。
	Probe(ctx context.Context) (*DriverCapabilities, error)

	// DownloadChunk 获取 [start, end]（含）区间内的字节，
	// 并写入到 file 的 offset `start` 位置。每次成功读取后，
	// 都会调用 onData 回调并传入本次追加的字节数。
	// 实现应当校验读取字节数并确保顺序正确。
	DownloadChunk(ctx context.Context, start, end int64, file *os.File, onData func(n int)) error

	// DownloadFallback 从 `offset` 处开始以单流方式拉取资源
	// 并追加到文件中（服务器不支持 Range）。作为多线程
	// 下载路径的回退方案使用。
	DownloadFallback(ctx context.Context, offset int64, file *os.File, onData func(n int)) error

	// Close 释放所有池化的连接与资源。
	Close() error
}
