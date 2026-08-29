// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build !linux && !darwin && !windows

package prealloc

import (
	"fmt"
	"os"
)

func platformPrealloc(f *os.File, size int64) error {
// 没有精心实现的预分配调用的通用 unix:强制调用者进入
// truncate+probe-write 回退。
	return fmt.Errorf("no native prealloc on this unix; using fallback")
}
