// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
)

// pieceMosaicCell 是热力图里一个格子的边长(逻辑像素,含缝隙)。
// 和 HTTP 面板的字节马赛克同一套视觉语言:小方块 + 一格缝。
const pieceMosaicCell = 16

// pieceMosaicHeight 是热力图区域的固定高度(逻辑像素)。
const pieceMosaicHeight = 180

// pieceMosaic 把 BT 的分片级进度画成一幅热力马赛克图。
//
// 数据来自 bittorrent.Detail.PieceBuckets:全部分片按等宽聚合成的桶,
// 每桶一个字节(0=未开始,255=完整),值是桶内已下载字节的占比。颜色按
// 完成度渐变——没开始的用输入框背景色(融进面板底色),进行中的从主题
// 主色渐变到成功色,下载完的用成功色(绿)。
//
// 分片是 BT 天然的进度单位:每个分片独立校验、独立传输,哪块下完了
// 与整体进度无关。按分片画才能看出「哪些区段已经拿到手」,这正是
// 字节马赛克在 BT 上做不到的(BT 没有连续的字节区间概念)。
//
// 整幅图用一张 canvas.Raster 直接画出来:1024 个分片桶如果每块都做成
// widget,每秒轮询一次就要重建上千个 canvas.Rectangle;一张位图只有
// 一个对象,重画只是重新生成像素。
type pieceMosaic struct {
	raster *canvas.Raster

	// mu 保护 buckets/palette:生成函数跑在绘制线程上,更新跑在 Fyne
	// 事件线程上,两边都要碰这两份数据。
	mu      sync.Mutex
	buckets []uint8
	palette piecePalette
}

// piecePalette 是画热力图要用的三个主题色。生成函数在绘制线程上跑,
// 不能直接去问当前主题(有竞态),所以更新时取好存下来。
type piecePalette struct {
	bg      color.NRGBA
	primary color.NRGBA
	success color.NRGBA
}

// newPieceMosaic 创建一个空的热力图。
func newPieceMosaic() *pieceMosaic {
	m := &pieceMosaic{}
	m.palette = currentPiecePalette()
	m.raster = canvas.NewRaster(m.generate)
	m.raster.SetMinSize(fyne.NewSize(560, pieceMosaicHeight))
	return m
}

// content 返回热力图本体,交给详情面板摆放。
func (m *pieceMosaic) content() fyne.CanvasObject { return m.raster }

// update 用一次分片采样刷新热力图。
func (m *pieceMosaic) update(buckets []uint8) {
	m.mu.Lock()
	m.buckets = append(m.buckets[:0], buckets...)
	m.palette = currentPiecePalette()
	m.mu.Unlock()
	m.raster.Refresh()
}

// snapshot 复制一份当前的分片数据与配色,供绘制线程使用。
func (m *pieceMosaic) snapshot() ([]uint8, piecePalette) {
	m.mu.Lock()
	defer m.mu.Unlock()
	buckets := make([]uint8, len(m.buckets))
	copy(buckets, m.buckets)
	return buckets, m.palette
}

// generate 画出整幅热力图。w/h 是屏幕像素(绘制线程给的),不是逻辑像素。
func (m *pieceMosaic) generate(w, h int) image.Image {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	buckets, pal := m.snapshot()
	// 底色:和输入框背景一致,没下载的部分融进面板里。
	draw.Draw(img, img.Bounds(), &image.Uniform{C: pal.bg}, image.Point{}, draw.Src)
	if len(buckets) == 0 || w <= 0 || h <= 0 {
		return img
	}

	// 格子尺寸按逻辑像素定义,拿到的却是屏幕像素,先换算比例。
	cell := int(math.Round(pieceMosaicCell * m.scale(w)))
	if cell < 2 {
		cell = 2
	}

	// 先按宽度算出每行能放几块,再按需要的行数把格子缩小,保证整幅图
	// 放得下(放不下的桶会被裁掉,宁可少画也别让格子挤成一团)。
	cols, rows, gap := pieceGrid(w, len(buckets), cell)
	for rows*cell-(rows-1)*gap > h && cell > 2 {
		cell--
		cols, rows, gap = pieceGrid(w, len(buckets), cell)
	}

	tile := cell - gap
	for i, b := range buckets {
		col := i % cols
		row := i / cols
		x := col * (cell + gap)
		y := row * (cell + gap)
		if y+tile > h {
			break
		}
		tw := tile
		if x+tw > w {
			tw = w - x
		}
		if tw <= 0 {
			continue
		}
		c := heatColor(pal, float64(b)/255)
		draw.Draw(img, image.Rect(x, y, x+tw, y+tile), &image.Uniform{C: c}, image.Point{}, draw.Src)
	}
	return img
}

// scale 返回屏幕像素与逻辑像素的比例。
func (m *pieceMosaic) scale(deviceWidth int) float64 {
	logical := m.raster.Size().Width
	if logical <= 0 || deviceWidth <= 0 {
		return 1
	}
	s := float64(deviceWidth) / float64(logical)
	if s <= 0 {
		return 1
	}
	return s
}

// pieceGrid 按可用宽度和格子边长算出每行几块、共几行、缝隙多宽。
// 缝隙取格子的四分之一,格子缩小后缝隙也跟着变小,方块看起来始终方正。
func pieceGrid(w, n, cell int) (cols, rows, gap int) {
	gap = cell / 4
	if gap < 1 {
		gap = 1
	}
	cols = (w + gap) / (cell + gap)
	if cols < 1 {
		cols = 1
	}
	rows = (n + cols - 1) / cols
	return cols, rows, gap
}

// heatColor 按完成度取色:没开始的用背景色,下载完的用成功色,中间的
// 在主色与成功色之间线性插值——这样一眼就能分出「没动」「在传」「好了」。
func heatColor(pal piecePalette, frac float64) color.NRGBA {
	switch {
	case frac <= 0:
		return pal.bg
	case frac >= 1:
		return pal.success
	default:
		return lerpNRGBA(pal.primary, pal.success, frac)
	}
}

// lerpNRGBA 在两个颜色之间线性插值。
func lerpNRGBA(a, b color.NRGBA, t float64) color.NRGBA {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	mix := func(x, y uint8) uint8 {
		return uint8(float64(x) + (float64(y)-float64(x))*t)
	}
	return color.NRGBA{
		R: mix(a.R, b.R),
		G: mix(a.G, b.G),
		B: mix(a.B, b.B),
		A: mix(a.A, b.A),
	}
}

// currentPiecePalette 取当前主题下的三个颜色。
func currentPiecePalette() piecePalette {
	th := fyne.CurrentApp().Settings().Theme()
	v := fyne.CurrentApp().Settings().ThemeVariant()
	return piecePalette{
		bg:      toNRGBA(th.Color(theme.ColorNameInputBackground, v)),
		primary: toNRGBA(th.Color(theme.ColorNamePrimary, v)),
		success: toNRGBA(th.Color(theme.ColorNameSuccess, v)),
	}
}

// toNRGBA 把任意颜色转成 NRGBA,方便做逐通道插值。
func toNRGBA(c color.Color) color.NRGBA {
	if c == nil {
		return color.NRGBA{}
	}
	return color.NRGBAModel.Convert(c).(color.NRGBA)
}
