// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"context"
	"crypto/rand"
	"fmt"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// TestMosaicTilesTurnGreenOnDownload 在一个不支持 Accept-Ranges 的 httptest
// 服务器上，通过真实的 scheduler + engine 跑一次下载（强制走 engine 的
// 流式回退路径）。完成后，engine 会折叠为覆盖整个文件的单一分块，且
// 进度等于总字节数。我们断言 mosaic 的 update() 会将每块瓦片都绘制
// 为 success 颜色。
//
// 瓦片颜色通过在每次更新时重建 canvas.Rectangle 子节点来设置，
// 从而规避 fyne-io/fyne#3216（在 GridWrap 单个子节点上调用 Refresh
// 可能会被静默丢弃）。
func TestMosaicTilesTurnGreenOnDownload(t *testing.T) {
	const size = 4 * 1024 * 1024
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			var start, end int64
			_, _ = fmt.Sscanf(rng, "bytes=%d-%d", &start, &end)
			if end >= size {
				end = size - 1
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[start : end+1])
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(size))
		w.Write(payload)
	}))
	defer srv.Close()

	test.NewApp()
	defer test.NewApp()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ldm.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	sc := scheduler.New(st)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sc.Run(ctx)

	tk, err := sc.Add(scheduler.AddTaskInput{
		URL:        srv.URL,
		SavePath:   filepath.Join(dir, "out.bin"),
		Protocol:   protocol.ProtoHTTP,
		ChunkCount: 4,
	})
	if err != nil {
		t.Fatalf("scheduler.Add: %v", err)
	}

	if err := sc.Start(tk.ID); err != nil {
		t.Fatalf("scheduler.Start: %v", err)
	}

	if cur, err := st.GetTask(tk.ID); err == nil {
		tk = cur
	}

	m := newChunkMosaic(tk.ID)
	m.resizeForTotal(tk.TotalSize)

	ch, unsub := sc.Subscribe()
	defer unsub()
	go func() {
		for ev := range ch {
			if ev.Task == nil || ev.Task.ID != tk.ID {
				continue
			}
			m.update(ev.Task.ChunkProgress, ev.Task.ChunkRanges, ev.Task.TotalSize)
		}
	}()

	m.update(tk.ChunkProgress, tk.ChunkRanges, tk.TotalSize)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := st.GetTask(tk.ID)
		if cur != nil && cur.Status == store.TaskStatus.Completed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(1 * time.Second)

	cur, _ := st.GetTask(tk.ID)
	if cur == nil {
		t.Fatalf("task vanished after completion")
	}
	fmt.Fprintf(os.Stderr, "post-complete: prog=%v ranges=%v total=%d\n",
		cur.ChunkProgress, cur.ChunkRanges, cur.TotalSize)
	m.update(cur.ChunkProgress, cur.ChunkRanges, cur.TotalSize)

	if m.blocks != 4 {
		t.Fatalf("expected 4 blocks, got %d", m.blocks)
	}
	if got := len(m.wrap.Objects); got != 4 {
		t.Fatalf("expected 4 tiles, got %d", got)
	}
	for i, obj := range m.wrap.Objects {
		rect, ok := obj.(*canvas.Rectangle)
		if !ok {
			t.Fatalf("tile %d is %T, want *canvas.Rectangle", i, obj)
		}
		// 流式下载完全完成后 → 每块瓦片都应是 success（绿色）。
		if rect.FillColor != successColor() {
			t.Errorf("tile %d FillColor = %v, want Success (%v)",
				i, rect.FillColor, successColor())
		}
	}
}

// successColor 解析主题的 success 颜色，以便我们比较瓦片
func successColor() color.Color {
	th := fyne.CurrentApp().Settings().Theme()
	// 的 FillColor（color.Color）与其是否相等。
	return th.Color(theme.ColorNameSuccess, fyne.CurrentApp().Settings().ThemeVariant())
}
