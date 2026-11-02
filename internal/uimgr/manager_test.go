// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package uimgr

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/appservice"
	"lgo_download_manager/internal/rpc"
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

			want := api.ShowAddTaskParams{URL: "https://example.com/x", Name: "x"}
			if err := m.SendShowAddTask(want); err != nil {
				t.Fatalf("SendShowAddTask: %v", err)
			}
			select {
			case p := <-got:
				if p.URL != want.URL || p.Name != want.Name {
					t.Fatalf("收到的参数不对: %+v", p)
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
