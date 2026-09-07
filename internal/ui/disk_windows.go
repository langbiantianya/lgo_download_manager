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

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceExW  = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// platformDiskUsage 使用 Win32 GetDiskFreeSpaceExW 读取 path 所在卷的
// 可用字节与总字节。等价于 POSIX 的 Statfs → Bavail/Total 语义。
//
// 该函数对路径不存在的容忍与 POSIX 版一致：失败时返回 (0, 0, err)，
// 调用方 (main_window.go / settings_dialog.go) 已对 err 做兜底显示。
func platformDiskUsage(path string) (free, total int64, err error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, fmt.Errorf("diskUsage: invalid path: %w", err)
	}

	var freeBytesAvailable, totalBytes, totalFreeBytes uint64
	r, _, e := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)),
	)
	if r == 0 {
		return 0, 0, fmt.Errorf("GetDiskFreeSpaceExW(%s): %v", path, e)
	}
	_ = totalFreeBytes // 与 POSIX 行为对齐：返回给 root 用户视角的可用空间
	return int64(freeBytesAvailable), int64(totalBytes), nil
}