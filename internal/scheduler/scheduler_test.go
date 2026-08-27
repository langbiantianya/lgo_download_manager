package scheduler

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"lgo_download_manager/internal/prealloc"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/store"
)

// TestSchedulerEndToEnd exercises the full path: add task, start, chunked
// download, complete. Verifies the 2s batched flush persisted final state
// and the destination file equals the source payload byte-for-byte.
func TestSchedulerEndToEnd(t *testing.T) {
	const size = 1 * 1024 * 1024 // 1 MiB
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			start, end := parseBytesRange(t, rng, size)
			w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(size, 10))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[start : end+1])
			return
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "ldm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sc := New(st)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sc.Run(ctx)

	tk, err := sc.Add(AddTaskInput{
		URL: srv.URL, SavePath: filepath.Join(dir, "out.bin"),
		Protocol:   protocol.ProtoHTTP,
		ChunkCount: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sc.Start(tk.ID); err != nil {
		t.Fatal(err)
	}

	// Wait for completion via event channel.
	ch, unsub := sc.Subscribe()
	defer unsub()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case ev := <-ch:
			if ev.Task != nil && ev.Task.ID == tk.ID && (ev.Why == "completed" || ev.Why == "failed") {
				goto done
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("did not reach completed in time")
done:

	// Flush must have happened (FlushInterval = 2s; we waited >2s).
	persisted, err := st.GetTask(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != store.StatusCompleted {
		t.Fatalf("status=%s want Completed", persisted.Status)
	}
	if persisted.Downloaded != size {
		t.Fatalf("downloaded=%d want %d", persisted.Downloaded, size)
	}

	got, err := os.ReadFile(tk.SavePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("file mismatch")
	}
}

// TestSchedulerResume verifies a task resumed with partial chunk_progress
// offsets successfully finishes from where it left off.
func TestSchedulerResume(t *testing.T) {
	const size = 512 * 1024
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			start, end := parseBytesRange(t, rng, size)
			w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(size, 10))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[start : end+1])
			return
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "ldm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sc := New(st)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sc.Run(ctx)

	tk, err := sc.Add(AddTaskInput{
		URL: srv.URL, SavePath: filepath.Join(dir, "out.bin"),
		Protocol:   protocol.ProtoHTTP,
		ChunkCount: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Pre-fill chunk_progress to simulate a partial download (chunk 0
	// fully done; chunk 1 partially done).
	if err := st.UpdateTaskProgress(tk.ID, int64(size)/2, []int64{size / 4, size / 8, 0, 0}, store.StatusPaused, ""); err != nil {
		t.Fatal(err)
	}
	dest, err := prealloc.Preallocate(tk.SavePath, size)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dest.WriteAt(payload[:size/4], 0); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.WriteAt(payload[size/4:3*size/8], size/4); err != nil {
		t.Fatal(err)
	}
	dest.Close()

	if err := sc.Start(tk.ID); err != nil {
		t.Fatal(err)
	}

	ch, unsub := sc.Subscribe()
	defer unsub()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case ev := <-ch:
			if ev.Task != nil && ev.Task.ID == tk.ID && (ev.Why == "completed" || ev.Why == "failed") {
				goto doneResume
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("did not reach completed after resume")
doneResume:
	persisted, err := st.GetTask(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != store.StatusCompleted {
		t.Fatalf("status=%s want Completed", persisted.Status)
	}
	got, err := os.ReadFile(tk.SavePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("resumed file mismatch")
	}
}

func parseBytesRange(t *testing.T, rng string, size int64) (int64, int64) {
	t.Helper()
	if !strings.HasPrefix(rng, "bytes=") {
		t.Fatalf("bad range %q", rng)
	}
	body := strings.TrimPrefix(rng, "bytes=")
	dash := strings.IndexByte(body, '-')
	if dash < 0 {
		t.Fatalf("no dash in %q", rng)
	}
	s, err := strconv.ParseInt(body[:dash], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	e, err := strconv.ParseInt(body[dash+1:], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if e >= size {
		e = size - 1
	}
	return s, e
}
