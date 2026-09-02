// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"fyne.io/systray"
)

// StartTray 启动系统托盘（独立 goroutine，不参与 Gio 主循环）。
//
// 返回 start/end：start() 启动托盘事件循环，end() 停止。
func StartTray(app *App, onExit func()) (start func(), end func()) {
	return systray.RunWithExternalLoop(makeOnReady(app), func() {
		if onExit != nil {
			onExit()
		}
	})
}

func makeOnReady(a *App) func() {
	return func() {
		systray.SetTitle("LDM")
		systray.SetTooltip("下载管理器")
		open := systray.AddMenuItem("显示主窗口", "打开下载管理器")
		quit := systray.AddMenuItem("退出", "退出应用")
		go func() {
			for {
				select {
				case <-open.ClickedCh:
					if a != nil {
						a.notifyAll()
					}
				case <-quit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}
}
