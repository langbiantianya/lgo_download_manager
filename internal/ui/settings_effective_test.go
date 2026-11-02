// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"testing"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// TestEffectiveSettingsMatchStoreDefaults 用硬编码期望值锁住四个
// Effective* 助手：默认值必须与 internal/store/store.go 的
// DefaultMaxConcurrent=3 / DefaultBTMaxPeers=50 /
// DefaultBTMaxSeeding=2 / DefaultBTMaxConcurrent=2 逐字相同。
//
// 期望值刻意写成字面量而不是引用被测常量——如果测试拿被测常量当期望，
// 把默认值调小（或调成 0）时断言基数会跟着一起变，改坏也抓不到。
func TestEffectiveSettingsMatchStoreDefaults(t *testing.T) {
	cases := []struct {
		name string
		fn   func(api.Settings) int
		set  func(*api.Settings, int)
		// 四档输入对应的期望：负数、零、正常小值、正常大值。
		want [4]int
	}{
		{
			name: "EffectiveMaxConcurrent",
			fn:   effectiveMaxConcurrent,
			set:  func(s *api.Settings, v int) { s.MaxConcurrent = v },
			want: [4]int{3, 3, 1, 8},
		},
		{
			name: "EffectiveBTMaxPeers",
			fn:   effectiveBTMaxPeers,
			set:  func(s *api.Settings, v int) { s.BTMaxPeers = v },
			want: [4]int{50, 50, 1, 8},
		},
		{
			name: "EffectiveBTMaxSeeding",
			fn:   effectiveBTMaxSeeding,
			set:  func(s *api.Settings, v int) { s.BTMaxSeeding = v },
			want: [4]int{2, 2, 1, 8},
		},
		{
			name: "EffectiveBTMaxConcurrent",
			fn:   effectiveBTMaxConcurrent,
			set:  func(s *api.Settings, v int) { s.BTMaxConcurrent = v },
			want: [4]int{2, 2, 1, 8},
		},
	}
	inputs := [4]int{-1, 0, 1, 8}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i, in := range inputs {
				var s api.Settings
				tc.set(&s, in)
				if got := tc.fn(s); got != tc.want[i] {
					t.Errorf("%s(%d) = %d，want %d（零值/负值必须回落到与 store 一致的默认值）",
						tc.name, in, got, tc.want[i])
				}
			}
		})
	}
}

// TestIsBittorrentTaskMatchesSchedulerSemantics 锁定 BT 判定的历史语义
// （对照 internal/scheduler/bittorrent.go:766 的旧 IsBittorrentTask）：
//   - nil → false
//   - Protocol 显式为 BT → true
//   - Protocol 非空且不是 BT → false（哪怕 URL 是磁力链接）
//   - Protocol 为空（历史数据）→ 按 URL 形态兜底识别磁力链接与 .torrent
func TestIsBittorrentTaskMatchesSchedulerSemantics(t *testing.T) {
	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"

	cases := []struct {
		name string
		task *api.Task
		want bool
	}{
		{"nil 任务", nil, false},
		{"显式 BT 协议", &api.Task{URL: "https://x/a.torrent", Protocol: "BT"}, true},
		{"显式 HTTP + 磁力链接", &api.Task{URL: magnet, Protocol: "HTTP"}, false},
		{"显式 BT 但 URL 是普通链接", &api.Task{URL: "https://x/a.bin", Protocol: "BT"}, true},
		{"协议为空 + 磁力链接", &api.Task{URL: magnet}, true},
		{"协议为空 + .torrent 链接", &api.Task{URL: "https://x/a.torrent"}, true},
		{"协议为空 + 普通链接", &api.Task{URL: "https://x/a.bin"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBittorrentTask(tc.task); got != tc.want {
				t.Fatalf("isBittorrentTask = %v，want %v", got, tc.want)
			}
		})
	}
}

// TestEffectiveSettingsCrossCheckWithStore 直接与 store.Settings.Effective*
// 对拍。
//
// 上面的 TestEffectiveSettingsMatchStoreDefaults 用的是硬编码期望值——它能
// 抓住「UI 侧自己改了默认值」，但抓不住「store 侧改了默认值、UI 没跟」。
// 这条用例把两侧放进同一个循环里比，任何一侧漂移都会红。
//
// internal/ui 的非测试代码不得 import internal/store；这里是测试代码，属于
// G9 明确允许的例外——只用于对拍，不参与生产路径。
func TestEffectiveSettingsCrossCheckWithStore(t *testing.T) {
	for _, in := range []int{-5, -1, 0, 1, 2, 3, 8, 64} {
		st := store.Settings{
			MaxConcurrent:   in,
			BTMaxConcurrent: in,
			BTMaxSeeding:    in,
			BTMaxPeers:      in,
		}
		ap := api.Settings{
			MaxConcurrent:   in,
			BTMaxConcurrent: in,
			BTMaxSeeding:    in,
			BTMaxPeers:      in,
		}
		checks := []struct {
			name      string
			ui, store int
		}{
			{"EffectiveMaxConcurrent", effectiveMaxConcurrent(ap), st.EffectiveMaxConcurrent()},
			{"EffectiveBTMaxConcurrent", effectiveBTMaxConcurrent(ap), st.EffectiveBTMaxConcurrent()},
			{"EffectiveBTMaxSeeding", effectiveBTMaxSeeding(ap), st.EffectiveBTMaxSeeding()},
			{"EffectiveBTMaxPeers", effectiveBTMaxPeers(ap), st.EffectiveBTMaxPeers()},
		}
		for _, c := range checks {
			if c.ui != c.store {
				t.Errorf("输入 %d 时 %s: ui=%d store=%d —— 两侧漂移", in, c.name, c.ui, c.store)
			}
		}
	}
}

// TestIsBittorrentTaskCrossCheckWithScheduler 直接与
// scheduler.IsBittorrentTask 对拍（同上的对拍理由）。
func TestIsBittorrentTaskCrossCheckWithScheduler(t *testing.T) {
	urls := []string{
		"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		"https://x/a.torrent",
		"http://x/a.torrent",
		"https://x/a.bin",
		"",
	}
	protocols := []string{"", "BT", "HTTP", "HTTPS", "FTP", "WEBDAV"}
	for _, u := range urls {
		for _, p := range protocols {
			ui := isBittorrentTask(&api.Task{URL: u, Protocol: p})
			sc := scheduler.IsBittorrentTask(&store.Task{URL: u, Protocol: p})
			if ui != sc {
				t.Errorf("url=%q protocol=%q: ui=%v scheduler=%v —— 两侧漂移", u, p, ui, sc)
			}
		}
	}
	if isBittorrentTask(nil) != scheduler.IsBittorrentTask(nil) {
		t.Error("nil 任务的判定在两侧不一致")
	}
}
