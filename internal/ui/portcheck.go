// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"fmt"
	"net"
)

// portBusy 报告 port 是否已经被别的程序占着。
//
// BT 客户端在同一个端口上同时监听 TCP 与 UDP,任意一个被占,客户端就
// 起不来。port <= 0 表示「交给系统随机分配」,不算被占。
//
// 检查的方式是「试着 bind 一下再放掉」——不真的连上去,也不会打扰
// 对方的监听者。
func portBusy(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	addr := fmt.Sprintf(":%d", port)
	tcpLn, err := net.Listen("tcp", addr)
	if err != nil {
		return true
	}
	tcpLn.Close()
	udpConn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return true
	}
	udpConn.Close()
	return false
}
