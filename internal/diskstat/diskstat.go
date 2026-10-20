// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package diskstat 读取某个路径所在文件系统的可用空间与总容量。
//
// 单独成包有两个原因：
//
//  1. 平台实现只依赖 syscall，不碰 cgo，因此可以被交叉编译检查
//     （GOOS=darwin/windows go build ./internal/diskstat/）。UI 包因为
//     依赖 Fyne 的 OpenGL 绑定必须开 cgo，没法这样检查——而本包曾经
//     在 macOS 上编译不过（Statfs_t.Bsize 在 darwin 是 uint32、在
//     linux 是 int64），正是缺这道交叉编译检查才漏到发布前的。
//  2. 平台差异（Statfs vs GetDiskFreeSpaceExW）与 UI 无关，
//     放在 UI 包里只会让窗口代码掺进系统调用的细节。
package diskstat

// Usage 返回 path 所在文件系统的可用字节数与总字节数。
//
// Linux/POSIX: syscall.Statfs(2) → Bavail/Blocks * Bsize.
// Windows    : GetDiskFreeSpaceExW → FreeBytesAvailable / TotalNumberOfBytes.
//
// path 不存在时返回错误；调用方负责展示「不可用」。
func Usage(path string) (free, total int64, err error) {
	return platformUsage(path)
}
