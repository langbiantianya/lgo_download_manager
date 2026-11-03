// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package urlkind

import "testing"

func TestFilename(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "路径末段",
			raw:  "https://example.com/files/archive.tar.gz?token=abc#download",
			want: "archive.tar.gz",
		},
		{
			name: "路径百分号编码",
			raw:  "https://example.com/files/%E4%B8%AD%E6%96%87%20%E6%96%87%E4%BB%B6.zip",
			want: "中文 文件.zip",
		},
		{
			name: "查询参数中的文件名",
			raw:  "https://example.com/download?token=1&filename=%E5%8F%91%E5%B8%83.zip",
			want: "发布.zip",
		},
		{
			name: "查询参数名大小写不敏感",
			raw:  "https://example.com/download?FileName=release.zip",
			want: "release.zip",
		},
		{
			name: "查询参数优先于泛化路径",
			raw:  "https://example.com/api/download?file=release-1.2.tar.xz",
			want: "release-1.2.tar.xz",
		},
		{
			name: "查询参数里的 Content-Disposition",
			raw: "https://example.com/download?response-content-disposition=" +
				"attachment%3B%20filename%2A%3DUTF-8%27%27report%2520final.pdf",
			want: "report final.pdf",
		},
		{
			name: "反斜杠路径只保留最后一段",
			raw:  "https://example.com/download?name=C%3A%5CUsers%5Cme%5Cmovie.mp4",
			want: "movie.mp4",
		},
		{
			name: "本地 Windows 路径",
			raw:  `C:\Users\me\Downloads\setup.exe`,
			want: "setup.exe",
		},
		{
			name: "目录尾斜杠不会退化成主机名",
			raw:  "https://example.com/files/",
			want: "",
		},
		{
			name: "纯主机地址",
			raw:  "https://example.com",
			want: "",
		},
		{
			name: "查询参数中的路径穿越",
			raw:  "https://example.com/download?filename=..%2F..%2Fevil.zip",
			want: "evil.zip",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Filename(c.raw); got != c.want {
				t.Fatalf("Filename(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

func TestFilenameFromContentDisposition(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "普通 quoted filename",
			raw:  `attachment; filename="release build; final.zip"`,
			want: "release build; final.zip",
		},
		{
			name: "RFC 5987 filename star",
			raw:  `attachment; filename*=UTF-8''%E4%B8%AD%E6%96%87.zip`,
			want: "中文.zip",
		},
		{
			name: "RFC 5987 带语言标签",
			raw:  `attachment; filename*=UTF-8'zh-CN'%E4%B8%AD%E6%96%87.zip`,
			want: "中文.zip",
		},
		{
			name: "filename 后跟其它参数",
			raw:  `attachment; filename="report.pdf"; size=123`,
			want: "report.pdf",
		},
		{
			name: "RFC 2047 encoded word",
			raw:  `attachment; filename="=?UTF-8?B?5Lit5paHLnppcA==?="`,
			want: "中文.zip",
		},
		{
			name: "残缺头也能解析",
			raw:  `attachment; filename="fallback.zip`,
			want: "fallback.zip",
		},
		{
			name: "没有 filename",
			raw:  `attachment`,
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FilenameFromContentDisposition(c.raw); got != c.want {
				t.Fatalf("FilenameFromContentDisposition(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"../../etc/passwd", "passwd"},
		{`C:\temp\file:name?.zip`, "file_name_.zip"},
		{"name. ", "name"},
		{".env", ".env"},
		{"CON", "_CON"},
		{"LPT1.txt", "_LPT1.txt"},
		{"..", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := SanitizeFilename(c.raw); got != c.want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}
