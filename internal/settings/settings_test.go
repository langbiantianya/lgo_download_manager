// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package settings

import (
	"path/filepath"
	"testing"

	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/store"
)

// TestLoad_FirstRunDefaults 验证首次运行的默认值——尤其是 LightMode
// 默认开启:点击主窗口 X 直接退出 UI 子进程,符合用户对窗口关闭按钮的
// 直觉预期;想要「关闭即隐藏」的用户可以在「设置」里手动取消。
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
	if !got.LightMode {
		t.Errorf("LightMode default = %v, want true (so X on main window exits the UI child)", got.LightMode)
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

// TestAutoStart_DefaultOff 验证首次运行 AutoStart 默认关闭——
// 「默认不打扰用户」原则;用户在「设置」里显式开启才会注册。
func TestAutoStart_DefaultOff(t *testing.T) {
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
	if got.AutoStart {
		t.Errorf("AutoStart default = %v, want false (do not bother users on first run)", got.AutoStart)
	}
}

// TestAutoStart_PersistedRoundTrip 验证用户开启 AutoStart 后,
// 后续 Load 能读到持久化的 true 值;使用 store.SaveSettings 直接落盘
// 以避免在测试里实际写注册表/.desktop/LaunchAgent。
func TestAutoStart_PersistedRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	// 首次运行后,模拟用户在 UI 中勾选 AutoStart=true 并提交。
	if _, err := Load(st, false); err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if err := st.SaveSettings(store.Settings{
		DefaultSaveDir: t.TempDir(), // firstRun=false
		AutoStart:      true,
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got, err := Load(st, false)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if !got.AutoStart {
		t.Errorf("AutoStart = %v, want true (user explicitly toggled it on)", got.AutoStart)
	}
}

// TestLoad_Language_PersistedWinsOverSystem 验证用户在设置里显式选过的
// 语言在后续启动时被尊重,不再被系统语言覆盖。这是 i18n 选型契约的核心:
// 用户显式偏好优先于 OS 推断,否则每次系统语言变化都会让 UI 跟着跳。
func TestLoad_Language_PersistedWinsOverSystem(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	// 模拟一次首启,触发默认值落盘。
	if _, err := Load(st, false); err != nil {
		t.Fatalf("first Load: %v", err)
	}
	// 随后用户在「设置 → 语言」里选了日语,保存。
	if err := st.SaveSettings(store.Settings{
		DefaultSaveDir: t.TempDir(), // firstRun=false
		Language:       "ja",
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// 再次启动:即便 OS 是英文(测试运行环境可能也是英文),Load 仍然
	// 返回 ja——持久化值优先。
	got, err := Load(st, false)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if got.Language != "ja" {
		t.Errorf("Language = %q, want %q (persisted value must win over OS detection)", got.Language, "ja")
	}
}

// TestLoad_Language_UnsetFallsBackToSystem 验证 DB 里 Language 为空时
// 走 SystemLanguage():用户没显式选过,就猜一个最像的;OS 标签若不在
// Supported 范围内,落到默认 zh-Hans。
func TestLoad_Language_UnsetFallsBackToSystem(t *testing.T) {
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
	// OS 语言探测的具体结果由测试机决定,但必须落在 Supported 里。
	want := ilocale.Normalize(ilocale.SystemLanguage())
	if got.Language != want {
		t.Errorf("Language = %q, want %q (SystemLanguage normalization)", got.Language, want)
	}
}

// TestLoad_Language_InvalidPersistedNormalized 验证持久化的无效标签
// (例如旧版本残留的脏值,或用户从外部修改了 db)会被归一化到 Supported
// 中的一项,而不是以原始字符串进入 UI 流程——后者会让 ilocale.NewLocalizer
// 报错或返回空字符串。
func TestLoad_Language_InvalidPersistedNormalized(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	if _, err := Load(st, false); err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if err := st.SaveSettings(store.Settings{
		DefaultSaveDir: t.TempDir(),
		Language:       "totally-invalid-tag", // 不在 Supported 也不在 BCP-47
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got, err := Load(st, false)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if got.Language != ilocale.DefaultLanguage {
		t.Errorf("Language = %q, want default %q (invalid persisted value must normalize)", got.Language, ilocale.DefaultLanguage)
	}
}
