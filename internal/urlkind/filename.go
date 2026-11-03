// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package urlkind

import (
	"mime"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// filenameQueryKeys 是下载站点常用来携带真实文件名的查询参数。顺序有
// 意义:filename/file/name 比泛化的 output/download 更具体。
//
// response-content-disposition 单独先处理:它本身就是一段
// Content-Disposition,值里可能还带 filename*=UTF-8”...。
var filenameQueryKeys = []string{
	"filename",
	"file",
	"name",
	"dn",
	"download",
	"fn",
	"fname",
	"output",
	"target",
}

// Filename 从下载 URL 中推导一个适合直接放到保存路径末尾的文件名。
//
// 解析顺序为 URL 查询参数里的 Content-Disposition、常见文件名参数、
// URL 路径末段。Path 与查询值都会解码百分号编码；结果还会去除路径分隔
// 符并清洗 Windows 不允许的字符，避免远端提供的名字把文件写到别处。
// 无法推导时返回空串，由调用方决定 fallback。
func Filename(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}

	u, err := url.Parse(rawURL)
	if err == nil {
		query := u.Query()
		if disposition := firstQueryValue(query, "response-content-disposition"); disposition != "" {
			if name := FilenameFromContentDisposition(disposition); name != "" {
				return name
			}
		}
		for _, key := range filenameQueryKeys {
			value := firstQueryValue(query, key)
			if value == "" {
				continue
			}
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "attachment;") {
				if name := FilenameFromContentDisposition(value); name != "" {
					return name
				}
			}
			if name := SanitizeFilename(value); name != "" {
				return name
			}
		}
		path := u.EscapedPath()
		if decoded, decodeErr := url.PathUnescape(path); decodeErr == nil {
			path = decoded
		}
		if name := SanitizeFilename(path); name != "" {
			return name
		}
		// Host-only URL 没有文件可命名；不要误把主机名当文件名。
		if u.Host != "" {
			return ""
		}
	}

	// 本地路径，尤其是 Windows 路径，可能无法按 URL 解析。此时仍可
	// 按两种目录分隔符取最后一段；去掉 fragment/query 后与浏览器行为一致。
	fallback := rawURL
	if i := strings.IndexAny(fallback, "?#"); i >= 0 {
		fallback = fallback[:i]
	}
	return SanitizeFilename(fallback)
}

// FilenameFromContentDisposition 从 HTTP Content-Disposition 解析文件名。
//
// 支持 RFC 6266 的 filename / filename*、RFC 5987 的 charset”value、
// 常见的百分号编码以及历史遗留的 RFC 2047 encoded-word。参数大小写
// 不敏感，引号内的分号也不会被误当成分隔符。
func FilenameFromContentDisposition(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if _, params, err := mime.ParseMediaType(raw); err == nil {
		for _, key := range []string{"filename*", "filename"} {
			if name := sanitizeFilenameParam(params[key]); name != "" {
				return name
			}
		}
	}

	// 有些服务端会生成 mime.ParseMediaType 不接受的残缺参数；退回到
	// 一个只识别 filename/filename* 的宽容解析器。
	params := parseDispositionParams(raw)
	for _, key := range []string{"filename*", "filename"} {
		if name := sanitizeFilenameParam(params[key]); name != "" {
			return name
		}
	}
	return ""
}

// SanitizeFilename 返回一个安全的单段文件名。它会取 Linux/Windows
// 路径的最后一段，替换两类系统的路径分隔符与 Windows 保留字符，并
// 处理 "." / ".."、保留设备名和末尾的句点/空白。无法得到有效名字时
// 返回空串。
func SanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range name {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			b.WriteByte('_')
		case '\n', '\r', '\t':
			b.WriteByte(' ')
		default:
			if unicode.IsControl(r) {
				continue
			}
			b.WriteRune(r)
		}
	}
	out := strings.TrimRight(strings.TrimSpace(b.String()), " .")
	if out == "" || out == "." || out == ".." {
		return ""
	}
	if isWindowsReservedName(out) {
		out = "_" + out
	}
	if len(out) > 180 {
		out = truncateUTF8(out, 180)
		out = strings.TrimRight(out, " .")
	}
	return out
}

func firstQueryValue(query url.Values, key string) string {
	for k, values := range query {
		if !strings.EqualFold(k, key) {
			continue
		}
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func sanitizeFilenameParam(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		raw = raw[1 : len(raw)-1]
	}
	if parts := strings.SplitN(raw, "'", 3); len(parts) == 3 {
		raw = parts[2]
	}
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = decoded
	}
	if decoded, err := new(mime.WordDecoder).DecodeHeader(raw); err == nil {
		raw = decoded
	}
	return SanitizeFilename(raw)
}

func parseDispositionParams(raw string) map[string]string {
	parts := splitQuoted(raw, ';')
	params := make(map[string]string, len(parts))
	for _, part := range parts[1:] {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' {
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			} else if value[len(value)-1] != '"' {
				value = strings.TrimPrefix(value, `"`)
			}
		}
		params[key] = value
	}
	return params
}

func splitQuoted(s string, sep rune) []string {
	var parts []string
	start := 0
	quoted := false
	escaped := false
	for i, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && quoted:
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == sep && !quoted:
			parts = append(parts, s[start:i])
			start = i + utf8.RuneLen(r)
		}
	}
	return append(parts, s[start:])
}

func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := s[:maxBytes]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

func isWindowsReservedName(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(strings.TrimSpace(stem))
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(stem) == 4 {
		prefix := stem[:3]
		return (prefix == "COM" || prefix == "LPT") && stem[3] >= '1' && stem[3] <= '9'
	}
	return false
}
