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

	"lgo_download_manager/internal/bittorrent"
	"lgo_download_manager/internal/ilocale"
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

	// 下面几个回调由主 goroutine 注册、由 IPC 读循环读取,统一由 pendMu
	// 保护——注册与读取分属不同 goroutine,不加锁就是数据竞争
	// (go test -race 能稳定报出来)。
	onShow        func()
	onClose       func()
	onShowAddTask func(ipc.ShowAddTaskParams)
	onLanguage    func() // 收到 MsgInit/MsgLanguage 时调用,负责 UI 重译

	// closed 记录连接是否已经断开。onClose 可能在连接断开之后才注册
	// (冷启动时序:业务进程先走,UI 子进程后连上),那时要把「已经关闭」
	// 补给它,否则 UI 不知道业务进程已经走了,窗口就一直挂着。
	closed bool

	// showAddTaskQ 在 onShowAddTask 尚未注册前缓存收到的 MsgShowAddTask。
	// 业务进程的 lgom:// 转发可能在 RunChild 还没调到 SetOnShowAddTask
	// 时就已发送;不缓存就直接丢弃,主窗口被 ShowFromTray 拉起后却看不到
	// 对话框。容量 1:同一会话内 URL 转发连续触发概率极低,丢一条比塞一队
	// 陈旧 URL 更安全。
	showAddTaskQ       []ipc.ShowAddTaskParams
	showAddTaskDrained bool

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
	// 握手有读超时:业务进程连上了却一直没推 MsgInit(它自己卡住了),
	// 没有超时的话 UI 子进程会永远挂在这里,连窗口都出不来。
	if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		conn.Close()
		return nil, store.Settings{}, fmt.Errorf("set handshake deadline: %w", err)
	}
	msg, err := conn.Recv()
	if err != nil {
		conn.Close()
		return nil, store.Settings{}, fmt.Errorf("wait init: %w", err)
	}
	// 握手完成:清掉读超时,运行期的连接要能一直等着业务进程的消息。
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, store.Settings{}, fmt.Errorf("clear handshake deadline: %w", err)
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
	// 同步应用权威语言到 ilocale:握手阶段在 c.run() 启动前就消费了
	// MsgInit,此时 NewMainWindow 尚未构造,onLanguage 还没注册;若
	// 把 ilocale 推迟到 run() 的 case ipc.MsgInit 分支,业务侧
	// acceptLoop 只发一次 MsgInit,run() 会卡在下一轮 Recv 上,
	// 永远不会触发语言切换,UI 一直停在默认 zh-Hans。
	// 这里同步落一次——MsgInit 是一次性快照,运行中切语言由
	// uimgr.SetLanguage → MsgLanguage 路径承担,仍走 run() 分支。
	ilocale.Set(init.Settings.Language)
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
				// 解析失败也要给等待方一个答复,否则这次请求会一直
				// 等到 30 秒超时才返回。
				res = ipc.ResultData{Error: "malformed result: " + err.Error()}
			}
			c.deliver(msg.ID, res)
		case ipc.MsgShow:
			if hook := c.showHook(); hook != nil {
				hook()
			}
		case ipc.MsgShowAddTask:
			var p ipc.ShowAddTaskParams
			if err := json.Unmarshal(msg.Data, &p); err != nil {
				continue
			}
			c.pendMu.Lock()
			hook := c.onShowAddTask
			// hook 未注册时缓存一条;drained=true 表示 hook 之前注册过
			// 又被清除(SetOnShowAddTask(nil)),此时不再缓存——防御性兜底,
			// 因为正常路径下 hook 一旦注册就永驻。
			if hook == nil && !c.showAddTaskDrained {
				c.showAddTaskQ = append(c.showAddTaskQ[:0], p)
			}
			c.pendMu.Unlock()
			if hook != nil {
				hook(p)
			}
		case ipc.MsgClose:
			c.notifyClose()
			return
		case ipc.MsgInit:
			var init ipc.InitData
			if err := json.Unmarshal(msg.Data, &init); err == nil {
				c.setSettings(init.Settings)
				// InitData 已经携带权威 Language,这里同步到 ilocale。
				// onLanguage 由 RunChild 在 NewMainWindow 之前/之后注册:
				// - 若 NewMainWindow 已经构造,applyLanguage 会跑;
				// - 若还没有,onLanguage 为 nil,RunChild 在 NewMainWindow
				//   完成后会基于 ilocale.Current 立即跑一次 applyLanguage,
				//   不需要额外通知。
				ilocale.Set(init.Settings.Language)
				if hook := c.languageHook(); hook != nil {
					hook() // 调用方自行切到 fyne 事件线程
				}
			}
		case ipc.MsgLanguage:
			var ld ipc.LanguageData
			if err := json.Unmarshal(msg.Data, &ld); err == nil {
				ilocale.Set(ld.Language)
				if hook := c.languageHook(); hook != nil {
					hook() // 调用方自行切到 fyne 事件线程
				}
			}
		}
	}
}

// showHook 返回 MsgShow 回调（读循环用）。注册与读取分属不同 goroutine,
// 这里统一由 pendMu 保护,避免数据竞争。
func (c *ipcClient) showHook() func() {
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	return c.onShow
}

// languageHook 返回语言变更回调（读循环用）。
func (c *ipcClient) languageHook() func() {
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	return c.onLanguage
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
		c.pendMu.Lock()
		c.closed = true
		hook := c.onClose
		c.pendMu.Unlock()
		if hook != nil {
			hook()
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
		// 退订与 broadcast 互斥:持 subsMu 关闭通道,保证不会有人正往
		// 这个通道里写(否则会撞出 "send on closed channel" panic)。
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
//
// 发送在 subsMu 保护下完成:退订会关闭通道,若在锁外发送,就会与
// 「退订的同时广播」撞出 send on closed channel。发送带 default,
// 不会阻塞持锁的 goroutine。
func (c *ipcClient) broadcast(ev scheduler.Event) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	for _, ch := range c.subs {
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
		Protocol:     in.Protocol,
		Auth:         in.Auth,
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

func (c *ipcClient) IsPreparing(taskID string) bool {
	var out bool
	if err := c.request(ipc.MethodIsPreparing, ipc.TaskParams{ID: taskID}, &out); err != nil {
		return false
	}
	return out
}
func (c *ipcClient) Probe(url string) (int64, error) {
	var res ipc.ProbeResult
	if err := c.request(ipc.MethodProbe, ipc.ProbeParams{URL: url}, &res); err != nil {
		return 0, err
	}
	return res.TotalSize, nil
}

// BTTaskDetail 向业务进程索取 BT 任务的实时详情快照。
func (c *ipcClient) BTTaskDetail(taskID string) (*bittorrent.Detail, error) {
	var d *bittorrent.Detail
	if err := c.request(ipc.MethodBTInfo, ipc.BTInfoParams{TaskID: taskID}, &d); err != nil {
		return nil, err
	}
	return d, nil
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
func (c *ipcClient) SetOnShow(f func()) {
	c.pendMu.Lock()
	c.onShow = f
	c.pendMu.Unlock()
}

// SetOnClose 注册业务侧要求退出（MsgClose / 连接断开）时的回调。
//
// 连接可能已经断了才轮到这次注册(冷启动时序),那种情况下立刻把
// 「已经关闭」补给它——否则 UI 会一直以为业务进程还在。
func (c *ipcClient) SetOnClose(f func()) {
	c.pendMu.Lock()
	c.onClose = f
	closed := c.closed
	c.pendMu.Unlock()
	if closed && f != nil {
		f()
	}
}

// SetOnShowAddTask 注册 MsgShowAddTask（业务转发 lgom:// 后弹出对话框）回调。
// f 在 IPC 读循环里被调用,实现在 Fyne 事件线程上自行 fyne.Do。
//
// 注册时如果有缓存的 MsgShowAddTask（hook 还没装好时就到的消息）,全部
// 按到达顺序补发一次——保证冷启动「业务先 send,RunChild 还没 SetOnShowAddTask」
// 这种窗口期里的 URL 不会丢。
func (c *ipcClient) SetOnShowAddTask(f func(ipc.ShowAddTaskParams)) {
	c.pendMu.Lock()
	c.onShowAddTask = f
	pending := c.showAddTaskQ
	c.showAddTaskQ = nil
	c.showAddTaskDrained = true
	c.pendMu.Unlock()
	for _, p := range pending {
		f(p)
	}
}

// SetOnLanguage 注册「语言变更」回调:在收到 MsgInit/MsgLanguage 时被调用,
// 用于让 UI 重译所有 widget。回调跑在 IPC 读循环里,实现在 Fyne 事件
// 线程上自行 fyne.Do。
func (c *ipcClient) SetOnLanguage(f func()) {
	c.pendMu.Lock()
	c.onLanguage = f
	c.pendMu.Unlock()
}
