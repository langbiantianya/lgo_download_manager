// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package rpc

import (
	"context"
	"fmt"

	"lgo_download_manager/internal/rpc/addr"
	"lgo_download_manager/internal/rpc/grpcx"
	"lgo_download_manager/internal/rpc/httpx"
)

// 传输协议名。
const (
	TransportGRPC = "grpc"
	TransportHTTP = "http"
)

// ServerOptions 描述一次服务端的启动参数。
type ServerOptions struct {
	// Transport 是传输协议名；为空时按 TransportGRPC 处理。
	Transport string
	// Addr 是监听地址，见 addr.Parse。
	Addr string
	// Token 是握手 token，每次调用都会校验。
	Token string
}

// Serve 按 opts 启动服务。handler 的实现由业务侧提供。
func Serve(ctx context.Context, opts ServerOptions, h Handler) error {
	a, err := addr.Parse(opts.Addr)
	if err != nil {
		return err
	}
	switch opts.Transport {
	case "", TransportGRPC:
		return grpcx.Serve(ctx, a, opts.Token, h)
	case TransportHTTP:
		return httpx.Serve(ctx, a, opts.Token, h)
	default:
		return fmt.Errorf("rpc: 未知传输协议 %q（可选：%s、%s）", opts.Transport, TransportGRPC, TransportHTTP)
	}
}

// Dial 连接到 endpoint 并返回客户端。endpoint 的格式与 addr.Parse 相同。
//
// ctx 应当是贯穿整个会话的 ctx：握手那一次调用用的就是它。两种传输都在
// Dial 阶段预连接一次，所以「业务进程没起来」在这里就暴露，而不是等到
// 第一次调用。
func Dial(ctx context.Context, transport, endpoint, token string) (Client, error) {
	a, err := addr.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	var tr Transport
	switch transport {
	case "", TransportGRPC:
		tr, err = grpcx.Dial(ctx, a, token)
	case TransportHTTP:
		tr, err = httpx.Dial(ctx, a, token)
	default:
		return nil, fmt.Errorf("rpc: 未知传输协议 %q（可选：%s、%s）", transport, TransportGRPC, TransportHTTP)
	}
	if err != nil {
		return nil, err
	}
	c, err := newClient(ctx, tr)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// 编译期断言：两种传输实现都满足 Transport。
var (
	_ Transport = (*grpcx.Client)(nil)
	_ Transport = (*httpx.Client)(nil)
)
