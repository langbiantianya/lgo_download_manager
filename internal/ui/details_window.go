// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"sync"

	"fyne.io/fyne/v2"
)

// detailsWindows 记录每个任务已经打开的「任务详情」窗口。
//
// 一个任务只允许开一个详情窗口：再点一次「详情」按钮时把已有的窗口置前，
// 而不是再开一个——否则手快连点两下，桌面上就会多出一个内容一模一样
// 的窗口，而且它自己还在每秒轮询，白白多一份开销。
//
// 不同任务各自一个窗口，不受这条限制。
var detailsWindows = struct {
	mu sync.Mutex
	m  map[string]fyne.Window
}{m: make(map[string]fyne.Window)}

// focusExistingDetails 如果该任务已经开着详情窗口，把它置前（请求焦点）
// 并返回 true——调用方据此知道「不用再开一个」。
func focusExistingDetails(taskID string) bool {
	detailsWindows.mu.Lock()
	w := detailsWindows.m[taskID]
	detailsWindows.mu.Unlock()
	if w == nil {
		return false
	}
	w.RequestFocus()
	return true
}

// registerDetailsWindow 登记详情窗口，窗口关闭时自动摘除。
//
// onClose（可为 nil）在窗口关闭时先被调用——BT 面板用它停轮询，HTTP 面板
// 用它退订业务事件流；随后窗口才从表里摘掉，这样「再点一次详情」立刻
// 能重新开一个。
func registerDetailsWindow(taskID string, w fyne.Window, onClose func()) {
	detailsWindows.mu.Lock()
	detailsWindows.m[taskID] = w
	detailsWindows.mu.Unlock()

	w.SetOnClosed(func() {
		if onClose != nil {
			onClose()
		}
		detailsWindows.mu.Lock()
		if detailsWindows.m[taskID] == w {
			delete(detailsWindows.m, taskID)
		}
		detailsWindows.mu.Unlock()
	})
}
