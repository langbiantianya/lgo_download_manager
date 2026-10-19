// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package scheduler

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"lgo_download_manager/internal/bittorrent"
	"lgo_download_manager/internal/engine"
	"lgo_download_manager/internal/logging"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/store"
)

// btProgressEvery 是 BT 任务的进度采样周期。BT 的字节数由客户端在连接层
// 累加,采样过于频繁没有意义;1s 足够 UI 平滑刷新进度与速度。
const btProgressEvery = time.Second

// btMetadataLogEvery 是「等待元数据」日志的间隔:磁力链接长时间没有
// peer 时周期性记一条,便于区分「网络不通」与「卡死」。
const btMetadataLogEvery = 30 * time.Second

// startSeedMonitor 可在测试中替换,避免用零值句柄启动真实分享率轮询。
// 在 init 中赋值以避开 monitor→dropSeedIf→promoteSeedQueue→本变量的
// 包级初始化环。
var startSeedMonitor func(s *Scheduler, taskID string, h *bittorrent.Handle, total int64)

func init() {
	startSeedMonitor = func(s *Scheduler, taskID string, h *bittorrent.Handle, total int64) {
		go s.monitorSeedRatio(taskID, h, total)
	}
}

// BittorrentConfigFrom 把持久化设置映射为 BT 客户端配置。
// 供 main.go 启动时与 uimgr 每次保存设置时调用。
func BittorrentConfigFrom(s store.Settings) bittorrent.Config {
	return bittorrent.Config{
		ListenPort:         s.BTPort,
		MaxPeersPerTorrent: s.EffectiveBTMaxPeers(),
		DownloadLimit:      int64(s.BTDownloadLimitKiB) * 1024,
		UploadLimit:        int64(s.BTUploadLimitKiB) * 1024,
		EnableDHT:          s.BTDHT,
		EnablePEX:          s.BTPEX,
		SeedRatio:          s.BTSeedRatio,
		MaxSeeding:         s.EffectiveBTMaxSeeding(),
		MaxConcurrentTasks: s.EffectiveBTMaxConcurrent(),
		ExtraTrackers:      SplitTrackers(s.BTTrackers),
		HTTPProxy:          protocol.HTTPProxyFunc(),
	}
}

// SplitTrackers 把设置里的附加 tracker 串(逗号/分号/空白/换行分隔)
// 拆成列表,顺带过滤空项。
func SplitTrackers(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\r', '\t', ' ':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SetBittorrentConfig 应用最新的 BT 设置。没有 BT 任务时不会创建客户端;
// 已有客户端时限速立刻生效,端口/DHT/PEX 等构造期参数在客户端可安全重建
// (没有任务在跑)时立即生效,否则延后到最后一个任务结束。
func (s *Scheduler) SetBittorrentConfig(cfg bittorrent.Config) {
	s.btMu.Lock()
	m := s.bt
	s.btCfg = cfg
	s.btMu.Unlock()
	s.mu.Lock()
	s.btMaxConcurrent = cfg.MaxConcurrentTasks
	s.mu.Unlock()
	// 关闭做种时立即停止已登记的做种句柄;分享率调小由各任务的监控
	// goroutine 在下一个采样周期读到新值后生效。
	if cfg.SeedRatio <= 0 {
		s.dropAllSeeds()
	} else {
		s.rebalanceSeeds()
	}
	if m != nil {
		m.ApplyConfig(cfg)
	}
	// BT 并发上限可能被调大,立即尝试提升排队的 Pending 任务。
	s.promotePending()
}

// bittorrentClient 返回共享的 BT 客户端管理器,首次调用时按当前配置创建。
func (s *Scheduler) bittorrentClient() (*bittorrent.Manager, error) {
	s.btMu.Lock()
	defer s.btMu.Unlock()
	if s.bt != nil {
		return s.bt, nil
	}
	m, err := bittorrent.New(s.btCfg)
	if err != nil {
		return nil, err
	}
	s.bt = m
	return m, nil
}

// CloseBittorrent 关闭 BT 客户端(退出流程调用),所有种子连接随之断开。
func (s *Scheduler) CloseBittorrent() {
	s.btMu.Lock()
	m := s.bt
	s.bt = nil
	s.btMu.Unlock()
	// 先关闭做种句柄,让分享率监控 goroutine 收到 Done 后退出;
	// 再关闭客户端统一释放剩余连接。
	s.dropAllSeeds()
	if m != nil {
		if err := m.Close(); err != nil {
			logging.Printf("scheduler: close bittorrent client: %v", err)
		}
	}
}

// ---- 做种任务簿记 ----

// keepSeeding 把「已下载完成但按设置继续做种」的句柄登记下来:任务从此
// 不再占用并发槽位(已 Completed),但客户端仍保持上传,直到分享率达标、
// 用户删除任务或退出程序。关闭做种时返回 false,调用方需要照常关闭句柄。
// 达到同时做种上限时,新完成的种子进入等待队列并暂停上传;已有种子
// 达到分享率释放名额后,队列按 FIFO 顺序接替上传。
func (s *Scheduler) keepSeeding(taskID string, h *bittorrent.Handle, total int64) bool {
	s.btMu.Lock()
	ratio := s.btCfg.SeedRatio
	limit := s.btCfg.MaxSeeding
	s.btMu.Unlock()
	if ratio <= 0 || total <= 0 {
		return false
	}
	if limit <= 0 {
		limit = store.DefaultBTMaxSeeding
	}

	s.seedsMu.Lock()
	if s.btSeeds == nil {
		s.btSeeds = map[string]*seedEntry{}
	}
	old := s.removeSeedLocked(taskID)
	entry := &seedEntry{handle: h, total: total}
	s.btSeeds[taskID] = entry
	queued := len(s.btSeedOrder) >= limit
	if queued {
		h.DisallowUpload()
		s.btSeedQueue = append(s.btSeedQueue, taskID)
	} else {
		h.AllowUpload()
		s.btSeedOrder = append(s.btSeedOrder, taskID)
	}
	s.seedsMu.Unlock()
	if old != nil && old.handle != nil && old.handle != h {
		old.handle.Close()
	}
	if queued {
		logging.Printf("scheduler: bt task %s queued for seeding (limit %d)", taskID, limit)
		return true
	}
	startSeedMonitor(s, taskID, h, total)
	return true
}

// removeSeedLocked 从活动集合与等待队列中删除记录;调用方需持有 seedsMu。
// 返回值仅用于调用方在释放锁后关闭句柄。
func (s *Scheduler) removeSeedLocked(taskID string) *seedEntry {
	e := s.btSeeds[taskID]
	if e == nil {
		return nil
	}
	delete(s.btSeeds, taskID)
	s.btSeedOrder = removeString(s.btSeedOrder, taskID)
	s.btSeedQueue = removeString(s.btSeedQueue, taskID)
	return e
}

// removeString 返回去掉 target 后的新切片,避免调用方持有旧底层数组。
func removeString(list []string, target string) []string {
	out := list[:0]
	for _, s := range list {
		if s != target {
			out = append(out, s)
		}
	}
	return out
}

// seedRatioCheckEvery 是分享率采样周期。做种上传本身由客户端维护,
// 这里只读取累计上传字节,5s 的检查频率足够且不会增加明显开销。
const seedRatioCheckEvery = 5 * time.Second

// monitorSeedRatio 周期性检查累计上传量,达到当前分享率后停止做种。
// 每次采样都读取最新配置,因此用户在设置里调小分享率或关闭做种后
// 不需要重启任务。句柄被删除/替换时 Done 关闭,goroutine 自行退出。
func (s *Scheduler) monitorSeedRatio(taskID string, h *bittorrent.Handle, total int64) {
	if total <= 0 {
		return
	}
	ticker := time.NewTicker(seedRatioCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-h.Done():
			return
		case <-ticker.C:
			// 句柄可能在进入本分支的同时被删除/替换;先确认仍存活再读统计。
			select {
			case <-h.Done():
				return
			default:
			}
			s.btMu.Lock()
			ratio := s.btCfg.SeedRatio
			s.btMu.Unlock()
			if ratio <= 0 {
				_ = s.dropSeedIf(taskID, h)
				return
			}
			if seedRatioReached(h.UploadedData(), total, ratio) {
				if s.dropSeedIf(taskID, h) {
					logging.Printf("scheduler: bt task %s reached seed ratio %.2f; stopping seed", taskID, ratio)
				}
				return
			}
		}
	}
}

// seedRatioReached 判断上传量是否达到 total*ratio。ratio<=0 时不做种。
func seedRatioReached(uploaded, total int64, ratio float64) bool {
	return ratio > 0 && total > 0 && float64(uploaded) >= float64(total)*ratio
}

// dropSeedIf 仅在 taskID 仍指向 h 时停止做种,避免误关同一任务的新句柄。
func (s *Scheduler) dropSeedIf(taskID string, h *bittorrent.Handle) bool {
	s.seedsMu.Lock()
	e := s.btSeeds[taskID]
	if e == nil || e.handle != h {
		s.seedsMu.Unlock()
		return false
	}
	s.removeSeedLocked(taskID)
	s.seedsMu.Unlock()
	h.Close()
	s.promoteSeedQueue()
	return true
}

// dropSeed 停止某个任务的做种(任务被删除/取消时调用)。
func (s *Scheduler) dropSeed(taskID string) {
	s.seedsMu.Lock()
	e := s.removeSeedLocked(taskID)
	s.seedsMu.Unlock()
	if e != nil && e.handle != nil {
		e.handle.Close()
	}
	s.promoteSeedQueue()
}

// dropAllSeeds 停止所有做种(退出流程调用)。
func (s *Scheduler) dropAllSeeds() {
	s.seedsMu.Lock()
	seeds := s.btSeeds
	s.btSeeds = map[string]*seedEntry{}
	s.btSeedOrder = nil
	s.btSeedQueue = nil
	s.seedsMu.Unlock()
	for _, e := range seeds {
		if e != nil && e.handle != nil {
			e.handle.Close()
		}
	}
}

// promoteSeedQueue 在有名额时把等待队列中的种子提升为上传状态,
// 每个接替上传的种子启动独立的分享率监控。
func (s *Scheduler) promoteSeedQueue() {
	s.btMu.Lock()
	ratio := s.btCfg.SeedRatio
	limit := s.btCfg.MaxSeeding
	s.btMu.Unlock()
	if ratio <= 0 {
		return
	}
	if limit <= 0 {
		limit = store.DefaultBTMaxSeeding
	}

	type promoted struct {
		taskID string
		entry  *seedEntry
	}
	var started []promoted
	s.seedsMu.Lock()
	for len(s.btSeedQueue) > 0 && len(s.btSeedOrder) < limit {
		taskID := s.btSeedQueue[0]
		s.btSeedQueue = s.btSeedQueue[1:]
		e := s.btSeeds[taskID]
		if e == nil || e.handle == nil {
			continue
		}
		e.handle.AllowUpload()
		s.btSeedOrder = append(s.btSeedOrder, taskID)
		started = append(started, promoted{taskID: taskID, entry: e})
	}
	s.seedsMu.Unlock()
	for _, p := range started {
		startSeedMonitor(s, p.taskID, p.entry.handle, p.entry.total)
	}
}

// rebalanceSeeds 在同时做种上限变化后调整活动集合:上限调小时把最新的
// 活动种子移入等待队列并暂停上传;调大时立即补位。
func (s *Scheduler) rebalanceSeeds() {
	s.btMu.Lock()
	ratio := s.btCfg.SeedRatio
	limit := s.btCfg.MaxSeeding
	s.btMu.Unlock()
	if ratio <= 0 {
		s.dropAllSeeds()
		return
	}
	if limit <= 0 {
		limit = store.DefaultBTMaxSeeding
	}

	s.seedsMu.Lock()
	for len(s.btSeedOrder) > limit {
		taskID := s.btSeedOrder[len(s.btSeedOrder)-1]
		s.btSeedOrder = s.btSeedOrder[:len(s.btSeedOrder)-1]
		if e := s.btSeeds[taskID]; e != nil && e.handle != nil {
			e.handle.DisallowUpload()
			// 从活动集合尾部往前移入时前插,保持等待队列按原活动顺序 FIFO。
			s.btSeedQueue = append([]string{taskID}, s.btSeedQueue...)
		}
	}
	s.seedsMu.Unlock()
	s.promoteSeedQueue()
}

// ---- 任务启动 ----

// startBittorrent 接管一个 BT 任务的启动并返回 true(会话由新 goroutine
// 负责关闭)。startAsync 在识别出 BT 协议后调用它。
//
// 与 HTTP 分片路径的差别:没有「探测 → 预分配 → 分片下载」三段,而是
// 「添加种子 → 等元数据 → 轮询进度 → 完成/失败」。
func (s *Scheduler) startBittorrent(taskID string, tk *store.Task, rj *runningJob, prepareCtx context.Context) bool {
	ctx, cancel := context.WithCancel(context.Background())
	// 准备阶段的取消(用户点了暂停)必须能立刻中断尚未建好的 BT 客户端。
	stopPrepare := context.AfterFunc(prepareCtx, cancel)

	s.mu.Lock()
	if _, alive := s.jobs[taskID]; !alive {
		// 已被 Pause/Cancel/Delete 摘掉槽位:不要启动。
		s.mu.Unlock()
		stopPrepare()
		cancel()
		return false
	}
	rj.task = tk
	rj.cancel = cancel
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer stopPrepare()
		defer cancel()

		rjRef := rj
		var (
			handle      *bittorrent.Handle
			seedingKept bool
		)
		defer func() {
			s.mu.Lock()
			delete(s.jobs, taskID)
			s.mu.Unlock()
			// 未转交给做种簿记的句柄在这里摘除种子(下载中/暂停/失败)。
			if handle != nil && !seedingKept {
				handle.Close()
			}
			rjRef.sess.finish()
			s.promotePending()
		}()

		if s.prepareCancelled(taskID) {
			s.abortPrepare(taskID, tk)
			return
		}
		client, err := s.bittorrentClient()
		if err != nil {
			s.fail(tk, err)
			s.releaseSlot(taskID)
			return
		}
		handle, err = s.addBittorrentTorrent(ctx, client, tk)
		if err != nil {
			if ctx.Err() != nil {
				// 用户已经暂停/删除:按「准备阶段被取消」收尾。
				s.abortPrepare(taskID, tk)
				return
			}
			s.fail(tk, err)
			s.releaseSlot(taskID)
			return
		}
		// 请求下载全部内容。磁力链接的元数据可能还没到,这一步会在
		// 元数据就绪后再标记分片(见 bittorrent.Handle.StartDownloading)。
		handle.StartDownloading()

		rjRef.dirtyMu.Lock()
		rjRef.status = store.TaskStatus.Downloading
		tk.Status = store.TaskStatus.Downloading
		started := tk.Clone()
		rjRef.dirtyMu.Unlock()
		if werr := s.st.UpdateTaskProgress(tk.ID, tk.Downloaded, tk.ChunkProgress, store.TaskStatus.Downloading, ""); werr != nil {
			logging.Printf("scheduler: bt mark downloading: %v", werr)
		}
		s.publish(Event{Why: "started", Task: started})

		s.runBittorrent(ctx, taskID, tk, rjRef, handle, &seedingKept)
	}()
	return true
}

// runBittorrent 是 BT 任务的进度循环:缓存元数据 → 周期采样 → 完成/暂停。
func (s *Scheduler) runBittorrent(
	ctx context.Context,
	taskID string,
	tk *store.Task,
	rj *runningJob,
	handle *bittorrent.Handle,
	seedingKept *bool,
) {
	metaSaved := false
	ticker := time.NewTicker(btProgressEvery)
	defer ticker.Stop()
	// 磁力链接在找不到持有元数据的 peer 时会长时间停在「等待元数据」,
	// 周期性记一条日志便于排障(说明这是网络/DHT 问题,不是卡死)。
	nextMetaLog := time.Now().Add(btMetadataLogEvery)

	for {
		select {
		case <-ctx.Done():
			// 暂停/取消:与引擎路径语义一致——落盘最新进度并标记 Paused,
			// 数据留在磁盘上供续传;种子由 goroutine 的 defer 摘除。
			s.markStatusFromJob(taskID, store.TaskStatus.Paused, rj)
			return
		case <-ticker.C:
		}

		snap := handle.Snapshot()
		if !metaSaved && len(snap.Files) == 0 && time.Now().After(nextMetaLog) {
			logging.Printf("scheduler: bt task %s waiting for metadata (peers=%d)", taskID, snap.Peers)
			nextMetaLog = time.Now().Add(btMetadataLogEvery)
		}
		// 元数据到手后缓存 info 字典:后续恢复不必再依赖 DHT/tracker
		// (离线、tracker 全挂也能续传)。顺带记下单文件种子的文件名,
		// 让 UI 的「打开文件」按钮能直接定位到完成后的文件。
		if !metaSaved && len(snap.Files) > 0 {
			if info := handle.InfoBytes(); len(info) > 0 {
				if err := s.st.SaveTorrentMetainfo(taskID, info); err != nil {
					logging.Printf("scheduler: cache torrent metainfo: %v", err)
				}
				rel := ""
				if len(snap.Files) == 1 {
					rel = snap.Files[0].Path
				}
				if err := s.st.UpdateTaskBTFile(taskID, rel); err != nil {
					logging.Printf("scheduler: record bt file: %v", err)
				}
				rj.dirtyMu.Lock()
				if rj.task != nil {
					rj.task.BTFile = rel
				}
				rj.dirtyMu.Unlock()
				metaSaved = true
			}
		}
		if snap.Total > 0 && snap.Complete {
			s.completeBittorrent(taskID, tk, rj, handle, snap, seedingKept)
			return
		}
		s.markBittorrentProgress(taskID, rj, snap)
	}
}

// markBittorrentProgress 把一次 BT 采样写入任务快照并广播进度事件。
// 与 markProgress 一样只更新内存快照(dirty 标记),落库交给 2s 的 flushAll。
func (s *Scheduler) markBittorrentProgress(taskID string, rj *runningJob, snap bittorrent.Snapshot) {
	if rj == nil {
		return
	}
	progress := btChunkProgress(snap.Files)
	ranges := btChunkRanges(snap.Files)
	rj.dirtyMu.Lock()
	rj.dirty = true
	rj.status = store.TaskStatus.Downloading
	rj.lastSnapshot = engine.Progress{
		TaskID:          taskID,
		TotalSize:       snap.Total,
		DownloadedBytes: snap.Downloaded,
		SpeedBPS:        snap.SpeedBPS,
	}
	if rj.task == nil {
		rj.dirtyMu.Unlock()
		return
	}
	if snap.Total > 0 {
		rj.task.TotalSize = snap.Total
	}
	rj.task.Downloaded = snap.Downloaded
	rj.task.Status = store.TaskStatus.Downloading
	rj.task.ChunkRanges = ranges
	rj.task.ChunkProgress = progress
	clone := rj.task.Clone()
	rj.dirtyMu.Unlock()
	s.publish(Event{Why: "progress", Task: clone, SpeedBPS: snap.SpeedBPS})
}

// completeBittorrent 收尾一个下载完成的 BT 任务:落盘终态、发布完成事件,
// 并按设置决定是否继续做种。
func (s *Scheduler) completeBittorrent(
	taskID string,
	tk *store.Task,
	rj *runningJob,
	handle *bittorrent.Handle,
	snap bittorrent.Snapshot,
	seedingKept *bool,
) {
	progress := btChunkProgress(snap.Files)
	ranges := btChunkRanges(snap.Files)
	if err := s.st.UpdateTaskMeta(taskID, snap.Total, false, tk.IsAllocated, len(snap.Files)); err != nil {
		logging.Printf("scheduler: bt update meta: %v", err)
	}
	if err := s.st.UpdateTaskChunkRanges(taskID, ranges); err != nil {
		logging.Printf("scheduler: bt update chunk ranges: %v", err)
	}
	if err := s.st.UpdateTaskProgress(taskID, snap.Downloaded, progress, store.TaskStatus.Completed, ""); err != nil {
		logging.Printf("scheduler: bt mark completed: %v", err)
	}
	if rj != nil {
		rj.dirtyMu.Lock()
		if rj.task != nil {
			rj.task.Downloaded = snap.Downloaded
			rj.task.TotalSize = snap.Total
			rj.task.ChunkProgress = progress
			rj.task.ChunkRanges = ranges
			rj.task.Status = store.TaskStatus.Completed
		}
		rj.dirtyMu.Unlock()
	}
	if tk2, err := s.st.GetTask(taskID); err == nil {
		s.publish(Event{Why: "completed", Task: tk2})
	}
	// 做种:任务已 Completed、槽位即将释放,但客户端继续保持该种子上传。
	// 句柄因此不在 defer 里摘除,而是登记到 btSeeds,等删除任务或退出时停止。
	if handle.Seeding() {
		if s.keepSeeding(taskID, handle, snap.Total) {
			*seedingKept = true
			logging.Printf("scheduler: bt task %s completed, seeding registered", taskID)
		} else {
			logging.Printf("scheduler: bt task %s completed, seeding disabled", taskID)
		}
	}
}

// addBittorrentTorrent 按任务 URL 的形态把种子添加到客户端。
//
// 优先级:本地缓存的 info 字典(磁力链接首次抓到元数据后落库)→ 磁力
// 链接 → 远程 .torrent(http/https) → 本地 .torrent 文件。
func (s *Scheduler) addBittorrentTorrent(ctx context.Context, client *bittorrent.Manager, tk *store.Task) (*bittorrent.Handle, error) {
	dir := tk.SavePath
	raw := strings.TrimSpace(tk.URL)

	if info, err := s.st.LoadTorrentMetainfo(tk.ID); err != nil {
		logging.Printf("scheduler: load cached torrent metainfo: %v", err)
	} else if len(info) > 0 {
		// 缓存不可用(损坏 / 纯 v2 种子)时回退到原始来源,不让任务因为
		// 一条陈旧的缓存而失败。
		if h, aerr := client.AddInfoBytes(ctx, info, magnetTrackers(raw), dir); aerr == nil {
			return h, nil
		} else {
			logging.Printf("scheduler: cached torrent metainfo unusable, falling back to source: %v", aerr)
		}
	}

	if strings.HasPrefix(strings.ToLower(raw), "magnet:") {
		return client.AddMagnet(ctx, raw, dir)
	}
	data, ok, err := torrentFileBytes(ctx, raw)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("scheduler: unsupported bittorrent source: %s", raw)
	}
	return client.AddTorrentBytes(ctx, data, dir)
}

// torrentFileBytes 读取 .torrent 内容(HTTP 走代理、file:// 与本地路径
// 直接读;实现见 bittorrent.FetchTorrentFile)。第二个返回值表示该 URL
// 是否是种子文件来源。
func torrentFileBytes(ctx context.Context, raw string) ([]byte, bool, error) {
	if !protocol.IsBitTorrent(raw) {
		return nil, false, nil
	}
	data, err := bittorrent.FetchTorrentFile(ctx, raw, protocol.HTTPProxyFunc())
	return data, true, err
}

// magnetTrackers 从磁力链接里取出自带的 tracker 列表(从缓存的元数据
// 恢复时仍然能公告到同一批 tracker)。
func magnetTrackers(raw string) [][]string {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "magnet") {
		return nil
	}
	trs := u.Query()["tr"]
	out := make([][]string, 0, len(trs))
	for _, tr := range trs {
		if tr = strings.TrimSpace(tr); tr != "" {
			out = append(out, []string{tr})
		}
	}
	return out
}

// ---- 进度 → 分片布局 ----
//
// BT 任务没有「字节区间分片」的概念,但任务详情页按 [start,end] +
// 已完成字节 渲染马赛克。这里把「每个种子文件」当作一个分片:区间按文件
// 在种子内的偏移依次拼接,进度取该文件的已完成字节。单文件种子因此是
// 一整块,多文件种子则一块一个文件。
func btChunkRanges(files []bittorrent.FileProgress) []int64 {
	if len(files) == 0 {
		return nil
	}
	ranges := make([]int64, 0, 2*len(files))
	var offset int64
	for _, f := range files {
		if f.Length <= 0 {
			// 零长度文件(占位目录):给一个空区间,保持与进度数组一一对应。
			ranges = append(ranges, offset, offset-1)
			continue
		}
		ranges = append(ranges, offset, offset+f.Length-1)
		offset += f.Length
	}
	return ranges
}

// btChunkProgress 把每个文件的已完成字节换算为「相对分片起点的偏移」。
func btChunkProgress(files []bittorrent.FileProgress) []int64 {
	if len(files) == 0 {
		return nil
	}
	out := make([]int64, 0, len(files))
	for _, f := range files {
		done := f.Completed
		if done < 0 {
			done = 0
		}
		if f.Length > 0 && done > f.Length {
			done = f.Length
		}
		out = append(out, done)
	}
	return out
}

// BittorrentDefaultDir 推导 BT 任务的默认目录名:磁力链接用 "dn" 参数
// (没有则用 infohash 前缀),.torrent 用去掉后缀的文件名。UI 与命令行
// 路径共用它,保证两者得到同一个目录名。
func BittorrentDefaultDir(rawURL string) string {
	if !protocol.IsBitTorrent(rawURL) {
		return ""
	}
	lower := strings.ToLower(strings.TrimSpace(rawURL))
	if strings.HasPrefix(lower, "magnet:") {
		if name := protocol.MagnetName(rawURL); name != "" {
			return protocol.SafeDirName(name, "")
		}
		if ih := protocol.MagnetInfoHash(rawURL); ih != "" {
			if len(ih) > 16 {
				ih = ih[:16]
			}
			return "magnet-" + ih
		}
		return "magnet"
	}
	if name := protocol.TorrentNameFromPath(rawURL); name != "" {
		return protocol.SafeDirName(name, "")
	}
	return ""
}

// IsBittorrentTask 报告一个任务是否走 BT 下载路径。历史数据里 protocol
// 列为空的任务按 URL 形态兜底判定。
func IsBittorrentTask(tk *store.Task) bool {
	if tk == nil {
		return false
	}
	if protocol.ProtocolKind(tk.Protocol) == protocol.ProtoBT {
		return true
	}
	if tk.Protocol != "" {
		return false
	}
	return protocol.IsBitTorrent(tk.URL)
}

// removeTaskData 删除任务在磁盘上的数据。普通任务的 SavePath 是一个文件;
// BT 任务的 SavePath 是 lgdm 为该任务专建的目录(种子内容与 .part 临时
// 文件都在里面),因此整体删除。
func removeTaskData(tk *store.Task) {
	if tk == nil || tk.SavePath == "" {
		return
	}
	if IsBittorrentTask(tk) {
		_ = os.RemoveAll(tk.SavePath)
		return
	}
	_ = os.Remove(tk.SavePath)
}
