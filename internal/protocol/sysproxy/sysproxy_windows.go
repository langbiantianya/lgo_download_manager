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
func detectWindows() (Config, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		registry.QUERY_VALUE)
	if err != nil {
		return Config{}, false
	}
	defer k.Close()
	autoConfigURL, _, _ := k.GetStringValue("AutoConfigURL")
	autoDetect, _, _ := k.GetIntegerValue("AutoDetect")
	if autoConfigURL != "" || autoDetect != 0 {
		override, _, _ := k.GetStringValue("ProxyOverride")
		return Config{
			Source:        "wininet-auto",
			AutoConfigURL: strings.TrimSpace(autoConfigURL),
			AutoDiscover:  autoConfigURL == "" && autoDetect != 0,
			Bypass:        NormalizeBypass(override),
		}, true
	}
	enabled, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return Config{Source: "wininet"}, true
	}
	server, _, err := k.GetStringValue("ProxyServer")
	if err != nil || server == "" {
		return Config{Source: "wininet"}, true
	}
	override, _, _ := k.GetStringValue("ProxyOverride")

	httpProxy, httpsProxy, socksProxy := parseWinINETProxyURLs(server)
	if httpProxy == "" && httpsProxy == "" && socksProxy == "" {
		return Config{Source: "wininet"}, true
	}
	proxyURL := httpsProxy
	if proxyURL == "" {
		proxyURL = httpProxy
	}
	if proxyURL == "" {
		proxyURL = socksProxy
	}
	return Config{
		ProxyURL:      proxyURL,
		HTTPProxyURL:  httpProxy,
		HTTPSProxyURL: httpsProxy,
		Bypass:        NormalizeBypass(override),
		Source:        "wininet",
	}, true
}

// parseWinINETProxyServer 解析 "ProxyServer" 字符串。
//
// 两种形式：
//
//	a) "host:port"              —— 单一代理，所有协议共享
//	b) "http=h:p;https=h:p;..." —— 按协议拆分
//
// 策略：优先级固定为 https > http > socks，与条目在字符串中的
// 出现顺序无关（Windows 写入的顺序并不稳定）。没有可用条目时返回 ""。
func parseWinINETProxyServer(s string) string {
	httpProxy, httpsProxy, socksProxy := parseWinINETProxyURLs(s)
	switch {
	case httpsProxy != "":
		return httpsProxy
	case httpProxy != "":
		return httpProxy
	default:
		return socksProxy
	}
}

// parseWinINETProxyURLs 保留 WinINET 的协议级代理划分。浏览器允许
// HTTP 与 HTTPS 指向不同端口，若只取其中一个，另一个协议的请求就会
// 发到错误的代理端点。
func parseWinINETProxyURLs(s string) (httpProxy, httpsProxy, socksProxy string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", ""
	}
	if !strings.Contains(s, "=") {
		// 形式 (a)：host:port —— 单一代理，所有协议共享。
		proxy := normalizeWinINETProxyURL(s, "http")
		return proxy, proxy, ""
	}
	for _, kv := range strings.Split(s, ";") {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(kv[:eq]))
		v := strings.TrimSpace(kv[eq+1:])
		if v == "" {
			continue
		}
		switch k {
		case "https":
			if httpsProxy == "" {
				httpsProxy = normalizeWinINETProxyURL(v, "http")
			}
		case "http":
			if httpProxy == "" {
				httpProxy = normalizeWinINETProxyURL(v, "http")
			}
		case "socks":
			if socksProxy == "" {
				socksProxy = normalizeWinINETProxyURL(v, "socks5")
			}
		}
	}
	return httpProxy, httpsProxy, socksProxy
}

func normalizeWinINETProxyURL(raw, defaultScheme string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "socks5://") ||
		strings.HasPrefix(lower, "socks://") {
		return raw
	}
	return defaultScheme + "://" + raw
}
