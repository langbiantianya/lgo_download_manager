// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build windows

package sysproxy

// detectPlatform 在 Windows 上分派到 WinINET 注册表读取。
func detectPlatform() (Config, bool) { return detectWindows() }
