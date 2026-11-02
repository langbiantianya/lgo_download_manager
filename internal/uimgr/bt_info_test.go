// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package uimgr

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/appservice"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// requireLoopback 在无法监听回环端口的环境(受限容器/沙箱)里跳过需要
// 真实网络栈的用例——BT 客户端要监听端口。
func requireLoopback(t *testing.T) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listening unavailable in this environment: %v", err)
	}
	_ = l.Close()
}

// TestBTInfoEndToEnd 验证「BT 任务详情」这条链路的端到端可用:
// scheduler 启动 BT 任务 → 句柄登记 → 契约层的 bt_info 方法 → 结果。
//
// 这是 UI「任务详情」面板的数据来源:面板每秒钟问一次业务进程要详情,
// 问不到就显示「会话已结束」。这条用例守住「问得到」这一半。
//
// 走的是 appservice.Call——业务侧现在由契约层承担,uimgr 不再自己
// 分派方法(旧实现走的是 uimgr.dispatch)。
func TestBTInfoEndToEnd(t *testing.T) {
	requireLoopback(t)

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "lgdm.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	sc := scheduler.New(st)
	svc := appservice.New(st, sc)
	t.Cleanup(func() {
		sc.CloseBittorrent()
		_ = st.Close()
	})

	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=lgdm-e2e"
	tk, err := sc.Add(scheduler.AddTaskInput{
		URL:      magnet,
		SavePath: filepath.Join(dir, "task"),
		Protocol: protocol.ProtoBT,
	})
	if err != nil {
		t.Fatalf("scheduler.Add: %v", err)
	}
	if err := sc.Start(tk.ID); err != nil {
		t.Fatalf("scheduler.Start: %v", err)
	}

	// BT 客户端是懒创建的,句柄登记要等它起来——轮询等一小会儿。
	var detail *api.BTDetail
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out := &api.BTDetail{}
		if err := svc.Call(context.Background(), api.MethodBTInfo,
			&api.BTInfoParams{TaskID: tk.ID}, out); err == nil {
			detail = out
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if detail == nil {
		t.Fatal("bt_info never returned a detail for a running BT task")
	}
	if detail.Name != "lgdm-e2e" {
		t.Errorf("detail name = %q, want lgdm-e2e", detail.Name)
	}
	// 元数据还没到(磁力链接没有 peer),总体积应为 0 而不是别的数。
	if detail.Total != 0 {
		t.Errorf("detail total = %d, want 0 before metadata arrives", detail.Total)
	}
	// 结果要能原样过 JSON:UI 进程收到的就是这个编码。
	blob, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	var back api.BTDetail
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if back.Name != detail.Name {
		t.Errorf("round-tripped name = %q, want %q", back.Name, detail.Name)
	}
	// 分片热力图的桶也要能过 JSON:UI 进程收到的就是这个编码。
	if len(back.PieceBuckets) != len(detail.PieceBuckets) {
		t.Errorf("round-tripped piece buckets = %d, want %d",
			len(back.PieceBuckets), len(detail.PieceBuckets))
	}
}

// TestBTInfoUnknownTask 验证任务不在运行时 bt_info 返回错误而不是空
// 详情——UI 据此把面板切到「会话已结束」。
func TestBTInfoUnknownTask(t *testing.T) {
	svc := appservice.New(nil, scheduler.New(nil))
	out := &api.BTDetail{}
	if err := svc.Call(context.Background(), api.MethodBTInfo,
		&api.BTInfoParams{TaskID: "nope"}, out); err == nil {
		t.Fatal("bt_info for an unknown task should fail")
	}
}
