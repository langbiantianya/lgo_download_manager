// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// localServiceAdapter 直接把 scheduler.Store 包成 Service——跳过 IPC，
// 便于在 Fyne test driver 下端到端验证 showAddTaskDialog 的行为。
type localServiceAdapter struct {
	sc *scheduler.Scheduler
	st *store.Store
}

func (l *localServiceAdapter) List(filter store.StatusFilter, sort store.TaskSort) ([]*store.Task, error) {
	return l.st.ListTasks(filter, sort)
}

func (l *localServiceAdapter) Subscribe() (<-chan scheduler.Event, func()) {
	return l.sc.Subscribe()
}

func (l *localServiceAdapter) AddTask(in AddTaskInput) (*store.Task, error) {
	return l.sc.Add(scheduler.AddTaskInput{
		URL:          in.URL,
		SavePath:     in.SavePath,
		Protocol:     protocol.ProtoHTTP,
		ChunkCount:   in.ChunkCount,
		MinChunkSize: in.MinChunkSize,
	})
}

func (l *localServiceAdapter) Start(id string) error  { return l.sc.Start(id) }
func (l *localServiceAdapter) Pause(id string) error  { return l.sc.Pause(id) }
func (l *localServiceAdapter) Delete(id string) error { return l.sc.Delete(id) }
func (l *localServiceAdapter) Probe(_ string) (int64, error) {
	return 0, nil
}
func (l *localServiceAdapter) Settings() store.Settings {
	s, _ := l.st.LoadSettings()
	return s
}
func (l *localServiceAdapter) SaveSettings(s store.Settings) error {
	return l.st.SaveSettings(s)
}
func (l *localServiceAdapter) Close() error { return nil }

// findAppWindow 在所有窗口中按标题子串查找。
func findAppWindow(a fyne.App, titleSubstr string) fyne.Window {
	for _, w := range a.Driver().AllWindows() {
		if strings.Contains(w.Title(), titleSubstr) {
			return w
		}
	}
	return nil
}

// findNthEntry 在 widget 树中按 DFS 顺序找第 n 个 Entry。
func findNthEntry(root fyne.CanvasObject, n int) *widget.Entry {
	count := 0
	var found *widget.Entry
	walk(root, func(o fyne.CanvasObject) {
		if found != nil {
			return
		}
		if e, ok := o.(*widget.Entry); ok {
			if count == n {
				found = e
			}
			count++
		}
	})
	return found
}

// walk 深度优先遍历所有 CanvasObject,调用 fn。
func walk(o fyne.CanvasObject, fn func(fyne.CanvasObject)) {
	fn(o)
	if c, ok := o.(*fyne.Container); ok {
		for _, child := range c.Objects {
			walk(child, fn)
		}
	}
}

// findButtonByText 在 widget 树中按文字找 Button。
func findButtonByText(root fyne.CanvasObject, txt string) *widget.Button {
	var found *widget.Button
	walk(root, func(o fyne.CanvasObject) {
		if found != nil {
			return
		}
		if b, ok := o.(*widget.Button); ok && b.Text == txt {
			found = b
		}
	})
	return found
}

// TestAddTaskDialog_AddAndStart 端到端验证：弹出对话框 → 填字段 → 点击
// 「开始下载」→ 任务被加入且状态离开 Pending。
func TestAddTaskDialog_AddAndStart(t *testing.T) {
	a := test.NewApp()
	defer test.NewApp()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ldm.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	saveDir := filepath.Join(dir, "dl")
	if err := st.SaveSettings(store.Settings{
		DefaultSaveDir: saveDir,
		DefaultThreads: 2,
		MinChunkSize:   1 << 20,
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	GlobalSettings, _ = st.LoadSettings()

	// HTTP server：返回完整 1024 字节，并支持 Range 请求，让 Probe 成功。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 1024)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(200)
		_, _ = w.Write(body)
	}))
	// 等待 scheduler.Run 退出后再关 store:flushAll 在 ctx.Done 上仍会
	ctx, cancel := context.WithCancel(context.Background())
	sc := scheduler.New(st)
	schedulerDone := make(chan struct{})
	defer func() {
		cancel()
		<-schedulerDone
	}()
	go func() {
		sc.Run(ctx)
		close(schedulerDone)
	}()

	svc := &localServiceAdapter{sc: sc, st: st}
	parent := a.NewWindow("main")
	showAddTaskDialog(parent, svc)

	wAdd := findAppWindow(a, "新建下载任务")
	if wAdd == nil {
		t.Fatalf("showAddTaskDialog did not open a window")
	}

	content := wAdd.Content()
	urlEntry := findNthEntry(content, 0)
	saveEntry := findNthEntry(content, 1)
	if urlEntry == nil || saveEntry == nil {
		t.Fatalf("could not find entries; urlEntry=%v saveEntry=%v", urlEntry, saveEntry)
	}
	urlEntry.SetText(srv.URL)
	saveEntry.SetText(filepath.Join(saveDir, "out.bin"))

	confirmBtn := findButtonByText(content, "开始下载")
	if confirmBtn == nil {
		t.Fatalf("could not find 开始下载 button")
	}
	test.Tap(confirmBtn)

	// 等待任务被加入 store。
	deadline := time.Now().Add(3 * time.Second)
	var tk *store.Task
	for time.Now().Before(deadline) {
		tasks, _ := st.ListTasks("", store.SortCreatedDesc)
		if len(tasks) == 1 {
			tk = tasks[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if tk == nil {
		t.Fatalf("task was not added")
	}
	// 关键断言:状态必须是 Downloading 或 Completed——证明 Start 走通且
	// engine 实际启动了下载(1 KB 测试文件可能瞬间跑完)。
	deadline = time.Now().Add(3 * time.Second)
	started := false
	var lastErr string
	for time.Now().Before(deadline) {
		cur, _ := st.GetTask(tk.ID)
		if cur.Status == store.TaskStatus.Downloading || cur.Status == store.TaskStatus.Completed {
			started = true
			break
		}
		if cur.Status == store.TaskStatus.Failed {
			lastErr = cur.ErrorMessage
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !started {
		cur, _ := st.GetTask(tk.ID)
		t.Fatalf("task in %q after click; Start was not invoked or engine did not start; errMsg=%q",
			cur.Status, lastErr)
	}
}

// TestAddTaskDialog_StartError_KeepsWindowOpen 验证 Start 失败时窗口不关闭。
func TestAddTaskDialog_StartError_KeepsWindowOpen(t *testing.T) {
	a := test.NewApp()
	defer test.NewApp()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ldm.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	if err := st.SaveSettings(store.Settings{
		DefaultSaveDir: filepath.Join(dir, "dl"),
		DefaultThreads: 2,
		MinChunkSize:   1 << 20,
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	GlobalSettings, _ = st.LoadSettings()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	// 等待 scheduler.Run 退出后再关 store,避免 flushAll 写已关闭的 DB。
	sc := scheduler.New(st)
	schedulerDone := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		<-schedulerDone
	}()
	go func() {
		sc.Run(ctx)
		close(schedulerDone)
	}()

	svc := &errStartService{inner: &localServiceAdapter{sc: sc, st: st}}
	parent := a.NewWindow("main")
	showAddTaskDialog(parent, svc)

	wAdd := findAppWindow(a, "新建下载任务")
	if wAdd == nil {
		t.Fatalf("showAddTaskDialog did not open a window")
	}

	content := wAdd.Content()
	urlEntry := findNthEntry(content, 0)
	saveEntry := findNthEntry(content, 1)
	urlEntry.SetText(srv.URL)
	saveEntry.SetText(filepath.Join(dir, "out.bin"))

	confirmBtn := findButtonByText(content, "开始下载")
	test.Tap(confirmBtn)

	time.Sleep(200 * time.Millisecond)

	// Start 失败时窗口必须仍存在。
	if findAppWindow(a, "新建下载任务") == nil {
		t.Fatalf("window closed unexpectedly after Start error")
	}
}

// errStartService 包了一层 inner Service,把 Start 强制返回错误。
type errStartService struct {
	inner Service
}

func (e *errStartService) List(f store.StatusFilter, s store.TaskSort) ([]*store.Task, error) {
	return e.inner.List(f, s)
}
func (e *errStartService) Subscribe() (<-chan scheduler.Event, func()) {
	return e.inner.Subscribe()
}
func (e *errStartService) AddTask(in AddTaskInput) (*store.Task, error) {
	return e.inner.AddTask(in)
}
func (e *errStartService) Start(_ string) error   { return errStartFailed }
func (e *errStartService) Pause(id string) error  { return e.inner.Pause(id) }
func (e *errStartService) Delete(id string) error { return e.inner.Delete(id) }
func (e *errStartService) Probe(u string) (int64, error) {
	return e.inner.Probe(u)
}
func (e *errStartService) Settings() store.Settings            { return e.inner.Settings() }
func (e *errStartService) SaveSettings(s store.Settings) error { return e.inner.SaveSettings(s) }
func (e *errStartService) Close() error                        { return e.inner.Close() }

type startErr string

func (s startErr) Error() string { return string(s) }

var errStartFailed = startErr("simulated start failure")
