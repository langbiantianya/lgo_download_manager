// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package prealloc 提供跨平台的下载文件磁盘预分配。
//
// 目标是在任何下载工作开始之前,在目标文件系统上预留连续的物理空间,以便:
//   - 避免在下载中途才发现磁盘空间不足。
//   - 生成的文件是连续的(在 HDD 上减少碎片)。
//
// 各平台实现:
//   - linux  : unix.Fallocate(mode=0) 高效地预留空间。
//   - windows: syscall.SetEndOfFile 扩展 sparse file。
//   - darwin : fcntl(F_PREALLOCATE) + fstore 使用 F_ALLOCATECONTIG。
//
// 如果平台特定的调用失败(某些文件系统不支持),则回退到 f.Truncate(size)
// 加上在文件末尾写入 1 字节,以强制文件系统实际预留字节(否则在 sparse FS
// 上 truncate 将是一个 no-op)。
package prealloc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Preallocate 为 path 处的文件预留 `size` 字节的物理磁盘空间。
// 它会打开(如果不存在则创建)该文件,尝试平台原生的预分配,
// 如果原生调用失败,则回退到 Truncate+SeekWrite。返回的句柄保持打开状态
// 供调用者使用;调用者负责 Close。
func Preallocate(path string, size int64) (*os.File, error) {
	if size < 0 {
		return nil, errors.New("prealloc: negative size")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("prealloc mkdir: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("prealloc open: %w", err)
	}
	if size == 0 {
		return f, nil
	}
	if err := platformPrealloc(f, size); err != nil {
// 回退:尽力而为。至少执行 truncate+seek-write 以强制 OS 预留字节。
		if err2 := truncateFallback(f, size); err2 != nil {
			f.Close()
			return nil, fmt.Errorf("prealloc native=%v fallback=%w", err, err2)
		}
	}
	return f, nil
}

// truncateFallback 通过截断然后在 size-1 偏移处写入单个字节,强制内核预留
// `size` 字节。在 sparse filesystem(支持 sparse 特性的 ext4、NTFS、APFS)
// 上,这仍然会使文件大部分保持 sparse,但 inode 现在反映了总长度,任何后续
// 的 WriteAt 都会按需分配。
func truncateFallback(f *os.File, size int64) error {
	if err := f.Truncate(size); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}
	if size == 0 {
		return nil
	}
	// 触及最后一个字节以实际提交 extent。这比写入整个文件便宜得多,
	// 并确保 FS 知道它。
	if _, err := f.WriteAt([]byte{0}, size-1); err != nil {
		return fmt.Errorf("probe-write: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("reseek: %w", err)
	}
	return nil
}
