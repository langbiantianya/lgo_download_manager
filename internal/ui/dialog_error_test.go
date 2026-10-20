// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"errors"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestErrorParentFallsBackToMainWindow 校验错误对话框的宿主窗口选取：
// 显式传入的窗口优先，其次是全局主窗口，最后才是应用里的其它窗口。
//
// lgom:// 转发路径（ShowAddTaskDialogForURL）没有主窗口可传，传进来的是
// nil——Fyne 的 dialog.ShowError 会解引用 parent.Canvas()，nil 直接
// panic，整个 UI 子进程消失。
func TestErrorParentFallsBackToMainWindow(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	old := globalWin
	defer func() { globalWin = old }()
	globalWin = nil

	// 显式传入的窗口优先。
	w := test.NewWindow(widget.NewLabel("x"))
	if got := errorParent(w); got != w {
		t.Errorf("errorParent(explicit) = %v, want the explicit window", got)
	}

	// 没有显式窗口时退回全局主窗口。
	globalWin = w
	if got := errorParent(nil); got != w {
		t.Errorf("errorParent(nil) = %v, want the main window", got)
	}

	// 全局主窗口也没有时：退回到应用里的某个窗口（而不是 nil）——
	// 只要有窗口能挂，错误就得让用户看见。
	globalWin = nil
	if got := errorParent(nil); got == nil {
		t.Error("errorParent(nil) should fall back to one of the app windows")
	}
}

// TestShowErrorWithoutParentDoesNotPanic 校验「没有窗口可挂」时弹错误
// 不会 panic——这是 lgom:// 转发路径曾经把整个 UI 进程带走的崩溃点。
func TestShowErrorWithoutParentDoesNotPanic(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	old := globalWin
	defer func() { globalWin = old }()
	globalWin = nil

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("showError without any window panicked: %v", r)
		}
	}()
	showError(nil, errors.New("boom"))
}

// TestShowErrorWithMainWindowDoesNotPanic 校验有主窗口时错误对话框能正常
// 弹出来（nil parent 的另一半：兜底路径本身也不能炸）。
func TestShowErrorWithMainWindowDoesNotPanic(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	old := globalWin
	defer func() { globalWin = old }()

	w := test.NewWindow(widget.NewLabel("x"))
	globalWin = w
	defer w.Close()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("showError with a main window panicked: %v", r)
		}
	}()
	showError(nil, errors.New("boom"))
}

var _ = fyne.CurrentApp
