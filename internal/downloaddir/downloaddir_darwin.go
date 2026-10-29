// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build darwin

package downloaddir

import "path/filepath"

func systemDefault() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	// Finder localizes the display name, but the current user's Downloads
	// folder has this fixed filesystem location on macOS.
	return filepath.Join(home, "Downloads"), nil
}
