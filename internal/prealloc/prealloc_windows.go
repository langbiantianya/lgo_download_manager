// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build windows

package prealloc

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procSetFilePointerEx = kernel32.NewProc("SetFilePointerEx")
	procSetEndOfFile     = kernel32.NewProc("SetEndOfFile")
)

func platformPrealloc(f *os.File, size int64) error {
	// 将指针移动到 size,然后 SetEndOfFile 使用当前位置作为新的 EOF。
	var newPos int64
	r, _, e := procSetFilePointerEx.Call(
		uintptr(f.Fd()),
		uintptr(size),
		uintptr(unsafe.Pointer(&newPos)),
		uintptr(0), // FILE_BEGIN
	)
	if r == 0 {
		return fmt.Errorf("SetFilePointerEx: %v", e)
	}
	r, _, e = procSetEndOfFile.Call(uintptr(f.Fd()))
	if r == 0 {
		return fmt.Errorf("SetEndOfFile: %v", e)
	}
	// 不再把文件指针移回开头:SetEndOfFile 已按当前位置确定 EOF,而所有
	// 调用方(http/ftp/webdav 的 chunk 写入)都使用 WriteAt,文件位置
	// 对它们没有意义——这里省掉一次多余的 syscall。
	return nil
}
