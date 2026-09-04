// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package settings

import (
	"path/filepath"
	"testing"

	"lgo_download_manager/internal/store"
)

// TestLoad_FirstRunDefaults 验证首次运行的默认值——尤其是 LightMode 默认关闭:
// 关闭主窗口只隐藏窗口, UI 子进程保持存活, 托盘可随时重新拉起窗口。
// 这避免了用户预期「X 只是关闭窗口」时,UI 却被完整退出。
func TestLoad_FirstRunDefaults(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	got, err := Load(st, false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.LightMode != false {
		t.Errorf("LightMode default = %v, want false (so that closing main window only hides it)", got.LightMode)
	}
}

// TestLoad_ForceLightOverridesDefault 验证 --light flag 强制开启 LightMode。
func TestLoad_ForceLightOverridesDefault(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	got, err := Load(st, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.LightMode {
		t.Errorf("LightMode = %v, want true (forceLight override)", got.LightMode)
	}
}

// TestLoad_PersistedLightModeHonored 验证已持久化的 LightMode 设置在后续启动时被尊重,
// 不会因为 --light=false 而被覆盖为 false。
func TestLoad_PersistedLightModeHonored(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	// 第一次: 用户在设置里勾上了 LightMode=true 并保存。
	if _, err := Load(st, false); err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if err := st.SaveSettings(store.Settings{
		DefaultSaveDir: t.TempDir(), // 已存在的用户——firstRun=false
		LightMode:      true,
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// 第二次启动: 应该读取到 LightMode=true,不应用默认覆盖。
	got, err := Load(st, false)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if !got.LightMode {
		t.Errorf("LightMode = %v, want true (user explicitly set it)", got.LightMode)
	}
}
