// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"fmt"
	"os"
	"path/filepath"

	"lgo_download_manager/internal/store"
)

const mib = int64(1 << 20)

// GlobalSettings 是“新建任务”预填字段时以及 main.go 中的 URL 启动器
// 使用的实时设置。
var GlobalSettings store.Settings

// LoadSettings 从存储中读取持久化的设置，并将其覆盖到 GlobalSettings 上。
func LoadSettings(st *store.Store) error {
	persisted, err := st.LoadSettings()
	if err != nil {
		return err
	}
	firstRun := persisted.DefaultSaveDir == ""
	if firstRun {
		home, _ := os.UserHomeDir()
		persisted.DefaultSaveDir = filepath.Join(home, "Downloads")
		persisted.DefaultThreads = 4
		persisted.MinChunkSize = 10 * mib
		persisted.UserAgent = "Wget/1.21.3"
		persisted.FTPPassive = true
		persisted.Prealloc = true
		persisted.TaskSort = store.SortCreatedDesc
	}
	GlobalSettings = persisted
	if firstRun {
		_ = SaveSettings(st)
	}
	return nil
}

// SaveSettings 将 GlobalSettings 写入存储。
func SaveSettings(st *store.Store) error {
	return st.SaveSettings(GlobalSettings)
}

// diskSpaceAt 返回 path 所在文件系统的剩余可用字节数（人类可读）。
func diskSpaceAt(path string) string {
	if path == "" {
		return "未设置"
	}
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	free, total, err := diskUsage(dir)
	if err != nil {
		return fmt.Sprintf("不可用 (%v)", err)
	}
	return fmt.Sprintf("可用 %s / 总计 %s", humanBytes(free), humanBytes(total))
}
