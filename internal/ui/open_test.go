// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenFile_RealPath_NoPanic 在有真实存在的文件上调用 openFile 不
// 崩溃。在 Windows 上跑 ShellExecuteW;在 Linux 上 exec xdg-open / open
// —— 没有桌面环境时 exec 找不到二进制,Run 返回 *exec.Error,被吞掉。
// 真正验证的是「按钮回调里调 openFile 不会让 UI 进程崩」。
func TestOpenFile_RealPath_NoPanic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "smoke.bin")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	openFile(p) // 不应 panic
}

// TestOpenFolder_RealPath_NoPanic 同上,真实目录。
func TestOpenFolder_RealPath_NoPanic(t *testing.T) {
	dir := t.TempDir()
	openFolder(dir)
}

// TestOpenFile_MissingPath_NoPanic 验证路径不存在时也不 panic:
// Windows:ShellExecuteW 返回 ERROR_FILE_NOT_FOUND(2),我们吞掉;
// POSIX:exec xdg-open 找不到文件,Run 返回 err,被吞掉。
func TestOpenFile_MissingPath_NoPanic(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "definitely-not-here.bin")
	openFile(missing) // 不应 panic,也不应阻塞测试太久。
}