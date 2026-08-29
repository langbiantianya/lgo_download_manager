// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

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

// 来自 <sys/fcntl.h> 的 struct fstore
// 对应 xnu: uint32_t flags, uint32_t posmode, off_t offset, off_t length,
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
