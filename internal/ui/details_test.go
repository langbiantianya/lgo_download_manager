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
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// TestMosaicTilesGoGreenOnDownload 在不支持 Range 的 httptest 服务器上跑一次
// 真实下载；engine 流式回退到单分块，进度 = TotalSize。我们断言
// chunkMosaic.update 后每块 tile 的 covered == 1.0。
func TestMosaicTilesGoGreenOnDownload(t *testing.T) {
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

	m := &chunkMosaic{}
	m.resizeForTotal(tk.TotalSize)
	m.update(tk.ChunkProgress, tk.ChunkRanges, tk.TotalSize)

	// 等到完成。
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
	m.update(cur.ChunkProgress, cur.ChunkRanges, cur.TotalSize)

	if m.blocks != 4 {
		t.Fatalf("expected 4 blocks, got %d", m.blocks)
	}
	if got := len(m.tiles); got != 4 {
		t.Fatalf("expected 4 tiles, got %d", got)
	}
	for i, tile := range m.tiles {
		if tile.covered < 1 {
			t.Errorf("tile %d covered = %f, want 1.0", i, tile.covered)
		}
	}
}
