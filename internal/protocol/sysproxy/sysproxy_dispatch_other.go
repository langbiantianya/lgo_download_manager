// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build !linux && !darwin && !windows

package sysproxy

// detectPlatform 在不支持的平台上返回 false —— Detect() 会降级到只读环境变量。
func detectPlatform() (Config, bool) { return Config{}, false }
