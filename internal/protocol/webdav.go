// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package protocol

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// webdavDriver 基于 HTTP 语义实现 ProtocolDriver：使用 PROPFIND
// 探测大小与能力，使用字节区间（RFC 7233）进行分片获取。
// 当提供用户名/密码时发送 Basic Auth；如有需要后续可再加入 Digest。
type webdavDriver struct {
	url  string
	auth AuthOptions
	cli  *http.Client
}

// propfindResponse 镜像 WebDAV multistatus 响应体中我们关心的字段。
// 故意保持最小——引擎当前尚不需要完整的 RFC 4918 字段集。
type propfindResponse struct {
	XMLName   xml.Name           `xml:"multistatus"`
	Responses []propfindRespItem `xml:"response"`
}

type propfindRespItem struct {
	Href     string `xml:"href"`
	PropStat []struct {
		Prop struct {
			GetContentLength string `xml:"getcontentlength"`
			DisplayName      string `xml:"displayname"`
			ResourceType     struct {
				Collection string `xml:"collection"`
			} `xml:"resourcetype"`
		} `xml:"prop"`
		Status string `xml:"status"`
	} `xml:"propstat"`
}

// newWebDAVDriver 是工厂入口。raw 已经是解析后的 http(s) URL
// （由工厂改写过 webdav://），因此与 httpDriver 唯一的差别
// 在于 Probe 实现。
func newWebDAVDriver(raw string, auth AuthOptions) (ProtocolDriver, error) {
	if raw == "" {
		return nil, fmt.Errorf("webdav: empty url")
	}
	tr := &http.Transport{
		Proxy:                 proxyFunc(effectiveAuth(auth)),
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &webdavDriver{
		url:  raw,
		auth: auth,
		cli:  &http.Client{Transport: tr, Timeout: 0},
	}, nil
}

func (d *webdavDriver) decorate(req *http.Request) {
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

// Probe 发起 PROPFIND Depth:0 请求并读取 getcontentlength。
func (d *webdavDriver) Probe(ctx context.Context) (*DriverCapabilities, error) {
	body := strings.NewReader(`<?xml version="1.0" encoding="utf-8"?>` +
		`<propfind xmlns="DAV:"><prop><getcontentlength xmlns="DAV:"/>` +
		`<displayname xmlns="DAV:"/><resourcetype xmlns="DAV:"/></prop></propfind>`)
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", d.url, body)
	if err != nil {
		return nil, fmt.Errorf("webdav probe build: %w", err)
	}
	d.decorate(req)
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", "application/xml")

	resp, err := d.cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webdav probe do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return d.fallbackProbe(ctx)
	}

	var parsed propfindResponse
	dec := xml.NewDecoder(resp.Body)
	if err := dec.Decode(&parsed); err != nil {
		return d.fallbackProbe(ctx)
	}

	caps := &DriverCapabilities{SupportRange: true, TotalSize: -1}
	for _, r := range parsed.Responses {
		for _, ps := range r.PropStat {
			if ps.Status != "" && !strings.Contains(ps.Status, "200") {
				continue
			}
			if n, err := strconv.ParseInt(strings.TrimSpace(ps.Prop.GetContentLength), 10, 64); err == nil {
				caps.TotalSize = n
			}
		}
	}
	if caps.TotalSize < 0 {
		return d.fallbackProbe(ctx)
	}
	return caps, nil
}

// fallbackProbe 在 PROPFIND 失败时改用 HEAD 进行探测。
func (d *webdavDriver) fallbackProbe(ctx context.Context) (*DriverCapabilities, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, d.url, nil)
	if err != nil {
		return nil, fmt.Errorf("webdav fallback head build: %w", err)
	}
	d.decorate(req)
	resp, err := d.cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webdav fallback head do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, NewStatusError(resp.StatusCode, fmt.Errorf("webdav probe: status %d", resp.StatusCode))
	}
	caps := &DriverCapabilities{
		TotalSize:    parseInt64(resp.Header.Get("Content-Length")),
		SupportRange: strings.EqualFold(resp.Header.Get("Accept-Ranges"), "bytes"),
		ServerInfo:   strings.Join(resp.Header["Server"], ", "),
	}
	return caps, nil
}

// DownloadChunk 在概念上与 httpDriver.DownloadChunk 一致。我们
// 保留一份并行实现（而非组合）以便未来在不改动 HTTP 的前提下
// 增加 WebDAV 特有的行为（锁、条件 GET 等）。
func (d *webdavDriver) DownloadChunk(ctx context.Context, start, end int64, file *os.File, onData func(n int)) error {
	if start < 0 || end < start {
		return fmt.Errorf("webdav chunk: invalid range [%d,%d]", start, end)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return fmt.Errorf("webdav chunk build: %w", err)
	}
	d.decorate(req)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := d.cli.Do(req)
	if err != nil {
		return fmt.Errorf("webdav chunk do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return NewStatusError(resp.StatusCode, fmt.Errorf("webdav chunk: status %d", resp.StatusCode))
	}

	buf := make([]byte, chunkSize)
	off := start
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := file.WriteAt(buf[:n], off); werr != nil {
				return fmt.Errorf("webdav chunk write: %w", werr)
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
			return fmt.Errorf("webdav chunk read: %w", rerr)
		}
		if off > end+1 {
			break
		}
	}
	return nil
}

// DownloadFallback 从 offset 处开始流式读取。
func (d *webdavDriver) DownloadFallback(ctx context.Context, offset int64, file *os.File, onData func(n int)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return fmt.Errorf("webdav fallback build: %w", err)
	}
	d.decorate(req)
	resp, err := d.cli.Do(req)
	if err != nil {
		return fmt.Errorf("webdav fallback do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return NewStatusError(resp.StatusCode, fmt.Errorf("webdav fallback: status %d", resp.StatusCode))
	}

	buf := make([]byte, chunkSize)
	off := offset
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := file.WriteAt(buf[:n], off); werr != nil {
				return fmt.Errorf("webdav fallback write: %w", werr)
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
			return fmt.Errorf("webdav fallback read: %w", rerr)
		}
	}
	return nil
}

// Close 关闭底层 http.Client 的空闲连接。
func (d *webdavDriver) Close() error {
	if tr, ok := d.cli.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
	return nil
}
