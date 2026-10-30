// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package api

import (
	"encoding/json"
	"testing"
)

func TestSpecCoversAllMethods(t *testing.T) {
	for _, m := range AllMethods {
		s := Spec(m)
		if s == nil {
			t.Fatalf("方法 %q 缺少规格", m)
		}
		if s.NewIn == nil || s.NewOut == nil {
			t.Fatalf("方法 %q 的规格不完整", m)
		}
	}
	if Spec("nope") != nil {
		t.Fatal("未知方法应返回 nil")
	}
}

func TestTaskJSONRoundTrip(t *testing.T) {
	in := Task{
		ID: "t1", URL: "http://x/a", SavePath: "/tmp/a", Protocol: "HTTP",
		TotalSize: 100, Downloaded: 40, SupportRange: true,
		ETag: "e", LastModified: "m", DigestAlgorithm: "sha256", Digest: "d",
		IsAllocated: true, ChunkCount: 4, MinChunkSize: 1024,
		ChunkProgress: []int64{1, 2}, ChunkRanges: []int64{0, 9, 10, 19},
		Status: StatusDownloading, AuthData: "{}", ErrorMessage: "e", ErrorKind: "k",
		BTFile: "f",
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Task
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if in.ID != out.ID || in.Status != out.Status || in.TotalSize != out.TotalSize {
		t.Fatalf("往返后字段不一致: %+v", out)
	}
	if len(out.ChunkProgress) != 2 || out.ChunkProgress[1] != 2 {
		t.Fatalf("切片字段丢失: %+v", out.ChunkProgress)
	}
}

func TestStatusValuesMatchStore(t *testing.T) {
	// 与 internal/store 的取值保持一致（首字母大写）。
	want := map[Status]string{
		StatusPending:     "Pending",
		StatusDownloading: "Downloading",
		StatusPaused:      "Paused",
		StatusCompleted:   "Completed",
		StatusFailed:      "Failed",
		StatusFileLost:    "FileLost",
	}
	for st, v := range want {
		if string(st) != v {
			t.Fatalf("状态 %q 的取值应为 %q", st, v)
		}
	}
}
