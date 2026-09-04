// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build windows

package sysproxy

import "testing"

func TestParseWinINETProxyServerSimple(t *testing.T) {
	got := parseWinINETProxyServer("127.0.0.1:7890")
	if got != "http://127.0.0.1:7890" {
		t.Errorf("simple form = %q, want http://127.0.0.1:7890", got)
	}
}

func TestParseWinINETProxyServerPrefersHTTPS(t *testing.T) {
	got := parseWinINETProxyServer("http=h:80;https=p:8080;socks=s:1080;ftp=f:21")
	if got != "http://p:8080" {
		t.Errorf("precedence = %q, want http://p:8080", got)
	}
}

func TestParseWinINETProxyServerFallsBackToHTTP(t *testing.T) {
	got := parseWinINETProxyServer("http=h:80;socks=s:1080")
	if got != "http://h:80" {
		t.Errorf("HTTP fallback = %q, want http://h:80", got)
	}
}

func TestParseWinINETProxyServerSOCKSOnly(t *testing.T) {
	got := parseWinINETProxyServer("socks=s:1080")
	if got != "socks5://s:1080" {
		t.Errorf("SOCKS-only = %q, want socks5://s:1080", got)
	}
}

func TestParseWinINETProxyServerEmpty(t *testing.T) {
	if got := parseWinINETProxyServer(""); got != "" {
		t.Errorf("empty = %q, want empty", got)
	}
}
