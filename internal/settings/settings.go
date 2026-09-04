// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package settings 提供业务进程对 store.Settings 的加载/保存封装：
// 首次运行默认值、--light 强制、以及下载引擎的进程级代理配置同步。
// 该包只依赖 store 与 protocol，不依赖 fyne —— 供业务主进程与
// UI 子进程（经 IPC 触发）共用。
package settings

import (
	"log"
	"os"
	"path/filepath"

	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/store"
)

const mib = int64(1 << 20)

// Load 从存储读取持久化设置，应用首次运行默认值与 forceLight 覆盖，
// 并把代理配置同步到 protocol 包。首次运行时自动保存默认值行。
func Load(st *store.Store, forceLight bool) (store.Settings, error) {
	persisted, err := st.LoadSettings()
	if err != nil {
		return store.Settings{}, err
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
		// LightMode 默认关闭：关闭主窗口只隐藏窗口，便于随时通过托盘恢复。
		// 用户可在「设置」里手动开启，开启后关闭主窗口会同时退出 UI 进程。
		persisted.LightMode = false
	}
	if forceLight {
		persisted.LightMode = true
	}
	ApplyProxy(persisted)
	if firstRun {
		if err := Save(st, persisted); err != nil {
			log.Printf("settings: save first-run defaults: %v", err)
		}
	}
	return persisted, nil
}

// Save 持久化设置并同步进程级代理配置。任何 UI 修改后均可安全调用。
// ProxyMode 为 System 时顺带清空系统代理缓存，使下一次探测生效。
func Save(st *store.Store, s store.Settings) error {
	ApplyProxy(s)
	if s.ProxyMode == protocol.ProxyModeSystem {
		protocol.InvalidateSystemProxyCache()
	}
	return st.SaveSettings(s)
}

// ApplyProxy 把设置写入 protocol 包级代理配置，使新创建/恢复的下载任务
// 立即生效。不访问磁盘。
func ApplyProxy(s store.Settings) {
	protocol.SetProxyConfig(protocol.ProxyConfig{
		Mode:        s.ProxyMode,
		ProxyURL:    s.ProxyURL,
		ProxyBypass: s.ProxyBypass,
	})
}
