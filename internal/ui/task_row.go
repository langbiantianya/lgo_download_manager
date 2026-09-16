// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"fmt"
	"path/filepath"
	"time"

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
	svc  Service

	// optimisticStatus 在用户点击 Start/Pause 后立即覆盖渲染,
	// 真实事件到达(status 与乐观值一致)时由 onEvent 清掉。
	// 用于消除按钮点击到后端事件回传之间肉眼可见的「卡顿」。
	optimisticStatus *store.Status

	// 第一行:名称 + 大小 + 状态
	name      *widget.Label
	size      *widget.Label
	statusLbl *widget.Label

	// 第二行:进度条
	progress *widget.ProgressBar

	// 第三行:左侧按钮,右侧速度+剩余时间+添加时间
	speed        *widget.Label
	remTime      *widget.Label
	createdAtLbl *widget.Label

	// 第三行:操作按钮
	startBtn      *widget.Button
	pauseBtn      *widget.Button
	cancelBtn     *widget.Button
	detailsBtn    *widget.Button
	openFolderBtn *widget.Button
	openFileBtn   *widget.Button

	// 在事件之间缓存的速度
	curSpeed float64

	// st 缓存上一次写入控件的值：进度事件每个 tick 都会到达，无差异判断
	// 地重复 SetText/Show/Hide/SetIcon 会触发高开销的布局与重绘。
	st rowState

	inner *fyne.Container
}

// rowButtons 是操作按钮可见性位图（cancel/details 始终可见，不参与）。
type rowButtons uint8

const (
	showStart rowButtons = 1 << iota
	showPause
	showFolder
	showFile
)

// rowState 缓存上一次渲染写入控件的值，用于短路无变化的写入。
// valid 为 false 时表示尚未渲染过（或任务已被解绑），所有字段都要重写。
type rowState struct {
	valid bool

	// 进度部分
	name      string
	size      string
	createdAt string
	pct       float64
	speed     string
	eta       string

	// speedShown/buttons 记录的是「已经应用到控件上的可见性」，
	// 其零值与 build() 结束时的控件状态一致（相关控件均被隐藏）。
	speedShown bool
	buttons    rowButtons

	// 状态部分
	status    store.Status
	preparing bool
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

	r.createdAtLbl = widget.NewLabel("")
	r.createdAtLbl.Alignment = fyne.TextAlignTrailing

	r.size = widget.NewLabel("")

	r.statusLbl = widget.NewLabel("")
	r.statusLbl.Alignment = fyne.TextAlignCenter

	r.progress = widget.NewProgressBar()
	r.progress.TextFormatter = func() string { return "" }

	r.speed = widget.NewLabel("")
	r.speed.Alignment = fyne.TextAlignTrailing
	r.remTime = widget.NewLabel("")
	r.remTime.Alignment = fyne.TextAlignTrailing
	r.startBtn = widget.NewButtonWithIcon("", theme.MediaPlayIcon(), func() {})
	r.pauseBtn = widget.NewButtonWithIcon("", theme.MediaPauseIcon(), func() {})
	r.cancelBtn = widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {})
	r.detailsBtn = widget.NewButtonWithIcon("", theme.InfoIcon(), func() {})
	r.openFolderBtn = widget.NewButtonWithIcon("", theme.FolderIcon(), func() {})
	r.openFileBtn = widget.NewButtonWithIcon("", theme.FileIcon(), func() {})

	for _, b := range []*widget.Button{r.startBtn, r.pauseBtn, r.cancelBtn, r.detailsBtn, r.openFolderBtn, r.openFileBtn} {
		b.Importance = widget.LowImportance
	}
	// 控件默认可见，先按「无任务」隐藏状态相关的控件：这样 rowState 的
	// 零值就等于控件的实际状态，refresh 只需处理真正的差异。
	// 首次 refresh（bind 时会立即发生）会按状态重新显示。
	r.startBtn.Hide()
	r.pauseBtn.Hide()
	r.openFolderBtn.Hide()
	r.openFileBtn.Hide()
	r.speed.Hide()
	r.remTime.Hide()

	// 第一行：名称（可扩展）[大小][状态]
	row1 := container.NewBorder(
		nil, nil, nil,
		container.NewHBox(r.size, r.statusLbl),
		r.name,
	)
	// 第二行：进度条占满整行
	row2 := container.NewStack(r.progress)
	// 第三行：左侧按钮，右侧速度+剩余时间+添加时间
	row3 := container.NewBorder(
		nil, nil,
		container.NewHBox(r.startBtn, r.pauseBtn, r.openFolderBtn, r.openFileBtn, r.detailsBtn, r.cancelBtn),
		container.NewHBox(r.speed, r.remTime, r.createdAtLbl),
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

func (r *taskRow) bind(t *store.Task, svc Service) {
	//   - curSpeed：不在 Task 结构体中，仅由进度事件更新。
	r.task = t
	r.svc = svc
	r.refresh()
	r.bindButtons(t, svc)
}

func (r *taskRow) bindButtons(t *store.Task, svc Service) {
	if t == nil {
		return
	}
	taskID := t.ID
	// 复制一份供闭包读取当前快照;乐观覆盖则在 refresh 内根据 r.task 计算。
	startBtn := r.startBtn
	pauseBtn := r.pauseBtn
	startBtn.OnTapped = func() {
		// 乐观更新:让用户立即看到「下载中/准备中」,不等事件回环。
		down := store.TaskStatus.Downloading
		r.optimisticStatus = &down
		fyne.Do(func() { r.refresh() })
		_ = svc.Start(taskID)
	}
	pauseBtn.OnTapped = func() {
		paused := store.TaskStatus.Paused
		r.optimisticStatus = &paused
		fyne.Do(func() { r.refresh() })
		_ = svc.Pause(taskID)
	}
	r.cancelBtn.OnTapped = func() { svc.Delete(taskID) }
	r.detailsBtn.OnTapped = func() { showChunkDetails(t, svc, globalWin) }
	r.openFolderBtn.OnTapped = func() {
		if t.SavePath == "" {
			return
		}
		openFolder(filepath.Dir(t.SavePath))
	}
	r.openFileBtn.OnTapped = func() {
		if t.SavePath == "" {
			return
		}
		openFile(t.SavePath)
	}
}

func (r *taskRow) refresh() {
	t := r.task
	if t == nil {
		// 未绑定任务：清空显示并隐藏动作按钮；下一次 bind 全量重渲染。
		r.st.valid = false
		r.name.SetText("")
		r.progress.SetValue(0)
		r.setVis(r.startBtn, showStart, false)
		r.setVis(r.pauseBtn, showPause, false)
		r.setSpeedVisible(false)
		return
	}
	eff, preparing := r.effectiveStatus(t)
	// 进度部分逐值短路；状态部分只在 effective status / 准备中判定变化时执行。
	r.refreshProgress(t, eff)
	r.refreshStatus(eff, preparing)
}

// effectiveStatus 返回实际用于渲染的状态：乐观覆盖优先于后端快照。
//
// 乐观状态：用户刚点完 Start/Pause，后端事件还没回环，先按用户意图渲染；
// 真实事件（status 与乐观值一致）由 taskList.onEvent 清掉覆盖，
// 中途收到的 progress 事件不清覆盖，直到 status 事件落地。
//
// 准备阶段判定：store 中 Status 仍是 Pending，且 scheduler 已为该任务预留了
// slot（engine 尚未接管）。乐观覆盖为 Downloading 时不再视为「准备中」，
// 用户刚点完 Start 不应再看到「准备中」闪烁。
func (r *taskRow) effectiveStatus(t *store.Task) (store.Status, bool) {
	eff := t.Status
	if r.optimisticStatus != nil {
		eff = *r.optimisticStatus
	}
	preparing := eff == store.TaskStatus.Pending && r.svc != nil && r.svc.IsPreparing(t.ID)
	return eff, preparing
}

// refreshProgress 写入随进度变化的部分：名称、添加时间、大小、进度条，
// 以及下载中才显示的速度与剩余时间。每个值都先与上次渲染值比较。
func (r *taskRow) refreshProgress(t *store.Task, eff store.Status) {
	r.setText(r.name, &r.st.name, displayName(t))
	r.setText(r.createdAtLbl, &r.st.createdAt, "添加于: "+formatTime(t.CreatedAt))
	r.setText(r.size, &r.st.size, formatBytes(t.TotalSize))

	pct := 0.0
	if t.TotalSize > 0 {
		pct = float64(t.Downloaded) / float64(t.TotalSize)
	}
	if pct > 1 {
		pct = 1
	}
	if !r.st.valid || r.st.pct != pct {
		r.st.pct = pct
		r.progress.SetValue(pct)
	}

	downloading := eff == store.TaskStatus.Downloading
	r.setSpeedVisible(downloading)
	if downloading {
		r.setText(r.speed, &r.st.speed, formatBPS(r.curSpeed))
		r.setText(r.remTime, &r.st.eta, etaText(t, r.curSpeed))
	}
}

// refreshStatus 只在 effective status（或「准备中」判定）变化时更新状态标签、
// 按钮可见性与图标——这些操作会触发布局与图标绘制，是每个 tick 里最贵的部分。
func (r *taskRow) refreshStatus(eff store.Status, preparing bool) {
	s := &r.st
	if s.valid && s.status == eff && s.preparing == preparing {
		return
	}
	s.status = eff
	s.preparing = preparing
	s.valid = true

	switch eff {
	case store.TaskStatus.Pending:
		if preparing {
			r.statusLbl.SetText("准备中")
			r.setVis(r.pauseBtn, showPause, true)
			r.setVis(r.startBtn, showStart, false)
		} else {
			r.statusLbl.SetText("等待中")
			r.startBtn.SetIcon(theme.MediaPlayIcon())
			r.startBtn.Importance = widget.LowImportance
			r.setVis(r.pauseBtn, showPause, false)
			r.setVis(r.startBtn, showStart, true)
		}
		r.setVis(r.openFolderBtn, showFolder, false)
		r.setVis(r.openFileBtn, showFile, false)
	case store.TaskStatus.Downloading:
		r.statusLbl.SetText("下载中")
		r.setVis(r.startBtn, showStart, false)
		r.setVis(r.pauseBtn, showPause, true)
		r.setVis(r.openFolderBtn, showFolder, false)
		r.setVis(r.openFileBtn, showFile, false)
	case store.TaskStatus.Paused:
		r.statusLbl.SetText("已暂停")
		r.startBtn.SetIcon(theme.MediaPlayIcon())
		r.startBtn.Importance = widget.LowImportance
		r.setVis(r.startBtn, showStart, true)
		r.setVis(r.pauseBtn, showPause, false)
		r.setVis(r.openFolderBtn, showFolder, false)
		r.setVis(r.openFileBtn, showFile, false)
	case store.TaskStatus.Completed:
		r.statusLbl.SetText("已完成")
		r.setVis(r.pauseBtn, showPause, false)
		r.setVis(r.startBtn, showStart, false)
		r.setVis(r.openFolderBtn, showFolder, true)
		r.setVis(r.openFileBtn, showFile, true)
	case store.TaskStatus.FileLost:
		r.statusLbl.SetText("文件丢失")
		r.startBtn.SetIcon(theme.DownloadIcon())
		r.startBtn.Importance = widget.HighImportance
		r.setVis(r.startBtn, showStart, true)
		r.setVis(r.pauseBtn, showPause, false)
		r.setVis(r.openFolderBtn, showFolder, false)
		r.setVis(r.openFileBtn, showFile, false)
	case store.TaskStatus.Failed:
		r.statusLbl.SetText("失败")
		r.startBtn.SetIcon(theme.MediaReplayIcon())
		r.startBtn.Importance = widget.LowImportance
		r.setVis(r.startBtn, showStart, true)
		r.setVis(r.pauseBtn, showPause, false)
	}
}

// setText 仅在文本与上次写入的不同（或尚未渲染过）时写标签。
func (r *taskRow) setText(l *widget.Label, cached *string, text string) {
	if r.st.valid && *cached == text {
		return
	}
	*cached = text
	l.SetText(text)
}

// setVis 仅在期望可见性与上次应用的不同的 Show/Hide 按钮。
func (r *taskRow) setVis(btn *widget.Button, bit rowButtons, want bool) {
	if (r.st.buttons&bit != 0) == want {
		return
	}
	if want {
		r.st.buttons |= bit
		btn.Show()
		return
	}
	r.st.buttons &^= bit
	btn.Hide()
}

// setSpeedVisible 统一控制速度与剩余时间两个标签的可见性。
func (r *taskRow) setSpeedVisible(show bool) {
	if r.st.speedShown == show {
		return
	}
	r.st.speedShown = show
	if show {
		r.speed.Show()
		r.remTime.Show()
		return
	}
	r.speed.Hide()
	r.remTime.Hide()
}
func (r *taskRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.inner)
}

// displayName 返回任务的人类可读文件名。
func displayName(t *store.Task) string {
	if t.SavePath != "" {
		return filepath.Base(t.SavePath)
	}
	return t.URL
}

// formatTime 把 time.Time 渲染为本地时区的 YYYY-MM-DD HH:MM:SS 字符串。
// 零值返回 "-" 表示尚未发生。
func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// formatBytes 使用二进制单位后缀格式化 n。
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

// formatBPS 以字节/秒为单位格式化速度。
func formatBPS(bps float64) string {
	if bps <= 0 {
		return "--"
	}
	return formatBytes(int64(bps)) + "/s"
}

// etaText 估算正在运行任务的剩余时间。
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

// setGlobalSvc 由 NewMainWindow 调用，以便在不需要
// 在每个 widget 构造器中传递 svc 的情况下，使业务服务
// 可被任务行按钮回调访问。
var globalSvc Service

func setGlobalSvc(svc Service) { globalSvc = svc }

var globalWin fyne.Window

func setGlobalWindow(win fyne.Window) { globalWin = win }
