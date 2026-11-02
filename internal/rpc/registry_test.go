// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package rpc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
)

// stubHandler 是最小业务侧实现。
type stubHandler struct {
	events   chan api.Event
	controls chan api.Message
}

func (s *stubHandler) Call(ctx context.Context, method string, in, out any) error {
	switch method {
	case api.MethodGetSettings:
		o, ok := out.(*api.Settings)
		if !ok {
			return errors.New("stub: 响应类型不对")
		}
		o.Language = "en"
	case api.MethodList:
		o, ok := out.(*api.ListResult)
		if !ok {
			return errors.New("stub: 响应类型不对")
		}
		o.Tasks = []*api.Task{{ID: "t1"}}
	default:
		return errors.New("stub: 未实现 " + method)
	}
	return nil
}

func (s *stubHandler) Events() (<-chan api.Event, func()) {
	return s.events, func() {}
}

func (s *stubHandler) Controls() <-chan api.Message { return s.controls }

// startServer 在临时目录里的 unix socket 上启动服务，返回地址与停服函数。
func startServer(t *testing.T, transport, token string) (string, func()) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "lgdm.sock")
	ctx, cancel := context.WithCancel(context.Background())
	h := &stubHandler{
		events:   make(chan api.Event, 4),
		controls: make(chan api.Message, 4),
	}
	served := make(chan error, 1)
	go func() {
		served <- Serve(ctx, ServerOptions{Transport: transport, Addr: "unix:" + sock, Token: token}, h)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("服务没有在超时前就绪")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop := func() {
		cancel()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Error("服务没有在超时前停下")
		}
	}
	t.Cleanup(stop)
	return sock, stop
}

// TestRegistryBothTransports 确认注册表在两种传输下都能起服务、连上、
// 调一次。
func TestRegistryBothTransports(t *testing.T) {
	for _, transport := range []string{TransportGRPC, TransportHTTP} {
		t.Run(transport, func(t *testing.T) {
			sock, _ := startServer(t, transport, "tok")
			c, err := Dial(context.Background(), transport, "unix:"+sock, "tok")
			if err != nil {
				t.Fatalf("连接失败: %v", err)
			}
			defer c.Close()

			tasks, err := c.List(api.FilterAll, api.SortCreatedDesc)
			if err != nil {
				t.Fatalf("list 失败: %v", err)
			}
			if len(tasks) != 1 || tasks[0].ID != "t1" {
				t.Fatalf("任务列表不对: %+v", tasks)
			}
			if got := c.Settings().Language; got != "en" {
				t.Fatalf("设置不对: %q", got)
			}
		})
	}
}

// TestRegistryDefaultTransport 确认不写传输名时默认走 gRPC。
func TestRegistryDefaultTransport(t *testing.T) {
	sock, _ := startServer(t, "", "tok")
	c, err := Dial(context.Background(), "", "unix:"+sock, "tok")
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer c.Close()
	if _, err := c.List(api.FilterAll, api.SortCreatedDesc); err != nil {
		t.Fatalf("list 失败: %v", err)
	}
}

// TestRegistryUnknownTransport 确认未知传输名会报错。
func TestRegistryUnknownTransport(t *testing.T) {
	err := Serve(context.Background(), ServerOptions{Transport: "quic", Addr: "unix:/tmp/x.sock"}, &stubHandler{})
	if err == nil {
		t.Fatal("未知传输名应当报错")
	}
	if _, err := Dial(context.Background(), "quic", "unix:/tmp/x.sock", ""); err == nil {
		t.Fatal("未知传输名应当报错")
	}
}

// TestRegistryBadAddr 确认地址解析失败会报错。
func TestRegistryBadAddr(t *testing.T) {
	err := Serve(context.Background(), ServerOptions{Addr: ""}, &stubHandler{})
	if err == nil {
		t.Fatal("空地址应当报错")
	}
	if _, err := Dial(context.Background(), TransportGRPC, "", ""); err == nil {
		t.Fatal("空地址应当报错")
	}
}
