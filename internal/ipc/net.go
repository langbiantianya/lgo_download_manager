// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ipc

import (
	"fmt"
	"net"
	"os"
	"time"
)

// Listen 在 socketPath 上创建 Unix socket 监听器。启动前清理残留 socket
// 文件，并把权限限制为当前用户可读写。
func Listen(socketPath string) (net.Listener, error) {
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("ipc remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("ipc listen %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("ipc chmod %s: %w", socketPath, err)
	}
	return ln, nil
}

// Dial 连接业务进程的 Unix socket，返回消息通道。retry 秒内持续重试，
// 以适应子进程启动晚于监听就绪的时序。
func Dial(socketPath string, retry time.Duration) (*Conn, error) {
	deadline := time.Now().Add(retry)
	for {
		conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
		if err == nil {
			return NewConn(conn), nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("ipc dial %s: %w", socketPath, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
