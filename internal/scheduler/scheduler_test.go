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

// TestSchedulerEndToEnd 走完完整路径:add task、start、分块下载、complete。
// 验证 2s 的批量刷新持久化了最终状态,且目标文件与源 payload 字节级一致。
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

	// 通过事件通道等待完成。
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

	// 必须已经发生过 flush(FlushInterval = 2s;我们已等待超过 2s)。
	persisted, err := st.GetTask(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != store.TaskStatus.Completed {
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

// TestSchedulerResume 验证一个带有部分 chunk_progress 偏移的任务
// 在恢复后可以从中断处继续完成。
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

	// 预填 chunk_progress 以模拟一次部分下载(chunk 0 已全部完成;
	// chunk 1 部分完成)。
	if err := st.UpdateTaskProgress(tk.ID, int64(size)/2, []int64{size / 4, size / 8, 0, 0}, store.TaskStatus.Paused, ""); err != nil {
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
	if persisted.Status != store.TaskStatus.Completed {
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

// TestStartIsAsync 验证 Start 在探测/预分配期间不会阻塞调用方。
// 用一个立即关闭的 HTTP 服务模拟探测失败（连接被拒），Start 必须
// 立即返回 nil；随后任务被标记为 Failed，并通过事件通道发布。
//
// 这是 UI 不卡死的核心保证：探测超时（最长 30s）发生在后台 goroutine。
func TestStartIsAsync(t *testing.T) {
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

	// Add 一个指向不存在端口的任务；探测会失败（连接被拒）。
	tk, err := sc.Add(AddTaskInput{
		URL:        "http://127.0.0.1:1/never.bin", // 端口 1 通常无监听
		SavePath:   filepath.Join(dir, "out.bin"),
		Protocol:   protocol.ProtoHTTP,
		ChunkCount: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Start 必须立即返回 nil —— 不能因探测阻塞。
	startReturned := make(chan time.Duration, 1)
	startStart := time.Now()
	if err := sc.Start(tk.ID); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	startReturned <- time.Since(startStart)

	const startBudget = 500 * time.Millisecond
	select {
	case d := <-startReturned:
		if d > startBudget {
			t.Fatalf("Start blocked for %v, want < %v (probe must be async)", d, startBudget)
		}
	case <-time.After(startBudget):
		t.Fatalf("Start did not return within %v", startBudget)
	}

	// 等待任务变为 Failed。
	ch, unsub := sc.Subscribe()
	defer unsub()
	deadline := time.Now().Add(10 * time.Second)
	failed := false
	for time.Now().Before(deadline) && !failed {
		select {
		case ev := <-ch:
			if ev.Task != nil && ev.Task.ID == tk.ID && ev.Task.Status == store.TaskStatus.Failed {
				failed = true
			}
		case <-time.After(100 * time.Millisecond):
			cur, _ := st.GetTask(tk.ID)
			if cur != nil && cur.Status == store.TaskStatus.Failed {
				failed = true
			}
		}
	}
	if !failed {
		cur, _ := st.GetTask(tk.ID)
		t.Fatalf("task did not reach Failed status; current=%v", cur)
	}

	// 失败时 ErrorMessage 必须非空，UI 才能展示原因。
	persisted, err := st.GetTask(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ErrorMessage == "" {
		t.Error("Failed task has empty ErrorMessage; UI cannot show reason")
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
