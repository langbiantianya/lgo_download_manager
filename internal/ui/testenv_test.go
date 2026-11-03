// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

// 本文件仅测试用：生产代码（internal/ui 的非测试文件）只依赖契约层
// rpc.Client + api DTO + urlkind，不得 import store/scheduler/appservice；
// 这里的 import 只为了在测试里起一份与生产完全相同的业务侧链路
// （store + scheduler + appservice → rpc.Serve → 真实传输 → rpc.Dial），
// 让 UI 测试跑在真实的调用链上，而不是自己写一个 Service 适配器。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/appservice"
	"lgo_download_manager/internal/rpc"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// testTransports 是 UI 测试要覆盖的两种传输。前端只认 rpc.Client，
// 同一个用例在两种线格式上都要能跑通。
var testTransports = []string{rpc.TransportGRPC, rpc.TransportHTTP}

// testService 是一套真实业务侧服务与它的客户端。
type testService struct {
	client rpc.Client
	store  *store.Store
	sched  *scheduler.Scheduler
	svc    *appservice.Service
}

// openTestStore 在临时目录里打开一份真实 store，测试结束时关闭。
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "lgdm.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// startTestService 在临时 unix socket 上起一份业务侧服务（scheduler 已
// 运行、appservice 已装载 store 里的设置快照），返回一个完成握手的
// rpc.Client。transport 取 rpc.TransportHTTP / rpc.TransportGRPC。
//
// 调用方负责在前面用 st.SaveSettings 配好默认设置：appservice 的
// 快照在这里从 store 读一次，与生产启动顺序一致。
func startTestService(t *testing.T, st *store.Store, transport string) *testService {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	sc := scheduler.New(st)
	schedDone := make(chan struct{})
	go func() {
		sc.Run(ctx)
		close(schedDone)
	}()

	asvc := appservice.New(st, sc)
	cur, err := st.LoadSettings()
	if err != nil {
		cancel()
		<-schedDone
		t.Fatalf("LoadSettings: %v", err)
	}
	asvc.SetSettings(cur)

	sock := filepath.Join(t.TempDir(), "lgdm.sock")
	const token = "ui-test-token"
	served := make(chan error, 1)
	go func() {
		served <- rpc.Serve(ctx, rpc.ServerOptions{
			Transport: transport,
			Addr:      "unix:" + sock,
			Token:     token,
		}, asvc)
	}()

	// 等监听器就绪：socket 文件出现即说明已经 listen。
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-schedDone
			t.Fatalf("%s 传输的服务没有在超时前就绪", transport)
		}
		time.Sleep(5 * time.Millisecond)
	}

	cli, err := rpc.Dial(context.Background(), transport, "unix:"+sock, token)
	if err != nil {
		cancel()
		<-schedDone
		t.Fatalf("%s 传输连接失败: %v", transport, err)
	}

	t.Cleanup(func() {
		_ = cli.Close()
		// 与生产退出流程一致：先暂停所有任务（等写者停止），再关 BT
		// 客户端，否则 BT 任务的 goroutine 还会往任务目录里写文件，
		// 与临时目录清理并发（Windows 上表现为 "The directory is not
		// empty"，用例随机失败）。
		pauseCtx, pauseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = sc.PauseAll(pauseCtx)
		pauseCancel()
		cancel()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Errorf("%s 传输的服务没有在超时前停下", transport)
		}
		<-schedDone
		sc.CloseBittorrent()
	})

	return &testService{client: cli, store: st, sched: sc, svc: asvc}
}

// startUIE2E 是 e2e 用例的公共准备：建 store、写入配置、起业务侧服务、
// 连上指定传输的客户端，并把 GlobalSettings 设成客户端握手拿到的快照——
// 与生产里 NewMainWindow 用 svc.Settings() 初始化的路径一致。
func startUIE2E(t *testing.T, transport string, cfg store.Settings) *testService {
	t.Helper()
	st := openTestStore(t)
	if err := st.SaveSettings(cfg); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	// 生产里默认下载目录在设置加载时就存在（downloaddir.Default 会建），
	// 测试的临时目录不是——先建好，免得引擎因为父目录缺失直接失败。
	if cfg.DefaultSaveDir != "" {
		if err := os.MkdirAll(cfg.DefaultSaveDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", cfg.DefaultSaveDir, err)
		}
	}
	env := startTestService(t, st, transport)
	old := GlobalSettings
	GlobalSettings = env.client.Settings()
	t.Cleanup(func() { GlobalSettings = old })
	return env
}

// nopClient 是 rpc.Client 的测试替身：所有方法给出无害默认值，具体
// 用例通过内嵌覆盖自己关心的那一个。它是「UI 只依赖接口」的直接体现——
// 除了接口本身，替身不需要知道任何业务类型。
type nopClient struct{}

func (nopClient) List(api.StatusFilter, api.TaskSort) ([]*api.Task, error) { return nil, nil }

func (nopClient) Subscribe() (<-chan api.Event, func()) {
	return make(chan api.Event), func() {}
}

func (nopClient) AddTask(api.AddTaskInput) (*api.Task, error)  { return nil, nil }
func (nopClient) Start(string) error                           { return nil }
func (nopClient) Pause(string) error                           { return nil }
func (nopClient) Delete(string) error                          { return nil }
func (nopClient) IsPreparing(string) bool                      { return false }
func (nopClient) Probe(string) (*api.ProbeResult, error)       { return &api.ProbeResult{}, nil }
func (nopClient) BTTaskDetail(string) (*api.BTDetail, error)   { return nil, nil }
func (nopClient) Settings() api.Settings                       { return api.Settings{} }
func (nopClient) SaveSettings(api.Settings) error              { return nil }
func (nopClient) SetOnShow(func())                             {}
func (nopClient) SetOnShowAddTask(func(api.ShowAddTaskParams)) {}
func (nopClient) SetOnLanguage(func())                         {}
func (nopClient) SetOnClose(func())                            {}
func (nopClient) Close() error                                 { return nil }

// errStartFailed 是 Start 失败路径测试里注入的业务侧错误。
var errStartFailed = errors.New("start failed: boom")
