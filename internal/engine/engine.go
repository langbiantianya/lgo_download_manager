// Package engine turns a unified ProtocolDriver and a plan of byte ranges
// into a concurrent, resumable write into a pre-allocated output file.
//
// The engine owns no file descriptors outside of `Dest`, which the caller
// opens (usually via prealloc.Preallocate). It is fully data-driven via
// the Progress callback and emits status events through Stop checks.
//
// Lifecycle of a Job:
//  1. Construct with NewJob(driver, totalSize, destFile, opts).
//  2. Set Progress callback in opts.
//  3. Call Run(ctx, useRange). It blocks until completion, ctx cancel, or
//     fatal error. The error is returned to the caller.
//  4. Stop() cancels the running job from another goroutine.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"lgo_download_manager/internal/protocol"
)

// Progress captures a snapshot of a running job.
type Progress struct {
	TaskID           string  // stable task id; "" if engine doesn't track tasks
	TotalSize        int64   // bytes total
	DownloadedBytes  int64   // bytes downloaded so far
	SpeedBPS         float64 // rolling average over the throttle window
	CompletedChunks  int     // chunks finished since last tick
	ActiveChunkIndex int     // most-recent chunk to make progress (best-effort)
}

// Options controls the partition / retry / resume behavior of a Job.
type Options struct {
	ChunkCount    int           // number of parallel chunks (default 4)
	ChunkSizes    []int64       // optional explicit (start,end) inclusive pairs
	ResumeFrom    []int64       // per-chunk resume offsets (already written)
	MinChunkSize  int64         // smallest chunk allowed (default 1 MiB)
	Progress      func(Progress)
	ProgressEvery time.Duration // default 250ms

	// TaskID is echoed into Progress so the UI can correlate. Optional.
	TaskID string
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

// Job runs one download end to end.
type Job struct {
	driver  protocol.ProtocolDriver
	dest    *os.File
	total   int64 // bytes total
	opts    Options
	chunks  []chunk
	stopped atomic.Bool
}

// chunk holds the byte range a goroutine owns and its current write
// progress (so we can resume and so the UI can show per-thread stats).
type chunk struct {
	idx      int
	start    int64 // inclusive
	end      int64 // inclusive (-1 for unbounded streaming)
	progress int64 // current write offset within [start, end+1)
}

// NewJob plans the byte ranges for a download using opts.
func NewJob(driver protocol.ProtocolDriver, total int64, dest *os.File, opts Options) *Job {
	opts.defaults()
	j := &Job{
		driver: driver,
		dest:   dest,
		total:  total,
		opts:   opts,
	}
	j.plan()
	return j
}

// plan divides total into chunks. If ResumeFrom[i] is provided, chunk i
// starts at chunk.start + ResumeFrom[i].
func (j *Job) plan() {
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
	n := j.opts.ChunkCount
	maxUseful := int(j.total / j.opts.MinChunkSize)
	if maxUseful > 0 && maxUseful < n {
		n = maxUseful
	}
	if n < 1 {
		n = 1
	}
	step := j.total / int64(n)
	rem := j.total % int64(n)
	start := int64(0)
	for i := range n {
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

// Chunks returns a snapshot of the planned partition.
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

// ChunkSnapshot is a read-only view of one chunk's planned range.
type ChunkSnapshot struct {
	Index    int
	Start    int64
	End      int64
	Progress int64
}

// IsStopped reports whether Stop was called or ctx was canceled.
func (j *Job) IsStopped() bool { return j.stopped.Load() }

// Stop signals the running job to abort. Idempotent.
func (j *Job) Stop() { j.stopped.Store(true) }

// Run executes the planned chunks concurrently.
func (j *Job) Run(ctx context.Context, useRange bool) error {
	j.stopped.Store(false)
	if len(j.chunks) == 0 {
		return errors.New("engine: empty chunk plan")
	}
	if err := j.sanityCheck(); err != nil {
		return err
	}

	var (
		bytesDone   atomic.Int64
		chunksDone  atomic.Int32
		lastTickAt  = time.Now()
		lastTickVal atomic.Int64
	)

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
		if j.opts.Progress != nil {
			j.opts.Progress(Progress{
				TaskID:           j.opts.TaskID,
				TotalSize:        j.total,
				DownloadedBytes:  cur,
				SpeedBPS:         bps,
				CompletedChunks:  int(chunksDone.Load()),
				ActiveChunkIndex: activeIdx,
			})
		}
	}

	// Single-chunk unknown-size case: streaming fallback path.
	streaming := j.total <= 0 || !useRange ||
		(len(j.chunks) == 1 && j.chunks[0].end == -1)
	if streaming {
		if len(j.chunks) > 1 {
			return errors.New("engine: unknown size needs single chunk")
		}
		c := &j.chunks[0]
		off := c.progress
		stopErr := j.driver.DownloadFallback(ctx, off, j.dest, func(n int) {
			bytesDone.Add(int64(n))
			off += int64(n)
			atomic.StoreInt64(&c.progress, off)
			emit(0)
		})
		if j.opts.Progress != nil {
			j.opts.Progress(Progress{
				TaskID:          j.opts.TaskID,
				TotalSize:       bytesDone.Load(),
				DownloadedBytes: bytesDone.Load(),
				SpeedBPS:        0,
				CompletedChunks: 1,
			})
		}
		return stopErr
	}

	// Concurrent chunked path. Each goroutine loops until its chunk is
	// fully written or Stop is called. Progress is recorded through the
	// onData callback that drivers invoke on each read; we reflect that
	// back to chunk.progress.
	var wg sync.WaitGroup
	errs := make([]error, len(j.chunks))
	for i := range j.chunks {
		i := i
		c := &j.chunks[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !j.stopped.Load() {
				if atomic.LoadInt64(&c.progress) > c.end {
					chunksDone.Add(1)
					return
				}
				// Compute the next start we need to fetch.
				start := atomic.LoadInt64(&c.progress)
				if start > c.end {
					chunksDone.Add(1)
					return
				}
				chunkCtx, cancel := context.WithCancel(ctx)
				// Stop quick path: cancel inner ctx immediately if outer stop.
				err := j.driver.DownloadChunk(chunkCtx, start, c.end, j.dest, func(n int) {
					bytesDone.Add(int64(n))
					atomic.AddInt64(&c.progress, int64(n))
					emit(i)
				})
				cancel()
				if err != nil {
					if j.stopped.Load() || ctx.Err() != nil {
						return
					}
					// Transient: back off briefly and retry.
					select {
					case <-ctx.Done():
						return
					case <-time.After(500 * time.Millisecond):
					}
					errs[i] = err
					continue
				}
				chunksDone.Add(1)
				return
			}
		}()
	}
	wg.Wait()

	for _, e := range errs {
		if e != nil && ctx.Err() == nil && !j.stopped.Load() {
			return e
		}
	}
	if j.opts.Progress != nil {
		j.opts.Progress(Progress{
			TaskID:          j.opts.TaskID,
			TotalSize:       j.total,
			DownloadedBytes: bytesDone.Load(),
			SpeedBPS:        0,
			CompletedChunks: int(chunksDone.Load()),
		})
	}
	return nil
}

// sanityCheck confirms the destination file size is at least `total`.
func (j *Job) sanityCheck() error {
	if j.total <= 0 || len(j.chunks) == 0 {
		return nil
	}
	st, err := j.dest.Stat()
	if err != nil {
		return fmt.Errorf("engine dest stat: %w", err)
	}
	if st.Size() < j.total {
		return fmt.Errorf("engine: dest size %d < total %d (call prealloc first)", st.Size(), j.total)
	}
	return nil
}

// TotalSize returns the planned total byte count.
func (j *Job) TotalSize() int64 { return j.total }

// Close releases the driver resources.
func (j *Job) Close() error { return j.driver.Close() }
