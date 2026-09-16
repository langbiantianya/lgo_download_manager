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
	"sync/atomic"
	"time"

	"lgo_download_manager/internal/engine"
	"lgo_download_manager/internal/prealloc"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/store"
)

const FlushInterval = 2 * time.Second

// stopWait 是 Cancel/Delete 等待引擎写者停止的上限。超过后仍继续执行
// （删除文件/写字终态），因为继续等待会让 UI 卡住。
const stopWait = 5 * time.Second

// Scheduler 是面向用户的下载编排器。
type Scheduler struct {
	st *store.Store

	mu   sync.Mutex
	jobs map[string]*runningJob

	// maxConcurrent 是同时运行的最大任务数。Start 会把超出限额的新
	// 加入任务留在 Pending(等于「等待中」),直到有 slot 释放再 promote。
	// 0 表示尚未初始化;SetMaxConcurrent 负责套用默认值并触发 promote。
	maxConcurrent int

	// wg 跟踪所有由 startAsync 启动的 per-job goroutine。PauseAll 通过
	// wg.Wait() 确保每个 job 都已落盘 Paused 后再返回,避免"已 cancel 但
	// DB 仍是 Downloading"的窗口。
	wg sync.WaitGroup

	// 事件订阅者(GUI)。筛选逻辑在消费侧完成。
	Subs []chan Event
}

type runningJob struct {
	task *store.Task
	// cancel 取消 engine 的运行上下文；prepareCancel 用于在 Start 预留 slot
	// 之后、engine goroutine 启动之前的「准备阶段」（probe / 预分配）取消
	// 任务。该阶段 cancel 仍为 nil，因此 Pause/PauseAll/Cancel/Delete 必须
	// 同时调用 prepareCancel，否则准备中的任务无法被中止，只能等其跑完
	// 探测后变成 Downloading 才能停。
	prepareCtx    context.Context
	prepareCancel context.CancelFunc
	cancel        context.CancelFunc
	job           *engine.Job

	// dirtyMu 保护 task 的易变字段（Downloaded/ChunkProgress/Status/
	// ErrorMessage/ChunkRanges）以及 dirty/status/lastSnapshot。
	// 访问这些字段的三条路径——engine 的进度回调、2 秒刷盘 ticker、
	// Pause/Fail/Complete 收尾——都必须持锁。
	//
	// 锁序：持 dirtyMu 时禁止调用 engine 的任何方法（engine.Chunks() 会取
	// engine 自己的 mu，而 engine 回调 OnPlanChanged/Progress 时会反过来取
	// dirtyMu，形成 ABBA 死锁）。因此所有 Chunks() 调用都在加锁之前完成。
	dirtyMu      sync.Mutex
	dirty        bool
	status       store.Status
	lastSnapshot engine.Progress // 用于追赶式刷新

	// sess 是任务级会话：engine goroutine 彻底退出（槽位已释放、终态已
	// 落盘、文件句柄已关闭）后关闭其 done，Cancel/Delete 借此等待写者
	// 停止后再删文件/写终态。Start 预留的占位符与之后替换进来的
	// runningJob 共享同一个 sess。
	sess *jobSession
}

// jobSession 是任务级生命周期信号，由 runningJob 共享。
type jobSession struct {
	done chan struct{}
	once sync.Once
}

func newJobSession() *jobSession { return &jobSession{done: make(chan struct{})} }

// finish 关闭 done，多次调用只生效一次。
func (js *jobSession) finish() {
	if js == nil {
		return
	}
	js.once.Do(func() { close(js.done) })
}

// setError 记录失败原因，供 markStatusFromJob 落盘。
func (rj *runningJob) setError(err error) {
	if rj == nil || rj.task == nil || err == nil {
		return
	}
	rj.dirtyMu.Lock()
	rj.task.ErrorMessage = err.Error()
	rj.dirtyMu.Unlock()
}

// chunkProgressFrom 把引擎快照换算成「相对分片起点的偏移」数组。
func chunkProgressFrom(cs []engine.ChunkSnapshot) []int64 {
	if len(cs) == 0 {
		return nil
	}
	out := make([]int64, len(cs))
	for i, c := range cs {
		out[i] = c.Progress - c.Start
		if out[i] < 0 {
			out[i] = 0
		}
	}
	return out
}

// New 构造一个由给定 Store 支撑的 Scheduler。
func New(s *store.Store) *Scheduler {
	return &Scheduler{
		st:            s,
		jobs:          map[string]*runningJob{},
		maxConcurrent: store.DefaultMaxConcurrent,
	}
}

// SetMaxConcurrent 设置同时运行的最大任务数。n<=0 时套用默认值。
// 改动后会触发 promotePending 把超出限额期间累计的 Pending 任务按
// FIFO 顺序提升到 Downloading(只要当前有空闲 slot)。
func (s *Scheduler) SetMaxConcurrent(n int) {
	if n <= 0 {
		n = store.DefaultMaxConcurrent
	}
	s.mu.Lock()
	s.maxConcurrent = n
	s.mu.Unlock()
	s.promotePending()
}

// promotePending 把 store 里最早创建的 Pending 任务(尚未占 slot 的)
// 按 FIFO 顺序提升到 Downloading,直到达到 MaxConcurrent 上限或没有
// 候选任务为止。每个候选走一次 Start——Start 内部仍受并发上限保护,
// 因此本方法在并发调用下也是安全的。
//
// 候选集合由 SQL 层按 status 过滤后一次取回（此前是每个候选做一次
// 全表 ListTasks，在历史任务多时是 O(N²) 的扫描 + JSON 解码）。
func (s *Scheduler) promotePending() {
	s.mu.Lock()
	limit := s.maxConcurrent
	s.mu.Unlock()

	pending, err := s.st.ListTasks(store.FilterPending, store.SortCreatedAsc)
	if err != nil {
		return
	}
	for _, tk := range pending {
		s.mu.Lock()
		_, busy := s.jobs[tk.ID]
		full := len(s.jobs) >= limit
		s.mu.Unlock()
		if full {
			return
		}
		if busy {
			continue
		}
		// Start 自身会再次核对 cap；单个候选失败不阻断其余候选。
		if err := s.Start(tk.ID); err != nil {
			continue
		}
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
	// 先读任务（SQLite 查询 + JSON 解码）再取锁：不能持 s.mu 做 I/O，
	// 否则忙时（busy_timeout 最长 5s）整个调度器都会被卡住。
	tk0, err := s.st.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("scheduler: %w", err)
	}

	s.mu.Lock()
	if _, ok := s.jobs[taskID]; ok {
		s.mu.Unlock()
		return errors.New("scheduler: task already running")
	}
	// 并发上限检查:任务已经在引擎中跑时直接放行;Pending 任务
	// (新加入的)若当前 slots 已满,留在 Pending 不报错——调用方
	// (UI 对话框)无需特殊处理,等待 promotePending 自然提升即可。
	if tk0.Status == store.TaskStatus.Pending && len(s.jobs) >= s.maxConcurrent {
		s.mu.Unlock()
		// 静默排队:不分配 slot、不增 wg;后续有任务完成时由
		// promotePending 把它提升到 Downloading。
		return nil
	}
	// 立即预留 slot,携带 prepareCtx/prepareCancel 以便 Pause/PauseAll
	// 在 probe/预分配阶段也能中止任务;同时把 wg.Add 提到这里,
	// 让 PauseAll 的 wg.Wait() 覆盖「准备阶段」,不会过早返回。
	prepareCtx, prepareCancel := context.WithCancel(context.Background())
	s.jobs[taskID] = &runningJob{
		prepareCtx:    prepareCtx,
		prepareCancel: prepareCancel,
		sess:          newJobSession(),
	}
	s.wg.Add(1)
	s.mu.Unlock()

	// 文件丢失状态:重置进度再继续
	if tk0.Status == store.TaskStatus.FileLost {
		_ = s.st.UpdateTaskProgress(tk0.ID, 0, nil, store.TaskStatus.Pending, "")
		tk0, _ = s.st.GetTask(taskID)
	}

	go s.startAsync(taskID, tk0)
	return nil
}

// releaseSlot 释放预留的 slot；用于 Start 的快速失败路径（探测/构造尚未开始）。
// 任意一次 slot 释放都触发 FIFO 提升——既能消化积压的 Pending,
// 也能让「用户把 MaxConcurrent 调大」立即可见。
func (s *Scheduler) releaseSlot(taskID string) {
	s.mu.Lock()
	delete(s.jobs, taskID)
	s.mu.Unlock()
	s.promotePending()
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
	var sess *jobSession
	if rj != nil && rj.prepareCtx != nil {
		prepareCtx = rj.prepareCtx
	}
	if rj != nil {
		sess = rj.sess
	}
	// 准备阶段结束时若还没有把 session 交给 engine goroutine，就由这里
	// 关闭它：否则等待 sess.done 的 Cancel/Delete 会一直阻塞到超时。
	handedOver := false
	defer func() {
		if !handedOver {
			sess.finish()
		}
	}()
	// 若 startAsync 一进入就已被取消（例如 Pause 在 Start 返回后立刻触发），
	// 直接走「准备阶段被取消」的快速路径，避免无谓的 probe。
	if s.prepareCancelled(taskID) {
		s.abortPrepare(taskID, tk)
		return
	}
	auth := decodeAuth(tk.AuthData)
	kind := protocol.ProtocolKind(tk.Protocol)
	if kind == "" {
		// 兼容历史数据：早期通过 lgom:// 添加的任务 protocol 落库为空串，
		// 直接用空 kind 调 protocol.New 会得到 "no driver for kind="。
		if detected, derr := protocol.DetectKind(tk.URL, ""); derr == nil {
			kind = detected
		}
	}
	driver, err := protocol.New(tk.URL, kind, protocol.Auth{AuthOptions: auth})
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
			// engine 保证在调用本回调时不持有自己的锁（见 engine.Run 契约），
			// 因此这里可以取 dirtyMu 安全更新共享任务快照。
			rj.dirtyMu.Lock()
			tk.ChunkRanges = ranges
			rj.dirtyMu.Unlock()
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
	rj.dirtyMu.Lock()
	tk.ChunkRanges = ranges
	rj.dirtyMu.Unlock()
	if err := s.st.UpdateTaskChunkRanges(tk.ID, ranges); err != nil {
		log.Printf("scheduler: persist chunk ranges: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	// 复用 Start 预留的那个 runningJob（而不是新建一个替换掉它）：
	// 这样 sess 与 job/cancel 都挂在同一个对象上，Cancel/Delete 无论
	// 抓到的是哪个阶段的对象都能等到引擎真正退出。
	s.mu.Lock()
	if _, alive := s.jobs[tk.ID]; !alive {
		// 已被 Pause/Cancel/Delete 摘掉槽位：不要启动 engine。
		s.mu.Unlock()
		cancel()
		_ = job.Close()
		_ = dest.Close()
		return
	}
	rj.task = tk
	rj.cancel = cancel
	rj.job = job
	s.mu.Unlock()

	rj.dirtyMu.Lock()
	rj.status = store.TaskStatus.Downloading
	tk.Status = store.TaskStatus.Downloading
	startedSnapshot := tk.Clone()
	rj.dirtyMu.Unlock()

	if err := s.st.UpdateTaskProgress(tk.ID, sumInts(tk.ChunkProgress), tk.ChunkProgress, store.TaskStatus.Downloading, ""); err != nil {
		// 非致命错误——稍后的批量刷新会追赶上来。
	}
	s.publish(Event{Why: "started", Task: startedSnapshot})

	// 在 goroutine 内同步运行 job;该 goroutine 会一直存活到
	// 任务完成或被取消。session 的关闭责任随之移交给它。
	handedOver = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// rjRef 在整个 goroutine 生命周期内持有运行中 job 的引用,
		// 以便状态/收尾调用能读取最新的 ChunkProgress。
		rjRef := rj
		defer func() {
			// 退出时进行清理(reap)。
			s.mu.Lock()
			delete(s.jobs, tk.ID)
			s.mu.Unlock()
			_ = dest.Close()
			// 引擎已停止写文件、终态已落盘 —— 现在再放行等待中的
			// Cancel/Delete（它们会删文件并写 Failed）。
			rjRef.sess.finish()
			// 引擎正常完成/失败/取消后都要尝试把排队的 Pending 提升上来。
			s.promotePending()
		}()
		err := job.Run(ctx, caps.SupportRange && caps.TotalSize > 0)
		if err != nil && ctx.Err() == nil {
			// 失败路径同样使用引擎的权威 chunk 偏移，避免把进度写成
			// 快照里的旧值（表现为进度“回退”）。
			rjRef.setError(err)
			s.markStatusFromJob(tk.ID, store.TaskStatus.Failed, rjRef)
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
		stopJob(rj)
	}

	done := make(chan struct{})
	go func() {
		waited := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(waited)
		}()
		// 同时监听 ctx：否则超时返回后这个 goroutine 会一直挂在
		// wg.Wait() 上（每个超时一次泄漏）。
		select {
		case <-waited:
			close(done)
		case <-ctx.Done():
		}
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stopJob 取消一个 job 的两个上下文（准备阶段与 engine 阶段）。
func stopJob(rj *runningJob) {
	if rj == nil {
		return
	}
	if rj.prepareCancel != nil {
		rj.prepareCancel()
	}
	if rj.cancel != nil {
		rj.cancel()
	}
}

// waitStopped 等待 job 的写者真正停止（引擎退出、文件句柄已关闭、
// 终态已落盘）。ctx 到期即返回 false，调用方据此继续但需容忍竞争。
func waitStopped(ctx context.Context, rj *runningJob) bool {
	if rj == nil || rj.sess == nil {
		return true
	}
	select {
	case <-rj.sess.done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Scheduler) Pause(taskID string) error {
	s.mu.Lock()
	rj, ok := s.jobs[taskID]
	s.mu.Unlock()
	if !ok {
		return errors.New("scheduler: task not running")
	}
	stopJob(rj)
	return nil
}

// Cancel 暂停任务并删除其部分文件。store 中的行会被保留,
// status 置为 Failed,供事后查看。
//
// 必须先等引擎退出再删文件、写 Failed：否则引擎的收尾路径
// (markStatusFromJob(Paused)) 会在其后覆盖这个 Failed，状态会在
// Failed/Paused 之间翻转，同时删文件也会与写者竞争。
func (s *Scheduler) Cancel(taskID string) error {
	s.mu.Lock()
	rj, ok := s.jobs[taskID]
	s.mu.Unlock()
	if ok {
		stopJob(rj)
		waitCtx, cancelWait := context.WithTimeout(context.Background(), stopWait)
		_ = waitStopped(waitCtx, rj)
		cancelWait()
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
		stopJob(rj)
		// 与 Cancel 同理：等写者停止后再删文件/删行，避免引擎收尾
		// 路径在行被删后仍去写状态。
		waitCtx, cancelWait := context.WithTimeout(context.Background(), stopWait)
		_ = waitStopped(waitCtx, rj)
		cancelWait()
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

// running 报告任务当前是否占着 slot（含准备阶段）。
func (s *Scheduler) running(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.jobs[taskID]
	return ok
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
		// 正在下载的任务其文件必然存在（引擎持有句柄），不要因为
		// 短时的 stat 失败（杀软扫描/占用）把它误判为 FileLost。
		if s.running(tk.ID) {
			continue
		}
		if _, err := os.Stat(tk.SavePath); os.IsNotExist(err) {
			if err := s.st.UpdateTaskProgress(tk.ID, tk.Downloaded, tk.ChunkProgress, store.TaskStatus.FileLost, "文件已丢失"); err != nil {
				continue
			}
			tk.Status = store.TaskStatus.FileLost
			s.publish(Event{Why: "updated", Task: tk})
		}
	}
}

// ResetTask 将任务进度清零、状态重置为 Pending，用于文件丢失后重新下载。
func (s *Scheduler) ResetTask(taskID string) error {
	// 运行中的任务不能重置：否则引擎会继续为一行「Pending」的任务
	// 推进进度（UI 上表现为状态与进度自相矛盾）。
	if s.running(taskID) {
		return errors.New("scheduler: task is running, pause it first")
	}
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
	if !ok || rj == nil || rj.job == nil {
		return
	}
	// 先取引擎的权威分片快照，再进 dirtyMu：锁序要求持 dirtyMu 时
	// 不得调用引擎方法（engine.Chunks() 会取引擎自身的锁）。
	cs := rj.job.Chunks()

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
	if cp := chunkProgressFrom(cs); cp != nil {
		rj.task.ChunkProgress = cp
	}
	snapshot := rj.task.Clone()
	rj.dirtyMu.Unlock()
	s.publish(Event{Why: "progress", Task: snapshot, SpeedBPS: p.SpeedBPS})
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

	var cs []engine.ChunkSnapshot
	if hasRJ && rj != nil && rj.job != nil {
		// 先取引擎快照（锁序：不得持 dirtyMu 调引擎）。
		cs = rj.job.Chunks()
	}

	if hasRJ && rj != nil {
		rj.dirtyMu.Lock()
		downloaded = rj.task.Downloaded
		// 优先使用 engine 的最新 chunk 偏移,而不是过时的任务快照。
		if progress := chunkProgressFrom(cs); progress != nil {
			chunkProg = progress
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
	// 先取引擎快照，再进 dirtyMu（锁序）。
	var cs []engine.ChunkSnapshot
	if rj.job != nil {
		cs = rj.job.Chunks()
	}
	rj.dirtyMu.Lock()
	progress := chunkProgressFrom(cs)
	if progress == nil {
		progress = append([]int64(nil), rj.task.ChunkProgress...)
	}
	downloaded := rj.task.Downloaded
	errMsg := rj.task.ErrorMessage
	rj.task.Status = st
	if errMsg != "" {
		rj.task.ErrorMessage = errMsg
	}
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
	// 先取引擎快照，再进 dirtyMu（锁序）。
	var cs []engine.ChunkSnapshot
	if rj.job != nil {
		cs = rj.job.Chunks()
	}
	progress := chunkProgressFrom(cs)
	var total int64
	for _, v := range progress {
		total += v
	}
	if err := s.st.UpdateTaskProgress(taskID, total, progress, store.TaskStatus.Completed, ""); err != nil {
		fmt.Fprintln(os.Stderr, "scheduler completeFromEngine:", err)
	}
	rj.dirtyMu.Lock()
	rj.task.ChunkProgress = progress
	rj.task.Downloaded = total
	rj.task.Status = store.TaskStatus.Completed
	rj.dirtyMu.Unlock()
	if tk, err := s.st.GetTask(taskID); err == nil {
		s.publish(Event{Why: "completed", Task: tk})
	}
}

// fail 把任务标记为 Failed。仅用于「准备阶段」失败（探测/预分配/打开文件）：
// 此时引擎尚未运行，直接使用 store 中的快照即可。
// 引擎运行期间失败走 markStatusFromJob，以便落盘引擎权威的 chunk 偏移。
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
	// 先把状态写为 Paused,再 releaseSlot 触发 promotePending——
	// 顺序至关重要:promotePending 通过 store 查询 Pending 任务,
	// 如果 releaseSlot 先跑,promotePending 会看到自己刚被取消的
	// 任务还显示为 Pending,导致无限重新启动。
	if writeErr := s.st.UpdateTaskProgress(taskID, tk.Downloaded, tk.ChunkProgress, store.TaskStatus.Paused, ""); writeErr != nil {
		fmt.Fprintln(os.Stderr, "scheduler abortPrepare:", writeErr)
	}
	s.releaseSlot(taskID)
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

	type dirtyJob struct {
		rj       *runningJob
		update   store.ProgressUpdate
		progress []int64
	}
	dirty := make([]dirtyJob, 0, len(jobs))
	for _, rj := range jobs {
		// 先取引擎快照（锁序：不得持 dirtyMu 调引擎）。
		var cs []engine.ChunkSnapshot
		if rj.job != nil {
			cs = rj.job.Chunks()
		}

		rj.dirtyMu.Lock()
		if !rj.dirty || rj.task == nil {
			rj.dirtyMu.Unlock()
			continue
		}
		p := rj.lastSnapshot
		status := rj.status
		rj.dirty = false
		rj.dirtyMu.Unlock()

		progress := chunkProgressFrom(cs)
		if progress == nil {
			progress = []int64{}
		}
		dirty = append(dirty, dirtyJob{
			rj:       rj,
			progress: progress,
			update: store.ProgressUpdate{
				ID:            rj.task.ID,
				Downloaded:    p.DownloadedBytes,
				ChunkProgress: progress,
				Status:        status,
			},
		})
	}
	if len(dirty) == 0 {
		return
	}
	// 本周期所有 dirty 任务合并为一次事务提交。
	updates := make([]store.ProgressUpdate, 0, len(dirty))
	for _, d := range dirty {
		updates = append(updates, d.update)
	}
	if err := s.st.UpdateTasksProgress(updates); err != nil {
		fmt.Fprintln(os.Stderr, "flush:", err)
		// 整批失败：全部标记回 dirty，下个周期重试。
		for _, d := range dirty {
			d.rj.dirtyMu.Lock()
			d.rj.dirty = true
			d.rj.dirtyMu.Unlock()
		}
		return
	}
	for _, d := range dirty {
		d.rj.dirtyMu.Lock()
		d.rj.task.ChunkProgress = d.progress
		d.rj.task.Downloaded = d.update.Downloaded
		d.rj.dirtyMu.Unlock()
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

// idSeq 是进程内自增序号，用于消除同毫秒内的 ID 碰撞。
var idSeq atomic.Uint64

// newID 生成任务 ID。
//
// 只靠「毫秒 + pid」并不唯一：实测连续调用 500 次只得到 1 个不同值，
// 而 id 是主键，同毫秒内加入的第二个任务会因 UNIQUE 约束插入失败并被
// 上层丢弃（批量转发 URL 时可复现）。因此追加进程内自增序号。
func newID() string {
	return fmt.Sprintf("ts-%d-%d-%d", time.Now().UnixMilli(), os.Getpid(), idSeq.Add(1))
}

// EnsureSaveDir 确保本地路径的父目录存在。由 UI 调用。
func EnsureSaveDir(p string) error {
	return os.MkdirAll(filepath.Dir(p), 0o755)
}
