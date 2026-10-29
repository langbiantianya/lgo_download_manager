// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build linux

package sysproxy

import (
	"context"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// detectLinux 实现 Linux 上的系统代理探测。
//
// 数据来源优先级（命中即返回，后续跳过）：
//  1. GNOME（gsettings）：绝大多数 GNOME-on-X11 / GNOME-on-Wayland 会话。
//  2. KDE（kioslaverc）：KDE Plasma 会话。X11 与 Wayland 同源。
//  3. /etc/environment：登录会话级别的系统级代理。
//
// 所有 exec 调用均带 5s 上下文超时，避免 gsettings 挂起整个 UI。
func detectLinux() (Config, bool) {
	if cfg, ok := detectGNOME(); ok {
		return cfg, true
	}
	if cfg, ok := detectKDE(); ok {
		return cfg, true
	}
	if cfg, ok := detectEtcEnvironment(); ok {
		return cfg, true
	}
	return Config{}, false
}

// --- GNOME -----------------------------------------------------------------

func detectGNOME() (Config, bool) {
	if _, err := exec.LookPath("gsettings"); err != nil {
		return Config{}, false
	}
	mode, ok := gsettingsGet("org.gnome.system.proxy", "mode", 5*time.Second)
	if !ok {
		return Config{}, false
	}
	// gsettings 的字符串/enum 输出带单引号，例如 'manual'、'none'。
	// 不去引号会把 manual 误判成“未知”，最终整套系统代理都被跳过。
	mode = stripQuotes(mode)
	switch mode {
	case "manual":
		return gnomeManual()
	case "auto":
		pacURL, _ := gsettingsGet("org.gnome.system.proxy", "autoconfig-url", 5*time.Second)
		pacURL = stripQuotes(pacURL)
		return Config{Source: "gnome-pac", AutoConfigURL: pacURL, AutoDiscover: pacURL == ""}, true
	case "none":
		return Config{Source: "gnome"}, true
	default:
		return Config{}, false
	}
}

func gnomeManual() (Config, bool) {
	httpProxy := gnomeProxyURL("org.gnome.system.proxy.http", "http")
	httpsProxy := gnomeProxyURL("org.gnome.system.proxy.https", "http")
	socksProxy := gnomeProxyURL("org.gnome.system.proxy.socks", "socks5")
	bypass, _ := gsettingsGet("org.gnome.system.proxy", "ignore-hosts", 5*time.Second)

	cfg := Config{
		HTTPProxyURL:  httpProxy,
		HTTPSProxyURL: httpsProxy,
		Bypass:        NormalizeBypass(stripQuotesAndBrackets(bypass)),
		Source:        "gnome",
	}
	switch {
	case httpProxy != "":
		cfg.ProxyURL = httpProxy
	case httpsProxy != "":
		cfg.ProxyURL = httpsProxy
	case socksProxy != "":
		cfg.ProxyURL = socksProxy
	}
	if socksProxy != "" && httpProxy == "" {
		cfg.HTTPProxyURL = socksProxy
	}
	if socksProxy != "" && httpsProxy == "" {
		cfg.HTTPSProxyURL = socksProxy
	}
	return cfg, true
}

// gnomeProxyURL 读取一个 GNOME 代理 schema。host/port 为空表示该协议
// 未单独配置；此时上层会回退到其它协议或 ProxyURL。
func gnomeProxyURL(schema, scheme string) string {
	host, hok := gsettingsGet(schema, "host", 5*time.Second)
	port, pok := gsettingsGet(schema, "port", 5*time.Second)
	host = stripQuotes(host)
	port = stripQuotes(port)
	if !hok || !pok || host == "" || port == "" || port == "0" {
		return ""
	}
	auth, _ := gsettingsGet(schema, "authentication-user", 5*time.Second)
	pass, _ := gsettingsGet(schema, "authentication-password", 5*time.Second)
	auth = stripQuotes(auth)
	pass = stripQuotes(pass)

	u := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
	if auth != "" {
		u.User = url.UserPassword(auth, pass)
	}
	return u.String()
}

// gsettingsGet 在 timeout 内执行 `gsettings get SCHEMA KEY`，返回原始字符串。
// 当 gsettings 不可用、超时或 key 缺失时返回 ( "", false )。
func gsettingsGet(schema, key string, timeout time.Duration) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gsettings", "get", schema, key)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// stripQuotes 去掉 gsettings 单引号包裹，例如 '127.0.0.1' → 127.0.0.1。
func stripQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	return s
}

// stripQuotesAndBrackets 把 GNOME 的 ['a','b'] 形式转换成 a,b。
func stripQuotesAndBrackets(s string) string {
	s = strings.TrimSpace(s)
	// gsettings 对空数组会输出 "@as []"；普通数组通常输出 "['a', 'b']"。
	if strings.HasPrefix(s, "@as ") {
		s = strings.TrimSpace(strings.TrimPrefix(s, "@as "))
	}
	if len(s) >= 2 && s[0] == '[' && s[len(s)-1] == ']' {
		s = s[1 : len(s)-1]
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = stripQuotes(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ",")
}

// --- KDE -------------------------------------------------------------------

func detectKDE() (Config, bool) {
	path := kioslavercPath()
	if path == "" {
		return Config{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, false
	}
	section, ok := parseINI(string(data), "Proxy Settings")
	if !ok {
		return Config{}, false
	}
	proxyType := strings.TrimSpace(section["ProxyType"])
	switch proxyType {
	case "0":
		return Config{Source: "kde"}, true
	case "1":
		// 手动。和 GNOME 一样保留 HTTP/HTTPS 各自独立的代理地址；
		// SOCKS 仅在对应协议未单独配置时兜底。
		var httpProxy, httpsProxy, socksProxy string
		for _, kv := range []struct {
			key    string
			scheme string
		}{
			{"httpProxy", "http"},
			{"httpsProxy", "http"},
			{"socksProxy", "socks5"},
		} {
			raw := strings.TrimSpace(section[kv.key])
			if raw == "" {
				continue
			}
			host, port := parseKDEHostPort(raw)
			if host == "" || port == "" {
				continue
			}
			proxyURL := (&url.URL{
				Scheme: kv.scheme,
				Host:   net.JoinHostPort(host, port),
			}).String()
			switch kv.key {
			case "httpProxy":
				httpProxy = proxyURL
			case "httpsProxy":
				httpsProxy = proxyURL
			case "socksProxy":
				socksProxy = proxyURL
			}
		}
		cfg := Config{
			HTTPProxyURL:  httpProxy,
			HTTPSProxyURL: httpsProxy,
			Bypass:        NormalizeBypass(section["NoProxyFor"]),
			Source:        "kde",
		}
		if httpProxy != "" {
			cfg.ProxyURL = httpProxy
		} else if httpsProxy != "" {
			cfg.ProxyURL = httpsProxy
		} else {
			cfg.ProxyURL = socksProxy
		}
		if socksProxy != "" && httpProxy == "" {
			cfg.HTTPProxyURL = socksProxy
		}
		if socksProxy != "" && httpsProxy == "" {
			cfg.HTTPSProxyURL = socksProxy
		}
		return cfg, true
	default:
		switch proxyType {
		case "2":
			return Config{
				Source:        "kde-pac",
				AutoConfigURL: strings.TrimSpace(section["Proxy Config Script"]),
			}, true
		case "3":
			return Config{Source: "kde-wpad", AutoDiscover: true}, true
		case "4":
			// 使用环境变量；上层 httpproxy 已单独处理。
			return Config{}, false
		default:
			return Config{}, false
		}
	}
}

func kioslavercPath() string {
	// Flatpak 把 XDG_CONFIG_HOME 改写到应用私有目录，而 finish-args
	// 通过 --filesystem=home 保留了宿主 ~/.config。必须直接读宿主
	// 路径，否则 KDE 用户系统里明明有代理，沙箱内却永远探测不到。
	if os.Getenv("FLATPAK_ID") != "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, ".config", "kioslaverc")
		}
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "kioslaverc")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "kioslaverc")
	}
	return ""
}

// parseINI 是极简的 INI 解析器，仅支持 [Section] + key=value 行，
// 用于解析 kioslaverc；不处理引号转义（kioslaverc 也不需要）。
func parseINI(content, section string) (map[string]string, bool) {
	inSection := false
	out := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inSection = strings.TrimSpace(line[1:len(line)-1]) == section
			continue
		}
		if !inSection {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		out[key] = val
	}
	return out, len(out) > 0
}

// parseKDEHostPort 解析 kioslaverc 中的代理条目：
//
//	http://127.0.0.1 7890   → 127.0.0.1, 7890
//	http://127.0.0.1:7890  → 127.0.0.1, 7890
//	127.0.0.1:7890          → 127.0.0.1, 7890
func parseKDEHostPort(raw string) (host, port string) {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	// 先尝试 host:port，再尝试 "host port"。
	if strings.Contains(raw, ":") {
		parts := strings.SplitN(raw, ":", 2)
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	parts := strings.Fields(raw)
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "", ""
}

// --- /etc/environment ------------------------------------------------------

func detectEtcEnvironment() (Config, bool) {
	data, err := os.ReadFile("/etc/environment")
	if err != nil {
		return Config{}, false
	}
	lines := strings.Split(string(data), "\n")
	var httpURL, httpsURL, bypass string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch strings.ToLower(key) {
		case "http_proxy", "httpproxy":
			httpURL = val
		case "https_proxy", "httpsproxy":
			httpsURL = val
		case "no_proxy", "noproxy":
			bypass = val
		}
	}
	if httpURL == "" && httpsURL == "" {
		return Config{}, false
	}
	cfg := Config{
		HTTPProxyURL:  normalizeScheme(httpURL, "http"),
		HTTPSProxyURL: normalizeScheme(httpsURL, "http"),
		Bypass:        NormalizeBypass(bypass),
		Source:        "/etc/environment",
	}
	if httpURL != "" {
		cfg.ProxyURL = cfg.HTTPProxyURL
	} else {
		cfg.ProxyURL = cfg.HTTPSProxyURL
	}
	return cfg, true
}

func normalizeScheme(raw, defaultScheme string) string {
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "socks5://") || strings.HasPrefix(lower, "socks://") {
		return raw
	}
	return defaultScheme + "://" + raw
}
