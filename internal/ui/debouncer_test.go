// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"sync"
	"testing"
	"time"
)

// TestDebouncer_CoalescesBurst 回归测试。
//
// 「新建任务」对话框原先在 URL 每变化一个字符时就发一次探测请求:
//  键入一个 60 字符的网址就会打出 60 个请求,业务进程每个都要真的
//  连一次服务器。合并之后,连续触发只执行最后一次。
func TestDebouncer_CoalescesBurst(t *testing.T) {
	d := &debouncer{delay: 60 * time.Millisecond}

	var mu sync.Mutex
	var got []uint64
	for i := 0; i < 10; i++ {
		d.Trigger(func(seq uint64) {
			mu.Lock()
			got = append(got, seq)
			mu.Unlock()
		})
	}

	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("triggered %d times, want 1 (the burst must be coalesced)", len(got))
	}
}

// TestDebouncer_OnlyLatestIsCurrent 验证序号语义:后来的触发会让先前的
// 结果失效——探测回来的早发晚归时,旧结果不该覆盖新结果。
func TestDebouncer_OnlyLatestIsCurrent(t *testing.T) {
	d := &debouncer{delay: time.Hour} // 不让它真的触发
	first := d.Trigger(func(uint64) {})
	second := d.Trigger(func(uint64) {})

	if d.IsCurrent(first) {
		t.Error("the first trigger is still current after a newer one")
	}
	if !d.IsCurrent(second) {
		t.Error("the latest trigger must be current")
	}
	d.Stop()
}

// TestDebouncer_StopCancelsPending 验证 Stop 之后待触发的动作不会再跑。
func TestDebouncer_StopCancelsPending(t *testing.T) {
	d := &debouncer{delay: 50 * time.Millisecond}

	var mu sync.Mutex
	fired := false
	d.Trigger(func(uint64) {
		mu.Lock()
		fired = true
		mu.Unlock()
	})
	d.Stop()

	time.Sleep(250 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if fired {
		t.Error("the pending trigger ran after Stop")
	}
}
