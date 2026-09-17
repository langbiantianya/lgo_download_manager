// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"sync/atomic"
	"testing"

	"fyne.io/fyne/v2/test"

	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// recordingSvc 统计 SaveSettings 调用次数。它实现了 NewMainWindow 构造
// 期间会碰到的全部 Service 方法(List/Subscribe/Settings/SaveSettings),
// 其余方法只在用户交互时才走到,测试不触发。
type recordingSvc struct {
	saved atomic.Int32
}

func (r *recordingSvc) List(store.StatusFilter, store.TaskSort) ([]*store.Task, error) {
	return nil, nil
}
func (r *recordingSvc) Subscribe() (<-chan scheduler.Event, func()) {
	return make(chan scheduler.Event), func() {}
}
func (r *recordingSvc) AddTask(AddTaskInput) (*store.Task, error) { return nil, nil }
func (r *recordingSvc) Start(string) error                        { return nil }
func (r *recordingSvc) Pause(string) error                        { return nil }
func (r *recordingSvc) Delete(string) error                       { return nil }
func (r *recordingSvc) IsPreparing(string) bool                   { return false }
func (r *recordingSvc) Probe(string) (int64, error)               { return 0, nil }
func (r *recordingSvc) Settings() store.Settings                  { return store.Settings{} }
func (r *recordingSvc) SaveSettings(_ store.Settings) error {
	r.saved.Add(1)
	return nil
}
func (r *recordingSvc) Close() error { return nil }

// TestSettingsPersist_SuppressBlocks 回归测试。
//
// 切语言后 applyLanguage 重建设置页期间,新构造的 widget(SetSelected /
// SetText)的 OnChanged 会同步落到 persist,persist 调 svc.SaveSettings
// 会再次让业务侧推 MsgLanguage,形成「SaveSettings → SetLanguage →
// MsgLanguage → applyLanguage → 重建设置页 → 再次 SaveSettings」无限
// 循环,最终 UI 进程 panic 闪退。修复:在重建窗口内 settingsPersist 收到
// suppressPersist()==true 时直接 return。
func TestSettingsPersist_SuppressBlocks(t *testing.T) {
	svc := &recordingSvc{}
	GlobalSettings = store.Settings{DefaultSaveDir: "/tmp"}
	defer func() { GlobalSettings = store.Settings{} }()

	var onChangeCalls atomic.Int32
	// 抑制态:SaveSettings 与 onChange 都必须 no-op。
	settingsPersist(svc, func() { onChangeCalls.Add(1) }, func() bool { return true })
	if got := svc.saved.Load(); got != 0 {
		t.Fatalf("SaveSettings called %d times while suppressed (would loop)", got)
	}
	if got := onChangeCalls.Load(); got != 0 {
		t.Fatalf("onChange called %d times while suppressed", got)
	}
}

// TestSettingsPersist_NilSuppressAllowsWrites 边界:suppressPersist 为 nil
// 时 persist 必须正常落盘——suppress 不能成为永远屏蔽的副作用。
func TestSettingsPersist_NilSuppressAllowsWrites(t *testing.T) {
	svc := &recordingSvc{}
	GlobalSettings = store.Settings{DefaultSaveDir: "/tmp"}
	defer func() { GlobalSettings = store.Settings{} }()

	settingsPersist(svc, nil, nil)
	if got := svc.saved.Load(); got != 1 {
		t.Fatalf("SaveSettings called %d times with nil suppress, want 1", got)
	}

	// 抑制态 = no-op。
	settingsPersist(svc, nil, func() bool { return true })
	if got := svc.saved.Load(); got != 1 {
		t.Fatalf("SaveSettings called %d times after setting suppress, want still 1", got)
	}

	// 解除抑制后再调一次,计数到 2。
	settingsPersist(svc, nil, func() bool { return false })
	if got := svc.saved.Load(); got != 2 {
		t.Fatalf("SaveSettings called %d times after clearing suppress, want 2", got)
	}
}

// TestSettingsPersist_NilSvcIsSafe 防御:svc=nil 时不能 panic,
// 真实路径里 dialog 关闭前的最后一帧 OnChanged 可能走到这里。
func TestSettingsPersist_NilSvcIsSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("settingsPersist panicked with nil svc: %v", r)
		}
	}()
	settingsPersist(nil, nil, nil) // must not panic
}

// TestApplyLanguage_SuppressesSettingsPersistDuringRebuild 端到端回归。
//
// 复现切语言导致 UI 闪退的循环:用户切语言后业务侧推 MsgLanguage,
// UI 侧 applyLanguage 重建设置页;重建产生的 SetSelected/SetText 会
// 触发 OnChanged → persist → SaveSettings,业务侧 dispatch 再推
// MsgLanguage,UI 再次重建(日志里 6 次「save settings: connection
// closed」即这条循环叠加 UI 进程崩溃的副作用)。
//
// 断言分两段:
//  1. 裸调 showSettingsPage(无抑制)会触发 ≥1 次 SaveSettings——证明
//     重建设置页确实会通过 SetSelected/SetText 打到 persist;
//  2. applyLanguage 走同一重建路径但全程抑制,必须 0 次。
func TestApplyLanguage_SuppressesSettingsPersistDuringRebuild(t *testing.T) {
	a := test.NewApp()
	defer test.NewApp()

	GlobalSettings = store.Settings{DefaultSaveDir: "/tmp"}
	defer func() { GlobalSettings = store.Settings{} }()

	// 基准:不经过 applyLanguage,直接 showSettingsPage → 无抑制,
	// 应当观察到重建设置页触发的 SaveSettings。
	base := &recordingSvc{}
	baseWin := NewMainWindow(a, base)
	before := base.saved.Load()
	baseWin.showSettingsPage()
	if got := base.saved.Load() - before; got == 0 {
		t.Fatalf("baseline: rebuilding settings page triggered 0 SaveSettings; guard test would be vacuous")
	}

	// 修复路径:applyLanguage 在重建窗口内抑制 persist,同一重建必须 0 次。
	guarded := &recordingSvc{}
	guardedWin := NewMainWindow(a, guarded)
	guardedWin.settingsPageOpen = true
	guardedBefore := guarded.saved.Load()
	guardedWin.applyLanguage()
	if got := guarded.saved.Load() - guardedBefore; got != 0 {
		t.Fatalf("applyLanguage rebuild caused %d SaveSettings calls (loop regression)", got)
	}
}
