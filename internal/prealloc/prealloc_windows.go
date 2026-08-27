//go:build windows

package prealloc

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procSetFilePointerEx  = kernel32.NewProc("SetFilePointerEx")
	procSetEndOfFile      = kernel32.NewProc("SetEndOfFile")
)

func platformPrealloc(f *os.File, size int64) error {
	h := f.Fd()
	var newPos int64
	// Move pointer to size, then SetEndOfFile uses the current position as new EOF.
	r, _, e := procSetFilePointerEx.Call(
		h,
		uintptr(size),
		uintptr(unsafe.Pointer(&newPos)),
		uintptr(0), // FILE_BEGIN
	)
	if r == 0 {
		return fmt.Errorf("SetFilePointerEx: %v", e)
	}
	r, _, e = procSetEndOfFile.Call(h)
	if r == 0 {
		return fmt.Errorf("SetEndOfFile: %v", e)
	}
	// Reset pointer to beginning for the caller.
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	return nil
}
