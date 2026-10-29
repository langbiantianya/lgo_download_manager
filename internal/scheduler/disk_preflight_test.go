// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package scheduler

import (
	"errors"
	"math"
	"path/filepath"
	"testing"

	"lgo_download_manager/internal/protocol"
)

func TestCheckAvailableDiskSpace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "nested", "file.bin")
	if err := checkAvailableDiskSpace(path, 1); err != nil {
		t.Fatalf("small required space: %v", err)
	}
	err := checkAvailableDiskSpace(path, math.MaxInt64)
	if !errors.Is(err, protocol.ErrInsufficientDiskSpace) {
		t.Fatalf("huge required space err=%v, want ErrInsufficientDiskSpace", err)
	}
}
