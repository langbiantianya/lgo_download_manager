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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lgo_download_manager/internal/bittorrent"
	"lgo_download_manager/internal/engine"
	"lgo_download_manager/internal/logging"
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
	// promoteMu 串行化 Pending 提升，避免多个任务同时完成时反复并发
	// 扫描同一批 Pending 行并争抢 Start。
	promoteMu sync.Mutex
	// prealloc 控制新任务是否在建连前按完整大小预留磁盘空间。
	prealloc atomic.Bool

	// wg 跟踪所有由 startAsync 启动的 per-job goroutine。PauseAll 通过
	// wg.Wait() 确保每个 job 都已落盘 Paused 后再返回,避免"已 cancel 但
	// DB 仍是 Downloading"的窗口。
	wg sync.WaitGroup

	// ---- BitTorrent ----

	// btMu 保护 bt(共享的 BT 客户端管理器)与 btCfg(最近一次设置)。
	// 客户端懒创建:没有 BT 任务时不占用端口、不建 DHT 路由表。
	btMu  sync.Mutex
	bt    *bittorrent.Manager
	btCfg bittorrent.Config
	// btMaxConcurrent 是 BT 任务专用的同时下载上限,独立于全局
	// maxConcurrent;<=0 时沿用默认值。
	btMaxConcurrent int

	// seedsMu 保护 btSeeds:「已下载完成但按设置继续做种」的任务句柄。
	// 这些任务已经 Completed、不占并发槽位,但客户端仍在为它们上传,
	// 直到任务被删除或程序退出(见 bittorrent.go)。
	seedsMu     sync.Mutex
	btSeeds     map[string]*seedEntry
	btSeedOrder []string // 正在上传的种子,按进入顺序排列
	btSeedQueue []string // 已保留种子但让出上传名额的等待队列(FIFO)

	// btTasksMu 保护 btTasks:BT 任务当前句柄的登记表(下载中与做种中
	// 都在内)。「任务详情」窗口靠它查实时 BT 状态(peer 数、上下行
	// 速度、做种情况)——这些数据不进 store.Task,那是落库的静态快照。
	btTasksMu sync.Mutex
	btTasks   map[string]*bittorrent.Handle

	// 事件订阅者(GUI)。筛选逻辑在消费侧完成。
	//
	// subs 由 subMu 单独保护,不与 mu 混用:退订路径会关闭通道,
	// 而 publish 要往通道里写,两者必须互斥,否则会撞出
	// "send on closed channel" panic(旧实现把快照复制到锁外再发送,
	// 退订正好在那个窗口里 close 掉通道)。
	subMu sync.Mutex
	subs  []chan Event
	// subCount 让高频进度回调无需获取 subMu 就能判断是否需要构造事件。
	subCount atomic.Int32
}

// seedEntry 是一个已完成后保留在 BT 客户端里的做种句柄。
type seedEntry struct {
	handle *bittorrent.Handle
	total  int64
}

type runningJob struct {
	task *store.Task
	// bt 标记该任务是否走 BitTorrent 路径,用于 BT 专用并发上限。
	// 准备阶段 task 可能尚未赋值,因此不能只靠 task.Protocol 判断。
	bt bool
	// cancel 取消 engine 的运行上下文；prepareCancel 用于在 Start 预留 slot
	// 之后、engine goroutine 启动之前的「准备阶段」（probe / 预分配）取消
	// 任务。该阶段 cancel 仍为 nil，因此 Pause/PauseAll/Cancel/Delete 必须
	// 同时调用 prepareCancel，否则准备中的任务无法被中止，只能等其跑完
	// 探测后变成 Downloading 才能停。
	// prepareCancel/cancel 由 Scheduler.mu 保护；job 也随 cancel 在同一
	// 临界区发布给进度与暂停路径。
	prepareCtx    context.Context
	prepareCancel context.CancelFunc
	cancel        context.CancelFunc
	job           *engine.Job

	// dirtyMu 保护 task 的易变字段（Downloaded/ChunkProgress/Status/
	// ErrorMessage/ChunkRanges）以及 status/lastSnapshot。dirty 独立使用
	// 原子量，让 2 秒 tick 能无锁跳过没有新进度的任务，不必每轮先取
	// 引擎分片锁。
	// 访问这些字段的三条路径——engine 的进度回调、2 秒刷盘 ticker、
	// Pause/Fail/Complete 收尾——都必须持锁。
	//
	// 锁序：持 dirtyMu 时禁止调用 engine 的任何方法（engine.Chunks() 会取
	// engine 自己的 mu，而 engine 回调 OnPlanChanged/Progress 时会反过来取
	// dirtyMu，形成 ABBA 死锁）。因此所有 Chunks() 调用都在加锁之前完成。
	dirtyMu      sync.Mutex
	dirty        atomic.Bool
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
	sc := &Scheduler{
		st:              s,
		jobs:            map[string]*runningJob{},
		maxConcurrent:   store.DefaultMaxConcurrent,
		btSeeds:         map[string]*seedEntry{},
		btMaxConcurrent: store.DefaultBTMaxConcurrent,
		// BT 客户端的首启默认配置:随机端口、DHT/PEX 开启、不限速、
		// 完成后不做种。main/uimgr 会用持久化设置覆盖它。
		btCfg: bittorrent.Config{
			MaxPeersPerTorrent: store.DefaultBTMaxPeers,
			EnableDHT:          true,
			EnablePEX:          true,
		},
	}
	sc.prealloc.Store(true)
	return sc
}

// SetPrealloc 把用户的磁盘预分配开关应用到后续 Start。关闭时仍会创建
// 目标文件，但不会提前占用完整大小；断点写入语义不受影响。
func (s *Scheduler) SetPrealloc(enabled bool) {
	s.prealloc.Store(enabled)
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

// effectiveBTMaxConcurrentLocked 返回 BT 专用并发上限;调用方需持有 s.mu。
func (s *Scheduler) effectiveBTMaxConcurrentLocked() int {
	if s.btMaxConcurrent <= 0 {
		return store.DefaultBTMaxConcurrent
	}
	return s.btMaxConcurrent
}

// btJobsLocked 统计当前已占 slot 的 BT 任务;调用方需持有 s.mu。
func (s *Scheduler) btJobsLocked() int {
	n := 0
	for _, rj := range s.jobs {
		if rj != nil && rj.bt {
			n++
		}
	}
	return n
}

// promotePending 把 store 里最早创建的 Pending 任务(尚未占 slot 的)
// 按 FIFO 顺序提升到 Downloading,直到达到全局/BT 专用并发上限或没有
// 候选任务为止。每个候选走一次 Start——Start 内部仍受并发上限保护,
// 因此本方法在并发调用下也是安全的。
//
// 候选集合由 SQL 层按 status 过滤后一次取回（此前是每个候选做一次
// 全表 ListTasks，在历史任务多时是 O(N²) 的扫描 + JSON 解码）。
func (s *Scheduler) promotePending() {
	s.promoteMu.Lock()
	defer s.promoteMu.Unlock()

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
		btFull := protocol.ProtocolKind(tk.Protocol) == protocol.ProtoBT &&
			s.btJobsLocked() >= s.effectiveBTMaxConcurrentLocked()
		s.mu.Unlock()
		if full {
			return
		}
		if btFull {
			// BT 专用上限已满时跳过该候选,但继续尝试后面的 HTTP/FTP
			// 任务,避免一个 BT 队列堵住其它协议。
			continue
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
	// 该任务若正在做种,先停止它:同一 infohash 在客户端里只能存在一个
	// Torrent,重复添加会被判定为 ErrAlreadyAdded。
	s.dropSeed(taskID)
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
	// 并发上限检查:Pending 任务同时受全局 maxConcurrent 与 BT 专用
	// btMaxConcurrent 约束。超出时留在 Pending 不报错——调用方(UI
	// 对话框)无需特殊处理,等待 promotePending 自然提升即可。
	if tk0.Status == store.TaskStatus.Pending {
		btFull := protocol.ProtocolKind(tk0.Protocol) == protocol.ProtoBT &&
			s.btJobsLocked() >= s.effectiveBTMaxConcurrentLocked()
		if len(s.jobs) >= s.maxConcurrent || btFull {
			s.mu.Unlock()
			// 静默排队:不分配 slot、不增 wg;后续有任务完成时由
			// promotePending 把它提升到 Downloading。
			return nil
		}
	}
	// 立即预留 slot,携带 prepareCtx/prepareCancel 以便 Pause/PauseAll
	// 在 probe/预分配阶段也能中止任务;同时把 wg.Add 提到这里,
	// 让 PauseAll 的 wg.Wait() 覆盖「准备阶段」,不会过早返回。
	prepareCtx, prepareCancel := context.WithCancel(context.Background())
	s.jobs[taskID] = &runningJob{
		prepareCtx:    prepareCtx,
		prepareCancel: prepareCancel,
		bt:            protocol.ProtocolKind(tk0.Protocol) == protocol.ProtoBT,
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
	// BitTorrent 不走 ProtocolDriver/engine:
	// 它由 bittorrent.Manager 直接驱动(种子元数据、peer、文件落盘都由
	// 客户端负责),scheduler 只做生命周期与进度采样。startBittorrent 返回
	// true 表示 session 已交给它的新 goroutine 收尾;返回 false(槽位已被
	// Pause/Delete 摘掉)时仍由本函数的 defer 关闭 session。
	if kind == protocol.ProtoBT {
		handedOver = s.startBittorrent(taskID, tk, rj, prepareCtx)
		return
	}
	downloadPath := tk.SavePath + ".part"
	driver, err := protocol.New(tk.URL, kind, protocol.Auth{AuthOptions: auth})
	if err != nil {
		s.fail(tk, err)
		s.releaseSlot(taskID)
		return
	}

	probeCtx, probeCancel := context.WithTimeout(prepareCtx, 30*time.Second)
	caps, err := probeWithRetry(probeCtx, driver)
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
	// 旧版本直接写最终文件；第一次恢复时先迁到 .part，后续的恢复数据
	// 完整性检查与重置都只面对一个确定的文件路径。
	if err := migrateLegacyDownloadToPart(tk); err != nil {
		_ = driver.Close()
		s.fail(tk, err)
		s.releaseSlot(taskID)
		return
	}
	resumeDataMissing, err := resumeDownloadDataMissing(tk, downloadPath)
	if err != nil {
		_ = driver.Close()
		s.fail(tk, err)
		s.releaseSlot(taskID)
		return
	}
	sourceSizeChanged := tk.TotalSize > 0 && caps.TotalSize > 0 && tk.TotalSize != caps.TotalSize
	validatorChanged := taskHasResumeState(tk) &&
		resourceValidatorsChanged(tk.ETag, tk.LastModified, caps.ETag, caps.LastModified)
	sourceChanged := sourceSizeChanged || validatorChanged || resumeDataMissing
	// 准备阶段失败统一经过 fail()/releaseSlot,此处继续走预分配/启动 engine 流程。
	if caps.TotalSize > 0 {
		metaAllocated := tk.IsAllocated && !sourceChanged
		if err := s.st.UpdateTaskMeta(tk.ID, caps.TotalSize, caps.SupportRange, metaAllocated, tk.ChunkCount); err != nil {
			_ = driver.Close()
			s.fail(tk, err)
			s.releaseSlot(taskID)
			return
		}
		tk.TotalSize = caps.TotalSize
		tk.SupportRange = caps.SupportRange
		// 暂时保留内存中的旧 IsAllocated/Downloaded，让 sourceChanged
		// 分支统一完成文件截断、进度清零和分配位更新。
	}

	if sourceChanged {
		// 总大小、资源版本或本地恢复数据完整性变化意味着旧偏移不能
		// 再作为续传依据。
		// 不能把旧文件中的偏移继续当作同一份内容；保留旧字节只会造成
		// 新旧版本拼接。把进度清零并删除旧数据，安全地从头下载。
		tk.IsAllocated = false
		tk.Downloaded = 0
		tk.ChunkProgress = make([]int64, tk.ChunkCount)
		if err := s.st.UpdateTaskProgress(tk.ID, 0, tk.ChunkProgress, tk.Status, ""); err != nil {
			_ = driver.Close()
			s.fail(tk, err)
			s.releaseSlot(taskID)
			return
		}
		if err := os.Truncate(downloadPath, 0); err != nil && !os.IsNotExist(err) {
			_ = driver.Close()
			s.fail(tk, err)
			s.releaseSlot(taskID)
			return
		}
	}
	if tk.ETag != "" || tk.LastModified != "" || caps.ETag != "" || caps.LastModified != "" {
		if err := s.st.UpdateTaskValidators(tk.ID, caps.ETag, caps.LastModified); err != nil {
			_ = driver.Close()
			s.fail(tk, err)
			s.releaseSlot(taskID)
			return
		}
		tk.ETag = caps.ETag
		tk.LastModified = caps.LastModified
	}

	// 若尚未预分配文件则进行预分配(恢复场景:文件已具有正确大小)。
	preallocEnabled := s.prealloc.Load()
	needAlloc := preallocEnabled && !tk.IsAllocated && caps.TotalSize > 0
	if needAlloc {
		dest, err := prealloc.Preallocate(downloadPath, caps.TotalSize)
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
	// 关闭预分配时仍要提前创建目标文件，但不会按完整大小占用空间；
	// 未知总大小时这里也是唯一的建文件路径。
	dest, err := os.OpenFile(downloadPath, os.O_RDWR|os.O_CREATE, 0o644)
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
		Logger: logging.L(),
		OnPlanChanged: func(ranges []int64) {
			// engine 保证在调用本回调时不持有自己的锁（见 engine.Run 契约），
			// 因此这里可以取 dirtyMu 安全更新共享任务快照。
			rj.dirtyMu.Lock()
			tk.ChunkRanges = ranges
			rj.dirtyMu.Unlock()
			if err := s.st.UpdateTaskChunkRanges(tk.ID, ranges); err != nil {
				logging.Printf("scheduler: persist chunk ranges: %v", err)
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
		logging.Printf("scheduler: persist chunk ranges: %v", err)
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
		// Completed 是对用户的持久性承诺。先把数据文件同步到存储设备，
		// 再写数据库状态，避免掉电后数据库显示完成但文件仍有零页。
		if err := dest.Sync(); err != nil {
			rjRef.setError(fmt.Errorf("sync completed file: %w", err))
			s.markStatusFromJob(tk.ID, store.TaskStatus.Failed, rjRef)
			return
		}
		if err := dest.Close(); err != nil {
			rjRef.setError(fmt.Errorf("close completed file: %w", err))
			s.markStatusFromJob(tk.ID, store.TaskStatus.Failed, rjRef)
			return
		}
		if _, err := os.Lstat(tk.SavePath); err == nil {
			rjRef.setError(fmt.Errorf("download target appeared while downloading: %s", tk.SavePath))
			s.markStatusFromJob(tk.ID, store.TaskStatus.Failed, rjRef)
			return
		} else if !os.IsNotExist(err) {
			rjRef.setError(fmt.Errorf("inspect completed file target: %w", err))
			s.markStatusFromJob(tk.ID, store.TaskStatus.Failed, rjRef)
			return
		}
		if err := os.Rename(downloadPath, tk.SavePath); err != nil {
			rjRef.setError(fmt.Errorf("publish completed file: %w", err))
			s.markStatusFromJob(tk.ID, store.TaskStatus.Failed, rjRef)
			return
		}
		if err := syncParentDir(tk.SavePath); err != nil {
			// 文件本身已 Sync 成功，正式名称也已原子发布。部分 FUSE/
			// 网络文件系统拒绝对目录执行 fsync；此时记为告警而不是在
			// 文件已经可用后把任务改成 Failed。
			logging.Printf("scheduler: sync completed directory: %v", err)
		}
		s.completeFromEngine(tk.ID, rjRef)
	}()
}

const maxProbeAttempts = 3

// probeWithRetry 处理限流/临时网络错误。探测发生在 engine 启动之前，
// 若直接返回会把可恢复的 429/503 变成一次失败；这里使用有限重试，并
// 优先遵循服务端 Retry-After，等待期间仍响应 Pause/Delete 的 ctx。
func probeWithRetry(ctx context.Context, driver protocol.ProtocolDriver) (*protocol.DriverCapabilities, error) {
	backoff := 500 * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= maxProbeAttempts; attempt++ {
		caps, err := driver.Probe(ctx)
		if err == nil {
			return caps, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			// 某些 Transport 包装错误不会稳定地兼容 errors.Is(err,
			// context.Canceled)；准备阶段取消必须立即退出，不能把 Pause
			// 误判成网络失败后继续退避重试。
			return nil, ctx.Err()
		}
		if protocol.IsTerminal(err) || attempt == maxProbeAttempts {
			return nil, err
		}
		wait := protocol.RetryAfter(err)
		if wait <= 0 {
			wait = backoff
		}
		if wait > 5*time.Minute {
			wait = 5 * time.Minute
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		backoff *= 2
	}
	return nil, lastErr
}

// syncParentDir 持久化目录项，确保崩溃或掉电后已重命名完成的文件仍然
// 可见。Windows 不支持对目录句柄执行 Sync，文件本身已在上方完成 Sync。
func syncParentDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func taskHasResumeState(tk *store.Task) bool {
	if tk == nil {
		return false
	}
	if tk.IsAllocated || tk.Downloaded > 0 {
		return true
	}
	for _, progress := range tk.ChunkProgress {
		if progress > 0 {
			return true
		}
	}
	return false
}

// resumeDownloadDataMissing 检查恢复所依赖的本地文件是否仍覆盖已记录
// 的最高写入位置。仅看“文件存在”不够：用户截断文件或文件系统恢复出
// 一个空壳时，继续按旧偏移下载会在前面留下零洞。
func resumeDownloadDataMissing(tk *store.Task, path string) (bool, error) {
	if !taskHasResumeState(tk) {
		return false, nil
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect partial download: %w", err)
	}
	if info.Size() == 0 && tk.Downloaded > 0 {
		return true, nil
	}
	if len(tk.ChunkRanges) < 2*len(tk.ChunkProgress) {
		return false, nil
	}
	var required int64
	for i, progress := range tk.ChunkProgress {
		if progress <= 0 {
			continue
		}
		if writtenThrough := tk.ChunkRanges[2*i] + progress; writtenThrough > required {
			required = writtenThrough
		}
	}
	return required > 0 && info.Size() < required, nil
}

// resourceValidatorsChanged 判断两次探测是否仍指向同一资源。只要有
// 一个可信校验器可用就优先使用；旧记录有校验器而新响应完全无法比较时
// 按“已变化”处理，宁可重下也不拼接未知版本。
func resourceValidatorsChanged(oldETag, oldLastModified, newETag, newLastModified string) bool {
	oldETag = strings.TrimSpace(oldETag)
	oldLastModified = strings.TrimSpace(oldLastModified)
	newETag = strings.TrimSpace(newETag)
	newLastModified = strings.TrimSpace(newLastModified)
	if oldETag == "" && oldLastModified == "" {
		return false
	}
	if oldETag != "" && newETag != "" {
		return oldETag != newETag
	}
	if oldLastModified != "" && newLastModified != "" {
		return oldLastModified != newLastModified
	}
	return true
}

// migrateLegacyDownloadToPart 把旧版本直接写在最终路径上的部分文件迁移
// 到 ".part"。仅在任务确有进度时执行，避免误移动同名的用户文件。
func migrateLegacyDownloadToPart(tk *store.Task) error {
	partPath := tk.SavePath + ".part"
	if _, err := os.Stat(partPath); err == nil {
		if _, err := os.Stat(tk.SavePath); err == nil {
			return fmt.Errorf("download target already exists beside partial file: %s", tk.SavePath)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect completed download target: %w", err)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect partial download: %w", err)
	}
	if !tk.IsAllocated && tk.Downloaded <= 0 {
		return nil
	}
	if _, err := os.Stat(tk.SavePath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect legacy download: %w", err)
	}
	if err := os.Rename(tk.SavePath, partPath); err != nil {
		return fmt.Errorf("migrate legacy download to .part: %w", err)
	}
	return nil
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
	stopAllJobs := func() {
		s.mu.Lock()
		jobs := make([]*runningJob, 0, len(s.jobs))
		for _, rj := range s.jobs {
			jobs = append(jobs, rj)
		}
		s.mu.Unlock()
		for _, rj := range jobs {
			s.stopJob(rj)
		}
	}
	stopAllJobs()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	// 某个 slot 释放时 promotePending 可能在同一时刻启动新任务；持续
	// 补发取消可覆盖这层竞争，避免 PauseAll 只取消到最初快照。
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return nil
		case <-ticker.C:
			stopAllJobs()
		case <-ctx.Done():
			// 若 wg 与 deadline 同时完成，select 可能随机选中 ctx；
			// 再检查一次，避免把已经完成的暂停误报为超时。
			select {
			case <-done:
				return nil
			default:
				return ctx.Err()
			}
		}
	}
}

// stopJob 取消一个 job 的两个上下文（准备阶段与 engine 阶段）。
// cancel 字段由 s.mu 保护，这里在锁内取出函数后立即释放锁再调用。
func (s *Scheduler) stopJob(rj *runningJob) {
	if rj == nil {
		return
	}
	s.mu.Lock()
	prepareCancel := rj.prepareCancel
	cancel := rj.cancel
	s.mu.Unlock()
	if prepareCancel != nil {
		prepareCancel()
	}
	if cancel != nil {
		cancel()
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
	s.stopJob(rj)
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
		s.stopJob(rj)
		waitCtx, cancelWait := context.WithTimeout(context.Background(), stopWait)
		_ = waitStopped(waitCtx, rj)
		cancelWait()
	}
	tk, err := s.st.GetTask(taskID)
	if err != nil {
		return err
	}
	// 先停做种(BT 任务完成后可能仍在为其它 peer 上传)。
	s.dropSeed(taskID)
	removeTaskData(tk)
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
		s.stopJob(rj)
		// 与 Cancel 同理：等写者停止后再删文件/删行，避免引擎收尾
		// 路径在行被删后仍去写状态。
		waitCtx, cancelWait := context.WithTimeout(context.Background(), stopWait)
		_ = waitStopped(waitCtx, rj)
		cancelWait()
	}
	tk, err := s.st.GetTask(taskID)
	if err == nil && tk != nil {
		s.dropSeed(taskID)
		removeTaskData(tk)
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
		path, check := taskExistingDataPath(tk, s.running(tk.ID))
		if !check || path == "" {
			continue
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := s.st.UpdateTaskProgress(tk.ID, tk.Downloaded, tk.ChunkProgress, store.TaskStatus.FileLost, "文件已丢失"); err != nil {
				continue
			}
			// 数据不在磁盘上了,做种也就失去意义(否则会一直为不存在的
			// 文件保持种子)。放在状态落盘之后,避免与引擎收尾竞争。
			s.dropSeed(tk.ID)
			tk.Status = store.TaskStatus.FileLost
			s.publish(Event{Why: "updated", Task: tk})
		}
	}
}

// taskExistingDataPath 返回任务当前应该存在的磁盘路径。运行中的任务由
// 引擎持有文件句柄，短暂 stat 失败不能据此误报 FileLost，因此直接跳过。
func taskExistingDataPath(tk *store.Task, running bool) (string, bool) {
	if tk == nil || tk.SavePath == "" || running {
		return "", false
	}
	switch tk.Status {
	case store.TaskStatus.Completed:
		return tk.SavePath, true
	case store.TaskStatus.Paused:
		if !taskHasResumeState(tk) {
			return "", false
		}
	case store.TaskStatus.Downloading:
		// 非运行中的 Downloading 是异常退出残留，交给 Reclaim 兜底，
		// 但文件检查仍可提前反映数据是否存在。
	default:
		return "", false
	}
	if protocol.ProtocolKind(tk.Protocol) == protocol.ProtoBT {
		return tk.SavePath, true
	}
	return tk.SavePath + ".part", true
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
	s.subMu.Lock()
	s.subs = append(s.subs, ch)
	s.subCount.Add(1)
	s.subMu.Unlock()
	return ch, func() {
		// 退订与 publish 互斥:持 subMu 关闭通道,保证不会有人正往
		// 这个通道里写。通道满了就丢弃事件的语义保持不变。
		s.subMu.Lock()
		defer s.subMu.Unlock()
		for i, c := range s.subs {
			if c == ch {
				s.subs = append(s.subs[:i], s.subs[i+1:]...)
				s.subCount.Add(-1)
				close(ch)
				return
			}
		}
	}
}

func (s *Scheduler) hasSubscribers() bool {
	return s.subCount.Load() > 0
}

// publish 向所有订阅者发送事件;通道已满时丢弃。
//
// 发送在 subMu 保护下完成,与退订(关闭通道)互斥。发送本身是非阻塞的
// (select + default),持锁不会拖住订阅者。
func (s *Scheduler) publish(ev Event) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for _, ch := range s.subs {
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
	notify := s.hasSubscribers()

	rj.dirtyMu.Lock()
	rj.dirty.Store(true)
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
	var snapshot *store.Task
	if notify {
		snapshot = rj.task.Clone()
	}
	rj.dirtyMu.Unlock()
	if snapshot != nil {
		s.publish(Event{Why: "progress", Task: snapshot, SpeedBPS: p.SpeedBPS})
	}
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
		logging.Printf("scheduler markStatusFromJob: %v", err)
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
		logging.Printf("scheduler completeFromEngine: %v", err)
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
		logging.Printf("scheduler fail: %v", writeErr)
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
		logging.Printf("scheduler abortPrepare: %v", writeErr)
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
	preparing := ok && rj != nil && rj.cancel == nil && rj.prepareCancel != nil
	s.mu.Unlock()
	if !preparing {
		return false
	}
	return true
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
		if !rj.dirty.Load() {
			continue
		}
		// 先取引擎快照（锁序：不得持 dirtyMu 调引擎）。
		var cs []engine.ChunkSnapshot
		if rj.job != nil {
			cs = rj.job.Chunks()
		}

		rj.dirtyMu.Lock()
		if !rj.dirty.Load() || rj.task == nil {
			rj.dirtyMu.Unlock()
			continue
		}
		p := rj.lastSnapshot
		status := rj.status
		// BT 任务没有 engine 分片快照(下面 cs 为 nil),分片进度直接来自
		// 任务快照(每个种子文件一块),必须在持锁期间取出来,否则会被
		// 下面的 chunkProgressFrom(nil) 覆盖成空数组。
		var taskProgress []int64
		if rj.job == nil {
			taskProgress = append([]int64(nil), rj.task.ChunkProgress...)
		}
		rj.dirty.Store(false)
		rj.dirtyMu.Unlock()

		progress := chunkProgressFrom(cs)
		if taskProgress != nil {
			progress = taskProgress
		}
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
		logging.Printf("flush: %v", err)
		// 整批失败：全部标记回 dirty，下个周期重试。
		for _, d := range dirty {
			d.rj.dirty.Store(true)
		}
		return
	}
	for _, d := range dirty {
		d.rj.dirtyMu.Lock()
		// 数据库提交期间可能已有更新进度；此时 dirty 会重新为 true，
		// 不能再用旧快照覆盖内存中的新值，留给下一轮刷盘。
		if !d.rj.dirty.Load() {
			d.rj.task.ChunkProgress = d.progress
			d.rj.task.Downloaded = d.update.Downloaded
		}
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
