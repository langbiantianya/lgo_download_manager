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
	"runtime"

	"fyne.io/systray"
)

// 嵌入托盘图标。三种尺寸分别覆盖：
//   - tray-22.png：Linux libappindicator 兼容性最佳
//   - tray-32.png：Linux 桌面高 DPI / Windows
//   - tray.png    ：macOS / 一般 fallback（64×64）
//
//go:embed assets/tray-22.png
var trayIcon22 []byte

//go:embed assets/tray-32.png
var trayIcon32 []byte

//go:embed assets/tray.png
var trayIcon64 []byte

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
// 启动后，托盘自带一个 “Open” 与 “退出” 菜单项；点击会触发对应的回调。
func Start(cb Callbacks) {
	onReady := func() {
		systray.SetTitle("下载管理器")
		systray.SetTooltip("下载管理器")

		systray.SetIcon(trayIconForPlatform())
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

// trayIconForPlatform 返回最适合当前平台的托盘图标字节。
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
