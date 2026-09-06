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

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/store"
)

// MainWindow 保存所有 GUI 状态以及顶层 Fyne 窗口。
type MainWindow struct {
	app     fyne.App
	svc     Service
	win     fyne.Window
	content *fyne.Container

	filter    binding.String
	taskList  *taskList
	statusBar *statusBar

	unsub func()
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
	m.buildMainUI()
	m.subscribe()
	m.statusBar.refreshDiskSpace()
	return m
}
func (m *MainWindow) buildMainUI() {
	m.win = m.app.NewWindow("下载管理器")
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

// showSettingsPage 将主内容切换到设置页面。
func (m *MainWindow) showSettingsPage() {
	m.win.SetContent(container.NewBorder(
		m.buildSettingsHeader(),
		nil, nil, nil,
		m.buildSettingsContent(),
	))
}

// showMainPage 将主内容切换回主视图。
func (m *MainWindow) showMainPage() {
	m.win.SetContent(m.content)
}

// buildSettingsHeader 返回带有返回按钮和标题的标题栏。
func (m *MainWindow) buildSettingsHeader() fyne.CanvasObject {
	backBtn := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
		m.showMainPage()
	})
	title := widget.NewLabel("设置")
	title.TextStyle.Bold = true
	return container.NewBorder(nil, nil, backBtn, nil, container.NewHBox(title, layout.NewSpacer()))
}

// buildSettingsContent 返回设置表单内容。onChange 在任意设置项变更后触发，
// 用于刷新任务列表（例如排序方式改变后）。
func (m *MainWindow) buildSettingsContent() fyne.CanvasObject {
	return buildSettingsContent(m.svc, func() { m.taskList.refresh() })
}

// buildToolbar 排列操作按钮、搜索框以及设置快捷按钮。
func (m *MainWindow) buildToolbar() fyne.CanvasObject {
	newBtn := widget.NewButtonWithIcon("新建任务", theme.ContentAddIcon(), func() {
		showAddTaskDialog(m.win, m.svc)
	})
	pauseAllBtn := widget.NewButtonWithIcon("暂停全部", theme.MediaPauseIcon(), func() {
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
	resumeAllBtn := widget.NewButtonWithIcon("恢复全部", theme.MediaPlayIcon(), func() {
		down := store.TaskStatus.Downloading
		for _, tk := range m.taskList.allTasks() {
			if tk.Status == store.TaskStatus.Paused || tk.Status == store.TaskStatus.Failed {
				m.taskList.setOptimistic(tk.ID, down)
				_ = m.svc.Start(tk.ID)
			}
		}
	})
	settingsBtn := widget.NewButtonWithIcon("设置", theme.SettingsIcon(), func() {
		m.showSettingsPage()
	})

	searchEntry := widget.NewEntry()
	searchEntry.SetPlaceHolder("搜索 URL 或保存路径…")
	searchEntry.OnChanged = func(s string) { m.taskList.setSearch(s) }

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
func (m *MainWindow) buildSidebar() fyne.CanvasObject {
	type filterDef struct {
		label     string
		filterVal string
	}
	filters := []filterDef{
		{"全部", "all"},
		{"下载中", "downloading"},
		{"已暂停", "paused"},
		{"已完成", "completed"},
		{"失败", "failed"},
		{"文件丢失", "filelost"},
	}
	labelToFilter := map[string]string{}
	for _, f := range filters {
		labelToFilter[f.label] = f.filterVal
	}
	radios := widget.NewRadioGroup([]string{}, func(s string) {
		_ = m.filter.Set(labelToFilter[s])
		// 状态过滤改变后必须刷新列表，否则 List 的 Length 不会重新求值。
		m.taskList.refresh()
	})
	labels := make([]string, len(filters))
	for i, f := range filters {
		labels[i] = f.label
	}
	radios.Options = labels
	radios.Required = true
	radios.SetSelected("全部")
	_ = m.filter.Set("all")
	radios.Horizontal = false
	header := widget.NewLabelWithStyle("任务筛选", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	return container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()),
		nil,
		nil, nil,
		radios,
	)
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

// Close 清理事件订阅（进程退出路径）。
func (m *MainWindow) Close() {
	if m.unsub != nil {
		m.unsub()
		m.unsub = nil
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
	diskLabel := widget.NewLabel("磁盘空间: 检测中...")
	diskLabel.SizeName = theme.SizeNameCaptionText

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
		sb.diskLabel.SetText("磁盘空间: 不可用")
		sb.diskBar.setProgress(0)
		return
	}
	frac := 1.0 - float64(free)/float64(total)
	sb.diskBar.setProgress(frac)
	sb.diskLabel.SetText(fmt.Sprintf("磁盘空间: 可用 %s / 总计 %s",
		humanBytes(free), humanBytes(total)))
}

// refreshDiskSpace 根据 GlobalSettings.DefaultSaveDir 更新磁盘空间显示。
func (sb *statusBar) refreshDiskSpace() {
	dir := GlobalSettings.DefaultSaveDir
	if dir == "" {
		sb.setDiskSpace(0, 0)
		return
	}
	d := filepath.Dir(dir)
	if d == "" {
		d = "."
	}
	free, total, err := diskUsage(d)
	if err != nil {
		sb.setDiskSpace(0, 0)
		return
	}
	sb.setDiskSpace(free, total)
}
