// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package grpcx

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"lgo_download_manager/internal/api"
)

// panicHandler 在指定方法上 panic，模拟业务侧的一个 bug。
type panicHandler struct {
	mockHandler
	panicMethod string
}

func (p *panicHandler) Call(ctx context.Context, method string, in, out any) error {
	if method == p.panicMethod {
		panic("业务侧的 bug")
	}
	return p.mockHandler.Call(ctx, method, in, out)
}

// panicStreamHandler 在订阅事件流时 panic。
type panicStreamHandler struct {
	mockHandler
}

func (p *panicStreamHandler) Events() (<-chan api.Event, func()) {
	panic("业务侧的 bug")
}

// TestHandlerPanicBecomesInternalError 确认 handler 的 panic 被拦截器兜成
// Internal 错误，而不是把整个进程带走。
//
// 这条用例本身就是「进程活着」的证据：去掉拦截器时它不是「断言失败」，
// 而是**整个测试进程被 panic 打死**——grpc-go 默认不 recover，panic 会
// 从 handler 一路冒到 server 的 goroutine 顶层，把进程带走。业务进程里
// 还跑着所有下载任务，一次 panic 就是「应用崩了、任务全丢」，而 gRPC
// 又是默认传输。
func TestHandlerPanicBecomesInternalError(t *testing.T) {
	h := &panicHandler{panicMethod: api.MethodGetSettings}
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{})
	if err == nil {
		t.Fatal("handler panic 应当变成错误返回")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("错误应当是 gRPC status: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Fatalf("错误码应当是 Internal，实际 %v: %v", st.Code(), err)
	}

	// 服务端还活着：换一个不 panic 的方法应当照常成功。
	if err := c.Call(context.Background(), api.MethodList, &api.ListParams{}, &api.ListResult{}); err != nil {
		t.Fatalf("panic 之后服务端应当还活着: %v", err)
	}
}

// TestStreamPanicBecomesInternalError 确认流式 handler 的 panic 也被兜住。
//
// 流式路径与一元路径是两条不同的拦截器（UnaryInterceptor 与
// StreamInterceptor），少挂一条就等于这条路径仍然会带走进程。
//
// 客户端侧看到的是「流结束了」——gRPC 的 Recv 报错只表现为消息通道关闭，
// 错误码不往上传。真正要断言的是两件事：流没有卡死，以及服务端还活着。
func TestStreamPanicBecomesInternalError(t *testing.T) {
	h := &panicStreamHandler{}
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	ch, err := c.Stream(context.Background())
	if err != nil {
		t.Fatalf("打开消息流失败: %v", err)
	}
	// 流必须自己结束，不能一直挂着。
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("流式 handler panic 之后消息流应当结束")
	}

	// 服务端还活着。
	if err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{}); err != nil {
		t.Fatalf("panic 之后服务端应当还活着: %v", err)
	}
}
