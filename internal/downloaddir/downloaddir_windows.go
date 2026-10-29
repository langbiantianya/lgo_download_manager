// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build windows

package downloaddir

import "golang.org/x/sys/windows"

func systemDefault() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Downloads, windows.KF_FLAG_DEFAULT)
}
