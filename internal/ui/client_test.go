// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/ipc"
	"lgo_download_manager/internal/store"
)

// newHandshakedClient 用 unix socket 完成一次完整的 ipc 握手,返回
// 已经就绪的 ipcClient 与对应的业务侧 conn。后台 goroutine 充当
// 业务侧:Accept → 收 Hello → 发 Init。dialAndHandshake 完成即表示
// 握手成功,后续业务消息直接写到 srvConn。
//
// ipc.Dial 内部硬编码 unix socket,所以这里用 UnixListener 而不是 TCP。
func newHandshakedClient(t *testing.T) (*ipcClient, net.Conn) {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "ipc.sock")
	_ = os.Remove(socketPath) // 起手清理可能的残留。
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen unix: %v", err)
	}

	type acceptResult struct {
		c   net.Conn
		err error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		c, err := ln.Accept()
		acceptCh <- acceptResult{c: c, err: err}
	}()

	// 业务侧在 dialAndHandshake 同步 Recv(init) 之前完成 Accept+recv+send,
	// 否则 dialAndHandshake 会卡死。
	srvCh := make(chan net.Conn, 1)
	go func() {
		a := <-acceptCh
		if a.err != nil {
			srvCh <- nil
			return
		}
		srv := ipc.NewConn(a.c)
		if _, err := srv.Recv(); err != nil { // 等 Hello
			srvCh <- nil
			return
		}
		initMsg, err := ipc.Encode(ipc.InitData{Settings: store.Settings{DefaultSaveDir: "/tmp"}})
		if err != nil {
			srvCh <- nil
			return
		}
		initMsg.Type = ipc.MsgInit
		if err := srv.Send(initMsg); err != nil {
			srvCh <- nil
			return
		}
		srvCh <- a.c
	}()

	client, _, err := dialAndHandshake(socketPath, "tok")
	if err != nil {
		_ = ln.Close()
		_ = os.Remove(socketPath)
		t.Fatalf("dialAndHandshake: %v", err)
	}
	srvConn := <-srvCh
	_ = ln.Close() // 已 accept 过 UI 子进程的连接,关掉避免 fd 泄漏。
	_ = os.Remove(socketPath)
	if srvConn == nil {
		client.Close()
		t.Fatalf("biz-side handshake failed")
	}
	_ = test.NewApp() // 让 fyne.CurrentApp() 不为 nil;ShowFromTray 不在本测试覆盖。
	return client, srvConn
}

// sendShowAddTask 从业务侧把 MsgShowAddTask 推给 UI 子进程。
func sendShowAddTask(t *testing.T, srv net.Conn, p ipc.ShowAddTaskParams) {
	t.Helper()
	m, err := ipc.Encode(p)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m.Type = ipc.MsgShowAddTask
	conn := ipc.NewConn(srv)
	if err := conn.Send(m); err != nil {
		t.Fatalf("send: %v", err)
	}
}

// TestIpcClient_ShowAddTaskBufferedBeforeHook 复现冷启动下的丢消息:
// 业务侧 send 早于 RunChild 调到 SetOnShowAddTask,验证 hook 注册时
// 仍能拿到这条消息(队列 + 回放机制)。
//
// 这是上一版 MsgShowAddTask 的回归测试:旧实现直接 nil hook → 静默
// 丢弃,业务进程日志只显示 send 成功但 UI 子进程从不弹框,用户体验是
// 「主窗口拉起来了但对话框没出现」。
func TestIpcClient_ShowAddTaskBufferedBeforeHook(t *testing.T) {
	client, srv := newHandshakedClient(t)

	// 关键:启动 run 之前不要注册 hook,模拟 RunChild 的真实顺序——
	// 先 go c.run(),再 SetOnShowAddTask。run() 与 send 是并发进行的。
	var wg sync.WaitGroup
	wg.Add(1)
	hookCalls := make(chan ipc.ShowAddTaskParams, 4)
	go func() {
		defer wg.Done()
		// 故意延迟 50ms 注册 hook,确保业务侧 send 已经到达并被队列。
		time.Sleep(50 * time.Millisecond)
		client.SetOnShowAddTask(func(p ipc.ShowAddTaskParams) {
			hookCalls <- p
		})
	}()

	go client.run()

	want := ipc.ShowAddTaskParams{
		URL:     "https://example.com/president.iso",
		Name:    "president.iso",
		UA:      "Mozilla/5.0 (lgom-test)",
		Cookies: "session=deadbeef",
	}
	sendShowAddTask(t, srv, want)

	// 等 hook 被调用。SetOnShowAddTask 注册时会同步回放队列,
	// 因此 hookCalls 应该立刻拿到 want。
	select {
	case got := <-hookCalls:
		if got != want {
			t.Fatalf("hook got %+v, want %+v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("hook not called within 2s; buffered MsgShowAddTask was lost")
	}

	wg.Wait()
	client.Close()
}

// TestIpcClient_ShowAddTaskDrainsLatestOnly 验证 hook 未注册时多条
// MsgShowAddTask 到达,只会保留最后一条——同一会话内连续 lgom:// 触发
// 是异常路径,容量 1 + 覆盖策略避免无限堆积;真正逐条处理依赖业务侧
// 顺序排队而不是 UI 端缓存。
func TestIpcClient_ShowAddTaskDrainsLatestOnly(t *testing.T) {
	client, srv := newHandshakedClient(t)

	go client.run()

	first := ipc.ShowAddTaskParams{URL: "https://example.com/first.bin"}
	second := ipc.ShowAddTaskParams{URL: "https://example.com/second.bin"}
	third := ipc.ShowAddTaskParams{URL: "https://example.com/third.bin"}
	sendShowAddTask(t, srv, first)
	sendShowAddTask(t, srv, second)
	sendShowAddTask(t, srv, third)

	// 给 read loop 一点点时间消化三条消息。
	time.Sleep(50 * time.Millisecond)

	got := make(chan ipc.ShowAddTaskParams, 4)
	client.SetOnShowAddTask(func(p ipc.ShowAddTaskParams) { got <- p })

	// 只应回放最后一条。
	select {
	case p := <-got:
		if p.URL != third.URL {
			t.Fatalf("drained got URL %q, want %q (only the last buffered entry should replay)", p.URL, third.URL)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("drain hook not called within 1s")
	}

	// 不能有第二条:再次 select 应超时。
	select {
	case p := <-got:
		t.Fatalf("unexpected extra drain entry: %+v", p)
	case <-time.After(100 * time.Millisecond):
	}

	client.Close()
}

// TestIpcClient_ShowAddTaskDirectDispatch 验证 hook 已经注册后再 send,
// 直接走 hook 不经过队列——这是「主窗口常驻,只是点托盘触发」的常见
// 路径,延迟必须低。
func TestIpcClient_ShowAddTaskDirectDispatch(t *testing.T) {
	client, srv := newHandshakedClient(t)

	got := make(chan ipc.ShowAddTaskParams, 4)
	client.SetOnShowAddTask(func(p ipc.ShowAddTaskParams) { got <- p })
	go client.run()

	want := ipc.ShowAddTaskParams{URL: "https://example.com/x.bin"}
	sendShowAddTask(t, srv, want)

	select {
	case p := <-got:
		if p != want {
			t.Fatalf("got %+v, want %+v", p, want)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("direct-dispatch hook not called within 1s")
	}
	client.Close()
}

// newHandshakedClientWithLang 是 newHandshakedClient 的变体:把 InitData
// 的 Language 字段强制设为指定值,用于验证握手阶段就把权威语言应用到
// ilocale——这是修复「用户在设置里选了英文,每次开窗却显示中文」的
// 回归测试。
func newHandshakedClientWithLang(t *testing.T, lang string) (*ipcClient, net.Conn) {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "ipc.sock")
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen unix: %v", err)
	}

	type acceptResult struct {
		c   net.Conn
		err error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		c, err := ln.Accept()
		acceptCh <- acceptResult{c: c, err: err}
	}()

	srvCh := make(chan net.Conn, 1)
	go func() {
		a := <-acceptCh
		if a.err != nil {
			srvCh <- nil
			return
		}
		srv := ipc.NewConn(a.c)
		if _, err := srv.Recv(); err != nil { // 等 Hello
			srvCh <- nil
			return
		}
		initMsg, err := ipc.Encode(ipc.InitData{
			Settings: store.Settings{DefaultSaveDir: "/tmp", Language: lang},
		})
		if err != nil {
			srvCh <- nil
			return
		}
		initMsg.Type = ipc.MsgInit
		if err := srv.Send(initMsg); err != nil {
			srvCh <- nil
			return
		}
		srvCh <- a.c
	}()

	client, _, err := dialAndHandshake(socketPath, "tok")
	if err != nil {
		_ = ln.Close()
		_ = os.Remove(socketPath)
		t.Fatalf("dialAndHandshake: %v", err)
	}
	srvConn := <-srvCh
	_ = ln.Close()
	_ = os.Remove(socketPath)
	if srvConn == nil {
		client.Close()
		t.Fatalf("biz-side handshake failed")
	}
	_ = test.NewApp()
	return client, srvConn
}

// TestHandshake_AppliesAuthoritativeLanguage 回归测试:dialAndHandshake
// 同步消费 MsgInit 时,必须立即把 InitData.Language 应用到 ilocale。
//
// 之前的 bug 是只在 run() 的 MsgInit case 里调 ilocale.Set,但业务侧
// acceptLoop 只发一次 MsgInit,run() 在下一轮 Recv 上永久阻塞,导致
// 语言永远停在默认 zh-Hans——用户在「设置」里选的语言不生效,UI 每次
// 开窗都被拉回中文。本测试在 run() 尚未启动时校验语言已应用。
func TestHandshake_AppliesAuthoritativeLanguage(t *testing.T) {
	// Reset 到一个未支持的语言,确保 handshake 之后真的被切到 en,
	// 而不是沿用初始状态。
	ilocale.Set("zh-Hans")
	defer ilocale.Set(ilocale.DefaultLanguage)

	client, _ := newHandshakedClientWithLang(t, "en")
	defer client.Close()
	// 此时 c.run() 尚未启动;但 ilocale 必须已是英文。
	if got := ilocale.Current(); got != "en" {
		t.Fatalf("ilocale.Current() = %q after handshake, want %q (language must be applied synchronously)", got, "en")
	}
	// 同时验证翻译也跟着切换——dialAndHandshake 之前的代码路径在
	// NewMainWindow 里会读 ilocale.T 拉标题/工具栏等,这些字符串
	// 必须已经是英文版本。
	if got := ilocale.T("main.window.title"); got == "" || got == "main.window.title" {
		t.Fatalf("T('main.window.title') = %q, want non-empty English translation", got)
	}
	if got := ilocale.T("main.toolbar.new"); got == "新建任务" {
		t.Fatalf("T('main.toolbar.new') still zh-Hans %q after handshake with lang=en", got)
	}
}