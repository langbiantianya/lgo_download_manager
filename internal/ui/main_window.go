// Package ui provides the Fyne-based GUI for the download manager.
// Thread-safety: the scheduler lives on background goroutines; all GUI
// mutations happen on the Fyne event thread via fyne.Do().
package ui

import (
	"fyne.io/fyne/v2"
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
	app fyne.App
	sc  *scheduler.Scheduler
	win fyne.Window

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
func NewMainWindow(a fyne.App, sc *scheduler.Scheduler) *MainWindow {
	setGlobalScheduler(sc)
	m := &MainWindow{
		app:    a,
		sc:     sc,
		filter: binding.NewString(),
	}
	m.taskList = newTaskList(sc, m.filter)
	m.statusBar = newStatusBar()
	m.buildWindow()
	m.subscribe()
	setGlobalWindow(m.win)
	return m
}

func (m *MainWindow) buildWindow() {
	m.win = m.app.NewWindow("Go Download Manager")
	m.win.SetMaster()

	content := container.NewBorder(
		m.buildToolbar(),
		m.statusBar.container(),
		nil, nil,
		m.buildMainSplit(),
	)
	m.win.SetContent(content)
	m.win.Resize(fyne.NewSize(1000, 640))
	m.win.CenterOnScreen()
	m.win.SetOnClosed(func() { m.Close() })
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
		showSettings(m.win)
	})

	searchEntry := widget.NewEntry()
	searchEntry.SetPlaceHolder("搜索 URL 或保存路径…")
	searchEntry.OnChanged = func(s string) { m.taskList.setSearch(s) }

	left := container.NewHBox(newBtn, pauseAllBtn, resumeAllBtn)
	right := container.NewHBox(searchEntry, settingsBtn)
	return container.NewBorder(nil, nil, left, right, layout.NewSpacer())
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
	footer := widget.NewLabelWithStyle("Go Download Manager", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})

	return container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()),
		container.NewVBox(widget.NewSeparator(), footer),
		nil, nil,
		radios,
	)
}

// Show displays the main window.
func (m *MainWindow) Show() {
	fyne.Do(func() { m.win.Show() })
}

// Close tears down subscriptions.
func (m *MainWindow) Close() {
	if m.unsub != nil {
		m.unsub()
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
				m.statusBar.onEvent(ev)
			})
		}
	}()
}

// SetGRPCStatus updates the gRPC status label (call from main after server starts).
func (m *MainWindow) SetGRPCStatus(s string) {
	fyne.Do(func() { m.statusBar.setGRPC(s) })
}

// statusBar shows gRPC service status and task counts.
type statusBar struct {
	grpcStatus *widget.Label
	countLabel *widget.Label
}

func newStatusBar() *statusBar {
	return &statusBar{
		grpcStatus: widget.NewLabel("gRPC 服务: 启动中..."),
		countLabel: widget.NewLabel("任务总数: 0"),
	}
}

func (sb *statusBar) container() fyne.CanvasObject {
	return container.NewVBox(
		widget.NewSeparator(),
		container.NewHBox(
			sb.grpcStatus,
			layout.NewSpacer(),
			sb.countLabel,
		),
	)
}

func (sb *statusBar) setGRPC(s string) { sb.grpcStatus.SetText("gRPC 服务: " + s) }

func (sb *statusBar) onEvent(ev scheduler.Event) {
	if sc := globalSc; sc != nil {
		if tks, err := sc.List(); err == nil {
			sb.countLabel.SetText(formatTaskSummary(tks))
		}
	}
	_ = ev
}

func formatTaskSummary(tks []*store.Task) string {
	var total, active, paused, done, failed int
	for _, t := range tks {
		total++
		switch t.Status {
		case store.StatusDownloading:
			active++
		case store.StatusPaused:
			paused++
		case store.StatusCompleted:
			done++
		case store.StatusFailed:
			failed++
		}
	}
	if total == 0 {
		return "任务总数: 0"
	}
	return "共 " + itoa(total) + " 个 | 下载 " + itoa(active) +
		" | 暂停 " + itoa(paused) + " | 完成 " + itoa(done) +
		" | 失败 " + itoa(failed)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}