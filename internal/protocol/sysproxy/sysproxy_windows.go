// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build windows

package sysproxy

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// detectWindows 通过 HKCU\Software\Microsoft\Windows\CurrentVersion\
// Internet Settings 注册表读取 WinINET 代理配置。
//
// 这是 Internet Explorer / Edge / 大多数桌面应用共享的代理存储位置；
// 用户在 Internet Options → Connections → LAN settings 中的修改会
// 立即被此函数读取。
//
// 暂不实现 PAC/WPAD 脚本解析（需要 JS 引擎）。
func detectWindows() (Config, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		registry.QUERY_VALUE)
	if err != nil {
		return Config{}, false
	}
	defer k.Close()
	enabled, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return Config{Source: "wininet"}, true
	}
	server, _, err := k.GetStringValue("ProxyServer")
	if err != nil || server == "" {
		return Config{Source: "wininet"}, true
	}
	override, _, _ := k.GetStringValue("ProxyOverride")

	// ProxyServer 两种格式：
	//   a) "host:port"             ← 单一代理，所有协议共享
	//   b) "http=h:p;https=h:p;..." ← 按协议拆分
	proxyURL := parseWinINETProxyServer(server)
	if proxyURL == "" {
		return Config{Source: "wininet"}, true
	}
	return Config{
		ProxyURL: proxyURL,
		Bypass:   NormalizeBypass(override),
		Source:   "wininet",
	}, true
}

// parseWinINETProxyServer 解析 "ProxyServer" 字符串。
//
// 策略：优先 https=http；http=http；socks=socks5。其余条目忽略。
func parseWinINETProxyServer(s string) string {
	if !strings.Contains(s, "=") {
		// 形式 (a)：host:port —— 默认 HTTP 代理。
		return "http://" + s
	}
	parts := strings.Split(s, ";")
	for _, kv := range parts {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(kv[:eq]))
		v := strings.TrimSpace(kv[eq+1:])
		switch k {
		case "https", "http":
			if v != "" {
				return "http://" + v
			}
		case "socks":
			if v != "" {
				return "socks5://" + v
			}
		}
	}
	return ""
}
