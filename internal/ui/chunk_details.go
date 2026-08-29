// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"encoding/json"
	"fmt"
	"image/color"
	"path/filepath"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// blockSize 是马赛克中使用的固定可视分块大小（1 MiB）。
const blockSize int64 = 1 << 20

// maxTiles 限制马赛克中的瓦片数量，避免为超大文件渲染成千上万个 widget。
// 1024 ≈ 在默认比例下显示 1 GiB；更大的文件会将多个 MiB 合并到一块瓦片中。
const maxTiles = 1024

// tileSize 是每个马赛克瓦片的边长（以 point 为单位）。
// 小方块会排列成一面瓦片墙。
const tileSize = 12

// chunkMosaic 将文件渲染为一面由小方块组成的瓦片墙。
// 每个瓦片代表 blockSize 字节（对于超大文件可能更多，
// 以保证 widget 数量不超过 maxTiles）。已完成的范围将
// 瓦片染为绿色；部分完成的范围使用主题的主色；空白范围
// 使用输入框背景色。
//
// 瓦片是 GridWrap 内的真实 canvas.Rectangle widget。
// 我们在每次更新时重建 wrap，使每个瓦片都是新创建的——
// 这规避了 fyne-io/fyne#3216：在 GridWrap 子节点上单独
// 调用 Refresh() 可能会被静默丢弃。
type chunkMosaic struct {
	widget.BaseWidget
	wrap    *fyne.Container
	taskID  string
	total   int64
	blockSz int64
	blocks  int
}

// showChunkDetails 打开一个窗口，展示任务按分块的下载进度。
// 每个分块被渲染为瓦片墙中的一块——参见 chunkMosaic。
func showChunkDetails(t *store.Task, sc *scheduler.Scheduler, parent fyne.Window) {
	titleStr := fmt.Sprintf("任务详情: %s", taskName(t))

	urlLabel := widget.NewLabel("URL: " + t.URL)
	urlLabel.Wrapping = fyne.TextWrapWord
	protoLabel := widget.NewLabel(fmt.Sprintf("协议: %s", t.Protocol))
	pathLabel := widget.NewLabel("路径: " + t.SavePath)
	sizeLabel := widget.NewLabel(fmt.Sprintf("总计: %s", formatBytes(t.TotalSize)))
	uaLabel := widget.NewLabel(uaForTask(t))
	createdAtLabel := widget.NewLabel("添加时间: " + formatTime(t.CreatedAt))
	completedAtLabel := widget.NewLabel("下载完成时间: " + formatTime(t.CompletedAt))

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
		widget.NewLabel("文件分块详情"),
	)
	content := container.NewVBox(header, mosaic.wrap)
	scroll := container.NewScroll(content)
	scroll.SetMinSize(fyne.NewSize(560, 360))

	w := fyne.CurrentApp().NewWindow(titleStr)
	w.SetContent(scroll)
	w.Resize(fyne.NewSize(640, 460))
	w.Show()

	ch, unsub := sc.Subscribe()
	go func() {
		for ev := range ch {
			if ev.Task == nil || ev.Task.ID != t.ID {
				continue
			}
			fyne.Do(func() {
				mosaic.update(ev.Task.ChunkProgress, ev.Task.ChunkRanges, ev.Task.TotalSize)
			})
		}
	}()
	w.SetOnClosed(func() { unsub() })
}

// newChunkMosaic 创建一个空马赛克；resizeForTotal 会在知道文件大小后
// 分配瓦片网格。
func newChunkMosaic(taskID string) *chunkMosaic {
	m := &chunkMosaic{
		taskID: taskID,
		wrap:   container.NewGridWrap(fyne.NewSize(tileSize, tileSize)),
	}
	m.ExtendBaseWidget(m)
	return m
}

// resizeForTotal 根据文件大小重建 wrap，并使用正确数量的瓦片。
func (m *chunkMosaic) resizeForTotal(total int64) {
	// 每个瓦片代表 blockSize 字节（对于超大文件可能更多，
	// 以保证瓦片总数不超过 maxTiles）。
	if total <= 0 {
		m.total = 0
		m.blocks = 0
		m.blockSz = 0
		m.wrap.Objects = nil
		m.wrap.Refresh()
		m.Refresh()
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
	m.wrap.Objects = m.makeTiles(make([]float64, blocks))
	m.wrap.Refresh()
	m.Refresh()
}

// update 根据给定的分块布局和进度，使用新绘制的瓦片重建 wrap。
func (m *chunkMosaic) update(chunkProgress, chunkRanges []int64, totalSize int64) {
	// 算法与之前相同：遍历每个分块的范围，确定每个瓦片字节范围内
	// 被覆盖的比例。
	//
	// chunkProgress 是每个分块自起始位置的偏移；chunkRanges 是成对的
	// [start0,end0,start1,end1,...]，end 为闭区间。
	if totalSize != m.total {
		m.total = totalSize
		m.resizeForTotal(totalSize)
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
			m.wrap.Objects = m.makeTiles(make([]float64, m.blocks))
			m.wrap.Refresh()
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

	covered := make([]float64, m.blocks)
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

	m.wrap.Objects = m.makeTiles(covered)
	m.wrap.Refresh()
	m.Refresh()
}

// makeTiles 新建一组 canvas.Rectangle 子节点——每个瓦片一个——
func (m *chunkMosaic) makeTiles(covered []float64) []fyne.CanvasObject {
	// 根据每块瓦片的覆盖比例选择颜色。重建而非就地修改可规避
	// fyne-io/fyne#3216。
	objs := make([]fyne.CanvasObject, len(covered))
	th := fyne.CurrentApp().Settings().Theme()
	for i, frac := range covered {
		r := canvas.NewRectangle(tileColor(th, frac))
		r.StrokeColor = th.Color(theme.ColorNameForeground, fyne.CurrentApp().Settings().ThemeVariant())
		r.StrokeWidth = 0.5
		objs[i] = r
	}
	return objs
}

// tileColor 根据瓦片的覆盖比例选择颜色。完全下载完成的瓦片
// 使用 success（绿色）；部分完成的瓦片使用主题的主色；
// 空瓦片使用输入框背景色。
func tileColor(th fyne.Theme, frac float64) color.Color {
	switch {
	case frac >= 1:
		return th.Color(theme.ColorNameSuccess, fyne.CurrentApp().Settings().ThemeVariant())
	case frac > 0:
		return th.Color(theme.ColorNamePrimary, fyne.CurrentApp().Settings().ThemeVariant())
	default:
		return th.Color(theme.ColorNameInputBackground, fyne.CurrentApp().Settings().ThemeVariant())
	}
}

// CreateRenderer 返回一个最小化的 renderer。瓦片由 m.wrap
func (m *chunkMosaic) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewWithoutLayout())
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
func uaForTask(t *store.Task) string {
	if t.AuthData == "" {
		return "UA: Wget/1.21.3 (default)"
	}
	var a struct {
		UserAgent string `json:"UserAgent"`
	}
	if err := json.Unmarshal([]byte(t.AuthData), &a); err == nil && a.UserAgent != "" {
		return "UA: " + a.UserAgent
	}
	return "UA: Wget/1.21.3 (default)"
}

// taskName 返回任务保存路径中的文件名部分。
func taskName(t *store.Task) string {
	return filepath.Base(t.SavePath)
}
