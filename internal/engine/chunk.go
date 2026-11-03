// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// 本文件负责单个分片的下载：优先走 Range 请求，Range 不可用时退化为
// 从头发送并丢弃已写入的部分；同时负责带宽限速。
package engine

import (
	"context"
	"errors"
	"fmt"

	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"lgo_download_manager/internal/protocol"
)

func (j *Job) downloadChunk(ctx context.Context, idx int, bytesDone *atomic.Int64, chunksDone *atomic.Int32, emit func(int)) error {
	j.mu.Lock()
	if idx >= len(j.chunks) {
		j.mu.Unlock()
		return nil
	}
	c := &j.chunks[idx]
	j.mu.Unlock()

	consecutiveFails := 0
	backoff := 500 * time.Millisecond
	for {
		if j.stopped.Load() || ctx.Err() != nil {
			return ctx.Err()
		}
		start := atomic.LoadInt64(&c.progress)
		if start > c.end {
			j.log.Info("chunk finished", "idx", idx, "range", fmt.Sprintf("%d-%d", c.start, c.end))
			return nil
		}

		chunkCtx, cancel := context.WithCancel(ctx)
		var throttleErr error
		err := j.driver.DownloadChunk(chunkCtx, start, c.end, j.dest, func(n int) {
			if throttleErr = j.throttle(chunkCtx, n); throttleErr != nil {
				cancel()
				return
			}
			bytesDone.Add(int64(n))
			atomic.AddInt64(&c.progress, int64(n))
			emit(idx)
		})
		cancel()
		if throttleErr != nil {
			err = throttleErr
		}
		if err == nil && atomic.LoadInt64(&c.progress) <= c.end {
			err = fmt.Errorf("chunk stopped at %d, want through %d: %w",
				atomic.LoadInt64(&c.progress)-1, c.end, protocol.ErrUnexpectedEOF)
		}

		if err == nil {
			chunksDone.Add(1)
			return nil
		}
		if j.stopped.Load() || ctx.Err() != nil {
			return ctx.Err()
		}
		if protocol.IsTerminal(err) {
			j.log.Warn("chunk terminal error", "idx", idx, "err", err)
			return err
		}
		consecutiveFails++
		j.log.Warn("chunk error backing off", "idx", idx, "err", err, "backoff", backoff, "fails", consecutiveFails, "maxFails", j.opts.MaxRetries)
		if consecutiveFails >= j.opts.MaxRetries {
			j.log.Warn("chunk giving up after consecutive failures", "idx", idx, "fails", consecutiveFails, "err", err)
			return err
		}
		wait := backoff + jitter(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
		if waitErr := waitRetry(ctx, wait); waitErr != nil {
			return waitErr
		}
	}
}

func downloadFallbackWithRetry(ctx context.Context, j *Job, c *chunk, bytesDone *atomic.Int64, emit func(int), chunkIdx int) error {
	backoff := 500 * time.Millisecond
	consecutiveFails := 0
	for {
		if j.stopped.Load() || ctx.Err() != nil {
			return ctx.Err()
		}
		start := atomic.LoadInt64(&c.progress)
		readCtx, readCancel := context.WithCancel(ctx)
		var throttleErr error
		err := j.driver.DownloadFallback(readCtx, start, j.dest, func(n int) {
			if throttleErr = j.throttle(readCtx, n); throttleErr != nil {
				readCancel()
				return
			}
			bytesDone.Add(int64(n))
			atomic.AddInt64(&c.progress, int64(n))
			emit(chunkIdx)
		})
		readCancel()
		if throttleErr != nil {
			err = throttleErr
		}
		if errors.Is(err, protocol.ErrResumeUnavailable) {
			delta := atomic.LoadInt64(&c.progress) - c.start
			if delta > 0 {
				bytesDone.Add(-delta)
			}
			// 无法续传时必须明确回到绝对字节 0，而不是“当前 chunk 的
			// 起点”；否则调用方若传入非零 c.start，会反复触发同一次失败。
			c.start = 0
			atomic.StoreInt64(&c.progress, 0)
			consecutiveFails = 0
			continue
		}
		if err == nil && c.end >= 0 && atomic.LoadInt64(&c.progress) <= c.end {
			err = fmt.Errorf("stream stopped at %d, want through %d: %w",
				atomic.LoadInt64(&c.progress)-1, c.end, protocol.ErrUnexpectedEOF)
		}
		if err == nil {
			return nil
		}
		if j.stopped.Load() || ctx.Err() != nil {
			return ctx.Err()
		}
		if protocol.IsTerminal(err) {
			return err
		}
		consecutiveFails++
		if consecutiveFails >= j.opts.MaxRetries {
			return err
		}
		j.log.Warn("streaming chunk error backing off", "err", err, "backoff", backoff, "fails", consecutiveFails, "maxFails", j.opts.MaxRetries)
		if waitErr := waitRetry(ctx, retryDelay(err, backoff)); waitErr != nil {
			return waitErr
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

// throttle 在当前任务共享限速器上等待 n 个字节的令牌。rate.Limiter
// 要求单次 WaitN 不超过 burst；这里按 burst 拆分，调用方无需关心设置。

func (j *Job) throttle(ctx context.Context, n int) error {
	limiter := j.opts.RateLimiter
	if limiter == nil || n <= 0 || limiter.Limit() == rate.Inf {
		return nil
	}
	burst := limiter.Burst()
	if burst <= 0 {
		return nil
	}
	for n > 0 {
		step := min(n, burst)
		if err := limiter.WaitN(ctx, step); err != nil {
			return err
		}
		n -= step
	}
	return nil
}

// retryDelay 优先遵循服务端的 Retry-After；没有提示时使用带抖动的
// 指数退避。对异常的超长提示设置上限，同时保持 ctx 可取消。
