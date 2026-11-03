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

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc"
	"lgo_download_manager/internal/store"
)

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
	// 两种传输都跑一遍：UI 只认 rpc.Client，换线格式不该改变行为。
	for _, transport := range testTransports {
		t.Run(transport, func(t *testing.T) { testAddTaskDialogAddAndStart(t, transport) })
	}
}

func testAddTaskDialogAddAndStart(t *testing.T, transport string) {
	a := test.NewApp()
	defer test.NewApp()

	dir := t.TempDir()
	saveDir := filepath.Join(dir, "dl")
	env := startUIE2E(t, transport, store.Settings{
		DefaultSaveDir: saveDir,
		DefaultThreads: 2,
		MinChunkSize:   1 << 20,
	})
	st := env.store

	// HTTP server：返回完整 1024 字节，并支持 Range 请求，让 Probe 成功。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 1024)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(200)
		_, _ = w.Write(body)
	}))
	svc := env.client
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

// TestAddTaskDialog_RapidDoubleClickAddsOneTask 回归快速双击：
// AddTask/Start 的同步调用尚未返回时，Fyne 仍会排队后续点击/回车；
// 第一次提交必须立刻关门闩并禁用按钮，不能让排队事件再创建同 URL、
// 同保存路径的任务。
func TestAddTaskDialog_RapidDoubleClickAddsOneTask(t *testing.T) {
	a := test.NewApp()
	defer test.NewApp()

	dir := t.TempDir()

	saveDir := filepath.Join(dir, "dl")
	env := startUIE2E(t, rpc.TransportHTTP, store.Settings{
		DefaultSaveDir: saveDir,
		DefaultThreads: 2,
		MinChunkSize:   1 << 20,
	})
	st := env.store

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, 1024))
	}))
	defer srv.Close()

	svc := env.client
	showAddTaskDialog(a.NewWindow("main"), svc)
	wAdd := findAppWindow(a, "新建下载任务")
	if wAdd == nil {
		t.Fatal("showAddTaskDialog did not open a window")
	}
	content := wAdd.Content()
	findNthEntry(content, 0).SetText(srv.URL + "/same.bin")
	findNthEntry(content, 1).SetText(filepath.Join(saveDir, "same.bin"))
	confirmBtn := findButtonByText(content, "开始下载")
	if confirmBtn == nil {
		t.Fatal("could not find 开始下载 button")
	}

	// Fyne 的测试点击是同步调用，与原问题中快速输入/点击产生排队事件
	// 的最终效果一致：第一次提交返回后继续触发同一个按钮。
	for range 6 {
		test.Tap(confirmBtn)
	}
	time.Sleep(200 * time.Millisecond)

	tasks, err := st.ListTasks("", store.SortCreatedDesc)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("rapid submissions created %d tasks, want exactly 1", len(tasks))
	}
	if tasks[0].URL != srv.URL+"/same.bin" || tasks[0].SavePath != filepath.Join(saveDir, "same.bin") {
		t.Fatalf("unexpected task: url=%q savePath=%q", tasks[0].URL, tasks[0].SavePath)
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
	saveDir := filepath.Join(dir, "dl")
	env := startUIE2E(t, rpc.TransportHTTP, store.Settings{
		DefaultSaveDir: saveDir,
		DefaultThreads: 2,
		MinChunkSize:   1 << 20,
		// 关键字段:不是默认的 Wget/1.21.3。
		UserAgent: "Mozilla/5.0 (X11; configured-test)",
		Cookies:   "session=abc",
	})
	st := env.store
	if GlobalSettings.UserAgent == "" {
		t.Fatalf("precondition: GlobalSettings.UserAgent must be set after SaveSettings/LoadSettings")
	}

	// HTTP server:只回应 200,不做实际下载——本测试只关心持久化的 AuthData。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// 走真实链路：UI → api.AddTaskInput.Auth → 传输 → appservice →
	// scheduler.Add → store.Task.AuthData。全局设置从客户端握手快照拿，
	// 与生产里对话框读取 GlobalSettings 的路径一致。
	svc := env.client

	// 与 showAddTaskDialog 内部 startDownload 一致的 Auth 预填。
	url := srv.URL + "/file.bin"
	savePath := filepath.Join(saveDir, "out.bin")
	tk, err := svc.AddTask(api.AddTaskInput{
		URL:          url,
		SavePath:     savePath,
		ChunkCount:   GlobalSettings.DefaultThreads,
		MinChunkSize: GlobalSettings.MinChunkSize,
		Auth: api.AuthOptions{
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
	env := startUIE2E(t, rpc.TransportHTTP, store.Settings{
		DefaultSaveDir: filepath.Join(dir, "dl"),
		DefaultThreads: 2,
		MinChunkSize:   1 << 20,
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	svc := &errStartService{Client: env.client}
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

// errStartService 直接内嵌真实客户端,只把 Start 强制返回错误——其余
// 调用（含 AddTask 与传输）都走生产路径。
type errStartService struct {
	rpc.Client
}

func (e *errStartService) Start(string) error { return errStartFailed }

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
	saveDir := filepath.Join(dir, "dl")
	// 全局 UA/Cookies 与 URL 自带的刻意分开,用来区分覆盖路径。
	const globalUA = "GlobalUA/1.0"
	const globalCookies = "global=1"
	const urlUA = "URLUA/2.0"
	const urlCookies = "url=2"
	env := startUIE2E(t, rpc.TransportHTTP, store.Settings{
		DefaultSaveDir: saveDir,
		DefaultThreads: 2,
		MinChunkSize:   1 << 20,
		UserAgent:      globalUA,
		Cookies:        globalCookies,
	})
	st := env.store

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	svc := env.client

	params := api.ShowAddTaskParams{
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
	saveDir := filepath.Join(dir, "dl")
	const globalUA = "GlobalUA/1.0"
	env := startUIE2E(t, rpc.TransportHTTP, store.Settings{
		DefaultSaveDir: saveDir,
		DefaultThreads: 2,
		MinChunkSize:   1 << 20,
		UserAgent:      globalUA,
	})
	st := env.store

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	svc := env.client

	// 仅带 URL,不带 UA/Cookies——应走 GlobalSettings。
	ShowAddTaskDialogForURL(svc, api.ShowAddTaskParams{URL: srv.URL + "/x.bin"})

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
