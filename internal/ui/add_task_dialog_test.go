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

func TestUniqueSavePath(t *testing.T) {
	dir := t.TempDir()

	// 文件不存在——返回原路径。
	got := uniqueSavePath(filepath.Join(dir, "download.bin"))
	if got != filepath.Join(dir, "download.bin") {
		t.Fatalf("non-existent should return as-is: %q", got)
	}

	// 创建一个同名文件，再调用应当返回 download(1).bin。
	if err := os.WriteFile(filepath.Join(dir, "download.bin"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got = uniqueSavePath(filepath.Join(dir, "download.bin"))
	want := filepath.Join(dir, "download(1).bin")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	// 如果 download.bin 与 download(1).bin 都已存在，应返回 download(2).bin。
	if err := os.WriteFile(filepath.Join(dir, "download(1).bin"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got = uniqueSavePath(filepath.Join(dir, "download.bin"))
	want = filepath.Join(dir, "download(2).bin")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	// 如果传入的路径已经是 (1) 形式，应跳过 (1)，从 (2) 开始。
	got = uniqueSavePath(filepath.Join(dir, "download(1).bin"))
	want = filepath.Join(dir, "download(2).bin")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	// 没有扩展名的文件也支持。
	if err := os.WriteFile(filepath.Join(dir, "noext"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got = uniqueSavePath(filepath.Join(dir, "noext"))
	want = filepath.Join(dir, "noext(1)")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}