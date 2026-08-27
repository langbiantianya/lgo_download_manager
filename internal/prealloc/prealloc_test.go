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

	// Writing at offset 0 should succeed and not extend the file.
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
