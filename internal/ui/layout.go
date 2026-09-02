// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// 设计 token：所有窗口用同一套间距尺度（Material Design 8dp 网格）。
package ui

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// 间隙系统。
const (
	SpaceXS unit.Dp = 4
	SpaceSM unit.Dp = 8
	SpaceMD unit.Dp = 16
	SpaceLG unit.Dp = 24
	SpaceXL unit.Dp = 32
)

// 控件尺寸。
const (
	RowHeight    unit.Dp = 48
	SidebarWidth unit.Dp = 200
	CardRadius   unit.Dp = 8
	ButtonRadius unit.Dp = 6
)

// vSpaceWidget 固定高度空白 widget（用于 widget 列表）。
func vSpaceWidget(dp unit.Dp) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Spacer{Height: dp}.Layout(gtx)
	}
}

// hSpaceWidget 固定宽度空白 widget。
func hSpaceWidget(dp unit.Dp) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Spacer{Width: dp}.Layout(gtx)
	}
}

// vspace Flex 子节点：固定高度空白。
func vspace(dp unit.Dp) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Spacer{Height: dp}.Layout(gtx)
	})
}

// hspace Flex 子节点：固定宽度空白。
func hspace(dp unit.Dp) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Spacer{Width: dp}.Layout(gtx)
	})
}

// flexHspace 弹性水平空白。
func flexHspace() layout.FlexChild {
	return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{}
	})
}

// separator 1dp 浅色横线，用于区块分隔（返回 widget）。
func separator(th *material.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: SpaceSM, Bottom: SpaceSM}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(unit.Dp(1)))
			defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, th.Palette.Fg)
			return layout.Dimensions{Size: size}
		})
	}
}

// toolbarButton 统一风格按钮：8dp 内边距、6dp 圆角、14sp 文字。
func toolbarButton(th *material.Theme, c *widget.Clickable, label string, onClick func()) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		for c.Clicked(gtx) {
			if onClick != nil {
				onClick()
			}
		}
		b := material.Button(th, c, label)
		b.CornerRadius = ButtonRadius
		b.Inset = layout.UniformInset(unit.Dp(8))
		b.TextSize = unit.Sp(14)
		return b.Layout(gtx)
	})
}

// toolbarSpacer 工具栏内按钮之间的小间距。
func toolbarSpacer() layout.FlexChild {
	return hspace(SpaceSM)
}

// sectionSpacer 区块之间大间距。
func sectionSpacer() layout.FlexChild {
	return vspace(SpaceMD)
}