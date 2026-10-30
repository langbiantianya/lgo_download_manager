// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package protocol

import (
	"fmt"

	"lgo_download_manager/internal/urlkind"
)

// ProtocolKind 是便于枚举的字符串类型，用于表示所支持的传输协议。
// 定义在 internal/urlkind，这里保留别名以便既有调用点不必改动。
type ProtocolKind = urlkind.ProtocolKind

const (
	ProtoHTTP   = urlkind.ProtoHTTP
	ProtoHTTPS  = urlkind.ProtoHTTPS
	ProtoFTP    = urlkind.ProtoFTP
	ProtoWebDAV = urlkind.ProtoWebDAV
	// ProtoBT 是 BitTorrent(磁力链接 / .torrent 种子文件)。BT 不使用
	// ProtocolDriver 接口——它由 internal/bittorrent 直接驱动调度器的
	// 任务生命周期,这里只作为任务上的协议标记与分发依据。
	ProtoBT = urlkind.ProtoBT
)

// DetectKind 解析给定 URL 应使用的协议。映射规则见 urlkind.DetectKind。
func DetectKind(raw string, hint ProtocolKind) (ProtocolKind, error) {
	return urlkind.DetectKind(raw, hint)
}

// IsBitTorrent 报告 raw 是否应交给 BitTorrent 引擎处理。
func IsBitTorrent(raw string) bool { return urlkind.IsBitTorrent(raw) }

// ResolveURL 返回一个真正可用的传输 URL。
func ResolveURL(raw string, kind ProtocolKind) string {
	return urlkind.ResolveURL(raw, kind)
}

// Auth 承载随任务一起传递的凭据。驱动按需读取其关心的字段；
// 不相关的字段将被忽略。
type Auth struct {
	AuthOptions
}

// New 为 `kind` 返回对应的驱动。所有驱动都会校验 URL。
// 返回的驱动拥有其自身的资源；调用方必须调用 Close()。
func New(raw string, kind ProtocolKind, auth Auth) (ProtocolDriver, error) {
	url := ResolveURL(raw, kind)
	switch kind {
	case ProtoHTTP, ProtoHTTPS:
		return newHTTPDriver(url, auth.AuthOptions)
	case ProtoFTP:
		return newFTPDriver(url, auth.AuthOptions)
	case ProtoWebDAV:
		return newWebDAVDriver(url, auth.AuthOptions)
	case ProtoBT:
		return nil, fmt.Errorf("protocol: bittorrent is driven by the bittorrent engine, not a ProtocolDriver")
	default:
		return nil, fmt.Errorf("protocol: no driver for kind=%s", kind)
	}
}
