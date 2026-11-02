// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package grpcx

import (
	"reflect"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/grpcx/pb"
)

// 测试用的固定时间点。用 time.Date 而不是 time.Now()：time.Now() 带单调
// 时钟读数，格式化后就没了，往返比较必然不相等。
var testTime = time.Date(2026, 3, 4, 5, 6, 7, 890123456, time.UTC)

// normTask 把任务里的空切片归一成 nil。
//
// proto3 的 repeated 字段为空时不上线传输，反序列化后必然是 nil。
// 归一之后「空切片」与「nil」在契约层就是同一件事。
func normTask(t *api.Task) *api.Task {
	if t == nil {
		return nil
	}
	c := *t
	if len(c.ChunkProgress) == 0 {
		c.ChunkProgress = nil
	}
	if len(c.ChunkRanges) == 0 {
		c.ChunkRanges = nil
	}
	return &c
}

// normBTDetail 把详情里的空切片归一成 nil。
func normBTDetail(d *api.BTDetail) *api.BTDetail {
	if d == nil {
		return nil
	}
	c := *d
	if len(c.PieceBuckets) == 0 {
		c.PieceBuckets = nil
	}
	if len(c.Files) == 0 {
		c.Files = nil
	}
	if len(c.Trackers) == 0 {
		c.Trackers = nil
	}
	return &c
}

// TestTaskRoundTrip 覆盖任务的完整字段往返。
func TestTaskRoundTrip(t *testing.T) {
	in := api.Task{
		ID: "t1", URL: "http://x/a", SavePath: "/tmp/a", Protocol: "HTTP",
		TotalSize: 100, Downloaded: 40, SupportRange: true,
		ETag: "e", LastModified: "m", DigestAlgorithm: "sha256", Digest: "d",
		IsAllocated: true, ChunkCount: 4, MinChunkSize: 1024,
		ChunkProgress: []int64{1, 2}, ChunkRanges: []int64{0, 9},
		Status: api.StatusDownloading, AuthData: "{}", ErrorMessage: "m",
		ErrorKind: "k", CreatedAt: testTime, UpdatedAt: testTime,
		CompletedAt: testTime, BTFile: "f",
	}
	got := taskFromPB(taskToPB(&in))
	if !reflect.DeepEqual(normTask(got), normTask(&in)) {
		t.Fatalf("Task 往返不一致:\n got = %+v\nwant = %+v", *got, in)
	}
}

// TestTaskRoundTripZeroTimes 覆盖零值时间字段。
//
// 零值 time.Time{} 格式化后是 "0001-01-01T00:00:00Z"，必须能原样解析
// 回零值——用空串表达零值会让「零值」与「没设置」混成一团。
func TestTaskRoundTripZeroTimes(t *testing.T) {
	in := api.Task{ID: "t0"}
	got := taskFromPB(taskToPB(&in))
	if !reflect.DeepEqual(normTask(got), normTask(&in)) {
		t.Fatalf("零值时间字段往返不一致:\n got = %+v\nwant = %+v", *got, in)
	}
	if !got.CreatedAt.IsZero() || !got.UpdatedAt.IsZero() || !got.CompletedAt.IsZero() {
		t.Fatalf("零值时间字段应当仍是零值: %+v", got)
	}
}

// TestTaskRoundTripNil 覆盖 nil 任务。
func TestTaskRoundTripNil(t *testing.T) {
	if got := taskToPB(nil); got != nil {
		t.Fatalf("nil 任务应当转成 nil: %+v", got)
	}
	if got := taskFromPB(nil); got != nil {
		t.Fatalf("nil proto 任务应当转成 nil: %+v", got)
	}
}

// TestSettingsRoundTrip 覆盖设置的完整字段往返。
func TestSettingsRoundTrip(t *testing.T) {
	in := api.Settings{
		DefaultSaveDir: "/tmp", DefaultThreads: 8, MinChunkSize: 4096,
		UserAgent: "UA", Cookies: "C", FTPPassive: true, Prealloc: true,
		TaskSort: api.SortNameAsc, ProxyMode: api.ProxyModeManual,
		ProxyURL: "http://p", ProxyBypass: "b", ProxyAutoConfigURL: "u",
		DownloadLimitKiB: 512, LightMode: true, MaxConcurrent: 3,
		AutoStart: true, Language: "en", BTPort: 6881, BTMaxPeers: 50,
		BTDownloadLimitKiB: 100, BTUploadLimitKiB: 50, BTDHT: true, BTPEX: true,
		BTSeedRatio: 1.5, BTMaxSeeding: 10, BTMaxConcurrent: 2,
		BTTrackers: "udp://tracker.example:1337",
	}
	got := settingsFromPB(settingsToPB(in))
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("Settings 往返不一致:\n got = %+v\nwant = %+v", got, in)
	}
}

// TestSettingsRoundTripZero 覆盖设置的零值。
func TestSettingsRoundTripZero(t *testing.T) {
	in := api.Settings{}
	got := settingsFromPB(settingsToPB(in))
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("零值 Settings 往返不一致:\n got = %+v\nwant = %+v", got, in)
	}
}

// TestAuthRoundTrip 覆盖凭据的完整字段往返。
func TestAuthRoundTrip(t *testing.T) {
	in := api.AuthOptions{
		Username: "u", Password: "p", UserAgent: "UA", Cookies: "C",
		Referer: "r", FTPPassive: true, ProxyMode: api.ProxyModeDisabled,
		ProxyURL: "http://p", ProxyBypass: "b", ProxyAutoConfigURL: "u",
	}
	got := authFromPB(authToPB(in))
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("AuthOptions 往返不一致:\n got = %+v\nwant = %+v", got, in)
	}
}

// TestAuthRoundTripZero 覆盖凭据的零值。
func TestAuthRoundTripZero(t *testing.T) {
	in := api.AuthOptions{}
	got := authFromPB(authToPB(in))
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("零值 AuthOptions 往返不一致:\n got = %+v\nwant = %+v", got, in)
	}
}

// TestBTDetailRoundTrip 覆盖 BT 详情的完整字段往返。
func TestBTDetailRoundTrip(t *testing.T) {
	in := api.BTDetail{
		Name: "n", InfoHash: "h", Total: 10, Downloaded: 5, Uploaded: 1,
		SpeedBPS: 2, UploadBPS: 3, Peers: 4, TotalPeers: 5, Seeders: 6,
		Leechers: 7, PieceLength: 8, Pieces: 9, PiecesDone: 10,
		PieceBuckets: []uint8{1, 2}, Seeding: true, Complete: true,
		Files:    []api.FileProgress{{Path: "p", Length: 1, Completed: 2}},
		Trackers: []string{"t"},
	}
	got := btDetailFromPB(btDetailToPB(&in))
	if !reflect.DeepEqual(normBTDetail(got), normBTDetail(&in)) {
		t.Fatalf("BTDetail 往返不一致:\n got = %+v\nwant = %+v", *got, in)
	}
}

// TestBTDetailRoundTripZero 覆盖 BT 详情的零值。
func TestBTDetailRoundTripZero(t *testing.T) {
	in := api.BTDetail{}
	got := btDetailFromPB(btDetailToPB(&in))
	if !reflect.DeepEqual(normBTDetail(got), normBTDetail(&in)) {
		t.Fatalf("零值 BTDetail 往返不一致:\n got = %+v\nwant = %+v", *got, in)
	}
}

// TestEventRoundTrip 覆盖事件的往返。
func TestEventRoundTrip(t *testing.T) {
	in := api.Event{
		Why:      "progress",
		Task:     &api.Task{ID: "t1", Status: api.StatusDownloading, CreatedAt: testTime},
		SpeedBPS: 1024,
	}
	got := eventFromPB(eventToPB(in))
	if !reflect.DeepEqual(normTask(got.Task), normTask(in.Task)) {
		t.Fatalf("Event 往返不一致:\n got = %+v\nwant = %+v", got, in)
	}
	if got.Why != in.Why || got.SpeedBPS != in.SpeedBPS {
		t.Fatalf("Event 字段往返不一致: %+v", got)
	}
}

// TestEventRoundTripNilTask 覆盖事件里的 nil 任务。
func TestEventRoundTripNilTask(t *testing.T) {
	in := api.Event{Why: "w"}
	got := eventFromPB(eventToPB(in))
	if got.Task != nil {
		t.Fatalf("nil 任务应当仍是 nil: %+v", got.Task)
	}
}

// TestMessageRoundTrip 覆盖消息流里每一种消息的往返。
func TestMessageRoundTrip(t *testing.T) {
	cases := []api.Message{
		{Kind: api.MessageKindEvent, Event: &api.Event{Why: "w", SpeedBPS: 1}},
		{Kind: api.MessageKindShow},
		{Kind: api.MessageKindShowAddTask, ShowAddTask: &api.ShowAddTaskParams{
			URL: "http://x", Name: "n", UA: "ua", Cookies: "c",
		}},
		{Kind: api.MessageKindLanguage, Language: "en"},
		{Kind: api.MessageKindClose},
	}
	for _, in := range cases {
		got := messageFromPB(messageToPB(in))
		if got.Kind != in.Kind {
			t.Fatalf("消息类别往返不一致: got = %q, want = %q", got.Kind, in.Kind)
		}
		switch in.Kind {
		case api.MessageKindEvent:
			if got.Event == nil || got.Event.Why != in.Event.Why {
				t.Fatalf("事件往返不一致: %+v", got.Event)
			}
		case api.MessageKindShowAddTask:
			if got.ShowAddTask == nil || *got.ShowAddTask != *in.ShowAddTask {
				t.Fatalf("弹窗参数往返不一致: %+v", got.ShowAddTask)
			}
		case api.MessageKindLanguage:
			if got.Language != in.Language {
				t.Fatalf("语言往返不一致: %q", got.Language)
			}
		}
	}
}

// TestMessageToPBHandlesNilEvent 是 E3 的回归测试。
//
// api.Message.Event 是指针，契约上允许为 nil；messageToPB 直接解引用会
// panic。
func TestMessageToPBHandlesNilEvent(t *testing.T) {
	got := messageToPB(api.Message{Kind: api.MessageKindEvent})
	if got == nil {
		t.Fatal("nil 事件也应当转成一个 proto 消息")
	}
	if got.GetEvent() == nil {
		t.Fatal("nil 事件应当转成空事件，而不是丢掉消息类别")
	}
	back := messageFromPB(got)
	if back.Kind != api.MessageKindEvent {
		t.Fatalf("消息类别应当保留: %q", back.Kind)
	}
	if back.Event == nil {
		t.Fatal("回转后的事件指针不应为 nil")
	}
}

// TestMessageFromPBNil 覆盖 nil proto 消息。
func TestMessageFromPBNil(t *testing.T) {
	got := messageFromPB(nil)
	if got.Kind != "" {
		t.Fatalf("nil 消息应当转成零值消息: %+v", got)
	}
}

// TestListParamsRoundTrip 覆盖筛选与排序枚举的往返（E8）。
func TestListParamsRoundTrip(t *testing.T) {
	cases := []api.ListParams{
		{Filter: api.FilterAll, Sort: api.SortCreatedDesc},
		{Filter: api.FilterPending, Sort: api.SortCreatedAsc},
		{Filter: api.FilterDownloading, Sort: api.SortNameAsc},
		{Filter: api.FilterPaused, Sort: api.SortNameDesc},
		{Filter: api.FilterCompleted, Sort: api.SortCreatedDesc},
		{Filter: api.FilterFailed, Sort: api.SortCreatedDesc},
		{Filter: api.FilterFileLost, Sort: api.SortCreatedDesc},
		// 零值表示「不过滤」与「默认排序」。
		{Filter: "", Sort: ""},
	}
	for _, in := range cases {
		got := listParamsFromPB(listParamsToPB(&in))
		if *got != in {
			t.Fatalf("ListParams 往返不一致: got = %+v, want = %+v", *got, in)
		}
	}
}

// TestListParamsFromPBUnspecified 覆盖 UNSPECIFIED 的语义（E8）。
//
// UNSPECIFIED 必须映射回空串，而不是映射成某个具体枚举——空串在存储层
// 表示「不过滤 / 默认排序」。
func TestListParamsFromPBUnspecified(t *testing.T) {
	got := listParamsFromPB(&pb.ListRequest{
		Filter: pb.StatusFilter_STATUS_FILTER_UNSPECIFIED,
		Sort:   pb.TaskSort_TASK_SORT_UNSPECIFIED,
	})
	if got.Filter != "" {
		t.Fatalf("UNSPECIFIED 筛选应当映射回空串: %q", got.Filter)
	}
	if got.Sort != "" {
		t.Fatalf("UNSPECIFIED 排序应当映射回空串: %q", got.Sort)
	}
}

// TestAddTaskInputRoundTrip 覆盖「添加下载任务」入参的往返。
func TestAddTaskInputRoundTrip(t *testing.T) {
	in := api.AddTaskInput{
		URL: "http://x", SavePath: "/tmp", Protocol: api.ProtoBT,
		Auth: api.AuthOptions{
			Username: "u", Password: "p", UserAgent: "UA", Cookies: "C",
			Referer: "r", FTPPassive: true, ProxyMode: api.ProxyModeManual,
			ProxyURL: "http://p", ProxyBypass: "b", ProxyAutoConfigURL: "u",
		},
		ChunkCount: 8, MinChunkSize: 1024,
	}
	got := addTaskInputFromPB(addTaskInputToPB(&in))
	if !reflect.DeepEqual(*got, in) {
		t.Fatalf("AddTaskInput 往返不一致:\n got = %+v\nwant = %+v", *got, in)
	}
}

// TestFormatParseTime 覆盖时间的格式化与解析（E14）。
func TestFormatParseTime(t *testing.T) {
	// 零值时间格式化成 "0001-01-01T00:00:00Z"，并且能原样解析回零值。
	if got := formatTime(time.Time{}); got != "0001-01-01T00:00:00Z" {
		t.Fatalf("零值时间应当格式化成 RFC3339 零值: %q", got)
	}
	if got := parseTime("0001-01-01T00:00:00Z"); !got.IsZero() {
		t.Fatalf("零值时间应当解析回零值: %v", got)
	}
	// 空串与非法值都退化成零值，不能炸。
	if got := parseTime(""); !got.IsZero() {
		t.Fatalf("空串应当解析成零值: %v", got)
	}
	if got := parseTime("不是时间"); !got.IsZero() {
		t.Fatalf("非法值应当解析成零值: %v", got)
	}
	// 正常时间往返无损。
	if got := parseTime(formatTime(testTime)); !got.Equal(testTime) {
		t.Fatalf("时间往返不一致: got = %v, want = %v", got, testTime)
	}
}

// TestEnumTablesCoverAllConstants 校验每张枚举映射表都双向闭合、覆盖
// UNSPECIFIED，并且都有真实调用点。
//
// 「有调用点」这一条是 E8 的要害：初稿里 ListRequest 用的是裸 string，
// filterToPB / filterFromPB 两张表因此是死代码，而只检查反向映射存在的
// 测试照样能过——测不出「枚举压根没被用」。
func TestEnumTablesCoverAllConstants(t *testing.T) {
	t.Run("状态", func(t *testing.T) {
		for s, p := range statusToPB {
			back, ok := statusFromPB[p]
			if !ok {
				t.Fatalf("状态 %q 没有反向映射", s)
			}
			if back != s {
				t.Fatalf("状态 %q 反向映射成了 %q", s, back)
			}
		}
		if _, ok := statusFromPB[pb.Status_STATUS_UNSPECIFIED]; !ok {
			t.Fatal("statusFromPB 缺少 UNSPECIFIED 的显式条目")
		}
		if got := statusFromPB[pb.Status_STATUS_UNSPECIFIED]; got != "" {
			t.Fatalf("UNSPECIFIED 应当映射回空串: %q", got)
		}
		// 调用点：状态走 Task。
		for s := range statusToPB {
			pbTask := taskToPB(&api.Task{Status: s})
			if pbTask.GetStatus() != statusToPB[s] {
				t.Fatalf("状态 %q 没有出现在 Task 的转换结果里", s)
			}
			if got := taskFromPB(pbTask).Status; got != s {
				t.Fatalf("状态 %q 经 Task 往返后变成了 %q", s, got)
			}
		}
	})

	t.Run("筛选", func(t *testing.T) {
		for s, p := range filterToPB {
			back, ok := filterFromPB[p]
			if !ok {
				t.Fatalf("筛选 %q 没有反向映射", s)
			}
			if back != s {
				t.Fatalf("筛选 %q 反向映射成了 %q", s, back)
			}
		}
		if _, ok := filterFromPB[pb.StatusFilter_STATUS_FILTER_UNSPECIFIED]; !ok {
			t.Fatal("filterFromPB 缺少 UNSPECIFIED 的显式条目")
		}
		// 调用点：筛选走 ListRequest。
		for s := range filterToPB {
			req := listParamsToPB(&api.ListParams{Filter: s})
			if req.GetFilter() != filterToPB[s] {
				t.Fatalf("筛选 %q 没有出现在 ListRequest 的转换结果里", s)
			}
			if got := listParamsFromPB(req).Filter; got != s {
				t.Fatalf("筛选 %q 经 ListRequest 往返后变成了 %q", s, got)
			}
		}
	})

	t.Run("排序", func(t *testing.T) {
		for s, p := range sortToPB {
			back, ok := sortFromPB[p]
			if !ok {
				t.Fatalf("排序 %q 没有反向映射", s)
			}
			if back != s {
				t.Fatalf("排序 %q 反向映射成了 %q", s, back)
			}
		}
		if _, ok := sortFromPB[pb.TaskSort_TASK_SORT_UNSPECIFIED]; !ok {
			t.Fatal("sortFromPB 缺少 UNSPECIFIED 的显式条目")
		}
		// 调用点：排序走 ListRequest。
		for s := range sortToPB {
			req := listParamsToPB(&api.ListParams{Sort: s})
			if req.GetSort() != sortToPB[s] {
				t.Fatalf("排序 %q 没有出现在 ListRequest 的转换结果里", s)
			}
			if got := listParamsFromPB(req).Sort; got != s {
				t.Fatalf("排序 %q 经 ListRequest 往返后变成了 %q", s, got)
			}
		}
	})

	t.Run("代理模式", func(t *testing.T) {
		for s, p := range proxyToPB {
			back, ok := proxyFromPB[p]
			if !ok {
				t.Fatalf("代理模式 %q 没有反向映射", s)
			}
			if back != s {
				t.Fatalf("代理模式 %q 反向映射成了 %q", s, back)
			}
		}
		if _, ok := proxyFromPB[pb.ProxyMode_PROXY_MODE_UNSPECIFIED]; !ok {
			t.Fatal("proxyFromPB 缺少 UNSPECIFIED 的显式条目")
		}
		// 调用点一：代理模式走 Settings。
		for s := range proxyToPB {
			pbSet := settingsToPB(api.Settings{ProxyMode: s})
			if pbSet.GetProxyMode() != proxyToPB[s] {
				t.Fatalf("代理模式 %q 没有出现在 Settings 的转换结果里", s)
			}
			if got := settingsFromPB(pbSet).ProxyMode; got != s {
				t.Fatalf("代理模式 %q 经 Settings 往返后变成了 %q", s, got)
			}
		}
		// 调用点二：代理模式走 AuthOptions。
		for s := range proxyToPB {
			pbAuth := authToPB(api.AuthOptions{ProxyMode: s})
			if pbAuth.GetProxyMode() != proxyToPB[s] {
				t.Fatalf("代理模式 %q 没有出现在 AuthOptions 的转换结果里", s)
			}
			if got := authFromPB(pbAuth).ProxyMode; got != s {
				t.Fatalf("代理模式 %q 经 AuthOptions 往返后变成了 %q", s, got)
			}
		}
	})

	t.Run("协议种类", func(t *testing.T) {
		for s, p := range kindToPB {
			back, ok := kindFromPB[p]
			if !ok {
				t.Fatalf("协议 %q 没有反向映射", s)
			}
			if back != s {
				t.Fatalf("协议 %q 反向映射成了 %q", s, back)
			}
		}
		if _, ok := kindFromPB[pb.ProtocolKind_PROTOCOL_KIND_UNSPECIFIED]; !ok {
			t.Fatal("kindFromPB 缺少 UNSPECIFIED 的显式条目")
		}
		// 调用点：协议种类走 AddTaskRequest。
		for s := range kindToPB {
			req := addTaskInputToPB(&api.AddTaskInput{Protocol: s})
			if req.GetProtocol() != kindToPB[s] {
				t.Fatalf("协议 %q 没有出现在 AddTaskRequest 的转换结果里", s)
			}
			if got := addTaskInputFromPB(req).Protocol; got != s {
				t.Fatalf("协议 %q 经 AddTaskRequest 往返后变成了 %q", s, got)
			}
		}
	})
}

// TestEnumTablesCoverEveryConstant 确认映射表没有漏掉契约层的常量。
//
// 只查「表里的项都有反向映射」不够：漏掉一个常量（比如新增了状态却忘了
// 加映射）那样查不出来。这边把契约层的常量逐个过一遍。
func TestEnumTablesCoverEveryConstant(t *testing.T) {
	for _, s := range []api.Status{
		api.StatusPending, api.StatusDownloading, api.StatusPaused,
		api.StatusCompleted, api.StatusFailed, api.StatusFileLost,
	} {
		if _, ok := statusToPB[s]; !ok {
			t.Fatalf("状态 %q 缺少 proto 映射", s)
		}
	}
	for _, f := range []api.StatusFilter{
		api.FilterAll, api.FilterPending, api.FilterDownloading,
		api.FilterPaused, api.FilterCompleted, api.FilterFailed,
		api.FilterFileLost,
	} {
		if _, ok := filterToPB[f]; !ok {
			t.Fatalf("筛选 %q 缺少 proto 映射", f)
		}
	}
	for _, s := range api.AllTaskSorts() {
		if _, ok := sortToPB[s]; !ok {
			t.Fatalf("排序 %q 缺少 proto 映射", s)
		}
	}
	for _, p := range []api.ProxyMode{
		api.ProxyModeSystem, api.ProxyModeDisabled, api.ProxyModeManual,
	} {
		if _, ok := proxyToPB[p]; !ok {
			t.Fatalf("代理模式 %q 缺少 proto 映射", p)
		}
	}
	for _, k := range []api.ProtocolKind{
		api.ProtoHTTP, api.ProtoHTTPS, api.ProtoFTP, api.ProtoWebDAV, api.ProtoBT,
	} {
		if _, ok := kindToPB[k]; !ok {
			t.Fatalf("协议 %q 缺少 proto 映射", k)
		}
	}
}
