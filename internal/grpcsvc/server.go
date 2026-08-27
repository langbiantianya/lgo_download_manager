// Package grpcsvc implements the DownloaderService gRPC server.
// It exposes AddTask so browser extensions can push download tasks over
// a local TCP connection without sharing the process memory map.
package grpcsvc

import (
	"context"
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"

	"lgo_download_manager/downloader_grpc"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
)

// Server implements downloader_grpc.DownloaderServiceServer.
// It adapts the gRPC wire format to the scheduler's in-process Add API.
type Server struct {
	downloader_grpc.UnimplementedDownloaderServiceServer
	sc *scheduler.Scheduler
}

// NewServer builds a Server that drives the scheduler.
func NewServer(sc *scheduler.Scheduler) *Server {
	return &Server{sc: sc}
}

// AddTask receives a download request from a browser extension and enqueues
// it in the scheduler. The task is persisted to SQLite immediately but
// NOT started — the GUI (or the extension) must call Start separately.
func (s *Server) AddTask(ctx context.Context, req *downloader_grpc.AddTaskRequest) (*downloader_grpc.AddTaskResponse, error) {
	if req.Url == "" {
		return failure("url is required"), nil
	}

	protoKind, err := protocolKindFromProto(req.Protocol)
	if err != nil {
		return failure(err.Error()), nil
	}

	savePath := req.FileName
	if savePath == "" {
		savePath = guessFileName(req.Url)
	}

	auth := protocol.AuthOptions{
		Username:   req.Username,
		Password:   req.Password,
		UserAgent:  req.UserAgent,
		Cookies:    req.Cookies,
		Referer:    req.Referer,
		FTPPassive: req.FtpPassiveMode,
	}

	chunkCount := int(req.ChunkCount)
	if chunkCount <= 0 {
		chunkCount = 4
	}

	tk, err := s.sc.Add(scheduler.AddTaskInput{
		URL:        req.Url,
		SavePath:   savePath,
		Protocol:   protoKind,
		Auth:       auth,
		ChunkCount: chunkCount,
	})
	if err != nil {
		return failure(fmt.Sprintf("add task: %s", err.Error())), nil
	}

	return &downloader_grpc.AddTaskResponse{
		Success: true,
		Message: "task enqueued",
		TaskId:  tk.ID,
	}, nil
}

// Register starts the gRPC server on the given TCP address. It blocks
// until the listener is closed. Pass a cancellable context to shut down
// cleanly via context cancellation.
func Register(ctx context.Context, addr string, sc *scheduler.Scheduler) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen: %w", err)
	}

	srv := grpc.NewServer()
	downloader_grpc.RegisterDownloaderServiceServer(srv, NewServer(sc))

	log.Printf("gRPC server listening on %s", addr)

	go func() {
		<-ctx.Done()
		srv.GracefulStop()
		log.Printf("gRPC server stopped")
	}()

	return srv.Serve(lis)
}

// protocolKindFromProto maps the protobuf enum to our domain type.
func protocolKindFromProto(pt downloader_grpc.ProtocolType) (protocol.ProtocolKind, error) {
	switch pt {
	case downloader_grpc.ProtocolType_HTTP:
		return protocol.ProtoHTTP, nil
	case downloader_grpc.ProtocolType_HTTPS:
		return protocol.ProtoHTTPS, nil
	case downloader_grpc.ProtocolType_FTP:
		return protocol.ProtoFTP, nil
	case downloader_grpc.ProtocolType_WEBDAV:
		return protocol.ProtoWebDAV, nil
	default:
		return "", fmt.Errorf("unknown protocol: %v", pt)
	}
}

// failure builds a response with success=false and the given message.
func failure(msg string) *downloader_grpc.AddTaskResponse {
	return &downloader_grpc.AddTaskResponse{
		Success: false,
		Message: msg,
		TaskId:  "",
	}
}

// guessFileName extracts the last path component from a URL as a fallback
// save path.
func guessFileName(rawURL string) string {
	if rawURL == "" {
		return "download"
	}
	base := rawURL
	// Strip query string and fragment.
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '?' || base[i] == '#' {
			base = base[:i]
			break
		}
	}
	// Take the last path segment.
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '/' {
			name := base[i+1:]
			if name != "" {
				return name
			}
		}
	}
	return "download"
}
