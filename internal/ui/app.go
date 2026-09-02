// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"reflect"
	"sync"

	"gioui.org/app"
	"gioui.org/io/system"

	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// App 是跨窗口共享状态。
type App struct {
	Store *store.Store
	Sched *scheduler.Scheduler

	mu      sync.Mutex
	filter  string
	search  string
	listRev uint64

	mainWin *app.Window

	hookMu   sync.Mutex
	listener []func()
}

// NewApp 构造共享状态。
func NewApp(st *store.Store, sc *scheduler.Scheduler) *App {
	return &App{
		Store:  st,
		Sched:  sc,
		filter: string(store.FilterAll),
	}
}

// SetMainWindow 注册主窗口（由 main_window.go 在 RunMainWindow 中调用）。
func (a *App) SetMainWindow(w *app.Window) {
	a.mu.Lock()
	a.mainWin = w
	a.mu.Unlock()
}

// QuitMainWindow 关闭主窗口（systray 退出时使用）。
func (a *App) QuitMainWindow() {
	a.mu.Lock()
	w := a.mainWin
	a.mu.Unlock()
	if w != nil {
		w.Perform(system.ActionClose)
	}
}

// Filter 返回当前 filter。
func (a *App) Filter() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.filter
}

// SetFilter 写 filter 并通知订阅者。
func (a *App) SetFilter(v string) {
	a.mu.Lock()
	if a.filter == v {
		a.mu.Unlock()
		return
	}
	a.filter = v
	a.listRev++
	a.mu.Unlock()
	a.notifyAll()
}

// Search 返回当前 search 字符串。
func (a *App) Search() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.search
}

// SetSearch 写 search 并通知订阅者。
func (a *App) SetSearch(v string) {
	a.mu.Lock()
	if a.search == v {
		a.mu.Unlock()
		return
	}
	a.search = v
	a.listRev++
	a.mu.Unlock()
	a.notifyAll()
}

// ListRev 返回 list revision 号。
func (a *App) ListRev() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.listRev
}

// Subscribe 注册状态变更回调，返回取消函数。
func (a *App) Subscribe(fn func()) func() {
	a.hookMu.Lock()
	a.listener = append(a.listener, fn)
	a.hookMu.Unlock()
	return func() {
		a.hookMu.Lock()
		defer a.hookMu.Unlock()
		ptr := funcPtr(fn)
		for i, x := range a.listener {
			if funcPtr(x) == ptr {
				a.listener = append(a.listener[:i], a.listener[i+1:]...)
				return
			}
		}
	}
}

// notifyAll 通知所有订阅者刷新。
func (a *App) notifyAll() {
	a.hookMu.Lock()
	subs := append([]func(){}, a.listener...)
	a.hookMu.Unlock()
	for _, fn := range subs {
		fn()
	}
}

// funcPtr 通过 reflect 取 func 的代码指针。
func funcPtr(f func()) uintptr {
	return reflect.ValueOf(f).Pointer()
}
