//go:build darwin

package prealloc

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func platformPrealloc(f *os.File, size int64) error {
	const _F_PREALLOCATE = 42
	const _F_ALLOCATECONTIG = 2

	// struct fstore from <sys/fcntl.h>
	// Mirrors xnu: uint32_t flags, uint32_t posmode, off_t offset, off_t length,
	//             off_t bytesdone, uint32_t mode
	type fstoreT struct {
		Flags     uint32
		Posmode   uint32
		Offset    int64
		Length    int64
		Bytesdone int64
		Mode      uint32
	}
	fs := fstoreT{
		Flags:   _F_ALLOCATECONTIG,
		Posmode: 0, // F_SETSIZE
		Offset:  0,
		Length:  size,
	}
	_, _, errno := syscall.Syscall(
		syscall.SYS_FCNTL,
		f.Fd(),
		uintptr(_F_PREALLOCATE),
		uintptr(unsafe.Pointer(&fs)),
	)
	if errno != 0 {
		return fmt.Errorf("fcntl F_PREALLOCATE: %v", errno)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	return nil
}
