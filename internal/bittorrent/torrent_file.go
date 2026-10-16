// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package bittorrent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// MaxTorrentFileSize 是允许读取/下载的 .torrent 体积上限:种子文件本身
// 是元数据(每分片 20 字节的哈希),几十 KB 已属超大,16 MiB 只是防御
// 异常响应(避免把一整个 ISO 当成种子读进内存)。
const MaxTorrentFileSize = 16 << 20

// torrentFetchTimeout 限制远程 .torrent 的下载时长。
const torrentFetchTimeout = 30 * time.Second

// FetchTorrentFile 取回一个 .torrent 文件的内容:
//
//   - http(s):// 走网络请求,并使用 proxy 解析的代理(与 HTTP 驱动同一套
//     代理设置,可为 nil 表示直连/按环境变量);
//   - file:// 与普通本地路径直接从磁盘读取。
//
// 供 scheduler(真正添加种子)与 uimgr(体积预览)共用,保证两条路径的
// 体积上限与代理行为完全一致。
func FetchTorrentFile(ctx context.Context, raw string, proxy func(*http.Request) (*url.URL, error)) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("bittorrent: empty torrent source")
	}
	if u, err := url.Parse(trimmed); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return fetchRemoteTorrentFile(ctx, u.String(), proxy)
	}
	path := trimmed
	if u, err := url.Parse(trimmed); err == nil && u.Scheme == "file" {
		path = u.Path
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("bittorrent: read torrent file: %w", err)
	}
	if len(data) > MaxTorrentFileSize {
		return nil, fmt.Errorf("bittorrent: torrent file too large (>%d bytes)", MaxTorrentFileSize)
	}
	return data, nil
}

// fetchRemoteTorrentFile 下载 http(s) 上的 .torrent。
func fetchRemoteTorrentFile(ctx context.Context, rawURL string, proxy func(*http.Request) (*url.URL, error)) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, torrentFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("bittorrent: fetch torrent file: %w", err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: proxy}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bittorrent: fetch torrent file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bittorrent: fetch torrent file: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxTorrentFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("bittorrent: fetch torrent file: %w", err)
	}
	if len(data) > MaxTorrentFileSize {
		return nil, fmt.Errorf("bittorrent: torrent file too large (>%d bytes)", MaxTorrentFileSize)
	}
	return data, nil
}
