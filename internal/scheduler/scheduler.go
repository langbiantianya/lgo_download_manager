// Package scheduler orchestrates download tasks end-to-end. It owns the
// mapping taskID -> running job and provides Add/Start/Pause/Resume/Cancel
// controls. The store layer is the persistence side; the engine does the
// actual file/network work.
//
// Flushing policy:
//   - Each running job reports Progress ~250ms; scheduler batches these
//     and calls store.UpdateTaskProgress every FlushInterval seconds
//     (default 2s).
//   - Lifecycle events (Start/Pause/Resume/Cancel/Complete/Fail) call
//     flushImmediate which writes synchronously.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"lgo_download_manager/internal/engine"
	"lgo_download_manager/internal/prealloc"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/store"
)

// FlushInterval matches the design doc (2s).
const FlushInterval = 2 * time.Second

// Scheduler is the user-facing orchestrator.
type Scheduler struct {
	st *store.Store

	mu   sync.Mutex
	jobs map[string]*runningJob

	// Event subscribers (GUI). Filtering happens in the consumer.
	Subs []chan Event
}

type runningJob struct {
	task    *store.Task
	cancel  context.CancelFunc
	job     *engine.Job
	dirty   bool        // needs flush
	dirtyMu sync.Mutex  // protects dirty + progressToFlush + statusToFlush
	stopped bool
	lastSnapshot engine.Progress // for catch-up flushes

	status        store.Status
	progressToFlush engine.Progress
	errMsg         string
}

// New constructs a Scheduler backed by the given Store.
func New(s *store.Store) *Scheduler {
	return &Scheduler{
		st:   s,
		jobs: map[string]*runningJob{},
	}
}

// Event is sent to subscribers. Why = why the event fired.
type Event struct {
	Why      string      // "added", "started", "paused", "resumed", "completed", "failed", "progress"
	Task     *store.Task // snapshot at event time (cloned)
	SpeedBPS float64     // latest measured speed; only meaningful for "progress"
}

// AddTaskInput is what callers pass when they create a new task.
type AddTaskInput struct {
	URL          string
	SavePath     string
	Protocol     protocol.ProtocolKind
	Auth         protocol.AuthOptions
	ChunkCount   int
}

// Add records a new task in the store and returns its generated ID.
// It does NOT start the download — call Start for that.
func (s *Scheduler) Add(in AddTaskInput) (*store.Task, error) {
	if in.URL == "" {
		return nil, errors.New("scheduler: empty url")
	}
	if in.SavePath == "" {
		return nil, errors.New("scheduler: empty save path")
	}
	if in.ChunkCount <= 0 {
		in.ChunkCount = 4
	}
	id := newID()
	authBlob, _ := json.Marshal(in.Auth)
	tk := &store.Task{
		ID:            id,
		URL:           in.URL,
		SavePath:      in.SavePath,
		Protocol:      string(in.Protocol),
		ChunkCount:    in.ChunkCount,
		Status:        store.StatusPending,
		ChunkProgress: make([]int64, in.ChunkCount),
		AuthData:      string(authBlob),
	}
	if err := s.st.CreateTask(tk); err != nil {
		return nil, err
	}
	s.publish(Event{Why: "added", Task: tk.Clone()})
	return tk, nil
}

// Start begins (or resumes) a task. If the task has chunk_progress already
// from a prior run, the engine picks up at those offsets.
func (s *Scheduler) Start(taskID string) error {
	s.mu.Lock()
	if _, ok := s.jobs[taskID]; ok {
		s.mu.Unlock()
		return errors.New("scheduler: task already running")
	}
	s.mu.Unlock()

	tk, err := s.st.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("scheduler: %w", err)
	}

	// Probe the server to discover capabilities.
	auth := decodeAuth(tk.AuthData)
	driver, err := protocol.New(tk.URL, protocol.ProtocolKind(tk.Protocol), protocol.Auth{AuthOptions: auth})
	if err != nil {
		return err
	}
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	caps, err := driver.Probe(probeCtx)
	probeCancel()
	if err != nil {
		_ = driver.Close()
		s.fail(tk, err)
		return err
	}
	if caps.TotalSize > 0 {
		if err := s.st.UpdateTaskMeta(tk.ID, caps.TotalSize, caps.SupportRange, tk.IsAllocated, tk.ChunkCount); err != nil {
			_ = driver.Close()
			return err
		}
		tk.TotalSize = caps.TotalSize
		tk.SupportRange = caps.SupportRange
	}

	// Preallocate file if not already (Resume case: file is already sized).
	needAlloc := !tk.IsAllocated && caps.TotalSize > 0
	if needAlloc {
		dest, err := prealloc.Preallocate(tk.SavePath, caps.TotalSize)
		if err != nil {
			_ = driver.Close()
			s.fail(tk, err)
			return err
		}
		if err := s.st.UpdateTaskMeta(tk.ID, caps.TotalSize, caps.SupportRange, true, tk.ChunkCount); err != nil {
			dest.Close()
			_ = driver.Close()
			return err
		}
		tk.IsAllocated = true
		// Reopen for engine (file already at correct size).
		dest.Close()
	}
	dest, err := os.OpenFile(tk.SavePath, os.O_RDWR, 0o644)
	if err != nil {
		_ = driver.Close()
		s.fail(tk, err)
		return err
	}

	// Resume offsets come from chunk_progress (each = bytes already
	// written into that chunk).
	resume := make([]int64, len(tk.ChunkProgress))
	copy(resume, tk.ChunkProgress)

	job := engine.NewJob(driver, caps.TotalSize, dest, engine.Options{
		ChunkCount:    tk.ChunkCount,
		ResumeFrom:    resume,
		ProgressEvery: 250 * time.Millisecond,
		Progress: func(p engine.Progress) {
			s.markProgress(tk.ID, p)
		},
		TaskID: tk.ID,
	})

	ctx, cancel := context.WithCancel(context.Background())
	rj := &runningJob{
		task:    tk,
		cancel:  cancel,
		job:     job,
		status:  store.StatusDownloading,
	}
	s.mu.Lock()
	s.jobs[tk.ID] = rj
	s.mu.Unlock()

	if err := s.st.UpdateTaskProgress(tk.ID, sumInts(tk.ChunkProgress), tk.ChunkProgress, store.StatusDownloading, ""); err != nil {
		// Non-fatal — biler flush will catch up.
	}
	s.publish(Event{Why: "started", Task: tk.Clone()})


	// Run the job synchronously inside a goroutine; the goroutine stays
	// until completion or cancel.
	go func() {
		// rjRef keeps a reference to the running job for the lifetime of
		// the goroutine so status/finalise calls can read live ChunkProgress.
		rjRef := rj
		defer func() {
			// Reap on exit.
			s.mu.Lock()
			delete(s.jobs, tk.ID)
			s.mu.Unlock()
			_ = dest.Close()
		}()
		err := job.Run(ctx, caps.SupportRange && caps.TotalSize > 0)
		if err != nil && ctx.Err() == nil {
			s.fail(tk, err)
			return
		}
		if ctx.Err() != nil {
			s.markStatusFromJob(tk.ID, store.StatusPaused, rjRef)
			return
		}
		s.completeFromEngine(tk.ID, rjRef)
	}()
	return nil
}

// Pause cancels a running task. The task remains in the store with
// progress and can be Resumed.
func (s *Scheduler) Pause(taskID string) error {
	s.mu.Lock()
	rj, ok := s.jobs[taskID]
	s.mu.Unlock()
	if !ok {
		return errors.New("scheduler: task not running")
	}
	rj.cancel()
	return nil
}

// Cancel pauses and removes the partial file. The store row is kept with
// status=Failed for inspection.
func (s *Scheduler) Cancel(taskID string) error {
	s.mu.Lock()
	rj, ok := s.jobs[taskID]
	s.mu.Unlock()
	if ok {
		rj.cancel()
	}
	tk, err := s.st.GetTask(taskID)
	if err != nil {
		return err
	}
	if tk.SavePath != "" {
		_ = os.Remove(tk.SavePath)
	}
	if err := s.markStatus(tk.ID, store.StatusFailed); err != nil {
		return err
	}
	return nil
}

// List returns all known tasks from the store. The store is the source
// of truth for the UI's sidebar.
func (s *Scheduler) List() ([]*store.Task, error) { return s.st.ListTasks() }

// Subscribe returns a channel of events. The returned func, when called,
// unsubscribes.
func (s *Scheduler) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	s.mu.Lock()
	s.Subs = append(s.Subs, ch)
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, c := range s.Subs {
			if c == ch {
				s.Subs = append(s.Subs[:i], s.Subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
}

// publish sends to all subscribers, drop on full.
func (s *Scheduler) publish(ev Event) {
	s.mu.Lock()
	subs := append([]chan Event(nil), s.Subs...)
	s.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// markProgress is the throttle-safe path for engine progress callbacks.
func (s *Scheduler) markProgress(taskID string, p engine.Progress) {
	s.mu.Lock()
	rj, ok := s.jobs[taskID]
	s.mu.Unlock()
	if !ok {
		return
	}
	rj.dirtyMu.Lock()
	rj.dirty = true
	rj.lastSnapshot = p
	rj.status = store.StatusDownloading

	// Sync engine's byte counts into the task snapshot so the Event carries
	// up-to-date Downloaded + chunk offsets + status to the UI and store.
	rj.task.Downloaded = p.DownloadedBytes
	rj.task.Status = store.StatusDownloading
	if len(rj.task.ChunkProgress) == 0 && p.CompletedChunks > 0 {
		// Initialise chunk progress array from engine chunks.
		rj.task.ChunkProgress = make([]int64, p.CompletedChunks)
	}
	rj.dirtyMu.Unlock()
	s.publish(Event{Why: "progress", Task: rj.task.Clone(), SpeedBPS: p.SpeedBPS})
}

// markStatus (Paused/Completed/Failed) writes the latest in-memory task state
// to the store and publishes a status event. rj is optional; when non-nil it
// is the live runningJob used to read fresh ChunkProgress/Downloaded.
func (s *Scheduler) markStatus(taskID string, st store.Status) error {
	var downloaded int64
	var chunkProg []int64
	var errMsg string

	s.mu.Lock()
	rj, hasRJ := s.jobs[taskID]
	s.mu.Unlock()

	if hasRJ && rj != nil {
		rj.dirtyMu.Lock()
		downloaded = rj.task.Downloaded
		chunkProg = append([]int64(nil), rj.task.ChunkProgress...)
		errMsg = rj.task.ErrorMessage
		rj.dirtyMu.Unlock()
	} else {
		tk, err := s.st.GetTask(taskID)
		if err != nil {
			return err
		}
		downloaded = tk.Downloaded
		chunkProg = tk.ChunkProgress
		errMsg = tk.ErrorMessage
	}

	if err := s.st.UpdateTaskProgress(taskID, downloaded, chunkProg, st, errMsg); err != nil {
		return err
	}
	if tk2, err := s.st.GetTask(taskID); err == nil {
		s.publish(Event{Why: statusWhy(st), Task: tk2})
	}
	return nil
}

// markStatusFromJob writes a status using a runningJob reference that's been
// removed from s.jobs but is still alive in the goroutine. This avoids losing
// the latest bytes on Pause/Fail.
func (s *Scheduler) markStatusFromJob(taskID string, st store.Status, rj *runningJob) {
	if rj == nil {
		_ = s.markStatus(taskID, st)
		return
	}
	rj.dirtyMu.Lock()
	cs := rj.job.Chunks()
	progress := make([]int64, len(cs))
	for i, c := range cs {
		progress[i] = c.Progress - c.Start
		if progress[i] < 0 {
			progress[i] = 0
		}
	}
	downloaded := rj.task.Downloaded
	errMsg := rj.task.ErrorMessage
	rj.dirtyMu.Unlock()

	if err := s.st.UpdateTaskProgress(taskID, downloaded, progress, st, errMsg); err != nil {
		fmt.Fprintln(os.Stderr, "scheduler markStatusFromJob:", err)
	}
	if tk2, err := s.st.GetTask(taskID); err == nil {
		s.publish(Event{Why: statusWhy(st), Task: tk2})
	}
}

// completeFromEngine flushes the final per-chunk offsets and total bytes
// from the engine's authoritative state, then marks the task Completed.
func (s *Scheduler) completeFromEngine(taskID string, rj *runningJob) {
	if rj == nil {
		s.mu.Lock()
		rj, _ = s.jobs[taskID]
		s.mu.Unlock()
	}
	if rj == nil {
		s.markStatus(taskID, store.StatusCompleted)
		return
	}
	cs := rj.job.Chunks()
	progress := make([]int64, len(cs))
	var total int64
	for i, c := range cs {
		progress[i] = c.Progress - c.Start
		if progress[i] < 0 {
			progress[i] = 0
		}
		total += progress[i]
	}
	if err := s.st.UpdateTaskProgress(taskID, total, progress, store.StatusCompleted, ""); err != nil {
		fmt.Fprintln(os.Stderr, "scheduler completeFromEngine:", err)
	}
	rj.task.ChunkProgress = progress
	rj.task.Downloaded = total
	if tk, err := s.st.GetTask(taskID); err == nil {
		s.publish(Event{Why: "completed", Task: tk})
	}
}

func (s *Scheduler) fail(tk *store.Task, err error) {
	if writeErr := s.st.UpdateTaskProgress(tk.ID, tk.Downloaded, tk.ChunkProgress, store.StatusFailed, err.Error()); writeErr != nil {
		fmt.Fprintln(os.Stderr, "scheduler fail:", writeErr)
	}
	if tk2, err := s.st.GetTask(tk.ID); err == nil {
		s.publish(Event{Why: "failed", Task: tk2})
	}
}

func statusWhy(s store.Status) string {
	switch s {
	case store.StatusPaused:
		return "paused"
	case store.StatusCompleted:
		return "completed"
	case store.StatusFailed:
		return "failed"
	default:
		return "progress"
	}
}

// Run starts the batched flusher goroutine. Call it once at startup; it
// runs until ctx is canceled.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.flushAll()
			return
		case <-t.C:
			s.flushAll()
		}
	}
}

func (s *Scheduler) flushAll() {
	s.mu.Lock()
	jobs := make([]*runningJob, 0, len(s.jobs))
	for _, rj := range s.jobs {
		jobs = append(jobs, rj)
	}
	s.mu.Unlock()

	for _, rj := range jobs {
		rj.dirtyMu.Lock()
		if !rj.dirty {
			rj.dirtyMu.Unlock()
			continue
		}
		p := rj.lastSnapshot
		status := rj.status
		rj.dirty = false
		rj.dirtyMu.Unlock()
		// Map chunk snapshot back to per-chunk offset-from-start.
		cs := rj.job.Chunks()
		progress := make([]int64, len(cs))
		for i, c := range cs {
			progress[i] = c.Progress - c.Start
			if progress[i] < 0 {
				progress[i] = 0
			}
		}
		if err := s.st.UpdateTaskProgress(rj.task.ID, p.DownloadedBytes, progress, status, ""); err != nil {
			fmt.Fprintln(os.Stderr, "flush:", err)
			// Mark dirty so we retry next interval.
			rj.dirtyMu.Lock()
			rj.dirty = true
			rj.dirtyMu.Unlock()
			continue
		}
		rj.task.ChunkProgress = progress
		rj.task.Downloaded = p.DownloadedBytes
	}
}

// decodeAuth reconstructs protocol.AuthOptions from a stored blob.
func decodeAuth(blob string) protocol.AuthOptions {
	var a protocol.AuthOptions
	if blob == "" {
		return a
	}
	_ = json.Unmarshal([]byte(blob), &a)
	return a
}

func sumInts(xs []int64) int64 {
	var s int64
	for _, x := range xs {
		s += x
	}
	return s
}

// newID makes a millisecond timestamp-based id, plus a small random
// suffix for uniqueness on rapid add.
func newID() string {
	return fmt.Sprintf("ts-%d-%d", time.Now().UnixMilli(), os.Getpid())
}

// EnsureSaveDir makes sure the local path's parent dir exists. Used by UI.
func EnsureSaveDir(p string) error {
	return os.MkdirAll(filepath.Dir(p), 0o755)
}
