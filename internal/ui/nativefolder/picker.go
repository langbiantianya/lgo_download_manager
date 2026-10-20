// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package nativefolder exposes a single PickFolder entry point that opens
// the operating system's native folder-selection dialog.
//
// The Windows backend uses the modern IFileOpenDialog COM interface (the
// same dialog Windows Explorer itself presents for "Choose a folder");
// the macOS backend drives NSOpenPanel via osascript; the Linux backend
// shells out to zenity (with kdialog as fallback). All backends are
// implemented as separate files gated by build tags so this file carries
// only the shared contract.
package nativefolder

import "lgo_download_manager/internal/ilocale"

// pickerTitle 是原生目录选择器的窗口标题。
//
// 早期版本在各平台实现里直接硬编码中文「选择保存位置」,界面语言切到
// 英文/日文/俄文之后,系统弹出的原生对话框仍然顶着中文标题。标题现在
// 跟着界面语言走。
func pickerTitle() string {
	return ilocale.T("picker.title")
}

// ErrCancelled is returned when the user dismisses the dialog without
// selecting a folder. Callers should treat it as a non-error.
var ErrCancelled = errCancelled{}

type errCancelled struct{}

func (errCancelled) Error() string { return "folder selection cancelled" }

// IsCancelled reports whether err originated from the user cancelling
// the dialog.
func IsCancelled(err error) bool {
	_, ok := err.(errCancelled)
	return ok
}
