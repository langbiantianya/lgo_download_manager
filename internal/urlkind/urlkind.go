// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package urlkind 收纳与传输协议无关的纯 URL 助手：协议判定、
// BitTorrent 资源识别、以及 BT 任务目录名推导。
//
// 这些函数过去散落在 internal/protocol 与 internal/scheduler 里，
// 而 UI 也需要用到它们。抽到这里之后 UI 与业务侧共享同一份实现，
// 且本包不依赖任何其它内部包，可以被任意层安全引用。
package urlkind

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"
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

// MagnetName 从磁力链接中取出显示名("dn" 参数)。取不到时返回空串——
// 调用方应退回到 infohash 之类的占位名,等元数据到手后再展示真实种子名。
//
// 只做字符串解析,不依赖 bittorrent 包,因此 UI 子进程与业务进程都能
// 用它推导「新建任务」对话框的默认保存目录。
func MagnetName(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "magnet") {
		return ""
	}
	return strings.TrimSpace(u.Query().Get("dn"))
}

// MagnetInfoHash 从磁力链接中取出 infohash(小写十六进制,40 位 v1 或
// 64 位 v2)。取不到时返回空串。
func MagnetInfoHash(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "magnet") {
		return ""
	}
	for _, xt := range u.Query()["xt"] {
		lower := strings.ToLower(xt)
		if rest, ok := strings.CutPrefix(lower, "urn:btih:"); ok {
			return strings.TrimSpace(rest)
		}
		if rest, ok := strings.CutPrefix(lower, "urn:btmh:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// TorrentNameFromPath 从 .torrent 的 HTTP URL 或本地路径推导种子名
// (去掉 .torrent 后缀的文件名),用于默认任务目录名。
func TorrentNameFromPath(raw string) string {
	path := strings.TrimSpace(raw)
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	if u, err := url.Parse(path); err == nil && u.Path != "" {
		path = u.Path
	}
	base := filepath.Base(path)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// SafeDirName 把任意种子名/文件名清洗成一个可以安全用作目录名的字符串:
// 去掉路径分隔符与 Windows 保留字符,折叠空白,过长的名字截断。
// 空结果返回 fallback。
func SafeDirName(name, fallback string) string {
	const maxLen = 80
	var b strings.Builder
	for _, r := range name {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			b.WriteRune('_')
		case '\n', '\r', '\t':
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	// 纯 "." / ".." 作为目录名会产生歧义(上跳目录),一并替换掉。
	if out == "." || out == ".." {
		out = ""
	}
	if len(out) > maxLen {
		// 按**字节**截断会切在多字节字符中间（中文名尤其常见），产出
		// 非法 UTF-8——目录名里出现非法字节会让后续的路径处理与
		// 文件系统调用行为变得不可预期。改为按 rune 边界回退：
		// 先粗截到 maxLen，再把末尾可能不完整的 UTF-8 序列去掉。
		cut := out[:maxLen]
		for len(cut) > 0 && !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1]
		}
		out = strings.TrimSpace(cut)
	}
	if out == "" {
		return fallback
	}
	return out
}

// BittorrentDefaultDir 推导 BT 任务的默认目录名:磁力链接用 "dn" 参数
// (没有则用 infohash 前缀),.torrent 用去掉后缀的文件名。UI 与命令行
// 路径共用它,保证两者得到同一个目录名。
func BittorrentDefaultDir(rawURL string) string {
	if !IsBitTorrent(rawURL) {
		return ""
	}
	lower := strings.ToLower(strings.TrimSpace(rawURL))
	if strings.HasPrefix(lower, "magnet:") {
		if name := MagnetName(rawURL); name != "" {
			return SafeDirName(name, "")
		}
		if ih := MagnetInfoHash(rawURL); ih != "" {
			if len(ih) > 16 {
				ih = ih[:16]
			}
			return "magnet-" + ih
		}
		return "magnet"
	}
	if name := TorrentNameFromPath(rawURL); name != "" {
		return SafeDirName(name, "")
	}
	return ""
}
