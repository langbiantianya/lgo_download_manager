// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package protocol

import (
	"fmt"
	"net/url"
	"strings"
)

// ProtocolKind 是便于枚举的字符串类型，用于表示所支持的传输协议。
type ProtocolKind string

const (
	ProtoHTTP   ProtocolKind = "HTTP"
	ProtoHTTPS  ProtocolKind = "HTTPS"
	ProtoFTP    ProtocolKind = "FTP"
	ProtoWebDAV ProtocolKind = "WEBDAV"
	// ProtoBT 是 BitTorrent(磁力链接 / .torrent 种子文件)。BT 不使用
	// ProtocolDriver 接口——它由 internal/bittorrent 直接驱动调度器的
	// 任务生命周期,这里只作为任务上的协议标记与分发依据。
	ProtoBT ProtocolKind = "BT"
)

// DetectKind 解析给定 URL 应使用的协议。映射规则遵循设计文档：
//
//   - http://、https://   -> HTTP / HTTPS（webdav:// 被改写为 https 以兼容
//     部分 NAS 固件；我们仍保留独立的代码路径）
//   - ftp://              -> FTP
//   - webdav://、dav://   -> WEBDAV（底层由 HTTP 实现）
//   - magnet:             -> BT（磁力链接）
//   - 以 .torrent 结尾的 http(s) URL / 本地文件路径 -> BT（种子文件）
//
// 调用方显式传入的 hint（例如 UI 中的选择）优先生效。
func DetectKind(raw string, hint ProtocolKind) (ProtocolKind, error) {
	if hint != "" {
		return hint, nil
	}
	// 磁力链接与 .torrent 都不是「URL 决定传输驱动」的常规情形,先于
	// scheme 分派识别,避免 uri 解析/magnet scheme 落到「不支持」分支。
	if kind, ok := detectBitTorrent(raw); ok {
		return kind, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return ProtoHTTP, nil
	case "https":
		return ProtoHTTPS, nil
	case "ftp":
		return ProtoFTP, nil
	case "webdav", "dav":
		return ProtoWebDAV, nil
	default:
		return "", fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
}

// detectBitTorrent 识别磁力链接与 .torrent 种子文件(含 HTTP/HTTPS URL
// 与本地路径两种形态)。第二个返回值为 false 表示「不是 BT 资源」。
func detectBitTorrent(raw string) (ProtocolKind, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "magnet:") {
		return ProtoBT, true
	}
	// 去掉 query/fragment 后再看扩展名,避免 "x.torrent?token=..." 漏判。
	path := trimmed
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	if strings.HasSuffix(strings.ToLower(path), ".torrent") {
		return ProtoBT, true
	}
	return "", false
}

// IsBitTorrent 报告 raw 是否应交给 BitTorrent 引擎处理。
func IsBitTorrent(raw string) bool {
	_, ok := detectBitTorrent(raw)
	return ok
}

// ResolveURL 返回一个真正可用的传输 URL。对于 WEBDAV，
// 调用方传入的是原始的 webdav:// 协议；webdav 驱动会
// 将其改写为 http(s)。
func ResolveURL(raw string, kind ProtocolKind) string {
	if kind == ProtoWebDAV {
		if strings.HasPrefix(raw, "webdav://") {
			return "https://" + strings.TrimPrefix(raw, "webdav://")
		}
		if strings.HasPrefix(raw, "dav://") {
			return "http://" + strings.TrimPrefix(raw, "dav://")
		}
	}
	return raw
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
