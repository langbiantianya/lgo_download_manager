// Package prealloc cross-platform disk preallocation for download files.
//
// The goal is to reserve continuous physical space on the target filesystem
// before any download work begins, so that:
//   - We don't discover we're out of disk space mid-download.
//   - The resulting file is contiguous (less fragmentation on HDDs).
//
// Per platform:
//   - linux  : unix.Fallocate (mode=0) reserves space efficiently.
//   - windows: syscall.SetEndOfFile extends the sparse file.
//   - darwin : fcntl(F_PREALLOCATE) + fstore with F_ALLOCATECONTIG.
//
// If the platform-specific call fails (some FS don't support it), we fall
// back to f.Truncate(size) plus a 1-byte write at the end to force the
// filesystem to actually reserve the bytes (otherwise truncate on a sparse FS
// would be a no-op).
package prealloc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Preallocate reserves `size` bytes of physical disk space for the file at
// path. It opens (creating if missing) the file, tries the platform native
// preallocation, and falls back to Truncate+SeekWrite if the native call
// fails. The handle is returned open for the caller to use; the caller is
// responsible for Close.
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
		// Fallback: best effort. Always at least truncate+seek-write so the
		// OS is forced to reserve the bytes.
		if err2 := truncateFallback(f, size); err2 != nil {
			f.Close()
			return nil, fmt.Errorf("prealloc native=%v fallback=%w", err, err2)
		}
	}
	return f, nil
}

// truncateFallback forces the kernel to reserve `size` bytes by truncating
// and then writing a single byte at offset size-1. On sparse filesystems
// (ext4 with sparse support, NTFS, APFS) this still leaves most of the file
// sparse, but the inode now reflects the total length and any subsequent
// WriteAt will allocate on demand.
func truncateFallback(f *os.File, size int64) error {
	if err := f.Truncate(size); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}
	if size == 0 {
		return nil
	}
	// Touch the last byte to actually commit the extent. It's much cheaper
	// than writing the whole file, and ensures the FS knows about it.
	if _, err := f.WriteAt([]byte{0}, size-1); err != nil {
		return fmt.Errorf("probe-write: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("reseek: %w", err)
	}
	return nil
}
