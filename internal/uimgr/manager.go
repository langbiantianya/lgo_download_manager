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
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"lgo_download_manager/internal/ipc"
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

	onExit func() // UI 会话结束后回调（业务侧日志/状态）
}

// New 创建 UI 管理器。
func New(st *store.Store, sc *scheduler.Scheduler) *Manager {
	return &Manager{
		st: st,
		sc: sc,
		onExit: func() {
			log.Println("uimgr: UI session ended; business keeps running")
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
		log.Printf("uimgr: cannot open UI: %v", err)
	}
}

// Start 生成 socket 并拉起 UI 子进程（同一可执行文件的自我复刻）。
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn != nil {
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
	m.uiSock = sock
	m.token = token

	ln, err := ipc.Listen(sock)
	if err != nil {
		return err
	}

	self, err := os.Executable()
	if err != nil {
		ln.Close()
		return fmt.Errorf("uimgr: resolve self executable: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.cmd = exec.Command(self)
	m.cmd.Env = append(os.Environ(),
		ipc.EnvUIChild+"=1",
		ipc.EnvUISocket+"="+sock,
		ipc.EnvUIToken+"="+token,
	)
	m.cmd.Stdout = os.Stdout
	m.cmd.Stderr = os.Stderr
	if err := m.cmd.Start(); err != nil {
		ln.Close()
		cancel()
		m.cmd = nil
		return fmt.Errorf("uimgr: spawn UI child: %w", err)
	}

	// 接受握手（后台，不阻塞业务启动流程）。
	go m.acceptLoop(ln, ctx)
	return nil
}

// acceptLoop 接受一次 UI 子进程连接并驱动会话；进程退出/断开后清理。
func (m *Manager) acceptLoop(ln net.Listener, ctx context.Context) {
	defer ln.Close()
	conn, err := ln.Accept()
	if err != nil {
		m.finishSession()
		return
	}
	ch := ipc.NewConn(conn)
	if err := m.handshake(ch); err != nil {
		log.Printf("uimgr: handshake failed: %v", err)
		ch.Close()
		m.finishSession()
		return
	}

	// 推送初始快照（权威设置）。
	initMsg, _ := ipc.Encode(ipc.InitData{Settings: m.Settings()})
	initMsg.Type = ipc.MsgInit
	if err := ch.Send(initMsg); err != nil {
		ch.Close()
		m.finishSession()
		return
	}

	m.mu.Lock()
	m.conn = ch
	prevCancel := m.cancel
	waitCh := make(chan struct{})
	m.waitCh = waitCh
	m.mu.Unlock()

	// 进程退出监视：UI 进程消亡 → 关闭会话。
	go func() {
		err := m.cmd.Wait()
		log.Printf("uimgr: UI child exited: %v", err)
		prevCancel()
		ch.Close()
		m.finishSession()
	}()

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

// finishSession 清理当前会话状态（conn/cmd/订阅）。
func (m *Manager) finishSession() {
	m.mu.Lock()
	ch := m.conn
	m.conn = nil
	if m.cancel != nil {
		m.cancel()
	}
	if m.uiSock != "" {
		_ = os.Remove(m.uiSock)
	}
	m.cmd = nil
	m.mu.Unlock()
	if ch != nil {
		_ = ch.Close()
	}
	if m.onExit != nil {
		m.onExit()
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
			payload, err := ipc.Encode(ipc.EventData{
				Why:      ev.Why,
				Task:     ev.Task,
				SpeedBPS: ev.SpeedBPS,
			})
			if err != nil {
				continue
			}
			payload.Type = ipc.MsgEvent
			if err := ch.Send(payload); err != nil {
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

// Close 优雅关闭 UI 子进程：先发 MsgClose，等其退出，超时后强杀。
func (m *Manager) Close() {
	m.mu.Lock()
	ch := m.conn
	cmd := m.cmd
	m.mu.Unlock()
	if ch != nil {
		_ = m.send(ipc.Message{Type: ipc.MsgClose})
		// 给 UI 一段窗口期自行退出。
		select {
		case <-time.After(closeGrace):
		case <-m.waitDone():
		}
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	m.finishSession()
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
