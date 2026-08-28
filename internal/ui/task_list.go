package ui

import (
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// taskList is the right-hand panel showing the download queue.
type taskList struct {
	sc      *scheduler.Scheduler
	filter  binding.String
	searchQ binding.String

	// fyne widgets
	list       *widget.List
	panel      *fyne.Container
	headerRow  *fyne.Container
	emptyLabel *widget.Label

	// rowMap maps taskID -> live taskRow for O(1) event dispatch.
	rowMu  sync.Mutex
	rowMap map[string]*taskRow
}

func newTaskList(sc *scheduler.Scheduler, filter binding.String) *taskList {
	tl := &taskList{
		sc:      sc,
		filter:  filter,
		searchQ: binding.NewString(),
		rowMap:  map[string]*taskRow{},
	}
	tl.panel = tl.build()
	return tl
}

func (tl *taskList) container() *fyne.Container { return tl.panel }

func (tl *taskList) allTasks() []*store.Task {
	tks, _ := tl.sc.List()
	return tks
}

func (tl *taskList) filtered() []*store.Task {
	fv, _ := tl.filter.Get()
	sv, _ := tl.searchQ.Get()
	var out []*store.Task
	for _, t := range tl.allTasks() {
		switch fv {
		case "downloading":
			if t.Status != store.StatusDownloading {
				continue
			}
		case "paused":
			if t.Status != store.StatusPaused {
				continue
			}
		case "completed":
			if t.Status != store.StatusCompleted {
				continue
			}
		case "failed":
			if t.Status != store.StatusFailed {
				continue
			}
		}
		if sv != "" {
			needle := strings.ToLower(sv)
			if !strings.Contains(strings.ToLower(t.URL), needle) &&
				!strings.Contains(strings.ToLower(t.SavePath), needle) {
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

// build assembles the task list panel: a header row + the scrolling list.
func (tl *taskList) build() *fyne.Container {
	tl.list = widget.NewList(
		func() int { return len(tl.filtered()) },
		func() fyne.CanvasObject { return newTaskRow(nil) },
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			tasks := tl.filtered()
			if int(id) >= len(tasks) {
				return
			}
			row := obj.(*taskRow)
			row.bind(tasks[int(id)], tl.sc)
			tl.bindRow(row, tasks[int(id)].ID)
		},
	)
	tl.list.OnSelected = func(id widget.ListItemID) { tl.list.Unselect(id) }

	tl.emptyLabel = widget.NewLabel("暂无任务，点击「新建任务」开始")
	tl.emptyLabel.Alignment = fyne.TextAlignCenter
	tl.emptyLabel.Importance = widget.LowImportance
	tl.headerRow = tl.buildHeader()

	listWithHeader := container.NewBorder(tl.headerRow, nil, nil, nil, tl.list)
	emptyLabel := container.NewCenter(container.NewMax(tl.emptyLabel))
	content := container.NewStack(listWithHeader, emptyLabel)
	tl.refreshEmptyState()
	return content
}

// buildHeader renders the column labels above the list rows.
func (tl *taskList) buildHeader() *fyne.Container {
	mkHdr := func(text string, w float32) fyne.CanvasObject {
		l := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		return container.NewGridWrap(fyne.NewSize(w, 28), l)
	}
	row := container.NewHBox(
		mkHdr("任务", 320),
		widget.NewLabelWithStyle("大小", fyne.TextAlignTrailing, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("状态", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
	)
	return container.NewVBox(
		row,
		widget.NewSeparator(),
	)
}

// bindRow registers row in rowMap under taskID.
func (tl *taskList) bindRow(row *taskRow, taskID string) {
	tl.rowMu.Lock()
	tl.rowMap[taskID] = row
	tl.rowMu.Unlock()
}

func (tl *taskList) setSearch(s string) {
	_ = tl.searchQ.Set(s)
	tl.refresh()
}

func (tl *taskList) refresh() {
	tl.list.Refresh()
	tl.refreshEmptyState()
}

func (tl *taskList) refreshEmptyState() {
	if len(tl.filtered()) == 0 {
		tl.emptyLabel.Show()
	} else {
		tl.emptyLabel.Hide()
	}
}

// onEvent refreshes the list and dispatches progress events to the matching row.
func (tl *taskList) onEvent(ev scheduler.Event) {
	if ev.Task != nil {
		tl.rowMu.Lock()
		row, ok := tl.rowMap[ev.Task.ID]
		tl.rowMu.Unlock()
		if ok && ev.Why == "progress" {
			row.onProgress(ev)
			return
		}
	}
	tl.refresh()
}
