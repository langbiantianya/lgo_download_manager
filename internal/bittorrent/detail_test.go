// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package bittorrent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDetailReportsTorrentFacts 校验 BT 详情快照:infohash、分片大小与
// 数量、总体积与文件清单都要与种子本身一致——「任务详情」面板把这些
// 字段直接摆给用户,错了就是在误导人。
func TestDetailReportsTorrentFacts(t *testing.T) {
	requireLoopback(t)
	content := fakePayload(300 << 10)
	const pieceLength = 64 << 10
	mi, torrentBytes := makeSingleFileTorrent(t, "detail.bin", content, pieceLength)

	m, err := New(Config{MaxPeersPerTorrent: 4, EnableDHT: false, EnablePEX: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	h, err := m.AddTorrentBytes(context.Background(), torrentBytes, t.TempDir())
	if err != nil {
		t.Fatalf("AddTorrentBytes: %v", err)
	}
	defer h.Close()
	h.StartDownloading()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.WaitInfo(ctx); err != nil {
		t.Fatalf("WaitInfo: %v", err)
	}

	d := h.Detail()
	if d.Name != "detail.bin" {
		t.Errorf("Name = %q, want detail.bin", d.Name)
	}
	if want := mi.HashInfoBytes().HexString(); d.InfoHash != want {
		t.Errorf("InfoHash = %q, want %q", d.InfoHash, want)
	}
	if d.PieceLength != pieceLength {
		t.Errorf("PieceLength = %d, want %d", d.PieceLength, pieceLength)
	}
	// 300 KiB / 64 KiB = 4.69 → 5 个分片。
	if d.Pieces != 5 {
		t.Errorf("Pieces = %d, want 5", d.Pieces)
	}
	if d.Total != int64(len(content)) {
		t.Errorf("Total = %d, want %d", d.Total, len(content))
	}
	if len(d.Files) != 1 || d.Files[0].Path != "detail.bin" {
		t.Fatalf("Files = %+v, want a single entry for detail.bin", d.Files)
	}
	if d.Files[0].Length != int64(len(content)) {
		t.Errorf("file length = %d, want %d", d.Files[0].Length, len(content))
	}
	if d.PiecesDone > d.Pieces {
		t.Errorf("PiecesDone = %d, must not exceed Pieces = %d", d.PiecesDone, d.Pieces)
	}
	// 分片热力图的数据:分片不多时一块一桶,桶数与分片数一致。
	if len(d.PieceBuckets) != d.Pieces {
		t.Errorf("PieceBuckets = %d, want %d", len(d.PieceBuckets), d.Pieces)
	}
	// 盘上没有数据,桶值应该全是 0(「未开始」)。
	for i, v := range d.PieceBuckets {
		if v != 0 {
			t.Errorf("bucket %d = %d, want 0", i, v)
		}
	}
	if d.Uploaded < 0 {
		t.Errorf("Uploaded = %d, want >= 0", d.Uploaded)
	}
	// 这个种子没有 announce 地址,tracker 列表应为空而不是塞进别的东西。
	if len(d.Trackers) != 0 {
		t.Errorf("Trackers = %v, want empty", d.Trackers)
	}
}

// TestDetailPieceBucketsReflectCompleteData 校验分片桶在真实 BT 客户端
// 上反映的是分片完成度:数据已经在盘上、anacrolix 认下这些分片之后,
// 桶值应该全是 255(「已下完」),而不是一片 0——分片热力图画的就是这个。
func TestDetailPieceBucketsReflectCompleteData(t *testing.T) {
	requireLoopback(t)
	content := fakePayload(300 << 10)
	const pieceLength = 64 << 10
	_, torrentBytes := makeSingleFileTorrent(t, "verify.bin", content, pieceLength)

	// 数据先放好:anacrolix 的文件存储按「正式文件在不在、长度对不对」
	// 认下已完成的分片(与 lgdm 断点续传的判定口径一致)。
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "verify.bin"), content, 0o644); err != nil {
		t.Fatalf("write data: %v", err)
	}

	m, err := New(Config{MaxPeersPerTorrent: 4, EnableDHT: false, EnablePEX: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	h, err := m.AddTorrentBytes(context.Background(), torrentBytes, dir)
	if err != nil {
		t.Fatalf("AddTorrentBytes: %v", err)
	}
	defer h.Close()
	h.StartDownloading()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.WaitInfo(ctx); err != nil {
		t.Fatalf("WaitInfo: %v", err)
	}

	d := h.Detail()
	if len(d.PieceBuckets) != d.Pieces {
		t.Fatalf("PieceBuckets = %d, want %d", len(d.PieceBuckets), d.Pieces)
	}
	for i, v := range d.PieceBuckets {
		if v != 255 {
			t.Errorf("bucket %d = %d, want 255 (the piece is already on disk)", i, v)
		}
	}
}

// TestDetailAfterCloseIsEmpty 校验句柄关闭后详情采样不会崩:关闭意味着
// 种子已从客户端摘除,再读统计就可能碰到上游的失效状态。详情面板的
// 轮询与做种监控的收尾是并发的,这条守住「关了也能查」。
func TestDetailAfterCloseIsEmpty(t *testing.T) {
	requireLoopback(t)
	content := fakePayload(64 << 10)
	_, torrentBytes := makeSingleFileTorrent(t, "closed.bin", content, 32<<10)

	m, err := New(Config{MaxPeersPerTorrent: 4, EnableDHT: false, EnablePEX: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	h, err := m.AddTorrentBytes(context.Background(), torrentBytes, t.TempDir())
	if err != nil {
		t.Fatalf("AddTorrentBytes: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.WaitInfo(ctx); err != nil {
		t.Fatalf("WaitInfo: %v", err)
	}
	h.Close()

	d := h.Detail()
	if d.InfoHash != "" || d.Total != 0 || len(d.Files) != 0 {
		t.Errorf("Detail after close = %+v, want an empty snapshot", d)
	}
	if s := h.Snapshot(); s.Total != 0 || len(s.Files) != 0 {
		t.Errorf("Snapshot after close = %+v, want an empty snapshot", s)
	}
}

// TestMeasureUploadSpeedUsesOwnClock 是回归用例:上传与下载方向必须各用
// 各的时间基准。共用一个基准时,Detail() 里先跑的 measureSpeed 会把
// 时间基准推到「刚刚」,后跑的 measureUploadSpeed 拿到的时间差≈0,
// 上传速度就永远是 0——做种时面板上的上传速率一动不动。
func TestMeasureUploadSpeedUsesOwnClock(t *testing.T) {
	h := &Handle{
		lastSample:   time.Now().Add(-2 * time.Second),
		lastUpSample: time.Now().Add(-2 * time.Second),
	}

	// 下载方向先采样一次(它会重置自己的基准)。
	h.measureSpeed(0)

	// 上传方向的时间基准不应被下载方向动过。
	if !h.lastUpSample.Before(time.Now().Add(-1500 * time.Millisecond)) {
		t.Error("the upload clock was reset by the download-side sampling")
	}

	// 上传方向独立计时:2 秒内多传 2000 字节 → 1000 B/s。
	h.lastUpBytes = 1000
	h.lastUpSample = time.Now().Add(-2 * time.Second)
	if got := h.measureUploadSpeed(3000); got != 1000 {
		t.Errorf("upload speed = %v, want 1000", got)
	}
}

// TestMeasureSpeedKeepsBaselineWhenSampledTooOften 校验采样过密时不重置
// 基准:调度器与详情面板都会读速度,各自重置基准会把一次字节增量分走,
// 两边算出来的速度都偏低。
func TestMeasureSpeedKeepsBaselineWhenSampledTooOften(t *testing.T) {
	h := &Handle{lastSample: time.Now().Add(-2 * time.Second)}

	// 第一次:间隔够长,正常算出速度。
	if got := h.measureSpeed(2000); got != 1000 {
		t.Fatalf("first sample = %v, want 1000", got)
	}
	// 紧接着再采一次:间隔太短,应沿用上次的结果而不是把增量分掉。
	if got := h.measureSpeed(2500); got != 1000 {
		t.Errorf("second sample = %v, want the previous 1000", got)
	}
}
