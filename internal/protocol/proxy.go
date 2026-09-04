// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package protocol

import (
	"net/http"
	"net/url"
	"strings"
	"sync"

	"lgo_download_manager/internal/protocol/sysproxy"
)

// ProxyMode 决定驱动如何解析 HTTP/HTTPS/WebDAV 请求的代理。
type ProxyMode int

const (
	// ProxyModeSystem 使用系统/环境变量中的代理设置（HTTP_PROXY、
	// HTTPS_PROXY、NO_PROXY 等），未配置环境变量时直连。默认值。
	ProxyModeSystem ProxyMode = iota
	// ProxyModeDisabled 始终直连，不走任何代理，忽略环境变量。
	ProxyModeDisabled
	// ProxyModeManual 使用 ProxyURL 字段中配置的代理地址；为空时
	// 退化为直连（视同 Disabled 但保留模式以便用户在 UI 中调整）。
	ProxyModeManual
)

// String 返回 ProxyMode 的稳定字符串表示，用于持久化。
func (m ProxyMode) String() string {
	switch m {
	case ProxyModeDisabled:
		return "disabled"
	case ProxyModeManual:
		return "manual"
	default:
		return "system"
	}
}

// ParseProxyMode 从字符串反向解析 ProxyMode，无法识别时回退到 System。
func ParseProxyMode(s string) ProxyMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "disabled", "off", "none":
		return ProxyModeDisabled
	case "manual", "custom":
		return ProxyModeManual
	default:
		return ProxyModeSystem
	}
}

// ProxyConfig 是协议包级别的代理配置，作为驱动在 AuthOptions 未显式
// 指定代理时的默认值。典型用法：在应用启动时由 UI 从持久化设置写入
// 一次，所有后续创建的驱动都会读到同一份配置。
//
// 读写由 SetProxyConfig / GetProxyConfig 提供；调用方负责并发安全，
// 通常仅在启动阶段或用户在设置中改动时写入一次，因此无需加锁。
type ProxyConfig struct {
	Mode        ProxyMode // 代理模式：System / Disabled / Manual
	ProxyURL    string    // 仅在 Mode == Manual 时生效
	ProxyBypass string    // 始终生效（即便走系统/直连路径）
}

var globalProxy ProxyConfig

// SetProxyConfig 覆盖包级别的代理配置。所有之后构造的驱动都会
// 在其 AuthOptions 没有显式代理时使用该配置。
func SetProxyConfig(c ProxyConfig) { globalProxy = c }

// GetProxyConfig 返回当前生效的包级代理配置。
func GetProxyConfig() ProxyConfig { return globalProxy }

// effectiveAuth 返回合并后的代理配置：优先使用 AuthOptions 中
// 非零值的字段；为空时回退到包级全局代理。
//
// ProxyMode 的合并规则：AuthOptions 显式指定（非零值）优先；零值时
// 采用全局模式；最终得到的模式决定 ProxyURL/ProxyBypass 的解释方式。
func effectiveAuth(auth AuthOptions) AuthOptions {
	if auth.ProxyMode != 0 || strings.TrimSpace(auth.ProxyURL) != "" || strings.TrimSpace(auth.ProxyBypass) != "" {
		if auth.ProxyMode == 0 {
			auth.ProxyMode = globalProxy.Mode
		}
		if strings.TrimSpace(auth.ProxyURL) == "" {
			auth.ProxyURL = globalProxy.ProxyURL
		}
		if strings.TrimSpace(auth.ProxyBypass) == "" {
			auth.ProxyBypass = globalProxy.ProxyBypass
		}
		return auth
	}
	auth.ProxyMode = globalProxy.Mode
	auth.ProxyURL = globalProxy.ProxyURL
	auth.ProxyBypass = globalProxy.ProxyBypass
	return auth
}

// proxyFunc 构造一个 http.Transport.Proxy 函数，依据 opt 中的代理模式
// 与地址返回对应的解析器。
//
// 返回值说明：
//   - nil：调用方应使用 http.ProxyFromEnvironment（由 net/http 默认行为）
//   - 返回固定 *url.URL 的函数：所有请求都走该代理
//   - 返回动态函数：根据请求主机动态决定是否走代理/直连
func proxyFunc(opt AuthOptions) func(*http.Request) (*url.URL, error) {
	mode := opt.ProxyMode
	if mode == 0 {
		// 兼容历史调用：未设模式时按"有 URL 视为 Manual、否则 System"
		// 推断；正常路径上 effectiveAuth 已经填好 Mode。
		if strings.TrimSpace(opt.ProxyURL) != "" {
			mode = ProxyModeManual
		} else {
			mode = ProxyModeSystem
		}
	}
	bypass := parseBypassList(opt.ProxyBypass)

	switch mode {
	case ProxyModeDisabled:
		// 始终直连；bypass 在此模式下无意义，但仍保留以备未来扩展。
		return func(*http.Request) (*url.URL, error) { return nil, nil }

	case ProxyModeManual:
		rawProxy := strings.TrimSpace(opt.ProxyURL)
		if rawProxy == "" {
			// Manual 模式但未配置地址：退化为直连，避免误用环境变量。
			return func(*http.Request) (*url.URL, error) { return nil, nil }
		}
		parsed, err := url.Parse(rawProxy)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			// 配置不合法：安全起见直连，并保留 bypass 语义在旁路生效。
			return func(*http.Request) (*url.URL, error) { return nil, nil }
		}
		if bypass == nil {
			return func(*http.Request) (*url.URL, error) { return parsed, nil }
		}
		return func(req *http.Request) (*url.URL, error) {
			if bypass.matches(req.URL.Hostname()) {
				return nil, nil
			}
			return parsed, nil
		}

	default: // ProxyModeSystem
		// System 模式：先看 HTTP_PROXY/HTTPS_PROXY/NO_PROXY 等环境变量；
		// 若环境完全没设，调用 sysproxy.Detect() 探测当前桌面会话的
		// 真实代理设置（Linux GNOME/KDE、macOS、WinINET）。这样在
		// 没有 .bashrc 代理配置的桌面用户也能"开箱即用"地走代理。
		if bypass == nil {
			return systemProxyFunc()
		}
		return func(req *http.Request) (*url.URL, error) {
			if bypass.matches(req.URL.Hostname()) {
				return nil, nil
			}
			return systemProxyCall(req)
		}
	}
}

// systemProxyCall 实现 per-request 的"系统代理"解析：先尝试
// http.ProxyFromEnvironment，若环境完全没设且未返回代理，再回退到
func systemProxyCall(req *http.Request) (*url.URL, error) {
	// 先看 HTTP_PROXY/HTTPS_PROXY/NO_PROXY 等环境变量（含 NO_PROXY 语义）；
	// 若环境完全没设，再回退到 sysproxy.Detect() 探测桌面会话的真实代理。
	if u, _ := http.ProxyFromEnvironment(req); u != nil {
		return u, nil
	}
	if u, ok := cachedSystemProxy(); ok && u != nil {
		return u, nil
	}
	return nil, nil
}

// systemProxyFunc 与 systemProxyCall 等价，但返回零参数签名供无 bypass 路径使用。
func systemProxyFunc() func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		return systemProxyCall(req)
	}
}
var (
	systemProxyOnce  sync.Once
	systemProxyCache *url.URL
	systemProxyOK    bool
)

// cachedSystemProxy 第一次调用时执行 sysproxy.Detect() 并缓存结果。
// 缓存粒度为进程级；若用户在程序运行中切换了桌面代理，需要重启进程
// 或调用 InvalidateSystemProxyCache()（在 settings_dialog 切到 System
// 模式时可调用一次）。
func cachedSystemProxy() (*url.URL, bool) {
	systemProxyOnce.Do(func() {
		cfg, ok := sysproxy.Detect()
		if !ok || cfg.ProxyURL == "" {
			systemProxyCache = nil
			systemProxyOK = false
			return
		}
		u, err := url.Parse(cfg.ProxyURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			systemProxyCache = nil
			systemProxyOK = false
			return
		}
		systemProxyCache = u
		systemProxyOK = true
	})
	return systemProxyCache, systemProxyOK
}

// InvalidateSystemProxyCache 清除进程内的系统代理缓存，使下一次
// 解析重新执行探测。UI 在用户切换代理模式或显式刷新时可调用。
func InvalidateSystemProxyCache() {
	systemProxyOnce = sync.Once{}
	systemProxyCache = nil
	systemProxyOK = false
}
// bypassList 是逗号分隔的主机/后缀集合，匹配逻辑与浏览器一致：
//   - 完全相等（例如 "example.com"）
//   - 后缀匹配（例如 "*.example.com" 或 ".example.com" 匹配 a.example.com）
//
// 仅匹配主机名部分，不涉及端口。
type bypassList []string

func parseBypassList(s string) bypassList {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make(bypassList, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		// 去掉前导 "*." 或 "."，统一以 ".<rest>" 形式存储后缀。
		p = strings.TrimPrefix(p, "*.")
		p = strings.TrimPrefix(p, ".")
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// matches 报告 host 是否被绕过。host 应已是小写化的纯主机名（无端口）。
func (b bypassList) matches(host string) bool {
	if len(b) == 0 || host == "" {
		return false
	}
	host = strings.ToLower(host)
	for _, entry := range b {
		entry = strings.ToLower(entry)
		if host == entry {
			return true
		}
		if strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}
