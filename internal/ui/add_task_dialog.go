// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
)

// showAddTaskDialog 打开“新建下载任务”对话框，并预填全局设置中的内容。
// 这里只显示必要的字段；高级选项位于“设置”对话框中。
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
	savePathEntry.SetText(filepath.Join(GlobalSettings.DefaultSaveDir, "download.bin"))
	browseBtn := widget.NewButton("浏览...", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			savePathEntry.SetText(filepath.Join(uri.Path(), "download.bin"))
		}, win)
	})

	formContent := container.NewVBox(
		makeRow("下载链接 (URL)", urlEntry),
		makeRow("保存路径", container.NewBorder(nil, nil, nil, browseBtn, savePathEntry)),
	)

	d := dialog.NewCustomConfirm("新建下载任务", "开始下载", "取消", formContent, func(c bool) {
		if !c {
			return
		}
		url := urlEntry.Text
		savePath := savePathEntry.Text
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
