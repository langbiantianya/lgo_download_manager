// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package scheduler

import (
	"fmt"
	"os"
	"path/filepath"

	"lgo_download_manager/internal/diskstat"
	"lgo_download_manager/internal/protocol"
)

// checkAvailableDiskSpace 在创建或预分配目标文件前检查最近一个已存在
// 的父目录。下载目录尚未创建是合法场景，不能因此跳过空间预检。
func checkAvailableDiskSpace(path string, required int64) error {
	if required <= 0 {
		return nil
	}
	dir := filepath.Dir(path)
	for {
		if info, err := os.Stat(dir); err == nil {
			if !info.IsDir() {
				return fmt.Errorf("disk preflight: parent is not a directory: %s", dir)
			}
			break
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("disk preflight: inspect %s: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	free, _, err := diskstat.Usage(dir)
	if err != nil {
		return fmt.Errorf("disk preflight: %w", err)
	}
	if free < required {
		return fmt.Errorf("%w: need %d bytes, %d available in %s",
			protocol.ErrInsufficientDiskSpace, required, free, dir)
	}
	return nil
}
