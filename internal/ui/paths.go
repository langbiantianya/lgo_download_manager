// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// archiveCompressionExts 列出常见的归档/压缩单段扩展名。
var archiveCompressionExts = []string{
	".gz", ".bz2", ".xz", ".zst", ".lz", ".lzma", ".lzo", ".br", ".sz", ".Z",
}

// uniqueSuffixRegex 匹配 'name(N)' 形式的后缀。
var uniqueSuffixRegex = regexp.MustCompile(`^(.*?)(\((\d+)\))$`)

// splitExt 拆分文件名后缀为 (stem, ext)，识别复合扩展名。
func splitExt(base string) (string, string) {
	lower := strings.ToLower(base)
	idx := strings.LastIndex(lower, ".")
	if idx <= 0 || idx == len(lower)-1 {
		return base[:len(base)-len(filepath.Ext(base))], filepath.Ext(base)
	}
	candidate := lower[idx:]
	for _, comp := range archiveCompressionExts {
		if candidate == comp {
			before := lower[:idx]
			if j := strings.LastIndex(before, "."); j > 0 {
				return base[:j], base[j:]
			}
		}
	}
	return base[:len(base)-len(filepath.Ext(base))], filepath.Ext(base)
}

// uniqueSavePath 若 path 指向的文件已存在，则在扩展名前插入 (N)。
func uniqueSavePath(path string) string {
	if _, err := os.Stat(path); err != nil {
		return path
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	stem, ext := splitExt(base)

	start := 1
	if m := uniqueSuffixRegex.FindStringSubmatch(stem); m != nil {
		if n, err := strconv.Atoi(m[3]); err == nil {
			start = n + 1
			stem = m[1]
		}
	}
	for n := start; n < 10000; n++ {
		candidate := filepath.Join(dir, stem+"("+strconv.Itoa(n)+")"+ext)
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
	}
	return path
}

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

// diskInfo 描述一个挂载点的磁盘占用。
type diskInfo struct {
	// Label 是给用户看的标识——Linux 上用 mount point，其他平台用 device 名。
	Label  string
	Free   int64
	Total  int64
	Used   int64
	Frac   float32
}

// listMounts 列出所有挂载点及其占用。
//
// Linux: 读 /proc/mounts，去重 + 跳过 pseudo FS（proc/sysfs/cgroup/tmpfs/devpts 等）。
// 其他平台: 返回单个 diskUsage(".")。
func listMounts() []diskInfo {
	return listMountsImpl()
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
