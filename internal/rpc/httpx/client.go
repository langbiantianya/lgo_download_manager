// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package httpx 是基于标准库 net/http 的传输实现：REST 风格路径 +
// JSON 正文，消息流用 NDJSON 逐行推送。
//
// 只用 net/http，不接 x/net/http2 的 h2c 手工接线——传输层跑在本机
// socket 上，不需要 HTTP/2 的多路复用。
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"sync"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
)

// servicePrefix 是所有业务端点的路径前缀。
const servicePrefix = "/lgdm.v1.DownloadService/"

// maxErrorBody 是错误响应体最多读取的字节数。
const maxErrorBody = 4096

// streamBuffer 是消息流 channel 的缓冲条数。
const streamBuffer = 64

// Client 是 net/http 传输的客户端。
type Client struct {
	base  string
	token string
	http  *http.Client

	// transport 是底层的 http.Transport，Close 时用来掐断空闲连接。
	transport *http.Transport

	// mu 保护 closed 与 streams。
	mu     sync.Mutex
	closed bool
	// streams 是当前活动消息流。Close 靠它们把流停掉。
	streams map[*stream]struct{}

	closeOnce sync.Once
}

// stream 是一次活动消息流的状态。cancel 停掉对应的 HTTP 请求。
type stream struct {
	cancel context.CancelFunc
}

// Dial 按地址建立 HTTP 客户端。token 随每次请求发送。
//
// Dial 会用 ctx 预连接一次再立刻断开。HTTP 是惰性连接——不预连接的话
// 「业务进程没起来」要等到第一次调用才暴露，调用方只会看到一个含义模糊的
// 握手失败。预连接让失败时机与 gRPC 传输一致。
func Dial(ctx context.Context, a addr.Addr, token string) (*Client, error) {
	conn, err := addr.DialContext(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("httpx: 连接 %s 失败: %w", a.Address, err)
	}
	_ = conn.Close()

	host := a.Address
	if a.Network == "unix" {
		// unix socket 没有主机名，用一个占位主机名即可。
		host = "lgdm"
	}
	tr := &http.Transport{
		// 用请求自己的 ctx 建连：一次超时的调用不该留下还在建连的 socket。
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return addr.DialContext(ctx, a)
		},
	}
	return &Client{
		base:      "http://" + host + servicePrefix,
		token:     token,
		http:      &http.Client{Transport: tr},
		transport: tr,
	}, nil
}

// Call 执行一次调用。
//
// 调用前先按 api.Spec 校验入参与出参的 Go 类型。HTTP 走 JSON，类型信息在
// 编码时被擦掉：入参类型不对时，服务端照样会按规格解码出一个零值对象；
// 出参类型不对时，服务端的响应会被解码进一个形状不合的对象里。两种情况
// 都是「静默成功」——调用方看不出自己传错了类型，错误要到业务逻辑不对的
// 时候才暴露。gRPC 侧靠 Go 类型断言当场报错（grpcx.asPtr），这里在序列化
// 之前补上同样的检查，让两种传输的行为一致。
func (c *Client) Call(ctx context.Context, method string, in, out any) error {
	if err := c.checkOpen(); err != nil {
		return err
	}
	if err := checkCallTypes(method, in, out); err != nil {
		return err
	}
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("httpx: 编码 %s 请求: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("httpx: 构造 %s 请求: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("httpx: %s 请求失败: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return fmt.Errorf("httpx: %s: %s", method, describeError(resp.StatusCode, raw))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("httpx: 解码 %s 响应: %w", method, err)
	}
	return nil
}

// checkCallTypes 按 api.Spec 校验入参与出参的 Go 类型。
//
// 错误信息带方法名、期望类型与实际类型，方便直接定位是哪个方法的哪个
// 参数传错了。
//
// 两个边界都是有意这么定的：
//
//   - out == nil 表示「调用方不关心结果」，不是「传错了类型」。调用照常
//     发出，响应体读了就扔（Call 里 out == nil 的提前返回分支）。没有类型
//     可比，所以不做校验。
//   - 未知方法（api.Spec 返回 nil）没有规格可比对，这里**不**报错，放行
//     给服务端判——服务端的兜底路由会回 404「未知路径」，错误照样传回
//     调用方。路由的真相在服务端，客户端不替它猜。
func checkCallTypes(method string, in, out any) error {
	spec := api.Spec(method)
	if spec == nil || spec.NewIn == nil || spec.NewOut == nil {
		// 未知方法：没有规格可比对，放行给服务端判（它会回 404）。
		return nil
	}
	wantIn := reflect.TypeOf(spec.NewIn())
	if reflect.TypeOf(in) != wantIn {
		return fmt.Errorf("httpx: %s: 请求类型错误: 期望 %s, 实际 %T", method, wantIn, in)
	}
	if out == nil {
		return nil
	}
	wantOut := reflect.TypeOf(spec.NewOut())
	if reflect.TypeOf(out) != wantOut {
		return fmt.Errorf("httpx: %s: 响应类型错误: 期望 %s, 实际 %T", method, wantOut, out)
	}
	return nil
}

// Stream 打开消息流。响应是 NDJSON，每行一个 api.Message。
func (c *Client) Stream(ctx context.Context) (<-chan api.Message, error) {
	if err := c.checkOpen(); err != nil {
		return nil, err
	}
	// 流的 ctx 单独派生：Close 要能只停掉流而不影响调用方自己的 ctx。
	streamCtx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(streamCtx, http.MethodPost, c.base+api.MethodStreamMessages, nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("httpx: 构造消息流请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/x-ndjson")
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("httpx: 打开消息流: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("httpx: 打开消息流: %s", describeError(resp.StatusCode, raw))
	}

	st := &stream{cancel: cancel}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		resp.Body.Close()
		return nil, errors.New("httpx: 客户端已关闭")
	}
	if c.streams == nil {
		c.streams = make(map[*stream]struct{})
	}
	c.streams[st] = struct{}{}
	c.mu.Unlock()

	out := make(chan api.Message, streamBuffer)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		defer c.clearStream(st)
		defer cancel()
		dec := json.NewDecoder(resp.Body)
		for {
			var msg api.Message
			if err := dec.Decode(&msg); err != nil {
				return
			}
			select {
			case out <- msg:
			case <-streamCtx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Close 释放底层连接与流。必须幂等——Close 可能被重复调用。
//
// 除了掐断空闲连接，还要取消活动消息流：正在跑的 NDJSON 响应不会因为
// 连接池被清空而结束，读循环会一直挂着。
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		sts := make([]*stream, 0, len(c.streams))
		for st := range c.streams {
			sts = append(sts, st)
		}
		c.streams = nil
		c.mu.Unlock()
		for _, st := range sts {
			st.cancel()
		}
		if c.transport != nil {
			c.transport.CloseIdleConnections()
		}
	})
	return nil
}

// checkOpen 检查客户端是否已关闭或未初始化。
func (c *Client) checkOpen() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("httpx: 客户端已关闭")
	}
	// 零值客户端的 http 字段是 nil，直接 Do 会 nil 指针解引用 panic。
	// Client 是导出类型，零值是被允许的构造物，这里要回错误而不是炸进程。
	if c.http == nil {
		return errors.New("httpx: 客户端未初始化，请用 Dial 创建")
	}
	return nil
}

// clearStream 清掉已结束的活动流，避免 Close 去取消一个已经结束的流。
func (c *Client) clearStream(st *stream) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.streams, st)
}

// describeError 把错误响应体翻译成一句人话。服务端的错误体形状固定为
// {"ok":false,"error":"..."}，这里按 error 字段解析；解析不出来就退回
// HTTP 状态码。
func describeError(status int, raw []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	return fmt.Sprintf("HTTP %d", status)
}
