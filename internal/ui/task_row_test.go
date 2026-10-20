// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"sync"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/bittorrent"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// rowActionSvc 记录任务行上触发过的动作调用。
type rowActionSvc struct {
	mu      sync.Mutex
	deleted []string
}

func (s *rowActionSvc) List(store.StatusFilter, store.TaskSort) ([]*store.Task, error) {
	return nil, nil
}
func (s *rowActionSvc) Subscribe() (<-chan scheduler.Event, func()) {
	return make(chan scheduler.Event), func() {}
}
func (s *rowActionSvc) AddTask(AddTaskInput) (*store.Task, error) { return nil, nil }
func (s *rowActionSvc) Start(string) error                        { return nil }
func (s *rowActionSvc) Pause(string) error                        { return nil }
func (s *rowActionSvc) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, id)
	return nil
}
func (s *rowActionSvc) IsPreparing(string) bool     { return false }
func (s *rowActionSvc) Probe(string) (int64, error) { return 0, nil }
func (s *rowActionSvc) BTTaskDetail(string) (*bittorrent.Detail, error) {
	return nil, nil
}
func (s *rowActionSvc) Settings() store.Settings          { return store.Settings{} }
func (s *rowActionSvc) SaveSettings(store.Settings) error { return nil }
func (s *rowActionSvc) Close() error                      { return nil }

func (s *rowActionSvc) deleteCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.deleted...)
}

// stubConfirm 替换掉确认对话框:confirmed 决定是否执行回调,模拟用户
// 点「删除」还是点「取消」。
func stubConfirm(t *testing.T, confirmed bool, shown *bool) {
	t.Helper()
	orig := showConfirmDialog
	showConfirmDialog = func(parent fyne.Window, title, message, confirmText string, onConfirm func()) {
		if shown != nil {
			*shown = true
		}
		if confirmed && onConfirm != nil {
			onConfirm()
		}
	}
	t.Cleanup(func() { showConfirmDialog = orig })
}

// TestDelete_AsksBeforeRemoving 回归测试。
//
// 删除任务会连同已下载的 .part 数据一起删掉(scheduler.Delete →
// removeTaskData),之前点一下删除按钮就直接删,零确认——手滑一下,
// 几个 GB 的进度就没了。这里锁定:点删除只弹确认框,真正删除必须等
// 用户点头。
func TestDelete_AsksBeforeRemoving(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	old := globalWin
	defer func() { globalWin = old }()
	w := test.NewWindow(widget.NewLabel("main"))
	globalWin = w
	defer w.Close()

	svc := &rowActionSvc{}
	tk := &store.Task{ID: "t1", URL: "https://example.com/a.bin", SavePath: "/tmp/a.bin"}
	row := newTaskRow(tk)
	row.bind(tk, svc)

	// 用户还没确认:不许删。
	shown := false
	stubConfirm(t, false, &shown)
	row.cancelBtn.OnTapped()
	if !shown {
		t.Fatal("no confirmation dialog was shown before deleting")
	}
	if got := svc.deleteCalls(); len(got) != 0 {
		t.Fatalf("task deleted without confirmation: %v", got)
	}

	// 用户确认了:这时才删。
	stubConfirm(t, true, nil)
	row.cancelBtn.OnTapped()
	got := svc.deleteCalls()
	if len(got) != 1 || got[0] != "t1" {
		t.Fatalf("delete calls = %v, want [t1]", got)
	}
}

// TestDelete_CancelKeepsTask 确认框点「取消」时任务必须还在。
func TestDelete_CancelKeepsTask(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	old := globalWin
	defer func() { globalWin = old }()
	w := test.NewWindow(widget.NewLabel("main"))
	globalWin = w
	defer w.Close()

	svc := &rowActionSvc{}
	tk := &store.Task{ID: "t2", URL: "https://example.com/b.bin", SavePath: "/tmp/b.bin"}
	row := newTaskRow(tk)
	row.bind(tk, svc)

	shown := false
	stubConfirm(t, false, &shown)
	row.cancelBtn.OnTapped()
	if !shown {
		t.Fatal("no confirmation dialog was shown")
	}
	if got := svc.deleteCalls(); len(got) != 0 {
		t.Fatalf("task deleted after cancelling the dialog: %v", got)
	}
}

// TestShowConfirm_NoWindowDoesNothing 防御:没有窗口可挂时不能 panic,
// 也不能执行确认动作——「没法问」的时候直接动手比悄悄删掉任务更糟。
func TestShowConfirm_NoWindowDoesNothing(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	old := globalWin
	defer func() { globalWin = old }()
	globalWin = nil

	called := false
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("showConfirm without any window panicked: %v", r)
		}
	}()
	showConfirm(nil, "title", "message", "ok", func() { called = true })
	if called {
		t.Error("onConfirm ran although no dialog could be shown")
	}
}
