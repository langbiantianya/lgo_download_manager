// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build !windows

package logging

// AttachParentConsole 在非 Windows 平台上始终返回 true:终端天然就是
// stdout/stderr 的目的地,不需要做任何额外动作。该 stub 让 main.go
// 在所有平台都能一致调用同一个 API。
func AttachParentConsole() bool { return true }