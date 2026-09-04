// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build linux

package sysproxy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseINI(t *testing.T) {
	const content = `# comment
[General]
foo=bar

[Proxy Settings]
ProxyType=1
httpProxy=http://127.0.0.1 7890
httpsProxy=https://127.0.0.1:7891
socksProxy=socks5://127.0.0.1:1080
NoProxyFor=localhost,127.0.0.1
`
	section, ok := parseINI(content, "Proxy Settings")
	if !ok {
		t.Fatal("parseINI: section not found")
	}
	if section["ProxyType"] != "1" {
		t.Errorf("ProxyType = %q, want 1", section["ProxyType"])
	}
	if section["httpProxy"] != "http://127.0.0.1 7890" {
		t.Errorf("httpProxy = %q", section["httpProxy"])
	}
	if section["NoProxyFor"] != "localhost,127.0.0.1" {
		t.Errorf("NoProxyFor = %q", section["NoProxyFor"])
	}
	// 不存在的 section 必须返回 ok=false
	if _, ok := parseINI(content, "Nonexistent"); ok {
		t.Error("parseINI returned ok=true for missing section")
	}
}

func TestParseKDEHostPort(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantHost string
		wantPort string
	}{
		{"scheme-space", "http://127.0.0.1 7890", "127.0.0.1", "7890"},
		{"scheme-colon", "http://127.0.0.1:7890", "127.0.0.1", "7890"},
		{"bare", "127.0.0.1:7890", "127.0.0.1", "7890"},
		{"https", "https://proxy.example.com:443", "proxy.example.com", "443"},
		{"empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, p := parseKDEHostPort(tt.in)
			if h != tt.wantHost || p != tt.wantPort {
				t.Errorf("parseKDEHostPort(%q) = (%q,%q), want (%q,%q)",
					tt.in, h, p, tt.wantHost, tt.wantPort)
			}
		})
	}
}

func TestDetectKDEMissingFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	// kioslaverc 不存在应返回 (Config{}, false)
	cfg, ok := detectKDE()
	if ok {
		t.Errorf("detectKDE returned ok=true with no file: %+v", cfg)
	}
}

func TestDetectKDEWithManualHTTP(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	path := filepath.Join(tmp, "kioslaverc")
	const content = `[Proxy Settings]
ProxyType=1
httpProxy=http://127.0.0.1 7890
NoProxyFor=localhost,127.0.0.1
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, ok := detectKDE()
	if !ok {
		t.Fatal("detectKDE returned ok=false")
	}
	if cfg.ProxyURL != "http://127.0.0.1:7890" {
		t.Errorf("ProxyURL = %q, want http://127.0.0.1:7890", cfg.ProxyURL)
	}
	if cfg.Source != "kde" {
		t.Errorf("Source = %q, want kde", cfg.Source)
	}
	if cfg.Bypass != "localhost,127.0.0.1" {
		t.Errorf("Bypass = %q, want localhost,127.0.0.1", cfg.Bypass)
	}
}

func TestDetectKDEType0MeansDisabled(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	path := filepath.Join(tmp, "kioslaverc")
	if err := os.WriteFile(path, []byte("[Proxy Settings]\nProxyType=0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, ok := detectKDE()
	if !ok {
		t.Fatal("detectKDE returned ok=false")
	}
	if cfg.ProxyURL != "" {
		t.Errorf("KDE disabled should yield empty ProxyURL, got %q", cfg.ProxyURL)
	}
}

func TestStripQuotes(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"'127.0.0.1'", "127.0.0.1"},
		{"'a'", "a"},
		{"unquoted", "unquoted"},
		{"''", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := stripQuotes(tt.in); got != tt.want {
				t.Errorf("stripQuotes(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStripQuotesAndBrackets(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"['localhost', '127.0.0.0/8']", "localhost,127.0.0.0/8"},
		{"['a']", "a"},
		{"[]", ""},
		{"nothing", "nothing"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := stripQuotesAndBrackets(tt.in); got != tt.want {
				t.Errorf("stripQuotesAndBrackets(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDetectEtcEnvironmentMissing(t *testing.T) {
	// /etc/environment 在大多数 CI 中不存在；不报错即可。
	_, _ = detectEtcEnvironment()
}
