// Package scheduler 端到端编排下载任务。它持有 taskID -> 运行中 job 的映射,
// 并提供 Add/Start/Pause/Resume/Cancel 控制接口。store 层负责持久化;
// engine 层负责实际的文件与网络 I/O。
//
// 刷新策略(flush policy):
//   - 每个运行中的 job 大约每 250ms 上报一次 Progress;scheduler 会对这些
//     上报进行批处理,并每 FlushInterval 秒(默认 2s)调用一次
//     store.UpdateTaskProgress。
//   - 生命周期事件(Start/Pause/Resume/Cancel/Complete/Fail)调用
//     flushImmediate 以同步方式写入。
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"lgo_download_manager/internal/engine"
	"lgo_download_manager/internal/prealloc"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/store"
)

const FlushInterval = 2 * time.Second

// Scheduler 是面向用户的下载编排器。
type Scheduler struct {
	st *store.Store

	mu   sync.Mutex
	jobs map[string]*runningJob

	// wg 跟踪所有由 startAsync 启动的 per-job goroutine。PauseAll 通过
	// wg.Wait() 确保每个 job 都已落盘 Paused 后再返回,避免"已 cancel 但
	// DB 仍是 Downloading"的窗口。
	wg sync.WaitGroup

	// 事件订阅者(GUI)。筛选逻辑在消费侧完成。
	Subs []chan Event
}

type runningJob struct {
	task         *store.Task
	cancel       context.CancelFunc
	// prepareCancel 用于在 Start 预留 slot 之后、engine goroutine 启动之前
	// 的「准备阶段」（probe / 预分配）取消任务。该阶段 rj.cancel 仍为 nil，
	// 因此 Pause/PauseAll/Cancel/Delete 必须同时调用 prepareCancel,否则
	// 准备中的任务无法被中止,只能等其跑完探测后变成 Downloading 才能停。
	prepareCtx    context.Context
	prepareCancel context.CancelFunc
	job          *engine.Job
	dirty        bool       // 需要刷新
	dirtyMu      sync.Mutex // 保护 dirty、progressToFlush 和 statusToFlush
	stopped      bool
	lastSnapshot engine.Progress // 用于追赶式刷新

	status          store.Status
	progressToFlush engine.Progress
	errMsg          string
}

// New 构造一个由给定 Store 支撑的 Scheduler。
func New(s *store.Store) *Scheduler {
	return &Scheduler{
		st:   s,
		jobs: map[string]*runningJob{},
	}
}

// Event 会被发送给订阅者。Why 表示事件触发的原因。
type Event struct {
	Why      string      // "added"、"started"、"paused"、"resumed"、"completed"、"failed"、"progress" 之一
	Task     *store.Task // snapshot at event time (cloned)
	SpeedBPS float64     // 最近测得的瞬时速度;仅对 "progress" 事件有意义
}

// AddTaskInput 是调用方在创建新任务时传入的参数。
type AddTaskInput struct {
	URL          string
	SavePath     string
	Protocol     protocol.ProtocolKind
	Auth         protocol.AuthOptions
	ChunkCount   int
	MinChunkSize int64 // 字节;<=0 时回退为 engine 默认值(1 MiB)
}

// Add 在 store 中记录一个新任务并返回其自动生成的 ID。
// 它并不会启动下载——如需启动请调用 Start。
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
	if in.MinChunkSize <= 0 {
		in.MinChunkSize = 1 << 20 // 1 MiB;与 engine 的默认值保持一致
	}
	id := newID()
	authBlob, _ := json.Marshal(in.Auth)
	tk := &store.Task{
		ID:            id,
		URL:           in.URL,
		SavePath:      in.SavePath,
		Protocol:      string(in.Protocol),
		ChunkCount:    in.ChunkCount,
		MinChunkSize:  in.MinChunkSize,
		Status:        store.TaskStatus.Pending,
		ChunkProgress: make([]int64, in.ChunkCount),
		AuthData:      string(authBlob),
	}
	if err := s.st.CreateTask(tk); err != nil {
		return nil, err
	}
	s.publish(Event{Why: "added", Task: tk.Clone()})
	return tk, nil
}

// Start 启动(或恢复)一个任务。如果任务在之前的运行中已经存在 chunk_progress,
// engine 将从这些偏移位置继续下载。
//
// Start 启动(或恢复)一个任务。如果任务在之前的运行中已经存在 chunk_progress,
// engine 将从这些偏移位置继续下载。
//
// Start 立即返回 nil，不等待服务器探测（HEAD 请求）或预分配完成。
// 任何错误都会被写入 store.Task.Status（Failed）并通过事件总线发布
// （Event{Why: "failed"}），UI 可订阅该事件并展示给用户。
//
// 由于探测在后台 goroutine 中进行,调用方可以在 UI 线程上安全地调用 Start:
// 不可达的服务器或超大的文件预分配都不会阻塞 UI。
func (s *Scheduler) Start(taskID string) error {
	s.mu.Lock()
	if _, ok := s.jobs[taskID]; ok {
		s.mu.Unlock()
		return errors.New("scheduler: task already running")
	}
	// 立即预留 slot,携带 prepareCtx/prepareCancel 以便 Pause/PauseAll
	// 在 probe/预分配阶段也能中止任务;同时把 wg.Add 提到这里,
	// 让 PauseAll 的 wg.Wait() 覆盖「准备阶段」,不会过早返回。
	prepareCtx, prepareCancel := context.WithCancel(context.Background())
	s.jobs[taskID] = &runningJob{
		prepareCtx:    prepareCtx,
		prepareCancel: prepareCancel,
	}
	s.wg.Add(1)
	s.mu.Unlock()

	tk, err := s.st.GetTask(taskID)
	if err != nil {
		s.releaseSlot(taskID)
		s.wg.Done()
		return fmt.Errorf("scheduler: %w", err)
	}

	// 文件丢失状态:重置进度再继续
	if tk.Status == store.TaskStatus.FileLost {
		_ = s.st.UpdateTaskProgress(tk.ID, 0, nil, store.TaskStatus.Pending, "")
		tk, _ = s.st.GetTask(taskID)
	}

	go s.startAsync(taskID, tk)
	return nil
}

// releaseSlot 释放预留的 slot；用于 Start 的快速失败路径（探测/构造尚未开始）。
func (s *Scheduler) releaseSlot(taskID string) {
	s.mu.Lock()
	delete(s.jobs, taskID)
	s.mu.Unlock()
}

// startAsync 在独立 goroutine 中执行 Start 的慢路径：
// 服务器探测 → 元数据持久化 → 文件预分配 → 启动 engine.Run。
// 任何阶段失败都会把任务标记为 Failed 并发布失败事件，由 UI 监听器显示。
func (s *Scheduler) startAsync(taskID string, tk *store.Task) {
	defer s.wg.Done()
	// 拿到本任务的 prepareCtx,后续所有阻塞调用都挂到它下面,确保
	// Pause/PauseAll 触发的 prepareCancel 能立即中断 probe/预分配。
	s.mu.Lock()
	rj := s.jobs[taskID]
	s.mu.Unlock()
	prepareCtx := context.Background()
	if rj != nil && rj.prepareCtx != nil {
		prepareCtx = rj.prepareCtx
	}
	// 若 startAsync 一进入就已被取消（例如 Pause 在 Start 返回后立刻触发），
	// 直接走「准备阶段被取消」的快速路径，避免无谓的 probe。
	if s.prepareCancelled(taskID) {
		s.abortPrepare(taskID, tk)
		return
	}
	auth := decodeAuth(tk.AuthData)
	driver, err := protocol.New(tk.URL, protocol.ProtocolKind(tk.Protocol), protocol.Auth{AuthOptions: auth})
	if err != nil {
		s.fail(tk, err)
		s.releaseSlot(taskID)
		return
	}

	probeCtx, probeCancel := context.WithTimeout(prepareCtx, 30*time.Second)
	caps, err := driver.Probe(probeCtx)
	probeCancel()
	if s.prepareCancelled(taskID) {
		_ = driver.Close()
		s.abortPrepare(taskID, tk)
		return
	}
	if err != nil {
		_ = driver.Close()
		s.fail(tk, err)
		s.releaseSlot(taskID)
		return
	}
// 准备阶段失败统一经过 fail()/releaseSlot,此处继续走预分配/启动 engine 流程。
	if caps.TotalSize > 0 {
		if err := s.st.UpdateTaskMeta(tk.ID, caps.TotalSize, caps.SupportRange, tk.IsAllocated, tk.ChunkCount); err != nil {
			_ = driver.Close()
			s.fail(tk, err)
			s.releaseSlot(taskID)
			return
		}
		tk.TotalSize = caps.TotalSize
		tk.SupportRange = caps.SupportRange
	}

	// 若尚未预分配文件则进行预分配(恢复场景:文件已具有正确大小)。
	// 若文件不存在（被删除）则强制重新预分配，即使 IsAllocated 为 true
	if _, statErr := os.Stat(tk.SavePath); statErr != nil {
		tk.IsAllocated = false
	}
	needAlloc := !tk.IsAllocated && caps.TotalSize > 0
	if needAlloc {
		dest, err := prealloc.Preallocate(tk.SavePath, caps.TotalSize)
		if err != nil {
			_ = driver.Close()
			s.fail(tk, err)
			s.releaseSlot(taskID)
			return
		}
		if err := s.st.UpdateTaskMeta(tk.ID, caps.TotalSize, caps.SupportRange, true, tk.ChunkCount); err != nil {
			dest.Close()
			_ = driver.Close()
			s.fail(tk, err)
			s.releaseSlot(taskID)
			return
		}
		tk.IsAllocated = true
		// 重新打开供 engine 使用(文件已具有正确大小)。
		dest.Close()
}
if s.prepareCancelled(taskID) {
	_ = driver.Close()
	s.abortPrepare(taskID, tk)
	return
}
dest, err := os.OpenFile(tk.SavePath, os.O_RDWR, 0o644)
	if err != nil {
		_ = driver.Close()
		s.fail(tk, err)
		s.releaseSlot(taskID)
		return
	}
	// 恢复偏移取自 chunk_progress(每个值表示对应 chunk 已写入的字节数)。
	resume := make([]int64, len(tk.ChunkProgress))
	copy(resume, tk.ChunkProgress)

	job := engine.NewJob(driver, caps.TotalSize, dest, engine.Options{
		ChunkCount:    tk.ChunkCount,
		MinChunkSize:  tk.MinChunkSize, // 0 表示使用 engine 的默认值
		ResumeFrom:    resume,
		ProgressEvery: 250 * time.Millisecond,
		Progress: func(p engine.Progress) {
			s.markProgress(tk.ID, p)
		},
		TaskID: tk.ID,
		OnPlanChanged: func(ranges []int64) {
			tk.ChunkRanges = ranges
			if err := s.st.UpdateTaskChunkRanges(tk.ID, ranges); err != nil {
				log.Printf("scheduler: persist chunk ranges: %v", err)
			}
		},
	})
	// 记录 engine 实际生效的 chunk 切分(取决于 MinChunkSize 和
	// 文件总大小,而不仅仅是 ChunkCount),以便 UI 能基于真实字节
	// 范围绘制 chunk 进度图。
	cs := job.Chunks()
	ranges := make([]int64, 0, len(cs)*2)
	for _, c := range cs {
		ranges = append(ranges, c.Start, c.End)
	}
	tk.ChunkRanges = ranges
	if err := s.st.UpdateTaskChunkRanges(tk.ID, ranges); err != nil {
		log.Printf("scheduler: persist chunk ranges: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	newRJ := &runningJob{
		task:   tk,
		cancel: cancel,
		job:    job,
		status: store.TaskStatus.Downloading,
	}
	s.mu.Lock()
	// 替换之前 Start 预留的空 slot。
	s.jobs[tk.ID] = newRJ
	s.mu.Unlock()

	if err := s.st.UpdateTaskProgress(tk.ID, sumInts(tk.ChunkProgress), tk.ChunkProgress, store.TaskStatus.Downloading, ""); err != nil {
		// 非致命错误——稍后的批量刷新会追赶上来。
	}
	s.publish(Event{Why: "started", Task: tk.Clone()})

	// 在 goroutine 内同步运行 job;该 goroutine 会一直存活到
	// 任务完成或被取消。
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// rjRef 在整个 goroutine 生命周期内持有运行中 job 的引用,
		// 以便状态/收尾调用能读取最新的 ChunkProgress。
		rjRef := newRJ
		defer func() {
			// 退出时进行清理(reap)。
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
			s.markStatusFromJob(tk.ID, store.TaskStatus.Paused, rjRef)
			return
		}
		s.completeFromEngine(tk.ID, rjRef)
	}()
}

// ReclaimDownloadingTasks 把上次进程异常退出后残留的 Downloading 任务
// 全部重置为 Paused, 保留 chunk_progress 与 downloaded 字节数,
// 以便用户后续手动「恢复」即可续传。
//
// 必须在任何 Start 之前调用一次(冷启动路径),否则 UI 会把这些任务显示
// 为「下载中」却永远没有进度。
func (s *Scheduler) ReclaimDownloadingTasks() error {
	tasks, err := s.st.ListTasks(store.FilterDownloading, store.SortCreatedDesc)
	if err != nil {
		return err
	}
	for _, tk := range tasks {
		if err := s.st.UpdateTaskProgress(tk.ID, tk.Downloaded, tk.ChunkProgress, store.TaskStatus.Paused, ""); err != nil {
			return err
		}
		latest, err := s.st.GetTask(tk.ID)
		if err != nil {
			return err
		}
		s.publish(Event{Why: "paused", Task: latest})
	}
	return nil
}

// PauseAll 优雅取消所有正在运行的 job, 并阻塞至每个 job 都已落盘 Paused
//
// ctx 用于控制最大阻塞时间——若 ctx 被取消(典型用法是 5s 超时),函数立即
// 返回 ctx.Err();此时部分 job 仍未退出,DB 中可能仍残留 Downloading,
// 但这些任务会由下次冷启动的 ReclaimDownloadingTasks 兜底。
//
// 准备阶段的 slot(Start 已 reserve 但 startAsync 尚未替换为带 cancel 的
// runningJob)同样会被取消:同时调用 prepareCancel 与 cancel,并由
// startAsync 的检查点把状态落盘为 Paused 后通过事件总线通知 UI。
func (s *Scheduler) PauseAll(ctx context.Context) error {
	s.mu.Lock()
	jobs := make([]*runningJob, 0, len(s.jobs))
	for _, rj := range s.jobs {
		jobs = append(jobs, rj)
	}
	s.mu.Unlock()

	for _, rj := range jobs {
		if rj.prepareCancel != nil {
			rj.prepareCancel()
		}
		if rj.cancel != nil {
			rj.cancel()
		}
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Scheduler) Pause(taskID string) error {
	s.mu.Lock()
	rj, ok := s.jobs[taskID]
	s.mu.Unlock()
	if !ok {
		return errors.New("scheduler: task not running")
	}
	if rj.prepareCancel != nil {
		rj.prepareCancel()
	}
	if rj.cancel != nil {
		rj.cancel()
	}
	return nil
}

// Cancel 暂停任务并删除其部分文件。store 中的行会被保留,
// status 置为 Failed,供事后查看。
func (s *Scheduler) Cancel(taskID string) error {
	s.mu.Lock()
	rj, ok := s.jobs[taskID]
	s.mu.Unlock()
	if ok {
		if rj.prepareCancel != nil {
			rj.prepareCancel()
		}
		if rj.cancel != nil {
			rj.cancel()
		}
	}
	tk, err := s.st.GetTask(taskID)
	if err != nil {
		return err
	}
	if tk.SavePath != "" {
		_ = os.Remove(tk.SavePath)
	}
	if err := s.markStatus(tk.ID, store.TaskStatus.Failed); err != nil {
		return err
	}
	return nil
}
// Delete 取消一个运行中的任务(若有),删除其部分文件,
// 并从 store 中永久移除该行。
func (s *Scheduler) Delete(taskID string) error {
	s.mu.Lock()
	rj, ok := s.jobs[taskID]
	s.mu.Unlock()
	if ok {
		if rj.prepareCancel != nil {
			rj.prepareCancel()
		}
		if rj.cancel != nil {
			rj.cancel()
		}
	}
	tk, err := s.st.GetTask(taskID)
	if err == nil && tk != nil && tk.SavePath != "" {
		_ = os.Remove(tk.SavePath)
	}
	if err := s.st.DeleteTask(taskID); err != nil {
		return err
	}
	s.publish(Event{Why: "deleted", Task: nil})
	return nil
}

// UI 侧边栏应当传 StoreFilterAll 或其它 StatusFilter 来获取子集。
func (s *Scheduler) List(filter store.StatusFilter, sort store.TaskSort) ([]*store.Task, error) {
	return s.st.ListTasks(filter, sort)
}

// ValidateFileExistence 检查所有已完成和下载中任务的文件是否存在，
// 不存在则将状态更新为 FileLost。用于窗口重新聚焦时的文件完整性检查。
func (s *Scheduler) ValidateFileExistence() {
	tasks, err := s.st.ListTasks(store.FilterAll, store.SortCreatedDesc)
	if err != nil {
		return
	}
	for _, tk := range tasks {
		if tk.Status != store.TaskStatus.Completed && tk.Status != store.TaskStatus.Downloading {
			continue
		}
		if tk.SavePath == "" {
			continue
		}
		if _, err := os.Stat(tk.SavePath); os.IsNotExist(err) {
			_ = s.st.UpdateTaskProgress(tk.ID, tk.Downloaded, tk.ChunkProgress, store.TaskStatus.FileLost, "文件已丢失")
			s.publish(Event{Why: "updated", Task: tk})
		}
	}
}

// ResetTask 将任务进度清零、状态重置为 Pending，用于文件丢失后重新下载。
func (s *Scheduler) ResetTask(taskID string) error {
	tk, err := s.st.GetTask(taskID)
	if err != nil {
		return err
	}
	if err := s.st.UpdateTaskProgress(tk.ID, 0, nil, store.TaskStatus.Pending, ""); err != nil {
		return err
	}
	// 重新获取最新状态的任务用于发布事件
	tk, _ = s.st.GetTask(taskID)
	s.publish(Event{Why: "updated", Task: tk})
	return nil
}

// Subscribe 返回一个事件通道。返回的 func 被调用时取消订阅。
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

// publish 向所有订阅者发送事件;通道已满时丢弃。
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

// markProgress 是 engine 进度回调的节流安全(throttle-safe)入口。
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
	rj.status = store.TaskStatus.Downloading

	// 将 engine 的字节计数同步到任务快照中,使 Event 能把最新的
	// Downloaded、各 chunk 偏移以及 status 传递给 UI 和 store。
	rj.task.Downloaded = p.DownloadedBytes
	rj.task.Status = store.TaskStatus.Downloading
	// 将 engine 的每个 chunk 偏移回写到 task,这样下次 flushAll
	// 之前若发生快速 Pause,也能通过 rj.task 看到最新值。
	cs := rj.job.Chunks()
	if len(cs) > 0 {
		cp := make([]int64, len(cs))
		for i, c := range cs {
			cp[i] = c.Progress - c.Start
			if cp[i] < 0 {
				cp[i] = 0
			}
		}
		rj.task.ChunkProgress = cp
	}
	rj.dirtyMu.Unlock()
	s.publish(Event{Why: "progress", Task: rj.task.Clone(), SpeedBPS: p.SpeedBPS})
}

// markStatus(Paused/Completed/Failed)把最新的内存任务状态写入
// store,并发布一条状态事件。rj 为可选参数;非 nil 时表示一个
// 仍然存活的 runningJob,用于读取最新的 ChunkProgress/Downloaded。
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
		// 优先使用 engine 的最新 chunk 偏移,而不是过时的任务快照。
		cs := rj.job.Chunks()
		if len(cs) > 0 {
			chunkProg = make([]int64, len(cs))
			for i, c := range cs {
				chunkProg[i] = c.Progress - c.Start
				if chunkProg[i] < 0 {
					chunkProg[i] = 0
				}
			}
		} else {
			chunkProg = append([]int64(nil), rj.task.ChunkProgress...)
		}
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

// markStatusFromJob 使用一个已被从 s.jobs 移除但在 goroutine 中
// 仍然存活的 runningJob 引用来写入状态。这样可以避免在 Pause/Fail
// 时丢失最新的字节进度。
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

// completeFromEngine 把 engine 权威状态中的每个 chunk 偏移和总字节数
// 刷新出去,然后将任务标记为 Completed。
func (s *Scheduler) completeFromEngine(taskID string, rj *runningJob) {
	if rj == nil {
		s.mu.Lock()
		rj, _ = s.jobs[taskID]
		s.mu.Unlock()
	}
	if rj == nil {
		s.markStatus(taskID, store.TaskStatus.Completed)
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
	if err := s.st.UpdateTaskProgress(taskID, total, progress, store.TaskStatus.Completed, ""); err != nil {
		fmt.Fprintln(os.Stderr, "scheduler completeFromEngine:", err)
	}
	rj.task.ChunkProgress = progress
	rj.task.Downloaded = total
	if tk, err := s.st.GetTask(taskID); err == nil {
		s.publish(Event{Why: "completed", Task: tk})
	}
}

func (s *Scheduler) fail(tk *store.Task, err error) {
	if writeErr := s.st.UpdateTaskProgress(tk.ID, tk.Downloaded, tk.ChunkProgress, store.TaskStatus.Failed, err.Error()); writeErr != nil {
		fmt.Fprintln(os.Stderr, "scheduler fail:", writeErr)
	}
	if tk2, err := s.st.GetTask(tk.ID); err == nil {
		s.publish(Event{Why: "failed", Task: tk2})
	}
}

func statusWhy(s store.Status) string {
	switch s {
	case store.TaskStatus.Paused:
		return "paused"
	case store.TaskStatus.Completed:
		return "completed"
	case store.TaskStatus.Failed:
		return "failed"
	default:
		return "progress"
	}
}

// prepareCancelled 在 startAsync 的关键检查点（probe 前/后、预分配后、
// 启动 engine 之前）报告 prepare 阶段是否已被 Pause/Cancel/Delete 取消。
// 仅看 prepareCtx,不看 rj.cancel:后者在准备阶段仍为 nil。
func (s *Scheduler) prepareCancelled(taskID string) bool {
	s.mu.Lock()
	rj, ok := s.jobs[taskID]
	s.mu.Unlock()
	if !ok || rj == nil || rj.prepareCtx == nil {
		return false
	}
	return rj.prepareCtx.Err() != nil
}

// abortPrepare 处理「准备阶段被取消」的快速收尾:
//   - 释放 slot
//   - 把任务持久化为 Paused(用户视角:刚点完暂停,任务应当立刻可恢复)
//   - 发布 Event{Why: "paused"} 通知 UI
//
// 调用方须在调用前已自行关闭任何已打开的 driver/dest 资源。
func (s *Scheduler) abortPrepare(taskID string, tk *store.Task) {
	s.releaseSlot(taskID)
	if writeErr := s.st.UpdateTaskProgress(taskID, tk.Downloaded, tk.ChunkProgress, store.TaskStatus.Paused, ""); writeErr != nil {
		fmt.Fprintln(os.Stderr, "scheduler abortPrepare:", writeErr)
	}
	if latest, err := s.st.GetTask(taskID); err == nil {
		s.publish(Event{Why: "paused", Task: latest})
	}
}

// IsPreparing 报告指定任务当前是否处于「准备阶段」(Start 已预留 slot,
// 但 engine 还未接管)。此时 rj.cancel 仍为 nil,Pause 必须通过
// prepareCancel 才能中止。
func (s *Scheduler) IsPreparing(taskID string) bool {
	s.mu.Lock()
	rj, ok := s.jobs[taskID]
	s.mu.Unlock()
	if !ok || rj == nil {
		return false
	}
	return rj.cancel == nil && rj.prepareCancel != nil
}
// Run 启动批量刷新 goroutine。请在启动时调用一次;它会一直运行
// 直到 ctx 被取消。
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
		// 将 chunk 快照映射回“相对于 chunk 起始的偏移”。
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
			// 标记为 dirty,以便下一个周期重试。
			rj.dirtyMu.Lock()
			rj.dirty = true
			rj.dirtyMu.Unlock()
			continue
		}
		rj.task.ChunkProgress = progress
		rj.task.Downloaded = p.DownloadedBytes
	}
}

// decodeAuth 从存储的 blob 中重建 protocol.AuthOptions。
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

// newID 基于毫秒级时间戳生成 ID,并附加一个小的随机后缀以保证
// 快速连续添加时的唯一性。
func newID() string {
	return fmt.Sprintf("ts-%d-%d", time.Now().UnixMilli(), os.Getpid())
}

// EnsureSaveDir 确保本地路径的父目录存在。由 UI 调用。
func EnsureSaveDir(p string) error {
	return os.MkdirAll(filepath.Dir(p), 0o755)
}
