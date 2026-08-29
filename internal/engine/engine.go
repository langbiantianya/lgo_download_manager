// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package engine 将统一的 ProtocolDriver 和字节区间规划
// 转换为对一个预分配输出文件的并发、可恢复写入。
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"lgo_download_manager/internal/protocol"
)

// Progress 描述一次下载任务的进度快照。
type Progress struct {
	TaskID           string
	TotalSize        int64
	DownloadedBytes  int64
	SpeedBPS         float64
	CompletedChunks  int
	ActiveChunkIndex int
}

type Options struct {
	ChunkCount    int
	ChunkSizes    []int64
	ResumeFrom    []int64
	MinChunkSize  int64
	Progress      func(Progress)
	ProgressEvery time.Duration
	TaskID        string
	OnChunkCountDecreased func(newCount int)
	// OnPlanChanged 在引擎合并或重新规划其分片时触发
	// (例如当服务器不支持字节区间时,引擎回退到单个流式分片)。
	// 该回调以新的 [start0,end0,start1,end1,...] 区间对作为参数调用。
	OnPlanChanged func(ranges []int64)
	Logger        Logger
}

type Logger interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
}

var noOpLogger Logger = noOpFn{}

type noOpFn struct{}

func (noOpFn) Infof(string, ...any) {}
func (noOpFn) Warnf(string, ...any) {}

func (o *Options) defaults() {
	if o.ChunkCount <= 0 {
		o.ChunkCount = 4
	}
	if o.MinChunkSize <= 0 {
		o.MinChunkSize = 1 << 20
	}
	if o.ProgressEvery <= 0 {
		o.ProgressEvery = 250 * time.Millisecond
	}
}

type Job struct {
	driver     protocol.ProtocolDriver
	dest       *os.File
	total      int64
	opts       Options
	chunks     []chunk
	stopped    atomic.Bool
	mu         sync.Mutex
	chunkCount int
	log        Logger
}


const oneMiB = 1 << 20

type chunk struct {
	idx      int
	start    int64
	end      int64
	progress int64
}

func NewJob(driver protocol.ProtocolDriver, total int64, dest *os.File, opts Options) *Job {
	opts.defaults()
	if opts.Logger == nil {
		opts.Logger = noOpLogger
	}
	j := &Job{
		driver:     driver,
		dest:       dest,
		total:      total,
		opts:       opts,
		chunkCount: opts.ChunkCount,
		log:        opts.Logger,
	}
	j.plan()
	return j
}

func (j *Job) plan() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.chunks = nil

	if len(j.opts.ChunkSizes) > 0 {
		for i := 0; i+1 < len(j.opts.ChunkSizes); i += 2 {
			start := j.opts.ChunkSizes[i]
			end := j.opts.ChunkSizes[i+1]
			j.chunks = append(j.chunks, chunk{idx: i / 2, start: start, end: end, progress: start})
		}
		return
	}
	if j.total <= 0 {
		j.chunks = append(j.chunks, chunk{idx: 0, start: 0, end: -1})
		return
	}
	// 文件小于 1 MiB：不分块，单个流式 chunk。
	if j.total < oneMiB {
		j.chunks = append(j.chunks, chunk{idx: 0, start: 0, end: j.total - 1, progress: 0})
		return
	}

	// 文件大于等于 1 MiB 但小于 MinChunkSize：
	// 按 ChunkCount 等分，忽略 MinChunkSize。
	if j.total < j.opts.MinChunkSize {
		n := j.chunkCount
		if n <= 0 {
			n = 1
		}
		step := j.total / int64(n)
		rem := j.total % int64(n)
		start := int64(0)
		for i := 0; i < n; i++ {
			size := step
			if i == n-1 {
				size += rem
			}
			end := start + size - 1
			c := chunk{idx: i, start: start, end: end, progress: start}
			if i < len(j.opts.ResumeFrom) {
				c.progress = c.start + j.opts.ResumeFrom[i]
				if c.progress < c.start {
					c.progress = c.start
				}
				if c.progress > c.end {
					c.progress = c.end + 1
				}
			}
			j.chunks = append(j.chunks, c)
			start = end + 1
		}
		return
	}

	// 文件大于等于 MinChunkSize：每个 chunk 至少 MinChunkSize，
	// 由 ChunkCount 限制最大并发数。
	n := int(j.total / j.opts.MinChunkSize)
	if j.total%j.opts.MinChunkSize != 0 {
		n++
	}
	if j.chunkCount > 0 && n > j.chunkCount {
		n = j.chunkCount
	}
	if n < 1 {
		n = 1
	}
	step := j.total / int64(n)
	rem := j.total % int64(n)
	start := int64(0)
	for i := 0; i < n; i++ {
		size := step
		if i == n-1 {
			size += rem
		}
		end := start + size - 1
		c := chunk{idx: i, start: start, end: end, progress: start}
		if i < len(j.opts.ResumeFrom) {
			c.progress = c.start + j.opts.ResumeFrom[i]
			if c.progress < c.start {
				c.progress = c.start
			}
			if c.progress > c.end {
				c.progress = c.end + 1
			}
		}
		j.chunks = append(j.chunks, c)
		start = end + 1
	}
}

func (j *Job) Chunks() []ChunkSnapshot {
	out := make([]ChunkSnapshot, len(j.chunks))
	for i := range j.chunks {
		c := &j.chunks[i]
		out[i] = ChunkSnapshot{
			Index:    c.idx,
			Start:    c.start,
			End:      c.end,
			Progress: atomic.LoadInt64(&c.progress),
		}
	}
	return out
}

type ChunkSnapshot struct {
	Index    int
	Start    int64
	End      int64
	Progress int64
}

func (j *Job) IsStopped() bool { return j.stopped.Load() }
func (j *Job) Stop()          { j.stopped.Store(true) }

func (j *Job) Run(ctx context.Context, useRange bool) error {
	j.stopped.Store(false)

	j.mu.Lock()
	if len(j.chunks) == 0 {
		j.mu.Unlock()
		return errors.New("engine: empty chunk plan")
	}
	j.mu.Unlock()

	if err := j.sanityCheck(); err != nil {
		return err
	}

	j.mu.Lock()
	j.log.Infof("starting download  total=%d bytes  range=%v  maxChunks=%d", j.total, useRange, j.chunkCount)
	j.mu.Unlock()

	var (
		bytesDone   atomic.Int64
		chunksDone  atomic.Int32
		lastTickAt  = time.Now()
		lastTickVal atomic.Int64
	)

	// resumedBytes 是本次会话开始之前磁盘上已有的字节总数。
	// bytesDone 是会话级别的累加;Progress 回调报告的是累计总量,
	// 这样在断点续传时 UI 不会回退到 0。
	var resumedBytes int64
	for _, off := range j.opts.ResumeFrom {
		if off > 0 {
			resumedBytes += off
		}
	}
	if len(j.opts.ChunkSizes) > 0 {
		for i := 0; i+1 < len(j.opts.ChunkSizes); i += 2 {
			resumedBytes += j.opts.ChunkSizes[i]
		}
	}

	emit := func(activeIdx int) {
		now := time.Now()
		if now.Sub(lastTickAt) < j.opts.ProgressEvery {
			return
		}
		cur := bytesDone.Load()
		dt := now.Sub(lastTickAt).Seconds()
		diff := cur - lastTickVal.Load()
		var bps float64
		if dt > 0 {
			bps = float64(diff) / dt
		}
		lastTickVal.Store(cur)
		lastTickAt = now
		slog.Info("engine progress",
			"taskID", j.opts.TaskID,
			"downloaded", cur+resumedBytes,
			"total", j.total,
			"speed", bps,
		)
		if j.opts.Progress != nil {
			j.opts.Progress(Progress{
				TaskID:           j.opts.TaskID,
				TotalSize:        j.total,
				DownloadedBytes:  cur + resumedBytes,
				SpeedBPS:         bps,
				CompletedChunks:  int(chunksDone.Load()),
				ActiveChunkIndex: activeIdx,
			})
		}
	}

	j.mu.Lock()
	isStreaming := j.total <= 0 || !useRange || (len(j.chunks) == 1 && j.chunks[0].end == -1)
	j.mu.Unlock()

	if isStreaming {
		j.mu.Lock()
		// 将计划合并为覆盖整个文件的单个分片,这样
		// Chunks() 只在一个分片上报告进度,而不是 N 个分片
		// 中只有 chunks[0] 真正被写入。否则,调度器会从
		// 不声明 Accept-Ranges 的服务器上读到类似 [N, 0, 0, 0]
		// 的进度——UI 只会点亮第一块马赛克。
		var progress int64
		if len(j.chunks) > 0 {
			progress = j.chunks[0].progress
		}
		end := int64(-1)
		if j.total > 0 {
			end = j.total - 1
		}
		j.chunks = []chunk{{idx: 0, start: progress, end: end, progress: progress}}
		c := &j.chunks[0]
		if j.opts.OnPlanChanged != nil {
			j.opts.OnPlanChanged([]int64{c.start, c.end})
		}
		stopErr := j.driver.DownloadFallback(ctx, c.progress, j.dest, func(n int) {
			bytesDone.Add(int64(n))
			atomic.AddInt64(&c.progress, int64(n))
			emit(0)
		})
		if j.opts.Progress != nil {
			j.opts.Progress(Progress{
				TaskID:          j.opts.TaskID,
				TotalSize:       bytesDone.Load() + resumedBytes,
				DownloadedBytes: bytesDone.Load() + resumedBytes,
				SpeedBPS:        0,
				CompletedChunks: 1,
			})
		}
		return stopErr
	}
	// 保守的并发增长策略:从 1 个分片开始。如果某个分片顺利完成,
	// 就再增加一个。一旦出现错误,就停止增长并回退。

	activeCount := 1

	// downloadChunk 运行单个分片的重试循环。
	downloadChunk := func(c *chunk, idx int, errCh chan<- error) {
		defer func() {
			chunksDone.Add(1)
		}()

		backoff := 500 * time.Millisecond
		for {
			if j.stopped.Load() || ctx.Err() != nil {
				return
			}
			start := atomic.LoadInt64(&c.progress)
			if start > c.end {
				j.log.Infof("chunk %d finished  range=%d-%d", idx, c.start, c.end)
				return
			}

			chunkCtx, cancel := context.WithCancel(ctx)
			err := j.driver.DownloadChunk(chunkCtx, start, c.end, j.dest, func(n int) {
				bytesDone.Add(int64(n))
				atomic.AddInt64(&c.progress, int64(n))
				emit(idx)
			})
			cancel()

			if err != nil {
				if j.stopped.Load() || ctx.Err() != nil {
					return
				}
				j.log.Warnf("chunk %d error: %v  backing off %v", idx, err, backoff)
				select {
				case errCh <- err:
				default:
				}
				wait := backoff
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
				continue
			}
			return
		}
	}

	errCh := make(chan error, 1)

	for {
		j.mu.Lock()
		curChunks := j.chunks
		j.mu.Unlock()

		toLaunch := activeCount
		if toLaunch > len(curChunks) {
			toLaunch = len(curChunks)
		}

		wg := sync.WaitGroup{}
		for i := 0; i < toLaunch; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				downloadChunk(&curChunks[idx], idx, errCh)
			}(i)
		}

		j.log.Infof("round starting  active=%d chunks", toLaunch)

		// 等待分片结束或出现错误。
		var lastErr error
		waitDone := make(chan struct{})
		go func() {
			wg.Wait()
			close(waitDone)
		}()

		select {
		case err := <-errCh:
			lastErr = err
		case <-waitDone:
			// 所有分片已完成且无错误。
			j.mu.Lock()
			allDone := true
			for _, c := range j.chunks {
				if atomic.LoadInt64(&c.progress) <= c.end {
					allDone = false
					break
				}
			}
			j.mu.Unlock()
			if allDone {
				j.log.Infof("download completed  %d bytes", bytesDone.Load()+resumedBytes)
				if j.opts.Progress != nil {
					j.opts.Progress(Progress{
						TaskID:          j.opts.TaskID,
						TotalSize:       j.total,
						DownloadedBytes: bytesDone.Load() + resumedBytes,
						SpeedBPS:        0,
						CompletedChunks: int(chunksDone.Load()),
					})
				}
				return nil
			}
			// 还未完成——尝试增加并发。
			if activeCount < j.chunkCount && activeCount < len(j.chunks) {
				activeCount++
				j.log.Infof("chunk completed, growing active chunks to %d", activeCount)
			}
			continue
		case <-ctx.Done():
			j.log.Infof("download stopped")
			return ctx.Err()
		}

		// 错误路径。
		if lastErr == nil {
			continue
		}

		j.log.Warnf("chunk error: %v  pausing growth, backing off 1s", lastErr)

		if j.stopped.Load() || ctx.Err() != nil {
			j.log.Infof("download stopped")
			return nil
		}

		// 缩减。
		oldActive := activeCount
		activeCount = activeCount / 2
		if activeCount < 1 {
			activeCount = 1
		}

		if oldActive != activeCount {
			j.mu.Lock()
			j.chunkCount = activeCount
			j.mu.Unlock()
			if j.opts.OnChunkCountDecreased != nil {
				j.opts.OnChunkCountDecreased(activeCount)
			}
			j.log.Warnf("reduced active chunks %d -> %d", oldActive, activeCount)
		}

		// 重新规划。
		j.mu.Lock()
		progress := make([]int64, len(j.chunks))
		for i, c := range j.chunks {
			progress[i] = atomic.LoadInt64(&c.progress) - c.start
		}
		j.opts.ResumeFrom = progress
		j.plan()
		if j.opts.OnPlanChanged != nil {
			ranges := make([]int64, 0, 2*len(j.chunks))
			for _, c := range j.chunks {
				ranges = append(ranges, c.start, c.end)
			}
			j.opts.OnPlanChanged(ranges)
		}
		j.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

func (j *Job) sanityCheck() error {
	stat, err := j.dest.Stat()
	if err != nil {
		return err
	}
	if stat.Size() < j.total {
		return fmt.Errorf("engine: dest file size %d < expected total %d", stat.Size(), j.total)
	}
	return nil
}

func (j *Job) TotalSize() int64 { return j.total }

func (j *Job) Close() error { return j.driver.Close() }