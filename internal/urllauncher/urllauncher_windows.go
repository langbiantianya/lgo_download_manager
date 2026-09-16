// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build windows

package urllauncher

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// Windows 侧由两部分组成:
//
//  1. 单实例锁。用 per-session 命名互斥体,而不是 flock(2)(Windows
//     没有)。互斥体名与命名管道名同源,都从 lockPath 推导:
//       - 内核命名空间必须选 `Local\`:`Global\` 需要
//         SeCreateGlobalPrivilege(只有服务有),普通交互用户拿到的是
//         ERROR_PATH_NOT_FOUND。`Local\` 是 per-session 命名空间,
//         任何用户都能用。
//       - NT 对象名里的反斜杠是命名空间分隔符,所以不能把 lockPath
//         (含 C:\...) 原样嵌进去,必须先拍平。
//  2. URL 转发。主实例在 `\\.\pipe\<instanceName>` 上监听,次级实例
//     把 lgom:// URL 写进去后退出 —— 与 Unix 侧的 Unix domain socket
//     语义一致,线上格式也共用(见 urllauncher.go)。
//
// 管道句柄由创建者进程的默认 DACL 保护(当前用户 + SYSTEM +
// Administrators),同机其他用户无法投递 URL;管道还带
// PIPE_REJECT_REMOTE_CLIENTS,拒绝远程连接。

const (
	// kernelObjectPrefix 是互斥体与管道名共用的前缀。
	kernelObjectPrefix = "lgo_dm_instance_"

	// pipePathPrefix 是 Win32 命名管道的路径前缀。
	pipePathPrefix = `\\.\pipe\`

	// pipeBufSize 是内核为管道分配的单向缓冲区。URL 帧只有几十到
	// 几百字节,4 KiB 绰绰有余。
	pipeBufSize = 4096

	// frameReadTimeout 是服务端读完一整帧的上限:客户端连上却不发
	// 数据时,不能让它永久占住 accept 循环。
	frameReadTimeout = 5 * time.Second

	// dialRetryDelay 是 SendURL 两次尝试之间的间隔。
	dialRetryDelay = 50 * time.Millisecond
)

// dialRetryWindow 是 SendURL 等待管道就绪的总时长。主实例串行 accept,
// 在「处理完一条连接」与「创建下一个管道实例」之间有极短的空窗期,
// 次级实例启动时也可能比主实例的管道早几毫秒,因此需要有限重试。
// 声明成变量是为了让测试把它缩短。
var dialRetryWindow = 2 * time.Second

// defaultBaseDir 返回 Windows 平台的用户级数据目录。这里不落盘,
// 只作为互斥体/管道名的推导来源。
func defaultBaseDir() string {
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return filepath.Join(dir, "lgo_download_manager")
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "lgo_download_manager")
	}
	return filepath.Join(os.TempDir(), "lgo_download_manager")
}

// instanceName 把 lockPath 拍平成一个 NT 对象名,互斥体与管道共用。
//
// Format: "lgo_dm_instance_<sanitized>"
//
// Sanitization rules:
//   - Strip Windows drive letter + colon ("C:" → "").
//   - Replace every path separator '\' with '_'.
//   - Keep only [A-Za-z0-9_-]; everything else becomes '_'.
//   - Collapse runs of '_'.
//   - Trim leading/trailing '_'.
//   - Hard-cap at 200 chars to stay well under the 260-char NT path limit.
//
// The result is stable for a given data directory and unique across users
// (each user has a different profile path and therefore a different
// sanitized string), but identical for repeated invocations by the same
// user.
func instanceName() string {
	const maxTail = 200

	p := lockPath
	if len(p) >= 2 && p[1] == ':' {
		p = p[2:] // strip drive letter "C:" / "D:" / …
	}
	var b strings.Builder
	b.Grow(len(p))
	lastUnderscore := false
	for _, r := range p {
		switch {
		case r == '\\' || r == '/':
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '.':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	tail := strings.Trim(b.String(), "_")
	if len(tail) > maxTail {
		tail = tail[:maxTail]
	}
	return kernelObjectPrefix + tail
}

// mutexKernelName 是单实例互斥体的内核对象名。
func mutexKernelName() string { return `Local\` + instanceName() }

// pipeKernelName 是 URL 转发命名管道的完整路径。
func pipeKernelName() string { return pipePathPrefix + instanceName() }

// AcquireLock uses a Windows per-session named mutex so the single-instance
// guarantee works on Windows even though flock(2) is unavailable.
//
// Returns:
//   - isPrimary=true, release!=nil, err=nil  → this instance is the primary.
//   - isPrimary=false, release=nil, err=nil  → another instance already holds
//     the lock; the caller should act as a forwarder.
//   - isPrimary=false, release=nil, err!=nil  → an OS-level error occurred.
func AcquireLock() (isPrimary bool, release func(), err error) {
	namePtr, err := windows.UTF16PtrFromString(mutexKernelName())
	if err != nil {
		return false, nil, fmt.Errorf("urllauncher: invalid mutex name: %w", err)
	}

	// SECURITY_ATTRIBUTES defaults: created in the per-session Local\
	// namespace without an inheritable or restrictive DACL. Any process in
	// the same user session can probe ownership; that is sufficient for the
	// single-instance contract and avoids the SeCreateGlobalPrivilege
	// requirement of Global\.
	handle, err := windows.CreateMutex(nil, false, namePtr)
	if err != nil {
		if errno, ok := err.(windows.Errno); ok && errno == windows.ERROR_ALREADY_EXISTS {
			// CreateMutex still returns a valid handle to the existing mutex;
			// we own a reference and must release it before returning.
			windows.CloseHandle(handle)
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("urllauncher: CreateMutex: %w", err)
	}

	// We hold the handle. Keep it alive until release() runs.
	return true, func() {
		windows.CloseHandle(handle)
	}, nil
}

// ListenAndServe creates the per-user named pipe and calls onURL for every
// URL received on it. It blocks until an unrecoverable error occurs.
//
// 连接是串行处理的:URL 转发是低频事件,串行既能省掉实例池,也避免了
// 多个转发同时改下载队列的额外同步。客户端连上却不发数据由
// frameReadTimeout 兜底,不会卡住后续转发。
func ListenAndServe(onURL func(url string)) error {
	namePtr, err := windows.UTF16PtrFromString(pipeKernelName())
	if err != nil {
		return fmt.Errorf("urllauncher: invalid pipe name: %w", err)
	}

	for {
		h, err := windows.CreateNamedPipe(
			namePtr,
			windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|
				windows.PIPE_REJECT_REMOTE_CLIENTS,
			windows.PIPE_UNLIMITED_INSTANCES,
			pipeBufSize, pipeBufSize,
			0, nil,
		)
		if err != nil {
			return fmt.Errorf("urllauncher: CreateNamedPipe: %w", err)
		}

		conn, err := newPipeConn(h)
		if err != nil {
			windows.CloseHandle(h)
			return err
		}

		if err := conn.connect(); err != nil {
			conn.Close()
			windows.CloseHandle(h)
			if errors.Is(err, windows.ERROR_NO_DATA) {
				// 客户端连上后立刻断开(它可能只是探测一下),不是
				// 致命错误,继续等下一个。
				continue
			}
			return fmt.Errorf("urllauncher: ConnectNamedPipe: %w", err)
		}

		if rawURL, err := readURLFrame(conn); err == nil && rawURL != "" {
			onURL(rawURL)
		}

		conn.Close()
		// 客户端可能已经断开,此时 DisconnectNamedPipe 会报
		// ERROR_PIPE_NOT_CONNECTED;句柄随即关闭,无需处理。
		_ = windows.DisconnectNamedPipe(h)
		windows.CloseHandle(h)
	}
}

// SendURL connects to the primary instance's pipe and sends the URL with the
// same length-prefixed JSON frame the Unix implementation uses.
func SendURL(url string) error {
	namePtr, err := windows.UTF16PtrFromString(pipeKernelName())
	if err != nil {
		return fmt.Errorf("urllauncher: invalid pipe name: %w", err)
	}

	h, err := dialPipe(namePtr)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)

	// 客户端句柄不带 FILE_FLAG_OVERLAPPED,WriteFile 同步完成即可:
	// 帧只有几百字节,远小于管道缓冲区。
	return writeURLFrame(pipeWriter{h}, url)
}

// dialPipe 打开主实例的管道实例。主实例串行 accept,两次
// CreateNamedPipe 之间存在极短的无监听者窗口,次级实例也可能比主实例
// 的管道先启动,因此对「管道不存在」做有限重试;超时即认为主实例其实
// 没在跑(或者已经退出),把错误交回调用方。
func dialPipe(namePtr *uint16) (windows.Handle, error) {
	deadline := time.Now().Add(dialRetryWindow)
	for {
		h, err := windows.CreateFile(
			namePtr,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0, nil,
			windows.OPEN_EXISTING,
			0, 0,
		)
		if err == nil {
			return h, nil
		}
		if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) &&
			!errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return 0, fmt.Errorf("urllauncher: open pipe %s: %w", pipeKernelName(), err)
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("urllauncher: no primary instance listening on %s: %w",
				pipeKernelName(), err)
		}
		time.Sleep(dialRetryDelay)
	}
}

// pipeConn 把 overlapped 模式的管道句柄包装成 io.Reader,并给读取加
// 上超时。超时靠 CancelIoEx 取消挂起的 I/O:句柄在超时后会被立即
// 断开并关闭,不必复用。
type pipeConn struct {
	h  windows.Handle
	ev windows.Handle
	ov windows.Overlapped
}

func newPipeConn(h windows.Handle) (*pipeConn, error) {
	// manual-reset event:一次完成信号可以被 GetOverlappedResult 与
	// 后续的等待顺序消费。
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("urllauncher: CreateEvent: %w", err)
	}
	return &pipeConn{h: h, ev: ev}, nil
}

func (c *pipeConn) Close() error {
	return windows.CloseHandle(c.ev)
}

// cancelPending 取消一次挂起的 overlapped 操作并通知内核尽快回收。
func (c *pipeConn) cancelPending() {
	_ = windows.CancelIoEx(c.h, &c.ov)
}

// wait 等待 c.ev 被置位,超时返回 os.ErrDeadlineExceeded。
func (c *pipeConn) wait(timeoutMillis uint32) error {
	ev, err := windows.WaitForSingleObject(c.ev, timeoutMillis)
	if err != nil {
		return fmt.Errorf("urllauncher: WaitForSingleObject: %w", err)
	}
	if ev == uint32(windows.WAIT_TIMEOUT) {
		return os.ErrDeadlineExceeded
	}
	return nil
}

// connect 等待客户端接入。CreateNamedPipe 之后立即调用,所以正常情况下
// 返回 ERROR_IO_PENDING,由事件对象唤醒;客户端抢在 ConnectNamedPipe
// 之前连上时返回 ERROR_PIPE_CONNECTED,同样算成功。
func (c *pipeConn) connect() error {
	c.ov = windows.Overlapped{HEvent: c.ev}
	err := windows.ConnectNamedPipe(c.h, &c.ov)
	switch {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		return nil
	case errors.Is(err, windows.ERROR_IO_PENDING):
		if werr := c.wait(windows.INFINITE); werr != nil {
			return werr
		}
		var done uint32
		return windows.GetOverlappedResult(c.h, &c.ov, &done, false)
	default:
		return err
	}
}

// Read 从管道读取字节;超过 frameReadTimeout 未读到数据即超时。
func (c *pipeConn) Read(p []byte) (int, error) {
	c.ov = windows.Overlapped{HEvent: c.ev}
	var n uint32
	err := windows.ReadFile(c.h, p, &n, &c.ov)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		if werr := c.wait(uint32(frameReadTimeout / time.Millisecond)); werr != nil {
			c.cancelPending()
			return 0, werr
		}
		err = windows.GetOverlappedResult(c.h, &c.ov, &n, false)
	}
	if err != nil {
		if errors.Is(err, windows.ERROR_BROKEN_PIPE) {
			return 0, io.EOF
		}
		return 0, err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return int(n), nil
}

// pipeWriter 把同步(非 overlapped)管道句柄适配成 io.Writer。
type pipeWriter struct {
	h windows.Handle
}

func (w pipeWriter) Write(p []byte) (int, error) {
	var n uint32
	if err := windows.WriteFile(w.h, p, &n, nil); err != nil {
		return int(n), err
	}
	return int(n), nil
}
