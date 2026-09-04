// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ipc

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// maxMessageBytes 限制单条消息大小（与 urllauncher 一致：1 MiB）。
const maxMessageBytes = 1 << 20

// Conn 包装一条已建立的 Unix socket 连接，提供长度前缀 JSON 帧的
// 双向消息收发。Send 可被多个 goroutine 并发调用；Recv 同一时刻
// 只应有一个读取方。
type Conn struct {
	c net.Conn

	wmu sync.Mutex
}

// NewConn 从已连接的 socket 创建消息通道。
func NewConn(c net.Conn) *Conn {
	return &Conn{c: c}
}

// Send 序列化并发送一条消息。线程安全。
func (x *Conn) Send(m Message) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("ipc marshal: %w", err)
	}
	if len(payload) > maxMessageBytes {
		return fmt.Errorf("ipc message too large: %d bytes", len(payload))
	}
	x.wmu.Lock()
	defer x.wmu.Unlock()
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := x.c.Write(header[:]); err != nil {
		return err
	}
	_, err = x.c.Write(payload)
	return err
}

// Recv 阻塞读取下一条消息。
func (x *Conn) Recv() (Message, error) {
	var header [4]byte
	if _, err := io.ReadFull(x.c, header[:]); err != nil {
		return Message{}, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n > maxMessageBytes {
		return Message{}, fmt.Errorf("ipc message too large: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(x.c, buf); err != nil {
		return Message{}, err
	}
	var m Message
	if err := json.Unmarshal(buf, &m); err != nil {
		return Message{}, fmt.Errorf("ipc unmarshal: %w", err)
	}
	return m, nil
}

// SetReadDeadline 设置后续 Recv 的读取截止时间（握手超时用）。
func (x *Conn) SetReadDeadline(t time.Time) error { return x.c.SetReadDeadline(t) }

// Close 关闭底层连接。
func (x *Conn) Close() error { return x.c.Close() }
