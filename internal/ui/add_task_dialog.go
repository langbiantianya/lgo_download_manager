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
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/ipc"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/ui/nativefolder"
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
	showAddTaskDialogWithPreset(parent, svc, addTaskPreset{})
}

// addTaskPreset 描述对话框打开时的可选预填值。所有字段都可选——空值
// 表示「保持默认/GlobalSettings」。用于:
//
//   - lgom:// URL 转发:从业务进程收到的 ShowAddTaskParams 转过来;
//   - 未来:拖拽、剪贴板粘贴等本地快捷入口。
//
// UA / Cookies 非空时,在「开始下载」时覆盖 GlobalSettings 的对应字段
// (URL 自带的凭据比全局默认更具体)。其它字段保持原行为不变。
type addTaskPreset struct {
	URL     string
	Name    string
	UA      string
	Cookies string
}

// ShowAddTaskDialogForURL 由 MsgShowAddTask 回调调用:把 IPC 负载转成
// 对话框预填值并打开。预期在 Fyne 事件线程上执行。
//
// URL 自带的 UA / Cookies 仅在该次提交中有效——它们由 showAddTaskDialog
// 内部作为 taskAuth 的覆盖值传入,关闭/取消后即丢弃;再次点开「新建
// 任务」时回到 GlobalSettings 的常规路径。
func ShowAddTaskDialogForURL(svc Service, params ipc.ShowAddTaskParams) {
	showAddTaskDialogWithPreset(nil, svc, addTaskPreset{
		URL:     params.URL,
		Name:    params.Name,
		UA:      params.UA,
		Cookies: params.Cookies,
	})
}

// showAddTaskDialogWithPreset 是 showAddTaskDialog 的内部实现。
// preset 仅在窗口首次构造时被读取,运行期间用户对 urlEntry 的修改
// 不会被覆盖。preset.Name 仅在用户未自定义保存路径时参与文件名推导。
func showAddTaskDialogWithPreset(parent fyne.Window, svc Service, preset addTaskPreset) {
	// 构建一个纵向布局、标签左对齐的表单。
	makeRow := func(label string, w fyne.CanvasObject) *fyne.Container {
		lbl := widget.NewLabel(label)
		lbl.Alignment = fyne.TextAlignLeading
		lbl.TextStyle.Bold = true
		return container.NewBorder(nil, nil, lbl, nil, w)
	}
	urlEntry := widget.NewEntry()
	urlEntry.SetPlaceHolder(ilocale.T("addTask.url.placeholder"))
	urlEntry.Validator = notEmptyValidator()

	defaultName := "download.bin"
	if preset.URL != "" {
		if u, err := url.Parse(preset.URL); err == nil && u.Path != "" {
			if base := filepath.Base(u.Path); base != "" && base != "." && base != "/" {
				defaultName = base
			}
		}
	}
	// preset.Name 覆盖 URL 末端推导出的文件名(lgom:// 的 &name= 参数
	// 比路径末尾更具体,例如镜像源常通过它指定真实文件名)。
	if preset.Name != "" {
		defaultName = preset.Name
	}
	savePathEntry := widget.NewEntry()
	savePathEntry.SetText(uniqueSavePath(filepath.Join(GlobalSettings.DefaultSaveDir, defaultName)))
	// 保存路径的标签会随 URL 形态切换:普通任务是「保存路径」(单个文件),
	// BT 任务是「保存目录」(种子的全部文件都落在该目录之下)。
	savePathLabel := widget.NewLabel(ilocale.T("addTask.savePath.label"))
	savePathLabel.TextStyle.Bold = true
	// 「浏览」按钮：调用系统原生文件夹选择对话框(资源管理器 / Finder /
	// GTK/Qt 通用 chooser),不走 fyne 自带的 dialog.ShowFolderOpen。
	// 同步阻塞在 UI 线程上,直接在 goroutine 中执行,选择结束后用
	// fyne.Do 把结果切回 UI 线程。
	browseBtn := widget.NewButton(ilocale.T("addTask.browse"), func() {
		// widget 的字段只能在事件线程上读:goroutine 里直接读
		// savePathEntry.Text 是数据竞争(go test -race 会报出来)。
		// 这里先把需要的值取出来再进 goroutine。
		cur := savePathEntry.Text
		base := filepath.Base(cur)
		if base == "." || base == string(filepath.Separator) {
			base = defaultName
		}
		startDir := filepath.Dir(cur)
		if startDir == "" || startDir == "." {
			startDir = GlobalSettings.DefaultSaveDir
		}
		go func() {
			chosen, err := nativefolder.PickFolder(startDir)
			if err != nil {
				if nativefolder.IsCancelled(err) {
					return
				}
				fyne.Do(func() { showError(parent, err) })
				return
			}
			fyne.Do(func() {
				savePathEntry.SetText(uniqueSavePath(filepath.Join(chosen, base)))
			})
		}()
	})

	// 文件大小预览标签
	sizeLabel := widget.NewLabel("")
	sizeLabel.Alignment = fyne.TextAlignTrailing
	sizeLabel.TextStyle.Italic = true

	// BT 任务的保存路径语义与普通任务不同(目录而非文件),用一行灰色
	// 说明在 URL 是磁力链接/种子文件时提示用户。默认隐藏。
	btHint := widget.NewLabel(ilocale.T("addTask.bt.hint"))
	btHint.TextStyle.Italic = true
	btHint.Wrapping = fyne.TextWrapWord
	btHint.Hide()

	// 关闭窗口的工具函数——确认和取消两条路径共用，避免重复 SetOnClosed 链。
	var closeWin func()
	var startDownload func()

	// autoSavePath 记录「对话框自动推导出来的保存路径」。用户一旦手动改过,
	// 后续 URL 变化不再覆盖用户输入——这既保住了人工选择,也让 BT/BT 之间
	// 的 URL 切换能自然刷新默认目录名。
	autoSavePath := filepath.Join(GlobalSettings.DefaultSaveDir, defaultName)

	// probeDebounce 是 URL 变化后等待多久才真正发起探测。连续键击时每敲
	// 一个字符就发一次探测请求,会在网络上打出一串无用的请求——这里等到
	// 用户停下来再探一次。
	const probeDebounce = 400 * time.Millisecond

	// probe 合并连续键击产生的探测请求,并给每次探测编号:只有最后一次
	// 探测的结果会被采纳。早先发起的探测可能回来得更晚,不能让它把大小
	// 预览覆盖成旧值。
	probe := &debouncer{delay: probeDebounce}

	// probeSize 在后台探测 URL 对应的文件大小,并把结果显示到大小标签上。
	// 探测失败或大小未知时清空预览(静默,大小只是个预览)。
	probeSize := func(rawURL string, seq uint64) {
		go func() {
			size, perr := svc.Probe(rawURL)
			if perr != nil || size <= 0 {
				fyne.Do(func() {
					if probe.IsCurrent(seq) {
						sizeLabel.SetText("")
					}
				})
				return
			}
			fyne.Do(func() {
				if probe.IsCurrent(seq) {
					sizeLabel.SetText(formatBytes(size))
				}
			})
		}()
	}

	// 从 URL 提取文件名并更新保存路径，同时异步探测文件大小
	updateFromURL := func(rawURL string) {
		nextDefault := filepath.Join(GlobalSettings.DefaultSaveDir, defaultName)
		if protocol.IsBitTorrent(rawURL) {
			// BT 任务的保存路径是「任务目录」:磁力链接用 dn 参数
			// (没有则 infohash 前缀),.torrent 用去掉后缀的文件名。
			savePathLabel.SetText(ilocale.T("addTask.saveDir.label"))
			btHint.Show()
			dirName := scheduler.BittorrentDefaultDir(rawURL)
			if dirName == "" {
				dirName = "bt-task"
			}
			nextDefault = filepath.Join(GlobalSettings.DefaultSaveDir, dirName)
		} else {
			savePathLabel.SetText(ilocale.T("addTask.savePath.label"))
			btHint.Hide()
			if u, err := url.Parse(rawURL); err == nil && u.Path != "" {
				nextDefault = filepath.Join(GlobalSettings.DefaultSaveDir, filepath.Base(u.Path))
			}
		}
		// 只在「用户没改过推导值」时刷新保存路径:否则手动选择会被 URL
		// 的每次键入覆盖掉。
		if savePathEntry.Text == "" || savePathEntry.Text == autoSavePath {
			savePathEntry.SetText(uniqueSavePath(nextDefault))
		}
		autoSavePath = nextDefault
	}

	// URL 变化时触发
	urlEntry.OnChanged = func(s string) {
		if s == "" {
			sizeLabel.SetText("")
			return
		}
		updateFromURL(s)
		// 探测合并:连续键击时只探最后一次,免得每敲一个字就发一次请求。
		probe.Trigger(func(seq uint64) {
			fyne.Do(func() { probeSize(s, seq) })
		})
	}

	formContent := container.NewVBox(
		makeRow(ilocale.T("addTask.url.label"), urlEntry),
		makeRow(ilocale.T("addTask.size.label"), sizeLabel),
		container.NewBorder(nil, nil, savePathLabel, browseBtn, savePathEntry),
		btHint,
	)

	w := fyne.CurrentApp().NewWindow(ilocale.T("addTask.window.title"))
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
		//
		// preset 中的 UA / Cookies 是 lgom:// URL 自带的凭据
		// (lgom://download?...&ua=...&cookies=...),优先级高于
		// GlobalSettings——非空时直接覆盖。覆盖仅作用在本次 AddTask 上,
		// 关闭/取消后即丢弃。
		ua := GlobalSettings.UserAgent
		if preset.UA != "" {
			ua = preset.UA
		}
		cookies := GlobalSettings.Cookies
		if preset.Cookies != "" {
			cookies = preset.Cookies
		}
		taskAuth := protocol.AuthOptions{
			UserAgent: ua,
			Cookies:   cookies,
		}
		// 协议由对话框显式给出(magnet:/.torrent → BT),业务进程据此
		// 走 BT 引擎;识别失败时留空,由业务进程按 URL 兜底判定。
		kind, _ := protocol.DetectKind(url, "")
		tk, err := svc.AddTask(AddTaskInput{
			URL:          url,
			SavePath:     savePath,
			Protocol:     kind,
			ChunkCount:   GlobalSettings.DefaultThreads,
			MinChunkSize: GlobalSettings.MinChunkSize,
			Auth:         taskAuth,
		})
		if err != nil {
			showError(parent, err)
			return
		}
		if err := svc.Start(tk.ID); err != nil {
			showError(parent, err)
			return
		}
		if closeWin != nil {
			closeWin()
		}
	}

	confirmBtn := widget.NewButtonWithIcon(ilocale.T("addTask.start"), theme.ConfirmIcon(), startDownload)
	confirmBtn.Importance = widget.HighImportance
	cancelBtn := widget.NewButtonWithIcon(ilocale.T("addTask.cancel"), theme.CancelIcon(), func() {
		if closeWin != nil {
			closeWin()
		}
	})
	footer := container.NewBorder(nil, nil, nil, nil, container.NewHBox(layout.NewSpacer(), cancelBtn, confirmBtn))

	w.SetOnClosed(func() {
		closeWin = nil
		// 对话框关了就不该再探测:停掉待触发的定时器,已经发出的探测
		// 回来后也只会更新一个没人看的标签。
		probe.Stop()
	})

	w.SetContent(container.NewBorder(nil, footer, nil, nil, container.NewPadded(formContent)))

	// URL Entry 上按 Enter 也触发「开始下载」，避免用户每次都得切到按钮。
	urlEntry.OnSubmitted = func(string) { startDownload() }
	savePathEntry.OnSubmitted = func(string) { startDownload() }

	// 让 closeWin 在所有控件构造完成后指向真实关闭逻辑。
	closeWin = w.Close

	// 预设 URL 必须在 SetContent/OnChanged 注册之后再写,否则 OnChanged
	// 第一次回调拿到的 savePathEntry.Text 还是默认占位符,会出现「用户
	// 没碰 URL 但保存路径被刷新」的怪异行为。同时显式触发一次 updateFromURL,
	// 让 URL 末尾文件名 + 大小预览就位。
	if preset.URL != "" {
		urlEntry.SetText(preset.URL)
	}

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

// errEmpty 在每次 Error() 时取 ilocale 当前翻译——切语言后下一次校验
// 失败就会用新语言的「必填」措辞显示,无需重建对话框。
var errEmpty = errEmptyField{}

type errEmptyField struct{}

func (errEmptyField) Error() string { return ilocale.T("addTask.required") }


