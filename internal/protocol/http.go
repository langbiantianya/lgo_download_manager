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
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// chunkSize 是将响应体字节流式写入磁盘时使用的读取缓冲区大小。
// 256 KiB 在顺序吞吐与系统调用开销之间取得较好的折中。
const chunkSize = 256 * 1024

// httpDriver 为 HTTP 与 HTTPS 实现 ProtocolDriver。每个驱动实例
// 仅持有一个 http.Client，因为每个分块 goroutine 都会使用自己
// 的短生命周期客户端（Transport 中的 round-tripper 可被复用）。
type httpDriver struct {
	url  string
	auth AuthOptions
	cli  *http.Client
}

// newHTTPDriver 是由 New(...) 调用的工厂入口。
func newHTTPDriver(raw string, auth AuthOptions) (ProtocolDriver, error) {
	if raw == "" {
		return nil, fmt.Errorf("http: empty url")
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &httpDriver{
		url:  raw,
		auth: auth,
		cli:  &http.Client{Transport: tr, Timeout: 0}, // 不设整体超时；由引擎的 ctx 控制
	}, nil
}

// decorate 将用户选项应用到新请求上：User-Agent、Cookies、Referer，
// 以及（可选的）Basic Auth。仅当用户名与密码均非空时才会发送
// Basic Auth——避免发送一组不完整的 Authorization 头。
func (d *httpDriver) decorate(req *http.Request) {
	if d.auth.UserAgent != "" {
		req.Header.Set("User-Agent", d.auth.UserAgent)
	}
	if d.auth.Cookies != "" {
		req.Header.Set("Cookie", d.auth.Cookies)
	}
	if d.auth.Referer != "" {
		req.Header.Set("Referer", d.auth.Referer)
	}
	if d.auth.Username != "" || d.auth.Password != "" {
		req.SetBasicAuth(d.auth.Username, d.auth.Password)
	}
}

// Probe 发起 HEAD 请求（失败时回退为 1 字节的 Range GET）以探测
// 文件大小以及服务器是否支持字节区间（Range）。
func (d *httpDriver) Probe(ctx context.Context) (*DriverCapabilities, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, d.url, nil)
	if err != nil {
		return nil, fmt.Errorf("http probe build: %w", err)
	}
	d.decorate(req)

	resp, err := d.cli.Do(req)
	if err != nil {
		// 部分服务器屏蔽 HEAD；改用最小的区间 GET 重试。
		return d.probeWithRange(ctx)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http probe: status %d", resp.StatusCode)
	}

	caps := &DriverCapabilities{
		ServerInfo:   strings.Join(resp.Header["Server"], ", "),
		SupportRange: supportsRanges(resp),
		TotalSize:    parseInt64(resp.Header.Get("Content-Length")),
	}
	// 部分服务器在 HEAD 响应中省略该字段，但在 GET 响应中包含。
	if caps.TotalSize < 0 {
		return d.probeWithRange(ctx)
	}
	return caps, nil
}

// probeWithRange 发起 1 字节的区间 GET，以促使服务器同时返回
// Content-Length 与 Accept-Ranges 字段。
func (d *httpDriver) probeWithRange(ctx context.Context) (*DriverCapabilities, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return nil, fmt.Errorf("http probe range build: %w", err)
	}
	d.decorate(req)
	req.Header.Set("Range", "bytes=0-0")

	resp, err := d.cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http probe range do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http probe range: status %d", resp.StatusCode)
	}
	caps := &DriverCapabilities{
		SupportRange: resp.StatusCode == http.StatusPartialContent,
		ServerInfo:   strings.Join(resp.Header["Server"], ", "),
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		caps.TotalSize = parseTotalFromContentRange(cr)
	} else {
		caps.TotalSize = parseInt64(resp.Header.Get("Content-Length"))
	}
	// 排掉我们请求的那 1 字节响应体；不需要保留。
	_, _ = io.Copy(io.Discard, resp.Body)
	return caps, nil
}

// supportsRanges 在服务器声明支持区间下载时返回 true。
// "bytes" 是规范取值；部分服务器返回 "1"，或干脆不返回该头。
func supportsRanges(resp *http.Response) bool {
	v := strings.ToLower(resp.Header.Get("Accept-Ranges"))
	if v == "" {
		return false
	}
	return v == "bytes" || v == "1"
}

// parseInt64 解析一个可能为空的头字段值为 int64，缺失或非法时返回 -1。
func parseInt64(s string) int64 {
	if s == "" {
		return -1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// parseTotalFromContentRange 解析形如 "bytes 0-0/12345" 的 Content-Range，
// 返回完整的总字节数 12345。
func parseTotalFromContentRange(s string) int64 {
	idx := strings.LastIndex(s, "/")
	if idx < 0 {
		return -1
	}
	return parseInt64(strings.TrimSpace(s[idx+1:]))
}
// DownloadChunk 以 "Range: bytes=start-end" 发起 HTTP GET，
// 并将响应体从 `start` 偏移处开始写入 file。每次 Read 后，
// 都会以本次追加的字节数调用 onData（便于实时进度展示）。
func (d *httpDriver) DownloadChunk(ctx context.Context, start, end int64, file *os.File, onData func(n int)) error {
	if start < 0 || end < start {
		return fmt.Errorf("http chunk: invalid range [%d,%d]", start, end)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return fmt.Errorf("http chunk build: %w", err)
	}
	d.decorate(req)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := d.cli.Do(req)
	if err != nil {
		return fmt.Errorf("http chunk do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("http chunk: status %d", resp.StatusCode)
	}

	buf := make([]byte, chunkSize)
	off := start
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := file.WriteAt(buf[:n], off); werr != nil {
				return fmt.Errorf("http chunk write: %w", werr)
			}
			off += int64(n)
			if onData != nil {
				onData(n)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("http chunk read: %w", rerr)
		}
		if off > end+1 {
			// 防御性处理：部分服务器会超出请求的 end 边界。
			// 一旦越过 boundary 即停止写入。
			break
		}
	}
	return nil
}

// DownloadFallback 是针对不支持 Range 的服务器的单流回退方案，
// 从 `offset` 偏移处开始写入。
func (d *httpDriver) DownloadFallback(ctx context.Context, offset int64, file *os.File, onData func(n int)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return fmt.Errorf("http fallback build: %w", err)
	}
	d.decorate(req)

	resp, err := d.cli.Do(req)
	if err != nil {
		return fmt.Errorf("http fallback do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("http fallback: status %d", resp.StatusCode)
	}

	buf := make([]byte, chunkSize)
	off := offset
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := file.WriteAt(buf[:n], off); werr != nil {
				return fmt.Errorf("http fallback write: %w", werr)
			}
			off += int64(n)
			if onData != nil {
				onData(n)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("http fallback read: %w", rerr)
		}
	}
	return nil
}

// Close 关闭底层 http.Client 的空闲连接。可以重复调用。
func (d *httpDriver) Close() error {
	if tr, ok := d.cli.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
	return nil
}
