// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package uimgr

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lgo_download_manager/internal/ipc"
	"lgo_download_manager/internal/store"
)

// fakeConn 用一对 net.Pipe 模拟业务端 socket 与「UI 子进程」之间的
// 半双工字节流。Manager 会拿其中一端做 listen+accept,我们直接作为
// 对端把消息「喂」进去并读出 Manager 发过来的数据。
type fakeConn struct {
	ln      net.Listener
	cliSide net.Conn // 与 Manager 对端(模拟 UI)
	srvSide net.Conn // 由 Manager accept,走它的读循环
}

// newFakeConn 用 TCP loopback 模拟一对已 accept 的连接。简化版
// Listen+Accept,避免拉起真实 Unix socket 路径在 Windows 上的兼容问题。
func newFakeConn(t *testing.T) *fakeConn {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen tcp: %v", err)
	}
	type accepted struct {
		c   net.Conn
		err error
	}
	ch := make(chan accepted, 1)
	go func() {
		c, err := ln.Accept()
		ch <- accepted{c: c, err: err}
	}()
	cli, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		_ = ln.Close()
		t.Fatalf("Dial tcp: %v", err)
	}
	a := <-ch
	if a.err != nil {
		_ = cli.Close()
		_ = ln.Close()
		t.Fatalf("Accept: %v", a.err)
	}
	return &fakeConn{ln: ln, cliSide: cli, srvSide: a.c}
}

func (c *fakeConn) close() {
	if c.srvSide != nil {
		_ = c.srvSide.Close()
	}
	if c.cliSide != nil {
		_ = c.cliSide.Close()
	}
	if c.ln != nil {
		_ = c.ln.Close()
	}
}

// recvShowAddTask 在 cli 侧读取 Manager 发来的第一条 biz.* 消息,断言
// 类型是 MsgShowAddTask 并返回解码后的 ShowAddTaskParams。
func recvShowAddTask(t *testing.T, c net.Conn, timeout time.Duration) ipc.ShowAddTaskParams {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	conn := ipc.NewConn(c)
	msg, err := conn.Recv()
	if err != nil {
		t.Fatalf("recv MsgShowAddTask: %v", err)
	}
	if msg.Type != ipc.MsgShowAddTask {
		t.Fatalf("got msg type %q, want %q", msg.Type, ipc.MsgShowAddTask)
	}
	var p ipc.ShowAddTaskParams
	if err := json.Unmarshal(msg.Data, &p); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	return p
}

// TestSendShowAddTask_ConnLive 验证 UI 已连接时,SendShowAddTask 立刻
// 把参数序列化后推出去;UI 侧能正确解码 URL/Name/UA/Cookies 全字段。
func TestSendShowAddTask_ConnLive(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lgdm.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	defer os.Remove(dbPath)

	m := New(st, nil) // nil scheduler 不会触发 SendShowAddTask 之外的调用
	// 直接把我们伪造的 srvSide 挂到 Manager.conn 上,跳过 Start()/acceptLoop。
	fc := newFakeConn(t)
	defer fc.close()
	m.mu.Lock()
	m.conn = ipc.NewConn(fc.srvSide)
	m.mu.Unlock()

	want := ipc.ShowAddTaskParams{
		URL:     "https://example.com/path/to/file.iso",
		Name:    "file.iso",
		UA:      "Mozilla/5.0 (lgom-test)",
		Cookies: "session=deadbeef",
	}
	if err := m.SendShowAddTask(want); err != nil {
		t.Fatalf("SendShowAddTask: %v", err)
	}

	got := recvShowAddTask(t, fc.cliSide, 2*time.Second)
	if got != want {
		t.Fatalf("recv params = %+v, want %+v", got, want)
	}
}

// TestSendShowAddTask_NoConn_ReturnsError 验证 Manager 没有可用连接
// 时返回错误而非崩溃——调用方据此决定是否回退到旧行为。
//
// 不直接走 Start() 路径:在测试里 Start 会自我复刻拉起本测试二进制,
// 由于缺少 UI 环境变量会立刻退出,即便如此也会引入噪声日志。改为:
// 先把 m.cancel 设为非 nil,Start 见到已有 cancel 会立刻返回
// 「uimgr: UI already running」错误,SendShowAddTask 把这个错误带回去。
func TestSendShowAddTask_NoConn_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lgdm.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	defer os.Remove(dbPath)

	m := New(st, nil)
	m.mu.Lock()
	m.cancel = func() {} // 让 Start() 走 already-running 早退路径
	m.mu.Unlock()

	if err := m.SendShowAddTask(ipc.ShowAddTaskParams{URL: "https://example.com/x"}); err == nil {
		t.Fatalf("SendShowAddTask without conn returned nil error; expected failure")
	}
}

// TestSetLanguage_OnLanguageChangedFires 验证 SetLanguage 在语言真的
// 切换时回调 OnLanguageChanged 钩子,供业务侧同步刷新托盘等非 UI 通道。
// 这是「设置里切语言 → 托盘菜单跟着切」的核心契约。
func TestSetLanguage_OnLanguageChangedFires(t *testing.T) {
	m := New(nil, nil)

	var got []string
	m.OnLanguageChanged(func(lang string) {
		got = append(got, lang)
	})

	// 首次调用:curLang 空 → 任何值都算"变化"。
	m.SetLanguage("en")
	if len(got) != 1 || got[0] != "en" {
		t.Fatalf("after first SetLanguage(en) hook got %v, want [en]", got)
	}
	// 相同语言:不触发。
	m.SetLanguage("en")
	if len(got) != 1 {
		t.Fatalf("after same-lang SetLanguage hook fired %v, want no extra call", got)
	}
	// 切到新语言:触发一次。
	m.SetLanguage("ja")
	if len(got) != 2 || got[1] != "ja" {
		t.Fatalf("after SetLanguage(ja) hook got %v, want [en ja]", got)
	}
}

// TestSetLanguage_NoConnStillFiresHook 验证 SetLanguage 不依赖 conn:
// UI 子进程还没起来的场景(冷启动早期),钩子仍要触发,否则托盘永远
// 收不到语言变更通知。本测试不启动 fake conn,直接断言 hook 被调用。
func TestSetLanguage_NoConnStillFiresHook(t *testing.T) {
	m := New(nil, nil)
	var fired bool
	m.OnLanguageChanged(func(lang string) {
		fired = true
	})
	m.SetLanguage("de")
	if !fired {
		t.Fatalf("hook not fired when no conn is attached")
	}
}
