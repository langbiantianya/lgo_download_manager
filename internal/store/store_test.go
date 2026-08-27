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
		Status:       StatusPending,
		AuthData:     `{"username":""}`,
	}
	if err := s.CreateTask(tk); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := s.UpdateTaskProgress("task-1", 512, []int64{128, 128, 128, 128}, StatusDownloading, ""); err != nil {
		t.Fatalf("UpdateTaskProgress: %v", err)
	}

	got, err := s.GetTask("task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.URL != tk.URL || got.Status != StatusDownloading || got.Downloaded != 512 {
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

	// ListTasks should return our one row.
	list, err := s.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("ListTasks len=%d", len(list))
	}

	// Delete + verify.
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
		ChunkProgress: []int64{}, Status: StatusPending,
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Reopen and read.
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
