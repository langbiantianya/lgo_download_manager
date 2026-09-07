// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package urllauncher provides:
//
//   - Cross-platform lgom:// URL parsing (HandleURL, DownloadRequest).
//   - Cross-platform single-instance lock (AcquireLock).
//   - Linux-only URL forwarding: a second instance forwards lgom:// URLs
//     to the primary instance via a Unix domain socket (ListenAndServe,
//     SendURL). On Windows, URL forwarding is currently not implemented
//     because the package does not yet wire a Windows-native IPC channel
//     for that role; the single-instance lock still works.
package urllauncher

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

const (
	socketName = "instance.sock"
	lockName   = "instance.lock"
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
	baseDir = func() string {
		dir, _ := os.UserHomeDir()
		if dir == "" {
			dir = "/tmp"
		}
		return filepath.Join(dir, ".local/share/lgo_download_manager")
	}()
	socketPath = filepath.Join(baseDir, socketName)
	lockPath   = filepath.Join(baseDir, lockName)
)

// SetBaseDir overrides the base directory (useful for testing).
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
