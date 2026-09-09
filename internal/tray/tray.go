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
	"log"
	"os"
	"path/filepath"
	"runtime"

	"fyne.io/systray"
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

// Start 在当前 goroutine 上启动 systray 事件循环。
//
// 它会一直阻塞直到 systray.Quit() 被调用——因此必须在主流程中
// 以独立 goroutine 启动。
//
func Start(cb Callbacks) {
	onReady := func() {
		systray.SetTitle("下载管理器")
		systray.SetTooltip("下载管理器")

		if err := setPlatformIcon(); err != nil {
			log.Printf("tray: set icon: %v", err)
		}
		systray.SetTemplateIcon(trayIcon32, trayIcon32)
		mOpen := systray.AddMenuItem("显示窗口", "显示主窗口")
		mQuit := systray.AddMenuItem("退出", "退出下载管理器")
		systray.AddSeparator()

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

	onExit := func() {
		log.Println("tray: exited")
	}

	systray.Run(onReady, onExit)
}

// Stop 通知 systray 退出事件循环。可以在主程序结束时调用以释放托盘。
func Stop() {
	systray.Quit()
}

// setPlatformIcon 按平台设置托盘图标。
//
// Windows 上 fyne.io/systray 会把传入的字节写入无扩展名的临时文件再交
// 给 Win32 LoadImageW；为了让 LoadImageW 可靠嗅探出图标资源，我们直接
// 走 SetIconFromFilePath，自行落盘一个 .ico 临时文件。
func setPlatformIcon() error {
	if runtime.GOOS == "windows" {
		path := filepath.Join(os.TempDir(), "lgo_download_manager_tray.ico")
		if err := os.WriteFile(path, trayIconICO, 0o644); err != nil {
			return err
		}
		return systray.SetIconFromFilePath(path)
	}
	systray.SetIcon(trayIconForPlatform())
	return nil
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
