// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package protocol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	ftp "github.com/jlaffaye/ftp"
)

// ftpDriver 为 FTP 实现 ProtocolDriver。每个分块 goroutine 持有
// 自己的 ServerConn，因为 FTP 数据连接在单次传输中是独占的；
// 共享同一条连接会把分块串行化。所有 ServerConn 使用同一组
// 凭据登录；由 Probe 探测可用性。
//
// 我们有意将连接池并发数限制为分块数上限，以避免资源泄漏。
type ftpDriver struct {
	host     string // host:port
	path     string // 远程路径
	auth     AuthOptions
	mu       sync.Mutex
	reserved bool // 服务器是否对 REST 0 响应了 350
}

// newFTPDriver 是工厂入口。
func newFTPDriver(raw string, auth AuthOptions) (ProtocolDriver, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("ftp parse: %w", err)
	}
	if u.Scheme != "ftp" {
		return nil, fmt.Errorf("ftp: bad scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("ftp: empty host")
	}
	host := u.Host
	if !hasPort(host) {
		host += ":21"
	}
	return &ftpDriver{
		host: host,
		path: strings.TrimPrefix(u.Path, "/"),
		auth: auth,
	}, nil
}

func hasPort(s string) bool {
	return strings.Contains(s, ":")
}

// dial 使用配置的凭据登录。PASV 由 AuthOptions.FTPPassive 通过
// DialWithDisabledEPSV 控制（在我们这里，主动模式是 "PASV" 的反面；
// FTPPassive 为 true（默认值）时保持 EPSV/PASV 开启）。
func (d *ftpDriver) dial(_ context.Context) (*ftp.ServerConn, error) {
	opts := []ftp.DialOption{ftp.DialWithTimeout(10 * time.Second)}
	if !d.auth.FTPPassive {
		// 主动模式：要求客户端禁用所有服务器端的被动模式变体，
		// 从而让 RETR 触发服务器回连到我们。

		opts = append(opts, ftp.DialWithDisabledEPSV(true))
	}
	conn, err := ftp.Dial(d.host, opts...)
	if err != nil {
		return nil, fmt.Errorf("ftp dial: %w", err)
	}
	user := d.auth.Username
	pass := d.auth.Password
	if user == "" {
		user = "anonymous"
	}
	if pass == "" {
		pass = "anonymous@"
	}
	if err := conn.Login(user, pass); err != nil {
		_ = conn.Quit()
		return nil, fmt.Errorf("ftp login: %w", err)
	}
	return conn, nil
}

// Probe 使用 SIZE 获取总大小，并通过 REST 0 探测服务器是否
// 支持断点续传（RFC 959）。350 表示支持；502 表示不支持。
func (d *ftpDriver) Probe(ctx context.Context) (*DriverCapabilities, error) {
	conn, err := d.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Quit() }()

	// 切换为二进制模式以进行 size/retr。
	if err := conn.Type("I"); err != nil {
		return nil, fmt.Errorf("ftp TYPE I: %w", err)
	}

	caps := &DriverCapabilities{ServerInfo: fmt.Sprintf("FTP %s", d.host)}

	if size, err := conn.FileSize(d.path); err == nil {
		caps.TotalSize = size
	} else {
		// 不支持 SIZE 的情况较为罕见；视为未知。
		caps.TotalSize = -1
	}
	// 探测 RESUME：在探测连接上发送 REST 0。
	d.mu.Lock()
	probe, err := dialRaw(d.host)
	if err == nil {
		_, msg, cerr := probe.cmd(-1, "REST 0")
		if cerr == nil && strings.HasPrefix(msg, "350") {
			d.reserved = true
		}
		probe.Close()
	}
	d.mu.Unlock()
	caps.SupportRange = d.reserved

	return caps, nil
}

// dialRaw 打开一个被动的文本模式连接，仅用于发送一条 REST 命令。
// 由 Probe 使用，以便干净地测试断点续传支持。
func dialRaw(addr string) (*rawConn, error) {
	c, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	rc := &rawConn{c: c, rd: c}
	rc.welcome()
	return rc, nil
}

type rawConn struct {
	c  net.Conn
	rd io.Reader
}

// welcome 读取 220 banner。后续的读/写都按行协议进行。
func (r *rawConn) welcome() {
	buf := make([]byte, 1024)
	_, _ = r.rd.Read(buf)
}

// readReply 返回下一行状态及其延续行，直到遇到形如 "NNN "（空格）的
// 终结符为止。
func (r *rawConn) readReply() (string, error) {
	var sb strings.Builder
	tmp := make([]byte, 1024)
	for {
		n, err := r.rd.Read(tmp)
		if err != nil {
			return sb.String(), err
		}
		sb.Write(tmp[:n])
		s := sb.String()
		if l := strings.Split(s, "\r\n"); len(l) >= 2 && len(l[len(l)-2]) >= 4 {
			line := l[len(l)-2]
			if line[3] == ' ' {
				return strings.TrimSpace(s), nil
			}
		}
	}
}

// cmd 发送单条命令并返回响应文本。探测场景下 expected 取 -1（任意）。
func (r *rawConn) cmd(_ int, format string, args ...interface{}) (int, string, error) {
	line := fmt.Sprintf(format, args...) + "\r\n"
	if _, err := r.c.Write([]byte(line)); err != nil {
		return 0, "", err
	}
	reply, err := r.readReply()
	if err != nil {
		return 0, "", err
	}
	code := 0
	if len(reply) >= 3 {
		if n, err := fmt.Sscanf(reply[:3], "%d", &code); err != nil || n != 1 {
			code = -1
		}
	}
	return code, reply, nil
}

func (r *rawConn) Close() error { return r.c.Close() }

// connFor 为单个分块传输打开一次全新的登录。
func (d *ftpDriver) connFor(ctx context.Context) (*ftp.ServerConn, error) {
	return d.dial(ctx)
}

// DownloadChunk 打开一条专用的控制连接，发送 REST start，再发 RETR，
// 然后将响应体从给定偏移开始写入 file。
func (d *ftpDriver) DownloadChunk(ctx context.Context, start, end int64, file *os.File, onData func(n int)) error {
	conn, err := d.connFor(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = conn.Quit()
	}()

	if err := conn.Type("I"); err != nil {
		return fmt.Errorf("ftp TYPE I: %w", err)
	}

	// 许多 FTP 客户端通过发送 REST 把 RETR 限制为「总大小以下的部分」。
	// 设置起始偏移；部分服务器在此处还期望收到高位字节。
	r, err := conn.RetrFrom(d.path, uint64(start))
	if err != nil {
		// 不支持 REST：直接失败，由引擎选择回退。
		return fmt.Errorf("ftp RETR/REST: %w", err)
	}
	defer r.Close()

	buf := make([]byte, chunkSize)
	off := start
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, werr := file.WriteAt(buf[:n], off); werr != nil {
				return fmt.Errorf("ftp chunk write: %w", werr)
			}
			off += int64(n)
			if onData != nil {
				onData(n)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("ftp chunk read: %w", rerr)
		}
		if off > end+1 {
			break
		}
	}
	return nil
}

// DownloadFallback 是一条不带续传的纯 RETR。当服务器不支持 REST 时使用。
func (d *ftpDriver) DownloadFallback(ctx context.Context, offset int64, file *os.File, onData func(n int)) error {
	conn, err := d.connFor(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = conn.Quit()
	}()

	if err := conn.Type("I"); err != nil {
		return fmt.Errorf("ftp TYPE I: %w", err)
	}

	r, err := conn.Retr(d.path)
	if err != nil {
		return fmt.Errorf("ftp RETR: %w", err)
	}
	defer r.Close()

	buf := make([]byte, chunkSize)
	off := offset
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, werr := file.WriteAt(buf[:n], off); werr != nil {
				return fmt.Errorf("ftp fallback write: %w", werr)
			}
			off += int64(n)
			if onData != nil {
				onData(n)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("ftp fallback read: %w", rerr)
		}
	}
	return nil
}

// Close 释放 reserved 标志所使用的互斥锁。连接资源按调用粒度持有
// （按需登录/登出）。
func (d *ftpDriver) Close() error {
	return nil
}
