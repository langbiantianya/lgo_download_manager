// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/autostart"
	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/logging"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/store"
)

const mib = int64(1 << 20)

// GlobalSettings 是 UI 侧持有的设置镜像，供“新建任务”预填、状态栏
// 磁盘空间、任务排序等即时读取。权威设置在业务进程（store）；UI 通过
// svc.SaveSettings 持久化，业务进程负责应用代理配置。每次修改镜像后
// 都同步保存，因此两者保持一致。
var GlobalSettings store.Settings

func buildSettingsContent(svc Service, onChange func()) fyne.CanvasObject {
	persist := func() {
		if svc == nil {
			return
		}
		if err := svc.SaveSettings(GlobalSettings); err != nil {
			logging.Printf("ui: save settings: %v", err)
		}
		if onChange != nil {
			onChange()
		}
	}
	dirEntry := widget.NewEntry()
	dirEntry.SetText(GlobalSettings.DefaultSaveDir)
	dirEntry.OnChanged = func(s string) {
		GlobalSettings.DefaultSaveDir = s
		persist()
	}

	threadsEntry := widget.NewEntry()
	threadsEntry.SetText(fmt.Sprintf("%d", GlobalSettings.DefaultThreads))
	threadsEntry.OnChanged = func(s string) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
			GlobalSettings.DefaultThreads = n
			persist()
		}
	}

	// 分块大小以 MiB 形式显示（十进制整数，例如“4”）。右侧静态的
	// “MB”标签仅为装饰——在传给引擎前，该值始终乘以 1<<20。
	chunkSizeEntry := widget.NewEntry()
	chunkSizeEntry.SetText(fmt.Sprintf("%d", GlobalSettings.MinChunkSize/mib))
	chunkSizeEntry.OnChanged = func(s string) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
			GlobalSettings.MinChunkSize = int64(n) * mib
			persist()
		}
	}
	chunkSizeRow := container.NewBorder(nil, nil, nil, widget.NewLabel(ilocale.T("settings.label.minChunkUnit")), chunkSizeEntry)

	uaEntry := widget.NewEntry()
	uaEntry.SetText(GlobalSettings.UserAgent)
	uaEntry.SetPlaceHolder(ilocale.T("settings.ua.placeholder"))
	uaEntry.OnChanged = func(s string) {
		GlobalSettings.UserAgent = s
		persist()
	}

	cookiesEntry := widget.NewEntry()
	cookiesEntry.SetText(GlobalSettings.Cookies)
	cookiesEntry.SetPlaceHolder(ilocale.T("settings.cookies.placeholder"))
	cookiesEntry.OnChanged = func(s string) {
		GlobalSettings.Cookies = s
		persist()
	}

	// 同时下载任务数上限。空值/非正数会被 settings.Load 套用默认值;
	// 这里仍接受用户输入并在持久化前做基本校验(正整数),
	// 以免 store 收到 0/负数(EffectiveMaxConcurrent 会兜底但 UX 不直观)。
	maxConcEntry := widget.NewEntry()
	maxConcEntry.SetText(fmt.Sprintf("%d", GlobalSettings.EffectiveMaxConcurrent()))
	maxConcEntry.OnChanged = func(s string) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
			GlobalSettings.MaxConcurrent = n
			persist()
		}
	}

	// 代理相关控件
	proxyURLEntry := widget.NewEntry()
	proxyURLEntry.SetText(GlobalSettings.ProxyURL)
	proxyURLEntry.SetPlaceHolder(ilocale.T("settings.proxyURL.placeholder"))
	proxyURLEntry.OnChanged = func(s string) {
		GlobalSettings.ProxyURL = strings.TrimSpace(s)
		persist()
	}

	proxyBypassEntry := widget.NewEntry()
	proxyBypassEntry.SetText(GlobalSettings.ProxyBypass)
	proxyBypassEntry.SetPlaceHolder(ilocale.T("settings.proxyBypass.placeholder"))
	proxyBypassEntry.OnChanged = func(s string) {
		GlobalSettings.ProxyBypass = strings.TrimSpace(s)
		persist()
	}

	// 代理模式与排序选项的「显示标签」来自翻译;底层 enum 值不变。
	// 切换语言时 buildSettingsContent 会被重建(从 buildSettingsContent
	// 调用入口 → showSettingsPage),label ↔ enum 的双向映射每次重建
	// 时重新生成。
	proxyModeOpts := make([]string, 0, 3)
	labelToProxyMode := map[string]protocol.ProxyMode{}
	for _, m := range []protocol.ProxyMode{protocol.ProxyModeSystem, protocol.ProxyModeDisabled, protocol.ProxyModeManual} {
		lbl := proxyModeLabel(m)
		proxyModeOpts = append(proxyModeOpts, lbl)
		labelToProxyMode[lbl] = m
	}

	// 切换模式时仅控制代理地址/绕过列表的可见性。地址本身始终保留，
	// 这样用户在 Manual↔System 之间来回切换不会丢失已填写的 URL。
	// 代理缓存失效在业务进程保存设置时处理（settings.Save）。
	proxyModeSelect := widget.NewSelect(proxyModeOpts, func(s string) {
		mode, ok := labelToProxyMode[s]
		if !ok {
			return
		}
		GlobalSettings.ProxyMode = mode
		manual := mode == protocol.ProxyModeManual
		if manual {
			proxyURLEntry.Show()
			proxyBypassEntry.Show()
		} else {
			proxyURLEntry.Hide()
			proxyBypassEntry.Hide()
		}
		persist()
	})
	initialMode := GlobalSettings.ProxyMode
	if initialMode == 0 {
		initialMode = protocol.ProxyModeSystem
	}
	proxyModeSelect.SetSelected(proxyModeLabel(initialMode))
	if initialMode != protocol.ProxyModeManual {
		proxyURLEntry.Hide()
		proxyBypassEntry.Hide()
	}

	ftpPassive := widget.NewCheck(ilocale.T("settings.ftp.pasv"), func(checked bool) {
		GlobalSettings.FTPPassive = checked
		persist()
	})
	ftpPassive.SetChecked(GlobalSettings.FTPPassive)

	prealloc := widget.NewCheck(ilocale.T("settings.prealloc.desc"), func(checked bool) {
		GlobalSettings.Prealloc = checked
		persist()
	})
	prealloc.SetChecked(GlobalSettings.Prealloc)

	browseBtn := widget.NewButton(ilocale.T("settings.browse"), func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			dirEntry.SetText(uri.Path())
		}, globalWin)
	})

	// 任务列表排序方式（单选下拉框）
	sortOpts := make([]string, 0, 4)
	labelToSort := map[string]store.TaskSort{}
	for _, s := range store.AllTaskSorts() {
		lbl := sortLabel(s)
		sortOpts = append(sortOpts, lbl)
		labelToSort[lbl] = s
	}
	sortSelect := widget.NewSelect(sortOpts, func(s string) {
		if sort, ok := labelToSort[s]; ok {
			GlobalSettings.TaskSort = sort
			persist()
		}
	})
	sortSelect.SetSelected(sortLabel(GlobalSettings.TaskSort))

	lightModeCheck := widget.NewCheck(ilocale.T("settings.lightMode.desc"), func(checked bool) {
		GlobalSettings.LightMode = checked
		persist()
	})
	lightModeCheck.SetChecked(GlobalSettings.LightMode)

	// 开机自启:启用后业务进程会在操作系统登录时以静默方式自启——
	// 不显示主窗口,只保留调度器 + 系统托盘;用户在托盘菜单恢复 UI。
	// UI 提交后由 settings.Save → ApplyAutoStart 在业务侧把
	// 注册表/.desktop/LaunchAgent 调到与开关一致,失败只记日志。
	autoStartCheck := widget.NewCheck(ilocale.T("settings.autoStart.desc"), func(checked bool) {
		GlobalSettings.AutoStart = checked
		persist()
	})

	// 每次打开设置时重新对齐 DB 与 OS 真实状态:
	//   - 用户可能用第三方工具(任务管理器启动标签、GNOME Tweaks、
	//     系统设置)直接改过,DB 没跟上;
	//   - 安装/卸载残留也可能让两者漂移。
	// OS 状态通过 autostart.IsEnabled() 直接读 HKCU Run / XDG autostart /
	// LaunchAgent —— 这是纯 OS 状态查询,不依赖业务进程,UI 子进程
	// 与业务进程读到的结果一致;若与 DB 不一致,以 OS 为准更新镜像并
	// 持久化回去(settings.Save → ApplyAutoStart 顺势确保 OS 也跟着对齐)。
	// 查询/持久化失败只记日志,不阻塞对话框打开。
	if enabled, err := autostart.IsEnabled(); err != nil {
		logging.Printf("ui: query autostart state: %v", err)
	} else if enabled != GlobalSettings.AutoStart {
		GlobalSettings.AutoStart = enabled
		persist()
	}
	autoStartCheck.SetChecked(GlobalSettings.AutoStart)

	// 语言选择:下拉显示「友好显示名」(由 ilocale.T("lang.<tag>") 给出),
	// 选中的就是 BCP-47 tag。语言变更的传播路径:
	//   UI select.OnChanged → 写 GlobalSettings.Language → svc.SaveSettings
	//   → 业务侧 settings.Save → uimgr.SetLanguage → UI 子进程收 MsgLanguage
	//   → ilocale.Set + applyLanguage(本文件 → settingsDialog 整体重建)
	//   → 重译所有 widget。
	langSelect := buildLanguageSelect(persist)

	form := widget.NewForm(
		widget.NewFormItem(ilocale.T("settings.label.language"), langSelect),
		widget.NewFormItem(ilocale.T("settings.label.autoStart"), autoStartCheck),
		widget.NewFormItem(ilocale.T("settings.label.lightMode"), lightModeCheck),
		widget.NewFormItem(ilocale.T("settings.label.sort"), sortSelect),
		widget.NewFormItem(ilocale.T("settings.label.saveDir"), container.NewBorder(nil, nil, nil, browseBtn, dirEntry)),
		widget.NewFormItem(ilocale.T("settings.label.threads"), threadsEntry),
		widget.NewFormItem(ilocale.T("settings.label.maxConcurrent"), maxConcEntry),
		widget.NewFormItem(ilocale.T("settings.label.minChunk"), chunkSizeRow),
		widget.NewFormItem(ilocale.T("settings.label.ua"), uaEntry),
		widget.NewFormItem(ilocale.T("settings.label.cookies"), cookiesEntry),
		widget.NewFormItem(ilocale.T("settings.label.proxyMode"), proxyModeSelect),
		widget.NewFormItem(ilocale.T("settings.label.proxyURL"), proxyURLEntry),
		widget.NewFormItem(ilocale.T("settings.label.proxyBypass"), proxyBypassEntry),
		widget.NewFormItem(ilocale.T("settings.label.ftp"), ftpPassive),
		widget.NewFormItem(ilocale.T("settings.label.prealloc"), prealloc),
	)

	scroll := container.NewScroll(form)
	scroll.SetMinSize(fyne.NewSize(500, 400))
	return scroll
}

// proxyModeLabel 返回代理模式在当前语言下的显示标签。
func proxyModeLabel(m protocol.ProxyMode) string {
	switch m {
	case protocol.ProxyModeSystem:
		return ilocale.T("settings.proxyMode.system")
	case protocol.ProxyModeDisabled:
		return ilocale.T("settings.proxyMode.disabled")
	case protocol.ProxyModeManual:
		return ilocale.T("settings.proxyMode.manual")
	}
	return ilocale.T("settings.proxyMode.system")
}

// sortLabel 返回任务排序方式在当前语言下的显示标签。
func sortLabel(s store.TaskSort) string {
	switch s {
	case store.SortCreatedDesc:
		return ilocale.T("settings.sort.createdDesc")
	case store.SortCreatedAsc:
		return ilocale.T("settings.sort.createdAsc")
	case store.SortNameAsc:
		return ilocale.T("settings.sort.nameAsc")
	case store.SortNameDesc:
		return ilocale.T("settings.sort.nameDesc")
	}
	return ilocale.T("settings.sort.createdDesc")
}

// buildLanguageSelect 构造「设置 → 语言」下拉框。选项按 ilocale.Supported
// 顺序排列,显示当前语言下的友好名称(自身语言的名字,例如对日文 UI
// 来说,英文条目显示 "English" 而不是 "英文")。变更通过 persist 写回
// svc.SaveSettings,业务进程随后推送 MsgLanguage 触发本进程 applyLanguage。
func buildLanguageSelect(persist func()) *widget.Select {
	opts := make([]string, 0, len(ilocale.Supported))
	labelToTag := map[string]string{}
	for _, tag := range ilocale.Supported {
		lbl := ilocale.T(ilocale.SupportedLabels[tag])
		opts = append(opts, lbl)
		labelToTag[lbl] = tag
	}
	sel := widget.NewSelect(opts, func(s string) {
		tag, ok := labelToTag[s]
		if !ok {
			return
		}
		GlobalSettings.Language = tag
		persist()
	})
	// 当前语言的标签:Normalize 后落到 Supported 中的一项,再用其自描述。
	cur := ilocale.Normalize(GlobalSettings.Language)
	curLbl := ilocale.T(ilocale.SupportedLabels[cur])
	if curLbl != "" {
		sel.SetSelected(curLbl)
	}
	return sel
}

// diskSpaceAt 返回 path 所在文件系统的剩余可用字节数。
func diskSpaceAt(path string) string {
	if path == "" {
		return ilocale.T("main.status.disk.unset")
	}
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	free, total, err := diskUsage(dir)
	if err != nil {
		return ilocale.TF("main.status.disk.error", fmt.Sprintf("不可用 (%v)", err), map[string]any{"Err": err.Error()})
	}
	return ilocale.TF("main.status.disk.format", fmt.Sprintf("可用 %s / 总计 %s", humanBytes(free), humanBytes(total)), map[string]any{
		"Free":  humanBytes(free),
		"Total": humanBytes(total),
	})
}

// humanBytes 将 n 格式化为简短的人类可读字符串。
func humanBytes(n int64) string {
	const (
		KB = 1 << 10
		MB = 1 << 20
		GB = 1 << 30
		TB = 1 << 40
	)
	switch {
	case n >= TB:
		return fmt.Sprintf("%.1f TB", float64(n)/TB)
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/GB)
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/KB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// formatRemainingTime 将剩余秒数渲染为简短的中文字符串。
func formatRemainingTime(secs int) string {
	if secs <= 0 {
		return "--"
	}
	if secs < 60 {
		return ilocale.TF("time.seconds", fmt.Sprintf("%d 秒", secs), map[string]any{"N": secs})
	}
	m := secs / 60
	s := secs % 60
	if m < 60 {
		// 中文/日文通常不需要精确到秒,这里直接用「分」为最小单位;
		// 其它语言也复用「分」模板,只输出整数分即可。
		_ = s
		return ilocale.TF("time.minutes", fmt.Sprintf("%dm", m), map[string]any{"N": m})
	}
	h := m / 60
	m = m % 60
	return ilocale.TF("time.hoursMinutes", fmt.Sprintf("%dh %dm", h, m), map[string]any{"H": h, "M": m})
}
