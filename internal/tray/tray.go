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

// 菜单项引用。Reload 直接在这些 item 上改标题/工具提示,
// 不重建菜单——systray.MenuItem.Remove() 会 close(ClickedCh),
// 而点击派发 goroutine 的 case <-mQuit.ClickedCh 在通道关闭后会立刻
// 收到零值并调用 Quit 回调,把业务进程误杀(实测:切语言 → 应用退出)。
//
// 这些字段只在 systray 事件循环(onReady/Reload)里读写,Reload 也可能
// 从业务进程其它 goroutine 调用,所以用 trayMu 串行化。
var (
	trayMu    sync.Mutex
	trayReady bool
	mOpen     *systray.MenuItem
	mQuit     *systray.MenuItem
)

// Start 在当前 goroutine 上启动 systray 事件循环。
//
// 它会一直阻塞直到 systray.Quit() 被调用——因此必须在主流程中
// 以独立 goroutine 启动。
func Start(cb Callbacks) {
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

		trayMu.Lock()
		systray.SetTitle(ilocale.T("main.window.title"))
		systray.SetTooltip(ilocale.T("main.window.title"))
		open := systray.AddMenuItem(ilocale.T("tray.menu.open"), ilocale.T("tray.menu.open.tooltip"))
		systray.AddSeparator()
		quit := systray.AddMenuItem(ilocale.T("tray.menu.quit"), ilocale.T("tray.menu.quit.tooltip"))
		mOpen, mQuit = open, quit
		trayReady = true
		trayMu.Unlock()

		go dispatchClicks(open.ClickedCh, quit.ClickedCh, cb)
	}

	onExit := func() {
		logging.Println("tray: exited")
	}

	systray.Run(onReady, onExit)
	// 事件循环结束后图标已无用处:即便调用方没走 Stop() 也不留临时文件。
	removeIconFile()
}

// dispatchClicks 消费托盘菜单的点击事件。open 触发 cb.Open 后继续等待,
// quit 触发 cb.Quit 后返回(退出整个托盘会话)。
//
// 关键不变量:通道被关闭时只退出,绝不调用回调。systray 的
// MenuItem.Remove() 会 close(ClickedCh);若把关闭当作「点击」处理,
// 一次菜单重建就会误触发 Quit,把业务进程杀掉(实测现象:切语言后
// 应用直接退出)。
func dispatchClicks(open, quit <-chan struct{}, cb Callbacks) {
	for {
		select {
		case _, ok := <-open:
			if !ok {
				return
			}
			if cb.Open != nil {
				cb.Open()
			}
		case _, ok := <-quit:
			if !ok {
				return
			}
			if cb.Quit != nil {
				cb.Quit()
			}
			return
		}
	}
}

// Reload 在语言变更后刷新托盘标题与菜单项文案。
//
// 用 MenuItem.SetTitle/SetTooltip 就地覆盖,不调用 ResetMenu——
// 后者会 Remove 菜单项并 close(ClickedCh),误触发点击派发 goroutine
// 的 Quit 分支(见上方字段注释)。调用前必须等 Start 的 onReady 跑完
// (菜单项尚未创建时直接跳过,onReady 会用当时最新的语言一次建好)。
func Reload() {
	trayMu.Lock()
	defer trayMu.Unlock()
	if !trayReady {
		// 托盘尚未就绪:首启菜单还没建,等 onReady 用最新语言一次建好即可。
		return
	}
	systray.SetTitle(ilocale.T("main.window.title"))
	systray.SetTooltip(ilocale.T("main.window.title"))
	if mOpen != nil {
		mOpen.SetTitle(ilocale.T("tray.menu.open"))
		mOpen.SetTooltip(ilocale.T("tray.menu.open.tooltip"))
	}
	if mQuit != nil {
		mQuit.SetTitle(ilocale.T("tray.menu.quit"))
		mQuit.SetTooltip(ilocale.T("tray.menu.quit.tooltip"))
	}
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
