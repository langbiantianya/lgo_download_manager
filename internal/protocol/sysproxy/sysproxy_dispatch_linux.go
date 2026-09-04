// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build linux

package sysproxy

// detectPlatform 在 Linux 上分派到 GNOME/KDE/etc-environment 探测链。
func detectPlatform() (Config, bool) { return detectLinux() }
