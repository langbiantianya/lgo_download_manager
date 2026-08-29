package ui

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// blockSize is the fixed visual chunk size used by the per-chunk progress
// bars (1 MiB).
const blockSize int64 = 1 << 20

// maxBars caps the number of per-chunk bars so we don't render thousands
// of widgets for very large files. 512 ≈ 512 MiB visible at the natural
// scale; larger files collapse multiple chunks per bar.
const maxBars = 512

// chunkMosaic renders one progress bar per byte-range "block" of the file.
// Completed ranges show as fully filled bars; partial ranges show as
// partially filled. Each bar is a real Fyne widget so SetValue always
// triggers a visible repaint (canvas.Rectangle.Refresh() inside a
// GridWrap can be silently dropped, see fyne-io/fyne#3216).
type chunkMosaic struct {
	widget.BaseWidget
	bars     []*widget.ProgressBar
	rows     *fyne.Container
	taskID   string
	total    int64
	blockSz  int64
	blocks   int
	lastVals []float64
}

// showChunkDetails opens a window showing per-block progress for a task.
// Each block is a real Fyne widget.ProgressBar so live updates always
// trigger a real repaint.
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
	content := container.NewVBox(header, mosaic.rows)
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

// newChunkMosaic creates an empty mosaic; resizeForTotal allocates bars
// once we know the file size.
func newChunkMosaic(taskID string) *chunkMosaic {
	m := &chunkMosaic{
		taskID: taskID,
		rows:   container.NewVBox(),
	}
	m.ExtendBaseWidget(m)
	return m
}

// resizeForTotal reallocates the per-bar layout based on the file size.
// Each bar represents blockSize bytes of the file (or more for very
// large files so we stay within maxBars widgets).
func (m *chunkMosaic) resizeForTotal(total int64) {
	if total <= 0 {
		m.total = 0
		m.blocks = 0
		m.blockSz = 0
		m.bars = nil
		m.lastVals = nil
		m.rows.RemoveAll()
		m.Refresh()
		return
	}
	blocks := int(total / blockSize)
	if total%blockSize != 0 {
		blocks++
	}
	if blocks > maxBars {
		blocks = maxBars
		m.blockSz = total / int64(maxBars)
		if total%int64(maxBars) != 0 {
			m.blockSz++
		}
	} else {
		m.blockSz = blockSize
	}
	m.total = total
	m.blocks = blocks
	m.bars = make([]*widget.ProgressBar, blocks)
	m.lastVals = make([]float64, blocks)
	m.rows.RemoveAll()
	for i := range m.bars {
		bar := widget.NewProgressBar()
		bar.TextFormatter = func() string { return "" }
		bar.Resize(fyne.NewSize(0, 14))
		m.bars[i] = bar
		m.rows.Add(bar)
	}
	m.Refresh()
}

// update repaints each bar based on the engine's actual chunk layout
// (chunkRanges, [start0,end0,start1,end1,...]) and per-chunk progress
// (chunkProgress, offset-from-start for each chunk). When chunkRanges
// is empty or mismatched, falls back to per-thread even-split.
//
// Each bar fills [0..1] based on how much of its byte range is fully
// downloaded. Done if the entire range is covered.
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
			for i, bar := range m.bars {
				if m.lastVals[i] != 0 {
					bar.SetValue(0)
					m.lastVals[i] = 0
				}
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

	// For each visual block, compute (a) which chunk(s) overlap it and
	// (b) what fraction of [tileStart,tileEnd) is covered by completed
	// chunk bytes. Done if the entire range is covered.
	for b := 0; b < m.blocks; b++ {
		tileStart := int64(b) * m.blockSz
		tileEnd := tileStart + m.blockSz
		if tileEnd > totalSize {
			tileEnd = totalSize
		}
		var covered int64
		tileSize := tileEnd - tileStart
		for i := 0; i+1 < len(ranges); i += 2 {
			cStart := ranges[i]
			cEnd := ranges[i+1] + 1 // inclusive end → half-open
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
				covered += overlapEnd - overlapStart
			} else {
				covered += writtenThrough - overlapStart
			}
		}
		var frac float64
		if tileSize > 0 {
			frac = float64(covered) / float64(tileSize)
			if frac > 1 {
				frac = 1
			}
		}
		// Always SetValue — Fyne's ProgressBar.SetValue always calls
		// Refresh internally, so it forces a redraw regardless of
		// whether the value changed.
		m.bars[b].SetValue(frac)
		m.lastVals[b] = frac
	}
}

// CreateRenderer returns a minimal renderer. The bars live in m.rows and
// render themselves; this widget just needs a renderer to satisfy
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