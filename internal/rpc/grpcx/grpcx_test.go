// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package grpcx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
)

const testToken = "tok-123"

// mockHandler 是测试用的业务侧实现。
type mockHandler struct {
	events   chan api.Event
	controls chan api.Message

	// settings 是 get_settings 的返回值。
	settings api.Settings
	// tasks 是 list 的返回值。
	tasks []*api.Task
	// callErr 非 nil 时 Call 直接返回它。
	callErr error

	// eventsCalled 在服务端进入消息流处理函数时关闭。
	eventsCalled chan struct{}
	eventsOnce   sync.Once

	// unsubed 在服务端的消息流处理函数返回（退订）时关闭。
	unsubed   chan struct{}
	unsubOnce sync.Once
}

func newMockHandler() *mockHandler {
	return &mockHandler{
		events:       make(chan api.Event, 8),
		controls:     make(chan api.Message, 8),
		eventsCalled: make(chan struct{}),
		unsubed:      make(chan struct{}),
	}
}

// newMockHandlerBuf 造一个事件通道缓冲条数可指定的 mockHandler。
//
// 灌缓冲的测试要一次塞进几十条事件，默认的 8 条缓冲不够。
func newMockHandlerBuf(n int) *mockHandler {
	h := newMockHandler()
	h.events = make(chan api.Event, n)
	return h
}

func (m *mockHandler) Call(ctx context.Context, method string, in, out any) error {
	if m.callErr != nil {
		return m.callErr
	}
	switch method {
	case api.MethodGetSettings:
		o, ok := out.(*api.Settings)
		if !ok {
			return errors.New("mock: 响应类型不对")
		}
		*o = m.settings
	case api.MethodList:
		o, ok := out.(*api.ListResult)
		if !ok {
			return errors.New("mock: 响应类型不对")
		}
		o.Tasks = m.tasks
	case api.MethodIsPreparing:
		o, ok := out.(*api.BoolResult)
		if !ok {
			return errors.New("mock: 响应类型不对")
		}
		o.Value = true
	case api.MethodProbe:
		o, ok := out.(*api.ProbeResult)
		if !ok {
			return errors.New("mock: 响应类型不对")
		}
		o.TotalSize = 4096
	case api.MethodBTInfo:
		o, ok := out.(*api.BTDetail)
		if !ok {
			return errors.New("mock: 响应类型不对")
		}
		*o = api.BTDetail{Name: "种子"}
	case api.MethodAdd:
		o, ok := out.(*api.Task)
		if !ok {
			return errors.New("mock: 响应类型不对")
		}
		p, ok := in.(*api.AddTaskInput)
		if !ok {
			return errors.New("mock: 请求类型不对")
		}
		o.ID = "new"
		o.URL = p.URL
		o.SavePath = p.SavePath
		o.Protocol = string(p.Protocol)
		o.Status = api.StatusPending
	case api.MethodStart, api.MethodPause, api.MethodDelete, api.MethodSaveSettings:
		// 没有返回值的方法。
	default:
		return errors.New("mock: 未知方法 " + method)
	}
	return nil
}

func (m *mockHandler) Events() (<-chan api.Event, func()) {
	m.eventsOnce.Do(func() { close(m.eventsCalled) })
	return m.events, func() {
		m.unsubOnce.Do(func() { close(m.unsubed) })
	}
}

func (m *mockHandler) Controls() <-chan api.Message { return m.controls }

// startServer 在临时目录里的 unix socket 上启动服务，返回监听地址。
func startServer(t *testing.T, h Handler, token string) addr.Addr {
	t.Helper()
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "lgdm.sock")}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = Serve(ctx, a, token, h) }()
	// socket 文件在 net.Listen 绑定成功时创建，文件出现即表示能连。
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(a.Address); err == nil {
			return a
		}
		if time.Now().After(deadline) {
			t.Fatalf("服务没有在超时前就绪: %v", a)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// dialClient 连上服务并返回一个客户端。
func dialClient(t *testing.T, a addr.Addr, token string) *Client {
	t.Helper()
	c, err := Dial(context.Background(), a, token)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestEndToEndCallAndStream 是端到端冒烟：真起服务、连一次、调一次、
// 收一条消息。
func TestEndToEndCallAndStream(t *testing.T) {
	h := newMockHandler()
	h.settings = api.Settings{Language: "en", DefaultThreads: 8}
	h.tasks = []*api.Task{{ID: "t1", Status: api.StatusDownloading}}
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	// 一元调用：拿设置。
	out := &api.Settings{}
	if err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, out); err != nil {
		t.Fatalf("get_settings 失败: %v", err)
	}
	if out.Language != "en" || out.DefaultThreads != 8 {
		t.Fatalf("设置不对: %+v", out)
	}

	// 一元调用：列任务。
	list := &api.ListResult{}
	if err := c.Call(context.Background(), api.MethodList,
		&api.ListParams{Filter: api.FilterAll, Sort: api.SortCreatedDesc}, list); err != nil {
		t.Fatalf("list 失败: %v", err)
	}
	if len(list.Tasks) != 1 || list.Tasks[0].ID != "t1" {
		t.Fatalf("任务列表不对: %+v", list.Tasks)
	}

	// 流式调用：收一条事件。
	stream, err := c.Stream(context.Background())
	if err != nil {
		t.Fatalf("打开消息流失败: %v", err)
	}
	h.events <- api.Event{Why: "progress", SpeedBPS: 512}
	select {
	case msg := <-stream:
		if msg.Kind != api.MessageKindEvent || msg.Event == nil {
			t.Fatalf("收到的事件不对: %+v", msg)
		}
		if msg.Event.Why != "progress" || msg.Event.SpeedBPS != 512 {
			t.Fatalf("事件内容不对: %+v", msg.Event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("没有收到事件")
	}
}

// TestClientCloseStopsStreamAndServerStream 验证 Close 之后连接真的断掉：
// 服务端的消息流处理函数必须返回。
func TestClientCloseStopsStreamAndServerStream(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c, err := Dial(context.Background(), a, testToken)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	if _, err := c.Stream(context.Background()); err != nil {
		t.Fatalf("打开消息流失败: %v", err)
	}
	// 等服务端真的进了消息流处理函数，再关客户端。
	select {
	case <-h.eventsCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("服务端没有进入消息流处理函数")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}
	select {
	case <-h.unsubed:
	case <-time.After(5 * time.Second):
		t.Fatal("客户端关闭后，服务端的消息流没有结束")
	}
}

// TestClientCloseIsIdempotent 是 E5 的回归测试。
//
// grpc.ClientConn.Close() 第二次调用返回 ErrClientConnClosing（非 nil），
// 而 rpc.Transport 的契约要求 Close 幂等。
func TestClientCloseIsIdempotent(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	for i := 0; i < 3; i++ {
		if err := c.Close(); err != nil {
			t.Fatalf("第 %d 次 Close 应当返回 nil，实际: %v", i+1, err)
		}
	}
}

// TestClientCallAfterClose 验证关闭之后调用返回错误而不是 panic。
func TestClientCallAfterClose(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)
	if err := c.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}
	out := &api.Settings{}
	err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, out)
	if err == nil {
		t.Fatal("关闭之后的调用应当返回错误")
	}
	if _, err := c.Stream(context.Background()); err == nil {
		t.Fatal("关闭之后的开流应当返回错误")
	}
}

// TestZeroValueClientReturnsError 验证零值客户端回错误而不是 panic。
func TestZeroValueClientReturnsError(t *testing.T) {
	c := &Client{}
	out := &api.Settings{}
	if err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, out); err == nil {
		t.Fatal("零值客户端的调用应当返回错误")
	}
	if _, err := c.Stream(context.Background()); err == nil {
		t.Fatal("零值客户端的开流应当返回错误")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("零值客户端的 Close 应当返回 nil: %v", err)
	}
}

// TestCallRejectsWrongInType 是 E6 的回归测试：类型不对要回错误，不能 panic。
func TestCallRejectsWrongInType(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	// 请求类型不对。
	err := c.Call(context.Background(), api.MethodList, "不是 ListParams", &api.ListResult{})
	if err == nil {
		t.Fatal("请求类型不对时应当返回错误")
	}
	// 响应类型不对。
	err = c.Call(context.Background(), api.MethodList, &api.ListParams{}, "不是 ListResult")
	if err == nil {
		t.Fatal("响应类型不对时应当返回错误")
	}
	// 未知方法。
	err = c.Call(context.Background(), "没有这个方法", &api.Empty{}, &api.Empty{})
	if err == nil {
		t.Fatal("未知方法应当返回错误")
	}
}

// TestServerRejectsBadToken 覆盖一元与流式两条鉴权路径。
func TestServerRejectsBadToken(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, "wrong-token")

	// 一元调用。
	err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{})
	if err == nil {
		t.Fatal("错的 token 应当被拒绝")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("应当是 Unauthenticated，实际: %v", err)
	}

	// 流式调用。服务端流式 RPC 的拒绝要等第一次收消息才浮现，对客户端
	// 表现为「流立刻结束」。另外，服务端的鉴权拦截器在业务处理函数之前
	// 就返回了，所以 mock 的 Events() 压根不会被调用——用它来确认请求
	// 真的被挡在了门外，而不是进了业务层才失败。
	stream, err := c.Stream(context.Background())
	if err == nil {
		select {
		case msg, ok := <-stream:
			if ok {
				t.Fatalf("错的 token 应当被拒绝，却收到了消息: %+v", msg)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("错的 token 应当被拒绝")
		}
	}
	select {
	case <-h.eventsCalled:
		t.Fatal("错的 token 不该进到业务侧的消息流处理函数")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestNonASCIITokenIsRejected 记录 gRPC 传输与 net/http 传输的一个已知
// 差异：gRPC 的 metadata 值只允许可打印 ASCII，带非 ASCII 字符的 token
// 根本发不出去（grpc-go 直接报错），而 httpx 的 Authorization 头收任意
// UTF-8。
//
// 实际部署不受影响：业务侧的握手 token 是 32 字节随机数的十六进制编码
// （见 internal/uimgr 的 randomToken），纯 ASCII。这里把行为钉死，免得
// 以后有人踩到时不知道是哪儿来的。
func TestNonASCIITokenIsRejected(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, "带中文的 token")
	err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{})
	if err == nil {
		t.Fatal("带非 ASCII 字符的 token 应当发不出去")
	}
	if !strings.Contains(err.Error(), "non-printable ASCII") {
		t.Fatalf("应当是 metadata 的可打印 ASCII 限制，实际: %v", err)
	}
}

// TestServerAcceptsGoodToken 确认对的 token 能过。
func TestServerAcceptsGoodToken(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)
	if err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{}); err != nil {
		t.Fatalf("对的 token 应当被接受: %v", err)
	}
}

// TestServerRejectsEmptyToken 确认空 token 也会被拒。
//
// 服务端要求 "Bearer " 后面还有内容（长度必须大于前缀长度），所以空
// token 发过去就是 "Bearer "，一律拒绝。
func TestServerRejectsEmptyToken(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	empty := dialClient(t, a, "")
	err := empty.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{})
	if err == nil {
		t.Fatal("空 token 应当被拒绝")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("应当是 Unauthenticated，实际: %v", err)
	}
}

// TestServeStopsOnContextCancelWithActiveStream 是 E7 的回归测试。
//
// 服务端停机用的是 Stop 而不是 GracefulStop：GracefulStop 会等所有在跑
// 的 RPC 结束，而 StreamMessages 是长连接流，客户端不主动断开它就永远
// 不结束——那样 Serve 会永久阻塞。
func TestServeStopsOnContextCancelWithActiveStream(t *testing.T) {
	h := newMockHandler()
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "lgdm.sock")}
	ctx, cancel := context.WithCancel(context.Background())

	served := make(chan error, 1)
	go func() { served <- Serve(ctx, a, testToken, h) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(a.Address); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("服务没有在超时前就绪")
		}
		time.Sleep(10 * time.Millisecond)
	}

	c, err := Dial(context.Background(), a, testToken)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer c.Close()
	if _, err := c.Stream(context.Background()); err != nil {
		t.Fatalf("打开消息流失败: %v", err)
	}
	// 等服务端真的进了消息流处理函数——这时候 GracefulStop 必然挂死。
	select {
	case <-h.eventsCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("服务端没有进入消息流处理函数")
	}

	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve 应当正常返回，实际: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ctx 取消后 Serve 没有返回（有活跃流时会挂死）")
	}
}

// TestServeStopsOnContextCancelWithoutStream 覆盖没有活跃流的停机。
func TestServeStopsOnContextCancelWithoutStream(t *testing.T) {
	h := newMockHandler()
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "lgdm.sock")}
	ctx, cancel := context.WithCancel(context.Background())

	served := make(chan error, 1)
	go func() { served <- Serve(ctx, a, testToken, h) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(a.Address); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("服务没有在超时前就绪")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve 应当正常返回，实际: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ctx 取消后 Serve 没有返回")
	}
}

// TestStreamMessagesContinuesAfterEventsClosed 是 E4 的回归测试。
//
// 事件通道关闭只停掉那一侧，控制消息流要继续——否则同一套业务侧实现在
// 两种传输下行为不一致。
//
// 测试要绕开一个陷阱：服务端同时盯着 events（已关闭 → 永远就绪）与
// controls（有缓冲消息），两个 case 都 ready 时 Go 随机选一个。所以
// 「关掉事件通道之后立刻发一条控制消息」这种写法是 flaky 的——坏实现
// （整体收手）有一半概率先看到 controls，把消息送出去，测试就误判为通过。
//
// 这里的写法让服务端的状态变化可观测：
//
//  1. 先发第一条控制消息并等它被收到——证明服务端活着、正在正常服务；
//  2. 关闭事件通道；
//  3. 断言消息流**还活着**（坏实现此时已经收手、流已经结束）；
//  4. 再发第二条控制消息，断言它能被收到。
//
// 第 3 步是关键的：它把「服务端已经看到事件通道关闭」这件事变成可观测
// 的事实，服务端处理完关闭之后，第 4 步的第二条消息就只能走 controls 那
// 一侧，坏实现在第 3 步就已经把流收掉了。
func TestStreamMessagesContinuesAfterEventsClosed(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	stream, err := c.Stream(context.Background())
	if err != nil {
		t.Fatalf("打开消息流失败: %v", err)
	}

	// 第一条控制消息：证明服务端在正常服务 select。
	h.controls <- api.Message{Kind: api.MessageKindLanguage, Language: "en"}
	select {
	case msg := <-stream:
		if msg.Kind != api.MessageKindLanguage || msg.Language != "en" {
			t.Fatalf("应当收到第一条控制消息，实际: %+v", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("没有收到第一条控制消息")
	}

	// 事件侧关闭。
	close(h.events)

	// 事件侧关闭之后，流必须还活着：控制侧仍然有消息要送。
	//
	// 这一段等待是必要的：等服务端处理完「事件通道已关闭」这个事实，
	// 后面那条控制消息才不会和它撞在同一个 select 里被随机挑走。
	select {
	case _, ok := <-stream:
		if !ok {
			t.Fatal("事件通道关闭后消息流就结束了（控制侧被一起掐掉了）")
		}
	case <-time.After(200 * time.Millisecond):
		// 流还活着，符合预期。
	}

	// 第二条控制消息：事件侧关闭之后仍然要能送到。
	h.controls <- api.Message{Kind: api.MessageKindShow}
	select {
	case msg := <-stream:
		if msg.Kind != api.MessageKindShow {
			t.Fatalf("应当收到第二条控制消息，实际: %+v", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("事件通道关闭后控制消息流没有继续")
	}
}

// TestStreamMessagesStopsAfterBothSidesClosed 覆盖两侧都关闭的情形。
func TestStreamMessagesStopsAfterBothSidesClosed(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	stream, err := c.Stream(context.Background())
	if err != nil {
		t.Fatalf("打开消息流失败: %v", err)
	}
	close(h.events)
	close(h.controls)
	// 两侧都关掉之后，服务端收手，客户端的流会结束。
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case _, ok := <-stream:
			if !ok {
				return
			}
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("两侧都关闭之后消息流没有结束")
		}
	}
}

// TestDialFailsWhenServerAbsent 覆盖 E2：预连接让「业务进程没起来」在
// Dial 阶段就暴露，而不是等到第一次调用。
func TestDialFailsWhenServerAbsent(t *testing.T) {
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "missing.sock")}
	if _, err := Dial(context.Background(), a, testToken); err == nil {
		t.Fatal("服务没起来时 Dial 应当失败")
	}
}

// TestDialRespectsContext 覆盖 E2：ctx 不是死参数。
func TestDialRespectsContext(t *testing.T) {
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "missing.sock")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Dial(ctx, a, testToken); err == nil {
		t.Fatal("ctx 取消之后 Dial 应当失败")
	}
}

// TestServerPropagatesBusinessError 确认业务侧的错误能传到客户端。
func TestServerPropagatesBusinessError(t *testing.T) {
	h := newMockHandler()
	h.callErr = errors.New("业务侧出错了")
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{})
	if err == nil {
		t.Fatal("业务侧的错误应当传回客户端")
	}
	if !strings.Contains(err.Error(), "业务侧出错了") {
		t.Fatalf("错误里应当带上业务侧的原因，实际: %v", err)
	}
}

// TestClientCloseStopsBlockedStreamGoroutine 是 I2 的回归测试。
//
// 消息流的 goroutine 可能阻塞在往缓冲通道发送上：缓冲 64 条满了、调用方
// 消费不过来，它就卡在 out <- msg 上。这时 conn.Close() 只会让流的下一次
// Recv 报错——goroutine 根本不在 Recv 里——select 的 ctx.Done() 分支也
// 不会触发，因为 ctx 是调用方传进来的，Close 从不取消它。于是这个
// goroutine 永久泄漏。
//
// 修法是 Close 先取消所有活动流的 ctx，所以这里断言：Close 之后，卡在
// 发送上的流 goroutine 都要退出。
func TestClientCloseStopsBlockedStreamGoroutine(t *testing.T) {
	before := countStreamGoroutines()
	for i := 0; i < 5; i++ {
		// 事件通道的缓冲要够大，一次把 64 条缓冲灌满。
		h := newMockHandlerBuf(streamBuffer + 8)
		a := startServer(t, h, testToken)
		c, err := Dial(context.Background(), a, testToken)
		if err != nil {
			t.Fatalf("连接失败: %v", err)
		}
		stream, err := c.Stream(context.Background())
		if err != nil {
			t.Fatalf("打开消息流失败: %v", err)
		}
		// 多灌一条：缓冲填满之后，客户端的 goroutine 就卡在发送上了。
		for j := 0; j < streamBuffer+1; j++ {
			h.events <- api.Event{Why: "fill"}
		}
		waitFor(t, "客户端的消息缓冲没有被填满", func() bool {
			return len(stream) == streamBuffer
		})
		// 给第 65 条留出到达客户端的时间：到了之后 goroutine 就卡在
		// out <- 上（缓冲已满），这正是会泄漏的姿势。
		time.Sleep(100 * time.Millisecond)
		if err := c.Close(); err != nil {
			t.Fatalf("关闭失败: %v", err)
		}
	}
	waitFor(t, "Close 之后还有残留的流 goroutine", func() bool {
		return countStreamGoroutines() <= before
	})
}

// TestStreamAfterCloseReturnsError 验证关闭之后开流立刻回错误。
func TestStreamAfterCloseReturnsError(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)
	if err := c.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}
	if _, err := c.Stream(context.Background()); err == nil {
		t.Fatal("关闭之后的开流应当返回错误")
	}
}

// TestDialFailsWithinTimeoutWhenServerAbsent 是 E1 的回归测试。
//
// 服务端没在监听时，带超时的 ctx 必须让 Dial 在超时内返回错误，而不是
// 挂住：建连要跟着 ctx 走，不能一路挂到操作系统的 TCP 超时。
func TestDialFailsWithinTimeoutWhenServerAbsent(t *testing.T) {
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "missing.sock")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	if _, err := Dial(ctx, a, testToken); err == nil {
		t.Fatal("服务没起来时 Dial 应当失败")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("Dial 挂住了，没有跟着 ctx 的超时走: %v", elapsed)
	}
}

// TestDialerRespectsContext 钉住 gRPC 的 dialer 必须用 gRPC 传进来的 ctx
// 建连。
//
// 少了 ctx 的话建连不会跟着调用方的超时走：一次超时的调用会留下一个还在
// 建连的 socket goroutine，「服务端没起来」时调用方拿到的超时也比预期晚。
// 这里用一个已经取消的 ctx 建连：走 ctx 的实现回的是 ctx 的错，丢掉 ctx
// 的实现会真的去连一次，回的是连接错误。
func TestDialerRespectsContext(t *testing.T) {
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "missing.sock")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := dialer(a)(ctx, "")
	if err == nil {
		t.Fatal("ctx 取消之后建连应当失败")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("建连应当跟着 ctx 取消走，实际: %v", err)
	}
}

// TestCallRejectsWrongTypesForEveryMethod 是 E6 的回归测试。
//
// Call 按方法分派，每个方法各自校验入参与出参的类型。漏掉任何一处
// 检查，类型不对就会变成「静默的成功」——调用方看不出自己传错了类型，
// 错误要到业务逻辑不对的时候才暴露。
//
// 这里把每个方法的两种类型错误都过一遍。
func TestCallRejectsWrongTypesForEveryMethod(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	cases := []struct {
		method string
		in     any
		out    any
	}{
		{api.MethodList, &api.ListParams{}, &api.ListResult{}},
		{api.MethodAdd, &api.AddTaskInput{}, &api.Task{}},
		{api.MethodStart, &api.TaskRef{}, &api.Empty{}},
		{api.MethodPause, &api.TaskRef{}, &api.Empty{}},
		{api.MethodDelete, &api.TaskRef{}, &api.Empty{}},
		{api.MethodIsPreparing, &api.TaskRef{}, &api.BoolResult{}},
		{api.MethodProbe, &api.ProbeParams{}, &api.ProbeResult{}},
		{api.MethodGetSettings, &api.Empty{}, &api.Settings{}},
		{api.MethodSaveSettings, &api.Settings{}, &api.Empty{}},
		{api.MethodBTInfo, &api.BTInfoParams{}, &api.BTDetail{}},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			// 入参类型不对。
			err := c.Call(context.Background(), tc.method, "错的类型", tc.out)
			if err == nil {
				t.Error("入参类型不对时应当返回错误")
			}
			// 出参类型不对。
			err = c.Call(context.Background(), tc.method, tc.in, "错的类型")
			if err == nil {
				t.Error("出参类型不对时应当返回错误")
			}
		})
	}
}

// waitFor 反复检查 cond，直到它成立或超时。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// countStreamGoroutines 统计还活着的消息流 goroutine 数量。
//
// 靠栈里的函数名定位，不看 runtime.NumGoroutine 的总数：gRPC 自己的后台
// goroutine 什么时候退出不受本测试控制，算进总数里就是噪声。
func countStreamGoroutines() int {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, block := range strings.Split(string(buf), "\n\ngoroutine ") {
		if strings.Contains(block, "grpcx.(*Client).Stream") {
			count++
		}
	}
	return count
}
