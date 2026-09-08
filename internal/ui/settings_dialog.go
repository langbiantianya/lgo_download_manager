// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

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
			log.Printf("ui: save settings: %v", err)
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
	chunkSizeEntry.SetPlaceHolder("整数 MiB")
	chunkSizeEntry.OnChanged = func(s string) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
			GlobalSettings.MinChunkSize = int64(n) * mib
			persist()
		}
	}
	chunkSizeRow := container.NewBorder(nil, nil, nil, widget.NewLabel("MB"), chunkSizeEntry)

	uaEntry := widget.NewEntry()
	uaEntry.SetText(GlobalSettings.UserAgent)
	uaEntry.SetPlaceHolder("可选，自定义 User-Agent")
	uaEntry.OnChanged = func(s string) {
		GlobalSettings.UserAgent = s
		persist()
	}

	cookiesEntry := widget.NewEntry()
	cookiesEntry.SetText(GlobalSettings.Cookies)
	cookiesEntry.SetPlaceHolder("可选，Cookie 字符串")
	cookiesEntry.OnChanged = func(s string) {
		GlobalSettings.Cookies = s
		persist()
	}

	// 同时下载任务数上限。空值/非正数会被 settings.Load 套用默认值;
	// 这里仍接受用户输入并在持久化前做基本校验(正整数),
	// 以免 store 收到 0/负数(EffectiveMaxConcurrent 会兜底但 UX 不直观)。
	maxConcEntry := widget.NewEntry()
	maxConcEntry.SetText(fmt.Sprintf("%d", GlobalSettings.EffectiveMaxConcurrent()))
	maxConcEntry.SetPlaceHolder("正整数，默认 3")
	maxConcEntry.OnChanged = func(s string) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
			GlobalSettings.MaxConcurrent = n
			persist()
		}
	}

// 代理相关控件
proxyURLEntry := widget.NewEntry()
proxyURLEntry.SetText(GlobalSettings.ProxyURL)
proxyURLEntry.SetPlaceHolder("例如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080")
proxyURLEntry.OnChanged = func(s string) {
	GlobalSettings.ProxyURL = strings.TrimSpace(s)
	persist()
}

	proxyBypassEntry := widget.NewEntry()
	proxyBypassEntry.SetText(GlobalSettings.ProxyBypass)
	proxyBypassEntry.SetPlaceHolder("逗号分隔，例如 example.com,*.lan")
	proxyBypassEntry.OnChanged = func(s string) {
		GlobalSettings.ProxyBypass = strings.TrimSpace(s)
		persist()
	}

	proxyModeLabels := map[protocol.ProxyMode]string{
		protocol.ProxyModeSystem:   "使用系统代理（默认）",
		protocol.ProxyModeDisabled: "不使用代理（始终直连）",
		protocol.ProxyModeManual:   "手动设置代理",
	}
	labelToProxyMode := map[string]protocol.ProxyMode{}
	proxyModeOpts := make([]string, 0, len(proxyModeLabels))
	for _, m := range []protocol.ProxyMode{protocol.ProxyModeSystem, protocol.ProxyModeDisabled, protocol.ProxyModeManual} {
		proxyModeOpts = append(proxyModeOpts, proxyModeLabels[m])
		labelToProxyMode[proxyModeLabels[m]] = m
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
	initialLabel := proxyModeLabels[initialMode]
	if initialLabel == "" {
		initialLabel = proxyModeLabels[protocol.ProxyModeSystem]
	}
	proxyModeSelect.SetSelected(initialLabel)
	if initialMode != protocol.ProxyModeManual {
		proxyURLEntry.Hide()
		proxyBypassEntry.Hide()
	}

	ftpPassive := widget.NewCheck("启用 FTP PASV 被动模式", func(checked bool) {
		GlobalSettings.FTPPassive = checked
		persist()
	})
	ftpPassive.SetChecked(GlobalSettings.FTPPassive)

	prealloc := widget.NewCheck("下载时磁盘预分配（连续大文件更稳定）", func(checked bool) {
		GlobalSettings.Prealloc = checked
		persist()
	})
	prealloc.SetChecked(GlobalSettings.Prealloc)

	browseBtn := widget.NewButton("浏览...", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			dirEntry.SetText(uri.Path())
		}, globalWin)
	})

	// 任务列表排序方式（单选下拉框）
	sortLabels := map[store.TaskSort]string{
		store.SortCreatedDesc: "添加时间倒序（最新在前）",
		store.SortCreatedAsc:  "添加时间正序（最老在前）",
		store.SortNameAsc:     "文件名正序（A-Z）",
		store.SortNameDesc:    "文件名倒序（Z-A）",
	}
	sortOpts := make([]string, 0, len(sortLabels))
	labelToSort := map[string]store.TaskSort{}
	for _, s := range store.AllTaskSorts() {
		sortOpts = append(sortOpts, sortLabels[s])
		labelToSort[sortLabels[s]] = s
	}
	sortSelect := widget.NewSelect(sortOpts, func(s string) {
		if sort, ok := labelToSort[s]; ok {
			GlobalSettings.TaskSort = sort
			persist()
		}
	})
	currentLabel := sortLabels[GlobalSettings.TaskSort]
	if currentLabel == "" {
		currentLabel = sortLabels[store.SortCreatedDesc]
	}
	lightModeCheck := widget.NewCheck("轻量模式（关闭主窗口时退出 UI 进程并释放内存）", func(checked bool) {
		GlobalSettings.LightMode = checked
		persist()
	})
	lightModeCheck.SetChecked(GlobalSettings.LightMode)

	sortSelect.SetSelected(currentLabel)
	form := widget.NewForm(
		widget.NewFormItem("默认保存目录", container.NewBorder(nil, nil, nil, browseBtn, dirEntry)),
		widget.NewFormItem("默认并发线程数", threadsEntry),
		widget.NewFormItem("同时下载任务数", maxConcEntry),
		widget.NewFormItem("最小分块大小", chunkSizeRow),
		widget.NewFormItem("默认 User-Agent", uaEntry),
		widget.NewFormItem("默认 Cookie", cookiesEntry),
		widget.NewFormItem("HTTP/HTTPS 代理模式", proxyModeSelect),
		widget.NewFormItem("代理地址", proxyURLEntry),
		widget.NewFormItem("代理绕过列表", proxyBypassEntry),
		widget.NewFormItem("FTP 模式", ftpPassive),
		widget.NewFormItem("磁盘预分配", prealloc),
		widget.NewFormItem("轻量模式", lightModeCheck),
		widget.NewFormItem("任务列表排序", sortSelect),
	)

	scroll := container.NewScroll(form)
	scroll.SetMinSize(fyne.NewSize(500, 400))
	return scroll
}

// diskSpaceAt 返回 path 所在文件系统的剩余可用字节数。
func diskSpaceAt(path string) string {
	if path == "" {
		return "未设置"
	}
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	free, total, err := diskUsage(dir)
	if err != nil {
		return fmt.Sprintf("不可用 (%v)", err)
	}
	return fmt.Sprintf("可用 %s / 总计 %s", humanBytes(free), humanBytes(total))
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
		return fmt.Sprintf("剩 %d 秒", secs)
	}
	m := secs / 60
	s := secs % 60
	if m < 60 {
		return fmt.Sprintf("剩 %dm %ds", m, s)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("剩 %dh %dm", h, m)
}
