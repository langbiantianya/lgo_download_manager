// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tk := &Task{
		ID:           "task-1",
		URL:          "https://example.com/a.bin",
		SavePath:     filepath.Join(dir, "a.bin"),
		Protocol:     "HTTPS",
		TotalSize:    1234,
		ChunkCount:   4,
		SupportRange: true,
		ETag:         `"v1"`,
		LastModified: "Thu, 29 Oct 2026 04:00:00 GMT",
		IsAllocated:  true,
		Status:       TaskStatus.Pending,
		AuthData:     `{"username":""}`,
	}
	if err := s.CreateTask(tk); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := s.UpdateTaskProgress("task-1", 512, []int64{128, 128, 128, 128}, TaskStatus.Downloading, ""); err != nil {
		t.Fatalf("UpdateTaskProgress: %v", err)
	}

	got, err := s.GetTask("task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.URL != tk.URL || got.Status != TaskStatus.Downloading || got.Downloaded != 512 {
		t.Fatalf("unexpected row: %+v", got)
	}
	if got.ETag != tk.ETag || got.LastModified != tk.LastModified {
		t.Fatalf("validators = %q / %q, want %q / %q", got.ETag, got.LastModified, tk.ETag, tk.LastModified)
	}
	if len(got.ChunkProgress) != 4 || got.ChunkProgress[0] != 128 {
		t.Fatalf("chunk progress mismatch: %v", got.ChunkProgress)
	}
	if err := s.UpdateTaskValidators("task-1", `"v2"`, "Fri, 30 Oct 2026 04:00:00 GMT"); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetTask("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ETag != `"v2"` || got.LastModified != "Fri, 30 Oct 2026 04:00:00 GMT" {
		t.Fatalf("updated validators = %q / %q", got.ETag, got.LastModified)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatal("timestamps should be set")
	}
	if !got.UpdatedAt.After(got.CreatedAt.Add(-1 * time.Second)) {
		t.Fatal("updated_at should be >= created_at")
	}

	// ListTasks 应该返回我们插入的那一行。
	list, err := s.ListTasks(FilterAll, SortCreatedDesc)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("ListTasks len=%d", len(list))
	}

	// 删除并验证。
	if err := s.DeleteTask("task-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTask("task-1"); err == nil {
		t.Fatal("expected error after Delete")
	}
}

func TestListPendingTasksLimitAndFIFO(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	base := time.Now().UTC()
	for i, id := range []string{"old", "middle", "new"} {
		if err := s.CreateTask(&Task{
			ID: id, URL: "https://example.com/" + id, SavePath: "/tmp/" + id,
			Protocol: "HTTPS", Status: TaskStatus.Pending,
			ChunkProgress: []int64{}, CreatedAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpdateTaskProgress("middle", 0, nil, TaskStatus.Downloading, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListPendingTasks(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "old" {
		t.Fatalf("ListPendingTasks(1) = %v, want oldest pending task", got)
	}
}

func TestListPendingTasksByProtocolKeepsHTTPVisibleBehindBT(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Now().UTC()
	for i, tk := range []*Task{
		{ID: "bt-old", URL: "magnet:?xt=urn:btih:old", SavePath: "/tmp/bt", Protocol: "BT", Status: TaskStatus.Pending, CreatedAt: base},
		{ID: "http-new", URL: "https://example.com/file", SavePath: "/tmp/file", Protocol: "HTTPS", Status: TaskStatus.Pending, CreatedAt: base.Add(time.Second)},
	} {
		tk.ChunkProgress = []int64{}
		if err := s.CreateTask(tk); err != nil {
			t.Fatalf("CreateTask %d: %v", i, err)
		}
	}
	got, err := s.ListPendingTasksByProtocol(1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "http-new" {
		t.Fatalf("non-BT pending = %v, want http-new", got)
	}
}

func TestSettingsRoundtripPACAndDownloadLimit(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want := Settings{
		DefaultSaveDir:     "/tmp/downloads",
		ProxyAutoConfigURL: "http://config.example/proxy.pac",
		DownloadLimitKiB:   4096,
	}
	if err := s.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.ProxyAutoConfigURL != want.ProxyAutoConfigURL || got.DownloadLimitKiB != want.DownloadLimitKiB {
		t.Fatalf("advanced settings = %q / %d", got.ProxyAutoConfigURL, got.DownloadLimitKiB)
	}
}

func TestStoreReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTask(&Task{
		ID: "x", URL: "u", SavePath: "/tmp/x", Protocol: "HTTP",
		ChunkProgress: []int64{}, Status: TaskStatus.Pending,
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// 重新打开并读取。
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.GetTask("x")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "x" {
		t.Fatalf("ID mismatch: %s", got.ID)
	}
}
func TestListTasksFilter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 插入 3 个不同状态的任务。
	mkTask := func(id string, status Status) {
		if err := s.CreateTask(&Task{
			ID: id, URL: "u", SavePath: "/tmp/" + id, Protocol: "HTTP",
			ChunkProgress: []int64{}, Status: status,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mkTask("a", TaskStatus.Pending)
	mkTask("b", TaskStatus.Downloading)
	mkTask("c", TaskStatus.Completed)

	// FilterAll 返回全部 3 行。
	all, err := s.ListTasks(FilterAll, SortCreatedDesc)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("FilterAll len=%d want 3", len(all))
	}

	// FilterDownloading 只返回 Downloading 状态的 1 行。
	dl, err := s.ListTasks(FilterDownloading, SortCreatedDesc)
	if err != nil {
		t.Fatal(err)
	}
	if len(dl) != 1 || dl[0].ID != "b" {
		t.Fatalf("FilterDownloading=%v want only task b", dl)
	}

	// FilterCompleted 只返回 1 行。
	done, err := s.ListTasks(FilterCompleted, SortCreatedDesc)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 1 || done[0].ID != "c" {
		t.Fatalf("FilterCompleted=%v want only task c", done)
	}
}
func TestCompletedAt(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 新建任务，CompletedAt 应为零值。
	if err := s.CreateTask(&Task{
		ID: "x", URL: "u", SavePath: "/tmp/x", Protocol: "HTTP",
		ChunkProgress: []int64{}, Status: TaskStatus.Pending,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTask("x")
	if err != nil {
		t.Fatal(err)
	}
	if !got.CompletedAt.IsZero() {
		t.Fatalf("CompletedAt should be zero initially, got %v", got.CompletedAt)
	}

	// 状态变为 Completed 时，CompletedAt 应被自动写入。
	if err := s.UpdateTaskProgress("x", 0, nil, TaskStatus.Completed, ""); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetTask("x")
	if err != nil {
		t.Fatal(err)
	}
	if got.CompletedAt.IsZero() {
		t.Fatal("CompletedAt should be set after Completed transition")
	}
	if !got.CompletedAt.After(time.Now().Add(-time.Hour)) {
		t.Fatalf("CompletedAt should be recent, got %v", got.CompletedAt)
	}

	// 多次 UpdateTaskProgress 不应覆盖原 CompletedAt（用 COALESCE 保留）。
	first := got.CompletedAt
	time.Sleep(10 * time.Millisecond)
	if err := s.UpdateTaskProgress("x", 0, nil, TaskStatus.Completed, ""); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetTask("x")
	if !got.CompletedAt.Equal(first) {
		t.Fatalf("CompletedAt should not be overwritten; was %v, now %v", first, got.CompletedAt)
	}
}
func TestListTasksSort(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 插入 3 个任务，通过 SetCreatedAt 控制时间先后。
	mk := func(id, path string, created time.Time) {
		if err := s.CreateTask(&Task{
			ID: id, URL: "u", SavePath: path, Protocol: "HTTP",
			ChunkProgress: []int64{}, Status: TaskStatus.Pending,
		}); err != nil {
			t.Fatal(err)
		}
		// 直接更新 created_at 绕过 CreateTask 默认值。
		if _, err := s.db.Exec(`UPDATE tasks SET created_at=? WHERE id=?`, created.UTC(), id); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().UTC()
	mk("a", "/tmp/banana.bin", base.Add(-2*time.Hour))
	mk("b", "/tmp/apple.bin", base.Add(-1*time.Hour))
	mk("c", "/tmp/cherry.bin", base)

	// SortCreatedDesc: c, b, a（最新的在前）
	got, err := s.ListTasks(FilterAll, SortCreatedDesc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != "c" || got[1].ID != "b" || got[2].ID != "a" {
		t.Fatalf("SortCreatedDesc order: %v", gotIDs(got))
	}

	// SortCreatedAsc: a, b, c
	got, err = s.ListTasks(FilterAll, SortCreatedAsc)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "c" {
		t.Fatalf("SortCreatedAsc order: %v", gotIDs(got))
	}

	// SortNameAsc: apple, banana, cherry
	got, err = s.ListTasks(FilterAll, SortNameAsc)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ID != "b" || got[1].ID != "a" || got[2].ID != "c" {
		t.Fatalf("SortNameAsc order: %v", gotIDs(got))
	}

	// SortNameDesc: cherry, banana, apple
	got, err = s.ListTasks(FilterAll, SortNameDesc)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ID != "c" || got[1].ID != "a" || got[2].ID != "b" {
		t.Fatalf("SortNameDesc order: %v", gotIDs(got))
	}
}

func gotIDs(ts []*Task) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}
func TestSettingsRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 首次加载返回零值。
	got, err := s.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskSort != "" {
		t.Fatalf("initial TaskSort should be empty, got %q", got.TaskSort)
	}

	// 保存完整 settings 后重新打开 DB。
	want := Settings{
		DefaultSaveDir: "/tmp/dl",
		DefaultThreads: 8,
		MinChunkSize:   4 * (1 << 20),
		UserAgent:      "curl/8.0",
		Cookies:        "k=v",
		FTPPassive:     false,
		Prealloc:       true,
		TaskSort:       SortNameAsc,
		MaxConcurrent:  5,
	}
	if err := s.SaveSettings(want); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err = s2.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskSort != want.TaskSort {
		t.Fatalf("TaskSort: got %q want %q", got.TaskSort, want.TaskSort)
	}
	if got.DefaultSaveDir != want.DefaultSaveDir {
		t.Fatalf("DefaultSaveDir: got %q want %q", got.DefaultSaveDir, want.DefaultSaveDir)
	}
	if got.DefaultThreads != want.DefaultThreads {
		t.Fatalf("DefaultThreads: got %d want %d", got.DefaultThreads, want.DefaultThreads)
	}
	if got.FTPPassive != want.FTPPassive {
		t.Fatalf("FTPPassive: got %v want %v", got.FTPPassive, want.FTPPassive)
	}
	if got.MaxConcurrent != want.MaxConcurrent {
		t.Fatalf("MaxConcurrent: got %d want %d", got.MaxConcurrent, want.MaxConcurrent)
	}
}

// TestSettingsBittorrentRoundtrip 覆盖 BT 设置列的往返:新增列在旧库上
// 由迁移补齐缺省值(DHT/PEX 开、端口随机、不限速),写入后再读必须原样
// 返回——「设置」页的每个开关都依赖这条路径。
func TestSettingsBittorrentRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 全新库上 LoadSettings 返回零值行——业务默认值由 settings.Load 的
	// 首次运行分支套用(见 internal/settings)。这里只锁住「0 表示未配置」
	// 这一约定,以及 EffectiveBTMaxPeers 的兜底。
	got, err := s.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.BTPort != 0 || got.BTMaxPeers != 0 {
		t.Errorf("default BTPort/BTMaxPeers = %d/%d, want 0/0", got.BTPort, got.BTMaxPeers)
	}
	if got.BTDownloadLimitKiB != 0 || got.BTUploadLimitKiB != 0 {
		t.Errorf("default limits = %d/%d, want 0/0", got.BTDownloadLimitKiB, got.BTUploadLimitKiB)
	}
	if got.EffectiveBTMaxPeers() != DefaultBTMaxPeers {
		t.Errorf("EffectiveBTMaxPeers() = %d, want %d", got.EffectiveBTMaxPeers(), DefaultBTMaxPeers)
	}
	// 列级缺省值必须对「迁移后已存在的行」同样生效:老库升级上来时,
	// settings 行早就存在,只有 ALTER 的 DEFAULT 能给它补上 DHT/PEX 开启。
	var dht, pex int
	if err := s.db.QueryRow(`SELECT bt_dht, bt_pex FROM settings WHERE id=1`).Scan(&dht, &pex); err != nil {
		t.Fatal(err)
	}
	if dht != 1 || pex != 1 {
		t.Errorf("migrated column defaults = dht %d / pex %d, want 1/1", dht, pex)
	}

	want := got
	want.BTPort = 51413
	want.BTMaxPeers = 64
	want.BTDownloadLimitKiB = 2048
	want.BTUploadLimitKiB = 128
	want.BTDHT = false
	want.BTPEX = false
	want.BTSeedRatio = 2.5
	want.BTMaxSeeding = 3
	want.BTMaxConcurrent = 4
	want.BTTrackers = "udp://tracker.example.com:6969/announce\nhttp://t.example.org/announce"
	if err := s.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err = s2.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.BTPort != want.BTPort || got.BTMaxPeers != want.BTMaxPeers {
		t.Errorf("BTPort/BTMaxPeers = %d/%d, want %d/%d", got.BTPort, got.BTMaxPeers, want.BTPort, want.BTMaxPeers)
	}
	if got.BTDownloadLimitKiB != want.BTDownloadLimitKiB || got.BTUploadLimitKiB != want.BTUploadLimitKiB {
		t.Errorf("limits = %d/%d, want %d/%d", got.BTDownloadLimitKiB, got.BTUploadLimitKiB,
			want.BTDownloadLimitKiB, want.BTUploadLimitKiB)
	}
	if got.BTDHT != want.BTDHT || got.BTPEX != want.BTPEX ||
		got.BTSeedRatio != want.BTSeedRatio || got.BTMaxSeeding != want.BTMaxSeeding ||
		got.BTMaxConcurrent != want.BTMaxConcurrent {
		t.Errorf("bt flags = %v/%v/%v/%d/%d, want %v/%v/%v/%d/%d",
			got.BTDHT, got.BTPEX, got.BTSeedRatio, got.BTMaxSeeding, got.BTMaxConcurrent,
			want.BTDHT, want.BTPEX, want.BTSeedRatio, want.BTMaxSeeding, want.BTMaxConcurrent)
	}
	if got.BTTrackers != want.BTTrackers {
		t.Errorf("BTTrackers = %q, want %q", got.BTTrackers, want.BTTrackers)
	}
}

// TestSettingsBittorrentSeedingMigration 验证旧库只有布尔 bt_seeding 时,
// 新列 bt_seed_ratio / bt_max_seeding / bt_max_concurrent 的 -1 哨兵
// 会落到新版默认值。
func TestSettingsBittorrentSeedingMigration(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.LoadSettings(); err != nil { // 创建 settings 行
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE settings SET bt_seeding=1, bt_seed_ratio=-1, bt_max_seeding=-1, bt_max_concurrent=-1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.BTSeedRatio != DefaultBTSeedRatio || got.BTMaxSeeding != DefaultBTMaxSeeding ||
		got.BTMaxConcurrent != DefaultBTMaxConcurrent {
		t.Errorf("legacy defaults = ratio %v / seeding %d / concurrent %d, want %v / %d / %d",
			got.BTSeedRatio, got.BTMaxSeeding, got.BTMaxConcurrent,
			DefaultBTSeedRatio, DefaultBTMaxSeeding, DefaultBTMaxConcurrent)
	}

	if _, err := s.db.Exec(`UPDATE settings SET bt_seeding=0, bt_seed_ratio=-1, bt_max_seeding=-1, bt_max_concurrent=-1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	got, err = s.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.BTSeedRatio != DefaultBTSeedRatio || got.BTMaxSeeding != DefaultBTMaxSeeding ||
		got.BTMaxConcurrent != DefaultBTMaxConcurrent {
		t.Errorf("legacy defaults = ratio %v / seeding %d / concurrent %d, want %v / %d / %d",
			got.BTSeedRatio, got.BTMaxSeeding, got.BTMaxConcurrent,
			DefaultBTSeedRatio, DefaultBTMaxSeeding, DefaultBTMaxConcurrent)
	}
}

// TestTorrentMetainfoCache 覆盖 BT 元数据缓存表:写入后可读、重复写入
// 覆盖、删除任务时一并清理(否则 bt_metainfo 会留下永不回收的孤儿行)。
func TestTorrentMetainfoCache(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 未缓存时返回 (nil, nil),调用方据此回退到磁力链接。
	if got, err := s.LoadTorrentMetainfo("t1"); err != nil || got != nil {
		t.Fatalf("LoadTorrentMetainfo(empty) = %v, %v; want nil, nil", got, err)
	}
	info := []byte("d4:infodё-fake-info-bytes")
	if err := s.SaveTorrentMetainfo("t1", info); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadTorrentMetainfo("t1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(info) {
		t.Fatalf("cached info = %q, want %q", got, info)
	}
	// 覆盖写。
	if err := s.SaveTorrentMetainfo("t1", []byte("second")); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.LoadTorrentMetainfo("t1"); string(got) != "second" {
		t.Fatalf("cached info after overwrite = %q, want second", got)
	}
	// 空字节视为「没有元数据」,不写库。
	if err := s.SaveTorrentMetainfo("t2", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.LoadTorrentMetainfo("t2"); got != nil {
		t.Fatalf("empty info should not be cached, got %q", got)
	}

	// 删除任务必须级联清掉缓存。
	tk := &Task{ID: "t1", URL: "magnet:?xt=urn:btih:x", SavePath: dir, Protocol: "BT",
		Status: TaskStatus.Pending}
	if err := s.CreateTask(tk); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTorrentMetainfo("t1", info); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTask("t1"); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.LoadTorrentMetainfo("t1"); got != nil {
		t.Fatalf("metainfo survived task deletion: %q", got)
	}
}

// TestTaskBTFileRoundtrip 覆盖 BT 单文件任务的相对路径列:它决定 UI 的
// 「打开文件」按钮指向哪个文件。
func TestTaskBTFileRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tk := &Task{ID: "bt1", URL: "magnet:?xt=urn:btih:y", SavePath: filepath.Join(dir, "task"),
		Protocol: "BT", Status: TaskStatus.Pending, BTFile: "ubuntu.iso"}
	if err := s.CreateTask(tk); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTask("bt1")
	if err != nil {
		t.Fatal(err)
	}
	if got.BTFile != "ubuntu.iso" {
		t.Fatalf("BTFile = %q, want ubuntu.iso", got.BTFile)
	}
	if err := s.UpdateTaskBTFile("bt1", ""); err != nil {
		t.Fatal(err)
	}
	if got, err = s.GetTask("bt1"); err != nil {
		t.Fatal(err)
	}
	if got.BTFile != "" {
		t.Fatalf("BTFile after clear = %q, want empty", got.BTFile)
	}
}

// TestLoadSettingsEx_SavedFlag 锁定「这份设置是否已经被保存过」的判定。
//
// settings 表要求恰好一行,LoadSettings 在库里没有行时会插入一条占位行。
// 占位行不能当成「用户保存过设置」——否则「下载目录被清空」这类单字段
// 异常会被误判成首次运行,把一整套设置重置成默认值。
func TestLoadSettingsEx_SavedFlag(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 全新库:还没有任何行。
	if _, saved, err := s.LoadSettingsEx(); err != nil {
		t.Fatal(err)
	} else if saved {
		t.Fatal("fresh database reported saved=true")
	}

	// LoadSettings 会插入占位行,但占位行不代表用户保存过设置。
	if _, err := s.LoadSettings(); err != nil {
		t.Fatal(err)
	}
	if _, saved, err := s.LoadSettingsEx(); err != nil {
		t.Fatal(err)
	} else if saved {
		t.Fatal("placeholder row must not count as saved")
	}

	// 真正保存过一次之后才算 saved。
	if err := s.SaveSettings(Settings{DefaultSaveDir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, saved, err := s.LoadSettingsEx(); err != nil {
		t.Fatal(err)
	} else if !saved {
		t.Fatal("after SaveSettings expected saved=true")
	}
}

// TestLoadSettingsEx_EmptyDirStillSaved 验证「保存过但目录为空」仍算
// saved:true —— 这正是旧版把整套设置重置掉的那个场景。
func TestLoadSettingsEx_EmptyDirStillSaved(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SaveSettings(Settings{DefaultThreads: 16}); err != nil {
		t.Fatal(err)
	}
	got, saved, err := s.LoadSettingsEx()
	if err != nil {
		t.Fatal(err)
	}
	if !saved {
		t.Fatal("saved=false for an explicitly saved row with an empty dir")
	}
	if got.DefaultThreads != 16 {
		t.Fatalf("DefaultThreads = %d, want 16", got.DefaultThreads)
	}
}

func TestMigrateAddsDownloadValidatorColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE tasks (
		id TEXT PRIMARY KEY,
		url TEXT NOT NULL,
		save_path TEXT NOT NULL,
		protocol TEXT NOT NULL,
		total_size INTEGER NOT NULL DEFAULT -1,
		downloaded INTEGER NOT NULL DEFAULT 0,
		support_range INTEGER NOT NULL DEFAULT 0,
		is_allocated INTEGER NOT NULL DEFAULT 0,
		chunk_count INTEGER NOT NULL DEFAULT 4,
		chunk_progress TEXT NOT NULL DEFAULT '[]',
		status TEXT NOT NULL DEFAULT 'Pending',
		auth_data TEXT NOT NULL DEFAULT '{}',
		error_message TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	INSERT INTO tasks (id,url,save_path,protocol,created_at,updated_at)
	VALUES ('legacy','https://example/file','/tmp/file','HTTP',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);`)
	if err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tk, err := s.GetTask("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if tk.ETag != "" || tk.LastModified != "" {
		t.Fatalf("legacy validators = %q / %q, want empty", tk.ETag, tk.LastModified)
	}
}
