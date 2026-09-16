// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"sync"
	"testing"

	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/test"

	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// countingService 记录 List 调用次数——列表的过滤结果必须有缓存，
// 否则每个事件、每个可见行都会产生一次阻塞的 List IPC 往返。
type countingService struct {
	mu    sync.Mutex
	calls int
	tasks []*store.Task
}

func (c *countingService) List(store.StatusFilter, store.TaskSort) ([]*store.Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return append([]*store.Task(nil), c.tasks...), nil
}

func (c *countingService) listCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *countingService) Subscribe() (<-chan scheduler.Event, func()) {
	return make(chan scheduler.Event), func() {}
}
func (c *countingService) AddTask(AddTaskInput) (*store.Task, error) { return nil, nil }
func (c *countingService) Start(string) error                        { return nil }
func (c *countingService) Pause(string) error                        { return nil }
func (c *countingService) Delete(string) error                       { return nil }
func (c *countingService) IsPreparing(string) bool                   { return false }
func (c *countingService) Probe(string) (int64, error)               { return 0, nil }
func (c *countingService) Settings() store.Settings                  { return store.Settings{} }
func (c *countingService) SaveSettings(store.Settings) error         { return nil }
func (c *countingService) Close() error                              { return nil }

// TestTaskListEventsDoNotRelist 验证事件派发不会为每个事件重新调用
// svc.List：事件自带任务快照，就地打补丁即可；只有过滤条件变化或
// 集合变化（added/deleted）才重新取数。
func TestTaskListEventsDoNotRelist(t *testing.T) {
	test.NewApp()
	defer test.NewApp()

	dl := &store.Task{
		ID: "t1", URL: "https://example.com/a.bin", SavePath: "/tmp/a.bin",
		Status: store.TaskStatus.Downloading, TotalSize: 100, Downloaded: 10,
	}
	other := &store.Task{
		ID: "t2", URL: "https://example.com/b.bin", SavePath: "/tmp/b.bin",
		Status: store.TaskStatus.Paused,
	}

	svc := &countingService{tasks: []*store.Task{dl, other}}
	tl := newTaskList(svc, binding.NewString())
	_ = tl.container()

	// 首次渲染：空状态判断需要真实取数一次（并建立缓存）。
	tl.refresh()
	base := svc.listCalls()
	if base == 0 {
		t.Fatalf("首次渲染应建立缓存并取数一次")
	}

	// 进度事件：只就地更新对应行。
	tl.onEvent(scheduler.Event{Why: "progress", Task: dl, SpeedBPS: 1024})
	// 状态事件（「全部」筛选下集合不变）：同样只打补丁。
	paused := *dl
	paused.Status = store.TaskStatus.Paused
	tl.onEvent(scheduler.Event{Why: "paused", Task: &paused})
	if got := svc.listCalls(); got != base {
		t.Fatalf("progress/状态事件产生了 %d 次额外的 List,期望 0 次", got-base)
	}

	// 搜索条件变化：必须重新取数一次。
	tl.setSearch("a.bin")
	afterSearch := svc.listCalls()
	if afterSearch != base+1 {
		t.Fatalf("搜索条件变化后 List 调用 = %d 次,期望 %d 次", afterSearch, base+1)
	}

	// 不匹配搜索条件的进度事件：就地丢弃,不重新取数。
	tl.onEvent(scheduler.Event{Why: "progress", Task: other})
	if got := svc.listCalls(); got != afterSearch {
		t.Fatalf("不匹配搜索的进度事件产生了 %d 次额外的 List,期望 0 次", got-afterSearch)
	}

	// 集合变化：added / deleted 必须重新取数。
	tl.onEvent(scheduler.Event{Why: "added", Task: other})
	if got := svc.listCalls(); got != afterSearch+1 {
		t.Fatalf("added 事件后 List 调用 = %d 次,期望 %d 次", got, afterSearch+1)
	}
	tl.onEvent(scheduler.Event{Why: "deleted"})
	if got := svc.listCalls(); got != afterSearch+2 {
		t.Fatalf("deleted 事件后 List 调用 = %d 次,期望 %d 次", got, afterSearch+2)
	}
}
