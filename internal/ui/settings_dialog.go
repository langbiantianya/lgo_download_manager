package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/scheduler"
)

// settings holds user-tunable defaults applied to new downloads.
type settings struct {
	DefaultSaveDir string
	DefaultThreads int
	UserAgent      string
	Cookies        string
	FTPPassive     bool
	Prealloc       bool
}

// GlobalSettings is the live settings used by "新建任务" when prefilling fields
// and by the URL launcher in main.go.
var GlobalSettings settings

func init() {
	home, _ := os.UserHomeDir()
	saveDir := filepath.Join(home, "Downloads")
	_ = runtime.GOOS // reserved for future platform-specific tweaks
	GlobalSettings = settings{
		DefaultSaveDir:  saveDir,
		DefaultThreads:  16,
		UserAgent:      "Wget/1.21.3",
		Cookies:        "",
		FTPPassive:     true,
		Prealloc:       true,
	}
}
func buildSettingsContent(sc *scheduler.Scheduler) fyne.CanvasObject {
	dirEntry := widget.NewEntry()
	dirEntry.SetText(GlobalSettings.DefaultSaveDir)
	dirEntry.SetPlaceHolder("/path/to/Downloads")
	dirEntry.OnChanged = func(s string) { GlobalSettings.DefaultSaveDir = s }

	threadsEntry := widget.NewEntry()
	threadsEntry.SetText(fmt.Sprintf("%d", GlobalSettings.DefaultThreads))
	threadsEntry.OnChanged = func(s string) {
		if n, err := parseThreadCount(s); err == nil && n > 0 {
			GlobalSettings.DefaultThreads = n
		}
	}

	uaEntry := widget.NewEntry()
	uaEntry.SetText(GlobalSettings.UserAgent)
	uaEntry.SetPlaceHolder("可选，自定义 User-Agent")
	uaEntry.OnChanged = func(s string) { GlobalSettings.UserAgent = s }

	cookiesEntry := widget.NewEntry()
	cookiesEntry.SetText(GlobalSettings.Cookies)
	cookiesEntry.SetPlaceHolder("可选，Cookie 字符串")
	cookiesEntry.OnChanged = func(s string) { GlobalSettings.Cookies = s }

	ftpPassive := widget.NewCheck("启用 FTP PASV 被动模式", func(checked bool) {
		GlobalSettings.FTPPassive = checked
	})
	ftpPassive.SetChecked(GlobalSettings.FTPPassive)

	prealloc := widget.NewCheck("下载时磁盘预分配（连续大文件更稳定）", func(checked bool) {
		GlobalSettings.Prealloc = checked
	})
	prealloc.SetChecked(GlobalSettings.Prealloc)

	form := widget.NewForm(
		widget.NewFormItem("默认保存目录", container.NewBorder(nil, nil, nil, widget.NewButton("浏览...", func() {
			dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
				if err != nil || uri == nil {
					return
				}
				dirEntry.SetText(uri.Path())
			}, globalWin)
		}), dirEntry)),
		widget.NewFormItem("默认并发线程数", threadsEntry),
		widget.NewFormItem("默认 User-Agent", uaEntry),
		widget.NewFormItem("默认 Cookie", cookiesEntry),
		widget.NewFormItem("FTP 模式", ftpPassive),
		widget.NewFormItem("磁盘预分配", prealloc),
	)

	scroll := container.NewScroll(form)
	scroll.SetMinSize(fyne.NewSize(500, 400))
	return scroll
}

// diskSpaceAt reports the free bytes on the filesystem holding path.
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

// humanBytes formats n as a short human-readable string.
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

// formatRemainingTime renders remaining seconds as a short Chinese string.
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