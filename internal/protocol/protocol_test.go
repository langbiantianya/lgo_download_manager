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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestParseTotalFromContentRange(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"bytes 0-0/12345", 12345},
		{"bytes 0-99/100", 100},
		{"bytes 0-0/0", 0},
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
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.Header().Set("Accept-Ranges", "bytes")
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
	f, _ := os.Create(filepath.Join(dir, "junk.bin"))
	os.Truncate(filepath.Join(dir, "junk.bin"), 100)
	f, _ = os.OpenFile(filepath.Join(dir, "junk.bin"), os.O_RDWR, 0)

	err := d.DownloadChunk(context.Background(), 0, 99, f, nil)
	if err == nil {
		t.Errorf("DownloadChunk err=nil, want error for 404")
	}
	f.Close()
	d.Close()
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
	if err != nil {
		t.Fatalf("DownloadFallback: %v", err)
	}

	got, _ := os.ReadFile(destPath)
	if !bytes.Equal(got[:4096], make([]byte, 4096)) {
		t.Errorf("offset area not zero")
	}
	// DownloadFallback 从服务器 offset 0 读取，但写入 file 的 offset 4096。
	// 字节 0..4095 保持为零（从未写入）。
	d.Close()
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
// FTP Driver 测试（使用 Python ftpdlib 虚拟环境）
// ---------------------------------------------------------------------------

	// pythonVenv 是 pyftpdlib FTP 测试服务器的 Python 虚拟环境路径。
	// 由 TestMain 在测试开始前创建。
	// 如需修改，请同时更新 scripts/setup_test_env.sh 和 scripts/ftp_test_server/setup.sh 中的路径。
	var pythonVenv = "/tmp/ftp_pytest_venv"

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
	ftpdBin := filepath.Join(os.TempDir(), fmt.Sprintf("ftpd_test_%d", os.Getpid()))
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
	ftpdBin := filepath.Join(os.TempDir(), fmt.Sprintf("ftpd_test_%d", os.Getpid()))
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


// TestFTPLive* 通过真实的 Python ftpdlib FTP 服务器测试完整的 Probe 与
// DownloadChunk 流程。pyftpdlib 虚拟环境由 scripts/setup_test_env.sh 创建。
//
// 如需运行，先初始化环境后启动 FTP 服务器：
//   ./scripts/setup_test_env.sh
//   /tmp/ftp_pytest_venv/bin/python3 scripts/ftp_server.py
// 然后运行测试（TestFTPLive* 默认跳过，需手动取消 Skip 或用 -run 精确匹配）。
//
// 注意：在某些容器环境中 setsid(1) 无法正常守护化 Python 进程，
// 导致连接立即被 RST。如遇此问题请改用手动启动服务器的方式。
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

// ---------------------------------------------------------------------------
// TestMain：初始化 Python 虚拟环境
// ---------------------------------------------------------------------------

// TestMain：初始化 Python 虚拟环境
func TestMain(m *testing.M) {
	// 初始化 Python 虚拟环境（pyftpdlib）。
	// 虚拟环境由 scripts/setup_test_env.sh 创建，或由本函数在首次运行时自动创建。
	// 如需手动初始化，请运行：./scripts/setup_test_env.sh
	venv := "/tmp/ftp_pytest_venv"
	if _, err := os.Stat(venv); os.IsNotExist(err) {
		if out, err := exec.Command("python3", "-m", "venv", venv).CombinedOutput(); err != nil {
			fmt.Printf("cannot create venv: %v\n%s\n", err, out)
			os.Exit(1)
		}
		if out, err := exec.Command(filepath.Join(venv, "bin", "pip"), "install", "pyftpdlib", "-q").CombinedOutput(); err != nil {
			fmt.Printf("cannot install pyftpdlib: %v\n%s\n", err, out)
			os.Exit(1)
		}
	}
	pythonVenv = venv

	os.Exit(m.Run())
}
