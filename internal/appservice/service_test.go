// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package appservice

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/settings"
	"lgo_download_manager/internal/store"
)

func TestCallUnknownMethod(t *testing.T) {
	s := New(nil, nil)
	if err := s.Call(context.Background(), "nope", &api.Empty{}, &api.Empty{}); err == nil {
		t.Fatal("未知方法应当报错")
	}
}

func TestCallListAndSettings(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// list 走的是 scheduler，必须给一个真实的调度器；用同一个 store 支撑。
	s := New(st, scheduler.New(st))
	s.SetSettings(store.Settings{Language: "en"})

	res := &api.ListResult{}
	if err := s.Call(context.Background(), api.MethodList, &api.ListParams{Filter: api.FilterAll, Sort: api.SortCreatedDesc}, res); err != nil {
		t.Fatal(err)
	}
	if res.Tasks == nil {
		t.Fatal("任务列表不应为 nil")
	}

	out := &api.Settings{}
	if err := s.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, out); err != nil {
		t.Fatal(err)
	}
	if out.Language != "en" {
		t.Fatalf("设置读取不对: %+v", out)
	}
}

func TestPushControl(t *testing.T) {
	s := New(nil, nil)
	s.PushControl(api.Message{Kind: api.MessageKindShow})
	select {
	case m := <-s.Controls():
		if m.Kind != api.MessageKindShow {
			t.Fatalf("控制消息不对: %+v", m)
		}
	default:
		t.Fatal("控制消息没有送达")
	}
}

func TestCallStreamMessages(t *testing.T) {
	s := New(nil, nil)
	err := s.Call(context.Background(), api.MethodStreamMessages, &api.Empty{}, &api.Empty{})
	if err == nil {
		t.Fatal("stream_messages 不该走通用分派")
	}
	// 契约里有这个方法，只是走 Events()/Controls() 两条流——不能报成
	// 「未知方法」，否则排障时会误判契约缺方法。
	if strings.Contains(err.Error(), "未知方法") {
		t.Fatalf("错误文案不应把 stream_messages 说成未知方法: %v", err)
	}
}

func TestProbeRespectsContextCancel(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	s := New(st, scheduler.New(st))
	// 直连：避免代理解析引入额外的网络访问。
	s.SetSettings(store.Settings{ProxyMode: protocol.ProxyModeDisabled})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 调用方已经取消

	out := &api.ProbeResult{}
	err = s.Call(ctx, api.MethodProbe, &api.ProbeParams{URL: "http://127.0.0.1:1/x"}, out)
	if err == nil {
		t.Fatal("上下文已取消，探测不该成功")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应当返回 context.Canceled，实际: %v", err)
	}
}

func TestEventsUnsubscribeTwice(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	s := New(st, scheduler.New(st))
	_, unsub := s.Events()
	unsub()
	unsub() // 重复退订不该 panic
}

// waitUntil 轮询直到 cond 为真或超时。
func waitUntil(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", what)
}

// blockingServer 起一个「接受连接但永不返回响应体」的 HTTP 服务，用来把
// 任务固定在 Downloading 状态。
func blockingServer(t *testing.T) string {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1048576")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	return srv.URL + "/payload.bin"
}

// TestSetSettingsAppliesToScheduler 守住「保存设置要立刻生效」这条链路。
//
// 这段逻辑原在 internal/uimgr.Manager.SetSettings 里，Task 9 搬进
// appservice（save_settings 已完全由 appservice 处理）。搬漏了的话，
// 用户在「设置」里把并发上限调大不会让排队任务动起来——纯功能退化，
// 且不会被任何编译错误或既有测试发现。
func TestSetSettingsAppliesToScheduler(t *testing.T) {
	url := blockingServer(t)

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sc := scheduler.New(st)
	svc := New(st, sc)
	// 关键：并发上限只经 svc.SetSettings 推给调度器，不直接调 sc 的 setter。
	svc.SetSettings(store.Settings{MaxConcurrent: 1, Language: "en"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sc.Run(ctx)

	add := func(name string) string {
		t.Helper()
		tk, aerr := sc.Add(scheduler.AddTaskInput{
			URL: url, SavePath: filepath.Join(dir, name),
			Protocol: protocol.ProtoHTTP, ChunkCount: 1,
		})
		if aerr != nil {
			t.Fatalf("sc.Add(%s): %v", name, aerr)
		}
		return tk.ID
	}
	a, b := add("a.bin"), add("b.bin")

	if err := sc.Start(a); err != nil {
		t.Fatalf("Start(a): %v", err)
	}
	waitUntil(t, 5*time.Second, "A 进入准备状态", func() bool { return sc.IsPreparing(a) })
	if err := sc.Start(b); err != nil {
		t.Fatalf("Start(b): %v", err)
	}
	// A 占住唯一 slot，B 必须还在排队。
	// 这里的失败几乎只有一种原因：appservice.SetSettings 没把并发上限
	// 推给调度器，调度器还在用默认值。
	if sc.IsPreparing(b) {
		t.Fatal("cap=1 时 B 已开始——appservice.SetSettings 没有把并发上限推给调度器")
	}

	// 调大并发上限——只能通过 appservice，这才是被测的路径。
	svc.SetSettings(store.Settings{MaxConcurrent: 2, Language: "en"})

	waitUntil(t, 5*time.Second, "调大 cap 后 B 被提升", func() bool { return sc.IsPreparing(b) })
}

// TestSaveSettingsTriggersLanguageCallbackAfterPersist 守住语言变更回调的
// 触发时机：必须在 settings.Save 成功之后回调，且只在语言真的变化时触发。
//
// 回调的消费方是 main.go 注册的 hook（同步刷新 ilocale 与托盘）。若时机
// 提到 Save 之前，hook 读到的持久化语言还是旧值；若误判成「总是触发」，
// 每次保存设置都会重载托盘。
func TestSaveSettingsTriggersLanguageCallbackAfterPersist(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	svc := New(st, scheduler.New(st))
	svc.SetSettings(store.Settings{Language: "en"})

	var got []string
	svc.OnLanguageChanged = func(lang string) {
		// 回调发生时，新语言必须已经落库——这就是「保存成功后触发」的定义。
		persisted, lerr := settings.Load(st, false)
		if lerr != nil {
			t.Errorf("回调里读设置失败: %v", lerr)
			return
		}
		if persisted.Language != lang {
			t.Errorf("回调参数 %q 与已持久化的语言 %q 不一致（回调早于落库）", lang, persisted.Language)
		}
		got = append(got, lang)
	}

	save := func(lang string) {
		t.Helper()
		out := &api.Settings{}
		if serr := svc.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, out); serr != nil {
			t.Fatalf("get_settings: %v", serr)
		}
		out.Language = lang
		if serr := svc.Call(context.Background(), api.MethodSaveSettings, out, &api.Empty{}); serr != nil {
			t.Fatalf("save_settings: %v", serr)
		}
	}

	save("ja")
	if len(got) != 1 || got[0] != "ja" {
		t.Fatalf("首次切语言回调 = %v，期望 [ja]", got)
	}

	save("ja") // 同语言：不该再触发
	if len(got) != 1 {
		t.Fatalf("同语言保存不应触发回调，实际 = %v", got)
	}

	save("de")
	if len(got) != 2 || got[1] != "de" {
		t.Fatalf("再次切语言回调 = %v，期望 [ja de]", got)
	}
}

// TestControlsBufferSurvivesUntilUIConnects 守住控制通道的缓冲语义。
//
// UI 子进程从「被拉起」到「通道建立」之间有一段窗口，业务侧可能在这期间
// 推控制消息（最典型的是 show_add_task）。PushControl 是非阻塞的「满就丢」，
// 所以这个缓冲是消息不丢的唯一保证——把它改小或改成无缓冲，UI 连上来时
// 对话框已经丢了，而调用方仍然收到「已送达」。
func TestControlsBufferSurvivesUntilUIConnects(t *testing.T) {
	s := New(nil, nil)

	// 无消费者时先推满一个缓冲周期的量。容量从通道自身取，而不是写死 16：
	// 写死的话「以后把缓冲调大」会被判成回归，而真正该抓的是「调小 / 无缓冲」。
	n := cap(s.Controls())
	if n <= 0 {
		t.Fatalf("控制通道没有缓冲（cap=%d）——UI 连上来之前推的消息会丢", n)
	}
	for i := 0; i < n; i++ {
		s.PushControl(api.Message{Kind: api.MessageKindShow})
	}
	// 再推一条：缓冲已满，按设计被丢弃（不阻塞、不报错）。
	s.PushControl(api.Message{Kind: api.MessageKindClose})

	// 现在「UI 连上来」逐个消费：应当拿到前 n 条，且顺序不变。
	for i := 0; i < n; i++ {
		select {
		case m := <-s.Controls():
			if m.Kind != api.MessageKindShow {
				t.Fatalf("第 %d 条控制消息类别不对: %+v", i, m)
			}
		default:
			t.Fatalf("第 %d 条丢失——缓冲容量不足 %d", i, n)
		}
	}
	// 第 n+1 条按设计被丢弃，不该留在通道里。
	select {
	case m := <-s.Controls():
		t.Fatalf("超出缓冲的消息应当被丢弃，却读到了 %+v", m)
	default:
	}
}
