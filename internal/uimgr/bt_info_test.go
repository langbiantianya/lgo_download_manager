// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package uimgr

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"lgo_download_manager/internal/bittorrent"
	"lgo_download_manager/internal/ipc"
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
// scheduler 启动 BT 任务 → 句柄登记 → uimgr 的 bt_info 方法 → 结果。
//
// 这是 UI「任务详情」面板的数据来源:面板每秒钟问一次业务进程要详情,
// 问不到就显示「会话已结束」。这条用例守住「问得到」这一半。
func TestBTInfoEndToEnd(t *testing.T) {
	requireLoopback(t)

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "lgdm.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	sc := scheduler.New(st)
	m := New(st, sc)
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

	raw, err := json.Marshal(ipc.BTInfoParams{TaskID: tk.ID})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	// BT 客户端是懒创建的,句柄登记要等它起来——轮询等一小会儿。
	var detail *bittorrent.Detail
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		res, derr := m.dispatch(ipc.MethodBTInfo, raw)
		if derr == nil {
			if d, ok := res.(*bittorrent.Detail); ok && d != nil {
				detail = d
				break
			}
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
	var back bittorrent.Detail
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if back.Name != detail.Name {
		t.Errorf("round-tripped name = %q, want %q", back.Name, detail.Name)
	}
}

// TestBTInfoUnknownTask 验证任务不在运行时 bt_info 返回错误而不是空
// 详情——UI 据此把面板切到「会话已结束」。
func TestBTInfoUnknownTask(t *testing.T) {
	m := New(nil, scheduler.New(nil))
	raw, err := json.Marshal(ipc.BTInfoParams{TaskID: "nope"})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if _, err := m.dispatch(ipc.MethodBTInfo, raw); err == nil {
		t.Fatal("bt_info for an unknown task should fail")
	}
}
