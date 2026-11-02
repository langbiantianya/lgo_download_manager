// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"strings"
	"sync/atomic"
	"testing"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/ilocale"
)

// recordingSvc 统计 SaveSettings 调用次数。其余 rpc.Client 方法由
// nopClient 给出无害默认值——NewMainWindow 构造期间只会碰到
// List/Subscribe/Settings，测试也只需要观察 SaveSettings。
type recordingSvc struct {
	nopClient
	saved atomic.Int32
}

func (r *recordingSvc) SaveSettings(_ api.Settings) error {
	r.saved.Add(1)
	return nil
}

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
	GlobalSettings = api.Settings{DefaultSaveDir: "/tmp"}
	defer func() { GlobalSettings = api.Settings{} }()

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
	GlobalSettings = api.Settings{DefaultSaveDir: "/tmp"}
	defer func() { GlobalSettings = api.Settings{} }()

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

// TestBuildSettingsContent_CategorizedTabs 锁定设置页的分类结构:
// 常规、下载、网络、BitTorrent 各自独立成页,避免新增设置项后又
// 把所有控件堆回同一条长表单。
func TestBuildSettingsContent_CategorizedTabs(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	prevLang := ilocale.Current()
	ilocale.Set("en")
	defer ilocale.Set(prevLang)

	GlobalSettings = api.Settings{DefaultSaveDir: "/tmp"}
	defer func() { GlobalSettings = api.Settings{} }()

	content := buildSettingsContent(nil, nil, nil)
	tabs, ok := content.(*container.AppTabs)
	if !ok {
		t.Fatalf("settings content = %T, want *container.AppTabs", content)
	}
	wantTabs := []struct {
		title     string
		firstItem string
	}{
		{"General", ilocale.T("settings.label.language")},
		{"Downloads", ilocale.T("settings.label.saveDir")},
		{"Network", ilocale.T("settings.label.ua")},
		{"BitTorrent", ilocale.T("settings.label.btPort")},
	}
	if len(tabs.Items) != len(wantTabs) {
		t.Fatalf("settings tabs = %d, want %d", len(tabs.Items), len(wantTabs))
	}
	for i, want := range wantTabs {
		if got := tabs.Items[i].Text; got != want.title {
			t.Errorf("tab %d title = %q, want %q", i, got, want.title)
		}
		scroll, ok := tabs.Items[i].Content.(*container.Scroll)
		if !ok {
			t.Fatalf("tab %d content = %T, want *container.Scroll", i, tabs.Items[i].Content)
		}
		form, ok := scroll.Content.(*widget.Form)
		if !ok {
			t.Fatalf("tab %d scroll content = %T, want *widget.Form", i, scroll.Content)
		}
		if len(form.Items) == 0 {
			t.Fatalf("tab %d has no form items", i)
		}
		if got := form.Items[0].Text; got != want.firstItem {
			t.Errorf("tab %d first item = %q, want %q", i, got, want.firstItem)
		}
	}
}

// TestBuildSettingsContent_BTConcurrencyTogether 锁定 BT 并发项的分组与
// 命名:同时下载/同时做种应相邻,且 BT 页签内所有设置项都不再带
// "BT "/"BT-" 前缀。
func TestBuildSettingsContent_BTConcurrencyTogether(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	prevLang := ilocale.Current()
	ilocale.Set("zh-Hans")
	defer ilocale.Set(prevLang)

	GlobalSettings = api.Settings{}
	defer func() { GlobalSettings = api.Settings{} }()

	content := buildSettingsContent(nil, nil, nil)
	tabs := content.(*container.AppTabs)
	scroll := tabs.Items[3].Content.(*container.Scroll)
	form := scroll.Content.(*widget.Form)

	downloadLabel := ilocale.T("settings.label.btMaxConcurrent")
	seedingLabel := ilocale.T("settings.label.btMaxSeeding")
	downloadIdx, seedingIdx := -1, -1
	for i, item := range form.Items {
		switch item.Text {
		case downloadLabel:
			downloadIdx = i
		case seedingLabel:
			seedingIdx = i
		}
	}
	for _, item := range form.Items {
		if strings.HasPrefix(item.Text, "BT ") || strings.HasPrefix(item.Text, "BT-") {
			t.Errorf("BT settings label %q still carries the BT prefix", item.Text)
		}
	}
	if downloadIdx < 0 || seedingIdx != downloadIdx+1 {
		t.Fatalf("download/seeding item indexes = %d/%d, want adjacent", downloadIdx, seedingIdx)
	}
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

	GlobalSettings = api.Settings{DefaultSaveDir: "/tmp"}
	defer func() { GlobalSettings = api.Settings{} }()

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

// TestSaveDirEntryRejectsEmpty 回归测试:下载目录输入框不接受空值。
//
// 空目录会被业务进程当成「没有有效保存位置」——旧版本更糟,直接当成
// 首次运行把整套设置重置成默认值。清空输入框时必须把文本弹回上一个
// 有效值,并且不产生一次落盘。
func TestSaveDirEntryRejectsEmpty(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	GlobalSettings = api.Settings{DefaultSaveDir: "/tmp/dl"}
	defer func() { GlobalSettings = api.Settings{} }()

	var persisted atomic.Int32
	entry := newSaveDirEntry(func() { persisted.Add(1) })

	// 清空输入框。
	entry.SetText("")
	if GlobalSettings.DefaultSaveDir != "/tmp/dl" {
		t.Fatalf("save dir = %q, want the previous value kept", GlobalSettings.DefaultSaveDir)
	}
	if entry.Text != "/tmp/dl" {
		t.Fatalf("entry text = %q, want the previous value restored", entry.Text)
	}
	if got := persisted.Load(); got != 0 {
		t.Fatalf("persist called %d times for an empty dir, want 0", got)
	}

	// 合法的新值照常生效并落盘。
	entry.SetText("/tmp/other")
	if GlobalSettings.DefaultSaveDir != "/tmp/other" {
		t.Fatalf("save dir = %q, want /tmp/other", GlobalSettings.DefaultSaveDir)
	}
	if got := persisted.Load(); got != 1 {
		t.Fatalf("persist called %d times, want 1", got)
	}
}

// TestBuildLanguageSelect_DoesNotOverrideLocaleOnInit 回归测试。
//
// Fyne 的 Select.SetSelected 会同步触发 OnChanged，而构建下拉框时用
// SetSelected 摆当前语言是纯初始化动作。如果回调里无条件 ilocale.Set，
// 初始值（例如 GlobalSettings.Language 为空 → 归一化成 zh-Hans）会
// 把当前界面语言反向覆盖掉——实测表现为「已经切成英文的设置页，打开
// 设置后又变回中文」。这里锁定：构建阶段不得改动 ilocale。
func TestBuildLanguageSelect_DoesNotOverrideLocaleOnInit(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	prev := ilocale.Current()
	defer ilocale.Set(prev)
	ilocale.Set("en")

	GlobalSettings = api.Settings{} // 语言为空：归一化会落到 zh-Hans
	defer func() { GlobalSettings = api.Settings{} }()

	_ = buildLanguageSelect(func() {})
	if got := ilocale.Current(); got != "en" {
		t.Fatalf("构建语言下拉框后 ilocale.Current() = %q，want en（初始化不得覆盖当前语言）", got)
	}
}

// TestLanguageSelectSwitchAppliesLocaleBeforePersist 守住「用户切语言」这条
// 路径的两个关键语义：
//
//  1. 本进程的 ilocale 必须**立刻**切到新语言——契约层的语言控制消息不带
//     语言标签（见 api.Message / rpc.Client.SetOnLanguage），UI 等不到标签；
//     而 SaveSettings 的应答与随后的控制消息谁先到不确定，等回调再切会让
//     界面语言落后一拍。
//  2. persist 必须被调用一次——否则用户的选择没有落库，重启后回退。
//
// 这条用例驱动的是真实的下拉框回调（SetSelected → OnChanged），不是直接
// 调内部函数。
func TestLanguageSelectSwitchAppliesLocaleBeforePersist(t *testing.T) {
	test.NewApp()

	prevLocale := ilocale.Current()
	prevSettings := GlobalSettings
	t.Cleanup(func() {
		ilocale.Set(prevLocale)
		GlobalSettings = prevSettings
	})

	// 从中文起步，切到英文。
	GlobalSettings.Language = "zh-Hans"
	ilocale.Set("zh-Hans")

	var persists int
	sel := buildLanguageSelect(func() { persists++ })

	enLbl := ilocale.T(ilocale.SupportedLabels["en"])
	if enLbl == "" {
		t.Fatal("前置条件不成立: 找不到英文的自描述标签")
	}

	// 触发用户选择。
	sel.SetSelected(enLbl)

	if got := ilocale.Current(); got != "en" {
		t.Fatalf("切语言后本进程 locale = %q，期望 en（必须本地先切，不能等控制消息）", got)
	}
	if GlobalSettings.Language != "en" {
		t.Fatalf("GlobalSettings.Language = %q，期望 en", GlobalSettings.Language)
	}
	if persists != 1 {
		t.Fatalf("persist 调用 %d 次，期望恰好 1 次", persists)
	}
}
