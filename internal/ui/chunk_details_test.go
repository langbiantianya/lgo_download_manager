// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"image/color"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

// mosaicCovered 在 m.mu 内取一份当前瓦片覆盖比例快照。
func mosaicCovered(m *chunkMosaic) []float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]float64(nil), m.covered...)
}

// mosaicAllGreen 报告所有瓦片是否都已染成 success（下载完成）。
func mosaicAllGreen(m *chunkMosaic) bool {
	for _, frac := range mosaicCovered(m) {
		if frac < 1 {
			return false
		}
	}
	m.mu.Lock()
	want := m.palette.success
	height := int(m.raster.MinSize().Height)
	m.mu.Unlock()
	if height <= 0 {
		return false
	}
	got := color.NRGBAModel.Convert(m.generate(560, height).At(5, 5)).(color.NRGBA)
	return got == want
}

// mosaicTestInput 构造 4 个等长分块的范围与进度。
func mosaicTestInput(total int64, written int64) (progress, ranges []int64) {
	const chunks = 4
	chunk := total / chunks
	ranges = make([]int64, 0, 2*chunks)
	cur := int64(0)
	for i := range chunks {
		size := chunk
		if i == chunks-1 {
			size = total - cur
		}
		ranges = append(ranges, cur, cur+size-1)
		cur += size
	}
	progress = []int64{written, written, written, written}
	return progress, ranges
}

// TestMosaicUpdateSkipsIdenticalInput 验证与上次渲染完全相同的输入不会
// 再重建瓦片（重建会新建最多 maxTiles 个 canvas.Rectangle）。
func TestMosaicUpdateSkipsIdenticalInput(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	const total = 4 * blockSize
	m := newChunkMosaic("t1")
	m.resizeForTotal(total)

	progress, ranges := mosaicTestInput(total, blockSize/2)
	m.update(progress, ranges, total)

	before := mosaicCovered(m)
	if len(before) != 4 {
		t.Fatalf("瓦片数 = %d, 期望 4", len(before))
	}
	m.mu.Lock()
	appliedAt := m.appliedAt
	m.mu.Unlock()
	m.update(progress, ranges, total)
	m.mu.Lock()
	after := m.appliedAt
	m.mu.Unlock()
	if !after.Equal(appliedAt) {
		t.Fatal("相同输入不应重建瓦片")
	}
}

// TestMosaicThrottleKeepsLatestState 验证节流窗口内被推迟的更新最终
// 一定会补渲染——否则最后一个状态会永久丢失（例如下载刚完成）。
func TestMosaicThrottleKeepsLatestState(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	const total = 4 * blockSize
	m := newChunkMosaic("t1")
	m.resizeForTotal(total)

	empty, ranges := mosaicTestInput(total, 0)
	m.update(empty, ranges, total)

	done, _ := mosaicTestInput(total, blockSize)
	m.update(done, ranges, total)
	if mosaicAllGreen(m) {
		t.Fatalf("节流窗口内的更新不应立即生效")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if mosaicAllGreen(m) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("节流窗口结束后未补渲染最新状态")
}
