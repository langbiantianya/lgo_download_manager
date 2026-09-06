package scheduler

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
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

// TestReclaimDownloadingTasks 模拟上次进程崩溃留下的 Downloading 任务
// (store 中仍是 Downloading,但 engine 已经没在跑了)。
// ReclaimDownloadingTasks 必须把它们回收成 Paused,保留 chunk_progress
// 和 downloaded 字节数,并发出 "paused" 事件供 UI 同步行状态。
func TestReclaimDownloadingTasks(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "ldm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mkTask := func(id string, status store.Status, downloaded int64, chunkProg []int64) {
		tk := &store.Task{
			ID:            id,
			URL:           "http://example/" + id,
			SavePath:      filepath.Join(dir, id+".bin"),
			Protocol:      string(protocol.ProtoHTTP),
			ChunkCount:    4,
			ChunkProgress: chunkProg,
			Status:        status,
			Downloaded:    downloaded,
		}
		if err := st.CreateTask(tk); err != nil {
			t.Fatal(err)
		}
		if status != store.TaskStatus.Pending {
			if err := st.UpdateTaskProgress(id, downloaded, chunkProg, status, ""); err != nil {
				t.Fatal(err)
			}
		}
	}

	// 4 个任务覆盖各状态。Downloading 的那个是关键——模拟崩溃残留。
	mkTask("dl-stuck", store.TaskStatus.Downloading, 4096, []int64{1024, 1024, 1024, 1024})
	mkTask("paused", store.TaskStatus.Paused, 2048, []int64{512, 512, 512, 512})
	mkTask("pending", store.TaskStatus.Pending, 0, []int64{0, 0, 0, 0})
	mkTask("completed", store.TaskStatus.Completed, 8192, []int64{2048, 2048, 2048, 2048})

	sc := New(st)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 订阅事件,确认 ReclaimDownloadingTasks 为每个被回收任务发 "paused"。
	ch, unsub := sc.Subscribe()
	defer unsub()

	if err := sc.ReclaimDownloadingTasks(); err != nil {
		t.Fatalf("ReclaimDownloadingTasks: %v", err)
	}

	// 1) dl-stuck 现在是 Paused,且字节进度完整保留。
	got, err := st.GetTask("dl-stuck")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.TaskStatus.Paused {
		t.Errorf("dl-stuck status = %s, want Paused", got.Status)
	}
	if got.Downloaded != 4096 {
		t.Errorf("dl-stuck downloaded = %d, want 4096 (preserved)", got.Downloaded)
	}
	wantCP := []int64{1024, 1024, 1024, 1024}
	if len(got.ChunkProgress) != len(wantCP) {
		t.Fatalf("dl-stuck chunk_progress len = %d, want %d", len(got.ChunkProgress), len(wantCP))
	}
	for i, v := range wantCP {
		if got.ChunkProgress[i] != v {
			t.Errorf("dl-stuck chunk_progress[%d] = %d, want %d", i, got.ChunkProgress[i], v)
		}
	}

	// 2) 其他状态的任务不应被改动。
	for _, id := range []string{"paused", "pending", "completed"} {
		got, err := st.GetTask(id)
		if err != nil {
			t.Fatal(err)
		}
		switch id {
		case "paused":
			if got.Status != store.TaskStatus.Paused {
				t.Errorf("%s status = %s, want Paused (unchanged)", id, got.Status)
			}
		case "pending":
			if got.Status != store.TaskStatus.Pending {
				t.Errorf("%s status = %s, want Pending (unchanged)", id, got.Status)
			}
		case "completed":
			if got.Status != store.TaskStatus.Completed {
				t.Errorf("%s status = %s, want Completed (unchanged)", id, got.Status)
			}
		}
	}

	// 3) 收到了一条 "paused" 事件,目标是 dl-stuck。
	select {
	case ev := <-ch:
		if ev.Task == nil || ev.Task.ID != "dl-stuck" {
			t.Errorf("event task id = %v, want dl-stuck", ev.Task)
		}
		if ev.Task.Status != store.TaskStatus.Paused {
			t.Errorf("event task status = %s, want Paused", ev.Task.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive paused event within 2s")
	}

	// 4) 幂等:再调一次不会报错也不会留下任何 Downloading。
	if err := sc.ReclaimDownloadingTasks(); err != nil {
		t.Fatalf("second ReclaimDownloadingTasks: %v", err)
	}
	tasks, _ := st.ListTasks(store.FilterDownloading, store.SortCreatedDesc)
	if len(tasks) != 0 {
		t.Errorf("found %d Downloading tasks after idempotent reclaim, want 0", len(tasks))
	}

	_ = ctx
}

// TestPauseAll 验证正常退出路径:scheduler 在下载中途被要求全部暂停,
// 每个正在运行的 job 都会被 cancel 并落盘 Paused,chunk_progress 保留,
// 且 wg.Wait() 在所有 job goroutine 退出后才返回——避免"已 cancel 但
// DB 仍是 Downloading"的窗口。
func TestPauseAll(t *testing.T) {
	// 单 chunk 32 KiB 让 probe 一次性返回;其余字节按字节流式推,每字节
	// 10us,使 engine 在短时间内能持续推进但远跑不完整个文件——
	// 足以让 PauseAll 命中 Downloading+有进度的窗口。
	const size = 8 * 1024 * 1024
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.Itoa(size))
		if rng := r.Header.Get("Range"); rng != "" {
			start, end := parseBytesRange(t, rng, size)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.WriteHeader(http.StatusPartialContent)
			// 慢推：每个 chunk 拉长到 ~100ms，让 PauseAll 有充裕窗口。
			flusher, _ := w.(http.Flusher)
			for i := start; i <= end; i++ {
				if _, err := w.Write(payload[i : i+1]); err != nil {
					return
				}
				if flusher != nil && (i-start)%4096 == 0 {
					flusher.Flush()
				}
				if i%64 == 0 {
					time.Sleep(time.Millisecond)
				}
			}
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write(payload[:4096])
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
		Protocol: protocol.ProtoHTTP, ChunkCount: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sc.Start(tk.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// 等任务进入 Downloading(server 慢推,引擎一启动就会处于 Downloading
	// 一段时间)。要验证 PauseAll 保留下载字节,得继续等到至少有一个
	// progress 事件携带非零 chunk_progress。
	ch, unsub := sc.Subscribe()
	defer unsub()
	deadline := time.Now().Add(10 * time.Second)
	seenProgress := false
	for time.Now().Before(deadline) {
		select {
		case ev := <-ch:
			if ev.Task != nil && ev.Task.ID == tk.ID && ev.Why == "progress" {
				for _, v := range ev.Task.ChunkProgress {
					if v > 0 {
						seenProgress = true
					}
				}
			}
		case <-time.After(5 * time.Millisecond):
		}
		if seenProgress {
			break
		}
	}

	if !seenProgress {
		t.Fatal("did not see any progress event with non-zero chunk_progress before deadline")
	}

	// PauseAll 必须阻塞至 DB 反映 Paused;用 5s 超时——足够正常退出,
	pauseCtx, pauseCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pauseCancel()
	if err := sc.PauseAll(pauseCtx); err != nil {
		t.Fatalf("PauseAll: %v", err)
	}

	cur, err := st.GetTask(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Status != store.TaskStatus.Paused {
		t.Errorf("status after PauseAll = %s, want Paused", cur.Status)
	}
	// chunk_progress 必须保留(下载过的字节不应丢失)。
	var hasProgress bool
	for _, v := range cur.ChunkProgress {
		if v > 0 {
			hasProgress = true
			break
		}
	}
	if !hasProgress {
		t.Errorf("chunk_progress all zero after PauseAll; want partial progress preserved: %v", cur.ChunkProgress)
	}

	// scheduler.jobs 在 PauseAll 返回后应清空。
	sc.mu.Lock()
	jobsLeft := len(sc.jobs)
	sc.mu.Unlock()
	if jobsLeft != 0 {
		t.Errorf("scheduler.jobs has %d entries after PauseAll, want 0", jobsLeft)
	}

	// 再次 PauseAll 是幂等的,不会阻塞或报错。
	if err := sc.PauseAll(pauseCtx); err != nil {
		t.Errorf("second PauseAll: %v", err)
	}
}

// TestPauseAll_Timeout 验证 ctx 取消时 PauseAll 立即返回 ctx.Err(),
// 不再无止境阻塞;残余 Downloading 任务由下次冷启动的
// ReclaimDownloadingTasks 兜底,不在本测试的关注范围内。
func TestPauseAll_Timeout(t *testing.T) {
	// 一个在 ctx 取消前不会退出循环的"挂起 job":模拟 engine 收到 cancel
	// 但 goroutine 因某些原因(如 GC、I/O 卡顿)在 ctx 触发后仍未跑完
	// markStatusFromJob。我们用 sync.WaitGroup 模拟一个故意延迟 wg.Done()
	// 的 per-job goroutine,绕过真实的 engine 路径,只验证 PauseAll 的
	// 超时契约。
	sc := &Scheduler{
		jobs: map[string]*runningJob{},
	}

	released := make(chan struct{})
	sc.wg.Add(1)
	go func() {
		<-released
		sc.wg.Done()
	}()

	// ctx 已超时:即使 wg 还没归零,PauseAll 也必须返回 DeadlineExceeded。
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := sc.PauseAll(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("PauseAll returned nil; want context.DeadlineExceeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("PauseAll err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("PauseAll blocked for %v; want close to ctx deadline (50ms)", elapsed)
	}

	// 释放挂起 goroutine 防止测试 goroutine 泄漏。
	close(released)
}

// TestPauseDuringPrepare 验证「Start 已预留 slot、但 engine 还没接管」
// 的准备阶段中,scheduler.Pause 必须能立即中止任务并把状态落盘为
// Paused,而不是只能等 startAsync 自然跑完探测/预分配。
func TestPauseDuringPrepare(t *testing.T) {
	// 一个故意在 probe 阶段挂住的 server：handler 阻塞直到测试主动
	// 关闭 channel,确保 Start 在探测返回前一直处于准备阶段。
	releaseProbe := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-releaseProbe
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
		Protocol: protocol.ProtoHTTP, ChunkCount: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sc.Start(tk.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// 等到「准备阶段」真的命中：scheduler.jobs 出现 slot,且 IsPreparing 为 true。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sc.IsPreparing(tk.ID) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !sc.IsPreparing(tk.ID) {
		close(releaseProbe)
		t.Fatal("task did not enter prepare phase within deadline")
	}

	// 订阅事件,验证 abortPrepare 会发出 paused 通知。
	ch, unsub := sc.Subscribe()
	defer unsub()

	if err := sc.Pause(tk.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}

	// 必须阻塞到准备阶段退出；2s 足够正常路径。
	pauseCtx, pauseCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pauseCancel()
	if err := sc.PauseAll(pauseCtx); err != nil {
		t.Fatalf("PauseAll: %v", err)
	}

	// 让挂住的 probe handler 收尾,避免 server 协程泄漏。
	close(releaseProbe)

	cur, err := st.GetTask(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Status != store.TaskStatus.Paused {
		t.Errorf("status after prepare-phase Pause = %s, want Paused", cur.Status)
	}

	// scheduler.jobs 必须清空。
	sc.mu.Lock()
	jobsLeft := len(sc.jobs)
	sc.mu.Unlock()
	if jobsLeft != 0 {
		t.Errorf("scheduler.jobs has %d entries, want 0", jobsLeft)
	}

	// 收到至少一条 paused 事件。
	select {
	case ev := <-ch:
		if ev.Task == nil || ev.Task.ID != tk.ID || ev.Why != "paused" {
			t.Errorf("event = %+v, want Why=paused for task %s", ev, tk.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive paused event")
	}
}

// TestPauseAllDuringPrepare 验证「暂停全部」同样能命中准备中的任务，
// 而不是被 wg.Wait() 提前绕过。
func TestPauseAllDuringPrepare(t *testing.T) {
	releaseProbe := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-releaseProbe
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

	var ids []string
	for i := range 3 {
		tk, err := sc.Add(AddTaskInput{
			URL: srv.URL, SavePath: filepath.Join(dir, fmt.Sprintf("out-%d.bin", i)),
			Protocol: protocol.ProtoHTTP, ChunkCount: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := sc.Start(tk.ID); err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
		ids = append(ids, tk.ID)
		// newID 基于毫秒级时间戳,串行添加之间留出 2ms 防止 UNIQUE 冲突。
		time.Sleep(2 * time.Millisecond)
	}

	// 等所有任务都进入 prepare。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ready := true
		for _, id := range ids {
			if !sc.IsPreparing(id) {
				ready = false
				break
			}
		}
		if ready {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, id := range ids {
		if !sc.IsPreparing(id) {
			close(releaseProbe)
			t.Fatalf("task %s not in prepare phase", id)
		}
	}

	pauseCtx, pauseCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer pauseCancel()
	if err := sc.PauseAll(pauseCtx); err != nil {
		t.Fatalf("PauseAll: %v", err)
	}
	close(releaseProbe)

	for _, id := range ids {
		cur, err := st.GetTask(id)
	if err != nil {
			t.Fatal(err)
		}
		if cur.Status != store.TaskStatus.Paused {
			t.Errorf("task %s status = %s, want Paused", id, cur.Status)
		}
	}
	sc.mu.Lock()
	jobsLeft := len(sc.jobs)
	sc.mu.Unlock()
	if jobsLeft != 0 {
		t.Errorf("scheduler.jobs has %d entries, want 0", jobsLeft)
	}
}
