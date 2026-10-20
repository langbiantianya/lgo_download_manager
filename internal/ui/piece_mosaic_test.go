// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"image"
	"image/color"
	"testing"

	"fyne.io/fyne/v2/test"
)

// TestPieceMosaicColorsByCompletion 校验热力图按完成度着色:没开始的格子
// 是背景色,下载完的是成功色,中间的在主色与成功色之间——这三档必须能
// 分开,否则「分片热力图」就退化成一张没意义的图。
func TestPieceMosaicColorsByCompletion(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	m := newPieceMosaic()
	m.update([]uint8{0, 128, 255})

	const w, h = 120, 40
	img := m.generate(w, h)

	pal := m.palette
	cell := pieceMosaicCell
	cols, _, gap := pieceGrid(w, 3, cell)

	wants := []color.NRGBA{
		pal.bg,
		heatColor(pal, 128.0/255.0),
		pal.success,
	}
	for i, want := range wants {
		x := (i%cols)*(cell+gap) + 1
		y := (i/cols)*(cell+gap) + 1
		got := nrgbaAt(t, img, x, y)
		if got != want {
			t.Errorf("tile %d color = %+v, want %+v", i, got, want)
		}
	}
}

// nrgbaAt 取图像上某个像素的 NRGBA 值。
func nrgbaAt(t *testing.T, img image.Image, x, y int) color.NRGBA {
	t.Helper()
	nrgba, ok := img.(*image.NRGBA)
	if !ok {
		t.Fatalf("image is %T, want *image.NRGBA", img)
	}
	return nrgba.NRGBAAt(x, y)
}

// TestPieceMosaicEmptyIsBackground 校验还没有分片数据时热力图是干净的
// 背景色,而不是画出一堆「未下载」的假块。
func TestPieceMosaicEmptyIsBackground(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	m := newPieceMosaic()
	img := m.generate(64, 32)
	bg := m.palette.bg
	for y := 0; y < 32; y++ {
		for x := 0; x < 64; x++ {
			if got := nrgbaAt(t, img, x, y); got != bg {
				t.Fatalf("pixel (%d,%d) = %+v, want the background %+v", x, y, got, bg)
			}
		}
	}
}

// TestPieceMosaicFillsEveryBucket 校验每个分片桶都画出了格子:少画一块
// 就意味着那一段文件的进度被吞掉了。
func TestPieceMosaicFillsEveryBucket(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	const n = 64
	buckets := make([]uint8, n)
	for i := range buckets {
		buckets[i] = uint8(i * 4)
	}
	m := newPieceMosaic()
	m.update(buckets)

	const w, h = 400, 200
	img := m.generate(w, h)

	// 统计画面上有多少种颜色:每种完成度一个颜色,加上背景色。
	seen := map[color.NRGBA]int{}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			seen[nrgbaAt(t, img, x, y)]++
		}
	}
	// 64 个桶里 0 号是背景色,其余 63 个完成度各不相同(加上背景色共 64 种)。
	if len(seen) < n {
		t.Errorf("rendered %d distinct colors, want at least %d", len(seen), n)
	}
}

// TestPieceMosaicKeepsBucketCount 校验更新后桶数与传入的一致,以及
// 面板拿到的确实是快照里的分片数据。
func TestPieceMosaicKeepsBucketCount(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	m := newPieceMosaic()
	buckets := []uint8{1, 2, 3, 4, 5}
	m.update(buckets)
	got, _ := m.snapshot()
	if len(got) != len(buckets) {
		t.Fatalf("buckets = %d, want %d", len(got), len(buckets))
	}
	for i := range buckets {
		if got[i] != buckets[i] {
			t.Errorf("bucket %d = %d, want %d", i, got[i], buckets[i])
		}
	}
}

// TestPieceGridFitsHeight 校验格子会按可用高度收缩,不会画到画布外面
// 去——分片多的时候格子必须变小,而不是溢出。
func TestPieceGridFitsHeight(t *testing.T) {
	const w, h, n = 400, 200, 1024
	cell := pieceMosaicCell
	cols, rows, gap := pieceGrid(w, n, cell)
	for rows*cell-(rows-1)*gap > h && cell > 2 {
		cell--
		cols, rows, gap = pieceGrid(w, n, cell)
	}
	if rows*cell-(rows-1)*gap > h {
		t.Errorf("grid needs %v px but only %d are available", rows*cell-(rows-1)*gap, h)
	}
	if cols < 1 || rows < 1 {
		t.Errorf("grid = %dx%d, want a positive size", cols, rows)
	}
}

// TestPieceMosaicGenerateIsSafeForTinyCanvas 校验画布很小时不会越界:
// 面板刚打开、还没布局完时生成函数可能被叫到很小的尺寸上。
func TestPieceMosaicGenerateIsSafeForTinyCanvas(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	m := newPieceMosaic()
	m.update([]uint8{0, 255, 128, 64})
	for _, size := range []image.Point{{0, 0}, {1, 1}, {3, 2}} {
		img := m.generate(size.X, size.Y)
		if img.Bounds().Dx() != size.X || img.Bounds().Dy() != size.Y {
			t.Errorf("generate(%d,%d) bounds = %v", size.X, size.Y, img.Bounds())
		}
	}
}
