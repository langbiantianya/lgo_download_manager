// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package uimgr 管理 Fyne UI 子进程的生命周期与 IPC 会话。
// 它运行在业务主进程中：负责生成 socket、spawn UI 子进程（自我复刻）、
// 校验握手、把 scheduler 事件推给 UI、并把 UI 的远程调用映射到
// scheduler/store/settings。
package uimgr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"lgo_download_manager/internal/ipc"
	"lgo_download_manager/internal/logging"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/settings"
	"lgo_download_manager/internal/store"
)

const (
	// validateEvery 为文件存在性轮询间隔（原 UI 窗口聚焦 ticker 的周期）。
	validateEvery = 10 * time.Second
	// handshakeTimeout 为等待 UI hello 与进程连接的最长时间。
	handshakeTimeout = 20 * time.Second
	// closeGrace 为收到退出指令后等待子进程自行退出的时间。
	closeGrace = 2 * time.Second
)

// Manager 拥有一个（至多一个）UI 子进程会话。
type Manager struct {
	st *store.Store
	sc *scheduler.Scheduler

	mu       sync.Mutex
	settings store.Settings

	uiSock string
	token  string

	cmd    *exec.Cmd
	conn   *ipc.Conn
	cancel context.CancelFunc
	waitCh chan struct{} // 会话结束通知
	gen    uint64        // 会话代号：清理路径据此识别自己是否仍属当前会话

	onExit   func()     // UI 会话结束后回调（业务侧日志/状态）
	exitOnce *sync.Once // 每个会话一个，保证 onExit 只触发一次
}

// New 创建 UI 管理器。
func New(st *store.Store, sc *scheduler.Scheduler) *Manager {
	return &Manager{
		st: st,
		sc: sc,
		onExit: func() {
			logging.Println("uimgr: UI session ended; business keeps running")
		},
	}
}

// SetSettings 更新管理器持有的权威设置（业务进程内共享）。
func (m *Manager) SetSettings(s store.Settings) {
	m.mu.Lock()
	m.settings = s
	m.mu.Unlock()
	// 把并发上限推到 scheduler:用户在「设置」里改 MaxConcurrent 后,
	// 下次保存就会触发 promotePending——可能提升之前因超出限额而
	// 排队的 Pending 任务。
	m.sc.SetMaxConcurrent(s.EffectiveMaxConcurrent())
}

// Settings 返回当前权威设置的副本。
func (m *Manager) Settings() store.Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

// OnExit 注册 UI 会话结束回调。
func (m *Manager) OnExit(f func()) {
	m.onExit = f
}

// Running 报告 UI 子进程会话是否存活。
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.conn != nil
}

// Open 确保 UI 可见：已有会话则发送 show；否则 spawn 一个新 UI 子进程。
func (m *Manager) Open() {
	if m.Running() {
		_ = m.send(ipc.Message{Type: ipc.MsgShow})
		return
	}
	if err := m.Start(); err != nil {
		logging.Printf("uimgr: cannot open UI: %v", err)
	}
}

// Start 生成 socket 并拉起 UI 子进程（同一可执行文件的自我复刻）。
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// conn 非 nil 表示已有可用会话；cancel 非 nil 表示会话正在握手
	// （子进程已拉起但尚未挂上连接），两种情况都不允许再拉起第二个
	// UI 子进程，否则会留下一个无人回收的窗口。
	if m.conn != nil || m.cancel != nil {
		return fmt.Errorf("uimgr: UI already running")
	}

	sock, err := m.makeSocketPath()
	if err != nil {
		return err
	}
	token, err := randomToken()
	if err != nil {
		return err
	}

	ln, err := ipc.Listen(sock)
	if err != nil {
		return err
	}
	// Listen 成功之后的任何失败都要关掉监听并删掉 socket 文件，
	// 否则会留下一个无人监听的残留路径。
	fail := func(err error) error {
		ln.Close()
		_ = os.Remove(sock)
		return err
	}

	self, err := os.Executable()
	if err != nil {
		return fail(fmt.Errorf("uimgr: resolve self executable: %w", err))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(),
		ipc.EnvUIChild+"=1",
		ipc.EnvUISocket+"="+sock,
		ipc.EnvUIToken+"="+token,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return fail(fmt.Errorf("uimgr: spawn UI child: %w", err))
	}

	m.gen++
	gen := m.gen
	m.uiSock = sock
	m.token = token
	m.cancel = cancel
	m.cmd = cmd
	m.exitOnce = &sync.Once{}

	// 子进程的唯一回收点：Wait 必须被且仅被调用一次。无论握手/init
	// 是否成功，进程消亡都会在这里被回收（失败路径另行 Kill），
	// 避免未完成握手的 UI 子进程变成无主进程、带着窗口一直存活。
	// 这里只捕获局部 cmd，不再读写 m.cmd，消除与 finishSession 的竞态。
	go func() {
		err := cmd.Wait()
		logging.Printf("uimgr: UI child exited: %v", err)
		cancel()
	}()

	// 接受握手（后台，不阻塞业务启动流程）。
	go m.acceptLoop(ln, ctx, gen)
	return nil
}

// killChild 终止会话 gen 的 UI 子进程（若它仍属于该会话）。被 Kill 的
// 进程仍由 Start 中的回收 goroutine 负责 Wait，不会留下僵尸进程。
func (m *Manager) killChild(gen uint64) {
	m.mu.Lock()
	cmd := m.cmd
	cur := m.gen == gen
	m.mu.Unlock()
	if !cur || cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

// acceptLoop 接受一次 UI 子进程连接并驱动会话。它是会话 gen 的唯一
// 结束者：无论握手成败，返回时都会 finishSession，并在失败路径主动
// 结束子进程。
func (m *Manager) acceptLoop(ln net.Listener, ctx context.Context, gen uint64) {
	defer ln.Close()
	// 子进程早退或会话被 Close 关停时关掉监听，避免 Accept 永久阻塞。
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			ln.Close()
		case <-stopped:
		}
	}()
	// 子进程必须在 budget 内完成连接与握手，否则同样是失败路径：
	// 关掉监听让 Accept 返回，从而 Kill 掉可能已经僵死的子进程并结束
	// 会话，而不是让会话永远停在「正在启动」。
	deadline := time.AfterFunc(handshakeTimeout, func() { ln.Close() })
	defer deadline.Stop()
	defer m.finishSession(gen)

	conn, err := ln.Accept()
	if err != nil {
		m.killChild(gen)
		return
	}
	ch := ipc.NewConn(conn)
	if err := m.handshake(ch); err != nil {
		logging.Printf("uimgr: handshake failed: %v", err)
		ch.Close()
		m.killChild(gen)
		return
	}
	// 握手已完成：连接/握手预算不再适用（后续读取由各自的 deadline 管）。
	deadline.Stop()

	// 推送初始快照（权威设置）。负载已序列化，直接拼帧发出去。
	initMsg, err := ipc.Encode(ipc.InitData{Settings: m.Settings()})
	if err == nil {
		err = ch.SendPayload(ipc.MsgInit, initMsg.Data)
	}
	if err != nil {
		logging.Printf("uimgr: send init: %v", err)
		ch.Close()
		m.killChild(gen)
		return
	}

	m.mu.Lock()
	if m.gen != gen {
		// 会话已在握手期间被回收：不要再把连接挂到管理器上。
		m.mu.Unlock()
		ch.Close()
		return
	}
	waitCh := make(chan struct{})
	m.conn = ch
	m.waitCh = waitCh
	m.mu.Unlock()

	// 业务侧文件存在性轮询（原 UI 焦点 ticker，随会话启停）。
	go m.validateLoop(ctx)

	// 事件转发与调用处理并行跑在同一个 conn 上。
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); m.serveCalls(ch, ctx) }()
	go func() { defer wg.Done(); m.forwardEvents(ch, ctx) }()
	wg.Wait()
	close(waitCh)
}

// finishSession 结束会话 gen：清空 conn/cmd/取消函数并移除 socket 文件。
// 只有仍是当前会话的调用会生效——重复清理、已被新会话取代的清理都是
// 空操作；onExit 因此每个会话只触发一次。
func (m *Manager) finishSession(gen uint64) {
	m.mu.Lock()
	if m.gen != gen {
		m.mu.Unlock()
		return
	}
	m.gen++ // 使本会话的后续清理失效
	ch := m.conn
	once := m.exitOnce
	onExit := m.onExit
	m.conn = nil
	m.exitOnce = nil
	m.waitCh = nil
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel = nil
	if m.uiSock != "" {
		_ = os.Remove(m.uiSock)
		m.uiSock = ""
	}
	m.cmd = nil
	m.mu.Unlock()

	if ch != nil {
		_ = ch.Close()
	}
	if once != nil && onExit != nil {
		once.Do(onExit)
	}
}

// handshake 等待 UI 的 hello 并校验 token。
func (m *Manager) handshake(ch *ipc.Conn) error {
	ch.SetReadDeadline(time.Now().Add(handshakeTimeout))
	defer ch.SetReadDeadline(time.Time{})
	msg, err := ch.Recv()
	if err != nil {
		return err
	}
	if msg.Type != ipc.MsgHello {
		return fmt.Errorf("uimgr: expected %s, got %s", ipc.MsgHello, msg.Type)
	}
	var hello ipc.HelloData
	if err := json.Unmarshal(msg.Data, &hello); err != nil {
		return fmt.Errorf("uimgr: decode hello: %w", err)
	}
	m.mu.Lock()
	tok := m.token
	m.mu.Unlock()
	if hello.Token == "" || hello.Token != tok {
		return fmt.Errorf("uimgr: bad handshake token")
	}
	return nil
}

// serveCalls 处理 UI 的远程调用并逐个应答。
func (m *Manager) serveCalls(ch *ipc.Conn, ctx context.Context) {
	for {
		msg, err := ch.Recv()
		if err != nil {
			return
		}
		if msg.Type != ipc.MsgCall {
			continue
		}
		var call ipc.CallData
		if err := json.Unmarshal(msg.Data, &call); err != nil {
			reply, _ := ipc.Reply(msg.ID, false, "decode call: "+err.Error(), nil)
			_ = ch.Send(reply)
			continue
		}
		result, rerr := m.dispatch(call.Method, call.Params)
		reply, merr := ipc.Reply(msg.ID, rerr == nil, errString(rerr), result)
		if merr != nil {
			return
		}
		if err := ch.Send(reply); err != nil {
			return
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// dispatch 把一种远程方法映射到 scheduler/store/settings。
func (m *Manager) dispatch(method string, raw json.RawMessage) (any, error) {
	switch method {
	case ipc.MethodList:
		var p ipc.ListParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return m.sc.List(store.StatusFilter(p.Filter), store.TaskSort(p.Sort))

	case ipc.MethodAdd:
		var p ipc.AddParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		kind := p.Protocol
		if kind == "" {
			var derr error
			kind, derr = protocol.DetectKind(p.URL, "")
			if derr != nil {
				return nil, derr
			}
		}
		return m.sc.Add(scheduler.AddTaskInput{
			URL:          p.URL,
			SavePath:     p.SavePath,
			Protocol:     kind,
			Auth:         p.Auth,
			ChunkCount:   p.ChunkCount,
			MinChunkSize: p.MinChunkSize,
		})

	case ipc.MethodStart:
		var p ipc.TaskParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return nil, m.sc.Start(p.ID)

	case ipc.MethodPause:
		var p ipc.TaskParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return nil, m.sc.Pause(p.ID)

	case ipc.MethodIsPreparing:
		var p ipc.TaskParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return m.sc.IsPreparing(p.ID), nil
	case ipc.MethodDelete:
		var p ipc.TaskParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return nil, m.sc.Delete(p.ID)

	case ipc.MethodSaveSettings:
		var s store.Settings
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		if err := settings.Save(m.st, s); err != nil {
			return nil, err
		}
		m.SetSettings(s)
		return struct{}{}, nil

	case ipc.MethodProbe:
		var p ipc.ProbeParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		size, err := m.probe(p.URL)
		if err != nil {
			return nil, err
		}
		return ipc.ProbeResult{TotalSize: size}, nil

	default:
		return nil, fmt.Errorf("uimgr: unknown method %q", method)
	}
}

// probe 在业务进程内探测 URL 目标大小（复用业务侧代理配置）。
func (m *Manager) probe(raw string) (int64, error) {
	kind, err := protocol.DetectKind(raw, "")
	if err != nil {
		return 0, err
	}
	s := m.Settings()
	driver, err := protocol.New(raw, kind, protocol.Auth{AuthOptions: protocol.AuthOptions{
		UserAgent:   s.UserAgent,
		Cookies:     s.Cookies,
		FTPPassive:  s.FTPPassive,
		ProxyMode:   s.ProxyMode,
		ProxyURL:    s.ProxyURL,
		ProxyBypass: s.ProxyBypass,
	}})
	if err != nil {
		return 0, err
	}
	defer driver.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	caps, err := driver.Probe(ctx)
	if err != nil {
		return 0, err
	}
	return caps.TotalSize, nil
}

// forwardEvents 把 scheduler 事件转换成 EventData 推给 UI。
func (m *Manager) forwardEvents(ch *ipc.Conn, ctx context.Context) {
	evCh, unsub := m.sc.Subscribe()
	defer unsub()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-evCh:
			msg, err := ipc.Encode(ipc.EventData{
				Why:      ev.Why,
				Task:     ev.Task,
				SpeedBPS: ev.SpeedBPS,
			})
			if err != nil {
				continue
			}
			// 负载已序列化，直接拼帧发送，避免整包再 marshal 一次。
			if err := ch.SendPayload(ipc.MsgEvent, msg.Data); err != nil {
				return
			}
		}
	}
}

// validateLoop 在 UI 会话存活期间定期做文件存在性校验。
func (m *Manager) validateLoop(ctx context.Context) {
	t := time.NewTicker(validateEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.sc.ValidateFileExistence()
		}
	}
}

// Close 关闭 UI 子进程：先安排超时强杀，再发 MsgClose 等其自行退出。
func (m *Manager) Close() {
	m.mu.Lock()
	gen := m.gen
	ch := m.conn
	cmd := m.cmd
	m.mu.Unlock()

	if ch != nil {
		// 兜底 Kill 必须先于 Send 排上定时器：Send 是持 wmu 的阻塞写，
		// 子进程僵死时可能永不返回，只有定时器能保证退出一定发生，
		// 不至于吃掉 main 的 5s 退出预算。
		var killTimer *time.Timer
		if cmd != nil && cmd.Process != nil {
			killTimer = time.AfterFunc(closeGrace, func() { _ = cmd.Process.Kill() })
		}
		_ = ch.Send(ipc.Message{Type: ipc.MsgClose})
		// 给 UI 一段窗口期自行退出。
		select {
		case <-time.After(closeGrace):
		case <-m.waitDone():
		}
		if killTimer != nil {
			killTimer.Stop()
		}
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	m.finishSession(gen)
}

func (m *Manager) waitDone() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.waitCh == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return m.waitCh
}

// send 发送一条消息（并发安全）。
func (m *Manager) send(msg ipc.Message) error {
	m.mu.Lock()
	ch := m.conn
	m.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("uimgr: no active UI connection")
	}
	return ch.Send(msg)
}

// SendShowAddTask 让 UI 弹出「新建下载任务」对话框。
//
// 行为:
//   - UI 已运行:立刻把参数序列化后发出,UI 端读完显示主窗口并打开对话框;
//   - UI 未运行:Start() 拉起一个子进程后,在握手/初始化预算内等待 conn
//     挂上,然后再 send——冷启动下 Start() 立刻返回,但 acceptLoop 的握手
//     仍是异步的,不等待就几乎一定丢失首条消息。
//
// 业务进程 lgom:// URL 转发路径用它替代原先直接 Add/Start,避免误触;
// 仅在所有路径都失败时返回 error,由调用方决定是否回退到旧行为。
func (m *Manager) SendShowAddTask(p ipc.ShowAddTaskParams) error {
	if !m.Running() {
		if err := m.Start(); err != nil {
			return fmt.Errorf("uimgr: cannot start UI for show_add_task: %w", err)
		}
		// 等待握手 + init 完成(conn 在 acceptLoop 中设置);握手上限是
		// handshakeTimeout=20s,留 30s 余量足够覆盖实际路径。
		if err := m.waitConnReady(30 * time.Second); err != nil {
			return err
		}
	}
	enc, err := ipc.Encode(p)
	if err != nil {
		return fmt.Errorf("uimgr: encode show_add_task: %w", err)
	}
	enc.Type = ipc.MsgShowAddTask
	if err := m.send(enc); err != nil {
		return err
	}
	return nil
}

// waitConnReady 阻塞直到 m.conn 非空或超时/被取消。Running() 已 true 时
// 立即返回 nil。用于 SendShowAddTask 等「先要 UI 起来才能发消息」的场景。
func (m *Manager) waitConnReady(timeout time.Duration) error {
	if m.Running() {
		return nil
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if m.Running() {
			return nil
		}
		select {
		case <-deadline.C:
			return fmt.Errorf("uimgr: UI connection not ready within %s", timeout)
		case <-tick.C:
		}
	}
}

// makeSocketPath 生成带随机后缀的 socket 路径（用户缓存目录）。
func (m *Manager) makeSocketPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	sub := filepath.Join(dir, "lgo_download_manager")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		return "", fmt.Errorf("uimgr: mkdir %s: %w", sub, err)
	}
	suffix, err := randomToken()
	if err != nil {
		return "", err
	}
	return filepath.Join(sub, "ui-"+suffix[:8]+".sock"), nil
}

// randomToken 生成 32 字节随机 hex 串。
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("uimgr: random token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
