// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package scheduler

import (
	"testing"

	"lgo_download_manager/internal/bittorrent"
)

// TestBTTaskDetailRegistry 校验 BT 句柄登记表:注册后能查到详情,摘除后
// 查不到;「旧句柄的迟到摘除」不能把同一任务的新句柄误删——那是重启
// 任务时最典型的竞态(旧 goroutine 的 defer 与新 handle 的注册交错)。
func TestBTTaskDetailRegistry(t *testing.T) {
	sc := New(nil)

	if _, err := sc.BTTaskDetail("missing"); err == nil {
		t.Fatal("expected an error for a task that is not registered")
	}

	old := &bittorrent.Handle{}
	sc.registerBTHandle("t1", old)
	d, err := sc.BTTaskDetail("t1")
	if err != nil {
		t.Fatalf("BTTaskDetail after register: %v", err)
	}
	if d == nil {
		t.Fatal("BTTaskDetail returned a nil detail")
	}

	// 同一任务换了新句柄:旧句柄的摘除必须被忽略。
	fresh := &bittorrent.Handle{}
	sc.registerBTHandle("t1", fresh)
	sc.unregisterBTHandle("t1", old)
	if _, err := sc.BTTaskDetail("t1"); err != nil {
		t.Fatalf("a stale unregister removed the current handle: %v", err)
	}

	// 当前句柄的摘除要真的生效。
	sc.unregisterBTHandle("t1", fresh)
	if _, err := sc.BTTaskDetail("t1"); err == nil {
		t.Fatal("expected an error after the handle was unregistered")
	}
}

// TestRegisterBTHandleIgnoresEmptyArgs 校验登记函数的边界:空任务 ID 或
// 空句柄都不该进表(否则详情查询会命中一个永远查不到东西的条目)。
func TestRegisterBTHandleIgnoresEmptyArgs(t *testing.T) {
	sc := New(nil)
	sc.registerBTHandle("", &bittorrent.Handle{})
	sc.registerBTHandle("t1", nil)
	sc.btTasksMu.Lock()
	n := len(sc.btTasks)
	sc.btTasksMu.Unlock()
	if n != 0 {
		t.Fatalf("btTasks has %d entries, want 0", n)
	}
}
