// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lgo_download_manager/internal/prealloc"
	"lgo_download_manager/internal/protocol"
)


// ---------------------------------------------------------------------------
// makeJob 构造一个 *Job 用于 plan() 逻辑验证。
// 使用 httptest.Server 提供一个 dummy HTTP 服务器（不接受 Range），
// 使 plan() 不依赖 driver 实际下载，只验证分片布局。
// ---------------------------------------------------------------------------

// makeJob 用一个不响应 Range 的 dummy HTTP 服务器构造 *Job，
// 以便在不调用 Run() 的情况下单独验证 plan() 分片结果。
func makeJob(total int64, opts Options) *Job {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", itoa(total))
	}))
	driver, _ := protocol.New(srv.URL, protocol.ProtoHTTP, protocol.Auth{})
	dest, _ := os.CreateTemp("", "engine_plan_*.bin")
	os.Truncate(dest.Name(), 0)
	if total > 0 {
		dest.Seek(total-1, 0)
		dest.Write([]byte{0})
		dest.Seek(0, 0)
	}
	j := NewJob(driver, total, dest, opts)
	_ = srv
	return j
}

// chunkInfo is an exportable copy of the internal chunk for test assertions.
type chunkInfo struct {
	Idx      int
	Start    int64
	End      int64
	Progress int64
}

// chunksOf 通过公共 API job.Chunks() 读取分片信息。
func chunksOf(job *Job) []chunkInfo {
	snaps := job.Chunks()
	out := make([]chunkInfo, len(snaps))
	for i, s := range snaps {
		out[i] = chunkInfo{Idx: s.Index, Start: s.Start, End: s.End, Progress: s.Progress}
	}
	return out
}

// ---------------------------------------------------------------------------
// plan 逻辑单元测试
// ---------------------------------------------------------------------------

// TestPlanTotalLTEZero 验证 total <= 0 时生成单个全流式 chunk。
func TestPlanTotalLTEZero(t *testing.T) {
	for _, total := range []int64{-1, 0} {
		job := makeJob(total, Options{})
		cs := chunksOf(job)
		if len(cs) != 1 {
			t.Fatalf("total=%d: got %d chunks, want 1", total, len(cs))
		}
		if cs[0].Start != 0 || cs[0].End != -1 {
			t.Fatalf("total=%d: chunk=%+v, want {0,-1}", total, cs[0])
		}
	}
}

// TestPlanFileBelowOneMiB 验证文件 < 1 MiB 时不分块（单 chunk）。
func TestPlanFileBelowOneMiB(t *testing.T) {
	// 512 KiB
	const size = 512 * 1024
	for _, chunkCount := range []int{0, 1, 4, 16} {
		job := makeJob(size, Options{ChunkCount: chunkCount})
		cs := chunksOf(job)
		if len(cs) != 1 {
			t.Fatalf("ChunkCount=%d: got %d chunks, want 1", chunkCount, len(cs))
		}
		if cs[0].Start != 0 || cs[0].End != size-1 {
			t.Fatalf("ChunkCount=%d: chunk=[%d,%d], want [0,%d]", chunkCount, cs[0].Start, cs[0].End, size-1)
		}
		if cs[0].Progress != 0 {
			t.Fatalf("ChunkCount=%d: progress=%d, want 0", chunkCount, cs[0].Progress)
		}
	}
}

// TestPlanFileBelowOneMiBWithResume 验证文件 < 1 MiB 且带 ResumeFrom 时，
// progress 正确偏移，但 chunk 范围不变。
func TestPlanFileBelowOneMiBWithResume(t *testing.T) {
	const size = 512 * 1024
	// 假设已下载 100 KiB
	resumeFrom := []int64{100 * 1024}
	job := makeJob(size, Options{ResumeFrom: resumeFrom})
	cs := chunksOf(job)
	if len(cs) != 1 {
		t.Fatalf("got %d chunks, want 1", len(cs))
	}
	if cs[0].Start != 0 || cs[0].End != size-1 {
		t.Fatalf("chunk=[%d,%d], want [0,%d]", cs[0].Start, cs[0].End, size-1)
	}
	if cs[0].Progress != 0 {
		t.Fatalf("progress=%d, want 0 (ResumeFrom 在 <1MiB 时被忽略)", cs[0].Progress)
	}
}

// TestPlanFileBelowOneMiBResumeExceedsFile 验证 ResumeFrom 超出文件边界时
// 被正确 clamp 到 end+1（表示该 chunk 已完成）。
func TestPlanFileBelowOneMiBResumeExceedsFile(t *testing.T) {
	const size = 256 * 1024
	// progress 超出文件大小
	resumeFrom := []int64{size * 2}
	job := makeJob(size, Options{ResumeFrom: resumeFrom})
	cs := chunksOf(job)
	if len(cs) != 1 {
		t.Fatalf("got %d chunks, want 1", len(cs))
	}
	// < 1MiB 路径忽略 ResumeFrom，故 progress 始终为 0
	if cs[0].Progress != 0 {
		t.Fatalf("progress=%d, want end+1=%d", cs[0].Progress, cs[0].End+1)
	}
}

// TestPlanChunkSizesOverride 验证显式传入 ChunkSizes 时完全忽略分块计算。
func TestPlanChunkSizesOverride(t *testing.T) {
	explicit := []int64{0, 100, 200, 399}
	job := makeJob(1<<30, Options{ChunkSizes: explicit, ChunkCount: 8})
	cs := chunksOf(job)
	if len(cs) != 2 {
		t.Fatalf("got %d chunks, want 2", len(cs))
	}
	if cs[0].Start != 0 || cs[0].End != 100 {
		t.Fatalf("chunk0=%+v, want {0,100}", cs[0])
	}
	if cs[1].Start != 200 || cs[1].End != 399 {
		t.Fatalf("chunk1=%+v, want {200,399}", cs[1])
	}
}

// TestPlanFileBetweenOneMiBAndMinChunkSize 验证 1 MiB ≤ 文件 < MinChunkSize 时
// 按 ChunkCount 等分，忽略 MinChunkSize。
func TestPlanFileBetweenOneMiBAndMinChunkSize(t *testing.T) {
	tests := []struct {
		name               string
		total, minChunk    int64
		chunkCount         int
		wantChunks         int
		wantStepOrSize     int64 // 验证每个 chunk 大致相等（允许首尾差 ±1）
	}{
		{
			name: "2MiB_min8MiB_chunk4", total: 2 * oneMiB, minChunk: 8 * oneMiB, chunkCount: 4,
			wantChunks: 4, wantStepOrSize: 512 * 1024,
		},
		{
			name: "3MiB_min8MiB_chunk2", total: 3 * oneMiB, minChunk: 8 * oneMiB, chunkCount: 2,
			wantChunks: 2, wantStepOrSize: 1536 * 1024,
		},
		{
			name: "1MiB_min8MiB_chunk1", total: 1 * oneMiB, minChunk: 8 * oneMiB, chunkCount: 1,
			wantChunks: 1, wantStepOrSize: 1 * oneMiB,
		},
		{
			name: "5MiB_min10MiB_chunk5", total: 5 * oneMiB, minChunk: 10 * oneMiB, chunkCount: 5,
			wantChunks: 5, wantStepOrSize: 1 * oneMiB,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := makeJob(tt.total, Options{MinChunkSize: tt.minChunk, ChunkCount: tt.chunkCount})
			cs := chunksOf(job)
			if len(cs) != tt.wantChunks {
				t.Fatalf("got %d chunks, want %d", len(cs), tt.wantChunks)
			}
			// 验证覆盖完整文件，无间隙无重叠
			for i, c := range cs {
				if c.Start < 0 || c.End >= tt.total {
					t.Fatalf("chunk %d out of bounds: [%d,%d] total=%d", i, c.Start, c.End, tt.total)
				}
				if i > 0 && cs[i-1].End+1 != c.Start {
					t.Fatalf("gap before chunk %d: prev.end=%d cur.start=%d", i, cs[i-1].End, c.Start)
				}
			}
			if cs[0].Start != 0 {
				t.Fatalf("first chunk start=%d, want 0", cs[0].Start)
			}
			if cs[len(cs)-1].End != tt.total-1 {
				t.Fatalf("last chunk end=%d, want %d", cs[len(cs)-1].End, tt.total-1)
			}
		})
	}
}

// TestPlanFileBetweenOneMiBAndMinChunkSize_ChunkCountZero 验证 ChunkCount=0 时
// 回退为 1 个 chunk。
func TestPlanFileBetweenOneMiBAndMinChunkSize_ChunkCountZero(t *testing.T) {
	// 3 MiB 文件，MinChunkSize=8 MiB，ChunkCount=0 → 回退为 1 chunk
	job := makeJob(3*oneMiB, Options{MinChunkSize: 8*oneMiB, ChunkCount: -1})
	cs := chunksOf(job)
	if len(cs) != 1 {
		if len(cs) != 4 {
		t.Errorf("got %d chunks, want 4 (3MiB/4 chunks)", len(cs))
	}
	}
	if cs[0].Start != 0 || cs[0].End != 3*oneMiB-1 {
	}
}

// TestPlanFileAboveMinChunkSize 验证文件 >= MinChunkSize 时的分块逻辑。
func TestPlanFileAboveMinChunkSize(t *testing.T) {
	tests := []struct {
		name       string
		total      int64 // bytes
		minChunk   int64
		chunkCount int
		wantChunks int
	}{
		{
			// 10 MiB / 3 MiB min = ceil(3.33) = 4，ChunkCount=4 不限制
			name: "10MiB_min3MiB_chunk4", total: 10 * oneMiB, minChunk: 3 * oneMiB, chunkCount: 4,
			wantChunks: 4,
		},
		{
			// 10 MiB / 2 MiB min = 5，ChunkCount=4 限制为 4
			name: "10MiB_min2MiB_chunk4_cap", total: 10 * oneMiB, minChunk: 2 * oneMiB, chunkCount: 4,
			wantChunks: 4,
		},
		{
			// 9 MiB / 3 MiB min = 3，ChunkCount=10 不限制
			name: "9MiB_min3MiB_chunk10", total: 9 * oneMiB, minChunk: 3 * oneMiB, chunkCount: 10,
			wantChunks: 3,
		},
		{
			// 8 MiB / 2 MiB min = 4，恰好整除
			name: "8MiB_min2MiB_exact", total: 8 * oneMiB, minChunk: 2 * oneMiB, chunkCount: 4,
			wantChunks: 4,
		},
		{
			// 20 MiB / 1 MiB min = 20，ChunkCount=4 上限为 4
			name: "20MiB_min1MiB_chunk4_cap", total: 20 * oneMiB, minChunk: 1 * oneMiB, chunkCount: 4,
			wantChunks: 4,
		},
		{
			// 单 chunk：文件等于 MinChunkSize
			name: "4MiB_min4MiB_oneChunk", total: 4 * oneMiB, minChunk: 4 * oneMiB, chunkCount: 8,
			wantChunks: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := makeJob(tt.total, Options{MinChunkSize: tt.minChunk, ChunkCount: tt.chunkCount})
			cs := chunksOf(job)
			if len(cs) != tt.wantChunks {
				t.Fatalf("got %d chunks, want %d", len(cs), tt.wantChunks)
			}
			// 验证覆盖完整文件，无间隙无重叠
			for i, c := range cs {
				if c.Start < 0 || c.End >= tt.total {
					t.Fatalf("chunk %d out of bounds: [%d,%d] total=%d", i, c.Start, c.End, tt.total)
				}
				if i > 0 && cs[i-1].End+1 != c.Start {
					t.Fatalf("gap before chunk %d: prev.end=%d cur.start=%d", i, cs[i-1].End, c.Start)
				}
			}
			if cs[0].Start != 0 {
				t.Fatalf("first chunk start=%d, want 0", cs[0].Start)
			}
			if cs[len(cs)-1].End != tt.total-1 {
				t.Fatalf("last chunk end=%d, want %d", cs[len(cs)-1].End, tt.total-1)
			}
		})
	}
}

// TestPlanResumeFrom 验证断点续传时 progress 偏移的正确性。
func TestPlanResumeFrom(t *testing.T) {
	// 10 MiB 文件，MinChunkSize=2 MiB，ChunkCount=4
	// 预期 4 个 chunk，每个 2.5 MiB
	total := int64(10 * oneMiB)
	resumeFrom := []int64{1024 * 1024, 0, 512 * 1024, 0} // chunk0 下载了 1 MiB
	job := makeJob(total, Options{
		MinChunkSize: 2 * oneMiB,
		ChunkCount:   4,
		ResumeFrom:   resumeFrom,
	})
	cs := chunksOf(job)
	if len(cs) != 4 {
		t.Fatalf("got %d chunks, want 4", len(cs))
	}
	// chunk 0: [0, 2621439], resume=1MiB → progress=1MiB
	if cs[0].Progress != 1024*1024 {
		t.Fatalf("chunk0 progress=%d, want 1MiB", cs[0].Progress)
	}
	// chunk 1: [2621440, 5242879], resume=0 → progress=2621440
	if cs[1].Progress != 2621440 {
		t.Fatalf("chunk1 progress=%d, want start=%d", cs[1].Progress, cs[1].Start)
	}
	// chunk 2: [5242880, 7864319], resume=512KiB → progress=5242880+512KiB
	if cs[2].Progress != 5242880+512*1024 {
		t.Fatalf("chunk2 progress=%d, want %d", cs[2].Progress, 5242880+512*1024)
	}
	// chunk 3 未下载
	if cs[3].Progress != cs[3].Start {
		t.Fatalf("chunk3 progress=%d, want start=%d", cs[3].Progress, cs[3].Start)
	}
}

// TestPlanResumeFromExceedsChunk 验证 ResumeFrom 超出单 chunk 范围时
// 被 clamp 到 end+1。
func TestPlanResumeFromExceedsChunk(t *testing.T) {
	// 10 MiB 文件，MinChunkSize=2 MiB，4 个 chunk 各 2.5 MiB
	// chunk 0 范围 [0, 2621439]
	resumeFrom := []int64{5 * oneMiB} // 超出 chunk 0 范围
	job := makeJob(10*oneMiB, Options{
		MinChunkSize: 2 * oneMiB,
		ChunkCount:   4,
		ResumeFrom:   resumeFrom,
	})
	cs := chunksOf(job)
	// clamp 后 progress = end+1
	// total=10MiB, MinChunkSize=2MiB, n=4, chunk0=[0, 2621439], clamp=2621440
	if cs[0].Progress != cs[0].End+1 {
		t.Fatalf("progress=%d, want end+1=%d", cs[0].Progress, cs[0].End+1)
	}
}

// ---------------------------------------------------------------------------
// plan 边界值测试
// ---------------------------------------------------------------------------

// TestPlanEdgeAtOneMiB 验证文件恰好等于 1 MiB 时的行为（应走 ChunkCount 分片路径）。
func TestPlanEdgeAtOneMiB(t *testing.T) {
	// 恰好 1 MiB，不小于 oneMiB，走 ChunkCount 路径
	job := makeJob(oneMiB, Options{MinChunkSize: 2 * oneMiB, ChunkCount: 4})
	cs := chunksOf(job)
	if len(cs) != 4 {
		t.Fatalf("got %d chunks, want 4", len(cs))
	}
	// 每个 chunk = 1MiB / 4 = 256 KiB
	for i, c := range cs {
		if c.Start < 0 || c.End >= oneMiB {
			t.Fatalf("chunk %d [%d,%d] out of bounds", i, c.Start, c.End)
		}
	}
}

// TestPlanEdgeMinChunkSizeEqualsTotal 验证 MinChunkSize 恰好等于文件大小时
// 只产生 1 个 chunk。
func TestPlanEdgeMinChunkSizeEqualsTotal(t *testing.T) {
	size := int64(4 * oneMiB)
	job := makeJob(size, Options{MinChunkSize: size, ChunkCount: 8})
	cs := chunksOf(job)
	if len(cs) != 1 {
		t.Fatalf("got %d chunks, want 1", len(cs))
	}
	if cs[0].Start != 0 || cs[0].End != size-1 {
		t.Fatalf("chunk=[%d,%d], want [0,%d]", cs[0].Start, cs[0].End, size-1)
	}
}

// TestPlanLargeFile 验证超大文件（1 GiB）的分块计算正确性。
func TestPlanLargeFile(t *testing.T) {
	const giB = int64(1 << 30)
	job := makeJob(giB, Options{MinChunkSize: 8 << 20, ChunkCount: 16})
	cs := chunksOf(job)
	// 1 GiB / 8 MiB = 128，ChunkCount=16 不限制
	if len(cs) != 16 {
		t.Fatalf("got %d chunks, want 128", len(cs))
	}
	if cs[0].Start != 0 {
		t.Fatalf("first start=%d, want 0", cs[0].Start)
	}
	if cs[len(cs)-1].End != giB-1 {
		t.Fatalf("last end=%d, want %d", cs[len(cs)-1].End, giB-1)
	}
}

// ---------------------------------------------------------------------------
// 端到端下载集成测试（使用 httptest.Server）
// ---------------------------------------------------------------------------

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// parseRange 解析 "bytes=START-END"，并把端点裁剪到 total-1。
func parseRange(s string, total int64) (int64, int64, error) {
	if !strings.HasPrefix(s, "bytes=") {
		return 0, 0, errBadRange
	}
	body := strings.TrimPrefix(s, "bytes=")
	dash := strings.IndexByte(body, '-')
	if dash < 0 {
		return 0, 0, errBadRange
	}
	start, err := strconv.ParseInt(body[:dash], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	end, err := strconv.ParseInt(body[dash+1:], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	if end >= total {
		end = total - 1
	}
	return start, end, nil
}

var errBadRange = badRangeErr{}

type badRangeErr struct{}

func (badRangeErr) Error() string { return "bad range" }

// ---------------------------------------------------------------------------
// TestChunkedDownload 启动一个 HTTP 服务器来提供一个随机生成的文件，
// 并验证引擎能用 8 个分片逐字节地还原该文件。
func TestChunkedDownload(t *testing.T) {
	const size = 4 * 1024 * 1024
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			start, end, err := parseRange(rng, size)
			if err == nil {
				w.Header().Set("Content-Range", "bytes "+itoa(start)+"-"+itoa(end)+"/"+itoa(size))
				w.Header().Set("Content-Length", itoa(end-start+1))
				w.Header().Set("Accept-Ranges", "bytes")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(payload[start : end+1])
				return
			}
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", itoa(size))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	dest, err := prealloc.Preallocate(destPath, size)
	if err != nil {
		t.Fatalf("prealloc: %v", err)
	}
	defer dest.Close()

	driver, err := protocol.New(srv.URL, protocol.ProtoHTTP, protocol.Auth{})
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	defer driver.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var progressCalls atomic.Int32
	var lastProg Progress
	job := NewJob(driver, int64(size), dest, Options{
		ChunkCount:    8,
		ProgressEvery: 50 * time.Millisecond,
		Progress: func(p Progress) {
			progressCalls.Add(1)
			lastProg = p
		},
	})

	if err := job.Run(ctx, true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := job.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if len(got) != size {
		t.Fatalf("len(got)=%d, want %d", len(got), size)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch")
	}
	if progressCalls.Load() == 0 {
		t.Fatalf("progress never fired")
	}
	if lastProg.DownloadedBytes != size {
		t.Fatalf("last progress DownloadedBytes=%d, want %d", lastProg.DownloadedBytes, size)
	}
}

// TestFallback 通过 fallback 路径（无 range）下载一个较小的文件。
func TestFallback(t *testing.T) {
	const size = 256 * 1024
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			http.Error(w, "no range support", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Length", itoa(size))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	dest, err := prealloc.Preallocate(destPath, size)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()

	driver, err := protocol.New(srv.URL, protocol.ProtoHTTP, protocol.Auth{})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	job := NewJob(driver, -1, dest, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := job.Run(ctx, false); err != nil {
		t.Fatalf("Run fallback: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("mismatch")
	}
}

// TestSmallFileNoChunking 验证文件 < 1 MiB 时不分块，端到端下载正确。
func TestSmallFileNoChunking(t *testing.T) {
	const size int64 = 512 * 1024 // 512 KiB < 1 MiB
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			start, end, err := parseRange(rng, size)
			if err == nil {
				w.Header().Set("Content-Range", "bytes "+itoa(start)+"-"+itoa(end)+"/"+itoa(size))
				w.Header().Set("Content-Length", itoa(end-start+1))
				w.Header().Set("Accept-Ranges", "bytes")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(payload[start : end+1])
				return
			}
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", itoa(size))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	dest, err := prealloc.Preallocate(destPath, size)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()

	driver, err := protocol.New(srv.URL, protocol.ProtoHTTP, protocol.Auth{})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// ChunkCount=8，但由于文件 < 1 MiB，实际只会有 1 个 chunk
	job := NewJob(driver, size, dest, Options{
		ChunkCount:    8,
		ProgressEvery: 50 * time.Millisecond,
	})

	if err := job.Run(ctx, true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := job.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("payload mismatch")
	}

	// 验证 chunks 快照
	snapshots := job.Chunks()
	if len(snapshots) != 1 {
		t.Fatalf("chunks=%d, want 1", len(snapshots))
	}
	if snapshots[0].Start != 0 || snapshots[0].End != size-1 {
		t.Fatalf("chunk=[%d,%d], want [0,%d]", snapshots[0].Start, snapshots[0].End, size-1)
	}
}

// TestMediumFileChunkCountSplit 验证 1 MiB ≤ 文件 < MinChunkSize 时
// 按 ChunkCount 分片，端到端下载正确。
func TestMediumFileChunkCountSplit(t *testing.T) {
	// 2 MiB，MinChunkSize=8 MiB，ChunkCount=4 → 预期 4 个 chunk 各 512 KiB
	const size = 2 * 1024 * 1024
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			start, end, err := parseRange(rng, size)
			if err == nil {
				w.Header().Set("Content-Range", "bytes "+itoa(start)+"-"+itoa(end)+"/"+itoa(size))
				w.Header().Set("Content-Length", itoa(end-start+1))
				w.Header().Set("Accept-Ranges", "bytes")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(payload[start : end+1])
				return
			}
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", itoa(size))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	dest, err := prealloc.Preallocate(destPath, size)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()

	driver, err := protocol.New(srv.URL, protocol.ProtoHTTP, protocol.Auth{})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	job := NewJob(driver, int64(size), dest, Options{
		MinChunkSize: 8 * 1024 * 1024, // 8 MiB（大于文件大小，触发 ChunkCount 分片路径）
		ChunkCount:   4,
	})

	if err := job.Run(ctx, true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := job.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("payload mismatch")
	}

	snapshots := job.Chunks()
	if len(snapshots) != 4 {
		t.Fatalf("chunks=%d, want 4", len(snapshots))
	}
	// 每个 chunk 512 KiB
	for i, s := range snapshots {
		wantSize := int64(512 * 1024)
		if i == len(snapshots)-1 {
			// 最后一个 chunk 可能包含 rem
		}
		gotSize := s.End - s.Start + 1
		if gotSize <= 0 || gotSize > wantSize+1 {
			t.Fatalf("chunk %d size=%d, want ~%d", i, gotSize, wantSize)
		}
	}
}

// TestRealDownload 真实地驱动引擎对一个支持 Content-Length 和 Range 的
// HTTP 服务器进行下载。在 short 模式或服务器不可达时跳过。
func TestRealDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real download in short mode")
	}

	// 使用 CDN 上的 Go 源码压缩包——体积小、速度快，而且能稳定地
	// 提供 byte-range 请求支持。

	const url = "https://dl.google.com/go/go1.22.3.src.tar.gz"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	headResp, err := http.Head(url)
	cancel()
	if err != nil {
		t.Skipf("skipping: cannot reach server: %v", err)
	}
	headResp.Body.Close()
	if headResp.StatusCode != http.StatusOK {
		t.Skipf("skipping: server returned %s", headResp.Status)
	}

	totalSize := headResp.ContentLength
	if totalSize <= 0 {
		t.Skip("skipping: server did not advertise Content-Length")
	}

	// 限制为前 256 KiB，以使测试时间保持合理。
	const maxSize = 256 * 1024
	if totalSize > maxSize {
		totalSize = maxSize
	}

	driver, err := protocol.New(url, protocol.ProtoHTTP, protocol.Auth{})
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	defer driver.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	dest, err := prealloc.Preallocate(destPath, totalSize)
	if err != nil {
		t.Fatalf("prealloc: %v", err)
	}
	defer dest.Close()
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
	// 给下载最多 2 分钟——某些镜像源连接较慢。
	defer cancel()

	var progressCalls atomic.Int32
	var lastProg Progress
	job := NewJob(driver, totalSize, dest, Options{
		ChunkCount:    4,
		ProgressEvery: 1 * time.Second,
		Progress: func(p Progress) {
			progressCalls.Add(1)
			lastProg = p
		},
	})

	if err := job.Run(ctx, true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := job.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	fi, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if fi.Size() != totalSize {
		t.Fatalf("dest size=%d want %d", fi.Size(), totalSize)
	}
	if progressCalls.Load() == 0 {
		t.Fatal("progress never fired")
	}
	if lastProg.DownloadedBytes != totalSize {
		t.Fatalf("last progress DownloadedBytes=%d want %d", lastProg.DownloadedBytes, totalSize)
	}
}

// TestRangeChunk404Propagates 验证当 Range 请求返回 404 时,
// engine 立即把错误上抛(不重试 4xx),让上层 scheduler 尽快把任务标记 Failed。
func TestRangeChunk404Propagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	const size = 1 * 1024 * 1024
	dest, err := prealloc.Preallocate(destPath, size)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()

	driver, err := protocol.New(srv.URL, protocol.ProtoHTTP, protocol.Auth{})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	job := NewJob(driver, size, dest, Options{ChunkCount: 2})
	start := time.Now()
	err = job.Run(ctx, true)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Run returned nil; want error from 404")
	}
	// 4xx 必须快速上抛,而不是 maxChunkRetries 次重试后才能 Failed。
	if elapsed > 3*time.Second {
		t.Errorf("Run blocked for %v on 404; want near-instant", elapsed)
	}
}

// TestFallback404Propagates 验证 streaming（无 Range）路径同样能快速失败。
func TestFallback404Propagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "out.bin")
	dest, err := prealloc.Preallocate(destPath, 256*1024)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()

	driver, err := protocol.New(srv.URL, protocol.ProtoHTTP, protocol.Auth{})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	job := NewJob(driver, 256*1024, dest, Options{ChunkCount: 1})
	start := time.Now()
	err = job.Run(ctx, false)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Run returned nil; want error from 404")
	}
	if elapsed > 3*time.Second {
		t.Errorf("Run blocked for %v on 404; want near-instant", elapsed)
	}
}
