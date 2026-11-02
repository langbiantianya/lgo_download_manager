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
func TestStreamMessagesContinuesAfterEventsClosed(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	stream, err := c.Stream(context.Background())
	if err != nil {
		t.Fatalf("打开消息流失败: %v", err)
	}
	// 事件侧关闭。
	close(h.events)
	// 控制侧仍然有消息。
	h.controls <- api.Message{Kind: api.MessageKindLanguage, Language: "en"}

	select {
	case msg := <-stream:
		if msg.Kind != api.MessageKindLanguage || msg.Language != "en" {
			t.Fatalf("应当收到控制消息，实际: %+v", msg)
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
