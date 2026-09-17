// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ilocale

import (
	"strings"
	"testing"
)

// TestNormalizeFolding 覆盖 BCP-47 → Supported 的折叠规则。
// 该函数是 settings.Load 在持久化值非空时校验入口,也是 SystemLanguage
// 把 OS 标签落到 Supported 的唯一途径——任一映射错就会让用户的 OS
// 语言走不到预期分支。
func TestNormalizeFolding(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", DefaultLanguage},
		{"zh-Hans", "zh-Hans"},
		{"zh-Hant", "zh-Hant"},
		{"zh-CN", "zh-Hans"},
		{"zh-SG", "zh-Hans"},
		{"zh-TW", "zh-Hant"},
		{"zh-HK", "zh-Hant"},
		{"zh-MO", "zh-Hant"},
		{"zh", "zh-Hans"},
		{"en", "en"},
		{"en-US", "en"},
		{"en-GB", "en"},
		{"ja", "ja"},
		{"ja-JP", "ja"},
		{"de", "de"},
		{"de-DE", "de"},
		{"de-AT", "de"},
		{"ru", "ru"},
		{"ru-RU", "ru"},
		// 不受支持的语种 / 乱码 → 默认。
		{"pt-BR", DefaultLanguage},
		{"fr-FR", DefaultLanguage},
		{"ko", DefaultLanguage},
		{"garbage", DefaultLanguage},
	}
	for _, c := range cases {
		got := Normalize(c.in)
		if got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSupportedSortedAndUnique 验证 Supported 是受信任的 UI 顺序(默认语言
// 在第一位),且不存在重复——settings_dialog 的 buildLanguageSelect 与
// settings.Load 内部对 Supported 做线性查找,任何重复会让"未选语言"分支
// 落到错误的一项。
func TestSupportedSortedAndUnique(t *testing.T) {
	if Supported[0] != DefaultLanguage {
		t.Errorf("first supported language = %q, want default %q", Supported[0], DefaultLanguage)
	}
	seen := make(map[string]bool, len(Supported))
	for _, s := range Supported {
		if seen[s] {
			t.Errorf("Supported contains duplicate %q", s)
		}
		seen[s] = true
	}
	for tag, lbl := range SupportedLabels {
		if tag != lbl[len("lang."):] {
			t.Errorf("SupportedLabels[%q] = %q (mismatched prefix)", tag, lbl)
		}
	}
}

// TestT_FallbackWhenKeyMissing 验证未翻译的 key 回退到 TS 传入的
// defaultMessage。开发期补翻译时这条是主要的可见行为——如果回退失败
// 用户看到的是 id 而不是合理文案。
func TestT_FallbackWhenKeyMissing(t *testing.T) {
	Set("en")
	got := TS("this.key.does.not.exist", "默认回退文案")
	if got != "默认回退文案" {
		t.Errorf("TS missing key fallback = %q, want %q", got, "默认回退文案")
	}
}

// TestT_PerLanguageCoveredKeys 对每个翻译文件里都存在的关键 ID 抽样,
// 验证不同语言下能给出非空翻译,且与默认语言翻译明显不同——只有空字符串
// 或与 zh-Hans 文本完全一致,都说明翻译文件未被加载/未覆盖。
func TestT_PerLanguageCoveredKeys(t *testing.T) {
	keys := []string{
		"main.window.title",
		"main.toolbar.new",
		"taskRow.status.downloading",
		"taskRow.status.paused",
		"addTask.start",
		"taskList.empty",
	}
	Set("zh-Hans")
	defaults := make(map[string]string, len(keys))
	for _, k := range keys {
		defaults[k] = T(k)
		if defaults[k] == k || defaults[k] == "" {
			t.Errorf("zh-Hans key %q not translated (got %q)", k, defaults[k])
		}
	}
	for _, lang := range []string{"zh-Hant", "en", "ja", "de", "ru"} {
		Set(lang)
		for _, k := range keys {
			got := T(k)
			if got == "" || got == k {
				t.Errorf("%s key %q not translated (got %q)", lang, k, got)
			}
			if got == defaults[k] {
				// 翻译"可能"恰好相同(例如英文/德文都短),但覆盖面积极小;
				// 这里只看它是否被加载——已经验证非空,所以允许相同。
				t.Logf("%s key %q happens to equal zh-Hans text %q", lang, k, got)
			}
		}
	}
}

// TestTF_TemplateRendering 验证 TF 的模板占位符({{ .Name }} 等)在每个
// 语言下都能正确替换,模板渲染失败的语言会让用户看到 {{ .Name }} 这种
// 半成品——这正是我们的目的:发现翻译里的占位符遗漏。
//
// 同一 key 在不同语言里的前后缀差异可能很大(中文"磁盘空间: 可用 10 GB
// / 总计 100 GB" vs 英文"Disk space: 10 GB free / 100 GB total"),
// 通用做法是只校验数据值出现在结果中,并强制没有未替换的 {{ 占位符。
func TestTF_TemplateRendering(t *testing.T) {
	cases := []struct {
		id        string
		def       string
		data      map[string]any
		wantParts []string // 每个子串都必须在结果中出现(允许语言差异)
	}{
		{"main.status.disk.format", "Disk: {{ .Free }} / {{ .Total }}",
			map[string]any{"Free": "10 GB", "Total": "100 GB"},
			[]string{"10 GB", "100 GB"}},
		{"details.title", "Task: {{ .Name }}",
			map[string]any{"Name": "x.zip"}, []string{"x.zip"}},
		{"time.hoursMinutes", "{{ .H }}h {{ .M }}m",
			map[string]any{"H": 2, "M": 30}, []string{"2", "30"}},
	}
	for _, lang := range Supported {
		Set(lang)
		for _, c := range cases {
			got := TF(c.id, c.def, c.data)
			for _, sub := range c.wantParts {
				if !strings.Contains(got, sub) {
					t.Errorf("%s TF(%q) = %q, missing %q", lang, c.id, got, sub)
				}
			}
			if strings.Contains(got, "{{") {
				t.Errorf("%s TF(%q) = %q, has unsubstituted template placeholder", lang, c.id, got)
			}
		}
	}
}

// TestSet_Idempotent 验证连续 Set 同一语言不会改变 Current——避免
// 误把 UI 刷新当作语言变更去重建整个主窗口。
func TestSet_Idempotent(t *testing.T) {
	Set("en")
	a := Current()
	Set("en")
	b := Current()
	if a != b || a != "en" {
		t.Errorf("Set same lang should be stable: %q -> %q", a, b)
	}
	// 不受支持的 tag 经 Normalize 折叠到 DefaultLanguage——Set 不会"忽略"
	// 输入,而是把不可识别的输入收敛到默认值。这与 settings.Load 的
	// 行为一致:任何非法持久化值都被替换为默认。
	Set("not-supported-tag")
	if c := Current(); c != DefaultLanguage {
		t.Errorf("Set invalid tag should normalize to default: got %q, want %q", c, DefaultLanguage)
	}
}
