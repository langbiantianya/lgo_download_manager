package ui

import (
	"context"
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

	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// TestMosaicBarsFillOnDownload runs a real scheduler + engine through a
// download against an httptest server (no Accept-Ranges → streaming
// path). After completion, the engine has collapsed to a single chunk
// covering the whole file, with progress = total bytes. We assert that
// feeding this data into the mosaic's update() leaves every bar at 1.0.
//
// We feed the data in two ways:
//   (a) via the Subscribe goroutine, simulating the production path;
//   (b) directly from the DB after completion, simulating the worst case
//       where the goroutine never delivered.
func TestMosaicBarsFillOnDownload(t *testing.T) {
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

	// scheduler.Start mutates its own tk pointer; re-read from DB so we
	// see the post-Probe TotalSize/ChunkRanges.
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

	// Wait for completion.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := st.GetTask(tk.ID)
		if cur != nil && cur.Status == store.StatusCompleted {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Give the Subscribe goroutine a chance to deliver the final event.
	time.Sleep(1 * time.Second)

	// Independently of whether the goroutine delivered, fetch the
	// post-completion state from DB and update the mosaic directly. This
	// is the same payload the goroutine would have delivered.
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
	for i, bar := range m.bars {
		if v := bar.Value; v < 0.999 {
			t.Errorf("bar %d Value = %v, want 1.0 (full)", i, v)
		}
	}
}