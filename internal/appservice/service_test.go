// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package appservice

import (
	"context"
	"path/filepath"
	"testing"

	"lgo_download_manager/internal/api"
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
