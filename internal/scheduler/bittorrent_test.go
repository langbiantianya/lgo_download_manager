// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"lgo_download_manager/internal/bittorrent"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/store"
)

// TestBittorrentConfigFromSettings 校验设置 → BT 客户端配置的映射:
// KiB/s 要换算成 bytes/s,<=0 表示不限速(mapped 成 0 由 bittorrent 包
// 解释为 rate.Inf),tracker 串要按行拆开。
func TestBittorrentConfigFromSettings(t *testing.T) {
	s := store.Settings{
		BTPort:             51413,
		BTMaxPeers:         25,
		BTDownloadLimitKiB: 1024,
		BTUploadLimitKiB:   64,
		BTDHT:              true,
		BTPEX:              false,
		BTSeeding:          true,
		BTTrackers:         "udp://a.example:6969/announce, http://b.example/announce\nudp://c.example:1337/announce",
	}
	cfg := BittorrentConfigFrom(s)
	if cfg.ListenPort != 51413 {
		t.Errorf("ListenPort = %d, want 51413", cfg.ListenPort)
	}
	if cfg.MaxPeersPerTorrent != 25 {
		t.Errorf("MaxPeersPerTorrent = %d, want 25", cfg.MaxPeersPerTorrent)
	}
	if cfg.DownloadLimit != 1024*1024 {
		t.Errorf("DownloadLimit = %d, want %d", cfg.DownloadLimit, 1024*1024)
	}
	if cfg.UploadLimit != 64*1024 {
		t.Errorf("UploadLimit = %d, want %d", cfg.UploadLimit, 64*1024)
	}
	if !cfg.EnableDHT || cfg.EnablePEX || !cfg.Seeding {
		t.Errorf("flags = dht:%v pex:%v seeding:%v, want true/false/true", cfg.EnableDHT, cfg.EnablePEX, cfg.Seeding)
	}
	want := []string{
		"udp://a.example:6969/announce",
		"http://b.example/announce",
		"udp://c.example:1337/announce",
	}
	if !reflect.DeepEqual(cfg.ExtraTrackers, want) {
		t.Errorf("ExtraTrackers = %v, want %v", cfg.ExtraTrackers, want)
	}
	if cfg.HTTPProxy == nil {
		t.Error("HTTPProxy = nil, want the protocol package's proxy resolver")
	}

	// MaxPeers 未配置(0)时必须落到默认值,否则客户端会以 0 连接数启动。
	empty := BittorrentConfigFrom(store.Settings{})
	if empty.MaxPeersPerTorrent != store.DefaultBTMaxPeers {
		t.Errorf("default MaxPeersPerTorrent = %d, want %d", empty.MaxPeersPerTorrent, store.DefaultBTMaxPeers)
	}
}

// TestSplitTrackers 覆盖 tracker 串的各种分隔方式与空项过滤。
func TestSplitTrackers(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   \n\t ", nil},
		{"udp://a:1", []string{"udp://a:1"}},
		{"udp://a:1,udp://b:2", []string{"udp://a:1", "udp://b:2"}},
		{"udp://a:1\n\nudp://b:2\n", []string{"udp://a:1", "udp://b:2"}},
		{"udp://a:1;;udp://b:2", []string{"udp://a:1", "udp://b:2"}},
	}
	for _, c := range cases {
		if got := SplitTrackers(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitTrackers(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestBittorrentDefaultDir 校验默认任务目录名的推导:磁力链接优先用 dn,
// 没有 dn 用 infohash 前缀,种子文件用去掉后缀的文件名。
func TestBittorrentDefaultDir(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Ubuntu%2024.04", "Ubuntu 24.04"},
		{"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", "magnet-0123456789abcdef"},
		{"magnet:?xt=urn:btmh:1220caf1e1c30e81cb361b9ee167c4aa64228a7fa4fa914f7d0483e0c8cc8b0bb1e1", "magnet-1220caf1e1c30e81"},
		{"magnet:?xs=urn:btih:zzz", "magnet"},
		{"https://example.com/files/debian-13.iso.torrent", "debian-13.iso"},
		{"/home/user/incoming/ubuntu.torrent", "ubuntu"},
		{"https://example.com/files/debian.iso.torrent?token=abc", "debian.iso"},
		{"https://example.com/a.zip", ""},
	}
	for _, c := range cases {
		if got := BittorrentDefaultDir(c.url); got != c.want {
			t.Errorf("BittorrentDefaultDir(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// TestIsBittorrentTask 校验任务是否走 BT 路径的判定:显式 protocol 优先,
// 历史空 protocol 的行按 URL 形态兜底。
func TestIsBittorrentTask(t *testing.T) {
	cases := []struct {
		task *store.Task
		want bool
	}{
		{&store.Task{URL: "magnet:?xt=urn:btih:x", Protocol: "BT"}, true},
		{&store.Task{URL: "https://example.com/x.zip", Protocol: "HTTPS"}, false},
		{&store.Task{URL: "magnet:?xt=urn:btih:x", Protocol: ""}, true},
		{&store.Task{URL: "/tmp/x.torrent", Protocol: ""}, true},
		{&store.Task{URL: "https://example.com/x.zip", Protocol: ""}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := IsBittorrentTask(c.task); got != c.want {
			t.Errorf("IsBittorrentTask(%+v) = %v, want %v", c.task, got, c.want)
		}
	}
}

// TestRemoveTaskData 校验删除语义:普通任务删文件(不动同级其它文件),
// BT 任务删整个任务目录(种子内容与 .part 都在里面)。
func TestRemoveTaskData(t *testing.T) {
	dir := t.TempDir()

	// 普通任务:SavePath 是文件,同级邻居必须保留。
	file := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(file, []byte("neighbour"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(target, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	removeTaskData(&store.Task{URL: "https://x/a.bin", Protocol: "HTTPS", SavePath: target})
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("file task data still present: %v", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("unrelated neighbour was removed: %v", err)
	}

	// BT 任务:SavePath 是任务目录,整体删除。
	taskDir := filepath.Join(dir, "torrent-task")
	if err := os.MkdirAll(filepath.Join(taskDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "sub", "part.bin.part"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	removeTaskData(&store.Task{URL: "magnet:?xt=urn:btih:y", Protocol: "BT", SavePath: taskDir})
	if _, err := os.Stat(taskDir); !os.IsNotExist(err) {
		t.Errorf("bt task dir still present: %v", err)
	}
}

// TestBtChunkLayout 校验「每个种子文件 = 一个分片」的换算:区间按文件偏移
// 依序拼接且互不重叠,进度取各文件已完成字节并被裁到文件长度以内。
func TestBtChunkLayout(t *testing.T) {
	files := []bittorrent.FileProgress{
		{Path: "t/a.bin", Length: 100, Completed: 100},
		{Path: "t/b.bin", Length: 50, Completed: 20},
		{Path: "t/empty", Length: 0, Completed: 0},
		{Path: "t/c.bin", Length: 30, Completed: 999}, // 越界应被裁剪
	}
	ranges := btChunkRanges(files)
	wantRanges := []int64{0, 99, 100, 149, 150, 149, 150, 179}
	if !reflect.DeepEqual(ranges, wantRanges) {
		t.Errorf("btChunkRanges = %v, want %v", ranges, wantRanges)
	}
	progress := btChunkProgress(files)
	wantProgress := []int64{100, 20, 0, 30}
	if !reflect.DeepEqual(progress, wantProgress) {
		t.Errorf("btChunkProgress = %v, want %v", progress, wantProgress)
	}

	// 元数据未到时两个换算都返回 nil(UI 保持「未知大小」)。
	if got := btChunkRanges(nil); got != nil {
		t.Errorf("btChunkRanges(nil) = %v, want nil", got)
	}
	if got := btChunkProgress(nil); got != nil {
		t.Errorf("btChunkProgress(nil) = %v, want nil", got)
	}
}

// TestFlushKeepsBtChunkProgress 是回归用例:BT 任务没有 engine 分片快照,
// flushAll 必须沿用任务快照里的分片进度,而不是把 chunkProgressFrom(nil)
// 的空数组写回数据库(那会让详情页的分块图每 2 秒被清空一次)。
func TestFlushKeepsBtChunkProgress(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sc := New(st)
	tk := &store.Task{
		ID: "bt-1", URL: "magnet:?xt=urn:btih:z", SavePath: filepath.Join(t.TempDir(), "task"),
		Protocol: "BT", Status: store.TaskStatus.Downloading, TotalSize: 150,
		ChunkRanges: []int64{0, 99, 100, 149}, ChunkProgress: []int64{100, 20},
	}
	if err := st.CreateTask(tk); err != nil {
		t.Fatal(err)
	}
	rj := &runningJob{task: tk, dirty: true, status: store.TaskStatus.Downloading}
	sc.jobs[tk.ID] = rj
	sc.flushAll()

	got, err := st.GetTask(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.ChunkProgress, []int64{100, 20}) {
		t.Fatalf("persisted ChunkProgress = %v, want [100 20]", got.ChunkProgress)
	}
	if got.Downloaded != 0 && got.Downloaded != 120 {
		t.Fatalf("persisted Downloaded = %d, want 0 or 120", got.Downloaded)
	}
}

// TestIsPreparingForBt 校验 BT 任务的「准备中」判定:Start 预留 slot 之后
// engine 接管之前才是准备中,engine(BT 客户端)接管后必须不再是准备中,
// 否则 UI 会一直显示「准备中」。
func TestIsPreparingForBt(t *testing.T) {
	sc := New(nil)
	// 复现 Start 的生命周期:准备阶段只有 prepareCancel;engine(BT 客户端)
	// 接管后补上 cancel,从此不再是「准备中」。
	prepareCtx, prepareCancel := context.WithCancel(context.Background())
	defer prepareCancel()
	rj := &runningJob{prepareCtx: prepareCtx, prepareCancel: prepareCancel, sess: newJobSession()}
	sc.jobs["t"] = rj
	if !sc.IsPreparing("t") {
		t.Error("task before engine handover should be reported as preparing")
	}
	rj.cancel = func() {}
	if sc.IsPreparing("t") {
		t.Error("task after engine handover must not be reported as preparing")
	}
}

// TestProtocolDetectionForBt 顺带锁住协议识别:磁力链接与 .torrent 必须
// 被识别为 BT,否则任务会落到 HTTP 驱动并立即失败。
func TestProtocolDetectionForBt(t *testing.T) {
	cases := map[string]protocol.ProtocolKind{
		"magnet:?xt=urn:btih:aa":            protocol.ProtoBT,
		"https://example.com/a.torrent":     protocol.ProtoBT,
		"https://example.com/a.torrent?x=1": protocol.ProtoBT,
		"/tmp/a.torrent":                    protocol.ProtoBT,
		"https://example.com/a.zip":         protocol.ProtoHTTPS,
		"ftp://example.com/a.bin":           protocol.ProtoFTP,
	}
	for raw, want := range cases {
		got, err := protocol.DetectKind(raw, "")
		if err != nil {
			t.Errorf("DetectKind(%q) error: %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("DetectKind(%q) = %q, want %q", raw, got, want)
		}
	}
}
