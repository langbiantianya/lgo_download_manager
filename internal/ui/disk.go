// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

// diskUsage 返回 path 所在文件系统的可用字节数和总字节数。
//
// Linux/POSIX: syscall.Statfs(2) → Bavail/Blocks * Bsize.
// Windows    : GetDiskFreeSpaceExW → FreeBytesAvailable / TotalNumberOfBytes.
func diskUsage(path string) (free, total int64, err error) {
	return platformDiskUsage(path)
}