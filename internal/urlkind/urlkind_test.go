// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package urlkind

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDetectKind(t *testing.T) {
	cases := []struct {
		raw  string
		hint ProtocolKind
		want ProtocolKind
		ok   bool
	}{
		{raw: "http://x/a.zip", want: ProtoHTTP, ok: true},
		{raw: "https://x/a.zip", want: ProtoHTTPS, ok: true},
		{raw: "ftp://x/a.zip", want: ProtoFTP, ok: true},
		{raw: "webdav://x/a", want: ProtoWebDAV, ok: true},
		{raw: "dav://x/a", want: ProtoWebDAV, ok: true},
		{raw: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", want: ProtoBT, ok: true},
		{raw: "http://x/a.torrent", want: ProtoBT, ok: true},
		{raw: "http://x/a.torrent?token=1", want: ProtoBT, ok: true},
		{raw: "http://x/a", hint: ProtoHTTP, want: ProtoHTTP, ok: true},
		{raw: "gopher://x/a", ok: false},
	}
	for _, c := range cases {
		got, err := DetectKind(c.raw, c.hint)
		if c.ok {
			if err != nil {
				t.Fatalf("DetectKind(%q,%q) 报错: %v", c.raw, c.hint, err)
			}
			if got != c.want {
				t.Fatalf("DetectKind(%q,%q) = %q, want %q", c.raw, c.hint, got, c.want)
			}
		} else if err == nil {
			t.Fatalf("DetectKind(%q,%q) 应当报错", c.raw, c.hint)
		}
	}
}

func TestIsBitTorrent(t *testing.T) {
	if !IsBitTorrent("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567") {
		t.Fatal("磁力链接应识别为 BT")
	}
	if !IsBitTorrent("/tmp/a.torrent") {
		t.Fatal(".torrent 应识别为 BT")
	}
	if IsBitTorrent("http://x/a.zip") {
		t.Fatal("普通 URL 不应识别为 BT")
	}
}

func TestResolveURL(t *testing.T) {
	if got := ResolveURL("webdav://h/p", ProtoWebDAV); got != "https://h/p" {
		t.Fatalf("ResolveURL webdav = %q", got)
	}
	if got := ResolveURL("dav://h/p", ProtoWebDAV); got != "http://h/p" {
		t.Fatalf("ResolveURL dav = %q", got)
	}
	if got := ResolveURL("https://h/p", ProtoHTTPS); got != "https://h/p" {
		t.Fatalf("ResolveURL https = %q", got)
	}
}

// TestBittorrentDefaultDir 校验默认任务目录名的推导:磁力链接优先用 dn,
// 没有 dn 用 infohash 前缀,种子文件用去掉后缀的文件名。
//
// 这组用例原先住在 internal/scheduler（那��有同名的薄封装）。实现迁到
// urlkind 后薄封装被删,用例一并搬过来——覆盖不能跟着封装一起消失。
func TestBittorrentDefaultDir(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Ubuntu%2024.04", "Ubuntu 24.04"},
		{"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", "magnet-0123456789abcdef"},
		{"magnet:?xt=urn:btmh:1220caf1e1c30e81cb361b9ee167c4aa64228a7fa4fa914f7d0483e0c8cc8b0bb1e1", "magnet-1220caf1e1c30e81"},
		{"magnet:?xs=urn:btih:zzz", "magnet"},
		{"https://example.com/files/debian-13.iso.torrent", "debian-13.iso"},
		{"/home/user/incoming/ubuntu.torrent", "ubuntu"},
		{"https://example.com/files/debian.iso.torrent?token=abc", "debian.iso"},
		{"https://example.com/a.zip", ""},
	}
	for _, c := range cases {
		if got := BittorrentDefaultDir(c.url); got != c.want {
			t.Errorf("BittorrentDefaultDir(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// TestSafeDirNameTruncatesOnRuneBoundary 守住「截断后仍是合法 UTF-8」。
//
// SafeDirName 按**字节**上限 80 截断名字。按字节切会切在多字节字符中间，
// 产出非法 UTF-8——中文种子名/文件名是常见输入，这条路径必须真的走到。
func TestSafeDirNameTruncatesOnRuneBoundary(t *testing.T) {
	// 40 个汉字 = 120 字节，必然超过 80 字节上限。
	name := strings.Repeat("测试种子名称", 8)

	got := SafeDirName(name, "fallback")

	if !utf8.ValidString(got) {
		t.Fatalf("截断结果不是合法 UTF-8: %q", got)
	}
	if len(got) > 80 {
		t.Fatalf("截断结果 %d 字节，超过上限 80", len(got))
	}
	if got == "fallback" {
		t.Fatal("不该退化成 fallback")
	}
	// 结果必须仍是完整的中文，不能出现半个字。
	if !strings.HasPrefix(got, "测试") {
		t.Fatalf("截断结果开头不对: %q", got)
	}
	for _, r := range got {
		if r == '�' {
			t.Fatalf("结果里有替换字符 U+FFFD，说明截断了多字节字符: %q", got)
		}
	}
}

// TestSafeDirNameKeepsShortNamesUnchanged 守住「不超长就不动」——截断逻辑
// 不能误伤正常长度的名字。
func TestSafeDirNameKeepsShortNamesUnchanged(t *testing.T) {
	if got := SafeDirName("My Movie", "fb"); got != "My Movie" {
		t.Fatalf("短名字被改动: %q", got)
	}
	if got := SafeDirName("  ", "fb"); got != "fb" {
		t.Fatalf("空白名应返回 fallback, got %q", got)
	}
	if got := SafeDirName("a/b:c", "fb"); got != "a_b_c" {
		t.Fatalf("非法字符未被替换: %q", got)
	}
}
