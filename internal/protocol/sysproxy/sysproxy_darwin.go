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
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, ":") {
			continue
		}
		eq := strings.Index(line, ":")
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		vals[key] = val
	}

	// PAC 启用但我们不解析 JS 脚本 —— 标记为不支持并直接返回。
	if vals["ProxyAutoConfigEnable"] == "1" && vals["ProxyAutoConfigURLString"] != "" {
		return Config{Source: "darwin-pac-unsupported"}, true
	}

	// 优先级 HTTPS > HTTP > SOCKS，与 macOS 系统设置面板一致。
	if vals["HTTPSEnable"] == "1" {
		if host := vals["HTTPSProxy"]; host != "" {
			if port := vals["HTTPSPort"]; port != "" && port != "0" {
				return Config{
					ProxyURL: "http://" + host + ":" + port,
					Bypass:   NormalizeBypass(vals["ExceptionsList"]),
					Source:   "darwin-scutil",
				}, true
			}
		}
	}
	if vals["HTTPEnable"] == "1" {
		if host := vals["HTTPProxy"]; host != "" {
			if port := vals["HTTPPort"]; port != "" && port != "0" {
				return Config{
					ProxyURL: "http://" + host + ":" + port,
					Bypass:   NormalizeBypass(vals["ExceptionsList"]),
					Source:   "darwin-scutil",
				}, true
			}
		}
	}
	if vals["SOCKSEnable"] == "1" {
		if host := vals["SOCKSProxy"]; host != "" {
			if port := vals["SOCKSPort"]; port != "" && port != "0" {
				return Config{
					ProxyURL: "socks5://" + host + ":" + port,
					Bypass:   NormalizeBypass(vals["ExceptionsList"]),
					Source:   "darwin-scutil",
				}, true
			}
		}
	}
	return Config{Source: "darwin-scutil"}, true
}
