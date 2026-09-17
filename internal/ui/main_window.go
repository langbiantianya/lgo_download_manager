// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package ui 为下载管理器提供基于 Fyne 的图形界面。
// 线程安全：业务服务（scheduler 等）运行在业务进程中；所有 GUI
// 修改都通过 fyne.Do() 在 Fyne 事件线程上进行。
package ui

import (
	"fmt"
	"image/color"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/store"
)

// diskRefreshInterval 是状态栏磁盘空间信息的刷新间隔。磁盘占用会因为
// 其它进程的写入而变化，只在启动时算一次会长期显示过期数据。
const diskRefreshInterval = 30 * time.Second

// MainWindow 保存所有 GUI 状态以及顶层 Fyne 窗口。
type MainWindow struct {
	app     fyne.App
	svc     Service
	win     fyne.Window
	content *fyne.Container

	filter    binding.String
	taskList  *taskList
	statusBar *statusBar

	// 可翻译 widget 引用,语言切换时由 applyLanguage 重新写入。
	winTitle       string
	tbNew          *widget.Button
	tbPauseAll     *widget.Button
	tbResumeAll    *widget.Button
	tbSettings     *widget.Button
	searchEntry    *widget.Entry
	sidebarHeader  *widget.Label
	sidebarRadios  *widget.RadioGroup
	diskLabel      *widget.Label

	// settingsHeaderTitle 在设置页切换时即时翻译(不像其它 widget 持久持有)。
	settingsPageOpen bool

	unsub func()

	// diskTicker 周期性刷新状态栏的磁盘空间；关闭 diskDone 即退出该循环。
	diskTicker *time.Ticker
	diskDone   chan struct{}
}

// NewMainWindow 构建附加到 app 的主窗口。
//
// 必须在 Fyne 事件线程上调用（通常在 app.New…() 之后、
// a.Run() 之前的主 goroutine 中），因为 widget 构造器依赖
// fyne.CurrentApp() 解析为刚创建的 app。
func NewMainWindow(a fyne.App, svc Service) *MainWindow {
	GlobalSettings = svc.Settings()
	setGlobalSvc(svc)
	m := &MainWindow{
		app:    a,
		svc:    svc,
		filter: binding.NewString(),
	}
	m.taskList = newTaskList(svc, m.filter)
	m.statusBar = newStatusBar()
	m.diskLabel = m.statusBar.diskLabel // 供 applyLanguage 找到标签引用
	m.buildMainUI()
	m.applyLanguage() // 首启把当前 ilocale.Current 应用到所有持久 widget
	m.subscribe()
	m.statusBar.refreshDiskSpace()
	m.startDiskTicker()
	return m
}
func (m *MainWindow) buildMainUI() {
	m.win = m.app.NewWindow("")
	m.winTitle = "" // applyLanguage 写入
	m.content = container.NewBorder(
		m.buildToolbar(),
		m.statusBar.container(),
		nil, nil,
		m.buildMainSplit(),
	)
	m.win.SetContent(m.content)
	m.win.Resize(fyne.NewSize(1000, 640))
	m.win.CenterOnScreen()
	setGlobalWindow(m.win)
	m.win.SetCloseIntercept(m.onCloseRequested)
}

// applyLanguage 把 ilocale.Current 对应的语言应用到所有持久持有的
// widget(title/工具栏/侧边栏/状态栏标签)。由 NewMainWindow 首启调用,
// 以及 MsgLanguage 回调触发。必须在 Fyne 事件线程上调用(write widget)。
func (m *MainWindow) applyLanguage() {
	if m.win != nil {
		title := ilocale.T("main.window.title")
		if title != m.winTitle {
			m.winTitle = title
			m.win.SetTitle(title)
		}
	}
	if m.tbNew != nil {
		m.tbNew.SetText(ilocale.T("main.toolbar.new"))
		m.tbPauseAll.SetText(ilocale.T("main.toolbar.pauseAll"))
		m.tbResumeAll.SetText(ilocale.T("main.toolbar.resumeAll"))
		m.tbSettings.SetText(ilocale.T("main.toolbar.settings"))
	}
	if m.searchEntry != nil {
		m.searchEntry.SetPlaceHolder(ilocale.T("main.toolbar.searchPlaceholder"))
	}
	if m.sidebarHeader != nil {
		m.sidebarHeader.SetText(ilocale.T("main.sidebar.header"))
	}
	if m.sidebarRadios != nil {
		// 选项标签与选中项都跟着语言走;保留当前选中的 filter 值不变。
		cur, _ := m.filter.Get()
		m.sidebarRadios.Options = m.sidebarOptions()
		m.sidebarRadios.SetSelected(m.sidebarLabel(cur))
		m.sidebarRadios.Refresh()
	}
	if m.diskLabel != nil {
		m.diskLabel.SetText(ilocale.T("main.status.disk.detecting"))
	}
	// 任务列表头/列、空状态、行状态 —— 通过 list/taskRow 暴露的 hook 通知。
	if m.taskList != nil {
		m.taskList.applyLanguage()
	}
	// 状态栏磁盘信息:上一次快照的状态文案需要重写一次。
	m.statusBar.refreshDiskSpace()
	// 设置页若正打开,标题与所有表单项即时重译。
	if m.settingsPageOpen {
		m.showSettingsPage()
	}
}

// showSettingsPage 将主内容切换到设置页面。
func (m *MainWindow) showSettingsPage() {
	m.settingsPageOpen = true
	m.win.SetContent(container.NewBorder(
		m.buildSettingsHeader(),
		nil, nil, nil,
		m.buildSettingsContent(),
	))
}

// showMainPage 将主内容切换回主视图。
func (m *MainWindow) showMainPage() {
	m.settingsPageOpen = false
	m.win.SetContent(m.content)
}

// buildSettingsHeader 返回带有返回按钮和标题的标题栏。
func (m *MainWindow) buildSettingsHeader() fyne.CanvasObject {
	backBtn := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
		m.showMainPage()
	})
	title := widget.NewLabel("")
	title.TextStyle.Bold = true
	title.SetText(ilocale.T("settings.header.title"))
	return container.NewBorder(nil, nil, backBtn, nil, container.NewHBox(title, layout.NewSpacer()))
}

// buildSettingsContent 返回设置表单内容。onChange 在任意设置项变更后触发，
// 用于刷新任务列表（例如排序方式改变后）。
func (m *MainWindow) buildSettingsContent() fyne.CanvasObject {
	return buildSettingsContent(m.svc, func() { m.taskList.refresh() })
}

// buildToolbar 排列操作按钮、搜索框以及设置快捷按钮。
func (m *MainWindow) buildToolbar() fyne.CanvasObject {
	newBtn := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		showAddTaskDialog(m.win, m.svc)
	})
	pauseAllBtn := widget.NewButtonWithIcon("", theme.MediaPauseIcon(), func() {
		// 不再按 Status 过滤:Start 已经预留 slot 但 engine 还没接管的
		// 「准备中」任务,scheduler.Pause 也能通过 prepareCancel 立刻中止;
		// 对已经不在运行的任务 Pause 会返回 error,这里忽略即可。
		// 同时为每个被请求暂停的 row 设置乐观状态,让 UI 立刻反映。
		paused := store.TaskStatus.Paused
		for _, tk := range m.taskList.allTasks() {
			m.taskList.setOptimistic(tk.ID, paused)
			_ = m.svc.Pause(tk.ID)
		}
	})
	resumeAllBtn := widget.NewButtonWithIcon("", theme.MediaPlayIcon(), func() {
		down := store.TaskStatus.Downloading
		for _, tk := range m.taskList.allTasks() {
			if tk.Status == store.TaskStatus.Paused || tk.Status == store.TaskStatus.Failed {
				m.taskList.setOptimistic(tk.ID, down)
				_ = m.svc.Start(tk.ID)
			}
		}
	})
	settingsBtn := widget.NewButtonWithIcon("", theme.SettingsIcon(), func() {
		m.showSettingsPage()
	})

	searchEntry := widget.NewEntry()
	searchEntry.OnChanged = func(s string) { m.taskList.setSearch(s) }

	m.tbNew = newBtn
	m.tbPauseAll = pauseAllBtn
	m.tbResumeAll = resumeAllBtn
	m.tbSettings = settingsBtn
	m.searchEntry = searchEntry

	left := container.NewHBox(newBtn, pauseAllBtn, resumeAllBtn)
	return container.NewBorder(
		nil,         // top
		nil,         // bottom
		left,        // left：按钮组
		settingsBtn, // right：设置按钮
		searchEntry, // center：搜索框自动撑满剩余空间
	)
}

// buildMainSplit 创建侧边栏和任务列表之间的水平分割。
func (m *MainWindow) buildMainSplit() *container.Split {
	split := container.NewHSplit(
		m.buildSidebar(),
		m.taskList.container(),
	)
	split.SetOffset(0.18)
	return split
}

// buildSidebar 渲染筛选单选按钮组。
//
// filter 与选项 label 的对应关系通过 sidebarFilterToLabel / sidebarLabelToFilter
// 维护——选项的 label 在 applyLanguage 里整体替换,但底层 filter 值
// （"all"/"downloading"/...）不变,selected 项也保持稳定。
func (m *MainWindow) buildSidebar() fyne.CanvasObject {
	header := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	m.sidebarHeader = header

	radios := widget.NewRadioGroup([]string{}, func(s string) {
		fv := m.sidebarLabelToFilter(s)
		if fv == "" {
			return
		}
		_ = m.filter.Set(fv)
		// 状态过滤改变后必须刷新列表，否则 List 的 Length 不会重新求值。
		m.taskList.refresh()
	})
	radios.Required = true
	radios.Horizontal = false
	m.sidebarRadios = radios
	radios.SetSelected(m.sidebarLabel("all"))
	_ = m.filter.Set("all")

	return container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()),
		nil,
		nil, nil,
		radios,
	)
}

// sidebarOptions 返回侧边栏筛选选项的当前语言标签,顺序固定。
func (m *MainWindow) sidebarOptions() []string {
	filters := []string{"all", "downloading", "paused", "completed", "failed", "filelost"}
	out := make([]string, len(filters))
	for i, f := range filters {
		out[i] = m.sidebarLabel(f)
	}
	return out
}

// sidebarLabel 把 filter 值翻译成当前语言的显示标签。
func (m *MainWindow) sidebarLabel(filterVal string) string {
	switch filterVal {
	case "all":
		return ilocale.T("main.filter.all")
	case "downloading":
		return ilocale.T("main.filter.downloading")
	case "paused":
		return ilocale.T("main.filter.paused")
	case "completed":
		return ilocale.T("main.filter.completed")
	case "failed":
		return ilocale.T("main.filter.failed")
	case "filelost":
		return ilocale.T("main.filter.filelost")
	}
	return filterVal
}

// sidebarLabelToFilter 是 sidebarLabel 的反向:由当前语言的 label 找
// 回 filter 值。filter 值集合固定(6 个),所以 label→filter 也是稳定的。
func (m *MainWindow) sidebarLabelToFilter(label string) string {
	for _, fv := range []string{"all", "downloading", "paused", "completed", "failed", "filelost"} {
		if m.sidebarLabel(fv) == label {
			return fv
		}
	}
	return ""
}

// Show 显示主窗口。
func (m *MainWindow) Show() {
	m.win.Show()
}

// ShowFromTray 在业务进程托盘「显示窗口」时调用（MsgShow）。
// 调用方必须保证在 Fyne 事件线程上（fyne.Do）。
func (m *MainWindow) ShowFromTray() {
	m.win.Show()
	m.win.RequestFocus()
}

// onCloseRequested 在用户点击窗口关闭按钮时被调用。
//
// LightMode 开启时(默认)整个 UI 子进程退出, 内存由操作系统彻底回收;
// 业务进程不受影响, 托盘可随时重新拉起 UI。需要「关闭即隐藏、由托盘
// 恢复」的轻量行为可以在「设置」里手动关闭 LightMode。
func (m *MainWindow) onCloseRequested() {
	if GlobalSettings.LightMode {
		m.Close()
		m.app.Quit()
		return
	}
	m.win.Hide()
}

// Close 清理事件订阅与后台周期任务（进程退出路径）。
func (m *MainWindow) Close() {
	if m.unsub != nil {
		m.unsub()
		m.unsub = nil
	}
	m.stopDiskTicker()
}

// startDiskTicker 启动状态栏磁盘空间的周期刷新。
func (m *MainWindow) startDiskTicker() {
	ticker := time.NewTicker(diskRefreshInterval)
	done := make(chan struct{})
	m.diskTicker, m.diskDone = ticker, done
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// 读取设置与改动控件都必须在 Fyne 事件线程上进行。
				fyne.Do(m.statusBar.refreshDiskSpace)
			}
		}
	}()
}

// stopDiskTicker 停止周期刷新；重复调用是安全的。
func (m *MainWindow) stopDiskTicker() {
	if m.diskTicker != nil {
		m.diskTicker.Stop()
		m.diskTicker = nil
	}
	if m.diskDone != nil {
		close(m.diskDone)
		m.diskDone = nil
	}
}

// subscribe 将业务事件流接入 fyne.Do 的 GUI 更新。
func (m *MainWindow) subscribe() {
	ch, unsub := m.svc.Subscribe()
	m.unsub = unsub
	go func() {
		for ev := range ch {
			ev := ev
			fyne.Do(func() {
				m.taskList.onEvent(ev)
			})
		}
	}()
}

// diskBar 是一个细长彩色条，用于显示磁盘使用情况（绿→红）。
type diskBar struct {
	widget.BaseWidget
	progress float64 // 0.0 到 1.0

	bg   *canvas.Rectangle
	fill *canvas.Rectangle
}

func newDiskBar() *diskBar {
	db := &diskBar{}
	db.ExtendBaseWidget(db)
	return db
}

func (db *diskBar) setProgress(frac float64) {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	db.progress = frac
	db.Refresh()
}

func (db *diskBar) CreateRenderer() fyne.WidgetRenderer {
	db.bg = canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	db.fill = canvas.NewRectangle(colorForProgress(db.progress))
	return &diskBarRenderer{db, db.bg, db.fill}
}

func (db *diskBar) MinSize() fyne.Size {
	return fyne.NewSize(200, 4)
}

func (db *diskBar) Refresh() {
	if db.fill == nil {
		// 还未接入 widget 树；CreateRenderer() 会调用 fill。
		return
	}
	db.fill.FillColor = colorForProgress(db.progress)
	db.fill.Refresh()
}

type diskBarRenderer struct {
	b    *diskBar
	bg   *canvas.Rectangle
	fill *canvas.Rectangle
}

func (r *diskBarRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	barW := float32(r.b.progress) * size.Width
	if barW < 0 {
		barW = 0
	}
	r.fill.Resize(fyne.NewSize(barW, size.Height))
}

func (r *diskBarRenderer) MinSize() fyne.Size           { return r.b.MinSize() }
func (r *diskBarRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.bg, r.fill} }
func (r *diskBarRenderer) Destroy()                     {}
func (r *diskBarRenderer) Refresh()                     {}
func colorForProgress(frac float64) color.Color {
	// 绿色：#4CAF50，黄色：#FFEB3B，红色：#F44336
	var r, g, b_ float64
	if frac < 0.5 {
		t := frac * 2
		r = t*255 + (1-t)*76
		g = t*235 + (1-t)*175
		b_ = t*59 + (1-t)*80
	} else {
		t := (frac - 0.5) * 2
		r = t*244 + (1-t)*255
		g = t*67 + (1-t)*235
		b_ = t*54 + (1-t)*59
	}
	return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b_), A: 255}
}

// statusBar 显示磁盘使用情况。
type statusBar struct {
	diskBar   *diskBar
	diskLabel *widget.Label
}

func newStatusBar() *statusBar {
	diskLabel := widget.NewLabel("")
	diskLabel.SizeName = theme.SizeNameCaptionText
	diskLabel.SetText(ilocale.T("main.status.disk.detecting"))

	return &statusBar{
		diskBar:   newDiskBar(),
		diskLabel: diskLabel,
	}
}

func (sb *statusBar) container() fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedVBoxLayout(0),
		widget.NewSeparator(),
		sb.diskLabel,
		sb.diskBar,
	)
}
func (sb *statusBar) setDiskSpace(free, total int64) {
	if total == 0 {
		sb.diskLabel.SetText(ilocale.T("main.status.disk.unavailable"))
		sb.diskBar.setProgress(0)
		return
	}
	frac := 1.0 - float64(free)/float64(total)
	sb.diskBar.setProgress(frac)
	sb.diskLabel.SetText(ilocale.TF("main.status.disk.format", fmt.Sprintf("磁盘空间: 可用 %s / 总计 %s", humanBytes(free), humanBytes(total)), map[string]any{
		"Free": humanBytes(free),
		"Total": humanBytes(total),
	}))
}

// refreshDiskSpace 根据 GlobalSettings.DefaultSaveDir 更新磁盘空间显示。
func (sb *statusBar) refreshDiskSpace() {
	dir := GlobalSettings.DefaultSaveDir
	if dir == "" {
		sb.diskLabel.SetText(ilocale.T("main.status.disk.unset"))
		sb.diskBar.setProgress(0)
		return
	}
	d := filepath.Dir(dir)
	if d == "" {
		d = "."
	}
	free, total, err := diskUsage(d)
	if err != nil {
		sb.diskLabel.SetText(ilocale.TF("main.status.disk.error", fmt.Sprintf("不可用 (%v)", err), map[string]any{
			"Err": err.Error(),
		}))
		sb.diskBar.setProgress(0)
		return
	}
	sb.setDiskSpace(free, total)
}
