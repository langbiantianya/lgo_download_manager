// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/bittorrent"
	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/store"
)

// btPollInterval 是 BT 详情面板的刷新周期。peer 数与上下行速度每秒都在
// 变,1s 足够看清变化;再密只是徒增 IPC 往返。
const btPollInterval = time.Second

// btDetailsPanel 是「任务详情」窗口里的 BT 专属面板。
//
// 字段组织参照 qBittorrent / aria2 的 BT 信息面板:种子哈希、分片大小与
// 数量、上下行速度、已连接 peer 的构成(做种 / 下载)、做种状态、tracker
// 列表与文件清单。HTTP 任务走的是另一套布局(通用字段 + 分块马赛克),
// 见 chunk_details.go。
//
// 面板内容来自 Service.BTTaskDetail 的实时快照:这些数据每秒都在变,
// 刻意不落库(store.Task 是静态快照),窗口打开期间按 btPollInterval
// 轮询刷新。
type btDetailsPanel struct {
	// 标量字段的值标签。刷新只改文本,不重建 widget 树。
	lblName       *widget.Label
	lblTotal      *widget.Label
	lblDownloaded *widget.Label
	lblUploaded   *widget.Label
	lblRatio      *widget.Label
	lblDownSpeed  *widget.Label
	lblUpSpeed    *widget.Label
	lblPeers      *widget.Label
	lblTotalPeers *widget.Label
	lblSeeders    *widget.Label
	lblLeechers   *widget.Label
	lblPieceLen   *widget.Label
	lblPieces     *widget.Label
	lblPiecesDone *widget.Label
	lblState      *widget.Label
	lblCreated    *widget.Label
	lblCompleted  *widget.Label
	lblInfoHash   *widget.Label
	lblTrackers   *widget.Label
	lblNote       *widget.Label

	// filesBox 承载种子内每个文件的一行(名称 + 进度条 + 尺寸)。
	filesBox *fyne.Container

	// root 是面板的纵向内容根容器,整体放进一个滚动容器里展示。
	root *fyne.Container

	// mu 保护 fileRows:文件清单的行数会随元数据到达而变化,轮询 goroutine
	// 与 Fyne 事件线程都要碰它。
	mu       sync.Mutex
	fileRows []*btFileRow

	// done 在窗口关闭时关闭,让轮询 goroutine 退出。
	done      chan struct{}
	closeOnce sync.Once
}

// stop 停止轮询。窗口关闭时调用,重复调用是安全的。
func (p *btDetailsPanel) stop() {
	p.closeOnce.Do(func() { close(p.done) })
}

// btFileRow 是文件清单里的一行:文件名 + 进度条 + 尺寸/进度文本。
type btFileRow struct {
	name *widget.Label
	bar  *widget.ProgressBar
	info *widget.Label
}

// showBTDetails 打开 BT 任务的详情窗口。
//
// 与 showChunkDetails 的 HTTP 版布局完全不同:这里不放「字节区间马赛克」
// (BT 没有字节区间分片的概念),而是给出种子特有的实时信息。
//
// 返回值是面板本身,供测试断言渲染内容。
func showBTDetails(t *store.Task, svc Service, parent fyne.Window) *btDetailsPanel {
	name := taskName(t)
	titleStr := ilocale.TF("details.title", fmt.Sprintf("任务详情: %s", name), map[string]any{"Name": name})

	p := newBTDetailsPanel(t)

	w := fyne.CurrentApp().NewWindow(titleStr)
	w.SetContent(p.content())
	w.Resize(fyne.NewSize(760, 720))
	w.Show()

	w.SetOnClosed(func() { p.stop() })
	go p.poll(svc, t.ID)
	return p
}

// newBTDetailsPanel 构造面板与其 widget 树。
func newBTDetailsPanel(t *store.Task) *btDetailsPanel {
	p := &btDetailsPanel{done: make(chan struct{})}

	p.lblName = widget.NewLabelWithStyle(taskName(t), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	p.lblTotal = widget.NewLabel("-")
	p.lblDownloaded = widget.NewLabel("-")
	p.lblUploaded = widget.NewLabel("-")
	p.lblRatio = widget.NewLabel("-")
	p.lblDownSpeed = widget.NewLabel("-")
	p.lblUpSpeed = widget.NewLabel("-")
	p.lblPeers = widget.NewLabel("-")
	p.lblTotalPeers = widget.NewLabel("-")
	p.lblSeeders = widget.NewLabel("-")
	p.lblLeechers = widget.NewLabel("-")
	p.lblPieceLen = widget.NewLabel("-")
	p.lblPieces = widget.NewLabel("-")
	p.lblPiecesDone = widget.NewLabel("-")
	p.lblState = widget.NewLabel(ilocale.T("details.bt.state.waiting"))
	p.lblInfoHash = widget.NewLabel("-")
	p.lblInfoHash.TextStyle.Monospace = true
	p.lblInfoHash.Wrapping = fyne.TextWrapBreak
	p.lblTrackers = widget.NewLabel("-")
	p.lblTrackers.Wrapping = fyne.TextWrapBreak
	p.lblNote = widget.NewLabel("")
	p.lblNote.TextStyle.Italic = true
	p.lblNote.Wrapping = fyne.TextWrapWord

	urlLabel := widget.NewLabel(ilocale.TF("details.url", "URL: "+t.URL, map[string]any{"URL": t.URL}))
	urlLabel.Wrapping = fyne.TextWrapBreak
	protoLabel := widget.NewLabel(ilocale.TF("details.protocol", "协议: "+t.Protocol, map[string]any{"Protocol": t.Protocol}))
	pathLabel := widget.NewLabel(ilocale.TF("details.path", "路径: "+t.SavePath, map[string]any{"Path": t.SavePath}))
	pathLabel.Wrapping = fyne.TextWrapBreak
	p.lblCreated = widget.NewLabel(formatTime(t.CreatedAt))
	p.lblCompleted = widget.NewLabel(formatTime(t.CompletedAt))

	// 种子信息:四列「标签 + 值」网格,每行两组,紧凑且对齐。
	// 顺序按「用户最常看的」排:状态与速度在最前,分片与时间在最后。
	infoGrid := container.NewGridWithColumns(4,
		btFieldLabel("details.bt.state", "状态"), p.lblState,
		btFieldLabel("details.bt.downloadSpeed", "下载速度"), p.lblDownSpeed,
		btFieldLabel("details.bt.uploadSpeed", "上传速度"), p.lblUpSpeed,
		btFieldLabel("details.bt.total", "总大小"), p.lblTotal,
		btFieldLabel("details.bt.downloaded", "已下载"), p.lblDownloaded,
		btFieldLabel("details.bt.uploaded", "已上传"), p.lblUploaded,
		btFieldLabel("details.bt.peers", "连接数"), p.lblPeers,
		btFieldLabel("details.bt.totalPeers", "节点总数"), p.lblTotalPeers,
		btFieldLabel("details.bt.seeders", "做种者"), p.lblSeeders,
		btFieldLabel("details.bt.leechers", "下载者"), p.lblLeechers,
		btFieldLabel("details.bt.pieceLength", "分片大小"), p.lblPieceLen,
		btFieldLabel("details.bt.pieces", "分片数"), p.lblPieces,
		btFieldLabel("details.bt.piecesDone", "已完成分片"), p.lblPiecesDone,
		btFieldLabel("details.bt.ratio", "分享率"), p.lblRatio,
		btFieldLabel("details.bt.createdAt", "添加时间"), p.lblCreated,
		btFieldLabel("details.bt.completedAt", "完成时间"), p.lblCompleted,
	)

	// 种子哈希单独一行:40 位十六进制,换行断行显示。
	infoHashRow := container.NewBorder(nil, nil,
		btFieldLabel("details.bt.infoHash", "种子哈希"),
		nil,
		p.lblInfoHash,
	)

	p.filesBox = container.NewVBox()

	// 面板的行数多(十几个字段 + 三个分区),默认的行间距(theme.Padding=4)
	// 会把内容顶出一屏。这里用 2px 的紧凑间距,让「种子信息 / Tracker /
	// 文件」三个分区在常见分辨率下都能一屏看完。
	p.root = &fyne.Container{
		Layout: layout.NewCustomPaddedVBoxLayout(2),
		Objects: []fyne.CanvasObject{
			p.lblName,
			widget.NewSeparator(),
			urlLabel,
			protoLabel,
			pathLabel,
			widget.NewSeparator(),
			infoHashRow,
			btSectionLabel("details.bt.section", "种子信息"),
			infoGrid,
			widget.NewSeparator(),
			btSectionLabel("details.bt.section.trackers", "Tracker"),
			p.lblTrackers,
			widget.NewSeparator(),
			btSectionLabel("details.bt.section.files", "文件"),
			p.filesBox,
			p.lblNote,
		},
	}
	return p
}

// content 返回面板的可滚动内容。
func (p *btDetailsPanel) content() fyne.CanvasObject {
	scroll := container.NewScroll(p.root)
	scroll.SetMinSize(fyne.NewSize(560, 360))
	return scroll
}

// poll 周期性拉取 BT 详情并刷新面板,直到窗口关闭。
func (p *btDetailsPanel) poll(svc Service, taskID string) {
	// 先立刻取一次,避免面板在打开的第一秒里全是占位符。
	p.fetch(svc, taskID)
	ticker := time.NewTicker(btPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
		}
		p.fetch(svc, taskID)
	}
}

// fetch 取一次实时详情并刷新面板。任务不在运行时把状态切到「会话已
// 结束」,其余字段保留最后一次采样到的值。
func (p *btDetailsPanel) fetch(svc Service, taskID string) {
	select {
	case <-p.done:
		return
	default:
	}
	d, err := svc.BTTaskDetail(taskID)
	if err != nil {
		fyne.Do(func() { p.markInactive(err) })
		return
	}
	fyne.Do(func() { p.update(d) })
}

// update 用一次实时快照刷新面板。
func (p *btDetailsPanel) update(d *bittorrent.Detail) {
	if d == nil {
		return
	}
	if d.Name != "" {
		p.lblName.SetText(d.Name)
	}
	p.lblTotal.SetText(btFormatBytes(d.Total))
	p.lblDownloaded.SetText(btFormatBytes(d.Downloaded))
	p.lblUploaded.SetText(btFormatBytes(d.Uploaded))
	p.lblRatio.SetText(btFormatRatio(d.Uploaded, d.Downloaded))
	p.lblDownSpeed.SetText(formatBPS(d.SpeedBPS))
	p.lblUpSpeed.SetText(formatBPS(d.UploadBPS))
	p.lblPeers.SetText(strconv.Itoa(d.Peers))
	p.lblTotalPeers.SetText(strconv.Itoa(d.TotalPeers))
	p.lblSeeders.SetText(strconv.Itoa(d.Seeders))
	p.lblLeechers.SetText(strconv.Itoa(d.Leechers))
	p.lblPieceLen.SetText(btFormatBytes(d.PieceLength))
	p.lblPieces.SetText(strconv.Itoa(d.Pieces))
	p.lblPiecesDone.SetText(fmt.Sprintf("%d / %d", d.PiecesDone, d.Pieces))
	p.lblState.SetText(btStateText(d))
	if d.InfoHash != "" {
		p.lblInfoHash.SetText(d.InfoHash)
	}
	if len(d.Trackers) > 0 {
		p.lblTrackers.SetText(strings.Join(d.Trackers, "\n"))
	} else {
		p.lblTrackers.SetText(ilocale.T("details.bt.trackers.empty"))
	}
	p.lblNote.SetText("")
	p.updateFiles(d.Files)
}

// markInactive 在任务不在运行(未启动 / 已暂停 / 已完成且未做种)时调用:
// 状态切到「会话已结束」,其余字段保留最后一次采样到的值。
func (p *btDetailsPanel) markInactive(err error) {
	p.lblState.SetText(ilocale.T("details.bt.state.inactive"))
	if err != nil {
		p.lblNote.SetText(ilocale.TF("details.bt.note", "提示: {{ .Err }}", map[string]any{"Err": err.Error()}))
	}
}

// updateFiles 刷新种子内的文件清单。文件数不变时只更新进度,避免重建
// widget 树;元数据刚到(文件数从 0 变成 N)时才整体重建。
func (p *btDetailsPanel) updateFiles(files []bittorrent.FileProgress) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(files) == len(p.fileRows) {
		for i, f := range files {
			p.fileRows[i].update(f)
		}
		return
	}
	p.fileRows = make([]*btFileRow, 0, len(files))
	for _, f := range files {
		row := newBTFileRow()
		row.update(f)
		p.fileRows = append(p.fileRows, row)
	}
	p.filesBox.Objects = nil
	for _, r := range p.fileRows {
		p.filesBox.Add(r.widget())
	}
	p.filesBox.Refresh()
}

// newBTFileRow 构造一行文件条目。
func newBTFileRow() *btFileRow {
	return &btFileRow{
		name: widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		bar:  widget.NewProgressBar(),
		info: widget.NewLabel(""),
	}
}

// widget 返回这一行对应的容器。
func (r *btFileRow) widget() fyne.CanvasObject {
	return container.NewVBox(r.name, r.bar, r.info)
}

// update 用一次文件采样刷新这一行。
func (r *btFileRow) update(f bittorrent.FileProgress) {
	r.name.SetText(f.Path)
	frac := 0.0
	if f.Length > 0 {
		frac = float64(f.Completed) / float64(f.Length)
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	r.bar.SetValue(frac)
	r.info.SetText(ilocale.TF("details.bt.file.progress",
		"{{ .Done }} / {{ .Total }} ({{ .Pct }}%)",
		map[string]any{
			"Done":  btFormatBytes(f.Completed),
			"Total": btFormatBytes(f.Length),
			"Pct":   fmt.Sprintf("%.1f", frac*100),
		}))
}

// btFieldLabel 返回信息面板里的字段名标签(斜体,与值区分开)。
func btFieldLabel(id, def string) *widget.Label {
	return widget.NewLabelWithStyle(ilocale.TS(id, def), fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
}

// btSectionLabel 返回分区标题。
func btSectionLabel(id, def string) *widget.Label {
	return widget.NewLabelWithStyle(ilocale.TS(id, def), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

// btFormatBytes 格式化字节数。与 formatBytes 的差别:0 显示成 "0 B"
// 而不是「未知大小」——BT 面板里 0 是有意义的(还没收到元数据、上传量为 0)。
func btFormatBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	return formatBytes(n)
}

// btFormatRatio 计算并格式化分享率(已上传 / 已下载)。
func btFormatRatio(uploaded, downloaded int64) string {
	if downloaded <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", float64(uploaded)/float64(downloaded))
}

// btStateText 把种子的状态翻译成面板上的文字。
func btStateText(d *bittorrent.Detail) string {
	switch {
	case d.Seeding:
		return ilocale.T("details.bt.state.seeding")
	case d.Complete:
		return ilocale.T("details.bt.state.complete")
	default:
		return ilocale.T("details.bt.state.downloading")
	}
}
