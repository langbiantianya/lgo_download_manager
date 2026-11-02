// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package httpx

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
)

// stubHandler 是最小业务侧实现。
type stubHandler struct {
	events   chan api.Event
	controls chan api.Message

	// unsubed 在退订时被关闭，用来观察服务端有没有收手。
	unsubed   chan struct{}
	unsubOnce sync.Once
}

func (s *stubHandler) Call(ctx context.Context, method string, in, out any) error {
	switch method {
	case api.MethodGetSettings:
		out.(*api.Settings).Language = "en"
	case api.MethodList:
		out.(*api.ListResult).Tasks = []*api.Task{{ID: "t1"}}
	case api.MethodIsPreparing:
		out.(*api.BoolResult).Value = true
	case api.MethodAdd:
		t := out.(*api.Task)
		t.ID = "new"
		t.URL = in.(*api.AddTaskInput).URL
	}
	return nil
}

func (s *stubHandler) Events() (<-chan api.Event, func()) {
	return s.events, func() {
		s.unsubOnce.Do(func() {
			if s.unsubed != nil {
				close(s.unsubed)
			}
		})
	}
}
func (s *stubHandler) Controls() <-chan api.Message { return s.controls }

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

func TestValidToken(t *testing.T) {
	if !validToken("Bearer secret", "secret") {
		t.Fatal("正确的 token 应当通过")
	}
	if validToken("Bearer wrong", "secret") {
		t.Fatal("错误的 token 应当被拒")
	}
	if validToken("secret", "secret") {
		t.Fatal("缺少 Bearer 前缀应当被拒")
	}
	if validToken("Bearer ", "secret") {
		t.Fatal("空 token 应当被拒")
	}
}

func TestServeCallAndAuth(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	mux := newMux(h, "secret")

	// 无 token → 401
	req := httptest.NewRequest(http.MethodPost, servicePrefix+api.MethodGetSettings, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("缺 token 应当 401, got %d", rec.Code)
	}

	// 错误 token → 401
	req = httptest.NewRequest(http.MethodPost, servicePrefix+api.MethodGetSettings, nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误 token 应当 401, got %d", rec.Code)
	}

	// 带 token → 200 + 设置
	req = httptest.NewRequest(http.MethodPost, servicePrefix+api.MethodGetSettings, nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("应当 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var s api.Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Language != "en" {
		t.Fatalf("设置不对: %+v", s)
	}
}

// TestServeRejectsInvalidJSON 验证坏请求体返回 400。
func TestServeRejectsInvalidJSON(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	mux := newMux(h, "secret")

	req := httptest.NewRequest(http.MethodPost, servicePrefix+api.MethodList, strings.NewReader("{"))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("坏 JSON 应当 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var e struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("错误体不是 JSON: %v (%s)", err, rec.Body.String())
	}
	if e.OK || e.Error == "" {
		t.Fatalf("错误体形状不对: %s", rec.Body.String())
	}
}

// TestServeUnknownMethod 验证 api.AllMethods 之外的方法返回 404，且错误体
// 形状与业务端点一致（{"ok":false,"error":"..."}）。
func TestServeUnknownMethod(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	mux := newMux(h, "secret")

	req := httptest.NewRequest(http.MethodPost, servicePrefix+"nope", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知方法应当 404, got %d", rec.Code)
	}
	assertErrorBody(t, rec, "未知路径")
}

// TestServeUnknownPathNeedsToken 验证未注册的路径也要过鉴权：没 token 时回
// 401，而不是 ServeMux 内置的纯文本 404。
//
// 这一条守住「不该让没 token 的调用方知道某个端点存不存在」：没有它，
// 401 与 404 的差别会让任何调用方枚举出端点。
func TestServeUnknownPathNeedsToken(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	mux := newMux(h, "secret")

	// 完全不带 token。
	req := httptest.NewRequest(http.MethodPost, "/nope", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未知路径不带 token 应当 401, got %d: %s", rec.Code, rec.Body.String())
	}
	assertErrorBody(t, rec, "无效的 token")

	// 带错误的 token。
	req = httptest.NewRequest(http.MethodPost, "/nope", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未知路径带错 token 应当 401, got %d: %s", rec.Code, rec.Body.String())
	}
	assertErrorBody(t, rec, "无效的 token")
}

// TestServeUnknownPathWithToken 验证带对 token 时未知路径回 404，且错误体
// 是统一的 JSON 形状。
func TestServeUnknownPathWithToken(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	mux := newMux(h, "secret")

	req := httptest.NewRequest(http.MethodPost, "/nope", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知路径带对 token 应当 404, got %d: %s", rec.Code, rec.Body.String())
	}
	assertErrorBody(t, rec, "未知路径 /nope")
}

// TestServeFallbackRouteKeepsDocsEndpointsOpen 验证兜底路由没有把文档端点
// 圈进去：/openapi.yaml、/openapi.json、/docs 仍按设计不参与 token 校验。
//
// 兜底路由是 "/"（兜底子树），文档端点是更精确的 pattern，更精确的优先
// 命中。这条测试守着这个前提——哪天有人把兜底路由改成「所有请求都先过
// 鉴权」的整体包装，文档页就跟着挂。
func TestServeFallbackRouteKeepsDocsEndpointsOpen(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	mux := newMux(h, "secret")

	for _, path := range []string{"/openapi.yaml", "/openapi.json", "/docs"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 不带 token 应当 200, got %d", path, rec.Code)
		}
	}
}

// TestServeRejectsNonPost 验证只接受 POST。
func TestServeRejectsNonPost(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	mux := newMux(h, "secret")

	req := httptest.NewRequest(http.MethodGet, servicePrefix+api.MethodGetSettings, nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应当 405, got %d", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow 头不对: %q", got)
	}
}

// TestServeStream 用真实 HTTP 服务验证 NDJSON 逐行的线格式。
//
// 这里不用 httptest.NewRecorder：它的 Body 是 *bytes.Buffer，不是并发安全的，
// 在 handler 还在写的时候读会触发数据竞争。
func TestServeStream(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event, 4), controls: make(chan api.Message, 4)}
	srv := httptest.NewServer(newMux(h, "secret"))
	defer srv.Close()

	resp := openStream(t, srv.URL)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应当 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("Content-Type 不对: %q", ct)
	}

	sc := bufio.NewScanner(resp.Body)

	// 先推事件，客户端读到之后再推控制消息——两个通道同时可读时服务端
	// 选中哪个是不确定的，测试不能依赖顺序。
	h.events <- api.Event{Why: "progress"}
	msg := scanMessage(t, sc)
	if msg.Kind != api.MessageKindEvent || msg.Event == nil {
		t.Fatalf("事件消息不对: %+v", msg)
	}
	if msg.Event.Why != "progress" {
		t.Fatalf("事件内容不对: %+v", msg.Event)
	}

	h.controls <- api.Message{Kind: api.MessageKindShow}
	msg = scanMessage(t, sc)
	if msg.Kind != api.MessageKindShow {
		t.Fatalf("控制消息不对: %+v", msg)
	}
}

// TestServeStreamContinuesAfterEventsClosed 验证事件流关闭后控制消息仍然
// 推送（事件通道结束不该顺带掐掉控制通道）。
func TestServeStreamContinuesAfterEventsClosed(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message, 4)}
	close(h.events)
	srv := httptest.NewServer(newMux(h, "secret"))
	defer srv.Close()

	resp := openStream(t, srv.URL)
	defer resp.Body.Close()

	sc := bufio.NewScanner(resp.Body)
	h.controls <- api.Message{Kind: api.MessageKindShow}
	msg := scanMessage(t, sc)
	if msg.Kind != api.MessageKindShow {
		t.Fatalf("控制消息不对: %+v", msg)
	}
}

// TestServeStreamStopsOnClientDisconnect 验证客户端断开后服务端收手：
// 请求 ctx 取消后消息流处理器要退出并退订事件。
func TestServeStreamStopsOnClientDisconnect(t *testing.T) {
	h := &stubHandler{
		events:   make(chan api.Event),
		controls: make(chan api.Message),
		unsubed:  make(chan struct{}),
	}
	srv := httptest.NewServer(newMux(h, "secret"))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+servicePrefix+api.MethodStreamMessages, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// 断开连接：服务端的请求 ctx 随之取消。
	cancel()
	resp.Body.Close()

	select {
	case <-h.unsubed:
	case <-time.After(5 * time.Second):
		t.Fatal("客户端断开后服务端没有退出消息流")
	}
}

// TestClientCallRoundTrip 用真实服务验证一次调用的往返。
func TestClientCallRoundTrip(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	a := startServer(t, h, "secret")

	ctx := context.Background()
	c, err := Dial(ctx, a, "secret")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	out := &api.Settings{}
	if err := c.Call(ctx, api.MethodGetSettings, &api.Empty{}, out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.Language != "en" {
		t.Fatalf("设置不对: %+v", out)
	}

	list := &api.ListResult{}
	if err := c.Call(ctx, api.MethodList, &api.ListParams{}, list); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(list.Tasks) != 1 || list.Tasks[0].ID != "t1" {
		t.Fatalf("列表不对: %+v", list.Tasks)
	}
}

// TestClientCallRejectsBadToken 验证 token 不对时调用失败。
func TestClientCallRejectsBadToken(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	a := startServer(t, h, "secret")

	c, err := Dial(context.Background(), a, "wrong")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	err = c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{})
	if err == nil {
		t.Fatal("错误 token 应当调用失败")
	}
	if !strings.Contains(err.Error(), "无效的 token") {
		t.Fatalf("错误信息应当带上服务端的原因: %v", err)
	}
}

// TestClientCallUnknownMethodKeepsErrorReason 验证客户端能从 404 的响应体
// 里读出错误原因。服务端的错误体形状固定为 {"ok":false,"error":"..."}，
// 客户端按 error 字段解析——解析不出来就退化成裸串「HTTP 404」。
func TestClientCallUnknownMethodKeepsErrorReason(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	a := startServer(t, h, "secret")

	c, err := Dial(context.Background(), a, "secret")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	err = c.Call(context.Background(), "nope", &api.Empty{}, &api.Empty{})
	if err == nil {
		t.Fatal("未知方法应当调用失败")
	}
	if !strings.Contains(err.Error(), "未知路径") {
		t.Fatalf("错误信息应当带上服务端的原因: %v", err)
	}
	if strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("错误原因不该退化成裸状态码: %v", err)
	}
}

// countingRoundTripper 记录自己被调用的次数。
//
// 它是「假」的：任何走到这里的请求都直接失败。类型校验如果漏了，测试会
// 同时看到「发了请求」与「错误信息不是类型错误」两个信号。
type countingRoundTripper struct {
	calls atomic.Int64
}

// RoundTrip 记录一次调用并返回错误。
func (rt *countingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	rt.calls.Add(1)
	return nil, errors.New("不该发出请求")
}

// wrongCallType 是一个不属于任何方法规格的类型，用来模拟「调用方传错了
// Go 类型」。它的字段刻意为空：只要 Go 类型不是规格里的那个，就算错。
type wrongCallType struct{}

// TestCallTypeMismatchDoesNotSendRequest 验证类型校验失败时客户端压根不发
// 请求。
//
// 这是 httpx 侧的护栏。JSON 会把类型信息擦掉：入参类型不对时，服务端照样
// 会按规格解码出一个零值对象；出参类型不对时，服务端的响应会被解码进一个
// 形状不合的对象里。两种情况都是「静默成功」，调用方看不出自己传错了
// 类型。所以校验必须发生在序列化之前——请求一旦发出去，错误就被吞掉了。
//
// 断言两件事：错误里带着方法名（方便定位是哪个方法传错了），以及假的
// RoundTripper 一次都没被调用。
func TestCallTypeMismatchDoesNotSendRequest(t *testing.T) {
	rt := &countingRoundTripper{}
	c := &Client{http: &http.Client{Transport: rt}}
	ctx := context.Background()

	for _, m := range api.AllMethods {
		t.Run(m, func(t *testing.T) {
			spec := api.Spec(m)
			if spec == nil {
				t.Fatalf("方法 %q 缺少规格", m)
			}

			// 入参类型不对（出参给对）：必须报「请求类型错误」。
			err := c.Call(ctx, m, &wrongCallType{}, spec.NewOut())
			if err == nil {
				t.Fatal("入参类型不对应当报错")
			}
			if !strings.Contains(err.Error(), "请求类型错误") {
				t.Fatalf("应当报请求类型错误: %v", err)
			}
			if !strings.Contains(err.Error(), m) {
				t.Fatalf("错误信息应当带上方法名 %q: %v", m, err)
			}

			// 出参类型不对（入参给对）：必须报「响应类型错误」。
			err = c.Call(ctx, m, spec.NewIn(), &wrongCallType{})
			if err == nil {
				t.Fatal("出参类型不对应当报错")
			}
			if !strings.Contains(err.Error(), "响应类型错误") {
				t.Fatalf("应当报响应类型错误: %v", err)
			}
			if !strings.Contains(err.Error(), m) {
				t.Fatalf("错误信息应当带上方法名 %q: %v", m, err)
			}
		})
	}

	if n := rt.calls.Load(); n != 0 {
		t.Fatalf("类型校验失败时不该发出请求，实际发了 %d 次", n)
	}
}

// TestCallUnknownMethodDoesNotPanic 验证未知方法返回错误而不是 panic。
//
// api.Spec 对未知方法返回 nil，校验逻辑不能因此解引用 nil。未知方法由
// 服务端判（兜底路由回 404），这里用假 RoundTripper 代替服务端：客户端
// 要么报错，要么把请求发出去让服务端拒——两种都不许 panic。
func TestCallUnknownMethodDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("未知方法不该 panic: %v", r)
		}
	}()

	rt := &countingRoundTripper{}
	c := &Client{http: &http.Client{Transport: rt}}
	err := c.Call(context.Background(), "no_such_method", &api.Empty{}, &api.Empty{})
	if err == nil {
		t.Fatal("未知方法应当返回错误")
	}
	if !strings.Contains(err.Error(), "no_such_method") {
		t.Fatalf("错误信息应当带上方法名: %v", err)
	}
}

// TestZeroValueClientReturnsError 验证零值客户端的 Call 与 Stream 返回错误
// 而不是 panic。
//
// Client 是导出类型，零值是被允许的构造物（contract_test.go 就用它验证
// Close 不炸）。零值客户端的 http 字段是 nil，直接 Do 会 nil 指针解引用。
func TestZeroValueClientReturnsError(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("零值客户端不该 panic: %v", r)
		}
	}()

	c := &Client{}
	err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{})
	if err == nil {
		t.Fatal("零值客户端的 Call 应当返回错误")
	}
	if !strings.Contains(err.Error(), "未初始化") {
		t.Fatalf("错误信息应当说明客户端未初始化: %v", err)
	}

	ch, err := c.Stream(context.Background())
	if err == nil {
		t.Fatal("零值客户端的 Stream 应当返回错误")
	}
	if !strings.Contains(err.Error(), "未初始化") {
		t.Fatalf("错误信息应当说明客户端未初始化: %v", err)
	}
	if ch != nil {
		t.Fatal("零值客户端的 Stream 不该返回消息通道")
	}
}

// TestClientStreamReceivesMessages 验证客户端能收到事件与控制消息两种。
func TestClientStreamReceivesMessages(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event, 4), controls: make(chan api.Message, 4)}
	a := startServer(t, h, "secret")

	c, err := Dial(context.Background(), a, "secret")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	ch, err := c.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	h.events <- api.Event{Why: "progress", SpeedBPS: 10}
	msg := recvMessage(t, ch)
	if msg.Kind != api.MessageKindEvent || msg.Event == nil {
		t.Fatalf("事件消息不对: %+v", msg)
	}
	if msg.Event.Why != "progress" || msg.Event.SpeedBPS != 10 {
		t.Fatalf("事件内容不对: %+v", msg.Event)
	}

	h.controls <- api.Message{Kind: api.MessageKindShow}
	msg = recvMessage(t, ch)
	if msg.Kind != api.MessageKindShow {
		t.Fatalf("控制消息不对: %+v", msg)
	}
}

// TestClientCloseIsIdempotent 验证 Close 可以重复调用。
func TestClientCloseIsIdempotent(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	a := startServer(t, h, "secret")

	c, err := Dial(context.Background(), a, "secret")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("第一次 Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("第二次 Close: %v", err)
	}
}

// TestClientCloseStopsStream 验证 Close 能停掉在跑的 NDJSON 流——只关空闲
// 连接是不够的，读循环会一直挂着。
func TestClientCloseStopsStream(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	a := startServer(t, h, "secret")

	c, err := Dial(context.Background(), a, "secret")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	ch, err := c.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("Close 之后不该再收到消息")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close 之后消息流没有结束")
	}
}

// TestDialFailsWhenServerAbsent 验证服务端没起来时 Dial 直接失败，而不是
// 等到第一次调用才炸。
func TestDialFailsWhenServerAbsent(t *testing.T) {
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "missing.sock")}
	if _, err := Dial(context.Background(), a, "secret"); err == nil {
		t.Fatal("服务端没起来时 Dial 应当失败")
	}
}

// assertErrorBody 断言响应是统一的错误信封：Content-Type 是 application/json，
// body 是 {"ok":false,"error":"..."}。wantContains 非空时，error 字段还要包含
// 它。
func assertErrorBody(t *testing.T, rec *httptest.ResponseRecorder, wantContains string) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("错误体的 Content-Type 不对: %q", ct)
	}
	var e struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("错误体不是 JSON: %v (%s)", err, rec.Body.String())
	}
	if e.OK {
		t.Fatalf("错误体的 ok 应当是 false: %s", rec.Body.String())
	}
	if e.Error == "" {
		t.Fatalf("错误体缺少 error 字段: %s", rec.Body.String())
	}
	if wantContains != "" && !strings.Contains(e.Error, wantContains) {
		t.Fatalf("错误信息应当包含 %q，实际是 %q", wantContains, e.Error)
	}
}

// openStream 打开一条消息流。
func openStream(t *testing.T, base string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+servicePrefix+api.MethodStreamMessages, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Accept", "application/x-ndjson")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// scanMessage 从 NDJSON 响应里读一行并解析成 api.Message。
func scanMessage(t *testing.T, sc *bufio.Scanner) api.Message {
	t.Helper()
	if !sc.Scan() {
		t.Fatalf("消息流没有输出: %v", sc.Err())
	}
	var msg api.Message
	if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
		t.Fatalf("消息不是合法 JSON: %v (%s)", err, sc.Text())
	}
	return msg
}

// recvMessage 从客户端的消息流里收一条消息。
func recvMessage(t *testing.T, ch <-chan api.Message) api.Message {
	t.Helper()
	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("消息流提前结束")
		}
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("没有收到消息")
	}
	return api.Message{}
}
