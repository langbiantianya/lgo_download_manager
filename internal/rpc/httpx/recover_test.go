// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lgo_download_manager/internal/api"
)

// panicHandler 在指定方法上 panic，模拟业务侧的一个 bug。
type panicHandler struct {
	stubHandler
	panicMethod string
}

func (p *panicHandler) Call(ctx context.Context, method string, in, out any) error {
	if method == p.panicMethod {
		panic("业务侧的 bug")
	}
	return p.stubHandler.Call(ctx, method, in, out)
}

// TestHandlerPanicBecomes500 确认 handler 的 panic 被中间件兜成 500，
// 而不是让连接被 net/http 直接掐断。
//
// net/http 自带 recover，但它的处理是「关掉连接」：客户端只看到 EOF 或
// 连接重置，拿不到错误体，只能自己猜「服务端崩了」。gRPC 那边用拦截器
// 把 panic 转成 Internal 错误——两种传输在这件事上应当对齐：客户端拿到
// 的是一个明确的「服务端内部错误」。
func TestHandlerPanicBecomes500(t *testing.T) {
	h := &panicHandler{panicMethod: api.MethodGetSettings}
	mux := recoverMiddleware(newMux(h, "tok"))

	req := httptest.NewRequest(http.MethodPost, servicePrefix+api.MethodGetSettings, nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码应当是 500，实际 %d（body: %s）", rec.Code, rec.Body.String())
	}
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("错误体不是 JSON: %v（body: %s）", err, rec.Body.String())
	}
	if body.OK {
		t.Fatal("错误体的 ok 应当是 false")
	}
	if body.Error == "" {
		t.Fatal("错误体应当带出错原因")
	}
}

// TestPanicDoesNotBreakOtherRequests 确认一次 panic 之后服务端还能继续
// 服务别的请求——panic 只影响那一个请求。
func TestPanicDoesNotBreakOtherRequests(t *testing.T) {
	h := &panicHandler{panicMethod: api.MethodGetSettings}
	mux := recoverMiddleware(newMux(h, "tok"))

	post := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := post(servicePrefix + api.MethodGetSettings); rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic 的那次应当是 500，实际 %d", rec.Code)
	}
	rec := post(servicePrefix + api.MethodList)
	if rec.Code != http.StatusOK {
		t.Fatalf("panic 之后别的请求应当照常成功，实际 %d（body: %s）", rec.Code, rec.Body.String())
	}
}

// TestGuardWriterKeepsFlusher 确认中间件包了一层 ResponseWriter 之后，
// 流式端点仍然拿得到 http.Flusher。
//
// serveStream 靠 w.(http.Flusher) 逐条刷 NDJSON；包装 ResponseWriter 时
// 少实现 Flush，流式端点就会退化成 500「响应不支持流式输出」——这类
// 回归不会让任何用例变红，只会安静地让消息流失效。
func TestGuardWriterKeepsFlusher(t *testing.T) {
	gw := &guardWriter{ResponseWriter: httptest.NewRecorder()}
	if _, ok := any(gw).(http.Flusher); !ok {
		t.Fatal("guardWriter 必须实现 http.Flusher，否则流式端点会退化")
	}
}

// TestGuardWriterTracksStarted 确认「响应已经开始」被记下来了：panic 发生
// 在写过头之后就不能再写错误体（会打出 superfluous WriteHeader 警告，
// 而且内容是错的）。
func TestGuardWriterTracksStarted(t *testing.T) {
	rec := httptest.NewRecorder()
	gw := &guardWriter{ResponseWriter: rec}

	if gw.started {
		t.Fatal("还没写的时候 started 应当是 false")
	}
	gw.WriteHeader(http.StatusOK)
	if !gw.started {
		t.Fatal("WriteHeader 之后 started 应当是 true")
	}

	// 写过一次之后仍然要是 true。
	gw2 := &guardWriter{ResponseWriter: httptest.NewRecorder()}
	_, _ = gw2.Write([]byte("x"))
	if !gw2.started {
		t.Fatal("Write 之后 started 应当是 true")
	}
}

// TestPanicOverRealServerBecomes500 走真实服务器，确认 Serve 里的接线
// 没漏——中间件写对了但没挂上去的话，上面那些用例照样全绿。
func TestPanicOverRealServerBecomes500(t *testing.T) {
	h := &panicHandler{panicMethod: api.MethodGetSettings}
	a := startServer(t, h, "tok")
	c, err := Dial(context.Background(), a, "tok")
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	err = c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{})
	if err == nil {
		t.Fatal("handler panic 应当变成错误")
	}
	if !strings.Contains(err.Error(), "服务端内部错误") {
		t.Fatalf("错误里应当带出服务端的说明，实际: %v", err)
	}
}

// TestOriginalWriterUnwraps 确认 MaxBytesReader 拿到的是包装前的 writer。
//
// net/http 的「请求体超限」信号靠一个未导出方法传，包装之后就丢了。这个
// 断言防的是「以为解包了、其实返回的还是包装后的那个」——那种写法测试
// 全绿、超限时连接却不再关闭。
func TestOriginalWriterUnwraps(t *testing.T) {
	rec := httptest.NewRecorder()
	gw := &guardWriter{ResponseWriter: rec}
	if got := originalWriter(gw); got != http.ResponseWriter(rec) {
		t.Fatalf("originalWriter 应当返回包装前的 writer，实际 %T", got)
	}
	// 没有包装时原样返回。
	if got := originalWriter(rec); got != http.ResponseWriter(rec) {
		t.Fatalf("没有包装时应当原样返回，实际 %T", got)
	}
}

// TestStreamEndpointStillStreams 确认加了 recover 中间件之后，消息流
// 端点还是能一条条吐 NDJSON——中间件不能把流式功能改坏。
func TestStreamEndpointStillStreams(t *testing.T) {
	h := &stubHandler{
		events:   make(chan api.Event, 1),
		controls: make(chan api.Message, 1),
	}
	mux := recoverMiddleware(newMux(h, "tok"))
	h.events <- api.Event{Why: "进度", Task: &api.Task{ID: "t1"}}

	req := httptest.NewRequest(http.MethodPost, servicePrefix+api.MethodStreamMessages, nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()

	// serveStream 会一直阻塞到事件流与控制流都关闭；这里只验证它能写出
	// 第一行就走，所以给一个会关闭的通道。
	h.controls <- api.Message{Kind: api.MessageKindEvent}
	close(h.events)
	close(h.controls)

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("流式端点应当是 200，实际 %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "ndjson") {
		t.Fatalf("Content-Type 应当是 ndjson，实际 %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "t1") {
		t.Fatalf("流式响应里应当有事件内容，实际 %q", rec.Body.String())
	}
}
