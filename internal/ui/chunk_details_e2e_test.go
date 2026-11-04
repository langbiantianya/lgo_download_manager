// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// 注意：本文件是测试代码，为了构造「有任务、有设置」的初始状态而 import
// 了 internal/store（以及 internal/scheduler / internal/appservice 之一）。
// 这是**仅测试用**的例外：internal/ui 的非测试代码必须只依赖契约层
// （internal/api + internal/rpc + internal/urlkind），不得 import 业务侧包。
// 见 internal/ui/boundary_test.go 与 Task 10 的 G4/G9 说明。

package ui

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc"
	"lgo_download_manager/internal/store"
)

// TestMosaicTilesTurnGreenOnDownload 在一个不支持 Accept-Ranges 的 httptest
// 服务器上，通过真实的 scheduler + engine 跑一次下载（强制走 engine 的
// 流式回退路径）。完成后，engine 会折叠为覆盖整个文件的单一分块，且
// 进度等于总字节数。我们断言 mosaic 的 update() 会将每块瓦片都绘制
// 为 success 颜色。
//
// 整张图由单个 canvas.Raster 绘制，避免为每块瓦片创建 widget。
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
	st := openTestStore(t)
	// 走真实链路：rpc.Client → 传输 → appservice → scheduler/engine。
	env := startTestService(t, st, rpc.TransportHTTP)
	svc := env.client

	tk, err := svc.AddTask(api.AddTaskInput{
		URL:        srv.URL,
		SavePath:   filepath.Join(dir, "out.bin"),
		Protocol:   api.ProtoHTTP,
		ChunkCount: 4,
	})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	if err := svc.Start(tk.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	m := newChunkMosaic(tk.ID)
	m.resizeForTotal(tk.TotalSize)

	ch, unsub := svc.Subscribe()
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
	if got := len(mosaicCovered(m)); got != 4 {
		t.Fatalf("expected 4 tiles, got %d", got)
	}
	if !mosaicAllGreen(m) {
		t.Fatalf("after completion covered = %v, want all green", mosaicCovered(m))
	}
}
