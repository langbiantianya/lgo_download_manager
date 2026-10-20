// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build !windows

package diskstat

import "syscall"

// platformUsage 使用 POSIX statfs(2) 读取 path 所在文件系统的
// 可用字节和总字节。语义保持原样：Bavail (非 root 可用) * Bsize。
//
// Bsize 的类型随平台而变（linux 是 int64，darwin 是 uint32），
// 因此先转成 int64 再相乘——直接相乘在 macOS 上编译不过。
func platformUsage(path string) (free, total int64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	bsize := int64(stat.Bsize)
	free = int64(stat.Bavail) * bsize
	total = int64(stat.Blocks) * bsize
	return free, total, nil
}
