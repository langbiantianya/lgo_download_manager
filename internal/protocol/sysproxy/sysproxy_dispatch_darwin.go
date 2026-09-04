// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build darwin

package sysproxy

// detectPlatform 在 macOS 上分派到 scutil 探测。
func detectPlatform() (Config, bool) { return detectDarwin() }
