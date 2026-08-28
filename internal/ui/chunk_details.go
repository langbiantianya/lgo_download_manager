package ui

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// blockSize is the fixed visual chunk size used by the mosaic (1 MiB).
const blockSize int64 = 1 << 20

// maxBlocks caps the mosaic for very large files so we don't render
// thousands of tiles. 512 tiles ≈ 512 MiB visible at the natural scale.
const maxBlocks = 512

// chunkMosaic is a bordered grid view of fixed-size file blocks. Each tile
// represents blockSize bytes of the file (or more for very large files).
type chunkMosaic struct {
	widget.BaseWidget
	tiles    []*canvas.Rectangle
	grid     *fyne.Container
	tileSize float32
	taskID   string
	sc       *scheduler.Scheduler
	total    int64
	blocks   int
	blockSz  int64
}

// showChunkDetails opens a window showing per-block progress for a task as
// a bordered mosaic. Completed tiles turn green. Live updates via the
// scheduler's event bus.
func showChunkDetails(t *store.Task, sc *scheduler.Scheduler, parent fyne.Window) {
	titleStr := fmt.Sprintf("任务详情: %s", taskName(t))

	urlLabel := widget.NewLabel("URL: " + t.URL)
	urlLabel.Wrapping = fyne.TextWrapWord

	protoLabel := widget.NewLabel(fmt.Sprintf("协议: %s", t.Protocol))
	pathLabel := widget.NewLabel("路径: " + t.SavePath)
	sizeLabel := widget.NewLabel(fmt.Sprintf("总计: %s", formatBytes(t.TotalSize)))
	uaLabel := widget.NewLabel(uaForTask(t))

	mosaic := newChunkMosaic(t.ID, sc)
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
		widget.NewSeparator(),
		widget.NewLabel("文件分块详情"),
	)
	content := container.NewVBox(header, mosaic)
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

// newChunkMosaic creates an empty mosaic; resizeForTotal sets the actual
// tile count once we know the file size.
func newChunkMosaic(taskID string, sc *scheduler.Scheduler) *chunkMosaic {
	m := &chunkMosaic{
		tiles:    nil,
		tileSize: 18,
		taskID:   taskID,
		sc:       sc,
		grid:     container.NewGridWithColumns(32),
	}
	m.ExtendBaseWidget(m)
	return m
}

// resizeForTotal reallocates the tile grid based on the file size.
func (m *chunkMosaic) resizeForTotal(total int64) {
	if total <= 0 {
		m.blocks = 0
		m.blockSz = 0
		m.tiles = nil
		m.grid = container.NewGridWithColumns(32)
		m.Refresh()
		return
	}
	blocks := int(total / blockSize)
	if total%blockSize != 0 {
		blocks++
	}
	if blocks > maxBlocks {
		blocks = maxBlocks
		m.blockSz = total / int64(maxBlocks)
		if total%int64(maxBlocks) != 0 {
			m.blockSz++
		}
	} else {
		m.blockSz = blockSize
	}
	m.blocks = blocks
	m.tiles = make([]*canvas.Rectangle, blocks)
	m.grid = container.NewGridWithColumns(32)
	for i := range m.tiles {
		r := canvas.NewRectangle(theme.Color(theme.ColorNameBackground))
		r.StrokeColor = theme.Color(theme.ColorNameForeground)
		r.StrokeWidth = 1.5
		m.tiles[i] = r
		m.grid.Add(container.NewGridWrap(fyne.NewSize(m.tileSize, m.tileSize), r))
	}
	m.Refresh()
}

// update repaints each tile based on the engine's actual chunk layout
// (chunkRanges, [start0,end0,start1,end1,...]) and per-chunk progress
// (chunkProgress, offset-from-start for each chunk). When chunkRanges
// is empty or mismatched (e.g. a task created before the feature,
// or a row that hasn't been updated since streaming collapsed the
// plan), falls back to a per-thread even-split so the window still
// renders something useful.
func (m *chunkMosaic) update(chunkProgress, chunkRanges []int64, totalSize int64) {
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
		threads := len(progress)
		if threads == 0 {
			for i := range m.tiles {
				m.tiles[i].FillColor = theme.Color(theme.ColorNameBackground)
				m.tiles[i].Refresh()
			}
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

	for b := 0; b < m.blocks; b++ {
		tileStart := int64(b) * m.blockSz
		tileEnd := tileStart + m.blockSz
		if tileEnd > totalSize {
			tileEnd = totalSize
		}
		allDone := true
		matched := false
		for i := 0; i+1 < len(ranges); i += 2 {
			cStart := ranges[i]
			cEnd := ranges[i+1] + 1 // inclusive end → half-open
			if cEnd <= tileStart || cStart >= tileEnd {
				continue
			}
			matched = true
			overlapEnd := min64(cEnd, tileEnd)
			var prog int64
			if i/2 < len(progress) {
				prog = progress[i/2]
			}
			writtenThrough := cStart + prog
			if writtenThrough < overlapEnd {
				allDone = false
				break
			}
		}
		if matched && allDone {
			m.tiles[b].FillColor = theme.Color(theme.ColorNameSuccess)
		} else {
			m.tiles[b].FillColor = theme.Color(theme.ColorNameBackground)
		}
		m.tiles[b].Refresh()
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// CreateRenderer returns a renderer that always reflects m.grid so the
// tile grid can be rebuilt by resizeForTotal.
func (m *chunkMosaic) CreateRenderer() fyne.WidgetRenderer {
	return &mosaicRenderer{r: widget.NewSimpleRenderer(m.grid)}
}

type mosaicRenderer struct {
	r fyne.WidgetRenderer
}

func (r *mosaicRenderer) Destroy() { r.r.Destroy() }
func (r *mosaicRenderer) Layout(s fyne.Size) { r.r.Layout(s) }
func (r *mosaicRenderer) MinSize() fyne.Size { return r.r.MinSize() }
func (r *mosaicRenderer) Objects() []fyne.CanvasObject { return r.r.Objects() }
func (r *mosaicRenderer) Refresh()       { r.r.Refresh() }

// uaForTask returns the User-Agent string applied to the task's downloads.
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

// taskName returns the file name portion of a task's save path.
func taskName(t *store.Task) string {
	return filepath.Base(t.SavePath)
}