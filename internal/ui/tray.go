// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

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

// TrayCallbacks 描述托盘菜单触发的回调。
//
// 全部回调必须由调用方保证线程安全：systray 自身运行在自己的事件循环中，
// 回调通常在非主 goroutine 上触发；Fyne 端的窗口操作通过 fyne.Do 投递
// 即可保证 GUI 线程安全。
type TrayCallbacks struct {
	// Open 在用户点击 “Open” 菜单项时调用。
	// 应当确保主窗口可见、获得焦点；轻量模式下还需重建内容。
	Open func()

	// Quit 在用户点击 “退出” 菜单项时调用。
	// 调用方负责结束主事件循环并清理资源（通常调用 fyne.App.Quit()）。
	Quit func()
}

// StartTray 在当前 goroutine 上启动 systray 事件循环。
//
// 它会一直阻塞直到 systray.Quit() 被调用——因此必须在 Fyne 主事件
// 循环运行之前启动，或者放在独立的 goroutine 里。
//
// 启动后，托盘自带一个 “Open” 与 “退出” 菜单项；点击会触发对应的回调。
func StartTray(cb TrayCallbacks) {
	onReady := func() {
		systray.SetTitle("下载管理器")
		systray.SetTooltip("下载管理器")

		// systray 在不同平台使用不同尺寸：
		//   - Linux：优先 22×22；AppIndicator 对尺寸敏感。
		//   - macOS：使用模板图标随系统主题反转（这里用普通图标亦可）。
		//   - Windows：32×32 最稳。
		// SetTemplateIcon（仅 macOS 真正生效）使用单色 alpha 通道图；
		// 我们使用 SetIcon 并按平台自动挑最合适的尺寸。
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

// trayIconForPlatform 返回最适合当前平台的托盘图标字节。
//
// 平台检测在运行时（fyne.io/systray 的 build tag 在 import 时已
// 决定了底层 driver，但不会自动挑选合适尺寸），因此用一个简单的
// 选择策略：默认 32×32；macOS 走 64×64 以适配 Retina。
func trayIconForPlatform() []byte {
	// fyne.io/systray 在 build 时已决定 driver；
	// runtime.GOOS 可用于按 OS 选择尺寸。
	switch runtime.GOOS {
	case "darwin":
		return trayIcon64
	case "linux":
		return trayIcon22
	default:
		return trayIcon32
	}
}

// StopTray 通知 systray 退出事件循环。可以在主程序结束时调用以释放托盘。
func StopTray() {
	systray.Quit()
}
