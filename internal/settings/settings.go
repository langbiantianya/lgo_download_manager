// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package settings 提供业务进程对 store.Settings 的加载/保存封装：
// 首次运行默认值、--light 强制、下载引擎的进程级代理配置同步，
// 以及「开机自启」开关位与操作系统登录启动项的真实注册/撤销。
// 该包只依赖 store、protocol、autostart —— 不依赖 fyne ——
// 供业务主进程与 UI 子进程（经 IPC 触发）共用。
package settings

import (
	"os"
	"path/filepath"

	"lgo_download_manager/internal/autostart"
	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/logging"
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
		// LightMode 默认开启:点击主窗口 X 直接退出 UI 子进程,
		// 行为符合大多数用户对窗口关闭按钮的直觉。需要「关闭即隐藏、
		// 托盘随时恢复」的用户可以在「设置」里手动取消。
		persisted.LightMode = true
		// 首次运行:同时下载任务数默认 3,与主流下载器轻量场景对齐。
		persisted.MaxConcurrent = store.DefaultMaxConcurrent
	}
	if forceLight {
		persisted.LightMode = true
	}
	// Language 兜底:
	//   - 持久化值非空 → Normalize 折叠一次,无效标签 → 默认 zh-Hans;
	//   - 持久化值为空 → 用 OS 语言作为偏好(走 jeandeaual/go-locale);
	//   - OS 语言读不到 / 不在 Supported → 落到默认 zh-Hans。
	// Normalize 还会顺手把过期/未知标签替换成默认值,旧用户升级后不会
	// 因 db 里残留的非法值而崩溃。首次运行也会在这里赋值并落盘,见下方
	// firstRun 分支的 Save。
	if persisted.Language == "" {
		persisted.Language = ilocale.SystemLanguage()
	} else {
		persisted.Language = ilocale.Normalize(persisted.Language)
	}
	ApplyProxy(persisted)
	if firstRun {
		if err := Save(st, persisted); err != nil {
			logging.Printf("settings: save first-run defaults: %v", err)
		}
	}
	return persisted, nil
}

// Save 持久化设置并同步进程级代理配置与开机自启。任何 UI 修改后均可安全调用。
// ProxyMode 为 System 时顺带清空系统代理缓存，使下一次探测生效；
// 开机自启开关变化时调用 ApplyAutoStart 把磁盘/注册表/LaunchAgent 实际状态
// 调到与 s.AutoStart 一致。
func Save(st *store.Store, s store.Settings) error {
	ApplyProxy(s)
	if s.ProxyMode == protocol.ProxyModeSystem {
		protocol.InvalidateSystemProxyCache()
	}
	if err := ApplyAutoStart(s.AutoStart); err != nil {
		// 真实注册失败只记日志:避免 UI 提交整笔失败、
		// 同时给用户后续排障线索(例:HKCU 权限不足)。
		logging.Printf("settings: apply autostart: %v", err)
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

// ApplyAutoStart 把操作系统的登录启动项调到与 want 一致:
//
//	want=true   → 调用 autostart.Enable()(注册表/.desktop/LaunchAgent)
//	want=false  → 调用 autostart.Disable() 撤销注册
//
// 任何一步失败都向上抛错误;由 Save/Load 在调用点决定是否只记日志。
// 不修改 store.Settings —— AutoStart 字段属于「开关位」,
// 是否真的注册由操作系统侧决定,两者由 Save/Load 时调和。
func ApplyAutoStart(want bool) error {
	if want {
		return autostart.Enable()
	}
	return autostart.Disable()
}

// ReconcileAutoStart 启动时调用:把持久化的 AutoStart 开关与操作系统
// 真实状态对齐。当磁盘/注册表/LaunchAgent 与 want 不一致时(用户用
// 第三方工具改过、卸载/重装后残留),以 want 为准重新注册/撤销。
// 查询/写入失败只记日志,不阻塞启动 —— 托盘/调度器与开机自启解耦。
func ReconcileAutoStart(want bool) {
	have, err := autostart.IsEnabled()
	if err != nil {
		logging.Printf("settings: query autostart state: %v", err)
		return
	}
	if have == want {
		return
	}
	if err := ApplyAutoStart(want); err != nil {
		logging.Printf("settings: reconcile autostart to %v: %v", want, err)
	}
}
