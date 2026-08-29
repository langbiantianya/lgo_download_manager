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

// blockSize is the fixed visual chunk size used by the mosaic (1 MiB).
const blockSize int64 = 1 << 20

// maxTiles caps the number of tiles in the mosaic so we don't render
// thousands of widgets for very large files. 1024 ≈ 1 GiB visible at
// the natural scale; larger files collapse multiple MiB per tile.
const maxTiles = 1024

// tileSize is the side length of each mosaic tile in points. Small
// squares wrap into a tiled wall.
const tileSize = 12

// chunkMosaic renders the file as a wall of small square tiles. Each
// tile represents blockSize bytes (or more for very large files so we
// stay within maxTiles widgets). Completed ranges turn the tile green;
// partial ranges use the theme's primary color; empty ranges use the
// input background.
//
// Tiles are real canvas.Rectangle widgets inside a GridWrap. We rebuild
// the wrap on every update so each tile is freshly created — this
// sidesteps fyne-io/fyne#3216 where Refresh() on individual children of
// a GridWrap can be silently dropped.
type chunkMosaic struct {
	widget.BaseWidget
	wrap    *fyne.Container
	taskID  string
	total   int64
	blockSz int64
	blocks  int
}

// showChunkDetails opens a window showing per-block progress for a task.
// Each block is rendered as a tile in a wall — see chunkMosaic.
func showChunkDetails(t *store.Task, sc *scheduler.Scheduler, parent fyne.Window) {
	titleStr := fmt.Sprintf("任务详情: %s", taskName(t))

	urlLabel := widget.NewLabel("URL: " + t.URL)
	urlLabel.Wrapping = fyne.TextWrapWord
	protoLabel := widget.NewLabel(fmt.Sprintf("协议: %s", t.Protocol))
	pathLabel := widget.NewLabel("路径: " + t.SavePath)
	sizeLabel := widget.NewLabel(fmt.Sprintf("总计: %s", formatBytes(t.TotalSize)))
	uaLabel := widget.NewLabel(uaForTask(t))

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

// newChunkMosaic creates an empty mosaic; resizeForTotal allocates the
// tile grid once we know the file size.
func newChunkMosaic(taskID string) *chunkMosaic {
	m := &chunkMosaic{
		taskID: taskID,
		wrap:   container.NewGridWrap(fyne.NewSize(tileSize, tileSize)),
	}
	m.ExtendBaseWidget(m)
	return m
}

// resizeForTotal rebuilds the wrap with the right number of tiles for
// the file size. Each tile is blockSize bytes (or more for very large
// files so we stay within maxTiles).
func (m *chunkMosaic) resizeForTotal(total int64) {
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

// update rebuilds the wrap with freshly-painted tiles for the given
// chunk layout and progress. Same algorithm as before: walk each chunk's
// range, decide which fraction of each tile's byte range is covered.
//
// chunkProgress is per-chunk offset-from-start; chunkRanges is paired
// [start0,end0,start1,end1,...] with inclusive end.
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
		// Fallback: assume N even chunks based on progress-array length.
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

// makeTiles creates a fresh slice of canvas.Rectangle children — one
// per tile — using the per-tile covered fraction to choose colour.
// Rebuilding rather than mutating sidesteps fyne-io/fyne#3216.
func (m *chunkMosaic) makeTiles(covered []float64) []fyne.CanvasObject {
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

// tileColor picks a color based on the tile's covered fraction. Fully
// done tiles use the success (green) color; partial tiles use the
// theme primary; empty tiles use the input background.
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

// CreateRenderer returns a minimal renderer. The tiles live in m.wrap
// and render themselves; this widget just needs a renderer to satisfy
// fyne.Widget.
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