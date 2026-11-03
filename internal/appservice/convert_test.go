// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package appservice

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/bittorrent"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/store"
)

func TestToAPITask(t *testing.T) {
	now := time.Now()
	src := &store.Task{
		ID: "t1", URL: "http://x/a", SavePath: "/tmp/a", Protocol: "HTTP",
		TotalSize: 100, Downloaded: 40, SupportRange: true,
		ETag: "e", LastModified: "m", DigestAlgorithm: "sha256", Digest: "d",
		IsAllocated: true, ChunkCount: 4, MinChunkSize: 1024,
		ChunkProgress: []int64{1, 2}, ChunkRanges: []int64{0, 9},
		Status: store.TaskStatus.Downloading, AuthData: "{}", ErrorMessage: "msg",
		ErrorKind: "net", CreatedAt: now, UpdatedAt: now, BTFile: "f",
	}
	got := toAPITask(src)
	if got.ID != "t1" || got.Status != api.StatusDownloading || got.TotalSize != 100 {
		t.Fatalf("转换结果不对: %+v", got)
	}
	if len(got.ChunkProgress) != 2 || got.ChunkProgress[1] != 2 {
		t.Fatalf("分片进度丢失: %+v", got.ChunkProgress)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("时间字段丢失")
	}
	if got.BTFile != "f" {
		t.Fatal("BTFile 丢失")
	}
}

func TestStatusValuesMatchStore(t *testing.T) {
	pairs := []struct {
		api   api.Status
		store store.Status
	}{
		{api.StatusPending, store.TaskStatus.Pending},
		{api.StatusDownloading, store.TaskStatus.Downloading},
		{api.StatusPaused, store.TaskStatus.Paused},
		{api.StatusCompleted, store.TaskStatus.Completed},
		{api.StatusFailed, store.TaskStatus.Failed},
		{api.StatusFileLost, store.TaskStatus.FileLost},
	}
	for _, p := range pairs {
		if string(p.api) != string(p.store) {
			t.Fatalf("状态取值不一致: api=%q store=%q", p.api, p.store)
		}
	}
}

func TestProxyModeRoundTrip(t *testing.T) {
	for _, m := range []protocol.ProxyMode{
		protocol.ProxyModeSystem, protocol.ProxyModeDisabled, protocol.ProxyModeManual,
	} {
		got := toAPIProxyMode(m)
		if toStoreProxyMode(got) != m {
			t.Fatalf("代理模式往返失败: %v → %q → %v", m, got, toStoreProxyMode(got))
		}
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	src := store.Settings{
		DefaultSaveDir: "/tmp", DefaultThreads: 8, MinChunkSize: 4096,
		UserAgent: "UA", Cookies: "C", FTPPassive: true, Prealloc: true,
		TaskSort: store.SortNameAsc, ProxyMode: protocol.ProxyModeManual,
		ProxyURL: "http://p", ProxyBypass: "b", ProxyAutoConfigURL: "u",
		DownloadLimitKiB: 512, LightMode: true, MaxConcurrent: 3,
		AutoStart: true, Language: "en", BTPort: 6881, BTMaxPeers: 50,
		BTDownloadLimitKiB: 100, BTUploadLimitKiB: 50, BTDHT: true, BTPEX: true,
		BTSeedRatio: 1.5, BTMaxSeeding: 10, BTMaxConcurrent: 2,
		BTTrackers: "udp://tracker.example:1337",
	}
	got := toAPISettings(src)
	if got.DefaultSaveDir != "/tmp" || got.TaskSort != api.SortNameAsc ||
		got.ProxyMode != api.ProxyModeManual || got.BTSeedRatio != 1.5 ||
		got.BTTrackers != src.BTTrackers {
		t.Fatalf("设置转换不对: %+v", got)
	}
	back := toStoreSettings(got)
	if back.DefaultSaveDir != src.DefaultSaveDir || back.ProxyMode != src.ProxyMode ||
		back.TaskSort != src.TaskSort || back.BTMaxPeers != src.BTMaxPeers ||
		back.BTTrackers != src.BTTrackers {
		t.Fatalf("设置往返失败: %+v", back)
	}
}

func TestAuthOptionsRoundTrip(t *testing.T) {
	src := api.AuthOptions{
		Username: "u", Password: "p", UserAgent: "UA", Cookies: "C",
		Referer: "r", FTPPassive: true, ProxyMode: api.ProxyModeManual,
		ProxyURL: "http://p", ProxyBypass: "b", ProxyAutoConfigURL: "u",
	}
	// 契约层 → 协议层
	proto := toProtocolAuthOptions(src)
	if proto.Username != "u" || proto.Password != "p" || proto.UserAgent != "UA" ||
		proto.Cookies != "C" || proto.Referer != "r" || !proto.FTPPassive ||
		proto.ProxyMode != protocol.ProxyModeManual || proto.ProxyURL != "http://p" ||
		proto.ProxyBypass != "b" || proto.ProxyAutoConfigURL != "u" {
		t.Fatalf("凭据转协议层不对: %+v", proto)
	}
	// 协议层 → 契约层，再回到起点
	back := toAPIAuthOptions(proto)
	if back != src {
		t.Fatalf("凭据往返失败: %+v → %+v", src, back)
	}
}

func TestToAPIBTDetail(t *testing.T) {
	src := &bittorrent.Detail{
		Name: "n", InfoHash: "h", Total: 10, Downloaded: 5, Uploaded: 1,
		SpeedBPS: 2, UploadBPS: 3, Peers: 4, TotalPeers: 5, Seeders: 6,
		Leechers: 7, PieceLength: 8, Pieces: 9, PiecesDone: 10,
		PieceBuckets: []uint8{1, 2}, Seeding: true, Complete: true,
		Files:    []bittorrent.FileProgress{{Path: "p", Length: 1, Completed: 2}},
		Trackers: []string{"t"},
	}
	got := toAPIBTDetail(src)
	if got.InfoHash != "h" || got.PieceBuckets[1] != 2 || got.Files[0].Path != "p" {
		t.Fatalf("BT 详情转换不对: %+v", got)
	}
}

// TestDTOFieldParity 用反射逐字段核对契约层 DTO 与来源结构体的字段集合。
//
// 这是「静默丢数据」的最后一道护栏：将来给 store.Task 加了字段却忘了在
// toAPITask 里搬运，契约层就会少一个字段，而其它测试照样全绿——用户
// 的设置或任务信息就那样消失了，没有任何报错。
func TestDTOFieldParity(t *testing.T) {
	cases := []struct {
		name   string
		api    any
		source any
	}{
		{"Task", api.Task{}, store.Task{}},
		{"Settings", api.Settings{}, store.Settings{}},
		{"BTDetail", api.BTDetail{}, bittorrent.Detail{}},
		{"AuthOptions", api.AuthOptions{}, protocol.AuthOptions{}},
	}
	for _, c := range cases {
		want := fieldNames(reflect.TypeOf(c.source))
		got := fieldNames(reflect.TypeOf(c.api))
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%s 的字段集合与来源不一致:\n来源 = %v\n契约 = %v", c.name, want, got)
		}
	}
}

// fieldNames 返回结构体的导出字段名集合（排序）。
func fieldNames(t reflect.Type) []string {
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // 非导出字段
		}
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

// TestConvertCarriesEveryFieldValue 守住「转换函数把**值**也搬对了」。
//
// TestDTOFieldParity 只比字段名集合：它抓得住「DTO 少了一个字段」，抓不住
// 「字段都在但转换函数忘搬了值」——后者会让某个设置在 UI 里显示成零值，
// 静默地改掉用户的行为。
//
// 这里对每一对「来源类型 → 契约类型」，用反射给来源的每个字段填一个
// 非零值，跑一遍转换，再逐字段断言契约侧拿到了同一个值。
func TestConvertCarriesEveryFieldValue(t *testing.T) {
	cases := []struct {
		name    string
		convert func() (src any, dst any)
	}{
		{"Settings", func() (any, any) {
			s := store.Settings{
				DefaultSaveDir: "/tmp/d", DefaultThreads: 7, MinChunkSize: 4096,
				UserAgent: "UA/1.0", Cookies: "a=b", FTPPassive: true, Prealloc: true,
				TaskSort: store.SortCreatedDesc, ProxyMode: protocol.ProxyModeManual,
				ProxyURL: "http://p:1", ProxyBypass: "localhost",
				ProxyAutoConfigURL: "http://pac", DownloadLimitKiB: 512,
				LightMode: true, MaxConcurrent: 9, AutoStart: true, Language: "ja",
				BTPort: 6881, BTMaxPeers: 33, BTDownloadLimitKiB: 64,
				BTUploadLimitKiB: 32, BTDHT: true, BTPEX: true, BTSeedRatio: 2,
				BTMaxSeeding: 4, BTMaxConcurrent: 5, BTTrackers: "udp://t:1337",
			}
			return s, toAPISettings(s)
		}},
		{"Task", func() (any, any) {
			t := store.Task{
				ID: "id1", URL: "http://x/a", SavePath: "/tmp/a", Protocol: "HTTP",
				TotalSize: 100, Downloaded: 40, SupportRange: true,
				ETag: "etag", LastModified: "lm", DigestAlgorithm: "sha256", Digest: "dg",
				IsAllocated: true, ChunkCount: 4, MinChunkSize: 1024,
				ChunkProgress: []int64{1, 2}, ChunkRanges: []int64{0, 9},
				Status: store.TaskStatus.Downloading, AuthData: "{}", ErrorMessage: "err",
				ErrorKind: "kind", BTFile: "bt",
			}
			return t, toAPITask(&t)
		}},
		{"AuthOptions", func() (any, any) {
			a := protocol.AuthOptions{
				UserAgent: "UA/2.0", Cookies: "c=d", ProxyMode: protocol.ProxyModeManual,
				ProxyURL: "http://q:2", ProxyBypass: "127.0.0.1", ProxyAutoConfigURL: "http://pac2",
			}
			return a, toAPIAuthOptions(a)
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src, dst := c.convert()
			assertSameFieldValues(t, reflect.TypeOf(src), reflect.ValueOf(src), reflect.ValueOf(dst))
		})
	}
}

// assertSameFieldValues 逐字段断言 dst 的每个字段与 src 的同名同类型字段
// 取值相同。
//
// 字段名可能不完全一致（例如协议枚举要转换），所以只在**两边同类型**时
// 比较；类型不同的字段由各自的转换函数负责（枚举那几个另有专项测试）。
func assertSameFieldValues(t *testing.T, typ reflect.Type, src, dst reflect.Value) {
	t.Helper()
	// dst 可能是指针（Task 的转换返回 *api.Task）。
	dstVal := dst
	for dstVal.Kind() == reflect.Ptr {
		dstVal = dstVal.Elem()
	}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.PkgPath != "" {
			continue // 非导出字段
		}
		sf := src.Field(i)
		df := dstVal.FieldByName(f.Name)
		if !df.IsValid() {
			t.Errorf("契约侧没有字段 %s", f.Name)
			continue
		}
		if sf.Type() != df.Type() {
			continue // 类型不同（枚举等），不在这里比
		}
		if !reflect.DeepEqual(sf.Interface(), df.Interface()) {
			t.Errorf("字段 %s 的值没搬过来: 来源 = %v，契约 = %v",
				f.Name, sf.Interface(), df.Interface())
		}
	}
}

// TestStatusFilterValuesMatchStore 守住筛选条件的取值一致。
//
// api.StatusFilter 与 store.StatusFilter 是两套独立的定义，取值必须逐字
// 相同：漂移了不会报错，只会让筛选**静默失效**——传过去的值在 store 那边
// 匹配不到任何任务，UI 上表现为「点了筛选但列表没变」。
//
// Status 已有 TestStatusValuesMatchStore 这类锁定，StatusFilter 之前没有。
func TestStatusFilterValuesMatchStore(t *testing.T) {
	pairs := []struct {
		name string
		api  api.StatusFilter
		st   store.StatusFilter
	}{
		{"all", api.FilterAll, store.FilterAll},
		{"pending", api.FilterPending, store.FilterPending},
		{"downloading", api.FilterDownloading, store.FilterDownloading},
		{"paused", api.FilterPaused, store.FilterPaused},
		{"completed", api.FilterCompleted, store.FilterCompleted},
		{"failed", api.FilterFailed, store.FilterFailed},
		{"filelost", api.FilterFileLost, store.FilterFileLost},
	}
	for _, p := range pairs {
		if string(p.api) != string(p.st) {
			t.Errorf("%s: api = %q，store = %q —— 两侧漂移", p.name, p.api, p.st)
		}
	}
}
