// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package addr 解析与监听传输层的网络地址。传输层既可以监听 Unix socket
// （默认，仅本机可达），也可以监听 TCP 的 host:port。
//
// 本包是叶子包：gRPC 与 net/http 两种传输实现都依赖它，但它不依赖任何
// 传输实现，因此不会形成包环。
package addr

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// Addr 描述一个监听或连接地址。
type Addr struct {
	// Network 是 "unix" 或 "tcp"。
	Network string
	// Address 是 unix socket 路径，或 tcp 的 host:port。
	Address string
}

// Parse 解析地址描述。支持三种写法：
//
//	unix:<path>       显式 Unix socket
//	tcp:<host:port>   显式 TCP
//	<裸值>            自动判别：含路径分隔符（/ 或 \）视为 unix，
//	                  否则按 host:port 视为 tcp
//
// 裸值判别必须区分 "/" 与 "\"：Windows 上的 socket 路径形如
// C:\Users\...\ui.sock，里面含冒号，按「含冒号即 host:port」会误判。
func Parse(s string) (Addr, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return Addr{}, fmt.Errorf("addr: 地址为空")
	case strings.HasPrefix(s, "unix:"):
		p := strings.TrimPrefix(s, "unix:")
		if p == "" {
			return Addr{}, fmt.Errorf("addr: unix 地址缺少路径: %q", s)
		}
		return Addr{Network: "unix", Address: p}, nil
	case strings.HasPrefix(s, "tcp:"):
		h := strings.TrimPrefix(s, "tcp:")
		if h == "" {
			return Addr{}, fmt.Errorf("addr: tcp 地址缺少 host:port: %q", s)
		}
		return Addr{Network: "tcp", Address: h}, nil
	case strings.ContainsAny(s, `/\`):
		return Addr{Network: "unix", Address: s}, nil
	case strings.Contains(s, ":"):
		return Addr{Network: "tcp", Address: s}, nil
	default:
		return Addr{}, fmt.Errorf("addr: 无法识别的地址 %q（示例：unix:/tmp/lgdm.sock 或 tcp:127.0.0.1:17830）", s)
	}
}

// Listen 按地址创建监听器。cleanup 负责关闭监听器；unix 分支还会删除
// socket 文件。
func Listen(a Addr) (net.Listener, func(), error) {
	if a.Network == "tcp" {
		ln, err := net.Listen("tcp", a.Address)
		if err != nil {
			return nil, nil, fmt.Errorf("addr: listen %s: %w", a.Address, err)
		}
		return ln, func() { _ = ln.Close() }, nil
	}
	// 启动前清理残留 socket 文件，否则上次异常退出留下的路径会让
	// net.Listen 报 "address already in use"。
	if err := os.Remove(a.Address); err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("addr: remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", a.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("addr: listen %s: %w", a.Address, err)
	}
	// 只允许当前用户读写。
	if err := os.Chmod(a.Address, 0o600); err != nil {
		_ = ln.Close()
		return nil, nil, fmt.Errorf("addr: chmod %s: %w", a.Address, err)
	}
	return ln, func() {
		_ = ln.Close()
		_ = os.Remove(a.Address)
	}, nil
}

// Dial 按地址建立连接。
func Dial(a Addr) (net.Conn, error) {
	conn, err := net.Dial(a.Network, a.Address)
	if err != nil {
		return nil, fmt.Errorf("addr: dial %s %s: %w", a.Network, a.Address, err)
	}
	return conn, nil
}
