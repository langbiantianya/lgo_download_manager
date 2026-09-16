// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build windows

package logging

import "syscall"

// AttachParentConsole 把当前进程重新挂到其父进程的 console 上
// (典型场景:从 cmd/PowerShell 启动一个 GUI 子系统构建出来的 .exe,
// 用户期望日志直接落到终端而不是凭空消失)。
//
// 返回值:
//   - true  :挂接成功,本进程后续对 os.Stdout/os.Stderr 的写入会出现在
//            父进程的 console 窗口里。
//   - false :没有父 console(例如从 Explorer/浏览器 URL 协议拉起),
//            或者 OS 拒绝了挂接。这种情况下日志应当走文件回退路径。
//
// 该函数只对用 -H windowsgui 编译出来的二进制有意义:console 子系统
// 的二进制天然就有 console,不需要重挂。
func AttachParentConsole() bool {
	h, err := syscall.LoadDLL("kernel32.dll")
	if err != nil {
		return false
	}
	proc, err := h.FindProc("AttachConsole")
	if err != nil {
		return false
	}
	// ATTACH_PARENT_PROCESS = (DWORD)-1,见 Microsoft Docs:
	// https://learn.microsoft.com/windows/console/attachconsole
	const attachParentProcess uintptr = 0xFFFFFFFF
	r, _, _ := proc.Call(attachParentProcess)
	return r != 0
}