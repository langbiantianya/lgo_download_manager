// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package appservice 是业务侧的应用服务：把 scheduler / store / bittorrent
// 的能力包装成与传输无关的契约（api 包）。传输层（gRPC / net/http）只认
// 这层契约，不直接接触数据库与调度器内部。
package appservice

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/bittorrent"
	"lgo_download_manager/internal/logging"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/settings"
	"lgo_download_manager/internal/store"
)

// Service 实现契约层的 Handler。
type Service struct {
	st *store.Store
	sc *scheduler.Scheduler

	mu       sync.Mutex
	settings store.Settings

	// controls 是控制消息通道（显示窗口 / 弹对话框 / 语言变更 / 退出）。
	controls chan api.Message

	// OnLanguageChanged 在保存设置导致语言变化时被调用。由 uimgr 注册，
	// 用来把语言变更推给 UI。
	OnLanguageChanged func(lang string)
}

// ControlBuffer 是控制通道的缓冲容量。
//
// 它决定了「UI 子进程还没连上来」这段窗口里能攒下多少条控制消息。
// 最典型的是 show_add_task：业务侧拉起 UI 后立刻推消息，此时通道上还没有
// 消费者；缓冲为 0 的话这条消息会当场被丢弃，而调用方仍收到「已送达」。
//
// 16 是经验值——正常冷启动最多攒一两条（show / show_add_task / language），
// 留足余量是为了容忍异常情况下的短时积压。
const ControlBuffer = 16

// New 创建应用服务。settings 由调用方通过 SetSettings 注入。
func New(st *store.Store, sc *scheduler.Scheduler) *Service {
	return &Service{
		st:       st,
		sc:       sc,
		controls: make(chan api.Message, ControlBuffer),
	}
}

// SetSettings 更新服务持有的权威设置快照，并把设置应用到调度器。
//
// 「应用到调度器」这一段是从旧的 internal/uimgr.Manager.SetSettings 搬
// 过来的：用户在 UI 的「设置」里改并发上限/预分配/限速/BT 配置后，这
// 些参数要立刻生效，而不是等到下次启动。放在这里而不是 uimgr，是因为
// 「保存设置」这条路径现在完全在 appservice 内部（uimgr 不再参与分派）。
func (s *Service) SetSettings(cur store.Settings) {
	s.mu.Lock()
	s.settings = cur
	s.mu.Unlock()
	s.applyToScheduler(cur)
}

// applyToScheduler 把设置推给调度器。调度器为空时什么都不做——
// 契约测试里存在不带调度器的 Service。
func (s *Service) applyToScheduler(cur store.Settings) {
	if s.sc == nil {
		return
	}
	// 并发上限推到 scheduler：改小之后超限的任务留在 Pending；改大之后
	// promotePending 会把之前排队的任务提上来。
	s.sc.SetMaxConcurrent(cur.EffectiveMaxConcurrent())
	s.sc.SetPrealloc(cur.Prealloc)
	s.sc.SetDownloadLimitKiB(cur.DownloadLimitKiB)
	// BitTorrent 设置同步：限速立即生效，端口/DHT/PEX 等在客户端可安全
	// 重建时生效（见 scheduler.SetBittorrentConfig）。
	s.sc.SetBittorrentConfig(scheduler.BittorrentConfigFrom(cur))
}

// ValidateFileExistence 校验运行中任务的落地文件是否还在。
//
// 业务进程在 UI 会话存活期间按周期调用它（见 internal/uimgr）：下载中的
// 任务对应的文件被用户删掉时，任务要落到「文件缺失」状态而不是永远挂着。
func (s *Service) ValidateFileExistence() {
	if s.sc == nil {
		return
	}
	s.sc.ValidateFileExistence()
}

// Settings 返回当前权威设置快照。
func (s *Service) Settings() store.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// PushControl 往控制消息通道推一条消息。通道满时丢弃——控制消息是
// 「尽力而为」的通知，丢了不该阻塞业务侧。
func (s *Service) PushControl(msg api.Message) {
	select {
	case s.controls <- msg:
	default:
	}
}

// Controls 返回控制消息通道。
func (s *Service) Controls() <-chan api.Message { return s.controls }

// Events 订阅调度器事件流。退订函数由调度器提供。
func (s *Service) Events() (<-chan api.Event, func()) {
	// 没有调度器时不能 Subscribe——那会 nil panic。返回一条**已关闭**的
	// 通道：调用方读它会立刻收到零值与 ok=false，正好是「事件流结束」的
	// 语义；退订函数是个空操作。
	//
	// 与 Call 入口的 needsScheduler 守卫是同一个道理：New(nil, nil) 是合法
	// 构造，收到真实调用时必须返回错误或空流，不能崩。
	if s.sc == nil {
		ch := make(chan api.Event)
		close(ch)
		return ch, func() {}
	}
	ch, unsub := s.sc.Subscribe()
	out := make(chan api.Event, cap(ch))
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(out)
		for {
			select {
			case <-stop:
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- toAPIEvent(ev):
				default:
				}
			}
		}
	}()
	return out, func() {
		once.Do(func() {
			close(stop)
			unsub()
		})
	}
}

// needsScheduler 报告该方法是否依赖调度器。
//
// 这些方法都要读写任务队列，没有调度器就无从谈起；get_settings /
// save_settings / probe 则不碰队列，probe 走的是 HTTP 探测。
func needsScheduler(method string) bool {
	switch method {
	case api.MethodList, api.MethodAdd, api.MethodStart, api.MethodPause,
		api.MethodDelete, api.MethodIsPreparing, api.MethodBTInfo:
		return true
	}
	return false
}

// Call 执行一次契约层调用。in / out 的具体类型由 api.Spec(method) 决定。
func (s *Service) Call(ctx context.Context, method string, in, out any) error {
	// 需要调度器的方法在服务没有调度器时必须返回错误，不能 panic。
	//
	// New(nil, nil) 是合法构造——契约测试里大量用它来验证 DTO 形状，
	// UI 的测试替身也用它。但它不该在收到真实调用时崩掉：panic 会顺着
	// 传输层冒到服务端，把一次「配置问题」变成「进程挂掉」。
	if s.sc == nil && needsScheduler(method) {
		return fmt.Errorf("appservice: %s 需要调度器，但服务未配置", method)
	}
	switch method {
	case api.MethodList:
		p, ok := in.(*api.ListParams)
		if !ok {
			return fmt.Errorf("appservice: list 参数类型不对")
		}
		tasks, err := s.sc.List(store.StatusFilter(p.Filter), store.TaskSort(p.Sort))
		if err != nil {
			return err
		}
		outTasks := make([]*api.Task, 0, len(tasks))
		for _, t := range tasks {
			outTasks = append(outTasks, toAPITask(t))
		}
		res, ok := out.(*api.ListResult)
		if !ok {
			return fmt.Errorf("appservice: list 结果类型不对")
		}
		res.Tasks = outTasks
		return nil

	case api.MethodAdd:
		p, ok := in.(*api.AddTaskInput)
		if !ok {
			return fmt.Errorf("appservice: add 参数类型不对")
		}
		kind := p.Protocol
		if kind == "" {
			var derr error
			kind, derr = protocol.DetectKind(p.URL, "")
			if derr != nil {
				return derr
			}
		}
		tk, err := s.sc.Add(scheduler.AddTaskInput{
			URL:          p.URL,
			SavePath:     p.SavePath,
			Protocol:     kind,
			Auth:         toProtocolAuthOptions(p.Auth),
			ChunkCount:   p.ChunkCount,
			MinChunkSize: p.MinChunkSize,
		})
		if err != nil {
			return err
		}
		res, ok := out.(*api.Task)
		if !ok {
			return fmt.Errorf("appservice: add 结果类型不对")
		}
		*res = *toAPITask(tk)
		return nil

	case api.MethodStart:
		p, ok := in.(*api.TaskRef)
		if !ok {
			return fmt.Errorf("appservice: start 参数类型不对")
		}
		return s.sc.Start(p.ID)

	case api.MethodPause:
		p, ok := in.(*api.TaskRef)
		if !ok {
			return fmt.Errorf("appservice: pause 参数类型不对")
		}
		return s.sc.Pause(p.ID)

	case api.MethodDelete:
		p, ok := in.(*api.TaskRef)
		if !ok {
			return fmt.Errorf("appservice: delete 参数类型不对")
		}
		return s.sc.Delete(p.ID)

	case api.MethodIsPreparing:
		p, ok := in.(*api.TaskRef)
		if !ok {
			return fmt.Errorf("appservice: is_preparing 参数类型不对")
		}
		res, ok := out.(*api.BoolResult)
		if !ok {
			return fmt.Errorf("appservice: is_preparing 结果类型不对")
		}
		res.Value = s.sc.IsPreparing(p.ID)
		return nil

	case api.MethodProbe:
		p, ok := in.(*api.ProbeParams)
		if !ok {
			return fmt.Errorf("appservice: probe 参数类型不对")
		}
		probe, err := s.probe(ctx, p.URL)
		if err != nil {
			return err
		}
		res, ok := out.(*api.ProbeResult)
		if !ok {
			return fmt.Errorf("appservice: probe 结果类型不对")
		}
		res.TotalSize = probe.totalSize
		res.Filename = probe.filename
		return nil

	case api.MethodGetSettings:
		res, ok := out.(*api.Settings)
		if !ok {
			return fmt.Errorf("appservice: get_settings 结果类型不对")
		}
		*res = toAPISettings(s.Settings())
		return nil

	case api.MethodSaveSettings:
		p, ok := in.(*api.Settings)
		if !ok {
			return fmt.Errorf("appservice: save_settings 参数类型不对")
		}
		return s.saveSettings(*p)

	case api.MethodBTInfo:
		p, ok := in.(*api.BTInfoParams)
		if !ok {
			return fmt.Errorf("appservice: bt_info 参数类型不对")
		}
		d, err := s.sc.BTTaskDetail(p.TaskID)
		if err != nil {
			return err
		}
		res, ok := out.(*api.BTDetail)
		if !ok {
			return fmt.Errorf("appservice: bt_info 结果类型不对")
		}
		*res = *toAPIBTDetail(d)
		return nil

	case api.MethodStreamMessages:
		// 消息流不走通用分派——传输层直接读 Events() 与 Controls() 两条流。
		// 这里显式分支，免得它掉进下面的「未知方法」默认分支，让排障时
		// 误以为契约里没有这个方法。
		return fmt.Errorf("appservice: 方法 %q 不走通用分派", method)

	default:
		return fmt.Errorf("appservice: 未知方法 %q", method)
	}
}

// saveSettings 持久化设置并同步权威快照。语言变化时通知调用方。
func (s *Service) saveSettings(in api.Settings) error {
	st := toStoreSettings(in)
	if err := settings.Save(s.st, st); err != nil {
		return err
	}
	prev := s.Settings().Language
	s.SetSettings(st)
	if st.Language != prev && s.OnLanguageChanged != nil {
		s.OnLanguageChanged(st.Language)
	}
	return nil
}

// probe 在业务进程内探测 URL 目标大小（复用业务侧代理配置）。
//
// ctx 由调用方传入：超时以它为基准派生，调用方取消能立即中止探测，
// 不会让一次已取消的请求继续占住处理协程。
type probeMetadata struct {
	totalSize int64
	filename  string
}

func (s *Service) probe(ctx context.Context, raw string) (*probeMetadata, error) {
	kind, err := protocol.DetectKind(raw, "")
	if err != nil {
		return nil, err
	}
	if kind == protocol.ProtoBT {
		size, err := s.probeBittorrent(ctx, raw)
		return &probeMetadata{totalSize: size}, err
	}
	cur := s.Settings()
	driver, err := protocol.New(raw, kind, protocol.Auth{AuthOptions: protocol.AuthOptions{
		UserAgent:          cur.UserAgent,
		Cookies:            cur.Cookies,
		FTPPassive:         cur.FTPPassive,
		ProxyMode:          cur.ProxyMode,
		ProxyURL:           cur.ProxyURL,
		ProxyBypass:        cur.ProxyBypass,
		ProxyAutoConfigURL: cur.ProxyAutoConfigURL,
	}})
	if err != nil {
		return nil, err
	}
	defer driver.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	caps, err := driver.Probe(ctx)
	if err != nil {
		return nil, err
	}
	return &probeMetadata{totalSize: caps.TotalSize, filename: caps.Filename}, nil
}

// probeBittorrent 解析 .torrent 种子文件的总体积。磁力链接缺元数据，
// 返回 0(体积未知)。任何取种子的失败都当作「未知」而不是错误——
// 体积预览不该拦住用户添加任务；失败原因记日志，便于排障。
//
// ctx 与 probe 同理：调用方取消能立即中止种子抓取（否则最坏要等 20s）。
func (s *Service) probeBittorrent(ctx context.Context, raw string) (int64, error) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "magnet:") {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		data, err := bittorrent.FetchTorrentFile(ctx, raw, protocol.HTTPProxyFunc())
		if err != nil {
			logging.Printf("appservice: probe torrent file: %v", err)
			return 0, nil
		}
		if _, total, perr := bittorrent.TorrentFileInfo(data); perr == nil {
			return total, nil
		} else {
			logging.Printf("appservice: parse torrent file: %v", perr)
		}
	}
	return 0, nil
}
