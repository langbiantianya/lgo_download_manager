// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package addr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		network string
		address string
		bad     bool
	}{
		{in: "unix:/tmp/lgdm.sock", network: "unix", address: "/tmp/lgdm.sock"},
		{in: "tcp:127.0.0.1:17830", network: "tcp", address: "127.0.0.1:17830"},
		{in: "tcp::17830", network: "tcp", address: ":17830"},
		{in: "/tmp/lgdm.sock", network: "unix", address: "/tmp/lgdm.sock"},
		{in: `./ui.sock`, network: "unix", address: `./ui.sock`},
		// Windows 路径含冒号，不能被当成 host:port。
		{in: `C:\Users\lbty\AppData\Local\ui.sock`, network: "unix", address: `C:\Users\lbty\AppData\Local\ui.sock`},
		{in: "127.0.0.1:17830", network: "tcp", address: "127.0.0.1:17830"},
		{in: "unix:", bad: true},
		{in: "tcp:", bad: true},
		{in: "nonsense", bad: true},
		{in: "", bad: true},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if c.bad {
			if err == nil {
				t.Fatalf("Parse(%q) 应当报错", c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("Parse(%q) 报错: %v", c.in, err)
		}
		if got.Network != c.network || got.Address != c.address {
			t.Fatalf("Parse(%q) = %+v, want %s/%s", c.in, got, c.network, c.address)
		}
	}
}

// TestListenUnixAndDial 验证 unix socket 的监听与连接：socket 文件会被创建、
// 权限收紧到 0600，cleanup 之后文件被删掉。
func TestListenUnixAndDial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lgdm.sock")
	a := Addr{Network: "unix", Address: path}
	ln, cleanup, err := Listen(a)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("socket 文件没创建: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket 权限 = %o, want 600", perm)
	}

	conn, err := Dial(a)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = conn.Close()

	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cleanup 后 socket 文件仍在: %v", err)
	}
	// cleanup 幂等：再调一次不能 panic。
	cleanup()
}
