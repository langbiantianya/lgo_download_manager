// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// diskRow 渲染一个挂载点的占用：固定宽标签 + 进度条 + 字节统计。
func diskRow(th *material.Theme, d diskInfo) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				// 标签列固定 80dp，让所有进度条起点对齐。
				gtx.Constraints.Min.X = gtx.Dp(unit.Dp(80))
				gtx.Constraints.Max.X = gtx.Dp(unit.Dp(80))
				return material.Body2(th, d.Label).Layout(gtx)
			}),
			hspace(SpaceMD),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return material.ProgressBar(th, d.Frac).Layout(gtx)
			}),
			hspace(SpaceMD),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return material.Caption(th, humanBytes(d.Used)+" / "+humanBytes(d.Total)).Layout(gtx)
			}),
		)
	}
}