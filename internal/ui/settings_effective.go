// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/urlkind"
)

// 契约层 api.Settings 只携带存储值，不带「零值即默认」的判定；而设置页
// 要把与业务侧一致的有效值显示给用户（空白输入框应该显示 3 而不是 0）。
// 下面四个助手是 internal/store.Settings 上同名 Effective* 方法的镜像。
//
// 默认值必须与 internal/store/store.go 的 Default* 常量逐字相同：
// UI 只是把业务侧的判定抄一份用于渲染，判定规则本身归业务侧所有。
// 两处一旦漂移，设置页显示的数字就会和实际生效值对不上，
// settings_effective_test.go 用硬编码期望值锁住这四个默认值。
const (
	defaultMaxConcurrent   = 3
	defaultBTMaxPeers      = 50
	defaultBTMaxSeeding    = 2
	defaultBTMaxConcurrent = 2
)

// effectiveMaxConcurrent 返回 HTTP/FTP 并发上限的有效值：<=0 视为默认。
func effectiveMaxConcurrent(s api.Settings) int {
	if s.MaxConcurrent <= 0 {
		return defaultMaxConcurrent
	}
	return s.MaxConcurrent
}

// effectiveBTMaxConcurrent 返回 BT 专用并发上限的有效值：<=0 视为默认。
func effectiveBTMaxConcurrent(s api.Settings) int {
	if s.BTMaxConcurrent <= 0 {
		return defaultBTMaxConcurrent
	}
	return s.BTMaxConcurrent
}

// effectiveBTMaxSeeding 返回同时做种任务数的有效值：<=0 视为默认。
func effectiveBTMaxSeeding(s api.Settings) int {
	if s.BTMaxSeeding <= 0 {
		return defaultBTMaxSeeding
	}
	return s.BTMaxSeeding
}

// effectiveBTMaxPeers 返回 BTMaxPeers 的有效值：<=0 视为默认。
func effectiveBTMaxPeers(s api.Settings) int {
	if s.BTMaxPeers <= 0 {
		return defaultBTMaxPeers
	}
	return s.BTMaxPeers
}

// isBittorrentTask 报告任务是否应交给 BitTorrent 面板处理。
//
// 与业务侧 scheduler.IsBittorrentTask 的判定保持一致：协议显式标记为
// BT 时直接成立；协议非空且不是 BT 时明确否定；协议为空（历史数据）
// 时再按 URL 形态兜底识别磁力链接与 .torrent。
func isBittorrentTask(t *api.Task) bool {
	if t == nil {
		return false
	}
	if t.Protocol == string(api.ProtoBT) {
		return true
	}
	if t.Protocol != "" {
		return false
	}
	return urlkind.IsBitTorrent(t.URL)
}
