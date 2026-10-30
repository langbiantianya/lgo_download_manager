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

// New 创建应用服务。settings 由调用方通过 SetSettings 注入。
func New(st *store.Store, sc *scheduler.Scheduler) *Service {
	return &Service{
		st:       st,
		sc:       sc,
		controls: make(chan api.Message, 16),
	}
}

// SetSettings 更新服务持有的权威设置快照。
func (s *Service) SetSettings(cur store.Settings) {
	s.mu.Lock()
	s.settings = cur
	s.mu.Unlock()
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

// Call 执行一次契约层调用。in / out 的具体类型由 api.Spec(method) 决定。
func (s *Service) Call(ctx context.Context, method string, in, out any) error {
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
		size, err := s.probe(p.URL)
		if err != nil {
			return err
		}
		res, ok := out.(*api.ProbeResult)
		if !ok {
			return fmt.Errorf("appservice: probe 结果类型不对")
		}
		res.TotalSize = size
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
func (s *Service) probe(raw string) (int64, error) {
	kind, err := protocol.DetectKind(raw, "")
	if err != nil {
		return 0, err
	}
	if kind == protocol.ProtoBT {
		return s.probeBittorrent(raw)
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
		return 0, err
	}
	defer driver.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	caps, err := driver.Probe(ctx)
	if err != nil {
		return 0, err
	}
	return caps.TotalSize, nil
}

// probeBittorrent 解析 .torrent 种子文件的总体积。磁力链接缺元数据，
// 返回 0(体积未知)。任何取种子的失败都当作「未知」而不是错误——
// 体积预览不该拦住用户添加任务；失败原因记日志，便于排障。
func (s *Service) probeBittorrent(raw string) (int64, error) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "magnet:") {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
