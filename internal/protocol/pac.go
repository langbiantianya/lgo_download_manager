// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package protocol

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

const (
	pacCacheTTL       = 5 * time.Minute
	pacNegativeTTL    = 30 * time.Second
	pacEvaluateLimit  = 2 * time.Second
	maxPACScriptBytes = 1 << 20
)

type cachedPAC struct {
	program *goja.Program
	err     error
	expires time.Time
}

var (
	pacCacheMu sync.Mutex
	pacCache   = map[string]cachedPAC{}
)

// resolvePACForRequest 解析系统/显式 PAC。环境变量优先于桌面设置，
// 便于 Flatpak、CI 或无桌面会话明确指定脚本。WPAD 只访问标准
// http://wpad/wpad.dat；不会执行未受信任的本地文件。
func resolvePACForRequest(req *http.Request, override string, system *systemProxySnapshot) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil
	}
	pacURL := firstNonEmpty(
		os.Getenv("AUTO_PROXY_URL"),
		os.Getenv("PAC_URL"),
		override,
	)
	if pacURL == "" && system != nil {
		pacURL = system.pacURL
		if pacURL == "" && system.wpads {
			pacURL = "wpad"
		}
	}
	pacURL = normalizePACURL(pacURL)
	if pacURL == "" {
		return nil, nil
	}

	result, err := evaluatePAC(req.Context(), pacURL, req.URL)
	if err != nil {
		// 损坏或被网络隔离的 PAC 不应让所有下载永久失败。返回直连，
		// 同时保留负缓存，避免每个分片都重复等待脚本超时。
		return nil, nil
	}
	return parsePACResult(result, req.URL)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func normalizePACURL(raw string) string {
	raw = strings.TrimSpace(raw)
	switch strings.ToLower(raw) {
	case "":
		return ""
	case "wpad", "auto", "wpad://":
		return "http://wpad/wpad.dat"
	}
	return raw
}

func loadPACProgram(ctx context.Context, pacURL string) (*goja.Program, error) {
	now := time.Now()
	pacCacheMu.Lock()
	if cached, ok := pacCache[pacURL]; ok && now.Before(cached.expires) {
		pacCacheMu.Unlock()
		return cached.program, cached.err
	}
	pacCacheMu.Unlock()

	program, err := fetchPACProgram(ctx, pacURL)
	entry := cachedPAC{program: program, err: err, expires: now.Add(pacCacheTTL)}
	if err != nil {
		entry.expires = now.Add(pacNegativeTTL)
	}
	pacCacheMu.Lock()
	pacCache[pacURL] = entry
	pacCacheMu.Unlock()
	return program, err
}

func fetchPACProgram(ctx context.Context, pacURL string) (*goja.Program, error) {
	u, err := url.Parse(pacURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("pac: invalid URL %q", pacURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := pacHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("pac: status %d", resp.StatusCode)
	}
	source, err := io.ReadAll(io.LimitReader(resp.Body, maxPACScriptBytes+1))
	if err != nil {
		return nil, err
	}
	if len(source) > maxPACScriptBytes {
		return nil, fmt.Errorf("pac: script exceeds %d bytes", maxPACScriptBytes)
	}
	return goja.Compile(u.String(), string(source), false)
}

var pacHTTPClient = func() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return &http.Client{
		Transport: tr,
		Timeout:   10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("pac: too many redirects")
			}
			return nil
		},
	}
}()

func evaluatePAC(ctx context.Context, pacURL string, target *url.URL) (string, error) {
	program, err := loadPACProgram(ctx, pacURL)
	if err != nil {
		return "", err
	}
	runtime := goja.New()
	installPACFunctions(runtime, ctx)
	interrupt := time.AfterFunc(pacEvaluateLimit, func() {
		runtime.Interrupt("PAC evaluation timed out")
	})
	defer interrupt.Stop()

	if _, err := runtime.RunProgram(program); err != nil {
		return "", fmt.Errorf("pac execute: %w", err)
	}
	fn, ok := goja.AssertFunction(runtime.Get("FindProxyForURL"))
	if !ok {
		return "", fmt.Errorf("pac: FindProxyForURL is missing")
	}
	value, err := fn(goja.Undefined(), runtime.ToValue(target.String()), runtime.ToValue(target.Hostname()))
	if err != nil {
		return "", fmt.Errorf("pac FindProxyForURL: %w", err)
	}
	return value.String(), nil
}

func installPACFunctions(runtime *goja.Runtime, ctx context.Context) {
	_ = runtime.Set("alert", func(any) {})
	_ = runtime.Set("isPlainHostName", func(host string) bool {
		return !strings.Contains(host, ".")
	})
	_ = runtime.Set("dnsDomainIs", func(host, domain string) bool {
		return strings.HasSuffix(strings.ToLower(host), strings.ToLower(domain))
	})
	_ = runtime.Set("localHostOrDomainIs", func(host, hostdom string) bool {
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		hostdom = strings.ToLower(strings.TrimSuffix(hostdom, "."))
		return host == hostdom || strings.HasPrefix(hostdom, host+".")
	})
	_ = runtime.Set("dnsDomainLevels", func(host string) int {
		return strings.Count(host, ".")
	})
	_ = runtime.Set("shExpMatch", func(value, pattern string) bool {
		matched, _ := path.Match(pattern, value)
		return matched
	})
	_ = runtime.Set("dnsResolve", func(host string) string {
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil || len(addrs) == 0 {
			return ""
		}
		return addrs[0]
	})
	_ = runtime.Set("isResolvable", func(host string) bool {
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		return err == nil && len(addrs) > 0
	})
	_ = runtime.Set("myIpAddress", func() string { return firstLocalIP() })
	_ = runtime.Set("isInNet", func(host, pattern, mask string) bool {
		ip := net.ParseIP(host)
		if ip == nil {
			addrs, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
			if err != nil || len(addrs) == 0 {
				return false
			}
			ip = addrs[0]
		}
		ip4, p4, m4 := ip.To4(), net.ParseIP(pattern).To4(), net.ParseIP(mask).To4()
		if ip4 == nil || p4 == nil || m4 == nil {
			return false
		}
		for i := 0; i < 4; i++ {
			if ip4[i]&m4[i] != p4[i]&m4[i] {
				return false
			}
		}
		return true
	})
	_ = runtime.Set("weekdayRange", func(args ...string) bool {
		now := time.Now().In(pacLocation(args))
		return pacRangeContains(args, []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"}, int(now.Weekday()))
	})
	_ = runtime.Set("dateRange", func(args ...string) bool {
		now := time.Now().In(pacLocation(args))
		return pacNumericRangeContains(args, now.Day())
	})
	_ = runtime.Set("timeRange", func(args ...string) bool {
		now := time.Now().In(pacLocation(args))
		return pacNumericRangeContains(args, now.Hour())
	})
}

func firstLocalIP() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return "127.0.0.1"
}

func pacLocation(args []string) *time.Location {
	if len(args) > 0 && strings.EqualFold(args[len(args)-1], "GMT") {
		return time.UTC
	}
	return time.Local
}

func pacRangeContains(args, names []string, current int) bool {
	if len(args) == 0 {
		return false
	}
	toIndex := func(value string) (int, bool) {
		value = strings.ToUpper(value)
		for i, name := range names {
			if value == name {
				return i, true
			}
		}
		return 0, false
	}
	first, ok := toIndex(args[0])
	if !ok {
		return false
	}
	last := first
	if len(args) > 1 && !strings.EqualFold(args[1], "GMT") {
		if parsed, valid := toIndex(args[1]); valid {
			last = parsed
		}
	}
	if first <= last {
		return current >= first && current <= last
	}
	return current >= first || current <= last
}

func pacNumericRangeContains(args []string, current int) bool {
	if len(args) == 0 {
		return false
	}
	first, err := strconv.Atoi(args[0])
	if err != nil {
		return false
	}
	last := first
	if len(args) > 1 && !strings.EqualFold(args[1], "GMT") {
		if parsed, err := strconv.Atoi(args[1]); err == nil {
			last = parsed
		}
	}
	if first <= last {
		return current >= first && current <= last
	}
	return current >= first || current <= last
}

func parsePACResult(result string, target *url.URL) (*url.URL, error) {
	for _, directive := range strings.Split(result, ";") {
		fields := strings.Fields(strings.TrimSpace(directive))
		if len(fields) == 0 {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "DIRECT":
			return nil, nil
		case "PROXY", "HTTP", "HTTPS", "SOCKS", "SOCKS5":
			if len(fields) < 2 {
				continue
			}
			endpoint := fields[1]
			if _, _, err := net.SplitHostPort(endpoint); err != nil {
				endpoint = net.JoinHostPort(endpoint, pacDefaultPort(fields[0]))
			}
			scheme := "http"
			switch strings.ToUpper(fields[0]) {
			case "HTTPS":
				scheme = "https"
			case "SOCKS", "SOCKS5":
				scheme = "socks5"
			}
			proxyURL := &url.URL{Scheme: scheme, Host: endpoint}
			if target != nil && target.Host != "" {
				// url.URL.Proxy contract does not require target credentials;
				// keep the proxy URL clean to avoid leaking request userinfo.
				proxyURL.RawQuery = ""
			}
			return proxyURL, nil
		}
	}
	return nil, nil
}

func pacDefaultPort(directive string) string {
	switch strings.ToUpper(directive) {
	case "HTTPS":
		return "443"
	case "SOCKS", "SOCKS5":
		return "1080"
	default:
		return "80"
	}
}

// InvalidateProxyCache 清除 PAC 脚本和系统代理快照。设置页保存后调用，
// 使新的代理地址立即生效。
func InvalidateProxyCache() {
	InvalidateSystemProxyCache()
	pacCacheMu.Lock()
	pacCache = map[string]cachedPAC{}
	pacCacheMu.Unlock()
}
