// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package protocol

import (
	"net/url"
	"path/filepath"
	"strings"
)

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
		out = strings.TrimSpace(out[:maxLen])
	}
	if out == "" {
		return fallback
	}
	return out
}
