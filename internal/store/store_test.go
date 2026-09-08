// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package store

import (
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
	if len(got.ChunkProgress) != 4 || got.ChunkProgress[0] != 128 {
		t.Fatalf("chunk progress mismatch: %v", got.ChunkProgress)
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
