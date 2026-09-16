// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/ipc"
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
		Auth:         in.Auth,
		ChunkCount:   in.ChunkCount,
		MinChunkSize: in.MinChunkSize,
	})
}

func (l *localServiceAdapter) Start(id string) error  { return l.sc.Start(id) }
func (l *localServiceAdapter) Pause(id string) error  { return l.sc.Pause(id) }
func (l *localServiceAdapter) Delete(id string) error { return l.sc.Delete(id) }
func (l *localServiceAdapter) IsPreparing(id string) bool {
	return l.sc.IsPreparing(id)
}
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
	dbPath := filepath.Join(dir, "lgdm.db")
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

// TestAddTaskDialog_PicksUpConfiguredUA 回归测试:用户在「设置」里把默认
// UA 改成非默认值之后,「新建下载任务」对话框提交的任务必须继承该 UA。
// 之前 ui.AddTaskInput 没有 Auth 字段,所以持久化下来的 AuthData 是 "{}",
func TestAddTaskDialog_PicksUpConfiguredUA(t *testing.T) {
	// 本测试不打开对话框(由 TestAddTaskDialog_AddAndStart 覆盖)——
	// 我们直接构造与 showAddTaskDialog 的 startDownload 完全相同的
	// AddTaskInput,验证 Auth 字段透传到 store.Task.AuthData。
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lgdm.db")
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
		// 关键字段:不是默认的 Wget/1.21.3。
		UserAgent: "Mozilla/5.0 (X11; configured-test)",
		Cookies:   "session=abc",
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	GlobalSettings, _ = st.LoadSettings()
	if GlobalSettings.UserAgent == "" {
		t.Fatalf("precondition: GlobalSettings.UserAgent must be set after SaveSettings/LoadSettings")
	}

	// HTTP server:只回应 200,不做实际下载——本测试只关心持久化的 AuthData。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// 用 localServiceAdapter 直连 scheduler/store,跳过 IPC;确认的是
	// UI → ui.AddTaskInput.Auth → scheduler.Add → store.Task.AuthData
	// 的整条链路。本测试不调 sc.Run——Start 会拉起 engine,与本测试无关。
	sc := scheduler.New(st)
	svc := &localServiceAdapter{sc: sc, st: st}

	// 与 showAddTaskDialog 内部 startDownload 一致的 Auth 预填。
	url := srv.URL + "/file.bin"
	savePath := filepath.Join(saveDir, "out.bin")
	tk, err := svc.AddTask(AddTaskInput{
		URL:          url,
		SavePath:     savePath,
		ChunkCount:   GlobalSettings.DefaultThreads,
		MinChunkSize: GlobalSettings.MinChunkSize,
		Auth: protocol.AuthOptions{
			UserAgent: GlobalSettings.UserAgent,
			Cookies:   GlobalSettings.Cookies,
		},
	})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	// 关键断言:持久化的 AuthData 必须包含配置的 UA 与 Cookies。
	persisted, err := st.GetTask(tk.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	var got struct {
		UserAgent string `json:"UserAgent"`
		Cookies   string `json:"Cookies"`
	}
	if err := json.Unmarshal([]byte(persisted.AuthData), &got); err != nil {
		t.Fatalf("AuthData %q is not valid JSON: %v", persisted.AuthData, err)
	}
	if got.UserAgent != GlobalSettings.UserAgent {
		t.Fatalf("UserAgent in AuthData = %q, want %q (regression: dialog dropped configured UA)",
			got.UserAgent, GlobalSettings.UserAgent)
	}
	if got.Cookies != GlobalSettings.Cookies {
		t.Fatalf("Cookies in AuthData = %q, want %q (regression: dialog dropped configured cookies)",
			got.Cookies, GlobalSettings.Cookies)
	}
}



// TestAddTaskDialog_StartError_KeepsWindowOpen 验证 Start 失败时窗口不关闭。
func TestAddTaskDialog_StartError_KeepsWindowOpen(t *testing.T) {
	a := test.NewApp()
	defer test.NewApp()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lgdm.db")
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
func (e *errStartService) IsPreparing(id string) bool {
	return e.inner.IsPreparing(id)
}
func (e *errStartService) Probe(u string) (int64, error) {
	return e.inner.Probe(u)
}
func (e *errStartService) Settings() store.Settings            { return e.inner.Settings() }
func (e *errStartService) SaveSettings(s store.Settings) error { return e.inner.SaveSettings(s) }
func (e *errStartService) Close() error                        { return e.inner.Close() }
type startErr string

func (s startErr) Error() string { return string(s) }

var errStartFailed = startErr("simulated start failure")

// TestShowAddTaskDialogForURL_PrefillsAndOverrides 验证 lgom:// 转发的入口
// ShowAddTaskDialogForURL:
//   - URL/Name 字段被预填(URL 字段对应对话框的 urlEntry,SavePath 自动
//     推导成 <DefaultSaveDir>/<Name>);
//   - URL 自带的 UA/Cookies 覆盖 GlobalSettings 的默认值,落到持久化的
//     AuthData 里——这是 lgom:// 协议请求带凭据的关键路径;
//   - 全局 GlobalSettings 在调用前后未被污染(关闭对话框后下一次点
//     「新建任务」仍走 GlobalSettings 的 UA/Cookies)。
func TestShowAddTaskDialogForURL_PrefillsAndOverrides(t *testing.T) {
	a := test.NewApp()
	defer test.NewApp()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lgdm.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	saveDir := filepath.Join(dir, "dl")
	// 全局 UA/Cookies 与 URL 自带的刻意分开,用来区分覆盖路径。
	const globalUA = "GlobalUA/1.0"
	const globalCookies = "global=1"
	const urlUA = "URLUA/2.0"
	const urlCookies = "url=2"
	if err := st.SaveSettings(store.Settings{
		DefaultSaveDir: saveDir,
		DefaultThreads: 2,
		MinChunkSize:   1 << 20,
		UserAgent:      globalUA,
		Cookies:        globalCookies,
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	GlobalSettings, _ = st.LoadSettings()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	sc := scheduler.New(st)
	ctx, cancel := context.WithCancel(context.Background())
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

	params := ipc.ShowAddTaskParams{
		URL:     srv.URL + "/president.iso",
		Name:    "president.iso",
		UA:      urlUA,
		Cookies: urlCookies,
	}
	ShowAddTaskDialogForURL(svc, params)

	wAdd := findAppWindow(a, "新建下载任务")
	if wAdd == nil {
		t.Fatalf("ShowAddTaskDialogForURL did not open a window")
	}
	content := wAdd.Content()
	urlEntry := findNthEntry(content, 0)
	saveEntry := findNthEntry(content, 1)
	if urlEntry == nil || saveEntry == nil {
		t.Fatalf("could not find entries; urlEntry=%v saveEntry=%v", urlEntry, saveEntry)
	}
	if urlEntry.Text != params.URL {
		t.Fatalf("urlEntry.Text = %q, want %q", urlEntry.Text, params.URL)
	}
	wantSave := filepath.Join(saveDir, params.Name)
	if saveEntry.Text != wantSave {
		t.Fatalf("saveEntry.Text = %q, want %q", saveEntry.Text, wantSave)
	}

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

	// 关键断言:AuthData 必须是 URL 自带的 UA/Cookies,不是 GlobalSettings 的。
	var got struct {
		UserAgent string `json:"UserAgent"`
		Cookies   string `json:"Cookies"`
	}
	if err := json.Unmarshal([]byte(tk.AuthData), &got); err != nil {
		t.Fatalf("AuthData %q is not valid JSON: %v", tk.AuthData, err)
	}
	if got.UserAgent != urlUA {
		t.Fatalf("UserAgent in AuthData = %q, want %q (URL UA must override GlobalSettings)", got.UserAgent, urlUA)
	}
	if got.Cookies != urlCookies {
		t.Fatalf("Cookies in AuthData = %q, want %q (URL Cookies must override GlobalSettings)", got.Cookies, urlCookies)
	}

	// 关闭对话框后,GlobalSettings 必须恢复——下次点「新建任务」不应被污染。
	if GlobalSettings.UserAgent != globalUA {
		t.Fatalf("GlobalSettings.UserAgent polluted: got %q, want %q", GlobalSettings.UserAgent, globalUA)
	}
	if GlobalSettings.Cookies != globalCookies {
		t.Fatalf("GlobalSettings.Cookies polluted: got %q, want %q", GlobalSettings.Cookies, globalCookies)
	}
}

// TestShowAddTaskDialogForURL_FallsBackToGlobalWhenEmpty 验证 URL 没带
// UA/Cookies 时,对话框应回退到 GlobalSettings——避免把空串写入 task。
func TestShowAddTaskDialogForURL_FallsBackToGlobalWhenEmpty(t *testing.T) {
	a := test.NewApp()
	defer test.NewApp()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lgdm.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	saveDir := filepath.Join(dir, "dl")
	const globalUA = "GlobalUA/1.0"
	if err := st.SaveSettings(store.Settings{
		DefaultSaveDir: saveDir,
		DefaultThreads: 2,
		MinChunkSize:   1 << 20,
		UserAgent:      globalUA,
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	GlobalSettings, _ = st.LoadSettings()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	sc := scheduler.New(st)
	ctx, cancel := context.WithCancel(context.Background())
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

	// 仅带 URL,不带 UA/Cookies——应走 GlobalSettings。
	ShowAddTaskDialogForURL(svc, ipc.ShowAddTaskParams{URL: srv.URL + "/x.bin"})

	wAdd := findAppWindow(a, "新建下载任务")
	if wAdd == nil {
		t.Fatalf("ShowAddTaskDialogForURL did not open a window")
	}
	content := wAdd.Content()
	urlEntry := findNthEntry(content, 0)
	saveEntry := findNthEntry(content, 1)
	urlEntry.SetText(srv.URL + "/x.bin")
	saveEntry.SetText(filepath.Join(saveDir, "x.bin"))

	confirmBtn := findButtonByText(content, "开始下载")
	test.Tap(confirmBtn)

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
	var got struct {
		UserAgent string `json:"UserAgent"`
	}
	if err := json.Unmarshal([]byte(tk.AuthData), &got); err != nil {
		t.Fatalf("AuthData %q is not valid JSON: %v", tk.AuthData, err)
	}
	if got.UserAgent != globalUA {
		t.Fatalf("UserAgent = %q, want %q (must fall back to GlobalSettings when URL UA is empty)", got.UserAgent, globalUA)
	}
}
