// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// 本文件负责分片规划与重规划：把总字节数切成可并发的工作单元，并在
// 失败后按需重划尚未完成的部分。
package engine

import (
	"sync/atomic"
)

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
			progress := start
			if i/2 < len(j.opts.ResumeFrom) {
				progress += j.opts.ResumeFrom[i/2]
				if progress < start {
					progress = start
				}
				if progress > end {
					progress = end + 1
				}
			}
			j.chunks = append(j.chunks, chunk{idx: i / 2, start: start, end: end, progress: progress})
		}
		return
	}
	if j.total <= 0 {
		j.chunks = append(j.chunks, chunk{idx: 0, start: 0, end: -1})
		return
	}
	// 文件小于 1 MiB：不分块，单个流式 chunk。
	if j.total < oneMiB {
		progress := int64(0)
		if len(j.opts.ResumeFrom) > 0 {
			progress = j.opts.ResumeFrom[0]
			if progress < 0 {
				progress = 0
			}
			if progress > j.total {
				progress = j.total
			}
		}
		j.chunks = append(j.chunks, chunk{idx: 0, start: 0, end: j.total - 1, progress: progress})
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
	logicalLimit := max(j.chunkCount, 1) * workStealingPartsPerWorker
	// 兼容旧版本任务：没有持久化 ChunkRanges 时，历史 ChunkProgress
	// 是按 ChunkCount 个等分块记录的。此时保持旧切分，避免把偏移套到
	// 另一套边界上；新任务以及已有范围快照仍使用可在 worker 间偷取的
	// 细粒度逻辑切片。
	if len(j.opts.ResumeFrom) > 0 {
		logicalLimit = max(j.chunkCount, 1)
	}
	if n > logicalLimit {
		n = logicalLimit
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
	ranges := make([]int64, 0, 2*len(j.chunks))
	for i := range j.chunks {
		d := atomic.LoadInt64(&j.chunks[i].progress) - j.chunks[i].start
		if d < 0 {
			d = 0
		}
		resume[i] = d
		ranges = append(ranges, j.chunks[i].start, j.chunks[i].end)
	}
	j.opts.ChunkSizes = ranges
	j.opts.ResumeFrom = resume
	j.planLocked()
	ranges = ranges[:0]
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
