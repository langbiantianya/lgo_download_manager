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
	"io"
	"log/slog"
	"math/rand/v2"
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
	ChunkCount            int
	ChunkSizes            []int64
	ResumeFrom            []int64
	MinChunkSize          int64
	Progress              func(Progress)
	ProgressEvery         time.Duration
	TaskID                string
	OnChunkCountDecreased func(newCount int)
	// OnPlanChanged 在引擎合并或重新规划其分片时触发
	// (例如当服务器不支持字节区间时,引擎回退到单个流式分片)。
	// 该回调以新的 [start0,end0,start1,end1,...] 区间对作为参数调用。
	OnPlanChanged func(ranges []int64)
	// Logger 是 engine 内部的诊断输出通道(分片重规划/错误等);
	// 业务侧应传 internal/logging.L() 以统一进入 lgdm 日志系统。
	// 留空时静默,不向任何 handler 写。
	Logger *slog.Logger
}

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

// silentLogger 是默认占位:写向 io.Discard,保证 engine 在调用方
// 没传 Logger 时不会去申请默认 stdout。
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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
	log        *slog.Logger
}

const oneMiB = 1 << 20

type chunk struct {
	idx      int
	start    int64
	end      int64
	progress int64
}

// NewJob 构造一个分片下载任务。
//
// 所有权约定：dest 由调用方持有并负责 Close（调度器在引擎 goroutine 退出时
// 关闭它）；Job.Close() 只关闭 driver。Run 只通过 WriteAt 写入 dest，
// 因此 dest 必须支持随机写且已按 total 预分配。
func NewJob(driver protocol.ProtocolDriver, total int64, dest *os.File, opts Options) *Job {
	opts.defaults()
	if opts.Logger == nil {
		opts.Logger = silentLogger()
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
	j.planLocked()
}

// planLocked 是 plan 的主体，调用方须持有 j.mu。
func (j *Job) planLocked() {
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

// resumedBytes 返回本次会话开始前磁盘上已有的字节数。plan() 已把
// ResumeFrom 折算进每个分片的 progress，因此这里按「分片内已完成字节」
// 求和即可——不能直接累加 ResumeFrom/ChunkSizes，否则在流式（total<=0）
// 或显式分片场景下会把未落盘的字节也算进去。
func (j *Job) resumedBytes() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	var n int64
	for i := range j.chunks {
		if d := j.chunks[i].progress - j.chunks[i].start; d > 0 {
			n += d
		}
	}
	return n
}

// chunkConcurrencyLimit 返回分片路径允许的最大并发（受配置与实际分片数限制）。
func (j *Job) chunkConcurrencyLimit() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	n := j.chunkCount
	if n < 1 {
		n = 1
	}
	if n > len(j.chunks) {
		n = len(j.chunks)
	}
	if n < 1 {
		n = 1
	}
	return n
}

// unfinishedIndexes 返回当前计划中尚未完成的分片下标。
func (j *Job) unfinishedIndexes() []int {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]int, 0, len(j.chunks))
	for i := range j.chunks {
		if atomic.LoadInt64(&j.chunks[i].progress) <= j.chunks[i].end {
			out = append(out, i)
		}
	}
	return out
}

// replan 按每个分片已完成的偏移重新切分剩余区间，返回新的
// [start0,end0,start1,end1,...]。必须在所有写者停止后调用（调用方保证）。
func (j *Job) replan() []int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	resume := make([]int64, len(j.chunks))
	for i := range j.chunks {
		d := atomic.LoadInt64(&j.chunks[i].progress) - j.chunks[i].start
		if d < 0 {
			d = 0
		}
		resume[i] = d
	}
	j.opts.ResumeFrom = resume
	j.planLocked()
	ranges := make([]int64, 0, 2*len(j.chunks))
	for i := range j.chunks {
		ranges = append(ranges, j.chunks[i].start, j.chunks[i].end)
	}
	return ranges
}

// Chunks 返回当前分片进度的快照。持锁读取，避免与 plan()/replan()
// 的切片重建竞争（调度器会在刷盘与事件路径上调用它）。
func (j *Job) Chunks() []ChunkSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
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
func (j *Job) Stop()           { j.stopped.Store(true) }

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
	j.log.Info("starting download", "total", j.total, "range", useRange, "maxChunks", j.chunkCount)
	j.mu.Unlock()

	// bytesDone 是本次会话累计写入的字节；resumedBytes 是会话开始前磁盘上
	// 已有的字节数。Progress 上报的是两者之和，因此断点续传时 UI 不会回退。
	var (
		bytesDone   atomic.Int64
		chunksDone  atomic.Int32
		lastTickAt  atomic.Int64
		lastTickVal atomic.Int64
	)
	lastTickAt.Store(time.Now().UnixNano())
	resumedBytes := j.resumedBytes()

	emit := func(activeIdx int) {
		now := time.Now().UnixNano()
		prev := lastTickAt.Load()
		if now-prev < int64(j.opts.ProgressEvery) {
			return
		}
		// 进度回调由多个分片 goroutine 并发触发，CAS 保证只有一个越过
		// 节流窗口（同时消除节流状态本身的数据竞争）。
		if !lastTickAt.CompareAndSwap(prev, now) {
			return
		}
		cur := bytesDone.Load()
		diff := cur - lastTickVal.Load()
		lastTickVal.Store(cur)
		var bps float64
		if dt := float64(now-prev) / float64(time.Second); dt > 0 {
			bps = float64(diff) / dt
		}
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
		// 将计划合并为覆盖整个文件的单个分片，这样 Chunks() 只在一个
		// 分片上报告进度，而不是 N 个分片中只有 chunks[0] 真正被写入。
		// 否则调度器会从不声明 Accept-Ranges 的服务器上读到类似
		// [N, 0, 0, 0] 的进度——UI 只会点亮第一块马赛克。
		j.mu.Lock()
		var progress int64
		if len(j.chunks) > 0 {
			progress = j.chunks[0].progress
		}
		end := int64(-1)
		if j.total > 0 {
			end = j.total - 1
		}
		j.chunks = []chunk{{idx: 0, start: progress, end: end, progress: progress}}
		plan := [2]int64{progress, end}
		j.mu.Unlock()

		// 回调不持锁调用：调度器会在回调里访问引擎与数据库。
		if j.opts.OnPlanChanged != nil {
			j.opts.OnPlanChanged([]int64{plan[0], plan[1]})
		}
		stopErr := downloadFallbackWithRetry(ctx, j, j.streamingChunk(), &bytesDone, emit, 0)
		if j.opts.Progress != nil {
			j.opts.Progress(Progress{
				TaskID:          j.opts.TaskID,
				TotalSize:       bytesDone.Load() + resumedBytes,
				DownloadedBytes: bytesDone.Load() + resumedBytes,
				CompletedChunks: 1,
			})
		}
		return stopErr
	}

	// ---- 分片路径：worker 池 ----
	//
	// 目标并发从 1 开始，每有一个分片成功完成就 +1（上限为配置的
	// ChunkCount）；一旦出现非终态失败就减半，重试预算用尽则整体失败。
	//
	// 关键：重新规划必须在所有在跑的分片退出之后进行。此前实现收到第一个
	// 错误就立刻重规划并重启分片，旧 goroutine 仍在写同一区间，导致重复
	// 下载与 bytesDone 重复计数。
	maxConc := j.chunkConcurrencyLimit()
	target := 1
	backoff := time.Second
	rounds := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if j.stopped.Load() {
			return errJobStopped
		}

		queue := j.unfinishedIndexes()
		if len(queue) == 0 {
			// 全部完成：补一次速度归零的终态进度。
			if j.opts.Progress != nil {
				j.opts.Progress(Progress{
					TaskID:          j.opts.TaskID,
					TotalSize:       j.total,
					DownloadedBytes: bytesDone.Load() + resumedBytes,
					CompletedChunks: int(chunksDone.Load()),
				})
			}
			return nil
		}

		roundCtx, roundCancel := context.WithCancel(ctx)
		results := make(chan error, len(queue))
		var wg sync.WaitGroup
		inflight := 0
		var lastErr error

		for {
			for inflight < target && len(queue) > 0 {
				idx := queue[0]
				queue = queue[1:]
				inflight++
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					// results 容量足够，worker 永不阻塞。
					results <- j.downloadChunk(roundCtx, idx, &bytesDone, &chunksDone, emit)
				}(idx)
			}
			if inflight == 0 {
				break
			}
			err := <-results
			inflight--
			if err == nil {
				// 成功完成一路：允许再开一路（保守增长）。
				if target < maxConc {
					target++
				}
				continue
			}
			lastErr = err
			break
		}
		roundCancel()
		// 等本轮所有分片真正退出，之后才能安全地重新规划。
		wg.Wait()

		if err := ctx.Err(); err != nil {
			return err
		}
		if j.stopped.Load() {
			return errJobStopped
		}
		if lastErr == nil {
			// 本轮队列已空且无错误：回到外层判断是否全部完成。
			continue
		}
		if protocol.IsTerminal(lastErr) {
			return lastErr
		}
		rounds++
		if rounds > maxReplanRounds {
			j.log.Warn("giving up after replan rounds", "rounds", rounds-1, "err", lastErr)
			return lastErr
		}
		old := target
		target /= 2
		if target < 1 {
			target = 1
		}
		if old != target {
			j.log.Warn("reducing chunk concurrency", "old", old, "new", target, "err", lastErr)
			if j.opts.OnChunkCountDecreased != nil {
				j.opts.OnChunkCountDecreased(target)
			}
		} else {
			j.log.Warn("chunk error", "err", lastErr)
		}
		// 回调不持锁调用：调度器会在回调里访问引擎与数据库。
		ranges := j.replan()
		if j.opts.OnPlanChanged != nil {
			j.opts.OnPlanChanged(ranges)
		}
		wait := backoff + jitter(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// streamingChunk 返回流式路径当前使用的那个分片。
func (j *Job) streamingChunk() *chunk {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.chunks) == 0 {
		return &chunk{idx: 0, start: 0, end: -1}
	}
	return &j.chunks[0]
}

// downloadChunk 运行单个分片的重试循环。返回 nil 表示该分片已完成；
// 返回错误表示它遇到终态错误或已耗尽重试预算。
//
// 调用方须保证计划（j.chunks）在本次调用期间不变：重新规划只会在
// 所有分片退出后（wg.Wait 之后）进行。
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
		err := j.driver.DownloadChunk(chunkCtx, start, c.end, j.dest, func(n int) {
			bytesDone.Add(int64(n))
			atomic.AddInt64(&c.progress, int64(n))
			emit(idx)
		})
		cancel()

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
		j.log.Warn("chunk error backing off", "idx", idx, "err", err, "backoff", backoff, "fails", consecutiveFails, "maxFails", maxChunkRetries)
		if consecutiveFails >= maxChunkRetries {
			j.log.Warn("chunk giving up after consecutive failures", "idx", idx, "fails", consecutiveFails, "err", err)
			return err
		}
		wait := backoff + jitter(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (j *Job) Close() error { return j.driver.Close() }

// maxChunkRetries 限制单 chunk 或 streaming 路径上的连续失败次数。
// 超过此次数后,engine 把 lastErr 返回给 scheduler.fail(),任务变为 Failed。
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
func downloadFallbackWithRetry(ctx context.Context, j *Job, c *chunk, bytesDone *atomic.Int64, emit func(int), chunkIdx int) error {
	backoff := 500 * time.Millisecond
	consecutiveFails := 0
	for {
		if j.stopped.Load() || ctx.Err() != nil {
			return ctx.Err()
		}
		start := atomic.LoadInt64(&c.progress)
		err := j.driver.DownloadFallback(ctx, start, j.dest, func(n int) {
			bytesDone.Add(int64(n))
			atomic.AddInt64(&c.progress, int64(n))
			emit(chunkIdx)
		})
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
		if consecutiveFails >= maxChunkRetries {
			return err
		}
		j.log.Warn("streaming chunk error backing off", "err", err, "backoff", backoff, "fails", consecutiveFails, "maxFails", maxChunkRetries)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff + jitter(backoff)):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
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
