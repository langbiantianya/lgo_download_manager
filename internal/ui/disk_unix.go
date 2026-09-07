// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build !windows

package ui

import "syscall"

// platformDiskUsage 使用 POSIX statfs(2) 读取 path 所在文件系统的
// 可用字节和总字节。语义保持原样：Bavail (非 root 可用) * Bsize。
func platformDiskUsage(path string) (free, total int64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	free = int64(stat.Bavail) * stat.Bsize
	total = int64(stat.Blocks) * stat.Bsize
	return free, total, nil
}