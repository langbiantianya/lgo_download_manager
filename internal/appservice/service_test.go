// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package appservice

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
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
