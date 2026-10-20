// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"path/filepath"
	"sync"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// detailsStubService 是 Service 的最小实现:订阅返回一个关得掉的
// channel,窗口关闭时订阅方退出,不留挂起的 goroutine。
type detailsStubService struct {
	btStubService
	mu sync.Mutex
}

func (s *detailsStubService) Subscribe() (<-chan scheduler.Event, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan scheduler.Event)
	once := &sync.Once{}
	return ch, func() { once.Do(func() { close(ch) }) }
}

// detailsWindowOf 取出某任务登记着的详情窗口。
func detailsWindowOf(taskID string) fyne.Window {
	detailsWindows.mu.Lock()
	defer detailsWindows.mu.Unlock()
	return detailsWindows.m[taskID]
}

// closeDetailsWindow 关掉某任务登记着的详情窗口。
func closeDetailsWindow(t *testing.T, taskID string) {
	t.Helper()
	w := detailsWindowOf(taskID)
	if w == nil {
		t.Fatalf("no details window registered for %s", taskID)
	}
	w.Close()
}

// clearDetailsWindow 清理某任务可能残留的详情窗口(前一个用例忘了关时
// 也不至于让后面的用例误判成「窗口已存在」)。
func clearDetailsWindow(t *testing.T, taskID string) {
	t.Helper()
	detailsWindows.mu.Lock()
	w := detailsWindows.m[taskID]
	delete(detailsWindows.m, taskID)
	detailsWindows.mu.Unlock()
	if w != nil {
		w.Close()
	}
}

// TestDetailsWindowIsSingletonPerTask 校验同一个任务只开一个详情窗口:
// 连点两次「详情」不会多出第二个窗口,而是复用已经开着的那一个。
func TestDetailsWindowIsSingletonPerTask(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	tk := &store.Task{
		ID:       "http-singleton",
		URL:      "https://example.com/a.zip",
		SavePath: filepath.Join("C:", "dl", "a.zip"),
		Protocol: "HTTPS",
	}

	base := len(fyne.CurrentApp().Driver().AllWindows())
	clearDetailsWindow(t, tk.ID)
	showChunkDetails(tk, &detailsStubService{}, nil)
	opened := len(fyne.CurrentApp().Driver().AllWindows())
	if opened != base+1 {
		t.Fatalf("windows = %d, want %d (one new details window)", opened, base+1)
	}
	first := detailsWindowOf(tk.ID)
	if first == nil {
		t.Fatal("the details window was not registered")
	}

	// 再点一次:窗口数不变,登记的也还是原来那一个。
	showChunkDetails(tk, &detailsStubService{}, nil)
	if got := len(fyne.CurrentApp().Driver().AllWindows()); got != opened {
		t.Errorf("windows after the second tap = %d, want %d (the open window is reused)", got, opened)
	}
	if second := detailsWindowOf(tk.ID); second != first {
		t.Error("the second tap opened a new details window instead of reusing the open one")
	}

	// 关掉之后能再开一个新的。
	closeDetailsWindow(t, tk.ID)
	if got := len(fyne.CurrentApp().Driver().AllWindows()); got != base {
		t.Errorf("windows after close = %d, want %d", got, base)
	}
	if detailsWindowOf(tk.ID) != nil {
		t.Error("the closed details window was not unregistered")
	}
	showChunkDetails(tk, &detailsStubService{}, nil)
	if got := len(fyne.CurrentApp().Driver().AllWindows()); got != base+1 {
		t.Errorf("windows after reopening = %d, want %d", got, base+1)
	}
	closeDetailsWindow(t, tk.ID)
}

// TestDetailsWindowPerTaskIsIndependent 校验限制是「每个任务一个」而不是
// 「全局只能开一个」:不同任务各自有自己的详情窗口。
func TestDetailsWindowPerTaskIsIndependent(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	mk := func(id string) *store.Task {
		return &store.Task{
			ID:       id,
			URL:      "https://example.com/" + id + ".zip",
			SavePath: filepath.Join("C:", "dl", id+".zip"),
			Protocol: "HTTPS",
		}
	}

	base := len(fyne.CurrentApp().Driver().AllWindows())
	clearDetailsWindow(t, "http-singleton")
	clearDetailsWindow(t, "http-other")
	showChunkDetails(mk("http-singleton"), &detailsStubService{}, nil)
	showChunkDetails(mk("http-other"), &detailsStubService{}, nil)
	if got := len(fyne.CurrentApp().Driver().AllWindows()); got != base+2 {
		t.Errorf("windows = %d, want %d (one per task)", got, base+2)
	}
	if detailsWindowOf("http-singleton") == nil || detailsWindowOf("http-other") == nil {
		t.Error("both tasks should have their own details window")
	}
	closeDetailsWindow(t, "http-singleton")
	closeDetailsWindow(t, "http-other")
}

// TestBTDetailsWindowIsSingletonPerTask 校验 BT 任务同样只开一个详情
// 窗口——BT 面板每秒轮询一次,多开一个窗口既重复显示又白费一次 IPC。
func TestBTDetailsWindowIsSingletonPerTask(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	tk := &store.Task{
		ID:       "bt-singleton",
		URL:      "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		SavePath: filepath.Join("C:", "dl", "bt"),
		Protocol: "BT",
	}

	base := len(fyne.CurrentApp().Driver().AllWindows())
	clearDetailsWindow(t, tk.ID)
	if p := showChunkDetails(tk, &btStubService{detail: sampleBTDetail()}, nil); p == nil {
		t.Fatal("a BT task must get the BT details panel")
	}
	opened := len(fyne.CurrentApp().Driver().AllWindows())
	if opened != base+1 {
		t.Fatalf("windows = %d, want %d (one new details window)", opened, base+1)
	}
	first := detailsWindowOf(tk.ID)

	// 再点一次:不新开窗口,也不新建面板(没有第二份轮询)。
	if p := showChunkDetails(tk, &btStubService{detail: sampleBTDetail()}, nil); p != nil {
		t.Error("the second tap must not build a second BT panel")
	}
	if got := len(fyne.CurrentApp().Driver().AllWindows()); got != opened {
		t.Errorf("windows after the second tap = %d, want %d", got, opened)
	}
	if second := detailsWindowOf(tk.ID); second != first {
		t.Error("the second tap opened a new details window instead of reusing the open one")
	}

	closeDetailsWindow(t, tk.ID)
	if detailsWindowOf(tk.ID) != nil {
		t.Error("the closed details window was not unregistered")
	}
}
