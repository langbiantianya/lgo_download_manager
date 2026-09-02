// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"path/filepath"
	"strings"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"lgo_download_manager/internal/store"
)

// OpenNewTaskFunc 是打开"新建任务"窗口的回调。
type OpenNewTaskFunc func()

// MainWindow 主窗口的状态——所有字段为本窗口私有。
type MainWindow struct {
	a  *App
	th *material.Theme
	w  *app.Window

	newBtn       widget.Clickable
	pauseAllBtn  widget.Clickable
	resumeAllBtn widget.Clickable
	settingsBtn  widget.Clickable
	searchEditor widget.Editor

	filterEnum widget.Enum

	taskList widget.List

	rowStates map[int]*rowState

	schedUnsub func()

	disks []diskInfo

	openNewTask OpenNewTaskFunc
}

// rowState 一行内所有可点击 widget 的状态。
type rowState struct {
	startBtn, pauseBtn, openDirBtn, openFileBtn, detailsBtn, cancelBtn widget.Clickable
}

// RunMainWindow 创建主窗口并启动 event loop。
func RunMainWindow(a *App) *MainWindow {
	w := new(app.Window)
	w.Option(app.Title("LDM - Download Manager"))
	w.Option(app.Size(unit.Dp(1100), unit.Dp(720)))

	th := newTheme()
	mw := &MainWindow{
		a:         a,
		th:        th,
		w:         w,
		rowStates: map[int]*rowState{},
	}
	mw.filterEnum.Value = a.Filter()
	mw.taskList.Axis = layout.Vertical

	a.SetMainWindow(w)

	ch, unsub := a.Sched.Subscribe()
	mw.schedUnsub = unsub
	go func() {
		for range ch {
			w.Invalidate()
		}
	}()

	a.Subscribe(func() { w.Invalidate() })

	runWindow(w, mw.Layout)
	return mw
}

// SetOpenNewTask 注册打开新建任务窗口的回调。
func (mw *MainWindow) SetOpenNewTask(fn OpenNewTaskFunc) {
	mw.openNewTask = fn
}

// Close 释放订阅。
func (mw *MainWindow) Close() {
	if mw.schedUnsub != nil {
		mw.schedUnsub()
	}
}

// Layout 是每帧渲染入口。
//
// 布局：
//
//	┌───────────────────────────────────────┐
//	│  Toolbar (按钮组 + 搜索框)            │
//	├──────────┬────────────────────────────┤
//	│ Sidebar  │  Task List                  │
//	│ (filter) │                            │
//	├──────────┴────────────────────────────┤
//	│  Status Bar (磁盘占用)                │
//	└───────────────────────────────────────┘
func (mw *MainWindow) Layout(gtx layout.Context) layout.Dimensions {
	// 处理 search editor 事件。
	for {
		ev, ok := mw.searchEditor.Update(gtx)
		if !ok {
			break
		}
		switch ev.(type) {
		case widget.ChangeEvent:
			mw.a.SetSearch(mw.searchEditor.Text())
		case widget.SubmitEvent:
			if mw.openNewTask != nil {
				mw.openNewTask()
			}
		}
	}
	if mw.filterEnum.Update(gtx) {
		mw.a.SetFilter(mw.filterEnum.Value)
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Toolbar：垂直 8dp 上下内边距、水平 16dp
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{
				Top:    SpaceSM,
				Bottom: SpaceSM,
				Left:   SpaceMD,
				Right:  SpaceMD,
			}.Layout(gtx, mw.layoutToolbar)
		}),
		// 主区域 + 状态条
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return mw.layoutBody(gtx)
		}),
	)
}

func (mw *MainWindow) layoutToolbar(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		toolbarButton(mw.th, &mw.newBtn, "新建任务", func() {
			if mw.openNewTask != nil {
				mw.openNewTask()
			}
		}),
		toolbarSpacer(),
		toolbarButton(mw.th, &mw.pauseAllBtn, "暂停全部", mw.pauseAll),
		toolbarSpacer(),
		toolbarButton(mw.th, &mw.resumeAllBtn, "恢复全部", mw.resumeAll),
		hspace(SpaceMD),
		// 搜索框：填满剩余空间，垂直对齐
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			ed := material.Editor(mw.th, &mw.searchEditor, "搜索 URL 或保存路径…")
			ed.TextSize = unit.Sp(14)
			return ed.Layout(gtx)
		}),
		hspace(SpaceMD),
		toolbarButton(mw.th, &mw.settingsBtn, "设置", func() {
			OpenSettingsWindow(mw.a)
		}),
	)
}

// layoutBody 主区域：左侧 sidebar + 右侧 task list + 底部 status bar。
func (mw *MainWindow) layoutBody(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				// Sidebar：固定宽度，贴左边
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Dp(SidebarWidth)
					gtx.Constraints.Max.X = gtx.Dp(SidebarWidth)
					return mw.layoutSidebar(gtx)
				}),
				hspace(SpaceMD),
				// Task List：填满剩余
				layout.Flexed(1, mw.layoutTaskList),
			)
		}),
		vspace(SpaceSM),
		// Status Bar
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{
				Top:    SpaceSM,
				Bottom: SpaceSM,
				Left:   SpaceMD,
				Right:  SpaceMD,
			}.Layout(gtx, mw.layoutStatus)
		}),
	)
}

// layoutSidebar 侧栏：标题 + 6 个 RadioButton。
func (mw *MainWindow) layoutSidebar(gtx layout.Context) layout.Dimensions {
	filters := []struct{ key, label string }{
		{string(store.FilterAll), "全部"},
		{string(store.FilterDownloading), "下载中"},
		{string(store.FilterPaused), "已暂停"},
		{string(store.FilterCompleted), "已完成"},
		{string(store.FilterFailed), "失败"},
		{string(store.FilterFileLost), "文件丢失"},
	}
	// 标题与列表各占一行。
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body1(mw.th, "任务筛选")
			l.Font.Weight = font.Bold
			l.TextSize = unit.Sp(14)
			return l.Layout(gtx)
		}),
		vspace(SpaceMD),
		layout.Rigid(separator(mw.th)),
		vspace(SpaceSM),
		// 每个 RadioButton 一行，垂直间距 SM
		func() layout.FlexChild {
			children := make([]layout.FlexChild, 0, len(filters)*2)
			for _, f := range filters {
				k, lbl := f.key, f.label
				children = append(children,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return material.RadioButton(mw.th, &mw.filterEnum, k, lbl).Layout(gtx)
					}),
					vspace(SpaceSM),
				)
			}
			return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
			})
		}(),
	)
}

// layoutTaskList 任务列表区域。
func (mw *MainWindow) layoutTaskList(gtx layout.Context) layout.Dimensions {
	filter := store.StatusFilter(mw.a.Filter())
	tasks, _ := mw.a.Sched.List(filter, GlobalSettings.TaskSort)
	search := strings.ToLower(mw.a.Search())
	filtered := tasks
	if search != "" {
		filtered = make([]*store.Task, 0, len(tasks))
		for _, t := range tasks {
			if strings.Contains(strings.ToLower(t.URL), search) ||
				strings.Contains(strings.ToLower(t.SavePath), search) {
				filtered = append(filtered, t)
			}
		}
	}
	if len(filtered) == 0 {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(mw.th, "暂无任务，点击「新建任务」开始")
			l.TextSize = unit.Sp(14)
			return l.Layout(gtx)
		})
	}
	return mw.taskList.Layout(gtx, len(filtered),
		func(gtx layout.Context, i int) layout.Dimensions {
			t := filtered[i]
			r := mw.rowStateFor(i)
			return taskRowWidget(mw.th, r, t, mw.a)(gtx)
		},
	)
}

func (mw *MainWindow) rowStateFor(i int) *rowState {
	if r, ok := mw.rowStates[i]; ok {
		return r
	}
	r := &rowState{}
	mw.rowStates[i] = r
	return r
}

// layoutStatus 底部状态条：磁盘占用。
func (mw *MainWindow) layoutStatus(gtx layout.Context) layout.Dimensions {
	mw.disks = listMounts()
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(separator(mw.th)),
		vspace(SpaceSM),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(mw.th, "磁盘占用")
			l.Font.Weight = font.Bold
			l.TextSize = unit.Sp(13)
			return l.Layout(gtx)
		}),
		vspace(SpaceSM),
		func() layout.FlexChild {
			children := make([]layout.FlexChild, 0, len(mw.disks)+1)
			if len(mw.disks) == 0 {
				children = append(children, layout.Rigid(
					material.Caption(mw.th, "(无挂载信息)").Layout))
			} else {
				for _, d := range mw.disks {
					d := d
					children = append(children,
						vspace(SpaceXS),
						layout.Rigid(diskRow(mw.th, d)),
					)
				}
			}
			return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
			})
		}(),
	)
}

func (mw *MainWindow) pauseAll() {
	tasks, _ := mw.a.Sched.List(store.StatusFilter(mw.a.Filter()), GlobalSettings.TaskSort)
	for _, tk := range tasks {
		if tk.Status == store.TaskStatus.Downloading {
			_ = mw.a.Sched.Pause(tk.ID)
		}
	}
}

func (mw *MainWindow) resumeAll() {
	tasks, _ := mw.a.Sched.List(store.StatusFilter(mw.a.Filter()), GlobalSettings.TaskSort)
	for _, tk := range tasks {
		if tk.Status == store.TaskStatus.Paused || tk.Status == store.TaskStatus.Failed {
			_ = mw.a.Sched.Start(tk.ID)
		}
	}
}

// taskRowWidget 返回一行的 Layout 函数。
func taskRowWidget(th *material.Theme, rs *rowState, t *store.Task, a *App) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		// 行整体：左右 16dp 边距、上下 12dp
		return layout.Inset{
			Top: SpaceSM, Bottom: SpaceSM, Left: SpaceMD, Right: SpaceMD,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				// 第一行：文件名 + size + status
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							name := material.Body1(th, displayName(t))
							name.Font.Weight = font.Bold
							name.TextSize = unit.Sp(14)
							name.MaxLines = 1
							return name.Layout(gtx)
						}),
						hspace(SpaceSM),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return material.Body2(th, formatBytes(t.TotalSize)).Layout(gtx)
						}),
						hspace(SpaceSM),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(th, statusText(t.Status))
							l.TextSize = unit.Sp(12)
							return l.Layout(gtx)
						}),
					)
				}),
				vspace(SpaceXS),
				// 第二行：进度条
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					pct := 0.0
					if t.TotalSize > 0 {
						pct = float64(t.Downloaded) / float64(t.TotalSize)
						if pct > 1 {
							pct = 1
						}
					}
					return material.ProgressBar(th, float32(pct)).Layout(gtx)
				}),
				vspace(SpaceSM),
				// 第三行：操作按钮
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						rowActionBtn(th, &rs.startBtn, "开始", func() { _ = a.Sched.Start(t.ID) }),
						hspace(SpaceXS),
						rowActionBtn(th, &rs.pauseBtn, "暂停", func() { _ = a.Sched.Pause(t.ID) }),
						hspace(SpaceXS),
						rowActionBtn(th, &rs.openDirBtn, "目录", func() { openPath(filepath.Dir(t.SavePath)) }),
						hspace(SpaceXS),
						rowActionBtn(th, &rs.openFileBtn, "文件", func() { openPath(t.SavePath) }),
						hspace(SpaceXS),
						rowActionBtn(th, &rs.detailsBtn, "详情", func() { OpenChunkDetails(a, t) }),
						hspace(SpaceXS),
						rowActionBtn(th, &rs.cancelBtn, "删除", func() { _ = a.Sched.Delete(t.ID) }),
					)
				}),
			)
		})
	}
}

func rowActionBtn(th *material.Theme, c *widget.Clickable, label string, onClick func()) layout.FlexChild {
	return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		for c.Clicked(gtx) {
			if onClick != nil {
				onClick()
			}
		}
		b := material.Button(th, c, label)
		b.CornerRadius = ButtonRadius
		b.Inset = layout.UniformInset(unit.Dp(8))
		b.TextSize = unit.Sp(13)
		return b.Layout(gtx)
	})
}

// statusText 中文状态映射。
func statusText(s store.Status) string {
	switch s {
	case store.TaskStatus.Pending:
		return "等待中"
	case store.TaskStatus.Downloading:
		return "下载中"
	case store.TaskStatus.Paused:
		return "已暂停"
	case store.TaskStatus.Completed:
		return "已完成"
	case store.TaskStatus.Failed:
		return "失败"
	case store.TaskStatus.FileLost:
		return "文件丢失"
	default:
		return string(s)
	}
}