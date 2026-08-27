//go:build linux

package prealloc

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func platformPrealloc(f *os.File, size int64) error {
	if err := unix.Fallocate(int(f.Fd()), 0, 0, size); err != nil {
		return fmt.Errorf("fallocate: %w", err)
	}
	return nil
}
