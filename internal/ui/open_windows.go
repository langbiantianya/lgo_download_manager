// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build windows

package ui

import (
	"fmt"
	"syscall"
	"unsafe"
)

// shell32 是 Windows ShellExecuteW 的宿主 DLL。延迟加载,无 GUI 进程
// 不会引入任何非必要的 Win32 依赖。
var shell32 = syscall.NewLazyDLL("shell32.dll")

var procShellExecuteW = shell32.NewProc("ShellExecuteW")

// SW_SHOWNORMAL 让打开的窗口按正常大小显示(不最小化、不最大化)。
const swShownormal = 1

// platformOpenFolder / platformOpenFile 都通过 ShellExecuteW + verb="open"
// 派发:Win32 自己根据路径类型选择处理程序——目录走 Explorer,文件走
// 默认关联应用。这是 2026 年仍受支持的现代 API(文档化的 ShellExecute
// 替代品是 ShellExecuteExW,但对「用默认应用打开」这种用例,ShellExecuteW
// 仍是惯用法;Office / 文件管理器 / 浏览器均稳定支持)。
//
// 返回值语义:>32 视为成功,<=32 是错误码;0/2/3/5/8/11/27/28/29/30/31
// 等都被映射到 error,方便日后调用方决定是否弹错(目前调用方吞掉)。
func shellOpen(target string) error {
	pathPtr, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return fmt.Errorf("shellOpen(%s): %w", target, err)
	}
	verbPtr, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return fmt.Errorf("shellOpen(%s): verb utf16: %w", target, err)
	}

	r, _, _ := procShellExecuteW.Call(
		0,                          // hwnd:无父窗口
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(pathPtr)),
		0, // lpParameters(命令行参数,文件打开场景不用)
		0, // lpDirectory(工作目录)
		uintptr(swShownormal),
	)
	// ShellExecuteW 返回值 > 32 表示实例句柄;<=32 是错误码。
	// 见 https://learn.microsoft.com/windows/win32/api/shellapi/nf-shellapi-shellexecutew
	if r <= 32 {
		return fmt.Errorf("ShellExecuteW(%s) failed: code %d", target, r)
	}
	return nil
}

func platformOpenFolder(dir string)  { _ = shellOpen(dir) }
func platformOpenFile(file string)  { _ = shellOpen(file) }