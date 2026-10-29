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
	"fmt"
	"net"

	"lgo_download_manager/internal/autostart"
	"lgo_download_manager/internal/downloaddir"
	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/logging"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/store"
)

const mib = int64(1 << 20)

// pickBTPort 可在测试中替换,避免依赖沙箱是否允许监听回环端口。
var pickBTPort = pickAvailableBTPort

// Load 从存储读取持久化设置，应用首次运行默认值与 forceLight 覆盖，
// 并把代理配置同步到 protocol 包。首次运行时自动保存默认值行。
//
// 「首次运行」的判定依据是「用户有没有保存过设置」(store 的 initialized
// 标记),而不是「下载目录是否为空」——后者会把「目录被清空」这种
// 单字段异常误判成首次运行,把线程数/分块/UA/BT 端口等一整套设置
// 全部重置成默认值。
func Load(st *store.Store, forceLight bool) (store.Settings, error) {
	persisted, saved, err := st.LoadSettingsEx()
	if err != nil {
		return store.Settings{}, err
	}
	firstRun := !saved
	if firstRun {
		persisted.DefaultSaveDir = downloaddir.Default()
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
		// BitTorrent 默认值:首次启动选一个当前可用的监听端口并持久化,
		// 之后每次启动沿用;不限速、DHT/PEX 开启(磁力链接没有 tracker
		// 时几乎只能靠它们找 peer)、分享率 2、同时做种 2 条、BT 同时下载 2 个。
		persisted.BTPort = pickBTPort()
		persisted.BTMaxPeers = store.DefaultBTMaxPeers
		persisted.BTDownloadLimitKiB = 0
		persisted.BTUploadLimitKiB = 0
		persisted.BTDHT = true
		persisted.BTPEX = true
		persisted.BTSeedRatio = store.DefaultBTSeedRatio
		persisted.BTMaxSeeding = store.DefaultBTMaxSeeding
		persisted.BTMaxConcurrent = store.DefaultBTMaxConcurrent
		persisted.BTTrackers = ""
	}
	if forceLight {
		persisted.LightMode = true
	}
	// BT 专用并发/做种上限 <=0 视为未配置,统一落到默认值;这样 UI 和
	// 持久化都不会显示/保存 0 这种需要 scheduler 再兜底的值。
	if persisted.BTMaxSeeding <= 0 {
		persisted.BTMaxSeeding = store.DefaultBTMaxSeeding
	}
	if persisted.BTMaxConcurrent <= 0 {
		persisted.BTMaxConcurrent = store.DefaultBTMaxConcurrent
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
	// 非首次运行、但下载目录为空时的兜底:只补这一个字段,其余设置是
	// 用户自己配的,一个字都不能动。旧版本把「目录为空」当成首次运行,
	// 一旦目录被清空就会把整套设置重置——这里只做最小修复。
	dirFallback := false
	if persisted.DefaultSaveDir == "" {
		persisted.DefaultSaveDir = downloaddir.Default()
		dirFallback = true
		logging.Printf("settings: save dir was empty, fallback to %q", persisted.DefaultSaveDir)
	}
	ApplyProxy(persisted)
	if firstRun {
		if err := Save(st, persisted); err != nil {
			logging.Printf("settings: save first-run defaults: %v", err)
		}
	} else if dirFallback {
		// 只回写这一个修复过的字段,用 store 直写避免触发 ApplyAutoStart
		// 等与本次修复无关的副作用。
		if err := st.SaveSettings(persisted); err != nil {
			logging.Printf("settings: persist save dir fallback: %v", err)
		}
	}
	return persisted, nil
}

// pickAvailableBTPort 返回一个 TCP 与 UDP 都能绑定的端口。BT 客户端会
// 同时用这两种协议监听,只检查 TCP 可能在真正启动时因 UDP 占用而失败。
// 返回 0 表示当前环境无法探测端口,调用方沿用旧语义交给系统随机分配。
func pickAvailableBTPort() int {
	for attempt := 0; attempt < 8; attempt++ {
		tcpLn, err := net.Listen("tcp", ":0")
		if err != nil {
			return 0
		}
		addr, ok := tcpLn.Addr().(*net.TCPAddr)
		if !ok {
			tcpLn.Close()
			return 0
		}
		udpAddr := fmt.Sprintf(":%d", addr.Port)
		udpLn, err := net.ListenPacket("udp", udpAddr)
		if err == nil {
			udpLn.Close()
			tcpLn.Close()
			return addr.Port
		}
		tcpLn.Close()
	}
	return 0
}

// Save 持久化设置并同步进程级代理配置与开机自启。任何 UI 修改后均可安全调用。
// ProxyMode 为 System 时顺带清空系统代理缓存，使下一次探测生效；
// 开机自启开关变化时调用 ApplyAutoStart 把磁盘/注册表/LaunchAgent 实际状态
// 调到与 s.AutoStart 一致。
func Save(st *store.Store, s store.Settings) error {
	ApplyProxy(s)
	if s.ProxyMode == protocol.ProxyModeSystem {
		protocol.InvalidateProxyCache()
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
		Mode:               s.ProxyMode,
		ProxyURL:           s.ProxyURL,
		ProxyBypass:        s.ProxyBypass,
		ProxyAutoConfigURL: s.ProxyAutoConfigURL,
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
