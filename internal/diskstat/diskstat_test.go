// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package diskstat

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUsageReadsCurrentFilesystem 校验能读到当前文件系统的大小：总容量
// 必须为正，且可用空间不应超过总容量。
//
// 这条用例在每个平台上跑，跨平台编译问题（Statfs_t.Bsize 在 darwin 是
// uint32）由 CI 的交叉编译检查兜住，这里守的是数值语义。
func TestUsageReadsCurrentFilesystem(t *testing.T) {
	dir := t.TempDir()
	free, total, err := Usage(dir)
	if err != nil {
		t.Fatalf("Usage(%s): %v", dir, err)
	}
	if total <= 0 {
		t.Errorf("total = %d, want > 0", total)
	}
	if free < 0 {
		t.Errorf("free = %d, want >= 0", free)
	}
	if free > total {
		t.Errorf("free = %d > total = %d", free, total)
	}
}

// TestUsageOnMissingPath 校验路径不存在时返回错误而不是静默的 0——
// 调用方据此显示「不可用」。
func TestUsageOnMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	if _, _, err := Usage(missing); err == nil {
		t.Error("Usage on a missing path should fail")
	}
}

// TestUsageOnExistingFile 校验路径是文件（不是目录）时也能读到——
// 状态栏传的是下载目录，但设置页可能传文件路径。
func TestUsageOnExistingFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "probe.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, total, err := Usage(f); err != nil {
		t.Errorf("Usage(%s): %v", f, err)
	} else if total <= 0 {
		t.Errorf("total = %d, want > 0", total)
	}
}
