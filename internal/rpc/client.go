// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package rpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"lgo_download_manager/internal/api"
)

// callTimeout 是单次 RPC 的超时上限。
const callTimeout = 30 * time.Second

// Client 是 UI 侧看到的全部能力。方法签名与旧的 ui.Service 一一对应，
// 只是类型换成了契约层的 api 包。
type Client interface {
	List(filter api.StatusFilter, sort api.TaskSort) ([]*api.Task, error)
	AddTask(in api.AddTaskInput) (*api.Task, error)
	Start(taskID string) error
	Pause(taskID string) error
	Delete(taskID string) error
	IsPreparing(taskID string) bool
	Probe(url string) (int64, error)
	BTTaskDetail(taskID string) (*api.BTDetail, error)
	Settings() api.Settings
	SaveSettings(s api.Settings) error

	// Subscribe 订阅进度事件；unsub 取消订阅。
	Subscribe() (<-chan api.Event, func())
	// SetOnShow 注册「显示窗口」回调。
	SetOnShow(f func())
	// SetOnShowAddTask 注册「弹出新建任务对话框」回调。
	SetOnShowAddTask(f func(api.ShowAddTaskParams))
	// SetOnLanguage 注册「语言变更」回调。
	SetOnLanguage(f func())
	// SetOnClose 注册「业务侧要求退出 / 连接断开」回调。
	SetOnClose(f func())

	Close() error
}

// client 是基于 Transport 的共享客户端实现。两种协议共用它，
// 因此超时、错误包装、事件扇出等语义只有一处。
type client struct {
	tr Transport

	settingsMu sync.Mutex
	settings   api.Settings

	subMu sync.Mutex
	subs  []chan api.Event

	hookMu        sync.Mutex
	onShow        func()
	onShowAddTask func(api.ShowAddTaskParams)
	onLanguage    func()
	onClose       func()

	closed bool
	once   sync.Once
}

var _ Client = (*client)(nil)

// newClient 建立客户端：先用 get_settings 做一次往返，既校验 token 又拿到
// 初始设置快照，然后拉起消息流。
func newClient(ctx context.Context, tr Transport) (*client, error) {
	c := &client{tr: tr}

	out := &api.Settings{}
	if err := c.call(ctx, api.MethodGetSettings, &api.Empty{}, out); err != nil {
		return nil, fmt.Errorf("rpc: 握手失败: %w", err)
	}
	c.settingsMu.Lock()
	c.settings = *out
	c.settingsMu.Unlock()

	go c.readLoop(ctx)
	return c, nil
}

// call 执行一次 RPC，带 30s 超时。
func (c *client) call(ctx context.Context, method string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return c.tr.Call(ctx, method, in, out)
}

// readLoop 消费服务端推送的消息流。
func (c *client) readLoop(ctx context.Context) {
	ch, err := c.tr.Stream(ctx)
	if err != nil {
		c.notifyClose()
		return
	}
	for {
		select {
		case <-ctx.Done():
			c.notifyClose()
			return
		case msg, ok := <-ch:
			if !ok {
				c.notifyClose()
				return
			}
			c.dispatch(msg)
		}
	}
}

// dispatch 把一条消息分发到事件订阅者或控制回调。
func (c *client) dispatch(msg api.Message) {
	switch msg.Kind {
	case api.MessageKindEvent:
		if msg.Event != nil {
			c.broadcast(*msg.Event)
		}
	case api.MessageKindShow:
		if h := c.showHook(); h != nil {
			h()
		}
	case api.MessageKindShowAddTask:
		if h := c.showAddTaskHook(); h != nil && msg.ShowAddTask != nil {
			h(*msg.ShowAddTask)
		}
	case api.MessageKindLanguage:
		if h := c.languageHook(); h != nil {
			h()
		}
	case api.MessageKindClose:
		c.notifyClose()
	}
}

func (c *client) showHook() func() {
	c.hookMu.Lock()
	defer c.hookMu.Unlock()
	return c.onShow
}

func (c *client) showAddTaskHook() func(api.ShowAddTaskParams) {
	c.hookMu.Lock()
	defer c.hookMu.Unlock()
	return c.onShowAddTask
}

func (c *client) languageHook() func() {
	c.hookMu.Lock()
	defer c.hookMu.Unlock()
	return c.onLanguage
}

// broadcast 向所有订阅者非阻塞派发事件。订阅者消费不过来时丢弃事件，
// 绝不阻塞读循环。
func (c *client) broadcast(ev api.Event) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for _, ch := range c.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe 订阅进度事件。退订时关闭通道，与广播互斥。
func (c *client) Subscribe() (<-chan api.Event, func()) {
	ch := make(chan api.Event, 64)
	c.subMu.Lock()
	c.subs = append(c.subs, ch)
	c.subMu.Unlock()
	return ch, func() {
		c.subMu.Lock()
		defer c.subMu.Unlock()
		for i, s := range c.subs {
			if s == ch {
				c.subs = append(c.subs[:i], c.subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
}

func (c *client) SetOnShow(f func()) {
	c.hookMu.Lock()
	c.onShow = f
	c.hookMu.Unlock()
}

func (c *client) SetOnShowAddTask(f func(api.ShowAddTaskParams)) {
	c.hookMu.Lock()
	c.onShowAddTask = f
	c.hookMu.Unlock()
}

func (c *client) SetOnLanguage(f func()) {
	c.hookMu.Lock()
	c.onLanguage = f
	c.hookMu.Unlock()
}

// SetOnClose 注册「业务侧要求退出 / 连接断开」回调。若连接在注册之前
// 就已经断开，立刻补一次回调——否则 UI 会一直以为业务进程还在。
func (c *client) SetOnClose(f func()) {
	c.hookMu.Lock()
	c.onClose = f
	closed := c.closed
	c.hookMu.Unlock()
	if closed && f != nil {
		f()
	}
}

// notifyClose 标记连接已断开并触发一次 onClose 回调。
func (c *client) notifyClose() {
	c.once.Do(func() {
		c.hookMu.Lock()
		c.closed = true
		h := c.onClose
		c.hookMu.Unlock()
		if h != nil {
			h()
		}
	})
}

// List 查询任务列表。
func (c *client) List(filter api.StatusFilter, sort api.TaskSort) ([]*api.Task, error) {
	out := &api.ListResult{}
	if err := c.call(context.Background(), api.MethodList, &api.ListParams{Filter: filter, Sort: sort}, out); err != nil {
		return nil, err
	}
	return out.Tasks, nil
}

// AddTask 添加一个任务（不自动启动）。
func (c *client) AddTask(in api.AddTaskInput) (*api.Task, error) {
	out := &api.Task{}
	if err := c.call(context.Background(), api.MethodAdd, &in, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Start 启动任务。
func (c *client) Start(taskID string) error {
	return c.call(context.Background(), api.MethodStart, &api.TaskRef{ID: taskID}, &api.Empty{})
}

// Pause 暂停任务。
func (c *client) Pause(taskID string) error {
	return c.call(context.Background(), api.MethodPause, &api.TaskRef{ID: taskID}, &api.Empty{})
}

// Delete 删除任务。
func (c *client) Delete(taskID string) error {
	return c.call(context.Background(), api.MethodDelete, &api.TaskRef{ID: taskID}, &api.Empty{})
}

// IsPreparing 报告任务是否处于「Start 已预留 slot 但引擎尚未启动」的
// 准备阶段。查询失败时按 false 处理（与旧实现一致）。
func (c *client) IsPreparing(taskID string) bool {
	out := &api.BoolResult{}
	if err := c.call(context.Background(), api.MethodIsPreparing, &api.TaskRef{ID: taskID}, out); err != nil {
		return false
	}
	return out.Value
}

// Probe 探测 URL 目标大小。
func (c *client) Probe(url string) (int64, error) {
	out := &api.ProbeResult{}
	if err := c.call(context.Background(), api.MethodProbe, &api.ProbeParams{URL: url}, out); err != nil {
		return 0, err
	}
	return out.TotalSize, nil
}

// BTTaskDetail 返回 BT 任务的实时详情。
func (c *client) BTTaskDetail(taskID string) (*api.BTDetail, error) {
	out := &api.BTDetail{}
	if err := c.call(context.Background(), api.MethodBTInfo, &api.BTInfoParams{TaskID: taskID}, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Settings 返回权威设置快照。
func (c *client) Settings() api.Settings {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	return c.settings
}

// SaveSettings 持久化设置。
func (c *client) SaveSettings(s api.Settings) error {
	if err := c.call(context.Background(), api.MethodSaveSettings, &s, &api.Empty{}); err != nil {
		return err
	}
	c.settingsMu.Lock()
	c.settings = s
	c.settingsMu.Unlock()
	return nil
}

// Close 关闭连接。
func (c *client) Close() error {
	c.notifyClose()
	return nil
}
