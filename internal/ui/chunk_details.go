package ui

import (
	"fmt"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// showChunkDetails opens a read-only dialog showing per-thread chunk progress
// for a running or paused task.
func showChunkDetails(t *store.Task, sc *scheduler.Scheduler, parent fyne.Window) {
	title := fmt.Sprintf("任务详情: %s", taskName(t))

	protoLabel := widget.NewLabel(fmt.Sprintf("协议: %s", t.Protocol))
	pathLabel := widget.NewLabel(fmt.Sprintf("路径: %s", t.SavePath))
	sizeLabel := widget.NewLabel(fmt.Sprintf("总计: %s", formatBytes(t.TotalSize)))

	chunkCount := t.ChunkCount
	if chunkCount <= 0 {
		chunkCount = 4
	}

	content := container.NewVBox(
		widget.NewLabel(title),
		protoLabel,
		pathLabel,
		sizeLabel,
		widget.NewSeparator(),
		widget.NewLabel("分块进度实时监控"),
	)

	// Build one progress bar per chunk.
	for i := range chunkCount {
		downloaded := int64(0)
		total := t.TotalSize / int64(chunkCount)
		if i == chunkCount-1 {
			total += t.TotalSize % int64(chunkCount)
		}
		if i < len(t.ChunkProgress) {
			downloaded = t.ChunkProgress[i]
		}
		pct := 0.0
		if total > 0 {
			pct = float64(downloaded) / float64(total)
		}
		pb := widget.NewProgressBar()
		pb.SetValue(pct)

		row := container.NewHBox(
			widget.NewLabel(fmt.Sprintf("线程 %d", i+1)),
			widget.NewLabel(fmt.Sprintf("%s / %s", formatBytes(downloaded), formatBytes(total))),
			pb,
		)
		content.Add(row)
	}

	scroll := container.NewScroll(content)
	scroll.SetMinSize(fyne.NewSize(500, 300))

	dialog.ShowCustomWithoutButtons("任务详情", scroll, parent)
}

// taskName returns the file name portion of a task's save path.
func taskName(t *store.Task) string {
	return filepath.Base(t.SavePath)
}
