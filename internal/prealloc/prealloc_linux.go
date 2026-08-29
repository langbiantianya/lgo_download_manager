// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

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
