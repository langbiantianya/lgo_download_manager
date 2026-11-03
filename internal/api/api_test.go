// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
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

func TestSettingsBTTrackersRoundTrip(t *testing.T) {
	in := Settings{BTTrackers: "udp://tracker.example:1337"}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Settings
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.BTTrackers != in.BTTrackers {
		t.Fatalf("BTTrackers 丢失: %q", out.BTTrackers)
	}
}

// TestSpecReturnsCopy 守住「Spec 不把全局状态交出去」。
//
// specs 是包级共享的 map。Spec 直接返回 map 里的指针时，调用方拿到的是
// 全局可变对象——改一下 NewIn，全进程的类型校验就都变了，而且极难排查。
// 这条用例模拟「调用方改了拿到的规格」并断言全局表没被影响。
func TestSpecReturnsCopy(t *testing.T) {
	s1 := Spec(MethodList)
	s2 := Spec(MethodList)
	if s1 == nil || s2 == nil {
		t.Fatal("list 应当有规格")
	}
	if s1 == s2 {
		t.Fatal("Spec 两次调用返回了同一个指针——改一个会影响另一个")
	}

	// 模拟调用方（或将来某个传输层）改了拿到的规格。
	orig := s1.NewIn
	s1.NewIn = func() any { return &Task{} }

	fresh := Spec(MethodList)
	if fresh.NewIn == nil {
		t.Fatal("改动后全局规格被清空了")
	}
	if _, ok := fresh.NewIn().(*ListParams); !ok {
		t.Fatalf("改动泄漏到了全局规格: NewIn 返回了 %T", fresh.NewIn())
	}
	s1.NewIn = orig
}

// TestSpecAndAllMethodsAreMutuallyConsistent 双向断言方法表与规格表一致。
//
// 原来只断言「AllMethods 里的每个方法都有规格」。反过来——specs 里多出来的
// 方法——没人查：它不会出现在 AllMethods 里，于是遍历方法表的契约测试与
// OpenAPI 一致性测试都看不见它，成了契约里的幽灵方法。
func TestSpecAndAllMethodsAreMutuallyConsistent(t *testing.T) {
	all := make(map[string]bool, len(AllMethods))
	for _, m := range AllMethods {
		if all[m] {
			t.Fatalf("AllMethods 里有重复项 %q", m)
		}
		all[m] = true
	}
	for m := range specs {
		if !all[m] {
			t.Fatalf("方法 %q 有规格但不在 AllMethods 里——契约测试与 OpenAPI 一致性测试遍历不到它", m)
		}
	}
	if len(specs) != len(AllMethods) {
		t.Fatalf("规格数 %d 与方法数 %d 不一致", len(specs), len(AllMethods))
	}
}

// TestTaskJSONRoundTripAllFields 守住 Task 的**每个字段**都真的往返。
//
// 原来的往返用例只断言了 4 个字段。剩下 20 个字段只要在 marshal 与
// unmarshal 之间不对称（比如一侧被漏掉），往返结果就会不同——这里用
// 整个结构体的 DeepEqual 一次覆盖所有字段。
//
// 注意它**抓不到 json tag 改名**：marshal 与 unmarshal 读的是同一份 tag，
// 改名后仍然对称。tag 的护栏在 internal/rpc/httpx 的
// TestOpenAPISchemaTypesMatchDTO——那里拿 Go struct 的 tag 去查 OpenAPI
// 规范里的属性名，tag 一改就红。
func TestTaskJSONRoundTripAllFields(t *testing.T) {
	// 每个字段都填非零值——空值会被 omitempty 跳过，字段就等于没被验证。
	in := Task{
		ID: "t1", URL: "http://x/a", SavePath: "/tmp/a", Protocol: "HTTP",
		TotalSize: 100, Downloaded: 40, SupportRange: true,
		ETag: "e", LastModified: "m", DigestAlgorithm: "sha256", Digest: "d",
		IsAllocated: true, ChunkCount: 4, MinChunkSize: 1024,
		ChunkProgress: []int64{1, 2}, ChunkRanges: []int64{0, 9, 10, 19},
		Status: StatusDownloading, AuthData: "{}", ErrorMessage: "e", ErrorKind: "k",
		BTFile:      "f",
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Task
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}

	// 逐字段比对。用 reflect.DeepEqual 覆盖整个结构体——新增字段时
	// 这条用例会直接提醒你把新字段也填上非零值，否则它不会被验证。
	//
	// 时间字段单独用 Equal 比：time.Time 的内部表示（wall/ext 编码、
	// location 指针）在 JSON 往返后与原值不同，DeepEqual 会误报。
	if !in.CreatedAt.Equal(out.CreatedAt) {
		t.Errorf("CreatedAt 往返后不一致: %v vs %v", in.CreatedAt, out.CreatedAt)
	}
	if !in.UpdatedAt.Equal(out.UpdatedAt) {
		t.Errorf("UpdatedAt 往返后不一致: %v vs %v", in.UpdatedAt, out.UpdatedAt)
	}
	if !in.CompletedAt.Equal(out.CompletedAt) {
		t.Errorf("CompletedAt 往返后不一致: %v vs %v", in.CompletedAt, out.CompletedAt)
	}
	in.CreatedAt, out.CreatedAt = time.Time{}, time.Time{}
	in.UpdatedAt, out.UpdatedAt = time.Time{}, time.Time{}
	in.CompletedAt, out.CompletedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("Task 往返后不一致:\n in = %+v\nout = %+v", in, out)
	}
}
