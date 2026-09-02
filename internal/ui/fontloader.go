// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"os"

	giofont "gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/text"
)
// cjkFontPaths 是按平台/常见发行版排序的 CJK 字体候选路径。
//
// 加载顺序：找到第一个存在的字体。所有字体都可解析为 OpenType/TTC collection。
// 缺失时返回 nil——Gio 退回到只使用 gofont（中文仍会显示为方框）。
var cjkFontPaths = []string{
	// Linux: Noto CJK（多语言，体积大但稳定）。
	"/usr/share/fonts/google-noto-sans-cjk-vf-fonts/NotoSansCJK-VF.ttc",
	"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	// Linux: Source Han Sans CN（中文专用，更小）。
	"/usr/share/fonts/adobe-source-han-sans-cn-fonts/SourceHanSansCN-Normal.otf",
	"/usr/share/fonts/adobe-source-han-sans-cn-fonts/SourceHanSansCN-Regular.otf",
	"/usr/share/fonts/source-han-sans/SourceHanSansCN-Regular.otf",
	// Linux: 文泉驿（fallback，质量低）。
	"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
	"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
	// macOS
	"/System/Library/Fonts/PingFang.ttc",
	"/System/Library/Fonts/STHeiti Medium.ttc",
	"/System/Library/Fonts/Hiragino Sans GB.ttc",
	// Windows
	`C:\Windows\Fonts\msyh.ttc`,
	`C:\Windows\Fonts\simhei.ttf`,
	`C:\Windows\Fonts\simsun.ttc`,
}

// loadFonts 构建 Gio shaper 使用的字体集合：gofont（latin）+ CJK 字体。
func loadFonts() []giofont.FontFace {
	out := []giofont.FontFace{}
	// gofont 提供 latin + 标点。
	out = append(out, gofont.Collection()...)
	// 加载 CJK：find first existing path then ParseCollection。
	for _, p := range cjkFontPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		faces, err := opentype.ParseCollection(data)
		if err != nil {
			continue
		}
		out = append(out, faces...)
		// 找到第一个就够——保留可扩展为多源。
		break
	}
	return out
}

// newShaper 返回支持中文的 shaper。
func newShaper() *text.Shaper {
	return text.NewShaper(text.WithCollection(loadFonts()))
}
