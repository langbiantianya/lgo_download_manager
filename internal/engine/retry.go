// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// 本文件集中分片重试与退避策略：重试次数、重规划轮数、抖动与等待。
package engine

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"lgo_download_manager/internal/protocol"
)

const maxChunkRetries = 8

// maxReplanRounds 限制「失败 → 重新规划」的轮数：单个区间的总尝试次数
// 约为 maxChunkRetries × maxReplanRounds。

const maxReplanRounds = 3

// errJobStopped 表示任务被显式 Stop()（区别于 ctx 取消导致的暂停）。

var errJobStopped = errors.New("engine: job stopped")

// jitter 返回 [0, d/2) 的随机抖动，避免多个任务在同一时刻重试形成尖峰。

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(d) / 2))
}

// downloadFallbackWithRetry 是 streaming 路径（不支持 Range）下的重试包装:
// 业务错误(4xx)立即返回让上层标记 Failed;瞬断则在重试预算内退避重连。

func retryDelay(err error, backoff time.Duration) time.Duration {
	const maxServerHint = 5 * time.Minute
	if hint := protocol.RetryAfter(err); hint > 0 {
		if hint > maxServerHint {
			hint = maxServerHint
		}
		return hint
	}
	return backoff + jitter(backoff)
}

// waitRetry 使用可停止的 Timer；暂停时 time.After 留下的定时器会一直
// 存活到原定触发时间，高并发分片同时取消时会制造一批无意义堆对象。

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
