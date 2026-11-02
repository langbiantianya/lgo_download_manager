// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package uimgr

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/appservice"
	"lgo_download_manager/internal/rpc"
	addrpkg "lgo_download_manager/internal/rpc/addr"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// newTestService 起一个带真实 store 与调度器的业务服务。
func newTestService(t *testing.T) (*appservice.Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "lgdm.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	sc := scheduler.New(st)
	t.Cleanup(func() {
		sc.CloseBittorrent()
		_ = st.Close()
	})
	return appservice.New(st, sc), st
}

// dialWithRetry 反复连接直到服务就绪。
func dialWithRetry(ctx context.Context, transport, endpoint, token string) (rpc.Client, error) {
	var cli rpc.Client
	var err error
	for i := 0; i < 50; i++ {
		cli, err = rpc.Dial(ctx, transport, endpoint, token)
		if err == nil {
			return cli, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, err
}

// TestSendShowAddTask_LiveSession 验证「弹新建任务对话框」的请求能通过
// 控制消息通道送到 UI 客户端。
//
// 两种传输各跑一遍：接线只在一侧能跑是最容易漏掉的缺陷。
func TestSendShowAddTask_LiveSession(t *testing.T) {
	for _, transport := range []string{rpc.TransportHTTP, rpc.TransportGRPC} {
		t.Run(transport, func(t *testing.T) {
			svc, _ := newTestService(t)

			m := New(svc)
			// 单元测试里不拉起子进程，直接把会话标记为活跃。
			m.mu.Lock()
			m.sessionActive = true
			m.mu.Unlock()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sock := filepath.Join(t.TempDir(), "ui.sock")
			endpoint := "unix:" + sock
			go func() {
				_ = rpc.Serve(ctx, rpc.ServerOptions{
					Transport: transport,
					Addr:      endpoint,
					Token:     "test-token",
				}, svc)
			}()

			cli, err := dialWithRetry(ctx, transport, endpoint, "test-token")
			if err != nil {
				t.Fatalf("连接失败: %v", err)
			}
			defer cli.Close()

			got := make(chan api.ShowAddTaskParams, 1)
			cli.SetOnShowAddTask(func(p api.ShowAddTaskParams) { got <- p })

			// 四个字段全考——旧的 IPC 用例也是全字段断言，只比 URL/Name
			// 会让「UA/Cookies 在接线时被丢掉」这类缺陷漏网。
			want := api.ShowAddTaskParams{
				URL:     "https://example.com/path/to/file.iso",
				Name:    "file.iso",
				UA:      "Mozilla/5.0 (lgom-test)",
				Cookies: "session=deadbeef",
			}
			if err := m.SendShowAddTask(want); err != nil {
				t.Fatalf("SendShowAddTask: %v", err)
			}
			select {
			case p := <-got:
				if p != want {
					t.Fatalf("收到的参数不对:\n got = %+v\nwant = %+v", p, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("show_add_task 没有送达客户端")
			}
		})
	}
}

// TestSendShowAddTask_NoSession_ReturnsError 验证 UI 拉不起来时如实报错——
// main.go 依赖这个错误告诉浏览器「请求没被接收」。
//
// 不直接走 Start() 的拉起路径:在测试里 Start 会自我复刻拉起本测试二进制,
// 造成一串失控的子进程。改为先把 m.cancel 设为非 nil,Start 见到已有
// cancel 会立刻返回「uimgr: UI already running」错误,SendShowAddTask 把
// 这个错误带回去。
func TestSendShowAddTask_NoSession_ReturnsError(t *testing.T) {
	m := New(appservice.New(nil, scheduler.New(nil)))
	m.mu.Lock()
	m.cancel = func() {} // 让 Start() 走 already-running 早退路径
	m.mu.Unlock()

	if err := m.SendShowAddTask(api.ShowAddTaskParams{URL: "https://example.com/x"}); err == nil {
		t.Fatal("没有会话时应当报错")
	}
}

// TestSetLanguage_OnLanguageChangedFires 验证 SetLanguage 在语言真的
// 切换时回调 OnLanguageChanged 钩子,供业务侧同步刷新托盘等非 UI 通道。
// 这是「设置里切语言 → 托盘菜单跟着切」的核心契约。
func TestSetLanguage_OnLanguageChangedFires(t *testing.T) {
	m := New(appservice.New(nil, scheduler.New(nil)))

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

// TestSetLanguage_NoSessionStillFiresHook 验证 SetLanguage 不依赖会话:
// UI 子进程还没起来的场景(冷启动早期),钩子仍要触发,否则托盘永远
// 收不到语言变更通知。本测试不启动会话,直接断言 hook 被调用。
func TestSetLanguage_NoSessionStillFiresHook(t *testing.T) {
	m := New(appservice.New(nil, scheduler.New(nil)))
	var fired bool
	m.OnLanguageChanged(func(lang string) {
		fired = true
	})
	m.SetLanguage("de")
	if !fired {
		t.Fatal("hook not fired when no session is attached")
	}
}

// TestSetLanguage_PushesControlMessage 验证语言变更会推进控制消息通道:
// UI 子进程靠这条消息立即重译,不用等下一次设置往返。
func TestSetLanguage_PushesControlMessage(t *testing.T) {
	svc := appservice.New(nil, scheduler.New(nil))
	m := New(svc)

	m.SetLanguage("en")
	select {
	case msg := <-svc.Controls():
		if msg.Kind != api.MessageKindLanguage {
			t.Fatalf("控制消息类别不对: %+v", msg)
		}
		if msg.Language != "en" {
			t.Fatalf("控制消息语言不对: %+v", msg)
		}
	default:
		t.Fatal("语言变更没有推进控制通道")
	}
}

// TestResolveAddrPrefersEnvOverride 验证监听地址优先取环境变量
// LDM_RPC_LISTEN——命令行 --rpc-listen 就是写进这个变量的。
func TestResolveAddrPrefersEnvOverride(t *testing.T) {
	t.Setenv(EnvRPCListen, "tcp:127.0.0.1:17830")
	m := New(appservice.New(nil, scheduler.New(nil)))
	addr, err := m.resolveAddr()
	if err != nil {
		t.Fatalf("resolveAddr: %v", err)
	}
	if addr != "tcp:127.0.0.1:17830" {
		t.Fatalf("addr = %q, want tcp:127.0.0.1:17830", addr)
	}
}

// TestResolveAddrGeneratesUnixSocket 验证缺省时生成 unix socket 路径,
// 且路径长度控制在 macOS 的 sockaddr_un 上限之内。
func TestResolveAddrGeneratesUnixSocket(t *testing.T) {
	t.Setenv(EnvRPCListen, "")
	m := New(appservice.New(nil, scheduler.New(nil)))
	addr, err := m.resolveAddr()
	if err != nil {
		t.Fatalf("resolveAddr: %v", err)
	}
	if len(addr) <= len("unix:") || addr[:len("unix:")] != "unix:" {
		t.Fatalf("addr = %q, want unix:<path>", addr)
	}
	// macOS 的 sockaddr_un 上限是 104 字节，路径放不下就会 bind 失败。
	if len(addr) > 100 {
		t.Fatalf("addr 过长（%d 字节），macOS 上会超出 sockaddr_un 上限: %q", len(addr), addr)
	}
}

// TestStartRejectsUnknownTransport 验证传输协议名非法时 Start 直接报错:
// 静默回退会让「我明明指定了 http 却跑成 grpc」这类问题很难排查。
func TestStartRejectsUnknownTransport(t *testing.T) {
	t.Setenv(rpc.EnvUITransport, "smoke-signal")
	m := New(appservice.New(nil, scheduler.New(nil)))
	if err := m.Start(); err == nil {
		t.Fatal("非法传输协议应当报错")
	}
	if m.Running() {
		t.Fatal("Start 失败后不应有活跃会话")
	}
}

// TestStartTwiceReturnsError 验证会话已在时不会拉起第二个 UI 子进程。
func TestStartTwiceReturnsError(t *testing.T) {
	m := New(appservice.New(nil, scheduler.New(nil)))
	m.mu.Lock()
	m.cancel = func() {}
	m.mu.Unlock()
	if err := m.Start(); err == nil {
		t.Fatal("已有会话时 Start 应当报错")
	}
}

// TestSpawnAliveDetectsImmediateExit 守住「UI 子进程启动即崩」的检测。
//
// 旧实现拉起 UI 后会等握手（上限 30s），握不上就报错；新架构没有
// 「会话就绪」信号，只靠这个观察窗口。少了它，Fyne 初始化失败时
// main.go 会告诉浏览器「请求已被接收」，对话框却永远不出现。
func TestSpawnAliveDetectsImmediateExit(t *testing.T) {
	m := New(appservice.New(nil, scheduler.New(nil)))
	ch := make(chan struct{})
	m.mu.Lock()
	m.waitCh = ch
	m.mu.Unlock()

	close(ch) // 模拟子进程已经退出（finishSession 会关闭 waitCh）

	if err := m.waitSpawnAlive(); err == nil {
		t.Fatal("子进程立即退出时应当返回错误")
	}
}

// TestSpawnAliveToleratesSlowStart 守住观察窗口的另一半：正常（慢）启动
// 不能被误判成失败，而且确实等满了窗口。
func TestSpawnAliveToleratesSlowStart(t *testing.T) {
	m := New(appservice.New(nil, scheduler.New(nil)))
	m.mu.Lock()
	m.waitCh = make(chan struct{}) // 一直在跑，不关闭
	m.mu.Unlock()

	start := time.Now()
	if err := m.waitSpawnAlive(); err != nil {
		t.Fatalf("慢启动不该被判定为失败: %v", err)
	}
	if elapsed := time.Since(start); elapsed < spawnGrace/2 {
		t.Fatalf("应当等满观察窗口，实际只等了 %v", elapsed)
	}
}

// TestFinishSessionClearsRunning 守住会话生命周期：会话结束后 Running()
// 必须变成 false，否则托盘「显示窗口」会一直以为 UI 还活着，每次点都
// 只推一条消息、什么也不发生，用户只能重启进程。
func TestFinishSessionClearsRunning(t *testing.T) {
	m := New(appservice.New(nil, scheduler.New(nil)))

	m.mu.Lock()
	m.gen++
	gen := m.gen
	m.sessionActive = true
	m.waitCh = make(chan struct{})
	// exitOnce 通常由 Start() 初始化；这里手工补上，模拟一个真实会话。
	m.exitOnce = &sync.Once{}
	closed := make(chan struct{})
	onExit := m.onExit
	m.onExit = func() { close(closed) }
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.onExit = onExit
		m.mu.Unlock()
	}()

	if !m.Running() {
		t.Fatal("前置条件不成立: 会话应为活跃")
	}
	m.finishSession(gen)

	if m.Running() {
		t.Fatal("会话结束后 Running() 仍为 true")
	}
	// onExit 必须触发一次——业务侧靠它打日志/更新托盘状态。
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("会话结束时没有触发 onExit")
	}
	// 重复清理必须是空操作，且不能重复触发 onExit（每次会话一次）。
	m.finishSession(gen)
}

// TestEnvUIConstantsAreTheDocumentedContract 锁定 UI 通道的四个环境变量名。
//
// 它们是业务进程与 UI 子进程之间的**对外约定**：两侧都用常量，改一处
// 会两侧一起变、测试全绿，但外部文档（计划里「UI 通道参数」一节、
// 用户手工设置环境变量）就对不上了。这里用字面量钉死。
func TestEnvUIConstantsAreTheDocumentedContract(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"EnvUIChild", rpc.EnvUIChild, "LDM_UI_CHILD"},
		{"EnvUIListen", rpc.EnvUIListen, "LDM_UI_LISTEN"},
		{"EnvUIToken", rpc.EnvUIToken, "LDM_UI_TOKEN"},
		{"EnvUITransport", rpc.EnvUITransport, "LDM_UI_TRANSPORT"},
		{"EnvRPCListen", EnvRPCListen, "LDM_RPC_LISTEN"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q，期望 %q", c.name, c.got, c.want)
		}
	}
}

// TestWaitListeningWaitsForLateListener 守住「拉子进程之前必须确认监听
// 就绪」。rpc.Serve 在自己的 goroutine 里绑定，而 rpc.Dial 不重试——
// 不等就拉子进程会让它「出生即死」。
//
// 白盒：直接测 waitListening，构造一个延迟才出现的监听者。
func TestWaitListeningWaitsForLateListener(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "late.sock")
	a, err := addrpkg.Parse("unix:" + sock)
	if err != nil {
		t.Fatalf("解析地址: %v", err)
	}

	const delay = 150 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 监听者在 delay 之后才出现，且只接受一次探测连接。
	go func() {
		time.Sleep(delay)
		ln, cleanup, lerr := addrpkg.Listen(a)
		if lerr != nil {
			return
		}
		defer cleanup()
		defer ln.Close()
		if c, aerr := ln.Accept(); aerr == nil {
			_ = c.Close()
		}
		// 保持监听一小会儿，让 waitListening 的返回不依赖对端关闭。
		time.Sleep(200 * time.Millisecond)
	}()

	var lastErr error
	start := time.Now()
	lastErr = waitListening(ctx, a, 3*time.Second)
	elapsed := time.Since(start)

	if lastErr != nil {
		t.Fatalf("waitListening 应当等到监听就绪，实际返回: %v", lastErr)
	}
	if elapsed < delay/2 {
		t.Fatalf("waitListening 返回过快（%v），没有真的等待监听出现", elapsed)
	}
}
