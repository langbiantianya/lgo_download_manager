// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package grpcx

import (
	"slices"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/grpcx/pb"
)

// 本文件是契约层（internal/api）与 protobuf 之间的唯一转换点：客户端与
// 服务端都走这里的函数，两边不会各自写一份而慢慢走偏。
//
// 枚举映射表一律显式写出 UNSPECIFIED 条目，不依赖「map 缺失键返回零值」
// 这类隐式行为——隐式行为在重构或换语言实现时很容易碎。
//
// 每张映射表都有真实调用点：状态走 Task，筛选与排序走 ListRequest，
// 代理模式走 Settings 与 AuthOptions，协议种类走 AddTaskRequest。

// ---- 状态枚举映射 ----

var statusToPB = map[api.Status]pb.Status{
	"":                    pb.Status_STATUS_UNSPECIFIED,
	api.StatusPending:     pb.Status_STATUS_PENDING,
	api.StatusDownloading: pb.Status_STATUS_DOWNLOADING,
	api.StatusPaused:      pb.Status_STATUS_PAUSED,
	api.StatusCompleted:   pb.Status_STATUS_COMPLETED,
	api.StatusFailed:      pb.Status_STATUS_FAILED,
	api.StatusFileLost:    pb.Status_STATUS_FILE_LOST,
}

var statusFromPB = map[pb.Status]api.Status{
	pb.Status_STATUS_UNSPECIFIED: "",
	pb.Status_STATUS_PENDING:     api.StatusPending,
	pb.Status_STATUS_DOWNLOADING: api.StatusDownloading,
	pb.Status_STATUS_PAUSED:      api.StatusPaused,
	pb.Status_STATUS_COMPLETED:   api.StatusCompleted,
	pb.Status_STATUS_FAILED:      api.StatusFailed,
	pb.Status_STATUS_FILE_LOST:   api.StatusFileLost,
}

// ---- 筛选枚举映射 ----

// 契约层的零值（空串）表示「不过滤」，与 HTTP 传输传空字符串的语义一致。
var filterToPB = map[api.StatusFilter]pb.StatusFilter{
	"":                    pb.StatusFilter_STATUS_FILTER_UNSPECIFIED,
	api.FilterAll:         pb.StatusFilter_STATUS_FILTER_ALL,
	api.FilterPending:     pb.StatusFilter_STATUS_FILTER_PENDING,
	api.FilterDownloading: pb.StatusFilter_STATUS_FILTER_DOWNLOADING,
	api.FilterPaused:      pb.StatusFilter_STATUS_FILTER_PAUSED,
	api.FilterCompleted:   pb.StatusFilter_STATUS_FILTER_COMPLETED,
	api.FilterFailed:      pb.StatusFilter_STATUS_FILTER_FAILED,
	api.FilterFileLost:    pb.StatusFilter_STATUS_FILTER_FILE_LOST,
}

var filterFromPB = map[pb.StatusFilter]api.StatusFilter{
	pb.StatusFilter_STATUS_FILTER_UNSPECIFIED: "",
	pb.StatusFilter_STATUS_FILTER_ALL:         api.FilterAll,
	pb.StatusFilter_STATUS_FILTER_PENDING:     api.FilterPending,
	pb.StatusFilter_STATUS_FILTER_DOWNLOADING: api.FilterDownloading,
	pb.StatusFilter_STATUS_FILTER_PAUSED:      api.FilterPaused,
	pb.StatusFilter_STATUS_FILTER_COMPLETED:   api.FilterCompleted,
	pb.StatusFilter_STATUS_FILTER_FAILED:      api.FilterFailed,
	pb.StatusFilter_STATUS_FILTER_FILE_LOST:   api.FilterFileLost,
}

// ---- 排序枚举映射 ----

// 契约层的零值（空串）表示「默认排序」，与 HTTP 传输传空字符串的语义一致。
var sortToPB = map[api.TaskSort]pb.TaskSort{
	"":                  pb.TaskSort_TASK_SORT_UNSPECIFIED,
	api.SortCreatedDesc: pb.TaskSort_TASK_SORT_CREATED_DESC,
	api.SortCreatedAsc:  pb.TaskSort_TASK_SORT_CREATED_ASC,
	api.SortNameAsc:     pb.TaskSort_TASK_SORT_NAME_ASC,
	api.SortNameDesc:    pb.TaskSort_TASK_SORT_NAME_DESC,
}

var sortFromPB = map[pb.TaskSort]api.TaskSort{
	pb.TaskSort_TASK_SORT_UNSPECIFIED:  "",
	pb.TaskSort_TASK_SORT_CREATED_DESC: api.SortCreatedDesc,
	pb.TaskSort_TASK_SORT_CREATED_ASC:  api.SortCreatedAsc,
	pb.TaskSort_TASK_SORT_NAME_ASC:     api.SortNameAsc,
	pb.TaskSort_TASK_SORT_NAME_DESC:    api.SortNameDesc,
}

// ---- 代理模式映射 ----

var proxyToPB = map[api.ProxyMode]pb.ProxyMode{
	"":                    pb.ProxyMode_PROXY_MODE_UNSPECIFIED,
	api.ProxyModeSystem:   pb.ProxyMode_PROXY_MODE_SYSTEM,
	api.ProxyModeDisabled: pb.ProxyMode_PROXY_MODE_DISABLED,
	api.ProxyModeManual:   pb.ProxyMode_PROXY_MODE_MANUAL,
}

var proxyFromPB = map[pb.ProxyMode]api.ProxyMode{
	pb.ProxyMode_PROXY_MODE_UNSPECIFIED: "",
	pb.ProxyMode_PROXY_MODE_SYSTEM:      api.ProxyModeSystem,
	pb.ProxyMode_PROXY_MODE_DISABLED:    api.ProxyModeDisabled,
	pb.ProxyMode_PROXY_MODE_MANUAL:      api.ProxyModeManual,
}

// ---- 协议种类映射 ----

var kindToPB = map[api.ProtocolKind]pb.ProtocolKind{
	"":              pb.ProtocolKind_PROTOCOL_KIND_UNSPECIFIED,
	api.ProtoHTTP:   pb.ProtocolKind_PROTOCOL_KIND_HTTP,
	api.ProtoHTTPS:  pb.ProtocolKind_PROTOCOL_KIND_HTTPS,
	api.ProtoFTP:    pb.ProtocolKind_PROTOCOL_KIND_FTP,
	api.ProtoWebDAV: pb.ProtocolKind_PROTOCOL_KIND_WEBDAV,
	api.ProtoBT:     pb.ProtocolKind_PROTOCOL_KIND_BT,
}

var kindFromPB = map[pb.ProtocolKind]api.ProtocolKind{
	pb.ProtocolKind_PROTOCOL_KIND_UNSPECIFIED: "",
	pb.ProtocolKind_PROTOCOL_KIND_HTTP:        api.ProtoHTTP,
	pb.ProtocolKind_PROTOCOL_KIND_HTTPS:       api.ProtoHTTPS,
	pb.ProtocolKind_PROTOCOL_KIND_FTP:         api.ProtoFTP,
	pb.ProtocolKind_PROTOCOL_KIND_WEBDAV:      api.ProtoWebDAV,
	pb.ProtocolKind_PROTOCOL_KIND_BT:          api.ProtoBT,
}

// ---- DTO 映射 ----

// listParamsToPB 把列表查询参数转成 proto 请求。
func listParamsToPB(p *api.ListParams) *pb.ListRequest {
	if p == nil {
		return &pb.ListRequest{}
	}
	return &pb.ListRequest{
		Filter: filterToPB[p.Filter],
		Sort:   sortToPB[p.Sort],
	}
}

// listParamsFromPB 把 proto 请求转回列表查询参数。
func listParamsFromPB(r *pb.ListRequest) *api.ListParams {
	if r == nil {
		return &api.ListParams{}
	}
	return &api.ListParams{
		Filter: filterFromPB[r.GetFilter()],
		Sort:   sortFromPB[r.GetSort()],
	}
}

// taskToPB 把任务转成 proto 消息。
func taskToPB(t *api.Task) *pb.Task {
	if t == nil {
		return nil
	}
	return &pb.Task{
		Id:              t.ID,
		Url:             t.URL,
		SavePath:        t.SavePath,
		Protocol:        t.Protocol,
		TotalSize:       t.TotalSize,
		Downloaded:      t.Downloaded,
		SupportRange:    t.SupportRange,
		Etag:            t.ETag,
		LastModified:    t.LastModified,
		DigestAlgorithm: t.DigestAlgorithm,
		Digest:          t.Digest,
		IsAllocated:     t.IsAllocated,
		ChunkCount:      int32(t.ChunkCount),
		MinChunkSize:    t.MinChunkSize,
		ChunkProgress:   slices.Clone(t.ChunkProgress),
		ChunkRanges:     slices.Clone(t.ChunkRanges),
		Status:          statusToPB[t.Status],
		AuthData:        t.AuthData,
		ErrorMessage:    t.ErrorMessage,
		ErrorKind:       t.ErrorKind,
		CreatedAt:       formatTime(t.CreatedAt),
		UpdatedAt:       formatTime(t.UpdatedAt),
		CompletedAt:     formatTime(t.CompletedAt),
		BtFile:          t.BTFile,
	}
}

// taskFromPB 把 proto 消息转回任务。
func taskFromPB(t *pb.Task) *api.Task {
	if t == nil {
		return nil
	}
	return &api.Task{
		ID:              t.GetId(),
		URL:             t.GetUrl(),
		SavePath:        t.GetSavePath(),
		Protocol:        t.GetProtocol(),
		TotalSize:       t.GetTotalSize(),
		Downloaded:      t.GetDownloaded(),
		SupportRange:    t.GetSupportRange(),
		ETag:            t.GetEtag(),
		LastModified:    t.GetLastModified(),
		DigestAlgorithm: t.GetDigestAlgorithm(),
		Digest:          t.GetDigest(),
		IsAllocated:     t.GetIsAllocated(),
		ChunkCount:      int(t.GetChunkCount()),
		MinChunkSize:    t.GetMinChunkSize(),
		ChunkProgress:   nilIfEmpty(t.GetChunkProgress()),
		ChunkRanges:     nilIfEmpty(t.GetChunkRanges()),
		Status:          statusFromPB[t.GetStatus()],
		AuthData:        t.GetAuthData(),
		ErrorMessage:    t.GetErrorMessage(),
		ErrorKind:       t.GetErrorKind(),
		CreatedAt:       parseTime(t.GetCreatedAt()),
		UpdatedAt:       parseTime(t.GetUpdatedAt()),
		CompletedAt:     parseTime(t.GetCompletedAt()),
		BTFile:          t.GetBtFile(),
	}
}

// settingsToPB 把设置转成 proto 消息。
func settingsToPB(s api.Settings) *pb.Settings {
	return &pb.Settings{
		DefaultSaveDir:     s.DefaultSaveDir,
		DefaultThreads:     int32(s.DefaultThreads),
		MinChunkSize:       s.MinChunkSize,
		UserAgent:          s.UserAgent,
		Cookies:            s.Cookies,
		FtpPassive:         s.FTPPassive,
		Prealloc:           s.Prealloc,
		TaskSort:           sortToPB[s.TaskSort],
		ProxyMode:          proxyToPB[s.ProxyMode],
		ProxyUrl:           s.ProxyURL,
		ProxyBypass:        s.ProxyBypass,
		ProxyAutoConfigUrl: s.ProxyAutoConfigURL,
		DownloadLimitKib:   int32(s.DownloadLimitKiB),
		LightMode:          s.LightMode,
		MaxConcurrent:      int32(s.MaxConcurrent),
		AutoStart:          s.AutoStart,
		Language:           s.Language,
		BtPort:             int32(s.BTPort),
		BtMaxPeers:         int32(s.BTMaxPeers),
		BtDownloadLimitKib: int32(s.BTDownloadLimitKiB),
		BtUploadLimitKib:   int32(s.BTUploadLimitKiB),
		BtDht:              s.BTDHT,
		BtPex:              s.BTPEX,
		BtSeedRatio:        s.BTSeedRatio,
		BtMaxSeeding:       int32(s.BTMaxSeeding),
		BtMaxConcurrent:    int32(s.BTMaxConcurrent),
		BtTrackers:         s.BTTrackers,
	}
}

// settingsFromPB 把 proto 消息转回设置。
func settingsFromPB(s *pb.Settings) api.Settings {
	if s == nil {
		return api.Settings{}
	}
	return api.Settings{
		DefaultSaveDir:     s.GetDefaultSaveDir(),
		DefaultThreads:     int(s.GetDefaultThreads()),
		MinChunkSize:       s.GetMinChunkSize(),
		UserAgent:          s.GetUserAgent(),
		Cookies:            s.GetCookies(),
		FTPPassive:         s.GetFtpPassive(),
		Prealloc:           s.GetPrealloc(),
		TaskSort:           sortFromPB[s.GetTaskSort()],
		ProxyMode:          proxyFromPB[s.GetProxyMode()],
		ProxyURL:           s.GetProxyUrl(),
		ProxyBypass:        s.GetProxyBypass(),
		ProxyAutoConfigURL: s.GetProxyAutoConfigUrl(),
		DownloadLimitKiB:   int(s.GetDownloadLimitKib()),
		LightMode:          s.GetLightMode(),
		MaxConcurrent:      int(s.GetMaxConcurrent()),
		AutoStart:          s.GetAutoStart(),
		Language:           s.GetLanguage(),
		BTPort:             int(s.GetBtPort()),
		BTMaxPeers:         int(s.GetBtMaxPeers()),
		BTDownloadLimitKiB: int(s.GetBtDownloadLimitKib()),
		BTUploadLimitKiB:   int(s.GetBtUploadLimitKib()),
		BTDHT:              s.GetBtDht(),
		BTPEX:              s.GetBtPex(),
		BTSeedRatio:        s.GetBtSeedRatio(),
		BTMaxSeeding:       int(s.GetBtMaxSeeding()),
		BTMaxConcurrent:    int(s.GetBtMaxConcurrent()),
		BTTrackers:         s.GetBtTrackers(),
	}
}

// authToPB 把凭据转成 proto 消息。
func authToPB(a api.AuthOptions) *pb.AuthOptions {
	return &pb.AuthOptions{
		Username:           a.Username,
		Password:           a.Password,
		UserAgent:          a.UserAgent,
		Cookies:            a.Cookies,
		Referer:            a.Referer,
		FtpPassive:         a.FTPPassive,
		ProxyMode:          proxyToPB[a.ProxyMode],
		ProxyUrl:           a.ProxyURL,
		ProxyBypass:        a.ProxyBypass,
		ProxyAutoConfigUrl: a.ProxyAutoConfigURL,
	}
}

// authFromPB 把 proto 消息转回凭据。
func authFromPB(a *pb.AuthOptions) api.AuthOptions {
	if a == nil {
		return api.AuthOptions{}
	}
	return api.AuthOptions{
		Username:           a.GetUsername(),
		Password:           a.GetPassword(),
		UserAgent:          a.GetUserAgent(),
		Cookies:            a.GetCookies(),
		Referer:            a.GetReferer(),
		FTPPassive:         a.GetFtpPassive(),
		ProxyMode:          proxyFromPB[a.GetProxyMode()],
		ProxyURL:           a.GetProxyUrl(),
		ProxyBypass:        a.GetProxyBypass(),
		ProxyAutoConfigURL: a.GetProxyAutoConfigUrl(),
	}
}

// addTaskInputToPB 把「添加下载任务」的入参转成 proto 请求。
func addTaskInputToPB(in *api.AddTaskInput) *pb.AddTaskRequest {
	if in == nil {
		return &pb.AddTaskRequest{}
	}
	return &pb.AddTaskRequest{
		Url:          in.URL,
		SavePath:     in.SavePath,
		Protocol:     kindToPB[in.Protocol],
		Auth:         authToPB(in.Auth),
		ChunkCount:   int32(in.ChunkCount),
		MinChunkSize: in.MinChunkSize,
	}
}

// addTaskInputFromPB 把 proto 请求转回「添加下载任务」的入参。
func addTaskInputFromPB(r *pb.AddTaskRequest) *api.AddTaskInput {
	if r == nil {
		return &api.AddTaskInput{}
	}
	return &api.AddTaskInput{
		URL:          r.GetUrl(),
		SavePath:     r.GetSavePath(),
		Protocol:     kindFromPB[r.GetProtocol()],
		Auth:         authFromPB(r.GetAuth()),
		ChunkCount:   int(r.GetChunkCount()),
		MinChunkSize: r.GetMinChunkSize(),
	}
}

// btDetailToPB 把 BT 详情转成 proto 消息。
func btDetailToPB(d *api.BTDetail) *pb.BTDetail {
	if d == nil {
		return nil
	}
	files := make([]*pb.FileProgress, 0, len(d.Files))
	for _, f := range d.Files {
		files = append(files, &pb.FileProgress{
			Path:      f.Path,
			Length:    f.Length,
			Completed: f.Completed,
		})
	}
	buckets := make([]uint32, 0, len(d.PieceBuckets))
	for _, b := range d.PieceBuckets {
		buckets = append(buckets, uint32(b))
	}
	return &pb.BTDetail{
		Name:         d.Name,
		InfoHash:     d.InfoHash,
		Total:        d.Total,
		Downloaded:   d.Downloaded,
		Uploaded:     d.Uploaded,
		SpeedBps:     d.SpeedBPS,
		UploadBps:    d.UploadBPS,
		Peers:        int32(d.Peers),
		TotalPeers:   int32(d.TotalPeers),
		Seeders:      int32(d.Seeders),
		Leechers:     int32(d.Leechers),
		PieceLength:  d.PieceLength,
		Pieces:       int32(d.Pieces),
		PiecesDone:   int32(d.PiecesDone),
		PieceBuckets: buckets,
		Seeding:      d.Seeding,
		Complete:     d.Complete,
		Files:        files,
		Trackers:     slices.Clone(d.Trackers),
	}
}

// btDetailFromPB 把 proto 消息转回 BT 详情。
func btDetailFromPB(d *pb.BTDetail) *api.BTDetail {
	if d == nil {
		return nil
	}
	files := make([]api.FileProgress, 0, len(d.GetFiles()))
	for _, f := range d.GetFiles() {
		files = append(files, api.FileProgress{
			Path:      f.GetPath(),
			Length:    f.GetLength(),
			Completed: f.GetCompleted(),
		})
	}
	buckets := make([]uint8, 0, len(d.GetPieceBuckets()))
	for _, b := range d.GetPieceBuckets() {
		buckets = append(buckets, uint8(b))
	}
	return &api.BTDetail{
		Name:         d.GetName(),
		InfoHash:     d.GetInfoHash(),
		Total:        d.GetTotal(),
		Downloaded:   d.GetDownloaded(),
		Uploaded:     d.GetUploaded(),
		SpeedBPS:     d.GetSpeedBps(),
		UploadBPS:    d.GetUploadBps(),
		Peers:        int(d.GetPeers()),
		TotalPeers:   int(d.GetTotalPeers()),
		Seeders:      int(d.GetSeeders()),
		Leechers:     int(d.GetLeechers()),
		PieceLength:  d.GetPieceLength(),
		Pieces:       int(d.GetPieces()),
		PiecesDone:   int(d.GetPiecesDone()),
		PieceBuckets: buckets,
		Seeding:      d.GetSeeding(),
		Complete:     d.GetComplete(),
		Files:        files,
		Trackers:     slices.Clone(d.GetTrackers()),
	}
}

// eventToPB 把事件转成 proto 消息。
func eventToPB(e api.Event) *pb.Event {
	return &pb.Event{
		Why:      e.Why,
		Task:     taskToPB(e.Task),
		SpeedBps: e.SpeedBPS,
	}
}

// eventFromPB 把 proto 消息转回事件。
func eventFromPB(e *pb.Event) api.Event {
	if e == nil {
		return api.Event{}
	}
	return api.Event{
		Why:      e.GetWhy(),
		Task:     taskFromPB(e.GetTask()),
		SpeedBPS: e.GetSpeedBps(),
	}
}

// messageToPB 把契约层消息转成 proto 消息。
func messageToPB(m api.Message) *pb.Message {
	switch m.Kind {
	case api.MessageKindEvent:
		// api.Message.Event 是指针，契约上允许为 nil（rpc 共享客户端的
		// dispatch 就显式判过 msg.Event != nil），这里不能直接解引用。
		if m.Event == nil {
			return &pb.Message{Payload: &pb.Message_Event{Event: &pb.Event{}}}
		}
		return &pb.Message{Payload: &pb.Message_Event{Event: eventToPB(*m.Event)}}
	case api.MessageKindShow:
		return &pb.Message{Payload: &pb.Message_Show{Show: true}}
	case api.MessageKindShowAddTask:
		p := m.ShowAddTask
		if p == nil {
			p = &api.ShowAddTaskParams{}
		}
		return &pb.Message{Payload: &pb.Message_ShowAddTask{ShowAddTask: &pb.ShowAddTaskParams{
			Url:     p.URL,
			Name:    p.Name,
			Ua:      p.UA,
			Cookies: p.Cookies,
		}}}
	case api.MessageKindLanguage:
		return &pb.Message{Payload: &pb.Message_Language{Language: m.Language}}
	case api.MessageKindClose:
		return &pb.Message{Payload: &pb.Message_Close{Close: true}}
	default:
		return &pb.Message{}
	}
}

// messageFromPB 把 proto 消息转回契约层消息。
func messageFromPB(m *pb.Message) api.Message {
	if m == nil {
		return api.Message{}
	}
	switch p := m.GetPayload().(type) {
	case *pb.Message_Event:
		ev := eventFromPB(p.Event)
		return api.Message{Kind: api.MessageKindEvent, Event: &ev}
	case *pb.Message_Show:
		return api.Message{Kind: api.MessageKindShow}
	case *pb.Message_ShowAddTask:
		return api.Message{
			Kind: api.MessageKindShowAddTask,
			ShowAddTask: &api.ShowAddTaskParams{
				URL:     p.ShowAddTask.GetUrl(),
				Name:    p.ShowAddTask.GetName(),
				UA:      p.ShowAddTask.GetUa(),
				Cookies: p.ShowAddTask.GetCookies(),
			},
		}
	case *pb.Message_Language:
		return api.Message{Kind: api.MessageKindLanguage, Language: p.Language}
	case *pb.Message_Close:
		return api.Message{Kind: api.MessageKindClose}
	default:
		return api.Message{}
	}
}

// formatTime 把时间转成 RFC3339 字符串。
//
// 零值时间会格式化成 "0001-01-01T00:00:00Z"，而不是空串：proto3 的
// string 字段本来就没有「有值 / 没值」之分，用空串表达零值会让「零值」
// 和「没设置」混成一团。
func formatTime(t time.Time) string {
	return t.Format(time.RFC3339Nano)
}

// parseTime 解析 RFC3339 字符串；空串或非法值返回零值。
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// nilIfEmpty 把空切片归一成 nil。
//
// proto3 的 repeated 字段在空的时候不上线传输，反序列化后必然是 nil。
// 归一之后「空切片」和「nil」在契约层就是同一件事，只在这一处处理。
func nilIfEmpty(v []int64) []int64 {
	if len(v) == 0 {
		return nil
	}
	return slices.Clone(v)
}
