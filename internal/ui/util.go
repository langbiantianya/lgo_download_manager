// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"image/color"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"

	"gioui.org/widget"
)

// funcPtrOf 通过 reflect 取 func 值的代码指针，用于回调取消时身份比较。
func funcPtrOf(f func()) uintptr {
	return reflect.ValueOf(f).Pointer()
}

// openPath 调系统默认应用打开 path。
func openPath(path string) {
	if path == "" {
		return
	}
	abs, _ := filepath.Abs(path)
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", abs).Run()
	case "windows":
		_ = exec.Command("cmd", "/c", "start", "", abs).Run()
	default:
		_ = exec.Command("xdg-open", abs).Run()
	}
}

// colorForProgress 返回磁盘条颜色（绿→黄→红）。
func colorForProgress(frac float32) color.NRGBA {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	var r, g, b float64
	if frac < 0.5 {
		t := float64(frac) * 2
		r = t*255 + (1-t)*76
		g = t*235 + (1-t)*175
		b = t*59 + (1-t)*80
	} else {
		t := (float64(frac) - 0.5) * 2
		r = t*244 + (1-t)*255
		g = t*67 + (1-t)*235
		b = t*54 + (1-t)*59
	}
	return color.NRGBA{R: byte(r), G: byte(g), B: byte(b), A: 255}
}

// 抑制未使用
var _ = widget.Clickable{}
