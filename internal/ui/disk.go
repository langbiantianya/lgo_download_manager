// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import "syscall"

// diskUsage 返回 path 所在文件系统的可用字节数和总字节数。
func diskUsage(path string) (free, total int64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	free = int64(stat.Bavail) * stat.Bsize
	total = int64(stat.Blocks) * stat.Bsize
	return free, total, nil
}
