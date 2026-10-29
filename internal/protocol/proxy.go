// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package protocol

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"
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
// 读写由 SetProxyConfig / GetProxyConfig 提供，并允许设置页与正在
// 下载的请求并发访问。
type ProxyConfig struct {
	Mode        ProxyMode // 代理模式：System / Disabled / Manual
	ProxyURL    string    // 仅在 Mode == Manual 时生效
	ProxyBypass string    // 始终生效（即便走系统/直连路径）
}

var (
	globalProxyMu sync.RWMutex
	globalProxy   ProxyConfig
)

// SetProxyConfig 覆盖包级别的代理配置。所有之后构造的驱动都会
// 在其 AuthOptions 没有显式代理时使用该配置。
func SetProxyConfig(c ProxyConfig) {
	globalProxyMu.Lock()
	globalProxy = c
	globalProxyMu.Unlock()
}

// GetProxyConfig 返回当前生效的包级代理配置。
func GetProxyConfig() ProxyConfig {
	globalProxyMu.RLock()
	defer globalProxyMu.RUnlock()
	return globalProxy
}

// effectiveAuth 返回合并后的代理配置：优先使用 AuthOptions 中
// 非零值的字段；为空时回退到包级全局代理。
//
// ProxyMode 的合并规则：AuthOptions 显式指定（非零值）优先；零值时
// 采用全局模式；最终得到的模式决定 ProxyURL/ProxyBypass 的解释方式。
func effectiveAuth(auth AuthOptions) AuthOptions {
	global := GetProxyConfig()
	if auth.ProxyMode != 0 || strings.TrimSpace(auth.ProxyURL) != "" || strings.TrimSpace(auth.ProxyBypass) != "" {
		if auth.ProxyMode == 0 {
			auth.ProxyMode = global.Mode
		}
		if strings.TrimSpace(auth.ProxyURL) == "" {
			auth.ProxyURL = global.ProxyURL
		}
		if strings.TrimSpace(auth.ProxyBypass) == "" {
			auth.ProxyBypass = global.ProxyBypass
		}
		return auth
	}
	auth.ProxyMode = global.Mode
	auth.ProxyURL = global.ProxyURL
	auth.ProxyBypass = global.ProxyBypass
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
	bypassRaw := strings.TrimSpace(opt.ProxyBypass)
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
		if len(bypass) == 0 {
			return func(*http.Request) (*url.URL, error) { return parsed, nil }
		}
		cfg := &httpproxy.Config{
			HTTPProxy:  parsed.String(),
			HTTPSProxy: parsed.String(),
			NoProxy:    bypassRaw,
		}
		fn := cfg.ProxyFunc()
		return func(req *http.Request) (*url.URL, error) {
			if bypassesHost(req.URL, cfg.NoProxy) {
				return nil, nil
			}
			return fn(req.URL)
		}

	default: // ProxyModeSystem
		// System 模式：先看 HTTP_PROXY/HTTPS_PROXY/ALL_PROXY/NO_PROXY，
		// 再回退到桌面会话的代理设置。用户 bypass 与系统 bypass 会合并。
		return func(req *http.Request) (*url.URL, error) {
			return systemProxyCall(req, bypassRaw)
		}
	}
}

// systemProxyCall 按请求解析系统代理。环境变量配置了当前协议的代理时
// 以环境为准；若当前请求被 NO_PROXY 命中，不会错误地继续落到桌面代理。
// 环境没有该协议的代理时才读取桌面会话设置。
func systemProxyCall(req *http.Request, configuredBypass string) (*url.URL, error) {
	env := environmentProxyConfig()
	env.NoProxy = joinNoProxy(env.NoProxy, configuredBypass)
	env.CGI = false
	if bypassesHost(req.URL, env.NoProxy) {
		return nil, nil
	}
	envFn := env.ProxyFunc()
	u, err := envFn(req.URL)
	if err != nil || u != nil {
		return u, err
	}
	if environmentProxyForScheme(env, req.URL.Scheme) != "" {
		// 有代理配置却返回 nil，说明被 NO_PROXY 命中，不能用桌面代理
		// 绕过用户明确写下的例外。
		return nil, nil
	}

	system := cachedSystemProxy()
	if system == nil {
		return nil, nil
	}
	cfg := &httpproxy.Config{
		HTTPProxy:  system.httpProxy,
		HTTPSProxy: system.httpsProxy,
		NoProxy:    joinNoProxy(system.noProxy, env.NoProxy),
	}
	if bypassesHost(req.URL, cfg.NoProxy) {
		return nil, nil
	}
	return cfg.ProxyFunc()(req.URL)
}

// HTTPProxyFunc 返回一个可直接赋给 http.Transport.Proxy 或
// torrent.ClientConfig.HTTPProxy 的动态代理解析函数。每次请求重新
// 读取包级代理配置，因此设置页修改代理后，长期存活的 BT 客户端无需
// 重建就能让 tracker/webseed 使用新配置。
//
// 与驱动内部使用的 proxyFunc 语义完全一致(含 bypass 列表与系统代理
// 探测),使 BitTorrent 的 tracker 公告、webseed、元数据 HTTP 抓取
// 与分片下载共用同一套代理设置。
func HTTPProxyFunc() func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		fn := proxyFunc(effectiveAuth(AuthOptions{}))
		if fn == nil {
			return nil, nil
		}
		return fn(req)
	}
}

// systemProxyCacheTTL 限制桌面代理快照的寿命。系统代理属于易变状态，
// 进程级永久缓存会让用户在系统设置中切换代理后仍继续走旧端点；同时
// 也不宜每个分片请求都执行 gsettings/scutil/注册表探测。
const systemProxyCacheTTL = 30 * time.Second

type systemProxySnapshot struct {
	httpProxy  string
	httpsProxy string
	noProxy    string
}

var (
	systemProxyMu      sync.Mutex
	systemProxyValue   *systemProxySnapshot
	systemProxyExpires time.Time
	detectSystemProxy  = sysproxy.Detect
)

// cachedSystemProxy 返回 30 秒内的桌面代理快照；过期后重新探测。
// 锁同时保护缓存读写与 InvalidateSystemProxyCache，避免设置页保存时
// 与下载 goroutine 发生数据竞争。
func cachedSystemProxy() *systemProxySnapshot {
	systemProxyMu.Lock()
	defer systemProxyMu.Unlock()
	if time.Now().Before(systemProxyExpires) {
		return systemProxyValue
	}

	cfg, ok := detectSystemProxy()
	systemProxyValue = nil
	if ok {
		httpProxy := cfg.ProxyForScheme("http")
		httpsProxy := cfg.ProxyForScheme("https")
		if httpProxy != "" || httpsProxy != "" {
			systemProxyValue = &systemProxySnapshot{
				httpProxy:  httpProxy,
				httpsProxy: httpsProxy,
				noProxy:    cfg.Bypass,
			}
		}
	}
	systemProxyExpires = time.Now().Add(systemProxyCacheTTL)
	return systemProxyValue
}

// InvalidateSystemProxyCache 清除进程内的系统代理缓存，使下一次
// 解析重新执行探测。UI 在用户切换代理模式或显式刷新时可调用。
func InvalidateSystemProxyCache() {
	systemProxyMu.Lock()
	systemProxyValue = nil
	systemProxyExpires = time.Time{}
	systemProxyMu.Unlock()
}

// environmentProxyConfig 读取当前环境变量，并补齐 Go 标准库未单独
// 支持的 ALL_PROXY。每次都重新读取，避免 http.ProxyFromEnvironment
// 的进程级缓存让运行中修改的环境变量失效。
func environmentProxyConfig() *httpproxy.Config {
	cfg := httpproxy.FromEnvironment()
	all := strings.TrimSpace(os.Getenv("ALL_PROXY"))
	if all == "" {
		all = strings.TrimSpace(os.Getenv("all_proxy"))
	}
	if cfg.HTTPProxy == "" {
		cfg.HTTPProxy = all
	}
	if cfg.HTTPSProxy == "" {
		cfg.HTTPSProxy = all
	}
	return cfg
}

func environmentProxyForScheme(cfg *httpproxy.Config, scheme string) string {
	if strings.EqualFold(scheme, "https") {
		return cfg.HTTPSProxy
	}
	if strings.EqualFold(scheme, "http") {
		return cfg.HTTPProxy
	}
	return ""
}

func joinNoProxy(parts ...string) string {
	var nonEmpty []string
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return sysproxy.NormalizeBypass(strings.Join(nonEmpty, ","))
}

// bypassesHost 补充 x/net/http/httpproxy 不覆盖的两类常见系统规则：
//   - Windows/macOS 的 "<local>" 单标签主机名；
//   - "192.168.*" 这类普通通配符（x/net 只识别 CIDR 与 "*.domain"）。
func bypassesHost(u *url.URL, noProxy string) bool {
	if u == nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	hostPort := strings.ToLower(u.Host)
	for _, entry := range strings.Split(sysproxy.NormalizeBypass(noProxy), ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		switch {
		case entry == "<local>":
			if host != "" && net.ParseIP(host) == nil && !strings.Contains(host, ".") {
				return true
			}
		case strings.Contains(entry, "*"):
			if matched, _ := path.Match(entry, host); matched {
				return true
			}
			if matched, _ := path.Match(entry, hostPort); matched {
				return true
			}
		}
	}
	return false
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
