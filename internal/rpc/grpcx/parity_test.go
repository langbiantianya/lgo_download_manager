// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// 这是外部测试包：grpcx 自己不能 import internal/rpc/httpx（虽然不成环，
// 但传输实现之间不该互相依赖），外部测试包可以。
//
// 这里放跨传输一致性测试：同一批 api.Message 过一遍 httpx、再过一遍
// grpcx，两边的结果必须一样。两种传输是同一个契约层（internal/api）的
// 两种线格式实现，行为不一致就意味着「换传输就换语义」——UI 侧的
// nil 判断（msg.Event != nil、msg.ShowAddTask == nil）在其中一边会失效。
package grpcx_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
	"lgo_download_manager/internal/rpc/grpcx"
	"lgo_download_manager/internal/rpc/httpx"
)

// parityToken 是两个服务共用的 token。
const parityToken = "parity-token"

// parityHandler 是跨传输一致性测试用的业务侧实现。
//
// grpcx.Handler 与 httpx.Handler 的方法集逐字一致，一个实现同时喂给两边。
type parityHandler struct {
	controls chan api.Message
}

func (h *parityHandler) Call(_ context.Context, method string, _, _ any) error {
	return errors.New("parity: 这个测试不该调用一元方法: " + method)
}

// Events 返回一个永远不会有消息的事件流：这个测试只关心控制消息。
func (h *parityHandler) Events() (<-chan api.Event, func()) {
	return make(chan api.Event), func() {}
}

func (h *parityHandler) Controls() <-chan api.Message { return h.controls }

// TestMessageRoundTripMatchesHTTPTransport 是 I3 的跨传输一致性测试。
//
// 每一行都要在两种传输下得到同样的结果：类别不变、载荷的在场与缺席不变。
func TestMessageRoundTripMatchesHTTPTransport(t *testing.T) {
	inputs := []api.Message{
		// 类别是事件，但事件载荷缺席。
		{Kind: api.MessageKindEvent},
		// 类别是事件，事件载荷在场。
		{Kind: api.MessageKindEvent, Event: &api.Event{Why: "progress", SpeedBPS: 1024}},
		// 类别是弹窗，但弹窗参数缺席。
		{Kind: api.MessageKindShowAddTask},
		// 类别是弹窗，弹窗参数在场。
		{Kind: api.MessageKindShowAddTask, ShowAddTask: &api.ShowAddTaskParams{
			URL: "http://x", Name: "n", UA: "ua", Cookies: "c",
		}},
		{Kind: api.MessageKindShow},
		{Kind: api.MessageKindLanguage, Language: "zh"},
		{Kind: api.MessageKindClose},
		// 未知类别也必须原样保留。
		{Kind: api.MessageKind("bogus")},
	}

	gotGRPC := roundTripGRPC(t, inputs)
	gotHTTP := roundTripHTTP(t, inputs)

	for i, in := range inputs {
		if !reflect.DeepEqual(gotGRPC[i], gotHTTP[i]) {
			t.Errorf("第 %d 条消息两种传输的结果不一致:\n grpcx = %+v\n  http = %+v\n 输入 = %+v",
				i, gotGRPC[i], gotHTTP[i], in)
		}
		if !reflect.DeepEqual(gotGRPC[i], in) {
			t.Errorf("第 %d 条消息过 grpcx 之后变了:\n got = %+v\nwant = %+v", i, gotGRPC[i], in)
		}
	}
}

// roundTripGRPC 把一批消息过一遍 gRPC 传输，拿回客户端看到的样子。
func roundTripGRPC(t *testing.T, inputs []api.Message) []api.Message {
	t.Helper()
	h := &parityHandler{controls: make(chan api.Message, len(inputs))}
	for _, m := range inputs {
		h.controls <- m
	}
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "lgdm.sock")}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = grpcx.Serve(ctx, a, parityToken, h) }()
	waitSocket(t, a)

	c, err := grpcx.Dial(context.Background(), a, parityToken)
	if err != nil {
		t.Fatalf("gRPC 连接失败: %v", err)
	}
	defer c.Close()
	stream, err := c.Stream(context.Background())
	if err != nil {
		t.Fatalf("gRPC 开流失败: %v", err)
	}
	return collect(t, stream, len(inputs), "gRPC")
}

// roundTripHTTP 把一批消息过一遍 httpx 传输，拿回客户端看到的样子。
func roundTripHTTP(t *testing.T, inputs []api.Message) []api.Message {
	t.Helper()
	h := &parityHandler{controls: make(chan api.Message, len(inputs))}
	for _, m := range inputs {
		h.controls <- m
	}
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "lgdm.sock")}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = httpx.Serve(ctx, a, parityToken, h) }()
	waitSocket(t, a)

	c, err := httpx.Dial(context.Background(), a, parityToken)
	if err != nil {
		t.Fatalf("HTTP 连接失败: %v", err)
	}
	defer c.Close()
	stream, err := c.Stream(context.Background())
	if err != nil {
		t.Fatalf("HTTP 开流失败: %v", err)
	}
	return collect(t, stream, len(inputs), "HTTP")
}

// collect 从消息流里收 n 条消息。
func collect(t *testing.T, stream <-chan api.Message, n int, which string) []api.Message {
	t.Helper()
	out := make([]api.Message, 0, n)
	for len(out) < n {
		select {
		case msg, ok := <-stream:
			if !ok {
				t.Fatalf("%s: 消息流提前结束，只收到 %d 条", which, len(out))
			}
			out = append(out, msg)
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: 只收到 %d 条消息", which, len(out))
		}
	}
	return out
}

// waitSocket 等 unix socket 文件出现：文件出现即表示服务已经能连。
func waitSocket(t *testing.T, a addr.Addr) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(a.Address); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("服务没有在超时前就绪: %v", a)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
