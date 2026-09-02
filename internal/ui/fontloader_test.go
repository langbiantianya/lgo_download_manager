// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import "testing"

// TestLoadFonts 验证 loadFonts 至少返回 gofont 的字体——CJK 字体可能
// 在 CI 容器里缺失，那时仍应能加载（中文仍会显示为方框，但程序不崩）。
func TestLoadFonts(t *testing.T) {
	faces := loadFonts()
	if len(faces) == 0 {
		t.Fatal("loadFonts returned no faces — gofont collection missing?")
	}
	t.Logf("loaded %d font faces", len(faces))
}
