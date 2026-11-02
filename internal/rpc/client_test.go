// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package rpc

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
)

// fakeTransport 是用于测试共享客户端行为的假传输。
type fakeTransport struct {
	calls   []string
	events  chan api.Message
	failErr error
	closed  bool
}

func (f *fakeTransport) Close() error {
	f.closed = true
	return nil
}

func (f *fakeTransport) Call(ctx context.Context, method string, in, out any) error {
	f.calls = append(f.calls, method)
	if f.failErr != nil {
		return f.failErr
	}
	switch method {
	case api.MethodGetSettings:
		s := out.(*api.Settings)
		s.Language = "en"
	case api.MethodList:
		r := out.(*api.ListResult)
		r.Tasks = []*api.Task{{ID: "t1"}}
	case api.MethodIsPreparing:
		b := out.(*api.BoolResult)
		b.Value = true
	}
	return nil
}

func (f *fakeTransport) Stream(ctx context.Context) (<-chan api.Message, error) {
	return f.events, nil
}

func TestClientList(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := c.List(api.FilterAll, api.SortCreatedDesc)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t1" {
		t.Fatalf("列表结果不对: %+v", tasks)
	}
	if c.Settings().Language != "en" {
		t.Fatalf("握手设置不对: %+v", c.Settings())
	}
	// 握手恰好发一次 get_settings，随后才是 list。
	if len(tr.calls) != 2 || tr.calls[0] != api.MethodGetSettings || tr.calls[1] != api.MethodList {
		t.Fatalf("调用序列不对: %v", tr.calls)
	}
}

func TestClientCloseClosesTransport(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !tr.closed {
		t.Fatal("Close 应当关闭底层传输")
	}
}

func TestClientLateShowAddTaskReplay(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	// 回调还没注册时到达的请求不能丢——业务进程的 lgom:// 转发可能
	// 在 UI 子进程还在启动时就发过来了。
	tr.events <- api.Message{
		Kind:        api.MessageKindShowAddTask,
		ShowAddTask: &api.ShowAddTaskParams{URL: "https://x/a"},
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.hookMu.Lock()
		n := len(c.showAddTaskQ)
		c.hookMu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("消息没有进队列")
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := make(chan api.ShowAddTaskParams, 1)
	c.SetOnShowAddTask(func(p api.ShowAddTaskParams) { got <- p })
	select {
	case p := <-got:
		if p.URL != "https://x/a" {
			t.Fatalf("补发的参数不对: %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("迟到的 show_add_task 没有被补发")
	}
}

// TestClientReadLoopSurvivesCallerCtxTimeout 验证读循环不受调用方 ctx 的
// 超时影响：调用方给的 ctx 到期后，读循环不能跟着退出——否则 UI 会把
// 「一次调用超时」误判成「业务进程已退出」。
func TestClientReadLoopSurvivesCallerCtxTimeout(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	c, err := newClient(ctx, tr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	closed := make(chan struct{}, 1)
	c.SetOnClose(func() { closed <- struct{}{} })

	// 等调用方的 ctx 到期。
	<-ctx.Done()
	select {
	case <-closed:
		t.Fatal("调用方 ctx 到期不应触发 onClose")
	case <-time.After(200 * time.Millisecond):
	}

	// 读循环还活着：仍能收到并分发事件。
	ch, unsub := c.Subscribe()
	defer unsub()
	tr.events <- api.Message{Kind: api.MessageKindEvent, Event: &api.Event{Why: "alive"}}
	select {
	case ev := <-ch:
		if ev.Why != "alive" {
			t.Fatalf("事件不对: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("读循环已经退出，事件没收到")
	}
}

// TestClientShowAddTaskQueueSharesHookLock 断言：show_add_task 队列必须与
// onShowAddTask 钩子共用 hookMu，不能另立一把锁。
//
// 为什么要有这条护栏：队列若另有其锁，dispatch 的「读钩子 → 入队」与
// SetOnShowAddTask 的「设钩子 → 排空」就不在同一个临界区，会出现
// 「读到钩子为空 → 对方注册并排空空队列 → 才入队」的交错——那条请求
// 既没被投递也没被补发，就永远卡在队列里送不出去。
//
// 这条竞态在自然调度下极罕见（实测约百万分之一的派发才会踩中），
// 任何黑盒用例都抓不住它——修复后的实现根本不存在「已入队但尚未投递」
// 这种中间态可供观察。因此这里用结构断言守死：队列一旦另立一把锁，
// 本用例立刻失败。
//
// 另有一道互补的护栏：TestClientLateShowAddTaskReplay 在 hookMu 下读队列，
// 若实现把队列写在别的锁上，`go test -race` 会直接报出数据竞争。
func TestClientShowAddTaskQueueSharesHookLock(t *testing.T) {
	ty := reflect.TypeOf(client{})
	mutexType := reflect.TypeOf(sync.Mutex{})
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		if f.Type != mutexType {
			continue
		}
		// settingsMu / subMu 各自守自己的字段，与本队列无关。
		if f.Name == "settingsMu" || f.Name == "subMu" || f.Name == "hookMu" {
			continue
		}
		if strings.Contains(f.Name, "showAddTask") || strings.Contains(f.Name, "Queue") {
			t.Fatalf("show_add_task 队列另有独立的锁 %q：必须与 onShowAddTask 共用 hookMu，"+
				"否则「读钩子/入队」与「设钩子/排空」不互斥，早到的请求会永久卡在队列里",
				f.Name)
		}
	}
}

func TestClientSubscribeAndBroadcast(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	ch, unsub := c.Subscribe()
	tr.events <- api.Message{Kind: api.MessageKindEvent, Event: &api.Event{Why: "progress"}}
	select {
	case ev := <-ch:
		if ev.Why != "progress" {
			t.Fatalf("事件不对: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("没有收到事件")
	}
	unsub()
}

func TestClientControlHooks(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	shown := make(chan struct{}, 1)
	c.SetOnShow(func() { shown <- struct{}{} })
	tr.events <- api.Message{Kind: api.MessageKindShow}
	select {
	case <-shown:
	case <-time.After(2 * time.Second):
		t.Fatal("show 回调没有触发")
	}
}

func TestClientHandshakeFailure(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message), failErr: context.Canceled}
	if _, err := newClient(context.Background(), tr); err == nil {
		t.Fatal("握手失败时应当报错")
	}
}

func TestClientCloseNotifiesHook(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{}, 1)
	c.SetOnClose(func() { closed <- struct{}{} })
	close(tr.events) // 模拟消息流结束
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("连接断开没有触发 onClose")
	}
}

func TestClientCloseBeforeHookRegistration(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	close(tr.events)
	time.Sleep(100 * time.Millisecond)
	closed := make(chan struct{}, 1)
	c.SetOnClose(func() { closed <- struct{}{} })
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("断开之后才注册的 hook 应当被立刻补发")
	}
}

func TestClientUnsubscribeThenBroadcast(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	_, unsub := c.Subscribe()
	unsub()
	// 退订之后再广播不能 panic（向已关闭的通道发送会 panic）。
	tr.events <- api.Message{Kind: api.MessageKindEvent, Event: &api.Event{Why: "x"}}
	time.Sleep(100 * time.Millisecond)
}

// TestClientLateShowAddTaskKeepsLatest 锁定「钩子注册前的多条
// show_add_task 只保留最新一条」。
//
// 旧实现（internal/ui 的 ipcClient）用容量 1 的队列 + 覆盖策略，
// 任务 4 的共享客户端改成了「只收第一条、后来的丢弃」——语义反了：
// 冷启动时业务侧连发两条 lgom:// 转发，用户最后点的那个反而被丢掉。
// 这里把覆盖策略钉死，同时保证补发仍然只有一条。
func TestClientLateShowAddTaskKeepsLatest(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"https://x/first", "https://x/second", "https://x/third"} {
		tr.events <- api.Message{
			Kind:        api.MessageKindShowAddTask,
			ShowAddTask: &api.ShowAddTaskParams{URL: u},
		}
	}

	// 等读循环把三条都消化掉：队列里最终只剩最后一条。
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.hookMu.Lock()
		n := len(c.showAddTaskQ)
		last := ""
		if n > 0 {
			last = c.showAddTaskQ[0].URL
		}
		c.hookMu.Unlock()
		if last == "https://x/third" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("队列没有收敛到最新一条，当前 %d 条，末尾 %q", n, last)
		}
		time.Sleep(5 * time.Millisecond)
	}

	got := make(chan api.ShowAddTaskParams, 4)
	c.SetOnShowAddTask(func(p api.ShowAddTaskParams) { got <- p })
	select {
	case p := <-got:
		if p.URL != "https://x/third" {
			t.Fatalf("补发的是 %q，want 最新一条 https://x/third", p.URL)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("迟到的 show_add_task 没有被补发")
	}
	select {
	case p := <-got:
		t.Fatalf("补发了多余的一条: %+v（队列容量应为 1）", p)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestClientBroadcastUnsubscribeRace 是旧
// internal/ui 的 TestIpcClient_BroadcastUnsubscribeRace 的等价护栏：
// 订阅/退订与广播并发时不得 panic（向已关闭的通道发送会直接 panic）。
//
// 旧实现的 broadcast 把订阅者快照复制到锁外再发送，退订正好落在窗口里
// 就会撞上 "send on closed channel"；共享客户端把发送与关闭都放在
// subMu 下互斥。这条用例只有在 -race 下才会暴露数据竞争，功能性 panic
// 则不加 race 也能抓。
func TestClientBroadcastUnsubscribeRace(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}

	const (
		subscribers = 64
		rounds      = 500
	)
	for round := 0; round < rounds; round++ {
		unsubAll := make([]func(), 0, subscribers)
		for i := 0; i < subscribers; i++ {
			_, unsub := c.Subscribe()
			unsubAll = append(unsubAll, unsub)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				c.broadcast(api.Event{Why: "race"})
			}
		}()
		go func() {
			defer wg.Done()
			for _, u := range unsubAll {
				u()
			}
		}()
		wg.Wait()
	}
}
