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
)

// DetectKind 解析给定 URL 应使用的协议。映射规则遵循设计文档：
//
//   - http://、https://   -> HTTP / HTTPS（webdav:// 被改写为 https 以兼容
//     部分 NAS 固件；我们仍保留独立的代码路径）
//   - ftp://              -> FTP
//   - webdav://、dav://   -> WEBDAV（底层由 HTTP 实现）
//
// 调用方显式传入的 hint（例如 UI 中的选择）优先生效。
func DetectKind(raw string, hint ProtocolKind) (ProtocolKind, error) {
	if hint != "" {
		return hint, nil
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
	default:
		return nil, fmt.Errorf("protocol: no driver for kind=%s", kind)
	}
}
