// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package addr

import "testing"

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
