// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package bittorrent

import "testing"

// TestAggregatePiecesBucketCount 校验桶数:分片不多时一块一桶,超过上限
// 就聚合到上限(热力图最多画那么多块,再多也看不出来)。
func TestAggregatePiecesBucketCount(t *testing.T) {
	complete := func(int) int64 { return 0 }
	if got := aggregatePieces(1024, 10*1024, 10, complete); len(got) != 10 {
		t.Errorf("buckets = %d, want 10", len(got))
	}
	if got := aggregatePieces(1024, 2000*1024, 2000, complete); len(got) != maxPieceBuckets {
		t.Errorf("buckets = %d, want %d", len(got), maxPieceBuckets)
	}
	// 边界:没有分片就没有桶。
	if got := aggregatePieces(1024, 0, 0, complete); got != nil {
		t.Errorf("buckets = %v, want nil", got)
	}
}

// TestAggregatePiecesCompletion 校验桶值就是「桶内已下载字节的占比」:
// 全满 255,全空 0,一半 127 左右。
func TestAggregatePiecesCompletion(t *testing.T) {
	cases := []struct {
		name    string
		missing func(int) int64
		want    uint8
	}{
		{"complete", func(int) int64 { return 0 }, 255},
		{"empty", func(int) int64 { return 1024 }, 0},
		{"half", func(int) int64 { return 512 }, 127},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := aggregatePieces(1024, 4*1024, 4, tc.missing)
			if len(got) != 4 {
				t.Fatalf("buckets = %d, want 4", len(got))
			}
			for i, v := range got {
				if v != tc.want {
					t.Errorf("bucket %d = %d, want %d", i, v, tc.want)
				}
			}
		})
	}
}

// TestAggregatePiecesWeightsByBytes 校验桶值按字节加权,不是按分片个数
// 平均:一个桶里一个整片 + 一个四分之三片,应该落在 7/8 附近——按分片
// 个数平均会算出 3/4。
func TestAggregatePiecesWeightsByBytes(t *testing.T) {
	// 2048 个分片 → 1024 个桶,每桶两个分片。
	const numPieces = 2048
	// 偶数片下满,奇数片还差 256 字节(共 1024)。
	missing := func(piece int) int64 {
		if piece%2 == 0 {
			return 0
		}
		return 256
	}
	got := aggregatePieces(1024, numPieces*1024, numPieces, missing)
	if len(got) != maxPieceBuckets {
		t.Fatalf("buckets = %d, want %d", len(got), maxPieceBuckets)
	}
	// (1024-0) + (1024-256) = 1792 / 2048 = 87.5% → 223。
	for i, v := range got {
		if v != 223 {
			t.Fatalf("bucket %d = %d, want 223", i, v)
		}
	}
}

// TestAggregatePiecesShortLastPiece 校验末片按实际长度算:末片通常比
// 其它片短,拿整片长度去比会把「末片还没下」算成「末片下了 62%」。
func TestAggregatePiecesShortLastPiece(t *testing.T) {
	// 3 个整片 + 一个 500 字节的末片(总长 3500)。
	missing := func(piece int) int64 {
		if piece == 3 {
			return 500
		}
		return 0
	}
	got := aggregatePieces(1000, 3500, 4, missing)
	if len(got) != 4 {
		t.Fatalf("buckets = %d, want 4", len(got))
	}
	if got[3] != 0 {
		t.Errorf("last bucket = %d, want 0 (the short last piece is untouched)", got[3])
	}
}

// TestAggregatePiecesSamplesHugeTorrent 校验分片极多时按等距抽样:抽样
// 只是估算,但桶数、完成度都要对,不能因为分片多就画不出来。
func TestAggregatePiecesSamplesHugeTorrent(t *testing.T) {
	const numPieces = maxPieceScan * 2
	complete := func(int) int64 { return 0 }
	got := aggregatePieces(1024, numPieces*1024, numPieces, complete)
	if len(got) != maxPieceBuckets {
		t.Fatalf("buckets = %d, want %d", len(got), maxPieceBuckets)
	}
	for i, v := range got {
		if v != 255 {
			t.Fatalf("bucket %d = %d, want 255", i, v)
		}
	}
	// 一半没下 → 0。
	empty := func(int) int64 { return 1024 }
	got = aggregatePieces(1024, numPieces*1024, numPieces, empty)
	for i, v := range got {
		if v != 0 {
			t.Fatalf("bucket %d = %d, want 0", i, v)
		}
	}
}
