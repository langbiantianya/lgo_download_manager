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
func TestUniqueSavePathCompoundExt(t *testing.T) {
	dir := t.TempDir()

	// archive.tar.gz 已存在 → archive(1).tar.gz（不要把 (1) 插在 .tar 后面）。
	if err := os.WriteFile(filepath.Join(dir, "archive.tar.gz"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := uniqueSavePath(filepath.Join(dir, "archive.tar.gz"))
	want := filepath.Join(dir, "archive(1).tar.gz")
	if got != want {
		t.Fatalf("tar.gz: got %q want %q", got, want)
	}

	// data.tar.bz2 已存在 → data(1).tar.bz2。
	if err := os.WriteFile(filepath.Join(dir, "data.tar.bz2"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got = uniqueSavePath(filepath.Join(dir, "data.tar.bz2"))
	want = filepath.Join(dir, "data(1).tar.bz2")
	if got != want {
		t.Fatalf("tar.bz2: got %q want %q", got, want)
	}

	// 同时存在 archive.tar.gz 与 archive(1).tar.gz，应继续递增到 (2)。
	if err := os.WriteFile(filepath.Join(dir, "archive(1).tar.gz"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got = uniqueSavePath(filepath.Join(dir, "archive.tar.gz"))
	want = filepath.Join(dir, "archive(2).tar.gz")
	if got != want {
		t.Fatalf("tar.gz chained: got %q want %q", got, want)
	}
}

func TestSplitExt(t *testing.T) {
	cases := []struct {
		base     string
		wantStem string
		wantExt  string
	}{
		{"archive.tar.gz", "archive", ".tar.gz"},
		{"archive.tar.xz", "archive", ".tar.xz"},
		{"file.bin", "file", ".bin"},
		{"noext", "noext", ""},
		{"multi.dotted.name.txt", "multi.dotted.name", ".txt"},
		{"UPPER.TAR.GZ", "UPPER", ".TAR.GZ"}, // splitExt 大小写不敏感匹配，但保留原大小写
	}
	for _, c := range cases {
		stem, ext := splitExt(c.base)
		if stem != c.wantStem || ext != c.wantExt {
			t.Errorf("splitExt(%q) = (%q, %q), want (%q, %q)",
				c.base, stem, ext, c.wantStem, c.wantExt)
		}
	}
}

// TestSplitExtForwardCompat 验证新增的压缩格式只需往 archiveCompressionExts 追加
// 一行就能立即支持新的复合扩展名——例如 .whl.gz、.crate.zst、.pkg.br。
func TestSplitExtForwardCompat(t *testing.T) {
	cases := []struct {
		base     string
		wantStem string
		wantExt  string
	}{
		// 任何"<anything>.<knownCompression>"形式都能被识别为复合扩展名。
		{"python-3.12.tar.gz", "python-3.12", ".tar.gz"},
		{"snapshot.img.zst", "snapshot", ".img.zst"},
		{"libfoo.crate.br", "libfoo", ".crate.br"},
		{"backup.cpio.xz", "backup", ".cpio.xz"},
		// 已知压缩格式单独出现时不应误判。
		{"archive.gz", "archive", ".gz"},
		// 非压缩扩展名（如 .txt）应保持普通单段扩展名行为。
		{"notes.tar.txt", "notes.tar", ".txt"},
	}
	for _, c := range cases {
		stem, ext := splitExt(c.base)
		if stem != c.wantStem || ext != c.wantExt {
			t.Errorf("splitExt(%q) = (%q, %q), want (%q, %q)",
				c.base, stem, ext, c.wantStem, c.wantExt)
		}
	}
}
