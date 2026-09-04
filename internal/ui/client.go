// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"lgo_download_manager/internal/ipc"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// ipcClient 是 Service 的 IPC 实现：UI 子进程通过它向业务进程发起
// 请求并接收事件流。一个进程内只允许一个实例。
type ipcClient struct {
	conn *ipc.Conn

	seqMu sync.Mutex
	seq   int64

	pendMu   sync.Mutex
	pending  map[string]chan ipc.ResultData
	subsMu   sync.Mutex
	subs     []chan scheduler.Event
	settings store.Settings

	onShow  func()
	onClose func()

	quit chan struct{}
	once sync.Once
}

// dialAndHandshake 连接业务 socket、发送 hello，并同步等待业务推送
// 的初始设置快照（MsgInit）。返回已就绪的 client（尚未启动读循环）。
func dialAndHandshake(socket, token string) (*ipcClient, store.Settings, error) {
	conn, err := ipc.Dial(socket, 10*time.Second)
	if err != nil {
		return nil, store.Settings{}, err
	}
	hello, err := ipc.Encode(ipc.HelloData{Token: token})
	if err != nil {
		conn.Close()
		return nil, store.Settings{}, err
	}
	hello.Type = ipc.MsgHello
	if err := conn.Send(hello); err != nil {
		conn.Close()
		return nil, store.Settings{}, fmt.Errorf("send hello: %w", err)
	}

	c := &ipcClient{
		conn:    conn,
		pending: map[string]chan ipc.ResultData{},
		quit:    make(chan struct{}),
	}

	// 等待第一条消息：业务端在握手成功后立即发送 MsgInit。
	msg, err := conn.Recv()
	if err != nil {
		conn.Close()
		return nil, store.Settings{}, fmt.Errorf("wait init: %w", err)
	}
	if msg.Type != ipc.MsgInit {
		conn.Close()
		return nil, store.Settings{}, fmt.Errorf("expected init, got %s", msg.Type)
	}
	var init ipc.InitData
	if err := json.Unmarshal(msg.Data, &init); err != nil {
		conn.Close()
		return nil, store.Settings{}, fmt.Errorf("decode init: %w", err)
	}
	c.settings = init.Settings
	return c, init.Settings, nil
}

// run 启动读循环（需在 UI 订阅建立后调用）。阻塞直到连接断开。
func (c *ipcClient) run() {
	defer c.conn.Close()
	for {
		msg, err := c.conn.Recv()
		if err != nil {
			c.notifyClose()
			return
		}
		switch msg.Type {
		case ipc.MsgEvent:
			var ed ipc.EventData
			if err := json.Unmarshal(msg.Data, &ed); err != nil {
				continue
			}
			c.broadcast(scheduler.Event{Why: ed.Why, Task: ed.Task, SpeedBPS: ed.SpeedBPS})
		case ipc.MsgResult:
			var res ipc.ResultData
			if err := json.Unmarshal(msg.Data, &res); err != nil {
				continue
			}
			c.deliver(msg.ID, res)
		case ipc.MsgShow:
			if c.onShow != nil {
				c.onShow()
			}
		case ipc.MsgClose:
			c.notifyClose()
			return
		case ipc.MsgInit:
			var init ipc.InitData
			if err := json.Unmarshal(msg.Data, &init); err == nil {
				c.setSettings(init.Settings)
			}
		}
	}
}

// request 发送一个远程调用并同步等待结果。
func (c *ipcClient) request(method string, params, result any) error {
	select {
	case <-c.quit:
		return errors.New("ui: connection closed")
	default:
	}

	c.seqMu.Lock()
	c.seq++
	id := fmt.Sprintf("%d", c.seq)
	c.seqMu.Unlock()

	ch := make(chan ipc.ResultData, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()
	defer func() {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
	}()

	call, err := ipc.Call(id, method, params)
	if err != nil {
		return err
	}
	if err := c.conn.Send(call); err != nil {
		return err
	}

	select {
	case <-c.quit:
		return errors.New("ui: connection closed")
	case res := <-ch:
		if !res.OK {
			return errors.New(res.Error)
		}
		if result != nil && len(res.Data) > 0 {
			return json.Unmarshal(res.Data, result)
		}
		return nil
	case <-time.After(30 * time.Second):
		return errors.New("ui: request timeout: " + method)
	}
}

func (c *ipcClient) deliver(id string, res ipc.ResultData) {
	c.pendMu.Lock()
	ch := c.pending[id]
	c.pendMu.Unlock()
	if ch != nil {
		select {
		case ch <- res:
		default:
		}
	}
}

func (c *ipcClient) setSettings(s store.Settings) {
	c.pendMu.Lock() // 复用同一把锁即可：settings 只在这里与 Settings() 访问
	defer c.pendMu.Unlock()
	c.settings = s
}

func (c *ipcClient) notifyClose() {
	c.once.Do(func() {
		close(c.quit)
		if c.onClose != nil {
			c.onClose()
		}
	})
}

// Subscribe 注册事件订阅；返回的事件流语义与 scheduler.Subscribe 一致。
func (c *ipcClient) Subscribe() (<-chan scheduler.Event, func()) {
	ch := make(chan scheduler.Event, 64)
	c.subsMu.Lock()
	c.subs = append(c.subs, ch)
	c.subsMu.Unlock()
	return ch, func() {
		c.subsMu.Lock()
		defer c.subsMu.Unlock()
		for i, s := range c.subs {
			if s == ch {
				c.subs = append(c.subs[:i], c.subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
}

// broadcast 向所有订阅者非阻塞派发事件（与 scheduler.publish 的丢弃语义一致）。
func (c *ipcClient) broadcast(ev scheduler.Event) {
	c.subsMu.Lock()
	subs := append([]chan scheduler.Event(nil), c.subs...)
	c.subsMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// ---- Service 实现 ----

func (c *ipcClient) List(filter store.StatusFilter, sort store.TaskSort) ([]*store.Task, error) {
	var tasks []*store.Task
	err := c.request(ipc.MethodList, ipc.ListParams{Filter: string(filter), Sort: string(sort)}, &tasks)
	return tasks, err
}

func (c *ipcClient) AddTask(in AddTaskInput) (*store.Task, error) {
	var tk *store.Task
	params := ipc.AddParams{
		URL:          in.URL,
		SavePath:     in.SavePath,
		ChunkCount:   in.ChunkCount,
		MinChunkSize: in.MinChunkSize,
	}
	err := c.request(ipc.MethodAdd, params, &tk)
	return tk, err
}

func (c *ipcClient) Start(taskID string) error {
	return c.request(ipc.MethodStart, ipc.TaskParams{ID: taskID}, nil)
}

func (c *ipcClient) Pause(taskID string) error {
	return c.request(ipc.MethodPause, ipc.TaskParams{ID: taskID}, nil)
}

func (c *ipcClient) Delete(taskID string) error {
	return c.request(ipc.MethodDelete, ipc.TaskParams{ID: taskID}, nil)
}

func (c *ipcClient) Probe(url string) (int64, error) {
	var res ipc.ProbeResult
	if err := c.request(ipc.MethodProbe, ipc.ProbeParams{URL: url}, &res); err != nil {
		return 0, err
	}
	return res.TotalSize, nil
}

func (c *ipcClient) Settings() store.Settings {
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	return c.settings
}

func (c *ipcClient) SaveSettings(s store.Settings) error {
	if err := c.request(ipc.MethodSaveSettings, s, nil); err != nil {
		return err
	}
	c.setSettings(s)
	return nil
}

func (c *ipcClient) Close() error {
	c.notifyClose()
	return c.conn.Close()
}

// SetOnShow 注册 MsgShow（业务托盘「显示窗口」）回调。
func (c *ipcClient) SetOnShow(f func()) { c.onShow = f }

// SetOnClose 注册业务侧要求退出（MsgClose / 连接断开）时的回调。
func (c *ipcClient) SetOnClose(f func()) { c.onClose = f }
