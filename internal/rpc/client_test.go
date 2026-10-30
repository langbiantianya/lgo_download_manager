// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package rpc

import (
	"context"
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
		c.showAddTaskMu.Lock()
		n := len(c.showAddTaskQ)
		c.showAddTaskMu.Unlock()
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
