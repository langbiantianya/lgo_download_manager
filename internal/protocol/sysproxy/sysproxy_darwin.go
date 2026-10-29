// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build darwin

package sysproxy

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// detectDarwin 通过 `scutil --proxy` 读取 macOS 系统代理设置。
// 凭据（用户名/密码）保存在 Keychain 中，scutil 不会暴露，所以
// 结果中不会包含 userinfo。
func detectDarwin() (Config, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "scutil", "--proxy").Output()
	if err != nil {
		return Config{}, false
	}
	return parseScutil(string(out))
}

// parseScutil 解析 `scutil --proxy` 的键值输出。
//
// 典型输出：
//
//	<dictionary> {
//	  HTTPEnable : 0
//	  HTTPProxy  :
//	  HTTPPort   : 0
//	  HTTPSEnable : 1
//	  HTTPSProxy : 127.0.0.1
//	  HTTPSPort  : 7890
//	  SOCKSEnable : 0
//	  ExceptionsList : <array> { ... }
//	  ProxyAutoConfigEnable : 0
//	  ProxyAutoConfigURLString :
//	}
func parseScutil(s string) (Config, bool) {
	vals := map[string]string{}
	var exceptions []string
	inExceptions := false
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ExceptionsList") && strings.Contains(line, ":") {
			inExceptions = true
			continue
		}
		if inExceptions {
			if line == "}" {
				inExceptions = false
				continue
			}
			if idx := strings.Index(line, ":"); idx >= 0 {
				if value := strings.TrimSpace(line[idx+1:]); value != "" {
					exceptions = append(exceptions, strings.Trim(value, `"`))
				}
			}
			continue
		}
		if !strings.Contains(line, ":") {
			continue
		}
		eq := strings.Index(line, ":")
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		vals[key] = val
	}

	// PAC 脚本交给上层执行；URL 为空时按 WPAD 自动发现处理。
	if vals["ProxyAutoConfigEnable"] == "1" && vals["ProxyAutoConfigURLString"] != "" {
		return Config{
			Source:        "darwin-pac",
			AutoConfigURL: strings.Trim(vals["ProxyAutoConfigURLString"], `"`),
			Bypass:        NormalizeBypass(strings.Join(exceptions, ",")),
		}, true
	}
	if vals["ProxyAutoDiscoveryEnable"] == "1" {
		return Config{
			Source:       "darwin-wpad",
			AutoDiscover: true,
			Bypass:       NormalizeBypass(strings.Join(exceptions, ",")),
		}, true
	}
	bypassParts := exceptions
	if vals["ExcludeSimpleHostnames"] == "1" {
		bypassParts = append(bypassParts, "<local>")
	}
	bypass := NormalizeBypass(strings.Join(bypassParts, ","))

	// 先分别收集三种代理，再选一个默认地址。HTTP 与 HTTPS 可以指向
	// 不同端口，不能只保留系统设置面板里的“首选”那个。
	var httpProxy, httpsProxy, socksProxy string
	if vals["HTTPSEnable"] == "1" {
		if host := vals["HTTPSProxy"]; host != "" {
			if port := vals["HTTPSPort"]; port != "" && port != "0" {
				httpsProxy = "http://" + host + ":" + port
			}
		}
	}
	if vals["HTTPEnable"] == "1" {
		if host := vals["HTTPProxy"]; host != "" {
			if port := vals["HTTPPort"]; port != "" && port != "0" {
				httpProxy = "http://" + host + ":" + port
			}
		}
	}
	if vals["SOCKSEnable"] == "1" {
		if host := vals["SOCKSProxy"]; host != "" {
			if port := vals["SOCKSPort"]; port != "" && port != "0" {
				socksProxy = "socks5://" + host + ":" + port
			}
		}
	}
	cfg := Config{
		HTTPProxyURL:  httpProxy,
		HTTPSProxyURL: httpsProxy,
		Bypass:        bypass,
		Source:        "darwin-scutil",
	}
	switch {
	case httpsProxy != "":
		cfg.ProxyURL = httpsProxy
	case httpProxy != "":
		cfg.ProxyURL = httpProxy
	default:
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
