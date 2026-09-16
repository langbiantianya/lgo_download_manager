// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build !windows && !linux && !darwin

package autostart

import "errors"

// errUnsupported 在 FreeBSD / OpenBSD / Solaris 等暂不支持的系统上返回;
// settings.ApplyAutoStart 看到这些错误会记日志,但不会让 UI 提交失败。
var errUnsupported = errors.New("autostart: not supported on this platform")

func Enable() error    { return errUnsupported }
func Disable() error   { return nil }
func IsEnabled() (bool, error) { return false, nil }