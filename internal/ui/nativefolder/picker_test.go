// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package nativefolder

import (
	"testing"

	"lgo_download_manager/internal/ilocale"
)

// TestPickerTitle_FollowsUILanguage 回归测试。
//
// 原生目录选择器的标题原先在三个平台实现里各写死一份中文
// 「选择保存位置」,界面语言切到英文之后,系统弹出的原生对话框仍然
// 顶着中文标题。标题现在必须跟着界面语言走。
func TestPickerTitle_FollowsUILanguage(t *testing.T) {
	prev := ilocale.Current()
	defer ilocale.Set(prev)

	cases := map[string]string{
		"en":      "Select save location",
		"zh-Hans": "选择保存位置",
		"zh-Hant": "選擇儲存位置",
		"de":      "Speicherort auswählen",
		"ja":      "保存先を選択",
		"ru":      "Выберите место сохранения",
	}
	for lang, want := range cases {
		ilocale.Set(lang)
		if got := pickerTitle(); got != want {
			t.Errorf("pickerTitle() in %s = %q, want %q", lang, got, want)
		}
	}
}
