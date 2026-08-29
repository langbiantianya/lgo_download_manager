// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package prealloc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreallocate_10MB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")

	const size = 10 * 1024 * 1024
	f, err := Preallocate(path, size)
	if err != nil {
		t.Fatalf("Preallocate: %v", err)
	}
	defer f.Close()

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Size() != size {
		t.Fatalf("size mismatch: got %d, want %d", st.Size(), size)
	}

// 在偏移 0 处写入应该成功,且不应扩展文件。
	if _, err := f.WriteAt([]byte("hello"), 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	st2, _ := os.Stat(path)
	if st2.Size() != size {
		t.Fatalf("size after write changed: %d", st2.Size())
	}
}

func TestPreallocate_Zero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.bin")
	f, err := Preallocate(path, 0)
	if err != nil {
		t.Fatalf("Preallocate 0: %v", err)
	}
	defer f.Close()
}
