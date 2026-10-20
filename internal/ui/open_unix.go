// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build !windows

package ui

import "os/exec"

// platformOpenFolder 在 POSIX 平台上以「默认文件管理器」打开目录。
// 优先 xdg-open(Linux),回退 open(macOS / BSD)。exec.Command 找不到
// 对应二进制时 Run 返回 *exec.Error,这里吞掉——按钮点击不应弹错。
//
// 整个过程放在后台 goroutine 里:xdg-open 会一直等到文件管理器真正起来
// (部分发行版还要等 dbus 握手),同步执行会把 Fyne 的事件线程卡住,
// 表现为点一下「打开文件夹」整个窗口就僵住。
func platformOpenFolder(dir string) {
	go func() {
		if err := exec.Command("xdg-open", dir).Run(); err == nil {
			return
		}
		// macOS / 类 BSD 系统的「open」工具不在 PATH 时同样静默。
		_ = exec.Command("open", dir).Run()
	}()
}

// platformOpenFile 在 POSIX 平台上以「默认应用」打开文件。语义与
// platformOpenFolder 一致:先 xdg-open,失败回退 open。同样异步执行,
// 不占用事件线程。
func platformOpenFile(file string) {
	go func() {
		if err := exec.Command("xdg-open", file).Run(); err == nil {
			return
		}
		_ = exec.Command("open", file).Run()
	}()
}