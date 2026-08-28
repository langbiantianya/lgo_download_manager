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
	tileSize float32
	taskID   string
	sc       *scheduler.Scheduler
	total    int64
	blocks   int // current number of tiles
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
	mosaic.update(t.ChunkProgress, t.TotalSize)

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
				mosaic.update(ev.Task.ChunkProgress, ev.Task.TotalSize)
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
	}
	m.ExtendBaseWidget(m)
	return m
}

// resizeForTotal reallocates the tile grid based on the file size. Each
// tile represents blockSize bytes (or more for very large files so we
// stay within maxBlocks tiles).
func (m *chunkMosaic) resizeForTotal(total int64) {
	if total <= 0 {
		m.blocks = 0
		m.blockSz = 0
		m.tiles = nil
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
	for i := range m.tiles {
		r := canvas.NewRectangle(theme.Color(theme.ColorNameBackground))
		r.StrokeColor = theme.Color(theme.ColorNameForeground)
		r.StrokeWidth = 1.5
		m.tiles[i] = r
	}
	m.Refresh()
}

// update repaints each tile. chunkProgress is per-thread offset-from-start;
// the engine writes contiguous byte ranges, so block completion =
// (thread_idx * threadSize) + thread_progress within a thread.
func (m *chunkMosaic) update(chunkProgress []int64, totalSize int64) {
	if totalSize != m.total {
		m.total = totalSize
		m.resizeForTotal(totalSize)
	}
	if totalSize <= 0 || m.blocks == 0 {
		return
	}
	// Derive thread ranges from a fresh chunk plan mirroring engine.plan().
	threads := len(chunkProgress)
	if threads == 0 {
		threads = 4
	}
	base := totalSize / int64(threads)
	rem := totalSize % int64(threads)
	for b := 0; b < m.blocks; b++ {
		// Absolute byte range this tile covers.
		tileStart := int64(b) * m.blockSz
		tileEnd := tileStart + m.blockSz
		if tileEnd > totalSize {
			tileEnd = totalSize
		}
		// Find which thread owns this byte range and how much of it is downloaded.
		done := false
		cur := int64(0)
		for ti := 0; ti < threads; ti++ {
			tSize := base
			if ti == threads-1 {
				tSize += rem
			}
			tEnd := cur + tSize
			if tileStart >= cur && tileStart < tEnd {
				var prog int64
				if ti < len(chunkProgress) {
					prog = chunkProgress[ti]
				}
				absoluteDownloaded := cur + prog
				if absoluteDownloaded >= tileEnd {
					done = true
				}
				break
			}
			cur = tEnd
		}
		if done {
			m.tiles[b].FillColor = theme.Color(theme.ColorNameSuccess)
		} else {
			m.tiles[b].FillColor = theme.Color(theme.ColorNameBackground)
		}
		m.tiles[b].Refresh()
	}
}

// CreateRenderer lays the tiles out as a flex-wrap grid.
func (m *chunkMosaic) CreateRenderer() fyne.WidgetRenderer {
	grid := container.NewGridWithColumns(32)
	for _, t := range m.tiles {
		grid.Add(container.NewGridWrap(fyne.NewSize(m.tileSize, m.tileSize), t))
	}
	return widget.NewSimpleRenderer(grid)
}

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