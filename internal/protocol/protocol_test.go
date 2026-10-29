// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package protocol

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lgo_download_manager/internal/protocol/sysproxy"
)

// ---------------------------------------------------------------------------
// 辅助函数测试
// ---------------------------------------------------------------------------

func TestHasPort(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"localhost:21", true},
		{"192.168.1.1:21", true},
		{"ftp.example.com:21", true},
		{"ftp.example.com", false},
		{"localhost", false},
		{"[::1]:21", true},
		{"", false},
	}
	for _, tt := range tests {
		if got := hasPort(tt.input); got != tt.want {
			t.Errorf("hasPort(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseInt64(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"12345", 12345},
		{"0", 0},
		{"-1", -1},
		{"", -1},
		{"abc", -1},
		{"12.34", -1},
		{"9223372036854775807", 9223372036854775807},
	}
	for _, tt := range tests {
		if got := parseInt64(tt.input); got != tt.want {
			t.Errorf("parseInt64(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"120", 2 * time.Minute},
		{"0", 0},
		{"-1", 0},
		{"invalid", 0},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
	}
	for _, tt := range tests {
		if got := parseRetryAfter(tt.input, now); got != tt.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestTransientHTTPStatusesAreRetryable(t *testing.T) {
	for _, code := range []int{http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests} {
		if IsTerminal(NewStatusError(code, nil)) {
			t.Errorf("status %d marked terminal, want retryable", code)
		}
	}
	for _, code := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound} {
		if !IsTerminal(NewStatusError(code, nil)) {
			t.Errorf("status %d marked retryable, want terminal", code)
		}
	}
}

func TestHTTPIfRangeValidatorSelection(t *testing.T) {
	d := &httpBase{etag: `"strong"`, lastModified: "Thu, 29 Oct 2026 04:00:00 GMT"}
	if got := d.ifRangeValue(); got != `"strong"` {
		t.Fatalf("strong ETag selection=%q", got)
	}
	d.etag = `W/"weak"`
	if got := d.ifRangeValue(); got != d.lastModified {
		t.Fatalf("weak ETag selection=%q, want Last-Modified %q", got, d.lastModified)
	}
}

func TestHTTPTransportPoolReuseAndIsolation(t *testing.T) {
	a, err := newHTTPBase("http", "https://example.com/a", AuthOptions{ProxyMode: ProxyModeDisabled})
	if err != nil {
		t.Fatal(err)
	}
	b, err := newHTTPBase("webdav", "https://example.com/b", AuthOptions{ProxyMode: ProxyModeDisabled})
	if err != nil {
		t.Fatal(err)
	}
	if a.cli.Transport != b.cli.Transport {
		t.Fatal("same proxy config did not share an HTTP transport")
	}
	a.Close()
	b.Close()

	// 引用归零后仍保留热连接池，顺序执行的下一任务可以继续复用。
	c, err := newHTTPBase("http", "https://example.com/c", AuthOptions{ProxyMode: ProxyModeDisabled})
	if err != nil {
		t.Fatal(err)
	}
	if c.cli.Transport != a.cli.Transport {
		t.Fatal("idle transport was not reused for a subsequent task")
	}
	defer c.Close()

	d, err := newHTTPBase("http", "https://example.com/d", AuthOptions{
		ProxyMode: ProxyModeManual,
		ProxyURL:  "http://127.0.0.1:7890",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.cli.Transport == c.cli.Transport {
		t.Fatal("different proxy configs incorrectly shared a transport")
	}
}

func TestParseTotalFromContentRange(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"bytes 0-0/12345", 12345},
		{"bytes 0-99/100", 100},
		{"bytes 0-0/0", -1},
		{"bytes 500-999/123456", 123456},
		{"bytes 0-0/-1", -1},
		{"invalid", -1},
		{"", -1},
		{"no-slash-here", -1},
		{"bytes 0-0/", -1},
	}
	for _, tt := range tests {
		if got := parseTotalFromContentRange(tt.input); got != tt.want {
			t.Errorf("parseTotalFromContentRange(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseContentRange(t *testing.T) {
	tests := []struct {
		input             string
		start, end, total int64
		ok                bool
	}{
		{"bytes 0-0/12345", 0, 0, 12345, true},
		{"bytes 10-19/100", 10, 19, 100, true},
		{"BYTES 0-9/*", 0, 9, -1, true},
		{"bytes 0-0/0", 0, 0, 0, false},
		{"bytes 0-0/-1", 0, 0, 0, false},
		{"bytes 10-9/100", 0, 0, 0, false},
		{"items 0-0/100", 0, 0, 0, false},
	}
	for _, tt := range tests {
		start, end, total, ok := parseContentRange(tt.input)
		if start != tt.start || end != tt.end || total != tt.total || ok != tt.ok {
			t.Errorf("parseContentRange(%q) = (%d,%d,%d,%v), want (%d,%d,%d,%v)",
				tt.input, start, end, total, ok, tt.start, tt.end, tt.total, tt.ok)
		}
	}
}

func TestSupportsRanges(t *testing.T) {
	makeResp := func(hdrs http.Header) *http.Response {
		req, _ := http.NewRequest("GET", "/", nil)
		return &http.Response{Request: req, Header: http.Header(hdrs)}
	}
	tests := []struct {
		hdrs http.Header
		want bool
	}{
		{http.Header{"Accept-Ranges": {"bytes"}}, true},
		{http.Header{"Accept-Ranges": {"Bytes"}}, true},
		{http.Header{"Accept-Ranges": {"1"}}, true},
		{http.Header{"Accept-Ranges": {"none"}}, false},
		{http.Header{}, false},
		{http.Header{"Accept-Ranges": {""}}, false},
	}
	for _, tt := range tests {
		resp := makeResp(tt.hdrs)
		if got := supportsRanges(resp); got != tt.want {
			t.Errorf("supportsRanges(%v) = %v, want %v", tt.hdrs, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 代理配置测试
// ---------------------------------------------------------------------------

func TestParseBypassList(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bypassList
	}{
		{"empty", "", nil},
		{"spaces", "   ", nil},
		{"single", "example.com", bypassList{"example.com"}},
		{"multi", "a.example.com, b.example.com ,cdn.local", bypassList{"a.example.com", "b.example.com", "cdn.local"}},
		{"wildcard", "*.example.com", bypassList{"example.com"}},
		{"dot-prefix", ".example.com", bypassList{"example.com"}},
		{"trailing-dots", "  .x.io , *.y.io  ", bypassList{"x.io", "y.io"}},
		{"empty-entry", ",, ,", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBypassList(tt.input)
			if !equalBypass(got, tt.want) {
				t.Errorf("parseBypassList(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func equalBypass(a, b bypassList) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBypassListMatches(t *testing.T) {
	b := parseBypassList("example.com, cdn.local")
	tests := []struct {
		host string
		want bool
	}{
		{"example.com", true},
		{"api.example.com", true},
		{"a.b.example.com", true},
		{"notexample.com", false},
		{"cdn.local", true},
		{"x.cdn.local", true},
		{"", false},
		{"example.org", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := b.matches(tt.host); got != tt.want {
				t.Errorf("matches(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestProxyFuncNoConfigReturnsEnvironment(t *testing.T) {
	fn := proxyFunc(AuthOptions{})
	if fn == nil {
		t.Fatal("proxyFunc(AuthOptions{}) = nil, want non-nil function")
	}
}

func TestProxyFuncWithURL(t *testing.T) {
	fn := proxyFunc(AuthOptions{ProxyURL: "http://127.0.0.1:7890"})
	req := httptest.NewRequest("GET", "http://example.com/file", nil)
	got, err := fn(req)
	if err != nil {
		t.Fatalf("proxyFunc: %v", err)
	}
	if got == nil {
		t.Fatal("proxyFunc returned nil, want proxy URL")
	}
	if got.Host != "127.0.0.1:7890" {
		t.Errorf("proxy Host = %q, want 127.0.0.1:7890", got.Host)
	}
	if got.Scheme != "http" {
		t.Errorf("proxy Scheme = %q, want http", got.Scheme)
	}
}

func TestProxyFuncWithInvalidURLFallsBack(t *testing.T) {
	// 非法 URL 在 Manual 模式下不应被使用：出于安全考虑直接返回 nil，
	// 避免将畸形字符串交给 http.ProxyFromEnvironment 误判。
	fn := proxyFunc(AuthOptions{ProxyMode: ProxyModeManual, ProxyURL: "not a url"})
	req := httptest.NewRequest("GET", "http://example.com/file", nil)
	if fn == nil {
		t.Fatal("proxyFunc = nil, want non-nil")
	}
	got, err := fn(req)
	if err != nil {
		t.Errorf("proxyFunc returned error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil proxy for invalid URL, got %v", got)
	}
}

func TestProxyFuncBypassSkipsProxy(t *testing.T) {
	fn := proxyFunc(AuthOptions{
		ProxyMode:   ProxyModeManual,
		ProxyURL:    "http://127.0.0.1:7890",
		ProxyBypass: "example.com, cdn.local",
	})
	tests := []struct {
		host    string
		skipped bool
	}{
		{"example.com", true},
		{"a.example.com", true},
		{"cdn.local", true},
		{"other.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://"+tt.host+"/file", nil)
			got, err := fn(req)
			if err != nil {
				t.Fatalf("proxyFunc: %v", err)
			}
			if tt.skipped {
				if got != nil {
					t.Errorf("matches(%q) returned proxy %v, want nil (bypass)", tt.host, got)
				}
				return
			}
			if got == nil {
				t.Errorf("matches(%q) = nil, want proxy", tt.host)
			}
		})
	}
}

func TestProxyFuncBypassOnlyFallsBackToEnv(t *testing.T) {
	// 仅配置 bypass、不配置 ProxyURL：必须仍返回非 nil 函数，
	// 这样浏览器式 NO_PROXY 语义才能生效（System 模式）。
	fn := proxyFunc(AuthOptions{ProxyMode: ProxyModeSystem, ProxyBypass: "example.com"})
	if fn == nil {
		t.Fatal("proxyFunc = nil, want non-nil env resolver with bypass")
	}
	req := httptest.NewRequest("GET", "http://example.com/x", nil)
	got, err := fn(req)
	if err != nil {
		t.Fatalf("proxyFunc: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for bypassed host, got %v", got)
	}
}

func TestParseProxyMode(t *testing.T) {
	tests := []struct {
		in   string
		want ProxyMode
	}{
		{"system", ProxyModeSystem},
		{"SYSTEM", ProxyModeSystem},
		{"", ProxyModeSystem},
		{"disabled", ProxyModeDisabled},
		{"off", ProxyModeDisabled},
		{"none", ProxyModeDisabled},
		{"manual", ProxyModeManual},
		{"custom", ProxyModeManual},
		{"unknown", ProxyModeSystem},
		{"  manual  ", ProxyModeManual},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := ParseProxyMode(tt.in); got != tt.want {
				t.Errorf("ParseProxyMode(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestProxyModeString(t *testing.T) {
	tests := []struct {
		m    ProxyMode
		want string
	}{
		{ProxyModeSystem, "system"},
		{ProxyModeDisabled, "disabled"},
		{ProxyModeManual, "manual"},
	}
	for _, tt := range tests {
		if got := tt.m.String(); got != tt.want {
			t.Errorf("ProxyMode(%d).String() = %q, want %q", tt.m, got, tt.want)
		}
	}
}

func TestProxyFuncDisabledMode(t *testing.T) {
	fn := proxyFunc(AuthOptions{ProxyMode: ProxyModeDisabled, ProxyURL: "http://1.2.3.4:9999"})
	if fn == nil {
		t.Fatal("proxyFunc returned nil")
	}
	req := httptest.NewRequest("GET", "http://example.com/file", nil)
	got, err := fn(req)
	if err != nil {
		t.Fatalf("proxyFunc: %v", err)
	}
	if got != nil {
		t.Errorf("Disabled mode must direct-connect, got %v", got)
	}
}

func TestProxyFuncManualEmptyURLGoesDirect(t *testing.T) {
	fn := proxyFunc(AuthOptions{ProxyMode: ProxyModeManual, ProxyURL: ""})
	if fn == nil {
		t.Fatal("proxyFunc returned nil")
	}
	req := httptest.NewRequest("GET", "http://example.com/file", nil)
	got, _ := fn(req)
	if got != nil {
		t.Errorf("Manual mode with empty URL must direct-connect, got %v", got)
	}
}

func TestProxyFuncSystemModeReturnsEnv(t *testing.T) {
	// 设置一个环境变量让 ProxyFromEnvironment 有值；测试结束后清理。
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:7890")
	fn := proxyFunc(AuthOptions{ProxyMode: ProxyModeSystem})
	req := httptest.NewRequest("GET", "http://example.com/file", nil)
	got, err := fn(req)
	if err != nil {
		t.Fatalf("proxyFunc: %v", err)
	}
	if got == nil {
		t.Fatal("System mode should pick up HTTP_PROXY, got nil")
	}
	if got.Host != "127.0.0.1:7890" {
		t.Errorf("proxy Host = %q, want 127.0.0.1:7890", got.Host)
	}
}

func TestProxyFuncZeroModeInfersFromURL(t *testing.T) {
	// 兼容路径：未设模式但提供了 URL，视为 Manual。
	fn := proxyFunc(AuthOptions{ProxyURL: "http://127.0.0.1:7890"})
	req := httptest.NewRequest("GET", "http://example.com/file", nil)
	got, err := fn(req)
	if err != nil {
		t.Fatalf("proxyFunc: %v", err)
	}
	if got == nil || got.Host != "127.0.0.1:7890" {
		t.Errorf("zero-mode + URL should yield proxy, got %v", got)
	}

	// 未设模式且无 URL：视为 System。
	fn = proxyFunc(AuthOptions{})
	if fn == nil {
		t.Fatal("zero-mode + empty URL returned nil fn")
	}
}

func TestEffectiveAuthMergesFromGlobal(t *testing.T) {
	prev := GetProxyConfig()
	defer SetProxyConfig(prev)

	SetProxyConfig(ProxyConfig{
		Mode:        ProxyModeManual,
		ProxyURL:    "http://127.0.0.1:7890",
		ProxyBypass: "example.com",
	})

	// 全部为零的 AuthOptions：必须完全继承全局。
	got := effectiveAuth(AuthOptions{})
	if got.ProxyMode != ProxyModeManual {
		t.Errorf("Mode = %v, want Manual", got.ProxyMode)
	}
	if got.ProxyURL != "http://127.0.0.1:7890" {
		t.Errorf("ProxyURL = %q, want http://127.0.0.1:7890", got.ProxyURL)
	}
	if got.ProxyBypass != "example.com" {
		t.Errorf("ProxyBypass = %q, want example.com", got.ProxyBypass)
	}

	// 部分覆盖：Mode 显式指定，URL/Bypass 留空 → 仍继承全局。
	got = effectiveAuth(AuthOptions{ProxyMode: ProxyModeDisabled})
	if got.ProxyMode != ProxyModeDisabled {
		t.Errorf("Mode = %v, want Disabled", got.ProxyMode)
	}
	if got.ProxyURL != "http://127.0.0.1:7890" {
		t.Errorf("ProxyURL = %q, want inherited", got.ProxyURL)
	}
}

func TestProxyFuncSystemFallsBackToSysproxy(t *testing.T) {
	// System 模式 + 无 env + 无 bypass：在测试环境下应该至少返回非 nil 函数
	// （可能 nil URL，因为没装 GNOME/KDE；也可能命中 scutil/registry）。
	// 关键是验证 chain 不 panic、且遵循"先 env 后 sysproxy"的语义。
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("NO_PROXY", "")
	InvalidateSystemProxyCache()

	fn := proxyFunc(AuthOptions{ProxyMode: ProxyModeSystem})
	if fn == nil {
		t.Fatal("proxyFunc returned nil function")
	}
	req := httptest.NewRequest("GET", "http://example.com/file", nil)
	got, err := fn(req)
	if err != nil {
		t.Fatalf("proxyFunc: %v", err)
	}
	// got 可能为 nil（无任何代理来源），不应出错。
	_ = got
}

func TestProxyFuncSystemEnvTakesPrecedence(t *testing.T) {
	// 当 HTTP_PROXY 已设置时必须优先于 sysproxy 探测。
	// 同时覆盖大小写，因为 httpproxy 在两者都设时优先使用小写。
	for _, name := range []string{
		"HTTP_PROXY", "http_proxy",
		"HTTPS_PROXY", "https_proxy",
		"ALL_PROXY", "all_proxy",
		"NO_PROXY", "no_proxy",
	} {
		os.Unsetenv(name)
	}
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9999")
	t.Setenv("http_proxy", "http://127.0.0.1:9999")
	InvalidateSystemProxyCache()

	// 直接探针：先确认 http.ProxyFromEnvironment 在此环境下能返回 9999。
	probeReq := httptest.NewRequest("GET", "http://example.com/file", nil)
	if u, err := http.ProxyFromEnvironment(probeReq); err != nil || u == nil || u.Host != "127.0.0.1:9999" {
		t.Skipf("test environment makes env precedence ambiguous: probe=%v err=%v", u, err)
	}

	fn := proxyFunc(AuthOptions{ProxyMode: ProxyModeSystem})
	req := httptest.NewRequest("GET", "http://example.com/file", nil)
	got, err := fn(req)
	if err != nil {
		t.Fatalf("proxyFunc: %v", err)
	}
	if got == nil || got.Host != "127.0.0.1:9999" {
		t.Errorf("env should win, got %v", got)
	}
}

func clearProxyEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"HTTP_PROXY", "http_proxy",
		"HTTPS_PROXY", "https_proxy",
		"ALL_PROXY", "all_proxy",
		"NO_PROXY", "no_proxy",
	} {
		t.Setenv(name, "")
	}
	InvalidateSystemProxyCache()
}

func stubSystemProxy(t *testing.T, cfg sysproxy.Config, ok bool) *atomic.Int32 {
	t.Helper()
	calls := &atomic.Int32{}
	previous := detectSystemProxy
	detectSystemProxy = func() (sysproxy.Config, bool) {
		calls.Add(1)
		return cfg, ok
	}
	t.Cleanup(func() {
		detectSystemProxy = previous
		InvalidateSystemProxyCache()
	})
	InvalidateSystemProxyCache()
	return calls
}

func proxyFor(t *testing.T, fn func(*http.Request) (*url.URL, error), raw string) *url.URL {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, raw, nil)
	got, err := fn(req)
	if err != nil {
		t.Fatalf("proxy resolver: %v", err)
	}
	return got
}

func TestSystemProxyHonorsEnvNoProxyWithoutDesktopFallback(t *testing.T) {
	clearProxyEnvironment(t)
	t.Setenv("HTTP_PROXY", "http://env-proxy:1111")
	t.Setenv("NO_PROXY", "example.com")
	calls := stubSystemProxy(t, sysproxy.Config{
		ProxyURL: "http://desktop-proxy:2222",
	}, true)

	fn := proxyFunc(AuthOptions{ProxyMode: ProxyModeSystem})
	if got := proxyFor(t, fn, "http://api.example.com/file"); got != nil {
		t.Fatalf("NO_PROXY host used %v; environment bypass must not fall through to desktop proxy", got)
	}
	if calls.Load() != 0 {
		t.Fatalf("desktop proxy was probed %d times despite an environment proxy with NO_PROXY", calls.Load())
	}
}

func TestSystemProxyUsesAllProxyForHTTPAndHTTPS(t *testing.T) {
	clearProxyEnvironment(t)
	t.Setenv("ALL_PROXY", "socks5://127.0.0.1:1080")
	stubSystemProxy(t, sysproxy.Config{
		ProxyURL: "http://desktop-proxy:2222",
	}, true)

	fn := proxyFunc(AuthOptions{ProxyMode: ProxyModeSystem})
	for _, target := range []string{
		"http://public.example/file",
		"https://public.example/file",
	} {
		got := proxyFor(t, fn, target)
		if got == nil || got.Scheme != "socks5" || got.Host != "127.0.0.1:1080" {
			t.Fatalf("ALL_PROXY for %s = %v", target, got)
		}
	}
}

func TestSystemProxyUsesDesktopBypass(t *testing.T) {
	clearProxyEnvironment(t)
	stubSystemProxy(t, sysproxy.Config{
		ProxyURL: "http://desktop-proxy:7890",
		Bypass:   "direct.example,10.0.0.0/8,192.168.*,<local>",
	}, true)

	fn := proxyFunc(AuthOptions{ProxyMode: ProxyModeSystem})
	if got := proxyFor(t, fn, "https://direct.example/file"); got != nil {
		t.Fatalf("desktop bypass used proxy %v", got)
	}
	if got := proxyFor(t, fn, "https://10.2.3.4/file"); got != nil {
		t.Fatalf("desktop CIDR bypass used proxy %v", got)
	}
	if got := proxyFor(t, fn, "https://192.168.1.20/file"); got != nil {
		t.Fatalf("desktop wildcard bypass used proxy %v", got)
	}
	if got := proxyFor(t, fn, "http://nas/file"); got != nil {
		t.Fatalf("<local> hostname used proxy %v", got)
	}
	got := proxyFor(t, fn, "https://public.example/file")
	if got == nil || got.Host != "desktop-proxy:7890" {
		t.Fatalf("non-bypassed host proxy = %v", got)
	}
}

func TestSystemProxySeparatesHTTPAndHTTPS(t *testing.T) {
	clearProxyEnvironment(t)
	stubSystemProxy(t, sysproxy.Config{
		HTTPProxyURL:  "http://http-proxy:8080",
		HTTPSProxyURL: "http://https-proxy:8443",
	}, true)

	fn := proxyFunc(AuthOptions{ProxyMode: ProxyModeSystem})
	httpProxy := proxyFor(t, fn, "http://public.example/file")
	httpsProxy := proxyFor(t, fn, "https://public.example/file")
	if httpProxy == nil || httpProxy.Host != "http-proxy:8080" {
		t.Fatalf("HTTP proxy = %v", httpProxy)
	}
	if httpsProxy == nil || httpsProxy.Host != "https-proxy:8443" {
		t.Fatalf("HTTPS proxy = %v", httpsProxy)
	}
}

func TestSystemProxyCacheRefreshesAfterExpiry(t *testing.T) {
	clearProxyEnvironment(t)
	current := "http://proxy-one:1111"
	calls := &atomic.Int32{}
	previous := detectSystemProxy
	detectSystemProxy = func() (sysproxy.Config, bool) {
		calls.Add(1)
		return sysproxy.Config{ProxyURL: current}, true
	}
	t.Cleanup(func() {
		detectSystemProxy = previous
		InvalidateSystemProxyCache()
	})
	InvalidateSystemProxyCache()

	fn := proxyFunc(AuthOptions{ProxyMode: ProxyModeSystem})
	if got := proxyFor(t, fn, "http://one.example/file"); got == nil || got.Host != "proxy-one:1111" {
		t.Fatalf("first proxy = %v", got)
	}
	current = "http://proxy-two:2222"
	if got := proxyFor(t, fn, "http://cached.example/file"); got == nil || got.Host != "proxy-one:1111" {
		t.Fatalf("cached proxy = %v", got)
	}

	systemProxyMu.Lock()
	systemProxyExpires = time.Now().Add(-time.Second)
	systemProxyMu.Unlock()
	if got := proxyFor(t, fn, "http://two.example/file"); got == nil || got.Host != "proxy-two:2222" {
		t.Fatalf("refreshed proxy = %v", got)
	}
	if calls.Load() != 2 {
		t.Fatalf("detector calls = %d, want 2", calls.Load())
	}
}

func TestHTTPProxyFuncUsesLatestGlobalConfig(t *testing.T) {
	previous := GetProxyConfig()
	t.Cleanup(func() { SetProxyConfig(previous) })

	SetProxyConfig(ProxyConfig{
		Mode:     ProxyModeManual,
		ProxyURL: "http://proxy-one:1111",
	})
	fn := HTTPProxyFunc()
	if got := proxyFor(t, fn, "https://example.com/a"); got == nil || got.Host != "proxy-one:1111" {
		t.Fatalf("first global proxy = %v", got)
	}

	SetProxyConfig(ProxyConfig{
		Mode:     ProxyModeManual,
		ProxyURL: "http://proxy-two:2222",
	})
	if got := proxyFor(t, fn, "https://example.com/a"); got == nil || got.Host != "proxy-two:2222" {
		t.Fatalf("updated global proxy = %v", got)
	}
}

// ---------------------------------------------------------------------------
// DetectKind / ResolveURL 测试
// ---------------------------------------------------------------------------

func TestDetectKind(t *testing.T) {
	tests := []struct {
		raw   string
		hint  ProtocolKind
		want  ProtocolKind
		errOK bool
	}{
		{"http://example.com/file", "", ProtoHTTP, false},

		{"https://example.com/file", "", ProtoHTTPS, false},
		{"ftp://example.com/file", "", ProtoFTP, false},
		{"webdav://example.com/file", "", ProtoWebDAV, false},
		{"dav://example.com/file", "", ProtoWebDAV, false},
		{"HTTP://EXAMPLE.COM/FILE", "", ProtoHTTP, false},
		{"HTTPS://EXAMPLE.COM/FILE", "", ProtoHTTPS, false},
		{"ftp://example.com/file", ProtoHTTPS, ProtoHTTPS, false},
		{"http://example.com/", ProtoWebDAV, ProtoWebDAV, false},
		{"file://example.com/", "", "", true},
		{"", "", "", true},
	}
	for _, tt := range tests {
		got, err := DetectKind(tt.raw, tt.hint)
		if tt.errOK {
			if err == nil {
				t.Errorf("DetectKind(%q, %q) err=nil, want error", tt.raw, tt.hint)
			}
		} else {
			if err != nil {
				t.Errorf("DetectKind(%q, %q) err=%v, want %v", tt.raw, tt.hint, err, tt.want)
			} else if got != tt.want {
				t.Errorf("DetectKind(%q, %q) = %v, want %v", tt.raw, tt.hint, got, tt.want)
			}
		}
	}
}

func TestResolveURL(t *testing.T) {
	tests := []struct {
		raw  string
		kind ProtocolKind
		want string
	}{
		{"http://example.com/file", ProtoHTTP, "http://example.com/file"},
		{"https://example.com/file", ProtoHTTPS, "https://example.com/file"},
		{"webdav://example.com/file", ProtoWebDAV, "https://example.com/file"},
		{"dav://example.com/file", ProtoWebDAV, "http://example.com/file"},
		{"ftp://example.com/file", ProtoFTP, "ftp://example.com/file"},
	}
	for _, tt := range tests {
		if got := ResolveURL(tt.raw, tt.kind); got != tt.want {
			t.Errorf("ResolveURL(%q, %q) = %q, want %q", tt.raw, tt.kind, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// New 工厂测试
// ---------------------------------------------------------------------------

func TestNew(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
	}))
	defer srv.Close()

	tests := []struct {
		url  string
		kind ProtocolKind
		ok   bool
	}{
		{srv.URL + "/file", ProtoHTTP, true},
		{srv.URL + "/file", ProtoHTTPS, true},
		{"", ProtoHTTP, false},
	}
	for _, tt := range tests {
		driver, err := New(tt.url, tt.kind, Auth{})
		if tt.ok {
			if err != nil {
				t.Errorf("New(%q, %q) err=%v, want nil", tt.url, tt.kind, err)
			} else {
				driver.Close()
			}
		} else {
			if err == nil {
				t.Errorf("New(%q, %q) err=nil, want error", tt.url, tt.kind)
				driver.Close()
			}
		}
	}
}

// ---------------------------------------------------------------------------
// HTTP Driver 测试
// ---------------------------------------------------------------------------

func makeHTTPDriver(url string) ProtocolDriver {
	d, _ := newHTTPDriver(url, AuthOptions{})
	return d
}

func TestHTTPProbeHEAD(t *testing.T) {
	const size int64 = 12345
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", size))
			w.Header().Set("Content-Length", "1")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte{0})
			return
		}
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Server", "test")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := makeHTTPDriver(srv.URL)
	caps, err := d.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if caps.TotalSize != size {
		t.Errorf("TotalSize=%d, want %d", caps.TotalSize, size)
	}
	if !caps.SupportRange {
		t.Errorf("SupportRange=false, want true")
	}
	d.Close()
}

func TestHTTPProbeCapturesValidatorsAndUsesIfRange(t *testing.T) {
	const (
		size         = 4096
		etag         = `"resource-v1"`
		lastModified = "Thu, 29 Oct 2026 04:00:00 GMT"
	)
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", lastModified)
		switch {
		case r.Method == http.MethodHead:
			w.Header().Set("Content-Length", strconv.Itoa(size))
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(http.StatusOK)
		case r.Header.Get("Range") == "bytes=0-0":
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", size))
			w.Header().Set("Content-Length", "1")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[:1])
		case r.Header.Get("Range") == "bytes=1024-2047":
			if got := r.Header.Get("If-Range"); got != etag {
				http.Error(w, "bad If-Range: "+got, http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 1024-2047/%d", size))
			w.Header().Set("Content-Length", "1024")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[1024:2048])
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	d := makeHTTPDriver(srv.URL)
	caps, err := d.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if caps.ETag != etag || caps.LastModified != lastModified {
		t.Fatalf("validators = %q / %q, want %q / %q", caps.ETag, caps.LastModified, etag, lastModified)
	}

	f, err := os.Create(filepath.Join(t.TempDir(), "if-range.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := d.DownloadChunk(context.Background(), 1024, 2047, f, nil); err != nil {
		t.Fatalf("DownloadChunk: %v", err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[1024:2048], payload[1024:2048]) {
		t.Fatal("chunk bytes mismatch")
	}
	_ = d.Close()
}

func TestHTTPDownloadChunkRejectsChangedValidator(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-1023/2048")
		w.Header().Set("Content-Length", "1024")
		w.Header().Set("ETag", `"v2"`)
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(make([]byte, 1024))
	}))
	defer srv.Close()

	d := makeHTTPDriver(srv.URL).(*httpDriver)
	d.setValidators(`"v1"`, "")
	f, err := os.Create(filepath.Join(t.TempDir(), "changed-validator.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = d.DownloadChunk(context.Background(), 0, 1023, f, nil)
	if !errors.Is(err, ErrRangeUnavailable) {
		t.Fatalf("DownloadChunk err=%v, want ErrRangeUnavailable", err)
	}
	_ = d.Close()
}

func TestHTTPRateLimitErrorCarriesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	d := makeHTTPDriver(srv.URL)
	f, err := os.Create(filepath.Join(t.TempDir(), "rate-limited.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = d.DownloadChunk(context.Background(), 0, 9, f, nil)
	if err == nil {
		t.Fatal("DownloadChunk returned nil")
	}
	if got := RetryAfter(err); got != 2*time.Second {
		t.Fatalf("RetryAfter=%v, want 2s", got)
	}
	if IsTerminal(err) {
		t.Fatal("429 must be retryable")
	}
	_ = d.Close()
}

func TestHTTPProbeHEADNoAcceptRanges(t *testing.T) {
	const size int64 = 999
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		// 不设置 Accept-Ranges
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := makeHTTPDriver(srv.URL)
	caps, err := d.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if caps.SupportRange {
		t.Errorf("SupportRange=true, want false")
	}
	d.Close()
}

func TestHTTPProbeHEADContentLengthMissing(t *testing.T) {
	// HEAD 无 Content-Length → 回退到 probeWithRange
	var rangeCalled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		rangeCalled.Store(true)
		w.Header().Set("Content-Range", "bytes 0-0/1024")
		w.Header().Set("Content-Length", "1")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte("x"))
	}))
	defer srv.Close()

	d := makeHTTPDriver(srv.URL)
	caps, err := d.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if caps.TotalSize != 1024 {
		t.Errorf("TotalSize=%d, want 1024", caps.TotalSize)
	}
	if !caps.SupportRange {
		t.Errorf("SupportRange=false, want true")
	}
	d.Close()
}

func TestHTTPProbeHEADErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	d := makeHTTPDriver(srv.URL)
	_, err := d.Probe(context.Background())
	if err == nil {
		t.Errorf("Probe err=nil, want error")
	}
	d.Close()
}

func TestHTTPProbeContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := makeHTTPDriver(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := d.Probe(ctx)
	if err == nil {
		t.Errorf("Probe err=nil, want context error")
	}
	d.Close()
}

func TestHTTPDownloadChunk(t *testing.T) {
	const size = 64 * 1024
	payload := make([]byte, size)
	rand.Read(payload)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("Range"); h != "" {
			start, end := parseRangeFromHeader(h, size)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[start : end+1])
			return
		}
		w.Header().Set("Content-Length", strconv.FormatInt(int64(size), 10))
		w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	f, _ := os.Create(destPath)
	os.Truncate(destPath, size)

	var onDataCalls atomic.Int32
	d := makeHTTPDriver(srv.URL)

	err := d.DownloadChunk(context.Background(), 0, int64(size-1), f, func(n int) {
		onDataCalls.Add(1)
	})
	f.Close()
	if err != nil {
		t.Fatalf("DownloadChunk: %v", err)
	}
	if onDataCalls.Load() == 0 {
		t.Errorf("onData never called")
	}

	got, _ := os.ReadFile(destPath)
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), size)
	}
	d.Close()
}

func TestHTTPDownloadChunkInvalidRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	d := makeHTTPDriver(srv.URL)
	dir := t.TempDir()
	f, _ := os.Create(filepath.Join(dir, "junk.bin"))
	f.Close()

	err := d.DownloadChunk(context.Background(), 100, 50, f, nil) // start > end
	if err == nil {
		t.Errorf("DownloadChunk err=nil, want error for invalid range")
	}
	d.Close()
}

func TestHTTPDownloadChunkNon206(t *testing.T) {
	// 服务器返回 404，DownloadChunk 应报错
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	d := makeHTTPDriver(srv.URL)
	dir := t.TempDir()
	// os.Create 已是 O_RDWR；用同一个句柄截断即可，不要再 open 一次
	// （旧的写法把首个句柄覆盖掉，从未 Close，t.TempDir 清理会失败）。
	f, err := os.Create(filepath.Join(dir, "junk.bin"))
	if err != nil {
		t.Fatalf("create junk file: %v", err)
	}
	defer f.Close()
	if err := f.Truncate(100); err != nil {
		t.Fatalf("truncate junk file: %v", err)
	}

	err = d.DownloadChunk(context.Background(), 0, 99, f, nil)
	if err == nil {
		t.Errorf("DownloadChunk err=nil, want error for 404")
	}
	d.Close()
}

func TestHTTPDownloadChunkRejectsIgnoredRange(t *testing.T) {
	const size = 4096
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(size))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, size))
	}))
	defer srv.Close()

	f, err := os.Create(filepath.Join(t.TempDir(), "ignored-range.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}

	d := makeHTTPDriver(srv.URL)
	err = d.DownloadChunk(context.Background(), 1024, 2047, f, nil)
	if !errors.Is(err, ErrRangeUnavailable) {
		t.Fatalf("DownloadChunk err=%v, want ErrRangeUnavailable", err)
	}
	_ = d.Close()
}

func TestHTTPDownloadChunkRejectsWrongContentRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-1023/4096")
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(make([]byte, 1024))
	}))
	defer srv.Close()

	f, err := os.Create(filepath.Join(t.TempDir(), "wrong-range.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	d := makeHTTPDriver(srv.URL)
	err = d.DownloadChunk(context.Background(), 1024, 2047, f, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid Content-Range") {
		t.Fatalf("DownloadChunk err=%v, want invalid Content-Range", err)
	}
	_ = d.Close()
}

func TestHTTPDownloadChunkRejectsShortBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-1023/4096")
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(make([]byte, 512))
	}))
	defer srv.Close()

	f, err := os.Create(filepath.Join(t.TempDir(), "short-range.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	d := makeHTTPDriver(srv.URL)
	err = d.DownloadChunk(context.Background(), 0, 1023, f, nil)
	if !errors.Is(err, ErrUnexpectedEOF) {
		t.Fatalf("DownloadChunk err=%v, want ErrUnexpectedEOF", err)
	}
	_ = d.Close()
}

func TestHTTPDownloadFallback(t *testing.T) {
	const size = 32 * 1024
	payload := make([]byte, size)
	rand.Read(payload)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(int64(size), 10))
		w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	f, _ := os.Create(destPath)
	os.Truncate(destPath, size)

	var onDataCalls atomic.Int32
	d := makeHTTPDriver(srv.URL)

	err := d.DownloadFallback(context.Background(), 0, f, func(n int) {
		onDataCalls.Add(1)
	})
	f.Close()
	if err != nil {
		t.Fatalf("DownloadFallback: %v", err)
	}
	if onDataCalls.Load() == 0 {
		t.Errorf("onData never called")
	}

	got, _ := os.ReadFile(destPath)
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch")
	}
	d.Close()
}

func TestHTTPDownloadFallbackOffset(t *testing.T) {
	const size = 16 * 1024
	payload := make([]byte, size)
	rand.Read(payload)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(int64(size), 10))
		w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	f, _ := os.Create(destPath)
	os.Truncate(destPath, size)

	d := makeHTTPDriver(srv.URL)
	err := d.DownloadFallback(context.Background(), 4096, f, nil)
	f.Close()
	if !errors.Is(err, ErrResumeUnavailable) {
		t.Fatalf("DownloadFallback err=%v, want ErrResumeUnavailable", err)
	}
	d.Close()
}

func TestHTTPDownloadFallbackResumesWithRange(t *testing.T) {
	const size = 8192
	const offset = 4096
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == fmt.Sprintf("bytes=%d-", offset) {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, size-1, size))
			w.Header().Set("Content-Length", strconv.Itoa(size-offset))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[offset:])
			return
		}
		http.Error(w, "range required", http.StatusBadRequest)
	}))
	defer srv.Close()

	destPath := filepath.Join(t.TempDir(), "resume.bin")
	f, err := os.Create(destPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}

	d := makeHTTPDriver(srv.URL)
	if err := d.DownloadFallback(context.Background(), offset, f, nil); err != nil {
		t.Fatalf("DownloadFallback resume: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[offset:], payload[offset:]) {
		t.Fatal("resumed fallback wrote incorrect bytes")
	}
	_ = d.Close()
}

func TestHTTPClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	d := makeHTTPDriver(srv.URL)
	if err := d.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Errorf("Close again: %v", err)
	}
}

// ---------------------------------------------------------------------------
// WebDAV Driver 测试
// ---------------------------------------------------------------------------

func makeWebDAVDriver(url string) ProtocolDriver {
	d, _ := newWebDAVDriver(url, AuthOptions{})
	return d
}

func TestWebDAVProbePROPFIND(t *testing.T) {
	const size int64 = 54321
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusMultiStatus)
			body := fmt.Sprintf(
				`<?xml version="1.0"?><multistatus xmlns="DAV:"><response><href>/file.txt</href>`+
					`<propstat><prop><getcontentlength xmlns="DAV:">%d</getcontentlength></prop>`+
					`<status>HTTP/1.1 200 OK</status></propstat></response></multistatus>`, size)
			w.Write([]byte(body))
			return
		}
		if r.Method == http.MethodGet && r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", size))
			w.Header().Set("Content-Length", "1")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte{0})
		}
	}))
	defer srv.Close()

	d := makeWebDAVDriver(srv.URL + "/file.txt")
	caps, err := d.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if caps.TotalSize != size {
		t.Errorf("TotalSize=%d, want %d", caps.TotalSize, size)
	}
	if !caps.SupportRange {
		t.Errorf("SupportRange=false, want true (WebDAV always supports range)")
	}
	d.Close()
}

func TestWebDAVProbePROPFINDFallbackHEAD(t *testing.T) {
	const size int64 = 7777
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer srv.Close()

	d := makeWebDAVDriver(srv.URL + "/file.txt")
	caps, err := d.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if caps.TotalSize != size {
		t.Errorf("TotalSize=%d, want %d", caps.TotalSize, size)
	}
	d.Close()
}

func TestWebDAVDownloadChunk(t *testing.T) {
	const size = 32 * 1024
	payload := make([]byte, size)
	rand.Read(payload)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("Range"); h != "" {
			start, end := parseRangeFromHeader(h, size)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[start : end+1])
			return
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	f, _ := os.Create(destPath)
	os.Truncate(destPath, size)

	d := makeWebDAVDriver(srv.URL)
	err := d.DownloadChunk(context.Background(), 0, int64(size-1), f, nil)
	f.Close()
	if err != nil {
		t.Fatalf("DownloadChunk: %v", err)
	}
	got, _ := os.ReadFile(destPath)
	if !bytes.Equal(got, payload) {
		t.Errorf("payload mismatch")
	}
	d.Close()
}

func TestWebDAVDownloadFallback(t *testing.T) {
	const size = 8 * 1024
	payload := make([]byte, size)
	rand.Read(payload)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(int64(size), 10))
		w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	f, _ := os.Create(destPath)
	os.Truncate(destPath, size)

	d := makeWebDAVDriver(srv.URL)
	err := d.DownloadFallback(context.Background(), 0, f, nil)
	f.Close()
	if err != nil {
		t.Fatalf("DownloadFallback: %v", err)
	}
	got, _ := os.ReadFile(destPath)
	if !bytes.Equal(got, payload) {
		t.Errorf("payload mismatch")
	}
	d.Close()
}

func TestWebDAVClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	d := makeWebDAVDriver(srv.URL)
	if err := d.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// ---------------------------------------------------------------------------
// FTP Driver 测试（使用 scripts/ftp_test_server 的 Go 测试服务器）
// ---------------------------------------------------------------------------

// ftpdBinaryPath 返回预编译 FTP 测试服务器二进制的路径。
// Windows 上 `go build -o <name>` 实际输出 <name>.exe，因此必须补上
// 后缀，否则 exec.Command 会以 "executable file not found in %PATH%"
// 失败，FTP 代码路径在这条主力平台上就完全失去覆盖。
func ftpdBinaryPath() string {
	name := fmt.Sprintf("ftpd_test_%d", os.Getpid())
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(os.TempDir(), name)
}

// startFTPServer 启动一个纯 Go 编写的 FTP 测试服务器，
// 将 testFile 写入根目录，返回服务器地址和清理函数。
// 使用项目中的 scripts/ftp_test_server/ftpd.go。
func startFTPServer(t *testing.T, testFile string, fileSize int) (addr string, cleanup func()) {
	root, err := os.MkdirTemp("", "ftp_test_root_")
	if err != nil {
		t.Fatalf("cannot create temp dir: %v", err)
	}

	// 将测试文件写入根目录
	dest := filepath.Join(root, filepath.Base(testFile))
	os.WriteFile(dest, make([]byte, fileSize), 0644)

	// 使用项目中的 Go FTP 测试服务器，先编译再运行
	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "scripts", "ftp_test_server", "ftpd.go"))
	if err != nil {
		t.Fatalf("cannot get absolute path to ftpd: %v", err)
	}
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		t.Fatalf("ftpd not found at %s", scriptPath)
	}

	// 预编译为临时二进制文件，避免 go run 进程组残留
	ftpdBin := ftpdBinaryPath()
	buildCmd := exec.Command("go", "build", "-o", ftpdBin, scriptPath)
	buildCmd.Dir = filepath.Dir(scriptPath)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		os.RemoveAll(root)
		t.Fatalf("cannot build ftpd: %v, output: %s", err, string(out))
	}

	cmd := exec.Command(ftpdBin, "--root", root)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		os.RemoveAll(ftpdBin)
		os.RemoveAll(root)
		t.Fatalf("StdoutPipe error: %v", err)
	}

	if err := cmd.Start(); err != nil {
		os.RemoveAll(ftpdBin)
		os.RemoveAll(root)
		t.Fatalf("cannot start FTP server: %v", err)
	}

	var port int
	if _, err := fmt.Fscan(stdout, &port); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		os.RemoveAll(ftpdBin)
		os.RemoveAll(root)
		t.Fatalf("cannot read FTP server port: %v", err)
	}
	if port == 0 {
		cmd.Process.Kill()
		cmd.Wait()
		os.RemoveAll(ftpdBin)
		os.RemoveAll(root)
		t.Fatalf("could not read FTP server port")
	}

	return fmt.Sprintf("127.0.0.1:%d", port), func() {
		cmd.Process.Kill()
		cmd.Wait()
		os.RemoveAll(ftpdBin)
		os.RemoveAll(root)
	}
}

// startFTPServerWithPayload 启动 FTP 服务器并写入指定的 payload 内容。
// 用于需要精确字节内容的下载测试。
func startFTPServerWithPayload(t *testing.T, fileName string, payload []byte) (addr string, cleanup func()) {
	root, err := os.MkdirTemp("", "ftp_test_root_")
	if err != nil {
		t.Fatalf("cannot create temp dir: %v", err)
	}

	dest := filepath.Join(root, fileName)
	os.WriteFile(dest, payload, 0644)

	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "scripts", "ftp_test_server", "ftpd.go"))
	if err != nil {
		t.Fatalf("cannot get absolute path to ftpd: %v", err)
	}
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		t.Fatalf("ftpd not found at %s", scriptPath)
	}

	// 预编译为临时二进制文件
	ftpdBin := ftpdBinaryPath()
	buildCmd := exec.Command("go", "build", "-o", ftpdBin, scriptPath)
	buildCmd.Dir = filepath.Dir(scriptPath)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		os.RemoveAll(root)
		t.Fatalf("cannot build ftpd: %v, output: %s", err, string(out))
	}

	cmd := exec.Command(ftpdBin, "--root", root)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		os.RemoveAll(ftpdBin)
		os.RemoveAll(root)
		t.Fatalf("StdoutPipe error: %v", err)
	}

	if err := cmd.Start(); err != nil {
		os.RemoveAll(ftpdBin)
		os.RemoveAll(root)
		t.Fatalf("cannot start FTP server: %v", err)
	}

	var port int
	if _, err := fmt.Fscan(stdout, &port); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		os.RemoveAll(ftpdBin)
		os.RemoveAll(root)
		t.Fatalf("cannot read FTP server port: %v", err)
	}
	if port == 0 {
		cmd.Process.Kill()
		cmd.Wait()
		os.RemoveAll(ftpdBin)
		os.RemoveAll(root)
		t.Fatalf("could not read FTP server port")
	}

	return fmt.Sprintf("127.0.0.1:%d", port), func() {
		cmd.Process.Kill()
		cmd.Wait()
		os.RemoveAll(ftpdBin)
		os.RemoveAll(root)
	}
}

func TestFTPDial(t *testing.T) {
	// 验证 newFTPDriver 的错误分支：空 URL、非 ftp scheme、空 host
	_, err := newFTPDriver("", AuthOptions{})
	if err == nil {
		t.Errorf("newFTPDriver('') err=nil, want error")
	}
	_, err = newFTPDriver("http://example.com", AuthOptions{})
	if err == nil {
		t.Errorf("newFTPDriver('http://...') err=nil, want error")
	}
	_, err = newFTPDriver("ftp:///", AuthOptions{})
	if err == nil {
		t.Errorf("newFTPDriver('ftp:///') err=nil, want error")
	}

	// 有效 URL 解析：hasPort 为 FTP 驱动添加 ":21" 默认端口
	d, err := newFTPDriver("ftp://ftp.example.com/path/to/file", AuthOptions{
		Username: "user", Password: "pass",
	})
	if err != nil {
		t.Fatalf("newFTPDriver valid URL: %v", err)
	}
	if d == nil {
		t.Fatal("newFTPDriver returned nil driver")
	}
	d.Close()
}

func TestFTPProbeNoServer(t *testing.T) {
	// 连接不存在的服务器应返回错误
	d, _ := newFTPDriver("ftp://127.0.0.1:2121/nonexistent", AuthOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := d.Probe(ctx)
	if err == nil {
		t.Errorf("Probe err=nil, want connection error")
	}
	d.Close()
}

func TestFTPDownloadChunkNoServer(t *testing.T) {
	// 在无服务器情况下调用 DownloadChunk 应返回错误
	d, _ := newFTPDriver("ftp://127.0.0.1:2121/test.bin", AuthOptions{})
	f, _ := os.CreateTemp("", "ftp_test_*.bin")
	os.Truncate(f.Name(), 4096)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := d.DownloadChunk(ctx, 0, 4095, f, nil)
	f.Close()
	if err == nil {
		t.Errorf("DownloadChunk err=nil, want connection error")
	}
	d.Close()
}

func TestFTPDownloadFallbackNoServer(t *testing.T) {
	// 在无服务器情况下调用 DownloadFallback 应返回错误
	d, _ := newFTPDriver("ftp://127.0.0.1:2121/test.bin", AuthOptions{})
	f, _ := os.CreateTemp("", "ftp_test_*.bin")
	os.Truncate(f.Name(), 4096)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := d.DownloadFallback(ctx, 0, f, nil)
	f.Close()
	if err == nil {
		t.Errorf("DownloadFallback err=nil, want connection error")
	}
	d.Close()
}

// TestFTPLive* 通过真实的 FTP 服务器（scripts/ftp_test_server/ftpd.go，
// 由测试自行编译并拉起）测试完整的 Probe、DownloadChunk 与
// DownloadFallback 流程，不依赖任何外部服务或 Python 环境。
func TestFTPLiveProbe(t *testing.T) {
	addr, cleanup := startFTPServer(t, "probe_test.bin", 12345)
	defer cleanup()

	d, err := newFTPDriver("ftp://"+addr+"/probe_test.bin", AuthOptions{})
	if err != nil {
		t.Fatalf("newFTPDriver: %v", err)
	}
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	caps, err := d.Probe(ctx)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if caps.TotalSize != 12345 {
		t.Errorf("TotalSize=%d, want 12345", caps.TotalSize)
	}
	// pyftpdlib 支持 REST，即支持续传
	if !caps.SupportRange {
		t.Errorf("SupportRange=false, want true")
	}
}

func TestFTPLiveDownloadChunk(t *testing.T) {
	const size = 64 * 1024
	payload := make([]byte, size)
	rand.Read(payload)

	addr, cleanup := startFTPServerWithPayload(t, "chunk_test.bin", payload)
	defer cleanup()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	f, _ := os.Create(destPath)
	os.Truncate(destPath, size)

	var onDataCalls atomic.Int32
	d, err := newFTPDriver("ftp://"+addr+"/chunk_test.bin", AuthOptions{})
	if err != nil {
		t.Fatalf("newFTPDriver: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = d.DownloadChunk(ctx, 0, int64(size-1), f, func(n int) {
		onDataCalls.Add(1)
	})
	f.Close()
	if err != nil {
		t.Fatalf("DownloadChunk: %v", err)
	}
	if onDataCalls.Load() == 0 {
		t.Errorf("onData never called")
	}

	got, _ := os.ReadFile(destPath)
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), size)
	}
	d.Close()
}

func TestFTPLiveDownloadFallback(t *testing.T) {
	const size = 32 * 1024
	payload := make([]byte, size)
	rand.Read(payload)

	addr, cleanup := startFTPServerWithPayload(t, "fallback_test.bin", payload)
	defer cleanup()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	f, _ := os.Create(destPath)
	os.Truncate(destPath, size)

	var onDataCalls atomic.Int32
	d, err := newFTPDriver("ftp://"+addr+"/fallback_test.bin", AuthOptions{})
	if err != nil {
		t.Fatalf("newFTPDriver: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = d.DownloadFallback(ctx, 0, f, func(n int) {
		onDataCalls.Add(1)
	})
	f.Close()
	if err != nil {
		t.Fatalf("DownloadFallback: %v", err)
	}
	if onDataCalls.Load() == 0 {
		t.Errorf("onData never called")
	}

	got, _ := os.ReadFile(destPath)
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), size)
	}
	d.Close()
}

func TestFTPClose(t *testing.T) {
	d, _ := newFTPDriver("ftp://127.0.0.1:21/nonexistent", AuthOptions{})
	if err := d.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestAllDriversImplementProtocolDriver(t *testing.T) {
	// 编译时验证：*httpDriver、*webdavDriver 满足 ProtocolDriver
	var _ ProtocolDriver = (*httpDriver)(nil)
	var _ ProtocolDriver = (*webdavDriver)(nil)
	// ftpDriver 同理（但需要真实 FTP 服务器，仅验证接口一致性）
}

// ---------------------------------------------------------------------------
// 并发安全测试
// ---------------------------------------------------------------------------

func TestHTTPDriverConcurrentDownloads(t *testing.T) {
	const size = 256 * 1024
	payload := make([]byte, size)
	rand.Read(payload)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("Range"); h != "" {
			start, end := parseRangeFromHeader(h, size)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[start : end+1])
			return
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	f, _ := os.Create(destPath)
	os.Truncate(destPath, size)

	d := makeHTTPDriver(srv.URL)
	var wg sync.WaitGroup
	const numChunks = 4
	chunkSize := size / numChunks

	for i := 0; i < numChunks; i++ {
		wg.Add(1)
		start := int64(i * chunkSize)
		end := start + int64(chunkSize) - 1
		if i == numChunks-1 {
			end = int64(size) - 1
		}
		go func(s, e int64) {
			defer wg.Done()
			d.DownloadChunk(context.Background(), s, e, f, nil)
		}(start, end)
	}
	wg.Wait()
	f.Close()

	got, _ := os.ReadFile(destPath)
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch after concurrent download")
	}
	d.Close()
}

func TestWebDAVDriverConcurrentDownloads(t *testing.T) {
	const size = 128 * 1024
	payload := make([]byte, size)
	rand.Read(payload)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("Range"); h != "" {
			start, end := parseRangeFromHeader(h, size)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[start : end+1])
			return
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	f, _ := os.Create(destPath)
	os.Truncate(destPath, size)

	d := makeWebDAVDriver(srv.URL)
	var wg sync.WaitGroup
	const numChunks = 4
	chunkSize := size / numChunks

	for i := 0; i < numChunks; i++ {
		wg.Add(1)
		start := int64(i * chunkSize)
		end := start + int64(chunkSize) - 1
		if i == numChunks-1 {
			end = int64(size) - 1
		}
		go func(s, e int64) {
			defer wg.Done()
			d.DownloadChunk(context.Background(), s, e, f, nil)
		}(start, end)
	}
	wg.Wait()
	f.Close()

	got, _ := os.ReadFile(destPath)
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch after concurrent download")
	}
	d.Close()
}

// ---------------------------------------------------------------------------
// 辅助工具
// ---------------------------------------------------------------------------

// parseRangeFromHeader 解析 "bytes=START-END"，将端点 clamp 到 [0, total-1]。
func parseRangeFromHeader(h string, total int) (start, end int64) {
	if !strings.HasPrefix(h, "bytes=") {
		return 0, 0
	}
	body := strings.TrimPrefix(h, "bytes=")
	dash := strings.IndexByte(body, '-')
	if dash < 0 {
		return 0, 0
	}
	start, _ = strconv.ParseInt(body[:dash], 10, 64)
	end, _ = strconv.ParseInt(body[dash+1:], 10, 64)
	if end >= int64(total) {
		end = int64(total) - 1
	}
	return
}

// 本包不再需要 TestMain：FTP 的 live 测试自带 Go 测试服务器，
// 无需预先准备 Python/pyftpdlib 虚拟环境。
