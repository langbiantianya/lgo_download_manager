// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package grpcx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
	"lgo_download_manager/internal/rpc/grpcx/pb"
)

// streamBuffer 是消息流 channel 的缓冲条数。
const streamBuffer = 64

// Client 是 gRPC 传输的客户端。
type Client struct {
	// conn 是底层连接，cli 是它的 gRPC 桩。
	conn *grpc.ClientConn
	cli  pb.DownloadServiceClient

	// closed 标记客户端是否已关闭。
	closed atomic.Bool

	// mu 保护 streams。
	//
	// 注册与 Close 扫表必须互斥：流要是注册在 Close 扫过之后，就再没人
	// 取消它了。
	mu sync.Mutex
	// streams 是当前活动消息流。Close 靠它们把流停掉。
	streams map[*stream]struct{}

	// closeOnce 保证底层连接只关一次。
	//
	// grpc.ClientConn.Close() 第二次调用返回 ErrClientConnClosing（非 nil
	// 错误），而 rpc.Transport 的契约明确要求 Close 幂等，所以必须用
	// sync.Once 包住。
	closeOnce sync.Once
}

// stream 是一次活动消息流的状态。cancel 停掉对应的流。
type stream struct {
	cancel context.CancelFunc
}

// Dial 按地址建立 gRPC 连接。token 随每次调用发送。
//
// Dial 会用 ctx 预连接一次再立刻断开。gRPC 的连接是惰性的
// （grpc.NewClient 不建连，要等第一次 RPC），不预连接的话
// 「业务进程没起来」要等到第一次调用才暴露，调用方只会看到一个含义模糊
// 的握手失败。预连接让失败时机与 httpx 传输一致。
func Dial(ctx context.Context, a addr.Addr, token string) (*Client, error) {
	conn, err := addr.DialContext(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("grpcx: 连接 %s 失败: %w", a.Address, err)
	}
	_ = conn.Close()

	// 显式用 passthrough 解析器：grpc.NewClient 默认走 dns 解析器，
	// 而我们的地址是 unix socket / host:port，由 dialer 负责解析。
	cc, err := grpc.NewClient("passthrough:///lgdm",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer(a)),
		grpc.WithUnaryInterceptor(clientUnaryToken(token)),
		grpc.WithStreamInterceptor(clientStreamToken(token)),
	)
	if err != nil {
		return nil, fmt.Errorf("grpcx: 建立连接: %w", err)
	}
	return &Client{conn: cc, cli: pb.NewDownloadServiceClient(cc)}, nil
}

// dialer 返回 gRPC 用的建连函数。
//
// 必须用 gRPC 传进来的 ctx 建连：一次超时的调用不该留下一个还在建连的
// socket goroutine。丢掉 ctx（写成 addr.Dial）的话，建连会一路挂到操作
// 系统的 TCP 超时（默认两分钟）才返回，而不是跟着调用方的超时走。
func dialer(a addr.Addr) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		return addr.DialContext(ctx, a)
	}
}

// clientUnaryToken 给一元调用附加 token。
func clientUnaryToken(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		// metadata 的 key 会被 gRPC 规范化为小写，服务端也按小写取，
		// 两边必须一致。
		//
		// 注意 gRPC 的 metadata 值只允许可打印 ASCII：token 里有非 ASCII
		// 字符时 grpc-go 会直接报错。实际部署不受影响——业务侧的握手
		// token 是随机数的十六进制编码，纯 ASCII。
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// clientStreamToken 给流式调用附加 token。
func clientStreamToken(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return streamer(ctx, desc, cc, method, opts...)
	}
}

// Call 执行一次调用。
func (c *Client) Call(ctx context.Context, method string, in, out any) error {
	if err := c.checkOpen(); err != nil {
		return err
	}
	switch method {
	case api.MethodList:
		p, ok := asPtr[api.ListParams](in)
		if !ok {
			return badIn(method, in, "*api.ListParams")
		}
		r, ok := asPtr[api.ListResult](out)
		if !ok {
			return badOut(method, out, "*api.ListResult")
		}
		res, err := c.cli.List(ctx, listParamsToPB(p))
		if err != nil {
			return methodErrorf(method, err)
		}
		r.Tasks = nil
		for _, t := range res.GetTasks() {
			r.Tasks = append(r.Tasks, taskFromPB(t))
		}
		return nil

	case api.MethodAdd:
		p, ok := asPtr[api.AddTaskInput](in)
		if !ok {
			return badIn(method, in, "*api.AddTaskInput")
		}
		r, ok := asPtr[api.Task](out)
		if !ok {
			return badOut(method, out, "*api.Task")
		}
		res, err := c.cli.AddTask(ctx, addTaskInputToPB(p))
		if err != nil {
			return methodErrorf(method, err)
		}
		t := taskFromPB(res.GetTask())
		if t == nil {
			return fmt.Errorf("grpcx: %s: 服务端返回的任务为空", method)
		}
		*r = *t
		return nil

	case api.MethodStart:
		p, ok := asPtr[api.TaskRef](in)
		if !ok {
			return badIn(method, in, "*api.TaskRef")
		}
		if _, ok := asPtr[api.Empty](out); !ok {
			return badOut(method, out, "*api.Empty")
		}
		if _, err := c.cli.StartTask(ctx, &pb.TaskRef{Id: p.ID}); err != nil {
			return methodErrorf(method, err)
		}
		return nil

	case api.MethodPause:
		p, ok := asPtr[api.TaskRef](in)
		if !ok {
			return badIn(method, in, "*api.TaskRef")
		}
		if _, ok := asPtr[api.Empty](out); !ok {
			return badOut(method, out, "*api.Empty")
		}
		if _, err := c.cli.PauseTask(ctx, &pb.TaskRef{Id: p.ID}); err != nil {
			return methodErrorf(method, err)
		}
		return nil

	case api.MethodDelete:
		p, ok := asPtr[api.TaskRef](in)
		if !ok {
			return badIn(method, in, "*api.TaskRef")
		}
		if _, ok := asPtr[api.Empty](out); !ok {
			return badOut(method, out, "*api.Empty")
		}
		if _, err := c.cli.DeleteTask(ctx, &pb.TaskRef{Id: p.ID}); err != nil {
			return methodErrorf(method, err)
		}
		return nil

	case api.MethodIsPreparing:
		p, ok := asPtr[api.TaskRef](in)
		if !ok {
			return badIn(method, in, "*api.TaskRef")
		}
		r, ok := asPtr[api.BoolResult](out)
		if !ok {
			return badOut(method, out, "*api.BoolResult")
		}
		res, err := c.cli.IsPreparing(ctx, &pb.TaskRef{Id: p.ID})
		if err != nil {
			return methodErrorf(method, err)
		}
		r.Value = res.GetValue()
		return nil

	case api.MethodProbe:
		p, ok := asPtr[api.ProbeParams](in)
		if !ok {
			return badIn(method, in, "*api.ProbeParams")
		}
		r, ok := asPtr[api.ProbeResult](out)
		if !ok {
			return badOut(method, out, "*api.ProbeResult")
		}
		res, err := c.cli.Probe(ctx, &pb.ProbeRequest{Url: p.URL})
		if err != nil {
			return methodErrorf(method, err)
		}
		r.TotalSize = res.GetTotalSize()
		r.Filename = res.GetFilename()
		return nil

	case api.MethodGetSettings:
		if _, ok := asPtr[api.Empty](in); !ok {
			return badIn(method, in, "*api.Empty")
		}
		r, ok := asPtr[api.Settings](out)
		if !ok {
			return badOut(method, out, "*api.Settings")
		}
		res, err := c.cli.GetSettings(ctx, &pb.Empty{})
		if err != nil {
			return methodErrorf(method, err)
		}
		*r = settingsFromPB(res.GetSettings())
		return nil

	case api.MethodSaveSettings:
		p, ok := asPtr[api.Settings](in)
		if !ok {
			return badIn(method, in, "*api.Settings")
		}
		if _, ok := asPtr[api.Empty](out); !ok {
			return badOut(method, out, "*api.Empty")
		}
		if _, err := c.cli.SaveSettings(ctx, &pb.SaveSettingsRequest{Settings: settingsToPB(*p)}); err != nil {
			return methodErrorf(method, err)
		}
		return nil

	case api.MethodBTInfo:
		p, ok := asPtr[api.BTInfoParams](in)
		if !ok {
			return badIn(method, in, "*api.BTInfoParams")
		}
		r, ok := asPtr[api.BTDetail](out)
		if !ok {
			return badOut(method, out, "*api.BTDetail")
		}
		res, err := c.cli.BTTaskDetail(ctx, &pb.BTInfoRequest{TaskId: p.TaskID})
		if err != nil {
			return methodErrorf(method, err)
		}
		d := btDetailFromPB(res.GetDetail())
		if d == nil {
			return fmt.Errorf("grpcx: %s: 服务端返回的详情为空", method)
		}
		*r = *d
		return nil

	default:
		if api.IsStreamMethod(method) {
			return fmt.Errorf("grpcx: %s: 消息流请用 Stream()，不能用 Call()", method)
		}
		return fmt.Errorf("grpcx: 未知方法 %q", method)
	}
}

// Stream 打开消息流。
func (c *Client) Stream(ctx context.Context) (<-chan api.Message, error) {
	if err := c.checkOpen(); err != nil {
		return nil, err
	}
	// 流的 ctx 单独派生：Close 要能只停掉流而不影响调用方自己的 ctx。
	streamCtx, cancel := context.WithCancel(ctx)
	rpcStream, err := c.cli.StreamMessages(streamCtx, &pb.Empty{})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("grpcx: 打开消息流: %w", err)
	}
	// 登记进活动流表：Close 靠它把流停掉。
	st := &stream{cancel: cancel}
	c.mu.Lock()
	if c.closed.Load() {
		// Close 刚刚跑过：这条流没人会取消它，现在就自己收手。
		c.mu.Unlock()
		cancel()
		return nil, errors.New("grpcx: 客户端已关闭")
	}
	if c.streams == nil {
		c.streams = make(map[*stream]struct{})
	}
	c.streams[st] = struct{}{}
	c.mu.Unlock()

	out := make(chan api.Message, streamBuffer)
	go func() {
		defer close(out)
		// 退出时把自己从活动流表里摘掉，表才不会无限增长。
		defer c.clearStream(st)
		defer cancel()
		for {
			msg, err := rpcStream.Recv()
			if err != nil {
				// 流结束（服务端收手、连接断开、ctx 取消）都在这里收尾：
				// 关闭 out，读循环随之知道消息流没了。
				return
			}
			select {
			case out <- messageFromPB(msg):
			case <-streamCtx.Done():
				// ctx 被取消（Close 或调用方）：立刻收手。
				//
				// 这一支不能省：缓冲满时 goroutine 阻塞在 out <- 上，
				// 这时 conn.Close() 只让 rpcStream.Recv() 报错，管不到
				// 已经卡在发送上的 goroutine——不取消 ctx 它就永久泄漏。
				return
			}
		}
	}()
	return out, nil
}

// Close 释放底层连接与流。必须幂等——Close 可能被重复调用。
//
// 先取消所有活动消息流，再关底层连接。只关连接是不够的：消息流的
// goroutine 可能正阻塞在往缓冲通道发送上（缓冲 64 条满了、调用方消费
// 不过来），那时 conn.Close() 只会让流的下一次 Recv 报错，管不到已经卡在
// 发送上的 goroutine——不取消流的 ctx，它就会永久泄漏。
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed.Store(true)
		sts := make([]*stream, 0, len(c.streams))
		for st := range c.streams {
			sts = append(sts, st)
		}
		c.streams = nil
		c.mu.Unlock()
		for _, st := range sts {
			st.cancel()
		}
		if c.conn != nil {
			_ = c.conn.Close()
		}
	})
	return nil
}

// clearStream 清掉已结束的活动流，避免活动流表无限增长。
func (c *Client) clearStream(st *stream) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.streams, st)
}

// checkOpen 检查客户端是否已关闭或未初始化。
func (c *Client) checkOpen() error {
	if c == nil {
		return errors.New("grpcx: 客户端未初始化，请用 Dial 创建")
	}
	if c.closed.Load() {
		return errors.New("grpcx: 客户端已关闭")
	}
	// 零值客户端的 conn 是 nil，直接发 RPC 会 nil 指针解引用 panic。
	// Client 是导出类型，零值是被允许的构造物，这里要回错误而不是炸进程。
	if c.conn == nil {
		return errors.New("grpcx: 客户端未初始化，请用 Dial 创建")
	}
	return nil
}

// asPtr 把 any 转成指定类型的指针，并报告是否转换成功。
//
// 类型不对必须回错误而不是 panic：gRPC 这边直接解引用就是 panic。
//
// 两种传输的校验时机相同——都在**客户端**校验、请求发不出去：
// httpx 那边由 Client.Call 开头的 checkCallTypes 拦（类型不对直接回错，
// 不会走到服务端），gRPC 这边由这里的 asPtr 拦。
func asPtr[T any](v any) (*T, bool) {
	p, ok := v.(*T)
	return p, ok
}

// badIn 构造「请求类型不对」的错误。
func badIn(method string, got any, want string) error {
	return fmt.Errorf("grpcx: %s: 请求类型错误: 期望 %s, 实际 %T", method, want, got)
}

// badOut 构造「响应类型不对」的错误。
func badOut(method string, got any, want string) error {
	return fmt.Errorf("grpcx: %s: 响应类型错误: 期望 %s, 实际 %T", method, want, got)
}
