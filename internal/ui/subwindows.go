// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// --- 设置窗口 ---

// OpenSettingsWindow 打开设置窗口。
func OpenSettingsWindow(a *App) {
	w := new(app.Window)
	w.Option(app.Title("Settings"))
	w.Option(app.Size(unit.Dp(640), unit.Dp(640)))

	sw := &settingsWindowState{a: a, w: w, th: newTheme()}
	sw.scrollList.Axis = layout.Vertical
	sw.refreshFromStore()
	runWindow(w, sw.Layout)
}

// settingsWindowState 设置窗口状态。
type settingsWindowState struct {
	a  *App
	w  *app.Window
	th *material.Theme

	scrollList widget.List

	dirEntry     widget.Editor
	threadsEntry widget.Editor
	chunkEntry   widget.Editor
	uaEntry      widget.Editor
	cookiesEntry widget.Editor
	browseBtn    widget.Clickable

	ftpPassive widget.Bool
	prealloc   widget.Bool

	sortEnum widget.Enum

	saveBtn  widget.Clickable
	closeBtn widget.Clickable

	saveTimer *time.Timer
}

func (s *settingsWindowState) refreshFromStore() {
	s.dirEntry.SetText(GlobalSettings.DefaultSaveDir)
	s.threadsEntry.SetText(strconv.FormatInt(int64(GlobalSettings.DefaultThreads), 10))
	s.chunkEntry.SetText(strconv.FormatInt(GlobalSettings.MinChunkSize/mib, 10))
	s.uaEntry.SetText(GlobalSettings.UserAgent)
	s.cookiesEntry.SetText(GlobalSettings.Cookies)
	s.ftpPassive.Value = GlobalSettings.FTPPassive
	s.prealloc.Value = GlobalSettings.Prealloc
	s.sortEnum.Value = string(GlobalSettings.TaskSort)
}

func (s *settingsWindowState) scheduleSave() {
	if s.saveTimer != nil {
		s.saveTimer.Stop()
	}
	s.saveTimer = time.AfterFunc(500*time.Millisecond, func() {
		if err := SaveSettings(s.a.Store); err != nil {
			fmt.Fprintln(os.Stderr, "save settings:", err)
		}
	})
}

func (s *settingsWindowState) applyEditorsToStore() {
	GlobalSettings.DefaultSaveDir = strings.TrimSpace(s.dirEntry.Text())
	if n, err := strconv.Atoi(strings.TrimSpace(s.threadsEntry.Text())); err == nil && n > 0 {
		GlobalSettings.DefaultThreads = n
	}
	if n, err := strconv.Atoi(strings.TrimSpace(s.chunkEntry.Text())); err == nil && n > 0 {
		GlobalSettings.MinChunkSize = int64(n) * mib
	}
	GlobalSettings.UserAgent = s.uaEntry.Text()
	GlobalSettings.Cookies = s.cookiesEntry.Text()
	s.scheduleSave()
}

// formItems 返回所有表单字段——按 widget 函数列表形式渲染。
//
// 注意：返回 []layout.Widget，每一项可以独立用 widget.List 渲染；
// 字段之间用 vSpaceWidget 占位。
func (s *settingsWindowState) formItems() []layout.Widget {
	return []layout.Widget{
		func(gtx layout.Context) layout.Dimensions {
			l := material.H4(s.th, "设置")
			l.Font.Weight = font.Bold
			return l.Layout(gtx)
		},
		vSpaceWidget(SpaceMD),
		separator(s.th),
		vSpaceWidget(SpaceMD),

		s.fieldWithBrowse(),
		vSpaceWidget(SpaceMD),
		s.field("默认并发线程数", material.Editor(s.th, &s.threadsEntry, "").Layout),
		vSpaceWidget(SpaceMD),
		s.field("最小分块大小 (MiB)", material.Editor(s.th, &s.chunkEntry, "").Layout),
		vSpaceWidget(SpaceMD),
		s.field("默认 User-Agent", material.Editor(s.th, &s.uaEntry, "").Layout),
		vSpaceWidget(SpaceMD),
		s.field("默认 Cookie", material.Editor(s.th, &s.cookiesEntry, "").Layout),
		vSpaceWidget(SpaceMD),
		s.fieldBool("FTP PASV 被动模式", &s.ftpPassive),
		vSpaceWidget(SpaceMD),
		s.fieldBool("磁盘预分配", &s.prealloc),
		vSpaceWidget(SpaceMD),
		s.fieldSort(),
	}
}

// Layout 整体框架：滚动表单区 + 底部分隔线 + 底部按钮行
func (s *settingsWindowState) Layout(gtx layout.Context) layout.Dimensions {
	// 处理各 Editor 的 ChangeEvent。
	for _, ed := range []*widget.Editor{
		&s.dirEntry, &s.threadsEntry, &s.chunkEntry, &s.uaEntry, &s.cookiesEntry,
	} {
		for {
			ev, ok := ed.Update(gtx)
			if !ok {
				break
			}
			if _, isChange := ev.(widget.ChangeEvent); isChange {
				s.applyEditorsToStore()
			}
		}
	}
	for s.browseBtn.Clicked(gtx) {
		dir := GlobalSettings.DefaultSaveDir
		if dir == "" {
			dir = homeDir()
		}
		_ = exec.Command("xdg-open", dir).Start()
	}
	if s.ftpPassive.Update(gtx) {
		GlobalSettings.FTPPassive = s.ftpPassive.Value
		s.scheduleSave()
	}
	if s.prealloc.Update(gtx) {
		GlobalSettings.Prealloc = s.prealloc.Value
		s.scheduleSave()
	}
	if s.sortEnum.Update(gtx) {
		GlobalSettings.TaskSort = store.TaskSort(s.sortEnum.Value)
		s.scheduleSave()
	}
	for s.saveBtn.Clicked(gtx) {
		s.applyEditorsToStore()
		_ = SaveSettings(s.a.Store)
	}
	for s.closeBtn.Clicked(gtx) {
		s.w.Perform(system.ActionClose)
	}

	items := s.formItems()

	return layout.Inset{
		Top: SpaceLG, Bottom: SpaceLG, Left: SpaceLG, Right: SpaceLG,
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			// 滚动表单（占满中间）
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				// 滚动的 Vertical Flex——每项 Min.Y=48 保证 widget.List 正确 measure。
				return s.scrollList.Layout(gtx, len(items),
					func(gtx layout.Context, i int) layout.Dimensions {
						w := items[i]
						// 强制最小 Y 高度，否则 Editor/CheckBox 在 Flex 中
						// 会让 widget.List 错以为每项高 0，结果只显示前几项。
						gtx.Constraints.Min.Y = gtx.Dp(RowHeight)
						return w(gtx)
					},
				)
			}),
			layout.Rigid(vSpaceWidget(SpaceSM)),
			layout.Rigid(separator(s.th)),
			layout.Rigid(vSpaceWidget(SpaceMD)),
			// 底部按钮：右对齐
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.End}.Layout(gtx,
					toolbarButton(s.th, &s.saveBtn, "立即保存", nil),
					hspace(SpaceSM),
					toolbarButton(s.th, &s.closeBtn, "关闭", nil),
				)
			}),
		)
	})
}

// field "label 在上方 + 控件占满宽度" 的通用行。
func (s *settingsWindowState) field(label string, w layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(s.th, label)
				l.Font.Weight = font.Bold
				l.TextSize = unit.Sp(13)
				return l.Layout(gtx)
			}),
			vspace(SpaceXS),
			layout.Rigid(w),
		)
	}
}
func (s *settingsWindowState) fieldBool(label string, b *widget.Bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(s.th, label)
				l.Font.Weight = font.Bold
				l.TextSize = unit.Sp(13)
				return l.Layout(gtx)
			}),
			vspace(SpaceXS),
			layout.Rigid(material.CheckBox(s.th, b, "").Layout),
		)
	}
}

func (s *settingsWindowState) fieldWithBrowse() layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(s.th, "默认保存目录")
				l.Font.Weight = font.Bold
				l.TextSize = unit.Sp(13)
				return l.Layout(gtx)
			}),
			vspace(SpaceXS),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, material.Editor(s.th, &s.dirEntry, "").Layout),
					hspace(SpaceSM),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return material.Button(s.th, &s.browseBtn, "浏览…").Layout(gtx)
					}),
				)
			}),
		)
	}
}

func (s *settingsWindowState) fieldSort() layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(s.th, "任务列表排序")
				l.Font.Weight = font.Bold
				l.TextSize = unit.Sp(13)
				return l.Layout(gtx)
			}),
			vspace(SpaceXS),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(material.RadioButton(s.th, &s.sortEnum, string(store.SortCreatedDesc), "添加时间倒序（最新在前）").Layout),
					vspace(SpaceXS),
					layout.Rigid(material.RadioButton(s.th, &s.sortEnum, string(store.SortCreatedAsc), "添加时间正序（最老在前）").Layout),
					vspace(SpaceXS),
					layout.Rigid(material.RadioButton(s.th, &s.sortEnum, string(store.SortNameAsc), "文件名正序").Layout),
					vspace(SpaceXS),
					layout.Rigid(material.RadioButton(s.th, &s.sortEnum, string(store.SortNameDesc), "文件名倒序").Layout),
				)
			}),
		)
	}
}

// --- 新建任务窗口 ---

// OpenNewTaskWindow 打开新建任务窗口。
func OpenNewTaskWindow(a *App) {
	w := new(app.Window)
	w.Option(app.Title("New Task"))
	w.Option(app.Size(unit.Dp(560), unit.Dp(420)))

	nt := &newTaskWindowState{a: a, w: w, th: newTheme()}
	if GlobalSettings.DefaultSaveDir != "" {
		defaultName := "download.bin"
		nt.saveEntry.SetText(uniqueSavePath(filepath.Join(GlobalSettings.DefaultSaveDir, defaultName)))
	}
	runWindow(w, nt.Layout)
}

// newTaskWindowState 新建任务窗口状态。
type newTaskWindowState struct {
	a  *App
	w  *app.Window
	th *material.Theme

	urlEntry  widget.Editor
	saveEntry widget.Editor
	browseBtn widget.Clickable
	startBtn  widget.Clickable
	cancelBtn widget.Clickable

	sizeProbe    string
	probeCancel  context.CancelFunc
	probeSeq     uint64
	lastProbeURL string
}

func (n *newTaskWindowState) Layout(gtx layout.Context) layout.Dimensions {
	for {
		ev, ok := n.urlEntry.Update(gtx)
		if !ok {
			break
		}
		if _, isChange := ev.(widget.ChangeEvent); isChange {
			n.probeURL(n.urlEntry.Text())
		}
	}
	for n.cancelBtn.Clicked(gtx) {
		n.w.Perform(system.ActionClose)
	}
	for n.startBtn.Clicked(gtx) {
		n.onStart()
		n.w.Perform(system.ActionClose)
	}
	for n.browseBtn.Clicked(gtx) {
		dir := GlobalSettings.DefaultSaveDir
		if dir == "" {
			dir = homeDir()
		}
		_ = exec.Command("xdg-open", dir).Start()
	}

	return layout.Inset{
		Top: SpaceLG, Bottom: SpaceLG, Left: SpaceLG, Right: SpaceLG,
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.H4(n.th, "新建下载任务")
				l.Font.Weight = font.Bold
				return l.Layout(gtx)
			}),
			vspace(SpaceMD),
			layout.Rigid(separator(n.th)),
			vspace(SpaceMD),

			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(n.th, "下载链接 (URL)")
						l.Font.Weight = font.Bold
						l.TextSize = unit.Sp(13)
						return l.Layout(gtx)
					}),
					vspace(SpaceXS),
					layout.Rigid(material.Editor(n.th, &n.urlEntry, "https://...").Layout),
					vspace(SpaceXS),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return material.Caption(n.th, "文件大小: "+n.sizeProbe).Layout(gtx)
					}),
				)
			}),
			vspace(SpaceMD),

			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(n.th, "保存路径")
						l.Font.Weight = font.Bold
						l.TextSize = unit.Sp(13)
						return l.Layout(gtx)
					}),
					vspace(SpaceXS),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, material.Editor(n.th, &n.saveEntry, "/path/to/file").Layout),
							hspace(SpaceSM),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return material.Button(n.th, &n.browseBtn, "浏览…").Layout(gtx)
							}),
						)
					}),
				)
			}),
			vspace(SpaceLG),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.End}.Layout(gtx,
					toolbarButton(n.th, &n.cancelBtn, "取消", nil),
					hspace(SpaceSM),
					toolbarButton(n.th, &n.startBtn, "开始下载", nil),
				)
			}),
		)
	})
}

func (n *newTaskWindowState) onStart() {
	url := n.urlEntry.Text()
	if url == "" {
		return
	}
	save := n.saveEntry.Text()
	if save == "" {
		save = filepath.Join(GlobalSettings.DefaultSaveDir, "download.bin")
	}
	proto, err := protocol.DetectKind(url, "")
	if err != nil {
		n.sizeProbe = "协议错误: " + err.Error()
		return
	}
	tk, err := n.a.Sched.Add(scheduler.AddTaskInput{
		URL:      url,
		SavePath: save,
		Protocol: proto,
		Auth: protocol.AuthOptions{
			UserAgent:  GlobalSettings.UserAgent,
			Cookies:    GlobalSettings.Cookies,
			FTPPassive: GlobalSettings.FTPPassive,
		},
		ChunkCount:   GlobalSettings.DefaultThreads,
		MinChunkSize: GlobalSettings.MinChunkSize,
	})
	if err != nil {
		n.sizeProbe = "添加失败: " + err.Error()
		return
	}
	_ = n.a.Sched.Start(tk.ID)
}

func (n *newTaskWindowState) probeURL(rawURL string) {
	if rawURL == "" {
		n.sizeProbe = ""
		n.lastProbeURL = ""
		return
	}
	if rawURL == n.lastProbeURL {
		return
	}
	n.lastProbeURL = rawURL
	if n.probeCancel != nil {
		n.probeCancel()
	}
	n.probeSeq++
	seq := n.probeSeq
	n.sizeProbe = "探测中…"
	n.w.Invalidate()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	n.probeCancel = cancel
	go func() {
		defer cancel()
		u, err := url.Parse(rawURL)
		if err != nil || u.Path == "" {
			return
		}
		proto, err := protocol.DetectKind(rawURL, "")
		if err != nil {
			return
		}
		driver, err := protocol.New(rawURL, proto, protocol.Auth{AuthOptions: protocol.AuthOptions{
			UserAgent:  GlobalSettings.UserAgent,
			Cookies:    GlobalSettings.Cookies,
			FTPPassive: GlobalSettings.FTPPassive,
		}})
		if err != nil {
			return
		}
		defer driver.Close()
		caps, err := driver.Probe(ctx)
		if err != nil || caps.TotalSize <= 0 {
			return
		}
		if seq == n.probeSeq {
			n.sizeProbe = formatSize(caps.TotalSize)
			n.w.Invalidate()
		}
	}()
}

// --- 任务详情窗口 ---

// OpenChunkDetails 打开分块详情窗口。
func OpenChunkDetails(a *App, t *store.Task) {
	if t == nil {
		return
	}
	w := new(app.Window)
	title := fmt.Sprintf("Details: %s", displayName(t))
	w.Option(app.Title(title))
	w.Option(app.Size(unit.Dp(720), unit.Dp(540)))

	dw := &detailsWindowState{
		a:  a,
		w:  w,
		th: newTheme(),
		t:  t,
	}
	dw.mosaic.resizeForTotal(t.TotalSize)
	dw.mosaic.update(t.ChunkProgress, t.ChunkRanges, t.TotalSize)

	ch, unsub := a.Sched.Subscribe()
	go func() {
		defer unsub()
		for ev := range ch {
			if ev.Task == nil || ev.Task.ID != t.ID {
				continue
			}
			dw.mosaic.update(ev.Task.ChunkProgress, ev.Task.ChunkRanges, ev.Task.TotalSize)
			w.Invalidate()
		}
	}()

	runWindow(w, dw.Layout)
}

// detailsWindowState 任务详情窗口状态。
type detailsWindowState struct {
	a  *App
	w  *app.Window
	th *material.Theme
	t  *store.Task

	mosaic chunkMosaic
}

func (d *detailsWindowState) Layout(gtx layout.Context) layout.Dimensions {
	return layout.Inset{
		Top: SpaceLG, Bottom: SpaceLG, Left: SpaceLG, Right: SpaceLG,
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.H4(d.th, fmt.Sprintf("任务详情: %s", displayName(d.t)))
				l.Font.Weight = font.Bold
				return l.Layout(gtx)
			}),
			vspace(SpaceMD),
			layout.Rigid(separator(d.th)),
			vspace(SpaceMD),

			layout.Rigid(d.infoRow("URL", d.t.URL)),
			vspace(SpaceSM),
			layout.Rigid(d.infoRow("路径", d.t.SavePath)),
			vspace(SpaceSM),
			layout.Rigid(d.infoRow("总计", formatBytes(d.t.TotalSize))),
			vspace(SpaceSM),
			layout.Rigid(d.infoRow("添加时间", formatTime(d.t.CreatedAt))),
			vspace(SpaceMD),

			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(d.th, "文件分块详情")
				l.Font.Weight = font.Bold
				return l.Layout(gtx)
			}),
			vspace(SpaceSM),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return d.mosaic.Layout(gtx)
			}),
		)
	})
}

func (d *detailsWindowState) infoRow(label, value string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(d.th, label)
				l.Font.Weight = font.Bold
				l.TextSize = unit.Sp(13)
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Spacer{Width: unit.Dp(96)}.Layout(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return material.Body2(d.th, value).Layout(gtx)
			}),
		)
	}
}