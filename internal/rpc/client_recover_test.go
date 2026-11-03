// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package rpc

import (
	"context"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
)

// TestReadLoopRecoversHookPanic 确认 UI 钩子里的 panic 不会带走整个
// 客户端进程。
//
// dispatch 会调 UI 侧注册的钩子。钩子里一个 panic 会顺着 readLoop 冒到
// goroutine 顶层，把整个 UI 子进程带走——界面直接消失，用户手里正在跑
// 的下载全丢。这里断言的是：panic 被兜住，并且会话被标记成断开（上层
// 按「UI 崩了」处理）。
func TestReadLoopRecoversHookPanic(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	closed := make(chan struct{})
	c.SetOnClose(func() { close(closed) })

	// 钩子里故意 panic。
	c.SetOnShow(func() { panic("UI 钩子的 bug") })

	tr.events <- api.Message{Kind: api.MessageKindShow}

	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("钩子 panic 之后应当把会话标记成断开")
	}

	// 客户端还活着：还能继续调用。
	if _, err := c.List(api.FilterAll, api.SortCreatedDesc); err != nil {
		t.Fatalf("panic 之后客户端应当还活着: %v", err)
	}
}
