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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestServeUnknownMethod 验证 api.AllMethods 之外的方法返回 404。
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
