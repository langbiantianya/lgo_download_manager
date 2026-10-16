// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package handoff

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// freePort 返回一个当前可绑定的端口号。注册与关闭之间理论上存在竞态，
// 但组合上 StartOn 的顺延逻辑，测试仍然确定。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen ephemeral: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func startServer(t *testing.T, handler func(Request) error) *Server {
	t.Helper()
	s, err := StartOn([]int{freePort(t)}, handler)
	if err != nil {
		t.Fatalf("StartOn: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func post(t *testing.T, s *Server, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/download", s.Port()),
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestPingReportsAppIdentity(t *testing.T) {
	s := startServer(t, func(Request) error { return nil })

	res, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/ping", s.Port()))
	if err != nil {
		t.Fatalf("get ping: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}

	var info Info
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		t.Fatalf("decode ping: %v", err)
	}
	if info.App != appID {
		t.Errorf("app = %q, want %q", info.App, appID)
	}
	if info.API != apiVersion {
		t.Errorf("api = %d, want %d", info.API, apiVersion)
	}
	if info.Port != s.Port() {
		t.Errorf("port = %d, want %d", info.Port, s.Port())
	}
}

func TestPingRejectsWebOrigin(t *testing.T) {
	s := startServer(t, func(Request) error { return nil })

	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/ping", s.Port()), nil)
	req.Header.Set("Origin", "https://evil.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get ping: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.StatusCode)
	}
}

func TestDownloadDeliversRequest(t *testing.T) {
	var got Request
	calls := 0
	s := startServer(t, func(r Request) error {
		calls++
		got = r
		return nil
	})

	body := `{"url":"https://example.com/a.zip","name":"a.zip","ua":"Mozilla/5.0","headers":"Referer: https://example.com/","cookies":"sid=1"}`
	res := post(t, s, body, map[string]string{
		clientHeader: "extension",
		"Origin":     "chrome-extension://abcdefghijklmnopabcdefghijklmnop",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}
	if got.URL != "https://example.com/a.zip" || got.Name != "a.zip" {
		t.Errorf("handler got %+v", got)
	}
	if got.Cookies != "sid=1" || got.Headers == "" || got.UA == "" {
		t.Errorf("handler lost metadata: %+v", got)
	}

	// 扩展来源要拿到回显的 CORS 头，否则读不到响应体。
	if res.Header.Get("Access-Control-Allow-Origin") == "" {
		t.Error("missing Access-Control-Allow-Origin for extension origin")
	}
}

func TestDownloadAcceptsMissingOrigin(t *testing.T) {
	// 不带 Origin 的调用方（curl、脚本）与业务进程同权限。
	calls := 0
	s := startServer(t, func(Request) error {
		calls++
		return nil
	})
	res := post(t, s, `{"url":"https://example.com/b.zip"}`, map[string]string{
		clientHeader: "cli",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}
}

func TestDownloadRejectsWebOrigin(t *testing.T) {
	calls := 0
	s := startServer(t, func(Request) error {
		calls++
		return nil
	})
	res := post(t, s, `{"url":"https://example.com/c.zip"}`, map[string]string{
		clientHeader: "extension",
		"Origin":     "https://evil.example",
	})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.StatusCode)
	}
	if calls != 0 {
		t.Fatalf("handler calls = %d, want 0", calls)
	}
}

func TestDownloadRejectsNullOrigin(t *testing.T) {
	// Origin: null 来自沙箱 iframe / data: 页面，不是扩展页面。
	s := startServer(t, func(Request) error { return nil })
	res := post(t, s, `{"url":"https://example.com/d.zip"}`, map[string]string{
		clientHeader: "extension",
		"Origin":     "null",
	})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.StatusCode)
	}
}

func TestDownloadRequiresClientHeader(t *testing.T) {
	calls := 0
	s := startServer(t, func(Request) error {
		calls++
		return nil
	})
	res := post(t, s, `{"url":"https://example.com/e.zip"}`, nil)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
	if calls != 0 {
		t.Fatalf("handler calls = %d, want 0", calls)
	}
}

func TestDownloadRejectsBadBodies(t *testing.T) {
	s := startServer(t, func(Request) error { return nil })
	cases := map[string]string{
		"not json":     `{`,
		"missing url":  `{"name":"a.zip"}`,
		"relative url": `{"url":"/just/a/path"}`,
		"non-absolute": `{"url":"example.com/a.zip"}`,
		"blank url":    `{"url":"   "}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			res := post(t, s, body, map[string]string{clientHeader: "extension"})
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", res.StatusCode)
			}
			var payload struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if payload.OK || payload.Error == "" {
				t.Errorf("body = %+v, want ok=false with an error", payload)
			}
		})
	}
}

func TestDownloadReportsHandlerError(t *testing.T) {
	s := startServer(t, func(Request) error { return errors.New("no ui") })
	res := post(t, s, `{"url":"https://example.com/f.zip"}`, map[string]string{
		clientHeader: "extension",
	})
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !bytes.Contains(body, []byte("no ui")) {
		t.Errorf("body = %s, want the handler error", body)
	}
}

func TestPreflightAllowsExtensionOriginOnly(t *testing.T) {
	s := startServer(t, func(Request) error { return nil })

	do := func(origin string) *http.Response {
		req, _ := http.NewRequest(
			http.MethodOptions,
			fmt.Sprintf("http://127.0.0.1:%d/download", s.Port()),
			nil,
		)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", clientHeader)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("options: %v", err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return res
	}

	ext := do("chrome-extension://abcdefghijklmnopabcdefghijklmnop")
	if ext.StatusCode != http.StatusNoContent {
		t.Fatalf("extension preflight status = %d, want 204", ext.StatusCode)
	}
	if got := ext.Header.Get("Access-Control-Allow-Origin"); got == "" {
		t.Error("extension preflight missing Access-Control-Allow-Origin")
	}
	if !strings.Contains(ext.Header.Get("Access-Control-Allow-Headers"), clientHeader) {
		t.Errorf("preflight allow-headers = %q", ext.Header.Get("Access-Control-Allow-Headers"))
	}

	web := do("https://evil.example")
	if web.StatusCode != http.StatusForbidden {
		t.Fatalf("web preflight status = %d, want 403", web.StatusCode)
	}
	if got := web.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("web preflight leaked origin %q", got)
	}
}

func TestPreflightAnswersPrivateNetworkRequest(t *testing.T) {
	s := startServer(t, func(Request) error { return nil })
	req, _ := http.NewRequest(
		http.MethodOptions,
		fmt.Sprintf("http://127.0.0.1:%d/download", s.Port()),
		nil,
	)
	req.Header.Set("Origin", "chrome-extension://abcdefghijklmnopabcdefghijklmnop")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Private-Network", "true")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	defer res.Body.Close()
	if got := res.Header.Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Errorf("allow-private-network = %q, want true", got)
	}
}

func TestStartOnSkipsBusyPorts(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port

	want := freePort(t)
	s, err := StartOn([]int{busyPort, want}, func(Request) error { return nil })
	if err != nil {
		t.Fatalf("StartOn: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.Port() != want {
		t.Fatalf("port = %d, want %d", s.Port(), want)
	}
}

func TestStartOnFailsWhenAllPortsBusy(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer busy.Close()

	s, err := StartOn([]int{busy.Addr().(*net.TCPAddr).Port}, func(Request) error { return nil })
	if err == nil {
		_ = s.Close()
		t.Fatal("StartOn succeeded on a busy port")
	}
	if !strings.Contains(err.Error(), "no free port") {
		t.Fatalf("error = %v, want the no-free-port message", err)
	}
}

func TestStartOnRejectsNilHandler(t *testing.T) {
	if _, err := StartOn([]int{freePort(t)}, nil); err == nil {
		t.Fatal("StartOn accepted a nil handler")
	}
}

func TestCloseReleasesPort(t *testing.T) {
	port := freePort(t)
	s, err := StartOn([]int{port}, func(Request) error { return nil })
	if err != nil {
		t.Fatalf("StartOn: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	again, err := StartOn([]int{port}, func(Request) error { return nil })
	if err != nil {
		t.Fatalf("StartOn after Close: %v", err)
	}
	_ = again.Close()
}

func TestWriteInfo(t *testing.T) {
	s := startServer(t, func(Request) error { return nil })
	path := filepath.Join(t.TempDir(), "handoff.json")
	if err := s.WriteInfo(path); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read info: %v", err)
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("decode info: %v", err)
	}
	if info.App != appID || info.Port != s.Port() || info.PID != os.Getpid() {
		t.Errorf("info = %+v", info)
	}
}

func TestPortCandidatesAreLoopbackOnlyAndStable(t *testing.T) {
	// 扩展侧硬编码同一份候选表；改动这里必须同步 constants.js。
	want := []int{17821, 17822, 17823, 17824, 17825}
	if len(PortCandidates) != len(want) {
		t.Fatalf("PortCandidates = %v, want %v", PortCandidates, want)
	}
	for i, port := range want {
		if PortCandidates[i] != port {
			t.Fatalf("PortCandidates = %v, want %v", PortCandidates, want)
		}
		if port < 1024 || port >= 32768 {
			t.Errorf("port %d outside the 1024-32767 range (must not clash with ephemeral ports)", port)
		}
	}
}

// 服务端只应绑定 127.0.0.1：本机地址可用，非回环地址上连不上。
func TestServerBindsLoopbackOnly(t *testing.T) {
	s := startServer(t, func(Request) error { return nil })
	res, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/ping", s.Port()))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}

	hostIP := firstNonLoopbackIPv4(t)
	if hostIP == "" {
		t.Skip("no non-loopback IPv4 address to probe")
	}
	conn, err := net.DialTimeout(
		"tcp",
		net.JoinHostPort(hostIP, strconv.Itoa(s.Port())),
		500*time.Millisecond,
	)
	if err == nil {
		conn.Close()
		t.Fatalf("service reachable on non-loopback address %s", hostIP)
	}
}

// firstNonLoopbackIPv4 返回本机第一个非回环 IPv4 地址，没有就返回空串。
func firstNonLoopbackIPv4(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipNet.IP.To4(); ip4 != nil {
			return ip4.String()
		}
	}
	return ""
}
