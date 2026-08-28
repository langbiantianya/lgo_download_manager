// Package ui provides the Fyne-based GUI for the download manager.
// Thread-safety: the scheduler lives on background goroutines; all GUI
// mutations happen on the Fyne event thread via fyne.Do().
package ui

import (
	"fmt"
	"image/color"
	"log"
	"path/filepath"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// MainWindow holds all GUI state and the top-level Fyne window.
type MainWindow struct {
	app      fyne.App
	sc       *scheduler.Scheduler
	win      fyne.Window
	content  *fyne.Container

	filter    binding.String
	taskList  *taskList
	statusBar *statusBar

	unsub func()
}

// NewMainWindow builds the main window attached to app.
//
// This MUST be called on the Fyne event thread (typically the main goroutine
// after app.New…() and before a.Run()), because widget constructors rely on
// fyne.CurrentApp() resolving to the just-created app.
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
// buildMainUI assembles the main window and stores the content container
// for page-switching (e.g., to settings page).
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

// showSettingsPage switches the main content to the settings page.
func (m *MainWindow) showSettingsPage() {
	m.win.SetContent(container.NewBorder(
		m.buildSettingsHeader(),
		nil, nil, nil,
		m.buildSettingsContent(),
	))
}

// showMainPage switches the main content back to the main view.
func (m *MainWindow) showMainPage() {
	m.win.SetContent(m.content)
}

// buildSettingsHeader returns a header bar with a back button and title.
func (m *MainWindow) buildSettingsHeader() fyne.CanvasObject {
	backBtn := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
		m.showMainPage()
	})
	title := widget.NewLabel("设置")
	title.TextStyle.Bold = true
	return container.NewBorder(nil, nil, backBtn, nil, container.NewHBox(title, layout.NewSpacer()))
}

// buildSettingsContent returns the settings form content.
func (m *MainWindow) buildSettingsContent() fyne.CanvasObject {
	return buildSettingsContent(m.sc)
}
// buildToolbar packs the action buttons, search and the settings shortcut.
func (m *MainWindow) buildToolbar() fyne.CanvasObject {
	newBtn := widget.NewButtonWithIcon("新建任务", theme.ContentAddIcon(), func() {
		showAddTaskDialog(m.win, m.sc)
	})
	pauseAllBtn := widget.NewButtonWithIcon("暂停全部", theme.MediaPauseIcon(), func() {
		for _, tk := range m.taskList.allTasks() {
			if tk.Status == store.StatusDownloading {
				_ = m.sc.Pause(tk.ID)
			}
		}
	})
	resumeAllBtn := widget.NewButtonWithIcon("恢复全部", theme.MediaPlayIcon(), func() {
		for _, tk := range m.taskList.allTasks() {
			if tk.Status == store.StatusPaused || tk.Status == store.StatusFailed {
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

// buildMainSplit creates the horizontal split between sidebar and task list.
func (m *MainWindow) buildMainSplit() *container.Split {
	split := container.NewHSplit(
		m.buildSidebar(),
		m.taskList.container(),
	)
	split.SetOffset(0.18)
	return split
}

// buildSidebar renders the filter radio group.
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
	}
	radios := widget.NewRadioGroup([]string{}, func(s string) {
		_ = m.filter.Set(s)
	})
	labels := make([]string, len(filters))
	for i, f := range filters {
		labels[i] = f.label
	}
	radios.Options = labels
	radios.Required = true
	radios.SetSelected("全部")
	radios.Horizontal = false
	header := widget.NewLabelWithStyle("任务筛选", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	return container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()),
		nil,
		nil, nil,
		radios,
	)
}

// Show displays the main window.
// Called from main goroutine before a.Run(), so no fyne.Do() needed.
func (m *MainWindow) Show() {
	m.win.Show()
}

// Close tears down subscriptions.
func (m *MainWindow) Close() {
	if m.unsub != nil {
		m.unsub()
	}
}
// setupTray adds a system tray icon with Show menu item.
func (m *MainWindow) setupTray() {
	openItem := fyne.NewMenuItem("Open", func() {
		m.win.Show()
		m.win.RequestFocus()
	})
	trayMenu := fyne.NewMenu("", openItem)

	// SetSystemTrayMenu/SetSystemTrayWindow are desktop-only methods on *fyneApp.
	if desk, ok := m.app.(interface {
		SetSystemTrayMenu(*fyne.Menu)
		SetSystemTrayWindow(fyne.Window)
	}); ok {
		desk.SetSystemTrayMenu(trayMenu)
		desk.SetSystemTrayWindow(m.win)
	}
}

// subscribe wires the scheduler event bus to fyne.Do GUI updates.
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

// diskBar is a thin colored bar showing disk usage (green→red).
type diskBar struct {
	widget.BaseWidget
	progress float64 // 0.0 to 1.0

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

func (r *diskBarRenderer) MinSize() fyne.Size  { return r.b.MinSize() }
func (r *diskBarRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.bg, r.fill} }
func (r *diskBarRenderer) Destroy()              {}
func (r *diskBarRenderer) Refresh()              {}
func colorForProgress(frac float64) color.Color {
	// green: #4CAF50, yellow: #FFEB3B, red: #F44336
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

// statusBar shows disk usage.
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
// refreshDiskSpace updates the disk space display from GlobalSettings.DefaultSaveDir.
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
