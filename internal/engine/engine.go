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

	// OnChunkCountDecreased is called whenever the engine reduces the
	// active chunk count (e.g. server rejected concurrent connections).
	OnChunkCountDecreased func(newCount int)

	// Logger receives human-readable phase messages. A no-op logger is used
	// when nil.
	Logger Logger
}

// Logger is the logging interface accepted by Options.Logger.
type Logger interface {
	// Infof writes an informational message.
	Infof(format string, args ...any)
	// Warnf writes a warning message.
	Warnf(format string, args ...any)
}

// noOpLogger is the default logger when Options.Logger is nil.
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

// Job runs a download end to end.
type Job struct {
	driver  protocol.ProtocolDriver
	dest    *os.File
	total   int64 // bytes total
	opts    Options
	chunks  []chunk
	stopped atomic.Bool

	mu         sync.Mutex // protects planned chunks during re-plan
	chunkCount int        // current planned chunk count
	log        Logger     // defaults to noOpLogger
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

// plan divides total into chunks. If ResumeFrom[i] is provided, chunk i
// starts at chunk.start + ResumeFrom[i].
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
	n := j.chunkCount
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

// Run executes the planned chunks concurrently, adapting chunk count down
// when servers reject concurrent connections.
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
	j.log.Infof("starting download  total=%d bytes  range=%v  chunks=%d", j.total, useRange, j.chunkCount)
	j.mu.Unlock()

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

	// Streaming fallback for unknown total size or single-chunk case.
	j.mu.Lock()
	isStreaming := j.total <= 0 || !useRange || (len(j.chunks) == 1 && j.chunks[0].end == -1)
	j.mu.Unlock()

	if isStreaming {
		j.mu.Lock()
		c := j.chunks[0]
		j.mu.Unlock()
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

	// downloadChunk runs the inner retry loop for one chunk.
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

			j.log.Infof("chunk %d downloading  range=%d-%d  offset=%d", idx, c.start, c.end, start)
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
		j.log.Infof("round starting  chunks=%d", j.chunkCount)
		chunks := j.chunks
		wg := sync.WaitGroup{}
		for i := range chunks {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				downloadChunk(&chunks[idx], idx, errCh)
			}(i)
		}
		j.mu.Unlock()
		wg.Wait()

		if j.stopped.Load() || ctx.Err() != nil {
			j.log.Infof("download stopped")
			break
		}

		// Drain errCh.
		var lastErr error
		for {
			select {
			case e := <-errCh:
				lastErr = e
			default:
			}
			break
		}

		if lastErr == nil {
			j.log.Infof("download completed  %d bytes", bytesDone.Load())
			break
		}

		// Reduce chunk count.
		j.mu.Lock()
		currentCount := j.chunkCount
		j.mu.Unlock()

		if currentCount <= 1 {
			j.log.Warnf("all chunks failed, giving up: %v", lastErr)
			return lastErr
		}

		newCount := currentCount / 2
		if newCount < 1 {
			newCount = 1
		}
		j.log.Warnf("reducing chunks %d -> %d  last error: %v", currentCount, newCount, lastErr)

		j.mu.Lock()
		j.chunkCount = newCount
		j.mu.Unlock()

		if j.opts.OnChunkCountDecreased != nil {
			j.opts.OnChunkCountDecreased(newCount)
		}

		// Re-plan: collect progress and redistribute.
		j.mu.Lock()
		progress := make([]int64, len(j.chunks))
		for i, c := range j.chunks {
			progress[i] = atomic.LoadInt64(&c.progress) - c.start
		}
		j.opts.ResumeFrom = progress
		j.plan()
		j.mu.Unlock()

		j.log.Infof("re-planned with %d chunks  backing off 1s before retry", newCount)
		select {
		case <-ctx.Done():
			return lastErr
		case <-time.After(1 * time.Second):
		}
	}

	j.log.Infof("download finished  bytesDone=%d", bytesDone.Load())
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
	stat, err := j.dest.Stat()
	if err != nil {
		return err
	}
	if stat.Size() < j.total {
		return fmt.Errorf("engine: dest file size %d < expected total %d", stat.Size(), j.total)
	}
	return nil
}

// TotalSize returns the planned total byte count.
func (j *Job) TotalSize() int64 { return j.total }

// Close releases the driver resources.
func (j *Job) Close() error { return j.driver.Close() }
