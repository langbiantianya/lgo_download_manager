// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package tray

import (
	"sync"
	"testing"
	"time"
)

// TestDispatchClicks_ClosedQuitChannelDoesNotQuit 回归测试。
//
// systray 的 MenuItem.Remove() 会 close(item.ClickedCh) 并清空内部
// 缓冲区;若 dispatchClicks 把「通道已关闭」当作一次点击,select 会
// 立刻返回零值 → 调 cb.Quit() → 业务进程被误杀。
//
// 真实触发场景:切语言 → Reload 重建菜单 → systray.ResetMenu() →
// MenuItem.Remove() → close(mQuit.ClickedCh)。上线前的实现正好踩到
// 这一点,用户切完语言应用直接退出。
func TestDispatchClicks_ClosedQuitChannelDoesNotQuit(t *testing.T) {
	open := make(chan struct{})
	quit := make(chan struct{})

	var mu sync.Mutex
	var quitCalls int
	done := make(chan struct{})
	go func() {
		dispatchClicks(open, quit, Callbacks{
			Quit: func() {
				mu.Lock()
				quitCalls++
				mu.Unlock()
			},
		})
		close(done)
	}()

	close(quit) // 模拟 MenuItem.Remove()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("dispatchClicks did not return after quit channel closed")
	}
	mu.Lock()
	defer mu.Unlock()
	if quitCalls != 0 {
		t.Fatalf("closed quit channel invoked Quit callback %d times; must be treated as teardown, not a click", quitCalls)
	}
}

// TestDispatchClicks_QuitClickInvokesOnce 保证正常点击路径没被「关闭即退出」
// 的修复误伤:向 quit 通道发一个值仍然要触发 Quit 回调。
func TestDispatchClicks_QuitClickInvokesOnce(t *testing.T) {
	open := make(chan struct{})
	quit := make(chan struct{})

	quitCalls := make(chan struct{}, 4)
	done := make(chan struct{})
	go func() {
		dispatchClicks(open, quit, Callbacks{Quit: func() { quitCalls <- struct{}{} }})
		close(done)
	}()

	quit <- struct{}{}
	select {
	case <-quitCalls:
	case <-time.After(time.Second):
		t.Fatalf("quit click did not invoke callback")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("dispatchClicks did not return after quit click")
	}
}

// TestDispatchClicks_OpenClickKeepsLooping 验证 Open 点击后循环继续,
// 后续的 quit 点击仍能生效——Open 不应终止派发。
func TestDispatchClicks_OpenClickKeepsLooping(t *testing.T) {
	open := make(chan struct{})
	quit := make(chan struct{})

	var opens int
	openDone := make(chan struct{}, 4)
	quitSeen := make(chan struct{})
	go dispatchClicks(open, quit, Callbacks{
		Open: func() {
			opens++
			openDone <- struct{}{}
		},
		Quit: func() { close(quitSeen) },
	})

	open <- struct{}{}
	open <- struct{}{}
	for i := 0; i < 2; i++ {
		select {
		case <-openDone:
		case <-time.After(time.Second):
			t.Fatalf("Open callback %d not invoked", i+1)
		}
	}
	if opens != 2 {
		t.Fatalf("Open invoked %d times, want 2", opens)
	}

	quit <- struct{}{}
	select {
	case <-quitSeen:
	case <-time.After(time.Second):
		t.Fatalf("quit after opens did not invoke Quit")
	}
}

// TestReload_BeforeStartIsNoop Reload 在托盘就绪前必须静默返回:
// 首启早期语言可能先于托盘设置好,此时菜单项尚未创建,
// 直接触碰 systray 会 panic。
func TestReload_BeforeStartIsNoop(t *testing.T) {
	trayMu.Lock()
	savedReady, savedOpen, savedQuit := trayReady, mOpen, mQuit
	trayReady, mOpen, mQuit = false, nil, nil
	trayMu.Unlock()
	defer func() {
		trayMu.Lock()
		trayReady, mOpen, mQuit = savedReady, savedOpen, savedQuit
		trayMu.Unlock()
	}()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Reload before Start panicked: %v", r)
		}
	}()
	Reload() // must not panic
}
