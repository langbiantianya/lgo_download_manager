// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

const (
	mosaicBlockSize int64 = 1 << 20
	maxTiles              = 1024
	tileSizeDp            = 12
)

// chunkMosaic 把分块进度可视化为瓦片墙。
type chunkMosaic struct {
	tiles []tileRect

	total   int64
	blockSz int64
	blocks  int
}

type tileRect struct{ covered float64 }

func (m *chunkMosaic) resizeForTotal(total int64) {
	if total <= 0 {
		m.total, m.blocks, m.blockSz = 0, 0, 0
		m.tiles = nil
		return
	}
	blocks := int(total / mosaicBlockSize)
	if total%mosaicBlockSize != 0 {
		blocks++
	}
	if blocks > maxTiles {
		blocks = maxTiles
		m.blockSz = total / int64(maxTiles)
		if total%int64(maxTiles) != 0 {
			m.blockSz++
		}
	} else {
		m.blockSz = mosaicBlockSize
	}
	m.total = total
	m.blocks = blocks
	m.tiles = make([]tileRect, blocks)
}

func (m *chunkMosaic) update(chunkProgress, chunkRanges []int64, totalSize int64) {
	if totalSize != m.total {
		m.total = totalSize
		m.resizeForTotal(totalSize)
	}
	if totalSize <= 0 || m.blocks == 0 {
		return
	}
	ranges := chunkRanges
	progress := chunkProgress
	if len(ranges) == 0 || len(ranges) != 2*len(progress) {
		threads := len(progress)
		if threads == 0 {
			m.tiles = make([]tileRect, m.blocks)
			return
		}
		base := totalSize / int64(threads)
		rem := totalSize % int64(threads)
		ranges = make([]int64, 0, 2*threads)
		cur := int64(0)
		for ti := range threads {
			size := base
			if ti == threads-1 {
				size += rem
			}
			ranges = append(ranges, cur, cur+size-1)
			cur += size
		}
	}
	for b := range m.blocks {
		tileStart := int64(b) * m.blockSz
		tileEnd := tileStart + m.blockSz
		if tileEnd > totalSize {
			tileEnd = totalSize
		}
		tileSize := tileEnd - tileStart
		var covered int64
		for i := 0; i+1 < len(ranges); i += 2 {
			cStart := ranges[i]
			cEnd := ranges[i+1] + 1
			if cEnd <= tileStart || cStart >= tileEnd {
				continue
			}
			oStart := max64(cStart, tileStart)
			oEnd := min64(cEnd, tileEnd)
			var prog int64
			if i/2 < len(progress) {
				prog = progress[i/2]
			}
			writtenThrough := cStart + prog
			if writtenThrough <= oStart {
				continue
			}
			if writtenThrough >= oEnd {
				covered += oEnd - oStart
			} else {
				covered += writtenThrough - oStart
			}
		}
		var frac float64
		if tileSize > 0 {
			frac = float64(covered) / float64(tileSize)
			if frac > 1 {
				frac = 1
			}
		}
		m.tiles[b].covered = frac
	}
}

// Layout 把瓦片渲染为 image.Image 上屏。
//
// 主题色硬编码（中性灰 + 绿），避免依赖 material.Theme 引用循环。
func (m *chunkMosaic) Layout(gtx layout.Context) layout.Dimensions {
	if len(m.tiles) == 0 {
		return layout.Dimensions{}
	}
	tileSz := gtx.Dp(unit.Dp(tileSizeDp))
	maxW := gtx.Constraints.Max.X
	cols := maxW / tileSz
	if cols < 1 {
		cols = 1
	}
	rows := (len(m.tiles) + cols - 1) / cols
	img := renderMosaicImage(m.tiles, cols, rows, tileSz)
	return widget.Image{Src: paint.NewImageOp(img), Fit: widget.Unscaled}.Layout(gtx)
}

// renderMosaicImage 把瓦片光栅化为 NRGBA 图像。
//
// 颜色：完成→绿色；部分→蓝色；空白→浅灰。
func renderMosaicImage(tiles []tileRect, cols, rows, tilePx int) image.Image {
	w := cols * tilePx
	h := rows * tilePx
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i, t := range tiles {
		var c color.NRGBA
		switch {
		case t.covered >= 1:
			c = color.NRGBA{R: 0x4c, G: 0xaf, B: 0x50, A: 0xff}
		case t.covered > 0:
			c = color.NRGBA{R: 0x21, G: 0x96, B: 0xf3, A: 0xff}
		default:
			c = color.NRGBA{R: 0xe0, G: 0xe0, B: 0xe0, A: 0xff}
		}
		col := i % cols
		row := i / cols
		x0 := col * tilePx
		y0 := row * tilePx
		for y := 0; y < tilePx && y0+y < h; y++ {
			for x := 0; x < tilePx && x0+x < w; x++ {
				off := (y0+y)*img.Stride + (x0+x)*4
				img.Pix[off+0] = c.R
				img.Pix[off+1] = c.G
				img.Pix[off+2] = c.B
				img.Pix[off+3] = c.A
			}
		}
	}
	return img
}
