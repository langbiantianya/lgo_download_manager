// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package tray 提供独立于 Fyne 的系统托盘（fyne.io/systray）。
// 托盘运行在业务主进程中：UI 子进程关闭/崩溃不影响托盘，
// 菜单可随时重新拉起 UI。
package tray

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"fyne.io/systray"

	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/logging"
)

// 嵌入托盘图标。各平台格式由 fyne.io/systray 的接口契约决定：
//   - Windows：必须是 .ico（Win32 LoadImageW + IMAGE_ICON 才能解码）。
//     当前文件含 16/32/48 三档 RGBA 图，任务栏会按系统 DPI 自动挑尺寸。
//   - macOS / Linux：PNG 即可。
//
//go:embed assets/tray-22.png
var trayIcon22 []byte

//go:embed assets/tray-32.png
var trayIcon32 []byte

//go:embed assets/tray.png
var trayIcon64 []byte

//go:embed assets/tray.ico
var trayIconICO []byte

// Callbacks 描述托盘菜单触发的回调。
//
// 全部回调由调用方保证线程安全：systray 自身运行在自己的事件循环中，
// 回调通常在非主 goroutine 上触发。Open/Quit 均回到业务主进程：
// Open 拉起/显示 UI 子进程；Quit 结束整个程序（含 UI 子进程）。
type Callbacks struct {
	// Open 在用户点击 “Open” 菜单项时调用。
	Open func()

	// Quit 在用户点击 “退出” 菜单项时调用。
	// 调用方负责结束业务进程（并顺带回收 UI 子进程）。
	Quit func()
}

// 持久化对菜单项的引用,以便 Reload 在运行中切语言时重建菜单/标题。
// systray 本身没有「刷新菜单文案」接口——只能 ResetMenu 后重新添加;
// 同一时刻只允许一个 systray 事件循环,所以这里不需要锁。
var (
	trayReloadMu  sync.Mutex
	trayCallbacks Callbacks
	trayReady     = make(chan struct{}) // onReady 完成时关闭,Reload 在 Start 前会阻塞
	trayQuit      func()                // Quit 回调入口,菜单重建后必须重新连上
)

// Start 在当前 goroutine 上启动 systray 事件循环。
//
// 它会一直阻塞直到 systray.Quit() 被调用——因此必须在主流程中
// 以独立 goroutine 启动。
func Start(cb Callbacks) {
	trayReloadMu.Lock()
	trayCallbacks = cb
	trayReloadMu.Unlock()

	onReady := func() {
		if err := setPlatformIcon(); err != nil {
			logging.Printf("tray: set icon: %v", err)
		}
		// 模板图标只在非 Windows 使用:Windows 上 SetTemplateIcon 会退化成
		// SetIcon(把 PNG 字节写到无扩展名临时文件),从而覆盖 setPlatformIcon
		// 专门用 SetIconFromFilePath 加载的 .ico。
		if runtime.GOOS != "windows" {
			systray.SetTemplateIcon(trayIcon32, trayIcon32)
		}
		buildMenu()
		close(trayReady)
	}

	onExit := func() {
		logging.Println("tray: exited")
	}

	systray.Run(onReady, onExit)
	// 事件循环结束后图标已无用处:即便调用方没走 Stop() 也不留临时文件。
	removeIconFile()
}

// buildMenu 在 systray 事件循环里执行:设置标题并添加菜单项。
// 被 onReady 与 Reload 共用——后者先 ResetMenu 再调一次以重建。
//
// systray 跨平台暴露的接口有限:macOS 上 SetTitle 是菜单栏标题;
// Windows/Linux 上图标旁的 tooltip 由 systray.SetTooltip(若有)设置,
// 当前版本包级没有 SetTooltip,只有 MenuItem.SetTooltip——所以菜单项
// 自带 hover tooltip,托盘图标本身的 hover 提示就放弃做翻译(英文原文
// 已经能传达"下载管理器"语义,跨平台行为本来就不一致)。
func buildMenu() {
	systray.SetTitle(ilocale.T("main.window.title"))

	trayReloadMu.Lock()
	cb := trayCallbacks
	trayReloadMu.Unlock()

	mOpen := systray.AddMenuItem(ilocale.T("tray.menu.open"), ilocale.T("tray.menu.open.tooltip"))
	systray.AddSeparator()
	mQuit := systray.AddMenuItem(ilocale.T("tray.menu.quit"), ilocale.T("tray.menu.quit.tooltip"))

	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				if cb.Open != nil {
					cb.Open()
				}
			case <-mQuit.ClickedCh:
				if cb.Quit != nil {
					cb.Quit()
				}
				return
			}
		}
	}()
}

// Reload 在语言变更后重建托盘菜单/标题。
//
// systray 没有「刷新现有菜单项文案」的接口;只能 ResetMenu 后重新添加。
// 调用方应该在自己的事件循环/业务 goroutine 上调用——systray 内部
// 同步执行,不会跨线程撕裂菜单状态;但调用前必须等 Start 的 onReady
// 跑完(否则菜单尚未初始化)。这里用 trayReady 信号同步。
func Reload() {
	trayReloadMu.Lock()
	ready := trayReady
	trayReloadMu.Unlock()

	select {
	case <-ready:
	default:
		// 托盘尚未就绪:首启窗口还未出现,菜单稍后由 onReady 用当前语言
		// 一次建好;这里跳过避免 ResetMenu 在未就绪状态下失败。
		return
	}
	systray.ResetMenu()
	buildMenu()
}

// Stop 通知 systray 退出事件循环。可以在主程序结束时调用以释放托盘。
func Stop() {
	systray.Quit()
	// 托盘图标已由 LoadImageW 加载进内存,临时 .ico 文件可以立即删除
	// (进程可能不会等到 systray.Run 返回就退出)。
	removeIconFile()
}

// iconFile 记录 Windows 下落盘的托盘图标临时文件路径;由 iconFileMu 保护。
var (
	iconFileMu sync.Mutex
	iconFile   string
)

// setPlatformIcon 按平台设置托盘图标。
//
// Windows 上 fyne.io/systray 会把传入的字节写入无扩展名的临时文件再交
// 给 Win32 LoadImageW;为了让 LoadImageW 可靠嗅探出图标资源,我们直接
// 走 SetIconFromFilePath,自行落盘一个 .ico 临时文件。
func setPlatformIcon() error {
	if runtime.GOOS == "windows" {
		// 文件名带 PID,多实例不再互相覆盖同一份临时文件。
		path := filepath.Join(os.TempDir(), fmt.Sprintf("lgo_download_manager_tray_%d.ico", os.Getpid()))
		if err := os.WriteFile(path, trayIconICO, 0o644); err != nil {
			return err
		}
		if err := systray.SetIconFromFilePath(path); err != nil {
			_ = os.Remove(path)
			return err
		}
		iconFileMu.Lock()
		iconFile = path
		iconFileMu.Unlock()
		return nil
	}
	systray.SetIcon(trayIconForPlatform())
	return nil
}

// removeIconFile 删除落盘的托盘图标临时文件(可重复调用)。
func removeIconFile() {
	iconFileMu.Lock()
	path := iconFile
	iconFile = ""
	iconFileMu.Unlock()
	if path != "" {
		_ = os.Remove(path)
	}
}

// trayIconForPlatform 返回最适合当前平台的托盘图标字节（非 Windows）。
func trayIconForPlatform() []byte {
	switch runtime.GOOS {
	case "darwin":
		return trayIcon64
	case "linux":
		return trayIcon22
	default:
		return trayIcon32
	}
}
