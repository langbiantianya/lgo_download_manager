// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// taskList 是右侧显示下载队列的面板。
type taskList struct {
	svc     Service
	filter  binding.String
	searchQ binding.String

	// fyne 控件
	list       *widget.List
	panel      *fyne.Container
	headerRow  *fyne.Container
	emptyLabel *widget.Label

	// rowMap 将 taskID 映射到活动的 taskRow，以便 O(1) 地派发事件。
	rowMu  sync.Mutex
	rowMap map[string]*taskRow

	// cacheMu 保护过滤结果缓存。Length()/UpdateItem()/空状态判断都会
	// 查询过滤结果，没有缓存时每个事件、每一个可见行都要走一次
	// svc.List（阻塞 IPC，最长 30s 超时）。
	cacheMu    sync.Mutex
	cache      []*store.Task
	cacheKey   string
	cacheValid bool
}

func newTaskList(svc Service, filter binding.String) *taskList {
	tl := &taskList{
		svc:     svc,
		filter:  filter,
		searchQ: binding.NewString(),
		rowMap:  map[string]*taskRow{},
	}
	tl.panel = tl.build()
	return tl
}

func (tl *taskList) container() *fyne.Container { return tl.panel }

// allTasks 直接向后端取一次当前状态筛选下的全部任务。
// 用于「暂停全部/恢复全部」这类一次性用户操作——它们要的是后端权威快照，
// 且不受搜索框影响，因此不走 filtered() 的缓存。
func (tl *taskList) allTasks() []*store.Task {
	fv, _ := tl.filter.Get()
	tks, _ := tl.svc.List(store.StatusFilter(fv), GlobalSettings.TaskSort)
	return tks
}

// filterKey 返回过滤条件的组合键：状态筛选 + 排序 + 搜索文本。
// 三者任一变化都必须重新向后端取数。
func (tl *taskList) filterKey() string {
	fv, _ := tl.filter.Get()
	sv, _ := tl.searchQ.Get()
	return string(fv) + "\x00" + string(GlobalSettings.TaskSort) + "\x00" + sv
}

// filtered 返回当前过滤条件下的任务快照。
//
// 结果被缓存：状态过滤由业务侧（SQL）完成，搜索框匹配 URL 或保存路径，
// 在内存里完成。只有过滤条件本身变化，或集合发生变化（added/deleted、
// 状态筛选下的归属变化）时才会重新调用 svc.List。
func (tl *taskList) filtered() []*store.Task {
	key := tl.filterKey()
	tl.cacheMu.Lock()
	if tl.cacheValid && tl.cacheKey == key {
		tasks := tl.cache
		tl.cacheMu.Unlock()
		return tasks
	}
	tl.cacheMu.Unlock()

	// 取数在锁外进行：svc.List 是阻塞 IPC，不应阻塞事件派发路径。
	fv, _ := tl.filter.Get()
	tasks, _ := tl.svc.List(store.StatusFilter(fv), GlobalSettings.TaskSort)
	sv, _ := tl.searchQ.Get()
	tasks = filterBySearch(tasks, strings.ToLower(sv))

	tl.cacheMu.Lock()
	tl.cache, tl.cacheKey, tl.cacheValid = tasks, key, true
	tl.cacheMu.Unlock()
	return tasks
}

// filterBySearch 返回匹配搜索关键字（URL 或保存路径，大小写不敏感）的任务。
// needle 必须已小写化；空关键字匹配所有任务。
func filterBySearch(tasks []*store.Task, needle string) []*store.Task {
	if needle == "" {
		return tasks
	}
	var out []*store.Task
	for _, t := range tasks {
		if matchesSearch(t, needle) {
			out = append(out, t)
		}
	}
	return out
}

// matchesSearch 报告任务是否匹配已小写的搜索关键字。
func matchesSearch(t *store.Task, needle string) bool {
	return strings.Contains(strings.ToLower(t.URL), needle) ||
		strings.Contains(strings.ToLower(t.SavePath), needle)
}

// invalidate 标记过滤缓存失效：下一次 filtered() 重新向后端取数。
func (tl *taskList) invalidate() {
	tl.cacheMu.Lock()
	tl.cacheValid = false
	tl.cacheMu.Unlock()
}

// patch 用事件携带的任务快照就地更新缓存，避免为一次变化重跑整表查询。
// 返回 true 表示缓存的归属/顺序需要后端重新确认（调用方应 invalidate）。
func (tl *taskList) patch(t *store.Task) bool {
	sv, _ := tl.searchQ.Get()
	needle := strings.ToLower(sv)
	fv, _ := tl.filter.Get()
	statusFiltered := fv != "" && fv != string(store.FilterAll)

	tl.cacheMu.Lock()
	defer tl.cacheMu.Unlock()
	if !tl.cacheValid {
		// 还没有缓存，下一次 filtered() 会整体取数。
		return false
	}
	for i, cur := range tl.cache {
		if cur == nil || cur.ID != t.ID {
			continue
		}
		// 状态筛选下，状态变化可能让任务进出集合，且它在结果中的排序位置
		// 由后端 SQL 决定——UI 不在这里复制那份映射，交给 filtered() 重查。
		if statusFiltered && cur.Status != t.Status {
			return true
		}
		if needle != "" && !matchesSearch(t, needle) {
			// 搜索条件不再匹配：就地移除即可，无需重查。
			tl.cache = append(tl.cache[:i], tl.cache[i+1:]...)
			return false
		}
		tl.cache[i] = t
		return false
	}
	// 不在缓存中：若它应当出现，其在排序中的位置只能由后端给出。
	return !statusFiltered && (needle == "" || matchesSearch(t, needle))
}

// build 组装任务列表面板：表头行加可滚动列表。
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
			row.bind(tasks[int(id)], tl.svc)
			tl.bindRow(row, tasks[int(id)].ID)
		},
	)
	tl.list.OnSelected = func(id widget.ListItemID) { tl.list.Unselect(id) }

	tl.emptyLabel = widget.NewLabel("")
	tl.emptyLabel.Alignment = fyne.TextAlignCenter
	tl.emptyLabel.Importance = widget.LowImportance
	tl.emptyLabel.SetText(ilocale.T("taskList.empty"))
	tl.headerRow = tl.buildHeader()

	listWithHeader := container.NewBorder(tl.headerRow, nil, nil, nil, tl.list)
	emptyLabel := container.NewCenter(container.NewMax(tl.emptyLabel))
	content := container.NewStack(listWithHeader, emptyLabel)
	tl.refreshEmptyState()
	return content
}

// applyLanguage 在语言切换时刷新 taskList 的可变字符串:表头三列、
// 空状态标签。taskRow 自身的状态/速度/ETA 等翻译发生在 row 内的
// refresh 路径,这里通过 resetRows 让所有可见 row 强制重渲。
//
// 表头 widget 被重建(原来的 headerRow 还在,但里面的 label 不再指向
// applyLanguage 创建的引用),所以先保存然后整体替换内容。
func (tl *taskList) applyLanguage() {
	if tl.emptyLabel != nil {
		tl.emptyLabel.SetText(ilocale.T("taskList.empty"))
	}
	if tl.headerRow != nil {
		tl.headerRow.Objects = []fyne.CanvasObject{tl.buildHeaderRow()}
		tl.headerRow.Refresh()
	}
	// 行内部文案(row 显示出来的任务名/添加时间/状态/速度/ETA)在 bind 或
	// refresh 时才写入;applyLanguage 把 rowState 标脏,强制下一轮
	// refresh 走完整路径覆盖所有翻译字段。
	tl.rowMu.Lock()
	for _, row := range tl.rowMap {
		row.st.valid = false
	}
	tl.rowMu.Unlock()
	tl.refresh()
}

// buildHeader 在列表行上方渲染列标题。
func (tl *taskList) buildHeader() *fyne.Container {
	return container.NewVBox(
		tl.buildHeaderRow(),
		widget.NewSeparator(),
	)
}

// buildHeaderRow 渲染表头的标题行(不含底部分隔线),供 buildHeader 与
// applyLanguage 共用——后者在重建时只换这一行,保留外层 VBox 不变。
func (tl *taskList) buildHeaderRow() fyne.CanvasObject {
	mkHdr := func(text string, w float32) fyne.CanvasObject {
		l := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		return container.NewGridWrap(fyne.NewSize(w, 28), l)
	}
	name := mkHdr(ilocale.T("taskList.header.name"), 320)
	size := widget.NewLabelWithStyle(ilocale.T("taskList.header.size"), fyne.TextAlignTrailing, fyne.TextStyle{Bold: true})
	status := widget.NewLabelWithStyle(ilocale.T("taskList.header.status"), fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	row := container.NewBorder(nil, nil, nil, container.NewHBox(size, status), name)
	return row
}

// bindRow 在 rowMap 中以 taskID 注册 row。
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

// onEvent 把事件反应到列表与对应的行上。
//
// 事件已经携带任务快照，因此非集合类事件只需就地打补丁：progress 事件
// 只更新对应行，状态事件就地替换缓存快照后再刷新可见行，两者都不再触发
// svc.List。只有集合真正变化（added/deleted、状态筛选下的归属变化）才
// 重新向后端取数。
//
// 状态事件到达时，若 row 持有匹配的乐观覆盖，则清掉它，让 refresh 重新按
// 真实 status 渲染（进度事件不清覆盖，以避免乐观闪烁）。
func (tl *taskList) onEvent(ev scheduler.Event) {
	if ev.Task == nil {
		// 集合变化事件（deleted 不带任务快照）：必须重新取数。
		tl.invalidate()
		tl.refresh()
		return
	}

	tl.rowMu.Lock()
	row, ok := tl.rowMap[ev.Task.ID]
	tl.rowMu.Unlock()

	if ev.Why == "progress" {
		// 进度事件只影响该行的进度显示：就地打补丁后直接派发，
		// 不触碰列表本身。
		tl.patch(ev.Task)
		if ok {
			row.onProgress(ev)
		}
		return
	}

	if ok && row.optimisticStatus != nil && *row.optimisticStatus == ev.Task.Status {
		row.optimisticStatus = nil
	}
	if ev.Why == "added" || tl.patch(ev.Task) {
		tl.invalidate()
	}
	tl.refresh()
}

// setOptimistic 给定任务 ID 立刻设置其 row 的乐观状态并刷新。
// 真实事件(status 与乐观值一致)由 onEvent 清掉,这里只负责「立即看见」。
func (tl *taskList) setOptimistic(taskID string, st store.Status) {
	tl.rowMu.Lock()
	row, ok := tl.rowMap[taskID]
	tl.rowMu.Unlock()
	if !ok {
		return
	}
	row.optimisticStatus = &st
	row.Refresh()
}
