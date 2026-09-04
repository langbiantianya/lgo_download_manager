// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build linux

package sysproxy

import (
	"context"
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
	switch mode {
	case "manual":
		return gnomeManual()
	case "auto":
		// PAC/WPAD 不在支持范围内；回退到 direct。
		return Config{Source: "gnome-pac-unsupported"}, true
	case "'none'":
		return Config{Source: "gnome"}, true
	default:
		return Config{}, false
	}
}

func gnomeManual() (Config, bool) {
	// http 优先；缺失则降级到 https；再降级到 socks。
	for _, scheme := range []struct {
		schemaKey string
		scheme    string
	}{
		{"org.gnome.system.proxy.http", "http"},
		{"org.gnome.system.proxy.https", "http"},
		{"org.gnome.system.proxy.socks", "socks5"},
	} {
		host, hok := gsettingsGet(scheme.schemaKey, "host", 5*time.Second)
		port, pok := gsettingsGet(scheme.schemaKey, "port", 5*time.Second)
		if !hok || !pok || host == "" {
			continue
		}
		// gsettings 输出常常带引号，例如 '127.0.0.1' 或 7890
		host = stripQuotes(host)
		port = stripQuotes(port)
		if host == "" || port == "" {
			continue
		}
		auth, _ := gsettingsGet(scheme.schemaKey, "authentication-user", 5*time.Second)
		pass, _ := gsettingsGet(scheme.schemaKey, "authentication-password", 5*time.Second)
		auth = stripQuotes(auth)
		pass = stripQuotes(pass)
		userInfo := ""
		if auth != "" {
			userInfo = auth
			if pass != "" {
				userInfo += ":" + pass
			}
			userInfo += "@"
		}
		bypass, _ := gsettingsGet("org.gnome.system.proxy", "ignore-hosts", 5*time.Second)
		bypass = stripQuotesAndBrackets(bypass)
		return Config{
			ProxyURL: scheme.scheme + "://" + userInfo + host + ":" + port,
			Bypass:   NormalizeBypass(bypass),
			Source:   "gnome",
		}, true
	}
	return Config{Source: "gnome"}, true
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
		// 手动
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
			return Config{
				ProxyURL: kv.scheme + "://" + host + ":" + port,
				Bypass:   NormalizeBypass(section["NoProxyFor"]),
				Source:   "kde",
			}, true
		}
		return Config{Source: "kde"}, true
	default:
		// 2=PAC, 3=WPAD, 4=use env（后者由 httpproxy.FromEnvironment 处理）
		return Config{}, false
	}
}

func kioslavercPath() string {
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
	chosen := httpURL
	scheme := "http"
	if chosen == "" {
		chosen = httpsURL
	}
	if chosen == "" {
		return Config{}, false
	}
	if strings.HasPrefix(strings.ToLower(chosen), "socks5://") || strings.HasPrefix(strings.ToLower(chosen), "socks://") {
		scheme = "socks5"
	}
	return Config{
		ProxyURL: normalizeScheme(chosen, scheme),
		Bypass:   NormalizeBypass(bypass),
		Source:   "/etc/environment",
	}, true
}

func normalizeScheme(raw, defaultScheme string) string {
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "socks5://") || strings.HasPrefix(lower, "socks://") {
		return raw
	}
	return defaultScheme + "://" + raw
}
