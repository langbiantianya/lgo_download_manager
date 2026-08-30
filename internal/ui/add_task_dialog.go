// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
)

// uniqueSuffixRegex 匹配 'name(N)' 形式的后缀——用于在文件名已存在时递增编号。
var uniqueSuffixRegex = regexp.MustCompile(`^(.*?)(\((\d+)\))$`)

// uniqueSavePath 若 path 指向的文件已存在，则在扩展名前插入 (N) 直到
// 找到不存在的名字（N 从 1 开始）。目录不存在或 IO 错误时返回原 path。
func uniqueSavePath(path string) string {
	if _, err := os.Stat(path); err != nil {
		// 不存在或无法访问——直接返回原 path，由调用方按需处理错误。
		return path
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]

	// 如果已经是 name(N) 形式，则从 N+1 开始递增；否则从 (1) 开始。
	start := 1
	if m := uniqueSuffixRegex.FindStringSubmatch(stem); m != nil {
		if n, err := strconv.Atoi(m[3]); err == nil {
			start = n + 1
			stem = m[1]
		}
	}
	for n := start; n < 10000; n++ {
		candidate := filepath.Join(dir, stem+"("+strconv.Itoa(n)+")"+ext)
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
	}
	return path
}

// showAddTaskDialog 打开"新建下载任务"对话框，并预填全局设置中的内容。
// 这里只显示必要的字段；高级选项位于"设置"对话框中。
//
// URL 变更时自动从路径末尾提取文件名填入保存路径，并探测文件大小进行预览。
func showAddTaskDialog(win fyne.Window, sc *scheduler.Scheduler) {
	// 构建一个纵向布局、标签左对齐的表单。
	makeRow := func(label string, w fyne.CanvasObject) *fyne.Container {
		lbl := widget.NewLabel(label)
		lbl.Alignment = fyne.TextAlignLeading
		lbl.TextStyle.Bold = true
		return container.NewBorder(nil, nil, lbl, nil, w)
	}
	urlEntry := widget.NewEntry()
	urlEntry.SetPlaceHolder("https://...")
	urlEntry.Validator = notEmptyValidator()

	savePathEntry := widget.NewEntry()
	savePathEntry.SetText(uniqueSavePath(filepath.Join(GlobalSettings.DefaultSaveDir, "download.bin")))
	browseBtn := widget.NewButton("浏览...", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			savePathEntry.SetText(uniqueSavePath(filepath.Join(uri.Path(), filepath.Base(savePathEntry.Text))))
		}, win)
	})

	// 文件大小预览标签
	sizeLabel := widget.NewLabel("")
	sizeLabel.Alignment = fyne.TextAlignTrailing
	sizeLabel.TextStyle.Italic = true

	// 从 URL 提取文件名并更新保存路径，同时探测文件大小
	updateFromURL := func(rawURL string) {
		u, err := url.Parse(rawURL)
		if err != nil || u.Path == "" {
			sizeLabel.SetText("")
			return
		}
		filename := filepath.Base(u.Path)
		if savePathEntry.Text == "" || savePathEntry.Text == filepath.Join(GlobalSettings.DefaultSaveDir, "download.bin") {
			savePathEntry.SetText(uniqueSavePath(filepath.Join(GlobalSettings.DefaultSaveDir, filename)))
		}
		// 异步探测文件大小
		go func() {
			proto, err := protocol.DetectKind(rawURL, "")
			if err != nil {
				fyne.Do(func() { sizeLabel.SetText("") })
				return
			}
			driver, err := protocol.New(rawURL, proto, protocol.Auth{AuthOptions: protocol.AuthOptions{
				UserAgent:  GlobalSettings.UserAgent,
				Cookies:    GlobalSettings.Cookies,
				FTPPassive: GlobalSettings.FTPPassive,
			}})
			if err != nil {
				fyne.Do(func() { sizeLabel.SetText("") })
				return
			}
			defer driver.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			caps, err := driver.Probe(ctx)
			if err != nil {
				fyne.Do(func() { sizeLabel.SetText("") })
				return
			}
			if caps.TotalSize <= 0 {
				fyne.Do(func() { sizeLabel.SetText("") })
				return
			}
			fyne.Do(func() {
				sizeLabel.SetText(formatSize(caps.TotalSize))
			})
		}()
	}

	// URL 变化时触发
	urlEntry.OnChanged = func(s string) {
		if s == "" {
			sizeLabel.SetText("")
			return
		}
		updateFromURL(s)
	}

	formContent := container.NewVBox(
		makeRow("下载链接 (URL)", urlEntry),
		makeRow("文件大小", sizeLabel),
		makeRow("保存路径", container.NewBorder(nil, nil, nil, browseBtn, savePathEntry)),
	)

	d := dialog.NewCustomConfirm("新建下载任务", "开始下载", "取消", formContent, func(c bool) {
		if !c {
			return
		}
		url := urlEntry.Text
		savePath := uniqueSavePath(savePathEntry.Text)
		if url == "" || savePath == "" {
			return
		}
		proto, err := protocol.DetectKind(url, "")
		if err != nil {
			dialog.ShowError(err, win)
			return
		}

		tk, err := sc.Add(scheduler.AddTaskInput{
			URL:      url,
			SavePath: savePath,
			Protocol: proto,
			Auth: protocol.AuthOptions{
				UserAgent:  GlobalSettings.UserAgent,
				Cookies:    GlobalSettings.Cookies,
				FTPPassive: GlobalSettings.FTPPassive,
			},
			ChunkCount:   GlobalSettings.DefaultThreads,
			MinChunkSize: GlobalSettings.MinChunkSize,
		})
		if err != nil {
			dialog.ShowError(err, win)
			return
		}
		_ = sc.Start(tk.ID)
	}, win)
	d.Resize(fyne.NewSize(600, d.MinSize().Height))
	d.Show()
}

func notEmptyValidator() fyne.StringValidator {
	return func(s string) error {
		if s == "" {
			return errEmpty
		}
		return nil
	}
}

var errEmpty = errEmptyField{}

type errEmptyField struct{}

func (errEmptyField) Error() string { return "required" }

// formatSize 将字节数格式化为人类可读字符串。
func formatSize(n int64) string {
	const KB = 1 << 10
	const MB = 1 << 20
	const GB = 1 << 30
	switch {
	case n >= GB:
		return strconv.FormatFloat(float64(n)/GB, 'f', 2, 64) + " GB"
	case n >= MB:
		return strconv.FormatFloat(float64(n)/MB, 'f', 2, 64) + " MB"
	case n >= KB:
		return strconv.FormatFloat(float64(n)/KB, 'f', 2, 64) + " KB"
	default:
		return strconv.FormatInt(n, 10) + " B"
	}
}
