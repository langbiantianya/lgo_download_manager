// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package ilocale 提供 lgdm 自己的本地化抽象:
//
//   - 一组 BCP-47 语言标签(zh-Hans / zh-Hant / ru / de / ja / en),
//     默认 zh-Hans;
//   - 用 go-i18n v2 加载并匹配翻译;
//   - 一组面向 UI 的 helper(T/TS/TEta),让上层免于直接拿 Localizer。
//
// 该包不依赖 fyne,可以被业务主进程和 UI 子进程共用;UI 进程启动时
// 通过 Set 选定语言,业务进程设置保存时也调用 Set 把权威语言同步到
// 当前进程的 Localizer。
//
// 不在业务侧主路径调用 T():业务进程只关心 Language 的持久化与下发,
// 不去翻译日志或 IPC 错误——翻译是 UI 用户的体验问题,日志保留英文
// (便于 grep),错误信息保留技术英文(便于排障)。
package ilocale

import (
	"embed"
	"fmt"
	"os"
	"sync"

	"github.com/jeandeaual/go-locale"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"go.yaml.in/yaml/v3"
	"golang.org/x/text/language"
)

//go:embed translations/*.yaml
var translationsFS embed.FS

// Supported 列出 lgdm 主动支持的语言标签。顺序就是用户在
// 「设置 → 语言」里看到的顺序。第一个是默认语言,不在 Supported
// 内的标签会被替换为 DefaultLanguage。
var Supported = []string{
	"zh-Hans", // 简体中文
	"zh-Hant", // 繁体中文
	"en",      // English
	"ja",      // 日本語
	"de",      // Deutsch
	"ru",      // Русский
}

// SupportedLabels 给 Settings 对话框用的「友好显示名」键值对;
// 每个值都是一条翻译 ID(显示自身语言的名字)。
var SupportedLabels = map[string]string{
	"zh-Hans": "lang.zh-Hans",
	"zh-Hant": "lang.zh-Hant",
	"en":      "lang.en",
	"ja":      "lang.ja",
	"de":      "lang.de",
	"ru":      "lang.ru",
}

// DefaultLanguage 是兜底语言:Settings 没有显式选过、系统语言也
// 不在 Supported 内时落到这里。
const DefaultLanguage = "zh-Hans"

// Normalize 把任意 BCP-47 标签折叠到 Supported 列表中的一项:
//   - 完全匹配 → 原样返回;
//   - 仅匹配基础子标签(如 zh-TW → zh-Hant、zh-CN → zh-Hans、zh → zh-Hans)→ 返回最接近的一项;
//   - 其它未匹配的标签 → 返回 DefaultLanguage。
//
// 拆分 zh-CN/zh-TW/zh-HK 这种地域变体时,简体优先 zh-Hans,繁体优先 zh-Hant;
// 不依赖外部 locale 数据,只覆盖我们的 Supported 集合。
//
// 实现细节:language.Make 会宽容地把 "garbage" 折叠成 "en"——这对
// 我们的目的(只允许白名单里的语种)太宽松。我们用 language.Parse 先行
// 兜底,Parse 拒绝任意不合法的 tag。极少数语言(如 ja-JP / de-AT)Parse
// 也接受,交给下面 base/region 派发;完全不在白名单的就走默认。
func Normalize(tag string) string {
	if tag == "" {
		return DefaultLanguage
	}
	for _, s := range Supported {
		if tag == s {
			return s
		}
	}
	t, err := language.Parse(tag)
	if err != nil {
		return DefaultLanguage
	}
	base, _ := t.Base()
	switch base.String() {
	case "zh":
		region, _ := t.Region()
		switch region.String() {
		case "TW", "HK", "MO":
			for _, s := range Supported {
				if s == "zh-Hant" {
					return s
				}
			}
		default:
			for _, s := range Supported {
				if s == "zh-Hans" {
					return s
				}
			}
		}
	case "en":
		for _, s := range Supported {
			if s == "en" {
				return s
			}
		}
	case "ja":
		for _, s := range Supported {
			if s == "ja" {
				return s
			}
		}
	case "de":
		for _, s := range Supported {
			if s == "de" {
				return s
			}
		}
	case "ru":
		for _, s := range Supported {
			if s == "ru" {
				return s
			}
		}
	}
	return DefaultLanguage
}

// SystemLanguage 通过 jeandeaual/go-locale 读取 OS 报告的界面语言
// (Windows GetUserDefaultUILanguage / POSIX LC_* / macOS CFLocale),
// 落到 Supported 中最接近的一项;读不到时返回 DefaultLanguage。
//
// 该函数在业务进程首启时调用一次,把 OS 语言作为「未设置时的偏好」,
// 但只有 Settings.Language 真的为空才会被采纳。
func SystemLanguage() string {
	// GetLanguage 在所有平台上返回「语言-地区」(如 zh-CN / en-US),
	// 是 UI 语言而非时区/数字格式,正合我们用途。
	tag, err := locale.GetLanguage()
	if err != nil || tag == "" {
		return DefaultLanguage
	}
	return Normalize(tag)
}

// 全局 Localizer。Set 只在程序启动时或用户切语言时调用,内部
// 用 RWMutex 串行化即可;T 是热路径,只读锁。
var (
	bundle *i18n.Bundle
	mu     sync.RWMutex
	cur    *i18n.Localizer
	curTag string
)

func init() {
	bundle = i18n.NewBundle(language.English)
	// go-i18n 的 LoadMessageFileFS 不自动注册 yaml 反序列化器,需要
	// 调用方手动提供——这里用 yaml.v3,把每条翻译填进 bundle。
	bundle.RegisterUnmarshalFunc("yaml", func(b []byte, v any) error {
		return yaml.Unmarshal(b, v)
	})
	for _, tag := range Supported {
		// 单个文件损坏不该拖垮其它语言——记录到 stderr,业务进程启动日志
		// 与 UI 进程启动日志都能看到;其余语言仍可正常工作。
		if _, err := bundle.LoadMessageFileFS(translationsFS, "translations/"+tag+".yaml"); err != nil {
			fmt.Fprintf(os.Stderr, "ilocale: load %s.yaml: %v\n", tag, err)
		}
	}
	curTag = DefaultLanguage
	cur = i18n.NewLocalizer(bundle, DefaultLanguage)
}

// Set 把全局 Localizer 切换到 lang。lang 应该是 Supported 中的一项;
// 调用前应用 Normalize 折叠一次避免拼写漂移。Set 之后所有 T() 调用
// 都返回新语言的翻译。
func Set(lang string) {
	mu.Lock()
	defer mu.Unlock()
	norm := Normalize(lang)
	if norm == curTag {
		return
	}
	curTag = norm
	cur = i18n.NewLocalizer(bundle, norm)
}

// Current 返回当前生效的语言标签(已经是 Supported 中的一项)。
func Current() string {
	mu.RLock()
	defer mu.RUnlock()
	return curTag
}

// T 把 id 对应的翻译以默认语言(default)兜底返回。translation 文件里
// 缺失的 id 会原样回退到 default。永远返回非空字符串。
//
// T 在 UI 渲染热路径上调用:每次 SetText 都会进,实现里走 RWMutex 的
// 读锁,go-i18n 内部的 messageTemplate 缓存命中后是 O(1)。
func T(id string) string {
	return TS(id, id)
}

// TS 等同 T 但允许用 defaultMessage 兜底翻译缺失。
// 翻译缺失时返回 defaultMessage 而不是 id——便于在「调试阶段少一个
// 翻译文件也能直接显示开发期文案」。
func TS(id, defaultMessage string) string {
	mu.RLock()
	l := cur
	mu.RUnlock()
	if l == nil {
		return defaultMessage
	}
	s, err := l.Localize(&i18n.LocalizeConfig{
		MessageID:      id,
		DefaultMessage: &i18n.Message{ID: id, Other: defaultMessage},
	})
	if err != nil || s == "" {
		return defaultMessage
	}
	return s
}

// TF 用模板数据格式化翻译,失败时回退到 printf(其它参数)。
//
// 例:
//
//	ilocale.TF("eta.seconds", "ETA %d 秒", secs)
//	ilocale.TF("size.format", "Total: %.2f MB", mb)
//
// 翻译文件里如果有模板占位符 {{ .N }},go-i18n 会用 TemplateData 替换;
// 没有模板时直接返回翻译本身。
func TF(id, defaultTemplate string, data map[string]any) string {
	mu.RLock()
	l := cur
	mu.RUnlock()
	if l == nil {
		return defaultTemplate
	}
	s, err := l.Localize(&i18n.LocalizeConfig{
		MessageID:      id,
		DefaultMessage: &i18n.Message{ID: id, Other: defaultTemplate},
		TemplateData:   data,
	})
	if err != nil || s == "" {
		return defaultTemplate
	}
	return s
}

// Sprintf 是 TS + fmt.Sprintf 的便捷封装:用翻译做 format 串,后续参数
// 走 fmt.Sprintf。注意 format 串是翻译后的字符串,不同语言的占位符
// 顺序可能不同——适合「同一种占位符顺序」的简单情形。复杂模板请用 TF。
func Sprintf(id, defaultMessage string, args ...any) string {
	return fmt.Sprintf(TS(id, defaultMessage), args...)
}
