// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package urllauncher provides:
//
//   - Cross-platform lgom:// URL parsing (HandleURL, DownloadRequest).
//   - Cross-platform single-instance lock (AcquireLock).
//   - URL forwarding from a secondary instance to the primary instance
//     (ListenAndServe, SendURL). Linux/macOS 用 Unix domain socket,
//     Windows 用 per-user 命名管道;两种传输共用同一份线上格式
//     (4 字节大端长度前缀 + JSON 负载),见 readURLFrame/writeURLFrame。
package urllauncher

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
)

const (
	socketName = "instance.sock"
	lockName   = "instance.lock"

	// maxFrameLen 是单帧负载上限。对端可以把长度前缀写成任意 32 位
	// 数字,不设上限就会让我们按它分配内存。
	maxFrameLen = 64 * 1024
)

// DownloadRequest holds parsed URL parameters.
type DownloadRequest struct {
	URL     string
	Name    string
	UA      string
	Headers string
	Cookies string
}

var (
	baseDir    = defaultBaseDir()
	socketPath = filepath.Join(baseDir, socketName)
	lockPath   = filepath.Join(baseDir, lockName)
)

// SetBaseDir overrides the base directory (useful for testing).
//
// Windows 上这个目录不落盘:它只被用来推导单实例互斥体与命名管道的名字
// (见 urllauncher_windows.go 的 instanceName)。
func SetBaseDir(dir string) {
	baseDir = dir
	socketPath = filepath.Join(baseDir, socketName)
	lockPath = filepath.Join(baseDir, lockName)
}

// HandleURL parses an lgom://download?... URL and returns a DownloadRequest.
func HandleURL(rawURL string) (*DownloadRequest, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "lgom" || u.Host != "download" {
		return nil, fmt.Errorf("invalid scheme or host: %s", rawURL)
	}

	q := u.Query()
	return &DownloadRequest{
		URL:     q.Get("url"),
		Name:    q.Get("name"),
		UA:      q.Get("ua"),
		Headers: q.Get("headers"),
		Cookies: q.Get("cookies"),
	}, nil
}

// urlFrame 是转发一条 URL 的线上负载。
type urlFrame struct {
	URL string `json:"url"`
}

// readURLFrame 读取一帧并返回其中的 URL。长度前缀非法、负载超限或
// JSON 无法解析都返回错误。
func readURLFrame(r io.Reader) (string, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return "", fmt.Errorf("read frame length: %w", err)
	}
	if length > maxFrameLen {
		return "", fmt.Errorf("frame too large: %d bytes", length)
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("read frame body: %w", err)
	}

	var f urlFrame
	if err := json.Unmarshal(buf, &f); err != nil {
		return "", fmt.Errorf("decode frame: %w", err)
	}
	return f.URL, nil
}

// writeURLFrame 写出一帧:4 字节大端长度前缀 + JSON 负载。
func writeURLFrame(w io.Writer, rawURL string) error {
	data, err := json.Marshal(urlFrame{URL: rawURL})
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(data))); err != nil {
		return fmt.Errorf("write frame length: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write frame body: %w", err)
	}
	return nil
}
