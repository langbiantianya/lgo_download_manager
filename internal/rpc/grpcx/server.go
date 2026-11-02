// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package grpcx 是基于 gRPC 的传输实现：类型化 RPC + protobuf 载荷。
//
// 与 httpx 并列，两者是同一个契约层（internal/api）的两种线格式实现。
// 本包只依赖 internal/api 与 internal/rpc/addr，不依赖 internal/rpc
// 本身——否则会形成 rpc → grpcx → rpc 的包环。业务侧能力的接口在这里
// 独立声明一份，方法集与 rpc.Handler 逐字一致，靠结构化类型对接。
package grpcx

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
	"lgo_download_manager/internal/rpc/grpcx/pb"
)

// Handler 是 grpcx 需要的业务侧能力。方法集与 internal/rpc 里的
// rpc.Handler 逐字一致，靠结构化类型与它对接——这里独立声明是为了避开
// grpcx → rpc 的包环（rpc → grpcx 已经存在）。
type Handler interface {
	// Call 执行一次调用。method 取 api.Method* 常量；in / out 的具体类型
	// 由 api.Spec(method) 决定，传输层分配好再传进来。
	Call(ctx context.Context, method string, in, out any) error

	// Events 返回进度事件流与退订函数。
	Events() (<-chan api.Event, func())

	// Controls 返回控制消息流（显示窗口 / 弹对话框 / 语言变更 / 退出）。
	Controls() <-chan api.Message
}

// Serve 在地址上启动 gRPC 服务。ctx 取消时服务关闭。
func Serve(ctx context.Context, a addr.Addr, token string, h Handler) error {
	ln, cleanup, err := addr.Listen(a)
	if err != nil {
		return err
	}
	defer cleanup()

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(unaryAuth(token)),
		grpc.StreamInterceptor(streamAuth(token)),
	)
	pb.RegisterDownloadServiceServer(srv, &server{h: h})

	// done 用来收尾监视 goroutine：Serve 返回后它就退出，不留悬挂 goroutine。
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			// 必须用 Stop 而不是 GracefulStop：GracefulStop 会等所有在跑
			// 的 RPC 结束，而 StreamMessages 是长连接流，客户端不主动断开
			// 它就永远不会结束——那样 Serve 会永久阻塞。Stop 立即取消所有
			// 活跃的 RPC 与流，与 httpx 的 srv.Close() 语义一致。
			srv.Stop()
		case <-done:
		}
	}()

	err = srv.Serve(ln)
	if errors.Is(err, grpc.ErrServerStopped) {
		return nil
	}
	return err
}

// unaryAuth 校验一元调用的 token。
func unaryAuth(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !checkToken(ctx, token) {
			return nil, status.Error(codes.Unauthenticated, "无效的 token")
		}
		return handler(ctx, req)
	}
}

// streamAuth 校验流式调用的 token。
func streamAuth(token string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !checkToken(ss.Context(), token) {
			return status.Error(codes.Unauthenticated, "无效的 token")
		}
		return handler(srv, ss)
	}
}

// checkToken 从 metadata 里取 Bearer token 并比对。
//
// metadata 的 key 会被 gRPC 规范化为小写，所以这里必须写小写
// "authorization"——写 "Authorization" 会取不到。
func checkToken(ctx context.Context, token string) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return false
	}
	const prefix = "Bearer "
	v := vals[0]
	if len(v) <= len(prefix) || !strings.HasPrefix(v, prefix) {
		return false
	}
	return v[len(prefix):] == token
}

// server 把生成的 gRPC 接口转成对业务侧契约的调用。
type server struct {
	pb.UnimplementedDownloadServiceServer
	h Handler
}

// internalErr 把业务侧错误转成 gRPC 状态错误。
//
// gRPC 的 status 里放不进 Go 的错误链，业务错误的原因只能靠 message
// 传递——这与 httpx 把错误写进响应体的做法等价。
func internalErr(err error) error {
	return status.Error(codes.Internal, err.Error())
}

func (s *server) List(ctx context.Context, req *pb.ListRequest) (*pb.ListResponse, error) {
	out := &api.ListResult{}
	if err := s.h.Call(ctx, api.MethodList, listParamsFromPB(req), out); err != nil {
		return nil, internalErr(err)
	}
	tasks := make([]*pb.Task, 0, len(out.Tasks))
	for _, t := range out.Tasks {
		tasks = append(tasks, taskToPB(t))
	}
	return &pb.ListResponse{Tasks: tasks}, nil
}

func (s *server) AddTask(ctx context.Context, req *pb.AddTaskRequest) (*pb.AddTaskResponse, error) {
	out := &api.Task{}
	if err := s.h.Call(ctx, api.MethodAdd, addTaskInputFromPB(req), out); err != nil {
		return nil, internalErr(err)
	}
	return &pb.AddTaskResponse{Task: taskToPB(out)}, nil
}

func (s *server) StartTask(ctx context.Context, req *pb.TaskRef) (*pb.Empty, error) {
	if err := s.h.Call(ctx, api.MethodStart, &api.TaskRef{ID: req.GetId()}, &api.Empty{}); err != nil {
		return nil, internalErr(err)
	}
	return &pb.Empty{}, nil
}

func (s *server) PauseTask(ctx context.Context, req *pb.TaskRef) (*pb.Empty, error) {
	if err := s.h.Call(ctx, api.MethodPause, &api.TaskRef{ID: req.GetId()}, &api.Empty{}); err != nil {
		return nil, internalErr(err)
	}
	return &pb.Empty{}, nil
}

func (s *server) DeleteTask(ctx context.Context, req *pb.TaskRef) (*pb.Empty, error) {
	if err := s.h.Call(ctx, api.MethodDelete, &api.TaskRef{ID: req.GetId()}, &api.Empty{}); err != nil {
		return nil, internalErr(err)
	}
	return &pb.Empty{}, nil
}

func (s *server) IsPreparing(ctx context.Context, req *pb.TaskRef) (*pb.BoolResponse, error) {
	out := &api.BoolResult{}
	if err := s.h.Call(ctx, api.MethodIsPreparing, &api.TaskRef{ID: req.GetId()}, out); err != nil {
		return nil, internalErr(err)
	}
	return &pb.BoolResponse{Value: out.Value}, nil
}

func (s *server) Probe(ctx context.Context, req *pb.ProbeRequest) (*pb.ProbeResponse, error) {
	out := &api.ProbeResult{}
	if err := s.h.Call(ctx, api.MethodProbe, &api.ProbeParams{URL: req.GetUrl()}, out); err != nil {
		return nil, internalErr(err)
	}
	return &pb.ProbeResponse{TotalSize: out.TotalSize}, nil
}

func (s *server) GetSettings(ctx context.Context, _ *pb.Empty) (*pb.GetSettingsResponse, error) {
	out := &api.Settings{}
	if err := s.h.Call(ctx, api.MethodGetSettings, &api.Empty{}, out); err != nil {
		return nil, internalErr(err)
	}
	return &pb.GetSettingsResponse{Settings: settingsToPB(*out)}, nil
}

func (s *server) SaveSettings(ctx context.Context, req *pb.SaveSettingsRequest) (*pb.Empty, error) {
	in := settingsFromPB(req.GetSettings())
	if err := s.h.Call(ctx, api.MethodSaveSettings, &in, &api.Empty{}); err != nil {
		return nil, internalErr(err)
	}
	return &pb.Empty{}, nil
}

func (s *server) BTTaskDetail(ctx context.Context, req *pb.BTInfoRequest) (*pb.BTDetailResponse, error) {
	out := &api.BTDetail{}
	if err := s.h.Call(ctx, api.MethodBTInfo, &api.BTInfoParams{TaskID: req.GetTaskId()}, out); err != nil {
		return nil, internalErr(err)
	}
	return &pb.BTDetailResponse{Detail: btDetailToPB(out)}, nil
}

// StreamMessages 把事件流与控制消息合并后逐条推送。
func (s *server) StreamMessages(_ *pb.Empty, stream pb.DownloadService_StreamMessagesServer) error {
	events, unsub := s.h.Events()
	defer unsub()
	controls := s.h.Controls()

	// 某一侧通道关闭只停掉那一侧：事件流结束不代表控制消息也没了。
	// 这与 httpx 的处理一致，否则同一套业务侧实现在两种传输下行为不同。
	for events != nil || controls != nil {
		select {
		case <-stream.Context().Done():
			// 客户端断开：立刻收手，别让 goroutine 挂着。
			return nil
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if err := stream.Send(messageToPB(api.Message{
				Kind:  api.MessageKindEvent,
				Event: &ev,
			})); err != nil {
				return err
			}
		case ctl, ok := <-controls:
			if !ok {
				controls = nil
				continue
			}
			if err := stream.Send(messageToPB(ctl)); err != nil {
				return err
			}
		}
	}
	return nil
}

// methodErrorf 构造带方法名的错误。
func methodErrorf(method string, err error) error {
	return fmt.Errorf("grpcx: %s: %w", method, err)
}
