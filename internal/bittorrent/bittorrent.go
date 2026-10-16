// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package bittorrent 基于 anacrolix/torrent 为 lgdm 提供 BitTorrent 下载。
//
// 设计要点:
//
//   - 进程内共享一个 torrent.Client(多任务共用同一个 TCP/uTP 监听端口、
//     一份 DHT 路由表与一份连接池),每个任务只持有自己的 *torrent.Torrent
//     句柄与各自的数据目录;
//   - 每个任务通过 storage.NewFileOpts(ClientBaseDir=任务目录) 指定独立的
//     存储根目录,种子内容因此落在 <任务目录>/<种子名>/... 之下(单文件
//     种子则是 <任务目录>/<文件名>);
//   - 数据先写入 ".part" 文件,该文件全部下载完成后由 anacrolix 重命名为
//     正式文件名——「任务目录里出现正式文件」即「该文件已完成」的天然
//     标记,断点续传因此不需要额外的状态文件;
//   - 下载/上传限速通过原地调整 rate.Limiter 生效;监听端口、DHT、PEX、
//     连接数上限这类客户端构造期参数则需要在客户端重建时生效:Manager
//     在没有活动任务时立刻重建,否则延后到最后一个任务结束(见 ApplyConfig)。
//
// 与 HTTP 分片引擎的关系:scheduler 对 BT 任务不再构造 ProtocolDriver,
// 而是走本包的 Manager/Handle,进度由 Handle.Snapshot 提供。
package bittorrent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"

	"lgo_download_manager/internal/logging"
)

// ErrAlreadyAdded 表示同一个种子(infohash)已经在当前客户端里下载。
// scheduler 用它判定「重复添加」并给出可读错误,而不是让两个任务
// 共享同一个 Torrent(那会让两者的进度互相覆盖)。
var ErrAlreadyAdded = errors.New("bittorrent: torrent already added")

// Config 是 BT 客户端的运行参数,由 scheduler 从 store.Settings 映射而来。
type Config struct {
	// ListenPort 是 BT 客户端的 TCP/uTP 监听端口;0 表示由系统随机分配。
	ListenPort int
	// MaxPeersPerTorrent 是每个种子允许建立的已连接 peer 数上限;<=0 用默认值。
	MaxPeersPerTorrent int
	// DownloadLimit / UploadLimit 是客户端级限速(字节/秒);<=0 表示不限速。
	DownloadLimit int64
	UploadLimit   int64
	// EnableDHT 控制是否启用 DHT(磁力链接无 tracker 时的主要 peer 来源)。
	EnableDHT bool
	// EnablePEX 控制是否启用 Peer Exchange(节点交换)。
	EnablePEX bool
	// Seeding 为 true 时下载完成后继续做种上传;为 false 时任务完成即断开。
	Seeding bool
	// ExtraTrackers 是追加到每个任务的 tracker 列表(逗号/换行已在
	// scheduler 侧拆分)。磁力链接自带 tracker 较少时尤其有用。
	ExtraTrackers []string
	// HTTPProxy 供 tracker 公告 / webseed / HTTP 抓取元数据使用的代理
	// 解析函数,通常来自 protocol.HTTPProxyFunc()。
	HTTPProxy func(*http.Request) (*url.URL, error)
}

// defaultMaxPeersPerTorrent 与 anacrolix/torrent 的
// EstablishedConnsPerTorrent 默认值保持一致。
const defaultMaxPeersPerTorrent = 50

// Manager 拥有进程内共享的 BT 客户端。
//
// 并发安全:所有导出方法都可以从多个 goroutine 调用。
type Manager struct {
	mu sync.Mutex
	// cfg 是最近一次生效的配置;重启客户端的延迟重建也以它为准。
	cfg Config
	cl  *torrent.Client
	// dl/ul 是客户端持有的限速器指针,原地 SetLimit 即可动态改限速,
	// 因此不必为了改限速重建客户端。
	dl, ul *rate.Limiter
	// active 是当前已加入客户端的种子数(下载中 + 做种中)。
	active int
	// restartPending 表示配置里存在只能在客户端构造期生效的改动,
	// 等 active 归零后重建客户端。
	restartPending bool
	closed         bool
}

// Handle 是单个 BT 任务的下载句柄。
type Handle struct {
	m   *Manager
	t   *torrent.Torrent
	dir string
	st  storage.ClientImplCloser

	once sync.Once
	// done 在 Close 时关闭,用于让「等元数据再标记下载」的 goroutine 退出。
	done chan struct{}

	sampleMu   sync.Mutex
	lastBytes  int64
	lastSample time.Time
	speed      float64
}

// FileProgress 是单个种子文件(相对任务目录)的进度。
type FileProgress struct {
	// Path 是文件相对任务目录的路径(含种子名目录,使用 '/' 分隔,
	// 与 anacrolix 默认文件存储的落盘布局一致):
	// 单文件种子 = <种子名>,多文件种子 = <种子名>/<种子内相对路径>。
	Path string
	// Length 是文件字节数;Completed 是已完成字节数。
	Length    int64
	Completed int64
}

// Snapshot 是一次进度采样:一次调用取齐 UI/调度器需要的全部易变字段。
type Snapshot struct {
	Name       string
	Total      int64
	Downloaded int64
	SpeedBPS   float64
	Peers      int
	Seeding    bool
	Complete   bool
	Files      []FileProgress
}

// New 构造 Manager 并立即创建底层客户端。cfg 中的代理函数可以为 nil。
func New(cfg Config) (*Manager, error) {
	m := &Manager{cfg: cfg}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.startClientLocked(); err != nil {
		return nil, err
	}
	return m, nil
}

// ApplyConfig 应用新的设置。限速会立刻生效;监听端口 / DHT / PEX /
// 连接数上限等构造期参数只在客户端重建后生效——没有活动任务时立即
// 重建,有任务时延后到最后一个任务结束(见 release)。
func (m *Manager) ApplyConfig(cfg Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.cfg
	m.cfg = cfg
	if m.cl == nil || m.closed {
		return
	}
	m.applyLimitsLocked()
	if requiresRestart(old, cfg) {
		if m.active == 0 {
			if err := m.restartLocked(); err != nil {
				logging.Printf("bittorrent: restart client: %v", err)
			}
			return
		}
		m.restartPending = true
	}
}

// Close 关闭底层客户端并结束所有任务。之后不应再调用其它方法。
func (m *Manager) Close() error {
	m.mu.Lock()
	cl := m.cl
	m.cl = nil
	m.closed = true
	m.active = 0
	m.mu.Unlock()
	if cl == nil {
		return nil
	}
	// anacrolix 的 Close 返回错误切片,合并成一个便于日志阅读的错误。
	if errs := cl.Close(); len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// Active 返回当前已加入客户端的种子数(下载中 + 做种中)。
func (m *Manager) Active() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

// startClientLocked 用当前 cfg 创建客户端。调用方须持有 m.mu。
func (m *Manager) startClientLocked() error {
	cfg := m.cfg
	tc := torrent.NewDefaultClientConfig()
	tc.ListenPort = cfg.ListenPort // 0 => 系统随机分配
	tc.NoDHT = !cfg.EnableDHT
	tc.DisablePEX = !cfg.EnablePEX
	tc.Seed = cfg.Seeding
	maxPeers := cfg.MaxPeersPerTorrent
	if maxPeers <= 0 {
		maxPeers = defaultMaxPeersPerTorrent
	}
	tc.EstablishedConnsPerTorrent = maxPeers
	tc.HalfOpenConnsPerTorrent = maxPeers / 2
	if tc.HalfOpenConnsPerTorrent < 1 {
		tc.HalfOpenConnsPerTorrent = 1
	}
	// 不主动改路由器配置(UPnP/NAT-PMP):BT 需要用户自行在路由器上做
	// 端口映射时才开放入站连接,lgdm 不在用户不知情的情况下修改网关设置。
	tc.NoDefaultPortForwarding = true
	tc.HTTPProxy = cfg.HTTPProxy
	// 客户端级默认存储:每个任务都会在 spec.Storage 里指定自己的任务目录,
	// 这里只是「没有显式存储时」的兜底。必须显式设置——anacrolix 在
	// DefaultStorage 为 nil 时会构造一个「按目录的 bolt 分片完成库」,
	// DataDir 为空时那就是进程的工作目录,等于在用户的 CWD 里凭空
	// 生成一个 .torrent.bolt.db。这里用「内存完成状态 + .part 文件」
	// 的组合(与每任务存储一致),不落任何额外文件。
	// ClientBaseDir 留空是安全的:lgdm 的每个任务都带 spec.Storage,
	// 客户端级默认存储实际上不会被用来落数据。
	tc.DefaultStorage = storage.NewFileOpts(storage.NewFileClientOpts{
		PieceCompletion: storage.NewMapPieceCompletion(),
	})
	if lg := logging.L(); lg != nil {
		tc.Slogger = lg
	}
	m.dl = newLimiter(cfg.DownloadLimit, 1<<20)
	m.ul = newLimiter(cfg.UploadLimit, 1<<17)
	tc.DownloadRateLimiter = m.dl
	tc.UploadRateLimiter = m.ul

	cl, err := torrent.NewClient(tc)
	if err != nil {
		return fmt.Errorf("bittorrent: new client: %w", err)
	}
	m.cl = cl
	m.restartPending = false
	return nil
}

// applyLimitsLocked 把当前 cfg 的限速值原地写进限速器(无需重建客户端)。
func (m *Manager) applyLimitsLocked() {
	setLimit(m.dl, m.cfg.DownloadLimit, 1<<20)
	setLimit(m.ul, m.cfg.UploadLimit, 1<<17)
}

// requiresRestart 报告从 old 到 cfg 是否有必须重建客户端的字段。
// 限速(下载/上传)是原地调整的,不参与比较。
func requiresRestart(old, cfg Config) bool {
	if old.ListenPort != cfg.ListenPort ||
		old.MaxPeersPerTorrent != cfg.MaxPeersPerTorrent ||
		old.EnableDHT != cfg.EnableDHT ||
		old.EnablePEX != cfg.EnablePEX ||
		old.Seeding != cfg.Seeding ||
		strings.Join(old.ExtraTrackers, ",") != strings.Join(cfg.ExtraTrackers, ",") ||
		(old.HTTPProxy == nil) != (cfg.HTTPProxy == nil) {
		return true
	}
	return false
}

// restartLocked 关闭旧客户端并按当前 cfg 重新创建(仅在 active==0 时调用)。
func (m *Manager) restartLocked() error {
	old := m.cl
	m.cl = nil
	if old != nil {
		if errs := old.Close(); len(errs) > 0 {
			logging.Printf("bittorrent: close old client: %v", errors.Join(errs...))
		}
	}
	return m.startClientLocked()
}

// clientLocked 返回当前客户端,必要时先把延后的重启做掉。调用方须持有 m.mu。
func (m *Manager) clientLocked() (*torrent.Client, error) {
	if m.closed {
		return nil, errors.New("bittorrent: manager closed")
	}
	if m.restartPending && m.active == 0 {
		if err := m.restartLocked(); err != nil {
			return nil, err
		}
	}
	if m.cl == nil {
		if err := m.startClientLocked(); err != nil {
			return nil, err
		}
	}
	return m.cl, nil
}

// release 在句柄关闭后调用:active 归零且有待生效的配置时重建客户端。
func (m *Manager) release() {
	m.mu.Lock()
	if m.active > 0 {
		m.active--
	}
	if m.active == 0 && m.restartPending && m.cl != nil && !m.closed {
		if err := m.restartLocked(); err != nil {
			logging.Printf("bittorrent: deferred restart: %v", err)
		}
	}
	m.mu.Unlock()
}

// AddMagnet 用磁力链接添加一个任务。元数据(种子名、文件列表)可能
// 尚未拿到,调用方可用 Handle.WaitInfo 等待。
func (m *Manager) AddMagnet(ctx context.Context, magnetURI, dir string) (*Handle, error) {
	spec, err := torrent.TorrentSpecFromMagnetUri(magnetURI)
	if err != nil {
		return nil, fmt.Errorf("bittorrent: parse magnet: %w", err)
	}
	return m.addSpec(ctx, spec, dir)
}

// AddTorrentBytes 用 .torrent 文件内容添加一个任务。
func (m *Manager) AddTorrentBytes(ctx context.Context, data []byte, dir string) (*Handle, error) {
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("bittorrent: parse torrent file: %w", err)
	}
	spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		return nil, fmt.Errorf("bittorrent: decode torrent info: %w", err)
	}
	return m.addSpec(ctx, spec, dir)
}

// AddInfoBytes 用本地缓存的 info 字典(见 store.SaveTorrentMetainfo)恢复
// 一个任务:恢复磁力链接时无需再次从 DHT/tracker 抓元数据,离线也能续传。
//
// 只接受 v1(BTIH)info:纯 v2(BEP 52)种子的 infohash 不是「info 字典的
// SHA-1」,无法由 info 字节反推,调用方应回退到磁力链接本身。
func (m *Manager) AddInfoBytes(ctx context.Context, info []byte, trackers [][]string, dir string) (*Handle, error) {
	parsed, err := (&metainfo.MetaInfo{InfoBytes: info}).UnmarshalInfo()
	if err != nil {
		return nil, fmt.Errorf("bittorrent: decode cached info: %w", err)
	}
	if !parsed.HasV1() {
		return nil, errors.New("bittorrent: cached info is not a v1 (BTIH) torrent")
	}
	spec := &torrent.TorrentSpec{
		Trackers: trackers,
		AddTorrentOpts: torrent.AddTorrentOpts{
			InfoBytes: info,
			InfoHash:  metainfo.HashBytes(info),
		},
	}
	return m.addSpec(ctx, spec, dir)
}

// TorrentFileInfo 解析 .torrent 文件内容,返回种子名与总字节数。
// 供「新建任务」对话框在下载前预览体积,以及 UI 推导默认任务目录名。
func TorrentFileInfo(data []byte) (name string, total int64, err error) {
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return "", 0, fmt.Errorf("bittorrent: parse torrent file: %w", err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return "", 0, fmt.Errorf("bittorrent: decode torrent info: %w", err)
	}
	return info.BestName(), info.TotalLength(), nil
}

// InfoBytesFromTorrentFile 抽出 .torrent 里的 info 字典原始字节,
// 供调用方缓存/复用(见 AddInfoBytes)。用做种端已知的种子文件直接
// 恢复任务时很有用。
func InfoBytesFromTorrentFile(data []byte) ([]byte, error) {
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("bittorrent: parse torrent file: %w", err)
	}
	return mi.InfoBytes, nil
}

// addSpec 是把 TorrentSpec 落到客户端的唯一路径。添加本身是同步且快速的
// (不等待元数据/peer),ctx 只用于在调用方已经取消时快速失败。
func (m *Manager) addSpec(ctx context.Context, spec *torrent.TorrentSpec, dir string) (*Handle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dir == "" {
		return nil, errors.New("bittorrent: empty data dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("bittorrent: create data dir: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cl, err := m.clientLocked()
	if err != nil {
		return nil, err
	}
	// 每个任务一份存储:数据落在 <dir>/<种子名>/ 之下,而 .part 文件
	// 与正式文件同目录,因此任务目录自解释、可整体搬迁。
	st := storage.NewFileOpts(storage.NewFileClientOpts{ClientBaseDir: dir})
	spec.Storage = st
	for _, tr := range m.cfg.ExtraTrackers {
		spec.Trackers = append(spec.Trackers, []string{tr})
	}
	t, isNew, err := cl.AddTorrentSpec(spec)
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("bittorrent: add torrent: %w", err)
	}
	if !isNew {
		_ = st.Close()
		return nil, ErrAlreadyAdded
	}
	m.active++
	return &Handle{m: m, t: t, dir: dir, st: st, done: make(chan struct{}), lastSample: time.Now()}, nil
}

// ---- Handle ----

// WaitInfo 等待种子元数据可用(磁力链接需要先从 peer 抓到 info 字典)。
func (h *Handle) WaitInfo(ctx context.Context) error {
	ch := h.t.GotInfo()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// StartDownloading 让客户端开始下载种子的全部内容。
//
// 元数据已在(.torrent / 缓存的 info)时立即标记;磁力链接需要先抓到
// info 字典——此时挂一个 goroutine 等元数据到达后再标记(anacrolix
// 的 Torrent.DownloadAll 在 info 就绪前会解引用空 info 直接 panic,
// 因此绝不能无条件调用)。句柄关闭时该 goroutine 会退出。
func (h *Handle) StartDownloading() {
	if h.t.Info() != nil {
		h.t.DownloadAll()
		return
	}
	go func() {
		select {
		case <-h.t.GotInfo():
			h.t.DownloadAll()
		case <-h.done:
		}
	}()
}

// Dir 返回任务的存储根目录。
func (h *Handle) Dir() string { return h.dir }

// Name 返回种子名(元数据未到时返回空串)。
func (h *Handle) Name() string { return h.t.Name() }

// Seeding 报告当前任务是否处于做种状态(数据已完整且愿意未取偿上传)。
func (h *Handle) Seeding() bool { return h.t.Seeding() }

// InfoBytes 返回种子的 info 字典(未拿到元数据时为 nil),供持久化缓存。
func (h *Handle) InfoBytes() []byte {
	mi := h.t.Metainfo()
	if len(mi.InfoBytes) == 0 {
		return nil
	}
	return mi.InfoBytes
}

// Trackers 返回当前种子的 tracker 分层列表,供缓存后恢复时复用。
func (h *Handle) Trackers() [][]string {
	mi := h.t.Metainfo()
	return mi.UpvertedAnnounceList()
}

// Snapshot 采样一次进度:总字节数按「各文件长度之和」统计(不含
// anacrolix 内部的 piece 对齐填充),因此恒有 Downloaded <= Total,
// 进度条不会超过 100%。
func (h *Handle) Snapshot() Snapshot {
	snap := Snapshot{Name: h.t.Name()}
	// 元数据未到时 anacrolix 还没有初始化文件表(initFiles 在 info 就绪时
	// 才跑),读 Files() 会解引用空指针直接崩溃。磁力链接在找到持有该
	// 元数据的 peer 之前会长时间停在这个分支:只报告连接数与速度。
	if h.t.Info() == nil {
		snap.Peers = h.t.Stats().ActivePeers
		snap.SpeedBPS = h.measureSpeed(0)
		return snap
	}
	files := h.t.Files()
	snap.Files = make([]FileProgress, 0, len(files))
	for _, f := range files {
		length := f.Length()
		done := f.BytesCompleted()
		if done > length {
			done = length
		}
		snap.Total += length
		snap.Downloaded += done
		snap.Files = append(snap.Files, FileProgress{
			// File.Path() 即「相对存储根目录的落盘路径」,UI 用它拼出
			// 完成后的文件绝对路径。注意不要用 DisplayPath():它不含
			// 种子名目录(单文件种子除外),且在当前版本恒返回空串。
			Path:      filepath.ToSlash(f.Path()),
			Length:    length,
			Completed: done,
		})
	}
	if len(files) == 0 {
		// 元数据还没到:只能报告 infohash 对应的种子名(可能为空)。
		snap.Total = 0
		snap.Downloaded = 0
	}
	stats := h.t.Stats()
	snap.Peers = stats.ActivePeers
	snap.Seeding = h.t.Seeding()
	snap.Complete = h.t.Complete().Bool()
	snap.SpeedBPS = h.measureSpeed(snap.Downloaded)
	return snap
}

// measureSpeed 用两次采样的字节差除以时间差得到瞬时速度。
// 采样由调度器按固定周期调用,首次采样速度为 0。
func (h *Handle) measureSpeed(downloaded int64) float64 {
	h.sampleMu.Lock()
	defer h.sampleMu.Unlock()
	now := time.Now()
	dt := now.Sub(h.lastSample).Seconds()
	diff := downloaded - h.lastBytes
	h.lastBytes = downloaded
	h.lastSample = now
	if dt <= 0 || diff <= 0 {
		h.speed = 0
		return 0
	}
	h.speed = float64(diff) / dt
	return h.speed
}

// Complete 报告种子数据是否已全部完成(含做种阶段)。
func (h *Handle) Complete() bool { return h.t.Complete().Bool() }

// Close 从客户端摘除种子并释放其存储句柄(幂等)。磁盘上的数据保留,
// 下次 Start 时按 .part / 正式文件名重新识别已完成的部分。
func (h *Handle) Close() {
	h.once.Do(func() {
		// 先唤醒「等元数据再标记下载」的 goroutine,再摘除种子。
		close(h.done)
		h.t.Drop()
		if h.st != nil {
			_ = h.st.Close()
		}
		h.m.release()
	})
}

// ---- 内部工具 ----

// newLimiter 把 bytesPerSec(<=0 表示不限速)转成 rate.Limiter。
// burst 至少能装下一个典型的 protobuf 块,避免限速把吞吐压到极低。
func newLimiter(bytesPerSec, burst int64) *rate.Limiter {
	if bytesPerSec <= 0 {
		return rate.NewLimiter(rate.Inf, int(burst))
	}
	return rate.NewLimiter(rate.Limit(bytesPerSec), int(burst))
}

// setLimit 原地调整限速器的速率与突发量。
func setLimit(l *rate.Limiter, bytesPerSec, burst int64) {
	if l == nil {
		return
	}
	if bytesPerSec <= 0 {
		l.SetLimit(rate.Inf)
		l.SetBurst(int(burst))
		return
	}
	l.SetBurst(int(burst))
	l.SetLimit(rate.Limit(bytesPerSec))
}
