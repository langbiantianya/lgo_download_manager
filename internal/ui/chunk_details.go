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

// chunkMosaic is a bordered grid view of per-chunk progress. Tiles turn
// green when a chunk is fully downloaded.
type chunkMosaic struct {
	widget.BaseWidget
	tiles    []*canvas.Rectangle
	tileSize float32
	taskID   string
	sc       *scheduler.Scheduler
}

// showChunkDetails opens a window showing per-chunk progress for a task as
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

	chunkCount := t.ChunkCount
	if chunkCount <= 0 {
		chunkCount = 4
	}

	mosaic := newChunkMosaic(t.ID, sc, chunkCount)
	mosaic.update(t.ChunkProgress, t.TotalSize, chunkCount)

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
				mosaic.update(ev.Task.ChunkProgress, ev.Task.TotalSize, chunkCount)
			})
		}
	}()
	w.SetOnClosed(func() { unsub() })
}

// newChunkMosaic creates a grid of bordered tiles sized by chunkCount.
func newChunkMosaic(taskID string, sc *scheduler.Scheduler, chunkCount int) *chunkMosaic {
	m := &chunkMosaic{
		tiles:    make([]*canvas.Rectangle, chunkCount),
		tileSize: 24,
		taskID:   taskID,
		sc:       sc,
	}
	for i := range m.tiles {
		r := canvas.NewRectangle(theme.Color(theme.ColorNameBackground))
		r.StrokeColor = theme.Color(theme.ColorNameForeground)
		r.StrokeWidth = 1.5
		m.tiles[i] = r
	}
	m.ExtendBaseWidget(m)
	return m
}

// update repaints each tile based on chunkProgress / totalSize.
func (m *chunkMosaic) update(chunkProgress []int64, totalSize int64, chunkCount int) {
	if totalSize <= 0 || chunkCount <= 0 {
		return
	}
	base := totalSize / int64(chunkCount)
	rem := totalSize % int64(chunkCount)
	for i := range m.tiles {
		if i >= chunkCount {
			break
		}
		chunkSize := base
		if i == chunkCount-1 {
			chunkSize += rem
		}
		var downloaded int64
		if i < len(chunkProgress) {
			downloaded = chunkProgress[i]
		}
		if downloaded >= chunkSize {
			m.tiles[i].FillColor = theme.Color(theme.ColorNameSuccess)
		} else {
			m.tiles[i].FillColor = theme.Color(theme.ColorNameBackground)
		}
		m.tiles[i].Refresh()
	}
}

// CreateRenderer lays the tiles out as a flex-wrap row.
func (m *chunkMosaic) CreateRenderer() fyne.WidgetRenderer {
	grid := container.NewGridWithColumns(16)
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