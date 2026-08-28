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

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// TestMosaicUpdatesViaSchedulerEventBus exercises the live update path:
// a real scheduler with a real engine downloads a file, the
// chunk-details mosaic subscribes to events, and we verify that tiles
// turn green as chunks complete.
func TestMosaicUpdatesViaSchedulerEventBus(t *testing.T) {
	const size = 4 * 1024 * 1024
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			var start, end int64
			if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
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

	m := newChunkMosaic(tk.ID, sc)
	m.resizeForTotal(int64(size))

	ch, unsub := sc.Subscribe()
	defer unsub()
	go func() {
		for ev := range ch {
			if ev.Task == nil || ev.Task.ID != tk.ID {
				continue
			}
			fyne.Do(func() {
				m.update(ev.Task.ChunkProgress, ev.Task.ChunkRanges, ev.Task.TotalSize)
			})
		}
	}()

	m.update(tk.ChunkProgress, tk.ChunkRanges, tk.TotalSize)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := st.GetTask(tk.ID)
		if cur != nil && cur.Status == store.StatusCompleted {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)

	if m.blocks != 4 {
		t.Fatalf("expected 4 blocks, got %d", m.blocks)
	}
	for i, tile := range m.tiles {
		if tile.FillColor != theme.Color(theme.ColorNameSuccess) {
			t.Errorf("tile %d fill = %v, want Success", i, tile.FillColor)
		}
	}
}