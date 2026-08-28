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

// showAddTaskDialog opens the "新建下载任务" dialog with global settings
// pre-populated. Only essential fields appear here; advanced options live
// in the Settings dialog.
func showAddTaskDialog(win fyne.Window, sc *scheduler.Scheduler) {
	// Build a vertically laid-out form with left-aligned labels.
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

	threadsEntry := widget.NewEntry()
	threadsEntry.SetText("")

	usernameEntry := widget.NewEntry()
	usernameEntry.SetPlaceHolder("可选")
	passwordEntry := widget.NewPasswordEntry()
	passwordEntry.SetPlaceHolder("可选")

	authCheck := widget.NewCheck("需要身份认证", func(checked bool) {
		if checked {
			usernameEntry.Show()
			passwordEntry.Show()
		} else {
			usernameEntry.Hide()
			passwordEntry.Hide()
		}
	})
	usernameEntry.Hide()
	passwordEntry.Hide()

	formContent := container.NewVBox(
		makeRow("下载链接 (URL)", urlEntry),
		makeRow("保存路径", container.NewBorder(nil, nil, nil, browseBtn, savePathEntry)),
		makeRow("并发线程数（留空使用全局默认）", threadsEntry),
		authCheck,
		container.NewBorder(nil, nil, widget.NewLabel("用户名"), nil, usernameEntry),
		container.NewBorder(nil, nil, widget.NewLabel("密码"), nil, passwordEntry),
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

		var user, pass string
		if authCheck.Checked {
			user = usernameEntry.Text
			pass = passwordEntry.Text
		}
		chunkCount := GlobalSettings.DefaultThreads
		if s := threadsEntry.Text; s != "" {
			if n, err := parseThreadCount(s); err == nil && n > 0 {
				chunkCount = n
			}
		}

		tk, err := sc.Add(scheduler.AddTaskInput{
			URL:      url,
			SavePath: savePath,
			Protocol: proto,
			Auth: protocol.AuthOptions{
				Username:   user,
				Password:   pass,
				UserAgent:  GlobalSettings.UserAgent,
				Cookies:    GlobalSettings.Cookies,
				FTPPassive: GlobalSettings.FTPPassive,
			},
			ChunkCount: chunkCount,
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

func parseThreadCount(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errBadThreadCount
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

var errBadThreadCount = badThreadCountErr{}

type badThreadCountErr struct{}

func (badThreadCountErr) Error() string { return "bad thread count" }
