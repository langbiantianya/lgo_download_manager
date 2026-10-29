// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build !windows && !darwin

package downloaddir

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const xdgDownloadEntry = "XDG_DOWNLOAD_DIR"

func systemDefault() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(configHome) {
		configHome = filepath.Join(home, ".config")
	}
	if dir, ok := xdgDownloadDir(configHome, home); ok {
		return dir, nil
	}
	return filepath.Join(home, "Downloads"), nil
}

// xdgDownloadDir reads XDG_DOWNLOAD_DIR from user-dirs.dirs. The XDG format
// supports quoted values and $HOME expansion; relative paths are invalid.
func xdgDownloadDir(configHome, home string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(configHome, "user-dirs.dirs"))
	if err != nil {
		return "", false
	}
	var located string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != xdgDownloadEntry {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			}
		}
		value = strings.ReplaceAll(value, "${HOME}", home)
		value = strings.ReplaceAll(value, "$HOME", home)
		located = value
	}
	if !filepath.IsAbs(located) {
		return "", false
	}
	located = filepath.Clean(located)
	if located == filepath.Clean(home) {
		// A disabled XDG download entry resolves to the home directory.
		return "", false
	}
	return located, true
}
