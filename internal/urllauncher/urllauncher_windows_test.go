// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build windows

package urllauncher

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// startPipeServer 在独立的 baseDir 上启动转发服务,返回收到的 URL 通道。
// 每个测试用独立的 baseDir,管道名因此互不冲突。
func startPipeServer(t *testing.T) <-chan string {
	t.Helper()
	SetBaseDir(t.TempDir())

	got := make(chan string, 8)
	done := make(chan error, 1)
	go func() { done <- ListenAndServe(func(u string) { got <- u }) }()

	t.Cleanup(func() {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("ListenAndServe returned: %v", err)
			}
		default:
			// 服务仍在等下一个客户端,符合预期。
		}
	})
	return got
}

// sendAndRecv 转发一条 URL 并返回服务端收到的那条。
func sendAndRecv(t *testing.T, got <-chan string, raw string) string {
	t.Helper()
	if err := SendURL(raw); err != nil {
		t.Fatalf("SendURL(%q): %v", raw, err)
	}
	select {
	case u := <-got:
		return u
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for the forwarded URL %q", raw)
		return ""
	}
}

// TestSendURLForwardsToPipe 覆盖主路径:次级实例写进命名管道的 URL 被
// 主实例的转发服务收到,且内容未被改动(解析仍由 HandleURL 负责)。
func TestSendURLForwardsToPipe(t *testing.T) {
	got := startPipeServer(t)

	const raw = "lgom://download?url=https%3A%2F%2Fexample.com%2Ffile.zip&name=file.zip"
	forwarded := sendAndRecv(t, got, raw)
	if forwarded != raw {
		t.Fatalf("forwarded URL = %q, want %q", forwarded, raw)
	}

	req, err := HandleURL(forwarded)
	if err != nil {
		t.Fatalf("HandleURL(forwarded): %v", err)
	}
	if req.URL != "https://example.com/file.zip" || req.Name != "file.zip" {
		t.Fatalf("parsed request = %+v, want url=https://example.com/file.zip name=file.zip", req)
	}
}

// TestListenAndServeSurvivesMissizedFrame 覆盖边界:客户端声明一个超过
// maxFrameLen 的帧后立刻断开,服务端必须拒绝这一帧并继续接受后续连接 ——
// 否则一个畸形客户端就能让 URL 转发在本进程内永久失效。
func TestListenAndServeSurvivesMissizedFrame(t *testing.T) {
	got := startPipeServer(t)

	namePtr, err := windows.UTF16PtrFromString(pipeKernelName())
	if err != nil {
		t.Fatalf("UTF16PtrFromString: %v", err)
	}
	h, err := dialPipe(namePtr)
	if err != nil {
		t.Fatalf("dialPipe: %v", err)
	}
	var n uint32
	if err := windows.WriteFile(h, []byte{0xFF, 0xFF, 0xFF, 0xFF}, &n, nil); err != nil {
		windows.CloseHandle(h)
		t.Fatalf("WriteFile: %v", err)
	}
	windows.CloseHandle(h)

	const raw = "lgom://download?url=https%3A%2F%2Fexample.com%2Fafter.bin"
	if forwarded := sendAndRecv(t, got, raw); forwarded != raw {
		t.Fatalf("forwarded URL after a malformed frame = %q, want %q", forwarded, raw)
	}
}

// TestSendURLWithoutPrimary 覆盖错误路径:没有主实例在监听时,SendURL 必须
// 在重试窗口后返回错误而不是无限等待 —— 次级实例靠这个错误决定以非零
// 状态退出并向用户报告。
func TestSendURLWithoutPrimary(t *testing.T) {
	SetBaseDir(t.TempDir())

	old := dialRetryWindow
	dialRetryWindow = 200 * time.Millisecond
	defer func() { dialRetryWindow = old }()

	err := SendURL("lgom://download?url=https%3A%2F%2Fexample.com%2Ff.zip")
	if err == nil {
		t.Fatal("SendURL without a primary instance must fail")
	}
	if !strings.Contains(err.Error(), "no primary instance") {
		t.Fatalf("unexpected error: %v", err)
	}
}
