package ui

import (
	"fmt"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

type taskRow struct {
	widget.BaseWidget
	task *store.Task
	sc   *scheduler.Scheduler

	// Row 1: name + size + status
	name      *widget.Label
	size      *widget.Label
	statusLbl *widget.Label

	// Row 2: progress + speed + eta
	progress *widget.ProgressBar
	speed    *widget.Label
	remTime  *widget.Label

	// Row 3: action buttons
	startBtn   *widget.Button
	pauseBtn   *widget.Button
	cancelBtn  *widget.Button
	detailsBtn *widget.Button

	// Cached speed between events
	curSpeed float64

	inner *fyne.Container
}

func newTaskRow(_ *store.Task) *taskRow {
	r := &taskRow{}
	r.ExtendBaseWidget(r)
	r.build()
	return r
}

func (r *taskRow) build() {
	r.name = widget.NewLabel("")
	r.name.TextStyle.Bold = true
	r.name.Truncation = fyne.TextTruncateEllipsis

	r.size = widget.NewLabel("")
	r.size.Alignment = fyne.TextAlignTrailing

	r.statusLbl = widget.NewLabel("")
	r.statusLbl.Alignment = fyne.TextAlignCenter

	r.progress = widget.NewProgressBar()
	r.progress.TextFormatter = func() string { return "" }

	r.speed = widget.NewLabel("")
	r.speed.Alignment = fyne.TextAlignTrailing
	r.speed.TextStyle.Monospace = true

	r.remTime = widget.NewLabel("")
	r.remTime.Alignment = fyne.TextAlignTrailing

	r.startBtn = widget.NewButtonWithIcon("", theme.MediaPlayIcon(), func() {})
	r.pauseBtn = widget.NewButtonWithIcon("", theme.MediaPauseIcon(), func() {})
	r.cancelBtn = widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {})
	r.detailsBtn = widget.NewButtonWithIcon("", theme.InfoIcon(), func() {})

	for _, b := range []*widget.Button{r.startBtn, r.pauseBtn, r.cancelBtn, r.detailsBtn} {
		b.Importance = widget.LowImportance
	}

	// Row 1: name (expand) [size][status]
	row1 := container.NewBorder(
		nil, nil, nil,
		container.NewHBox(r.size, r.statusLbl),
		r.name,
	)
	// Row 2: progress (expand) full width
	row2 := container.NewStack(r.progress)

	// Row 3: speed + ETA on the left, buttons aligned right
	row3 := container.NewBorder(
		nil, nil,
		container.NewHBox(r.startBtn, r.pauseBtn, r.cancelBtn, r.detailsBtn),
		container.NewHBox(r.speed, r.remTime),
		layout.NewSpacer(),
	)

	r.inner = container.NewVBox(row1, row2, row3)
}

func (r *taskRow) onProgress(ev scheduler.Event) {
	r.task = ev.Task
	if ev.SpeedBPS > 0 {
		r.curSpeed = ev.SpeedBPS
	}
	r.refresh()
}

func (r *taskRow) bind(t *store.Task, sc *scheduler.Scheduler) {
	//   - curSpeed: not in Task struct, only updated by progress events.
	r.task = t
	r.sc = sc
	r.refresh()
	r.bindButtons(t, sc)
}

func (r *taskRow) bindButtons(t *store.Task, sc *scheduler.Scheduler) {
	if t == nil {
		return
	}
	taskID := t.ID
	r.startBtn.OnTapped = func() { _ = sc.Start(taskID) }
	r.pauseBtn.OnTapped = func() { _ = sc.Pause(taskID) }
	r.cancelBtn.OnTapped = func() { sc.Delete(taskID) }
	r.detailsBtn.OnTapped = func() { showChunkDetails(t, sc, globalWin) }
}

func (r *taskRow) refresh() {
	t := r.task
	if t == nil {
		r.name.SetText("")
		r.size.SetText("")
		r.statusLbl.SetText("")
		r.progress.SetValue(0)
		r.speed.SetText("--")
		r.remTime.SetText("--")
		r.startBtn.Hide()
		r.pauseBtn.Hide()
		return
	}
	r.name.SetText(displayName(t))
	r.size.SetText(formatBytes(t.TotalSize))

	pct := 0.0
	if t.TotalSize > 0 {
		pct = float64(t.Downloaded) / float64(t.TotalSize)
	}
	if pct > 1 {
		pct = 1
	}

	switch t.Status {
	case store.StatusPending:
		r.statusLbl.SetText("等待中")
		r.speed.SetText("--")
		r.remTime.SetText("--")
		r.pauseBtn.Hide()
		r.startBtn.Show()
	case store.StatusDownloading:
		r.statusLbl.SetText("下载中")
		r.speed.SetText(formatBPS(r.curSpeed))
		r.remTime.SetText(etaText(t, r.curSpeed))
		r.startBtn.Hide()
		r.pauseBtn.Show()
	case store.StatusPaused:
		r.statusLbl.SetText("已暂停")
		r.speed.SetText("--")
		r.remTime.SetText("--")
		r.startBtn.Show()
		r.pauseBtn.Hide()
	case store.StatusCompleted:
		r.statusLbl.SetText("已完成")
		r.speed.SetText("--")
		r.remTime.SetText("--")
		r.pauseBtn.Hide()
		r.startBtn.Hide()
	case store.StatusFailed:
		r.statusLbl.SetText("失败")
		r.speed.SetText("--")
		r.remTime.SetText("--")
		r.startBtn.Show()
		r.pauseBtn.Hide()
	}
	r.progress.SetValue(pct)
}

func (r *taskRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.inner)
}

// displayName returns the human-readable filename for a task.
func displayName(t *store.Task) string {
	if t.SavePath != "" {
		return filepath.Base(t.SavePath)
	}
	return t.URL
}

// formatBytes formats n with a binary unit suffix.
func formatBytes(n int64) string {
	if n <= 0 {
		return "未知大小"
	}
	const (
		KB = 1 << 10
		MB = 1 << 20
		GB = 1 << 30
		TB = 1 << 40
	)
	switch {
	case n >= TB:
		return fmt.Sprintf("%.2f TB", float64(n)/TB)
	case n >= GB:
		return fmt.Sprintf("%.2f GB", float64(n)/GB)
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/KB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// formatBPS formats a speed in bytes/second.
func formatBPS(bps float64) string {
	if bps <= 0 {
		return "--"
	}
	return formatBytes(int64(bps)) + "/s"
}

// etaText estimates remaining time for a running task.
func etaText(t *store.Task, bps float64) string {
	if bps <= 0 || t.TotalSize <= 0 {
		return "ETA --"
	}
	remaining := t.TotalSize - t.Downloaded
	if remaining <= 0 {
		return "ETA 0 秒"
	}
	secs := int(float64(remaining) / bps)
	if secs < 0 {
		secs = 0
	}
	return "ETA " + formatRemainingTime(secs)
}

// setGlobalScheduler is called from NewMainWindow to make the scheduler
// accessible to task row button callbacks without threading sc through
// every widget constructor.
var globalSc *scheduler.Scheduler

func setGlobalScheduler(sc *scheduler.Scheduler) { globalSc = sc }
var globalWin fyne.Window

func setGlobalWindow(win fyne.Window) { globalWin = win }