// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package sysproxy 解析当前桌面会话的系统代理设置。
//
// 支持范围：
//   - Linux（GNOME 与 KDE，X11 与 Wayland 同源；不区分显示服务器）
//   - macOS（通过 scutil 读取系统设置）
//   - Windows（通过注册表读取 WinINET 设置）
//
// 不支持：PAC / WPAD 自动配置代理脚本解析（涉及 JS 引擎，超出本项目范围）。
//
// Detect() 每次调用都会重新查询系统；若桌面代理在程序运行期间被修改，
// 调用方需要主动重新读取（例如监听设置变化事件）。
package sysproxy

import (
	"fmt"
	"strings"
)

// Config 是从系统代理设置中读取到的结果。
//
// ProxyURL 是 "http://host:port" 或 "socks5://host:port" 形式，可直接
// 交给 http.Transport.Proxy / net/http 的 Proxy 函数。空字符串表示
// 系统当前未配置可用代理。
//
// Bypass 是分号或逗号分隔的绕过列表（Windows 用 ';'，GNOME/KDE/scutil 都用 ','）。
// 调用方应使用与 NO_PROXY 一致的解析规则；本包不强制具体形式，仅做归一化。
//
// Source 用于诊断输出（UI 中可显示"当前来源：GNOME"）。
type Config struct {
	ProxyURL string
	Bypass   string
	Source   string
}

// String 返回便于日志/UI 展示的简短字符串。
func (c Config) String() string {
	if c.ProxyURL == "" {
		return "direct"
	}
	src := c.Source
	if src == "" {
		src = "system"
	}
	return fmt.Sprintf("%s via %s", c.ProxyURL, src)
}

// NormalizeBypass 把多平台常见的 ',' / ';' / 空白分隔统一转换为逗号形式，
// 去除空项，方便上游 bypass 解析器（protocol.proxyFunc）直接复用。
func NormalizeBypass(s string) string {
	if s == "" {
		return ""
	}
	// 统一分隔符为逗号（Windows 风格使用分号）。
	s = strings.ReplaceAll(s, ";", ",")
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ",")
}

// Detect 读取当前桌面会话的系统代理设置并返回归一化结果。
//
// 返回 (cfg, true) 表示找到系统代理配置（即便 ProxyURL 为空 —— 此时
// 表示"系统明确禁用了代理"或"PAC 不在支持范围"）。返回 (_, false) 表示
// 当前平台/会话无任何已知代理来源，调用方应回退到环境变量。
//
// 实现按平台分派到 detectLinux / detectDarwin / detectWindows，
// 不支持的平台走 stub。
func Detect() (Config, bool) {
	return detectPlatform()
}
