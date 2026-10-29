// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package downloaddir resolves the current user's default download directory.
package downloaddir

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Default returns the current user's platform download directory. A missing
// desktop configuration falls back to the platform's standard directory.
func Default() string {
	if dir, err := systemDefault(); err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Clean(dir)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "Downloads")
	}
	return "."
}

func homeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("user home directory is unavailable")
	}
	return home, nil
}
