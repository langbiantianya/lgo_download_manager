// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/logging"
)

// errorParent 返回能承载对话框的窗口。
//
// lgom:// 转发路径没有主窗口可传（parent 为 nil），而 Fyne 的
// dialog.ShowError 会解引用 parent.Canvas()——nil 会直接 panic，整个 UI
// 子进程就此消失（业务进程还活着，只是窗口没了）。这里退回到全局主
// 窗口，再退回到应用里的第一个窗口。
func errorParent(parent fyne.Window) fyne.Window {
	if parent != nil {
		return parent
	}
	if globalWin != nil {
		return globalWin
	}
	for _, w := range fyne.CurrentApp().Driver().AllWindows() {
		if w != nil {
			return w
		}
	}
	return nil
}

// showError 弹一个错误对话框。
//
// parent 可能为 nil（lgom:// 转发路径），此时退回全局主窗口；实在没有
// 窗口可挂（不该发生）就只记日志——绝不因为「没有窗口」而 panic。
func showError(parent fyne.Window, err error) {
	if err == nil {
		return
	}
	p := errorParent(parent)
	if p == nil {
		logging.Printf("ui: no window to show the error dialog: %v", err)
		return
	}
	dialog.ShowError(err, p)
}

// showConfirmDialog 是弹出确认对话框的入口。
//
// 抽成变量是为了让测试能替换它：确认框由 Fyne 渲染成 canvas overlay，
// 测试里没必要去翻那棵内部对象树找按钮——替换掉之后可以直接断言
// 「用户确认之前不执行动作」。
var showConfirmDialog = showConfirm

// showConfirm 弹出一个确认对话框，onConfirm 只在用户确认时调用。
//
// 用 dialog.NewCustomConfirm 而不是 dialog.ShowConfirm：后者把两个按钮
// 的文案写死成 Fyne 自带的 "Yes"/"No"（lang.L），不跟本应用的翻译走。
//
// 没有窗口可挂时不弹对话框、也不执行 onConfirm——「没法问」的时候直接
// 动手比悄悄删掉用户的任务更糟。
func showConfirm(parent fyne.Window, title, message, confirmText string, onConfirm func()) {
	p := errorParent(parent)
	if p == nil {
		logging.Printf("ui: no window to show the confirm dialog: %q", title)
		return
	}
	msg := widget.NewLabel(message)
	msg.Wrapping = fyne.TextWrapWord
	d := dialog.NewCustomConfirm(title, confirmText, ilocale.T("common.cancel"), msg, func(ok bool) {
		if ok && onConfirm != nil {
			onConfirm()
		}
	}, p)
	d.Show()
}
