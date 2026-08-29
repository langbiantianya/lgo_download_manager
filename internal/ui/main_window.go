// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package ui 为下载管理器提供基于 Fyne 的图形界面。
// 线程安全：scheduler 运行在后台 goroutine 中；所有 GUI
// 修改都通过 fyne.Do() 在 Fyne 事件线程上进行。
package ui

import (
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"image/color"
	"log"
	"path/filepath"
	"time"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// MainWindow 保存所有 GUI 状态以及顶层 Fyne 窗口。
type MainWindow struct {
	app     fyne.App
	sc      *scheduler.Scheduler
	win     fyne.Window
	content *fyne.Container

	filter    binding.String
	taskList  *taskList
	statusBar *statusBar

	unsub     func()
	focusTicker *time.Ticker
}

// NewMainWindow 构建附加到 app 的主窗口。
//
// 必须在 Fyne 事件线程上调用（通常在 app.New…() 之后、
// a.Run() 之前的主 goroutine 中），因为 widget 构造器依赖
// fyne.CurrentApp() 解析为刚创建的 app。
func NewMainWindow(a fyne.App, st *store.Store, sc *scheduler.Scheduler) *MainWindow {
	setGlobalStore(st)
	if err := LoadSettings(st); err != nil {
		log.Printf("ui: load settings: %v", err)
	}
	setGlobalScheduler(sc)
	m := &MainWindow{
		app:    a,
		sc:     sc,
		filter: binding.NewString(),
	}
	m.taskList = newTaskList(sc, m.filter)
	m.statusBar = newStatusBar()
	m.buildMainUI()
	m.statusBar.refreshDiskSpace()
	m.subscribe()
	m.setupTray()
	setGlobalWindow(m.win)
	return m
}

// buildMainUI 组装主窗口并保存内容容器，以便在页面之间切换（例如切换到设置页面）。
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
	m.win.SetOnClosed(func() { m.Close() })
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

// buildSettingsContent 返回设置表单内容。
func (m *MainWindow) buildSettingsContent() fyne.CanvasObject {
	return buildSettingsContent(m.sc)
}

// buildToolbar 排列操作按钮、搜索框以及设置快捷按钮。
func (m *MainWindow) buildToolbar() fyne.CanvasObject {
	newBtn := widget.NewButtonWithIcon("新建任务", theme.ContentAddIcon(), func() {
		showAddTaskDialog(m.win, m.sc)
	})
	pauseAllBtn := widget.NewButtonWithIcon("暂停全部", theme.MediaPauseIcon(), func() {
		for _, tk := range m.taskList.allTasks() {
			if tk.Status == store.TaskStatus.Downloading {
				_ = m.sc.Pause(tk.ID)
			}
		}
	})
	resumeAllBtn := widget.NewButtonWithIcon("恢复全部", theme.MediaPlayIcon(), func() {
		for _, tk := range m.taskList.allTasks() {
			if tk.Status == store.TaskStatus.Paused || tk.Status == store.TaskStatus.Failed {
				_ = m.sc.Start(tk.ID)
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

// Show 显示主窗口并在周期性地检查文件是否存在。
// 在主 goroutine 中、a.Run() 之前调用，因此不需要 fyne.Do()。
func (m *MainWindow) Show() {
	m.win.Show()
	if m.focusTicker == nil {
		m.focusTicker = time.NewTicker(10 * time.Second)
		go func() {
			for range m.focusTicker.C {
				m.sc.ValidateFileExistence()
			}
		}()
	}
}

// Close 清理订阅和定时器。
func (m *MainWindow) Close() {
	if m.focusTicker != nil {
		m.focusTicker.Stop()
		m.focusTicker = nil
	}
	if m.unsub != nil {
		m.unsub()
	}
}

// setupTray 添加带有 Show 菜单项的系统托盘图标。
func (m *MainWindow) setupTray() {
	openItem := fyne.NewMenuItem("Open", func() {
		m.win.Show()
		m.win.RequestFocus()
	})
	trayMenu := fyne.NewMenu("", openItem)

	// SetSystemTrayMenu/SetSystemTrayWindow 是 *fyneApp 上仅限桌面端的方法。
	if desk, ok := m.app.(interface {
		SetSystemTrayMenu(*fyne.Menu)
		SetSystemTrayWindow(fyne.Window)
	}); ok {
		desk.SetSystemTrayMenu(trayMenu)
		desk.SetSystemTrayWindow(m.win)
	}
}

// subscribe 将 scheduler 事件总线接入 fyne.Do 的 GUI 更新。
func (m *MainWindow) subscribe() {
	ch, unsub := m.sc.Subscribe()
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
