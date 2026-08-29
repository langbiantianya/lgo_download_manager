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
	list, err := s.ListTasks(FilterAll)
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
	all, err := s.ListTasks(FilterAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("FilterAll len=%d want 3", len(all))
	}

	// FilterDownloading 只返回 Downloading 状态的 1 行。
	dl, err := s.ListTasks(FilterDownloading)
	if err != nil {
		t.Fatal(err)
	}
	if len(dl) != 1 || dl[0].ID != "b" {
		t.Fatalf("FilterDownloading=%v want only task b", dl)
	}

	// FilterCompleted 只返回 1 行。
	done, err := s.ListTasks(FilterCompleted)
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
