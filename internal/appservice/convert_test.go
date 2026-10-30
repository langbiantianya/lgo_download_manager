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
