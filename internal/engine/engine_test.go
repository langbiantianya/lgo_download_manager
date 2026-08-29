// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lgo_download_manager/internal/prealloc"
	"lgo_download_manager/internal/protocol"
)

// TestChunkedDownload 启动一个 HTTP 服务器来提供一个随机生成的文件,
// 并验证引擎能用 8 个分片逐字节地还原该文件。
func TestChunkedDownload(t *testing.T) {
	const size = 4 * 1024 * 1024
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			start, end, err := parseRange(rng, size)
			if err == nil {
				w.Header().Set("Content-Range", "bytes "+itoa(start)+"-"+itoa(end)+"/"+itoa(size))
				w.Header().Set("Content-Length", itoa(end-start+1))
				w.Header().Set("Accept-Ranges", "bytes")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(payload[start : end+1])
				return
			}
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", itoa(size))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	dest, err := prealloc.Preallocate(destPath, size)
	if err != nil {
		t.Fatalf("prealloc: %v", err)
	}
	defer dest.Close()

	driver, err := protocol.New(srv.URL, protocol.ProtoHTTP, protocol.Auth{})
	if err != nil {
		t.Fatalf("driver: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var progressCalls atomic.Int32
	var lastProg Progress
	job := NewJob(driver, size, dest, Options{
		ChunkCount:    8,
		ProgressEvery: 50 * time.Millisecond,
		Progress: func(p Progress) {
			progressCalls.Add(1)
			lastProg = p
		},
	})

	if err := job.Run(ctx, true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := job.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if len(got) != size {
		t.Fatalf("len(got)=%d, want %d", len(got), size)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch")
	}
	if progressCalls.Load() == 0 {
		t.Fatalf("progress never fired")
	}
	if lastProg.DownloadedBytes != size {
		t.Fatalf("last progress DownloadedBytes=%d, want %d", lastProg.DownloadedBytes, size)
	}
}

// TestFallback 通过 fallback 路径(无 range)下载一个较小的文件。
func TestFallback(t *testing.T) {
	const size = 256 * 1024
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			http.Error(w, "no range support", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Length", itoa(size))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	dest, err := prealloc.Preallocate(destPath, size)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()

	driver, err := protocol.New(srv.URL, protocol.ProtoHTTP, protocol.Auth{})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	job := NewJob(driver, -1, dest, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := job.Run(ctx, false); err != nil {
		t.Fatalf("Run fallback: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("mismatch")
	}
}

// TestRealDownload 真实地驱动引擎对一个支持 Content-Length 和 Range 的
// HTTP 服务器进行下载。在 short 模式或服务器不可达时跳过。
func TestRealDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real download in short mode")
	}

	// 使用 CDN 上的 Go 源码压缩包——体积小、速度快,而且能稳定地
	// 提供 byte-range 请求支持。

	const url = "https://dl.google.com/go/go1.22.3.src.tar.gz"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	headResp, err := http.Head(url)
	cancel()
	if err != nil {
		t.Skipf("skipping: cannot reach server: %v", err)
	}
	headResp.Body.Close()
	if headResp.StatusCode != http.StatusOK {
		t.Skipf("skipping: server returned %s", headResp.Status)
	}

	totalSize := headResp.ContentLength
	if totalSize <= 0 {
		t.Skip("skipping: server did not advertise Content-Length")
	}

	// 限制为前 256 KiB,以使测试时间保持合理。
	const maxSize = 256 * 1024
	if totalSize > maxSize {
		totalSize = maxSize
	}

	driver, err := protocol.New(url, protocol.ProtoHTTP, protocol.Auth{})
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	defer driver.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	dest, err := prealloc.Preallocate(destPath, totalSize)
	if err != nil {
		t.Fatalf("prealloc: %v", err)
	}
	defer dest.Close()
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
	// 给下载最多 2 分钟——某些镜像源连接较慢。
	defer cancel()

	var progressCalls atomic.Int32
	var lastProg Progress
	job := NewJob(driver, totalSize, dest, Options{
		ChunkCount:    4,
		ProgressEvery: 1 * time.Second,
		Progress: func(p Progress) {
			progressCalls.Add(1)
			lastProg = p
		},
	})

	if err := job.Run(ctx, true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := job.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	fi, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if fi.Size() != totalSize {
		t.Fatalf("dest size=%d want %d", fi.Size(), totalSize)
	}
	if progressCalls.Load() == 0 {
		t.Fatal("progress never fired")
	}
	if lastProg.DownloadedBytes != totalSize {
		t.Fatalf("last progress DownloadedBytes=%d want %d", lastProg.DownloadedBytes, totalSize)
	}
}

// parseRange 解析 "bytes=START-END",并把端点裁剪到 total-1。
func parseRange(s string, total int64) (int64, int64, error) {
	if !strings.HasPrefix(s, "bytes=") {
		return 0, 0, errBadRange
	}
	body := strings.TrimPrefix(s, "bytes=")
	dash := strings.IndexByte(body, '-')
	if dash < 0 {
		return 0, 0, errBadRange
	}
	start, err := strconv.ParseInt(body[:dash], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	end, err := strconv.ParseInt(body[dash+1:], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	if end >= total {
		end = total - 1
	}
	return start, end, nil
}

var errBadRange = badRangeErr{}

type badRangeErr struct{}

func (badRangeErr) Error() string { return "bad range" }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
