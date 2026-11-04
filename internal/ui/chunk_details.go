// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/rpc"
)

// blockSize 是马赛克中使用的固定可视分块大小（1 MiB）。
const blockSize int64 = 1 << 20

// maxTiles 限制马赛克中的瓦片数量，避免为超大文件渲染成千上万个 widget。
// 1024 ≈ 在默认比例下显示 1 GiB；更大的文件会将多个 MiB 合并到一块瓦片中。
const maxTiles = 1024

// tileSize 是每个马赛克瓦片的边长（以 point 为单位）。
// 小方块会排列成一面瓦片墙。
const tileSize = 12

// chunkMosaicColumns keeps the raster layout stable across redraws. The actual
// canvas width determines the tile pixel size on high-DPI displays.
const chunkMosaicColumns = 46

// mosaicThrottle 是两次瓦片重建之间的最小间隔。进度事件远密于此，
// 而每次重建都要重算最多 maxTiles 个瓦片的覆盖比例。
const mosaicThrottle = 400 * time.Millisecond

// chunkMosaic 将文件渲染为一面由小方块组成的瓦片墙。
// 每个瓦片代表 blockSize 字节（对于超大文件可能更多，
// 以保证 widget 数量不超过 maxTiles）。已完成的范围将
// 瓦片染为绿色；部分完成的范围使用主题的主色；空白范围
// 使用输入框背景色。
//
// 整面墙由一张 canvas.Raster 绘制。BT 详情页已经采用同样的方案；
// 它避免了为最多 1024 个瓦片分别创建 Rectangle、renderer 与纹理对象。
type chunkMosaic struct {
	raster  *canvas.Raster
	taskID  string
	total   int64
	blockSz int64
	blocks  int
	covered []float64
	palette chunkMosaicPalette

	// mu 串行化全部内部状态与 widget 树变更。
	// update/resizeForTotal 会被事件派发 goroutine 与窗口自身并发调用
	// （测试与关闭路径直接调用，不经过 fyne.Do），没有这把锁就会
	// 并发改写覆盖数据并踩到 Fyne 的绘制缓存。
	mu sync.Mutex

	// seq 每次 update() 递增；补渲染捕获自己的值，若期间已有更新的
	// 输入到达就放弃补渲染，避免把旧状态盖回去。
	seq uint64

	// applied* 是上一次实际重建瓦片所用的输入，用于跳过重复渲染。
	appliedAt     time.Time
	appliedProg   []int64
	appliedRanges []int64
	appliedTotal  int64

	// pending* 是节流窗口内被丢弃的最新输入；窗口结束后由 flushPending
	// 补渲染一次，保证最终状态不会因为落在窗口内而永久丢失。
	pending       bool
	pendingSeq    uint64
	pendingProg   []int64
	pendingRanges []int64
	pendingTotal  int64
	flushTimer    *time.Timer
}

type chunkMosaicPalette struct {
	empty   color.NRGBA
	primary color.NRGBA
	success color.NRGBA
}

// showChunkDetails 打开一个窗口，展示任务的下载进度。
//
// HTTP/FTP 等任务按分块渲染为瓦片墙(参见 chunkMosaic);BT 任务换一套
// 面板——种子哈希、分片、上下行速度、peer 构成、tracker 与文件清单
// (参见 bt_details.go),与 qBittorrent / aria2 的 BT 信息面板一致。
//
// 一个任务只开一个详情窗口:已经开着的把老窗口置前(参见
// details_window.go),不再新开一个。
//
// 返回值是本次新建的 BT 面板(非 BT 任务、或者窗口已存在时为 nil),
// 供测试断言渲染内容。
func showChunkDetails(t *api.Task, svc rpc.Client, parent fyne.Window) *btDetailsPanel {
	if isBittorrentTask(t) {
		return showBTDetails(t, svc, parent)
	}
	// 同一任务已经开着详情窗口:置前即可,别再开一个一模一样的。
	if focusExistingDetails(t.ID) {
		return nil
	}
	name := taskName(t)
	titleStr := ilocale.TF("details.title", fmt.Sprintf("任务详情: %s", name), map[string]any{"Name": name})

	urlLabel := widget.NewLabel(ilocale.TF("details.url", "URL: "+t.URL, map[string]any{"URL": t.URL}))
	urlLabel.Wrapping = fyne.TextWrapWord
	protoLabel := widget.NewLabel(ilocale.TF("details.protocol", fmt.Sprintf("协议: %s", t.Protocol), map[string]any{"Protocol": t.Protocol}))
	pathLabel := widget.NewLabel(ilocale.TF("details.path", "路径: "+t.SavePath, map[string]any{"Path": t.SavePath}))
	sizeLabel := widget.NewLabel(ilocale.TF("details.total", fmt.Sprintf("总计: %s", taskSizeText(t.TotalSize)), map[string]any{"Size": taskSizeText(t.TotalSize)}))
	uaLabel := widget.NewLabel(uaForTask(t))
	createdAtLabel := widget.NewLabel(ilocale.TF("details.createdAt", "添加时间: "+formatTime(t.CreatedAt), map[string]any{"Time": formatTime(t.CreatedAt)}))
	completedAtLabel := widget.NewLabel(ilocale.TF("details.completedAt", "下载完成时间: "+formatTime(t.CompletedAt), map[string]any{"Time": formatTime(t.CompletedAt)}))

	mosaic := newChunkMosaic(t.ID)
	mosaic.resizeForTotal(t.TotalSize)
	mosaic.update(t.ChunkProgress, t.ChunkRanges, t.TotalSize)

	header := container.NewVBox(
		widget.NewLabel(titleStr),
		widget.NewSeparator(),
		urlLabel,
		protoLabel,
		pathLabel,
		sizeLabel,
		uaLabel,
		createdAtLabel,
		completedAtLabel,
		widget.NewSeparator(),
		widget.NewLabel(ilocale.T("details.section.chunks")),
	)
	content := container.NewVBox(header, mosaic.content())
	scroll := container.NewScroll(content)
	scroll.SetMinSize(fyne.NewSize(560, 360))

	w := fyne.CurrentApp().NewWindow(titleStr)
	w.SetContent(scroll)
	w.Resize(fyne.NewSize(640, 460))
	w.Show()

	// 先订阅业务事件流拿到 unsub，再登记窗口（登记时会挂上 SetOnClosed），
	// 保证窗口关闭时取消订阅。
	ch, unsub := svc.Subscribe()
	events := newUIEventPump(fyne.Do, func(ev api.Event) {
		mosaic.update(ev.Task.ChunkProgress, ev.Task.ChunkRanges, ev.Task.TotalSize)
	})
	registerDetailsWindow(t.ID, w, func() {
		events.stop()
		unsub()
	})
	go func() {
		for ev := range ch {
			if ev.Task == nil || ev.Task.ID != t.ID {
				continue
			}
			events.enqueue(ev)
		}
	}()
	return nil
}

// newChunkMosaic 创建一个空马赛克；resizeForTotal 会在知道文件大小后
// 分配瓦片网格。
func newChunkMosaic(taskID string) *chunkMosaic {
	m := &chunkMosaic{
		taskID:  taskID,
		palette: currentChunkMosaicPalette(),
	}
	m.raster = canvas.NewRaster(m.generate)
	m.raster.SetMinSize(fyne.NewSize(560, tileSize))
	return m
}

func (m *chunkMosaic) content() fyne.CanvasObject { return m.raster }

// resizeForTotal 根据文件大小重建分片覆盖数据与最小显示高度。
func (m *chunkMosaic) resizeForTotal(total int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resizeLocked(total)
}

// resizeLocked 是 resizeForTotal 的实现；调用者必须持有 m.mu。
func (m *chunkMosaic) resizeLocked(total int64) {
	// 每个瓦片代表 blockSize 字节（对于超大文件可能更多，
	// 以保证瓦片总数不超过 maxTiles）。
	if total <= 0 {
		m.total = 0
		m.blocks = 0
		m.blockSz = 0
		m.covered = nil
		m.raster.SetMinSize(fyne.NewSize(560, tileSize))
		m.raster.Refresh()
		return
	}
	blocks := int(total / blockSize)
	if total%blockSize != 0 {
		blocks++
	}
	if blocks > maxTiles {
		blocks = maxTiles
		m.blockSz = total / int64(maxTiles)
		if total%int64(maxTiles) != 0 {
			m.blockSz++
		}
	} else {
		m.blockSz = blockSize
	}
	m.total = total
	m.blocks = blocks
	m.covered = make([]float64, blocks)
	m.updateMinSizeLocked()
	m.raster.Refresh()
}

// update 根据给定的分块布局和进度重绘整张瓦片图。
//
// 进度事件远比肉眼可辨的刷新率密集。因此这里做两件事：
//   - 输入与上一次实际渲染完全相同 → 直接返回；
//   - 距上次重建不足 mosaicThrottle → 只记下最新输入，
//     窗口结束后由 flushPending 补渲染（最终状态不会丢失）。
func (m *chunkMosaic) update(chunkProgress, chunkRanges []int64, totalSize int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++

	if !m.appliedAt.IsZero() && m.appliedTotal == totalSize &&
		slices.Equal(m.appliedProg, chunkProgress) &&
		slices.Equal(m.appliedRanges, chunkRanges) {
		return
	}

	if since := time.Since(m.appliedAt); since < mosaicThrottle {
		m.pending = true
		m.pendingSeq = m.seq
		// 拷贝输入：调用方的切片可能被复用/改写。
		m.pendingProg = append(m.pendingProg[:0], chunkProgress...)
		m.pendingRanges = append(m.pendingRanges[:0], chunkRanges...)
		m.pendingTotal = totalSize
		if m.flushTimer == nil {
			m.flushTimer = time.AfterFunc(mosaicThrottle-since, m.flushPending)
		}
		return
	}

	m.applyLocked(chunkProgress, chunkRanges, totalSize)
}

// flushPending 在节流窗口结束后补渲染最后一次被丢弃的输入。
func (m *chunkMosaic) flushPending() {
	m.mu.Lock()
	m.flushTimer = nil
	if !m.pending {
		m.mu.Unlock()
		return
	}
	seq := m.pendingSeq
	prog := m.pendingProg
	ranges := m.pendingRanges
	total := m.pendingTotal
	m.pending = false
	m.mu.Unlock()

	// 定时器回调不在 Fyne 事件线程上，widget 树的改动必须回到事件线程。
	fyne.Do(func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.seq != seq || m.pending {
			// 期间已有更新的输入：要么已经渲染，要么已有新的补渲染在排队。
			return
		}
		m.applyLocked(prog, ranges, total)
	})
}

// applyLocked 用给定的分块布局和进度重建瓦片。调用者必须持有 m.mu。
func (m *chunkMosaic) applyLocked(chunkProgress, chunkRanges []int64, totalSize int64) {
	// 算法与之前相同：遍历每个分块的范围，确定每个瓦片字节范围内
	// 被覆盖的比例。
	//
	// chunkProgress 是每个分块自起始位置的偏移；chunkRanges 是成对的
	// [start0,end0,start1,end1,...]，end 为闭区间。
	m.appliedAt = time.Now()
	m.appliedTotal = totalSize
	m.appliedProg = append(m.appliedProg[:0], chunkProgress...)
	m.appliedRanges = append(m.appliedRanges[:0], chunkRanges...)
	m.palette = currentChunkMosaicPalette()

	if totalSize != m.total {
		m.total = totalSize
		m.resizeLocked(totalSize)
	}
	if totalSize <= 0 || m.blocks == 0 {
		return
	}

	ranges := chunkRanges
	progress := chunkProgress
	if len(ranges) == 0 || len(ranges) != 2*len(progress) {
		// 回退方案：基于 progress 数组长度假定 N 个均匀分块。
		threads := len(progress)
		if threads == 0 {
			m.covered = make([]float64, m.blocks)
			m.raster.Refresh()
			return
		}
		base := totalSize / int64(threads)
		rem := totalSize % int64(threads)
		ranges = make([]int64, 0, 2*threads)
		cur := int64(0)
		for ti := 0; ti < threads; ti++ {
			size := base
			if ti == threads-1 {
				size += rem
			}
			ranges = append(ranges, cur, cur+size-1)
			cur += size
		}
	}

	covered := m.covered
	if len(covered) != m.blocks {
		covered = make([]float64, m.blocks)
		m.covered = covered
	}
	for b := 0; b < m.blocks; b++ {
		tileStart := int64(b) * m.blockSz
		tileEnd := tileStart + m.blockSz
		if tileEnd > totalSize {
			tileEnd = totalSize
		}
		tileSize := tileEnd - tileStart
		var coveredBytes int64
		for i := 0; i+1 < len(ranges); i += 2 {
			cStart := ranges[i]
			cEnd := ranges[i+1] + 1
			if cEnd <= tileStart || cStart >= tileEnd {
				continue
			}
			overlapStart := max64(cStart, tileStart)
			overlapEnd := min64(cEnd, tileEnd)
			var prog int64
			if i/2 < len(progress) {
				prog = progress[i/2]
			}
			writtenThrough := cStart + prog
			if writtenThrough <= overlapStart {
				continue
			}
			if writtenThrough >= overlapEnd {
				coveredBytes += overlapEnd - overlapStart
			} else {
				coveredBytes += writtenThrough - overlapStart
			}
		}
		var frac float64
		if tileSize > 0 {
			frac = float64(coveredBytes) / float64(tileSize)
			if frac > 1 {
				frac = 1
			}
		}
		covered[b] = frac
	}

	m.raster.Refresh()
}

func (m *chunkMosaic) updateMinSizeLocked() {
	rows := (m.blocks + chunkMosaicColumns - 1) / chunkMosaicColumns
	if rows <= 0 {
		rows = 1
	}
	height := rows * tileSize
	m.raster.SetMinSize(fyne.NewSize(560, float32(height)))
}

// generate paints all tiles into one bitmap. This runs on Fyne's draw thread,
// so it only reads the palette snapshot prepared on the event thread.
func (m *chunkMosaic) generate(w, h int) image.Image {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))

	m.mu.Lock()
	covered := append([]float64(nil), m.covered...)
	palette := m.palette
	m.mu.Unlock()

	fillNRGBA(img, img.Bounds(), palette.empty)
	if len(covered) == 0 || w <= 0 || h <= 0 {
		return img
	}

	scale := float64(w) / 560
	if scale < 1 {
		scale = 1
	}
	cell := int(math.Round(tileSize * scale))
	if cell < 2 {
		cell = 2
	}
	gap := int(math.Round(scale))
	if gap < 1 {
		gap = 1
	}
	tile := cell - gap

	for i, frac := range covered {
		col := i % chunkMosaicColumns
		row := i / chunkMosaicColumns
		x := col * cell
		y := row * cell
		if x+tile > w || y+tile > h {
			break
		}
		fill := palette.empty
		switch {
		case frac >= 1:
			fill = palette.success
		case frac > 0:
			fill = palette.primary
		}
		fillNRGBA(img, image.Rect(x, y, x+tile, y+tile), fill)
	}
	return img
}

func fillNRGBA(img *image.NRGBA, rect image.Rectangle, c color.NRGBA) {
	rect = rect.Intersect(img.Bounds())
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		row := img.Pix[y*img.Stride:]
		for x := rect.Min.X; x < rect.Max.X; x++ {
			off := x * 4
			row[off] = c.R
			row[off+1] = c.G
			row[off+2] = c.B
			row[off+3] = c.A
		}
	}
}

func currentChunkMosaicPalette() chunkMosaicPalette {
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	th := fyne.CurrentApp().Settings().Theme()
	return chunkMosaicPalette{
		empty:   color.NRGBAModel.Convert(th.Color(theme.ColorNameInputBackground, variant)).(color.NRGBA),
		primary: color.NRGBAModel.Convert(th.Color(theme.ColorNamePrimary, variant)).(color.NRGBA),
		success: color.NRGBAModel.Convert(th.Color(theme.ColorNameSuccess, variant)).(color.NRGBA),
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// uaForTask 返回应用于该任务下载的 User-Agent 字符串。
func uaForTask(t *api.Task) string {
	if t.AuthData == "" {
		return ilocale.T("details.ua.default")
	}
	var a struct {
		UserAgent string `json:"UserAgent"`
	}
	if err := json.Unmarshal([]byte(t.AuthData), &a); err == nil && a.UserAgent != "" {
		return ilocale.TF("details.ua.used", "UA: "+a.UserAgent, map[string]any{"UA": a.UserAgent})
	}
	return ilocale.T("details.ua.default")
}

// taskName 返回任务保存路径中的文件名部分。
func taskName(t *api.Task) string {
	return filepath.Base(t.SavePath)
}
