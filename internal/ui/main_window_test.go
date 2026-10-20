// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// TestDiskBar_RefreshUpdatesFillWidth 回归测试。
//
// 磁盘占用条的填充长度原先只在 Layout() 里计算,而 Layout() 只在重新
// 布局时才会跑:进度从 20% 跳到 80%,颜色变了,长度却纹丝不动(实测两种
// 情况下填充部分的像素宽度都是 156)。刷新时必须同时更新长度。
func TestDiskBar_RefreshUpdatesFillWidth(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	const barWidth = 200.0
	db := newDiskBar()
	db.Resize(fyne.NewSize(barWidth, 4))

	db.setProgress(0.2)
	if got := db.fill.Size().Width; got != 40 {
		t.Errorf("fill width at 20%% = %v, want 40", got)
	}
	db.setProgress(0.8)
	if got := db.fill.Size().Width; got != 160 {
		t.Errorf("fill width at 80%% = %v, want 160", got)
	}
	db.setProgress(0)
	if got := db.fill.Size().Width; got != 0 {
		t.Errorf("fill width at 0%% = %v, want 0", got)
	}
}

// TestDiskBar_FillNeverExceedsBar 边界:进度被夹到 [0,1] 之外时,填充
// 长度不能超出条本身,否则会画到控件外面去。
func TestDiskBar_FillNeverExceedsBar(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	db := newDiskBar()
	db.Resize(fyne.NewSize(200, 4))

	db.setProgress(2)
	if got := db.fill.Size().Width; got != 200 {
		t.Errorf("fill width at 200%% = %v, want 200 (clamped to the bar width)", got)
	}
	db.setProgress(-1)
	if got := db.fill.Size().Width; got != 0 {
		t.Errorf("fill width at -100%% = %v, want 0 (clamped to zero)", got)
	}
}
