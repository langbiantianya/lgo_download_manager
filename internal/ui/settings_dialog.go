// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

const mib = int64(1 << 20)

// GlobalSettings 是“新建任务”预填字段时以及 main.go 中的 URL 启动器
// 使用的实时设置。它与 SQLite 存储 settings 表中的持久化行一一对应，
// 并在启动时由 LoadSettings 加载。
var GlobalSettings store.Settings

// LoadSettings 从存储中读取持久化的设置，并将其覆盖到 GlobalSettings 上。
// 对于零值具有歧义（空字符串 / 0）的字段，会应用默认值——首次运行时，
// 行级“未存储值”标记就是空的 DefaultSaveDir。首次保存后，
// 所有字段将按原值持久化。
func LoadSettings(st *store.Store) error {
	persisted, err := st.LoadSettings()
	if err != nil {
		return err
	}
	firstRun := persisted.DefaultSaveDir == ""
	if firstRun {
		home, _ := os.UserHomeDir()
		persisted.DefaultSaveDir = filepath.Join(home, "Downloads")
		persisted.DefaultThreads = 4
		persisted.MinChunkSize = 10 * mib
		persisted.UserAgent = "Wget/1.21.3"
		persisted.FTPPassive = true
		persisted.Prealloc = true
		persisted.TaskSort = store.SortCreatedDesc
	}
	GlobalSettings = persisted
	if firstRun {
		// 持久化默认值，以便后续加载时能找到真实数据行。
		_ = SaveSettings(st)
	}
	return nil
}
// SaveSettings 将 GlobalSettings 写入存储。每次 UI 修改后均可安全调用。
func SaveSettings(st *store.Store) error {
	return st.SaveSettings(GlobalSettings)
}
func buildSettingsContent(sc *scheduler.Scheduler, onChange func()) fyne.CanvasObject {
	persist := func() {
		if globalStore == nil {
			return
		}
		if err := SaveSettings(globalStore); err != nil {
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
	sortSelect.SetSelected(currentLabel)
	form := widget.NewForm(
		widget.NewFormItem("默认保存目录", container.NewBorder(nil, nil, nil, browseBtn, dirEntry)),
		widget.NewFormItem("默认并发线程数", threadsEntry),
		widget.NewFormItem("最小分块大小", chunkSizeRow),
		widget.NewFormItem("默认 User-Agent", uaEntry),
		widget.NewFormItem("默认 Cookie", cookiesEntry),
		widget.NewFormItem("FTP 模式", ftpPassive),
		widget.NewFormItem("磁盘预分配", prealloc),
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
