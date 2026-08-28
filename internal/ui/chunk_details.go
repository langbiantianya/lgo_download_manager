package ui

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// showChunkDetails opens a dialog showing per-thread chunk progress and
// connection info for a task. The dialog has a single 关闭 button.
func showChunkDetails(t *store.Task, sc *scheduler.Scheduler, parent fyne.Window) {
	title := fmt.Sprintf("任务详情: %s", taskName(t))

	urlLabel := widget.NewLabel("URL: " + t.URL)
	urlLabel.Wrapping = fyne.TextWrapWord

	protoLabel := widget.NewLabel(fmt.Sprintf("协议: %s", t.Protocol))
	pathLabel := widget.NewLabel("路径: " + t.SavePath)
	sizeLabel := widget.NewLabel(fmt.Sprintf("总计: %s", formatBytes(t.TotalSize)))
	uaLabel := widget.NewLabel(uaForTask(t))

	infoBox := container.NewVBox(
		widget.NewLabel(title),
		widget.NewSeparator(),
		urlLabel,
		protoLabel,
		pathLabel,
		sizeLabel,
		uaLabel,
		widget.NewSeparator(),
		widget.NewLabel("分块进度实时监控"),
	)

	chunkCount := t.ChunkCount
	if chunkCount <= 0 {
		chunkCount = 4
	}

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
		infoBox.Add(row)
	}

	scroll := container.NewScroll(infoBox)
	scroll.SetMinSize(fyne.NewSize(560, 360))

	dialog.ShowCustom("任务详情", "关闭", scroll, parent)
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