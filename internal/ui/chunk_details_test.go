// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
)

// mosaicTiles 在 m.mu 内取一份当前瓦片快照：测试与事件派发/补渲染
// goroutine 可能并发访问马赛克。
func mosaicTiles(m *chunkMosaic) []fyne.CanvasObject {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]fyne.CanvasObject(nil), m.wrap.Objects...)
}

// mosaicAllGreen 报告所有瓦片是否都已染成 success（下载完成）。
func mosaicAllGreen(m *chunkMosaic) bool {
	for _, obj := range mosaicTiles(m) {
		rect, ok := obj.(*canvas.Rectangle)
		if !ok {
			return false
		}
		if rect.FillColor != successColor() {
			return false
		}
	}
	return true
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

	before := mosaicTiles(m)
	if len(before) != 4 {
		t.Fatalf("瓦片数 = %d, 期望 4", len(before))
	}
	m.update(progress, ranges, total)
	after := mosaicTiles(m)
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("相同输入不应重建瓦片：第 %d 块发生了变化", i)
		}
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
