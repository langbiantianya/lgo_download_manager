// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/protocol"
)

// archiveCompressionExts 列出常见的归档/压缩单段扩展名。任何"<x>.<archive>"
// 形式都会被 splitExt 当作复合扩展名处理——例如 .tar.gz、.cpio.gz、.img.zst。
// 这样新出现的压缩格式（.sz、.br、.zst 等）无需改动代码即可正确拆分。
//
// 注意：第二段必须以 'archive'/'compression' 角色出现；常见的次级扩展名覆盖了
// 当前主流（gzip / bzip2 / xz / zstd / lz 系列），未来若有新压缩格式被广泛
// 使用，只需在此追加一行。
var archiveCompressionExts = []string{
	".gz", ".bz2", ".xz", ".zst", ".lz", ".lzma", ".lzo", ".br", ".sz", ".Z",
}

// uniqueSuffixRegex 匹配 'name(N)' 形式的后缀——用于在文件名已存在时递增编号。
var uniqueSuffixRegex = regexp.MustCompile(`^(.*?)(\((\d+)\))$`)

// splitExt 拆分文件名后缀为 (stem, ext)。优先识别复合扩展名：
// 当 base 形如 "<something>.<archiveCompressionExt>" 且 <archiveCompressionExt>
// 是已知的压缩归档格式时，把整段当作 ext。否则退化为 filepath.Ext。
// 大小写不敏感匹配，但返回的 ext 保留原始大小写。
//
// 这种启发式无需枚举所有可能的复合扩展名——只要新增了压缩格式（往
// archiveCompressionExts 追加），新的复合形式就会自动工作。
func splitExt(base string) (string, string) {
	lower := strings.ToLower(base)
	// 找到最后一个 '.'，尝试把它当作复合扩展名的分界点。
	idx := strings.LastIndex(lower, ".")
	if idx <= 0 || idx == len(lower)-1 {
		// 没有 '.' 或以 '.' 结尾——退回 filepath.Ext。
		return base[:len(base)-len(filepath.Ext(base))], filepath.Ext(base)
	}
	candidate := lower[idx:] // 例如 ".gz"
	for _, comp := range archiveCompressionExts {
		if candidate == comp {
			// 第二段是已知的压缩格式。再往左看是否有"更外层"的 .xxx。
			before := lower[:idx] // 例如 "archive.tar"
			if j := strings.LastIndex(before, "."); j > 0 {
				return base[:j], base[j:]
			}
		}
	}
	return base[:len(base)-len(filepath.Ext(base))], filepath.Ext(base)
}

// uniqueSavePath 若 path 指向的文件已存在，则在扩展名前插入 (N) 直到
// 找到不存在的名字（N 从 1 开始）。目录不存在或 IO 错误时返回原 path。
func uniqueSavePath(path string) string {
	if _, err := os.Stat(path); err != nil {
		// 不存在或无法访问——直接返回原 path，由调用方按需处理错误。
		return path
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	stem, ext := splitExt(base)

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

// showAddTaskDialog 打开"新建下载任务"窗口，并预填全局设置中的内容。
// 这里只显示必要的字段；高级选项位于"设置"窗口中。
//
// 弹出方式为独立窗口（与任务详情一致），用户可以同时与主窗口交互。
// URL 变更时自动从路径末尾提取文件名填入保存路径，并异步请求业务进程
// 探测文件大小进行预览（探针复用业务侧代理配置）。
func showAddTaskDialog(parent fyne.Window, svc Service) {
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
		}, parent)
	})

	// 文件大小预览标签
	sizeLabel := widget.NewLabel("")
	sizeLabel.Alignment = fyne.TextAlignTrailing
	sizeLabel.TextStyle.Italic = true

	// 关闭窗口的工具函数——确认和取消两条路径共用，避免重复 SetOnClosed 链。
	var closeWin func()
	var startDownload func()

	// 从 URL 提取文件名并更新保存路径，同时异步探测文件大小
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
		// 异步探测（经业务进程，代理配置一致）。失败时静默清空预览。
		go func() {
			size, perr := svc.Probe(rawURL)
			if perr != nil {
				fyne.Do(func() { sizeLabel.SetText("") })
				return
			}
			if size <= 0 {
				fyne.Do(func() { sizeLabel.SetText("") })
				return
			}
			fyne.Do(func() {
				sizeLabel.SetText(formatSize(size))
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

	w := fyne.CurrentApp().NewWindow("新建下载任务")
	w.Resize(fyne.NewSize(640, 260))
	w.CenterOnScreen()

	// 「开始下载」回调——校验 + 创建任务 + 关闭窗口。
	startDownload = func() {
		url := urlEntry.Text
		savePath := uniqueSavePath(savePathEntry.Text)
		if url == "" || savePath == "" {
			return
		}
		// 用 GlobalSettings 预填默认 UA/Cookie:用户改设置后再开对话框,
		// 立即生效;留空也照样透传(业务进程据此把 UA 字段保留为空)。
		// 仅设置中已存在的字段才会真正下发,避免给 URL/FTP 驱动传
		// 与协议无关的代理/凭据导致副作用。
		taskAuth := protocol.AuthOptions{
			UserAgent: GlobalSettings.UserAgent,
			Cookies:   GlobalSettings.Cookies,
		}
		tk, err := svc.AddTask(AddTaskInput{
			URL:          url,
			SavePath:     savePath,
			ChunkCount:   GlobalSettings.DefaultThreads,
			MinChunkSize: GlobalSettings.MinChunkSize,
			Auth:         taskAuth,
		})
		if err != nil {
			dialog.ShowError(err, parent)
			return
		}
		if err := svc.Start(tk.ID); err != nil {
			dialog.ShowError(err, parent)
			return
		}
		if closeWin != nil {
			closeWin()
		}
	}

	confirmBtn := widget.NewButtonWithIcon("开始下载", theme.ConfirmIcon(), startDownload)
	confirmBtn.Importance = widget.HighImportance
	cancelBtn := widget.NewButtonWithIcon("取消", theme.CancelIcon(), func() {
		if closeWin != nil {
			closeWin()
		}
	})
	footer := container.NewBorder(nil, nil, nil, nil, container.NewHBox(layout.NewSpacer(), cancelBtn, confirmBtn))

	w.SetOnClosed(func() {
		closeWin = nil
	})

	w.SetContent(container.NewBorder(nil, footer, nil, nil, container.NewPadded(formContent)))

	// URL Entry 上按 Enter 也触发「开始下载」，避免用户每次都得切到按钮。
	urlEntry.OnSubmitted = func(string) { startDownload() }
	savePathEntry.OnSubmitted = func(string) { startDownload() }

	// 让 closeWin 在所有控件构造完成后指向真实关闭逻辑。
	closeWin = w.Close

	w.Show()
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
