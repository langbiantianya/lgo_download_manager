// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package ui provides the Gio-based frontend.
//
// Window model: each logical window (main, new-task, settings, details)
// owns its own *app.Window and runs its own event loop on its own
// goroutine. Layout closures (called per FrameEvent) read & write the
// window's own widget state without sharing state with other windows.
//
// Shared state (filter, search, settings) lives on *App and is read by
// each window's Layout under a mutex. Writes come from the window that
// owns the input (e.g. search box → main window), then call
// App.Invalidate on every dependent window.
package ui

import (
	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget/material"
)

// runWindow 启动一个 Gio app.Window 并按 frame 调用 root。
//
// 在它自己的 goroutine 中运行；返回时（窗口关闭）goroutine 退出。
// 把所有窗口的 runWindow 启动后，调用方在主 goroutine 上调用 app.Main()
// 即可。当所有窗口都关闭时 app.Main 自然返回。
func runWindow(w *app.Window, root func(gtx layout.Context) layout.Dimensions) {
	go func() {
		for {
			e := w.Event()
			switch ev := e.(type) {
			case app.DestroyEvent:
				return
			case app.FrameEvent:
				gtx := app.NewContext(&op.Ops{}, ev)
				root(gtx)
				ev.Frame(gtx.Ops)
			}
		}
	}()
}

// newTheme 返回所有窗口共享的主题。
//
// 字体优先级：Noto Sans CJK SC（含 Bold/Medium weight）→ Go (gofont, latin)。
// 这样中文正常字重渲染，latin 数字/标点仍优雅。
func newTheme() *material.Theme {
	th := material.NewTheme()
	th.Shaper = newShaper()
	th.Face = "Noto Sans CJK SC,Go"
	return th
}
