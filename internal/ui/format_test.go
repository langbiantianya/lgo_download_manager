// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"net"
	"testing"

	"lgo_download_manager/internal/ilocale"
)

// TestFormatBytes 锁定字节格式化的输出。
//
// 这段逻辑之前散在三处、精度还不一样,现在只有这一份,各处显示必须一致。
func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{-1, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1 << 20, "1.0 MB"},
		{1 << 30, "1.00 GB"},
		{1 << 40, "1.00 TB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.in); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTaskSizeText 未知大小显示本地化的「未知大小」,其余走标准格式。
func TestTaskSizeText(t *testing.T) {
	if got := taskSizeText(0); got != ilocale.T("taskRow.size.unknown") {
		t.Errorf("taskSizeText(0) = %q, want %q", got, ilocale.T("taskRow.size.unknown"))
	}
	if got := taskSizeText(2048); got != "2.0 KB" {
		t.Errorf("taskSizeText(2048) = %q, want 2.0 KB", got)
	}
}

// TestPortBusy 验证端口占用检测:被占着的端口必须被认出来,空闲端口
// 不能误报(否则设置页会一直显示「端口被占用」)。
func TestPortBusy(t *testing.T) {
	// 监听所有网卡的端口:与 portBusy 的探测方式一致,这样才会真的撞上。
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Skipf("cannot bind a local port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if !portBusy(port) {
		t.Errorf("portBusy(%d) = false, want true (the port is held by this test)", port)
	}
	ln.Close()

	// 0 表示「交给系统随机分配」,不算被占。
	if portBusy(0) {
		t.Error("portBusy(0) = true, want false (0 means 'pick any port')")
	}
	if portBusy(70000) {
		t.Error("portBusy(70000) = true, want false (out of range)")
	}
}
