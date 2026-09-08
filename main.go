// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// ldm（Local Download Manager）单一可执行文件承载两种角色：
//
//  1. 业务主进程（默认）：SQLite store + 下载 scheduler + 托盘 +
//     URL 转发 socket server + UI 管理器（拉起 UI 子进程）。
//  2. Fyne UI 子进程：由业务进程自我复刻启动，连接业务 socket 运行
//     Fyne 界面，关闭即由操作系统回收所有内存。
//
// 角色分派通过 ipc.EnvUIChild 环境变量完成：业务进程拉起子进程时
// 会把 EnvUIChild=1 与 socket/token 一并注入。
//
// URL scheme：lgom://download?url=<encoded>[&name=<encoded>[&ua=<encoded>[&headers=<encoded>[&cookies=<encoded>]]]]
package main

import (
	"context"
	"flag"
	"lgo_download_manager/internal/ipc"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/settings"
	"lgo_download_manager/internal/store"
	"lgo_download_manager/internal/tray"
	"lgo_download_manager/internal/ui"
	"lgo_download_manager/internal/uimgr"
	"lgo_download_manager/internal/urllauncher"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const defaultDBPath = "ldm.sqlite"

func main() {
	// UI 子进程角色：直接把控制权交给 ui.RunChild。必须最先判断，
	// 这样业务子进程即便被误调用也不会去解析业务 flag 或拉起 store。
	if os.Getenv(ipc.EnvUIChild) == "1" {
		os.Exit(ui.RunChild())
	}

	dbPath := flag.String("db", defaultDBPath, "path to SQLite database")
	noGUI := flag.Bool("no-gui", false, "start without spawning the Fyne UI child")
	openURL := flag.String("open-url", "", "download URL (lgom://... format)")
	light := flag.Bool("light", false, "force lightweight mode (destroy UI on close)")
	flag.Parse()

	// 收集 URL：--open-url 与位置参数两种来源都支持。
	var urls []string
	if *openURL != "" {
		urls = append(urls, *openURL)
	}
	for _, arg := range flag.Args() {
		if strings.HasPrefix(arg, "lgom://") {
			urls = append(urls, arg)
		}
	}

	// 单实例锁：第二个实例只负责把 URL 转发给已运行实例。
	isPrimary, release, err := urllauncher.AcquireLock()
	if err != nil {
		log.Fatalf("urllauncher: %v", err)
	}
	if !isPrimary {
		if len(urls) > 0 {
			for _, u := range urls {
				if err := urllauncher.SendURL(u); err != nil {
					log.Fatalf("failed to forward URL: %v", err)
				}
			}
			log.Printf("forwarded %d URL(s) to primary instance", len(urls))
		} else {
			log.Println("another instance is already running")
		}
		os.Exit(0)
		return
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 存储 + 设置加载。
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	cur, err := settings.Load(st, *light)
	if err != nil {
		log.Fatalf("settings: %v", err)
	}

	// 调度器。
	sc := scheduler.New(st)

	// 把设置中的并发上限推到 scheduler。SetMaxConcurrent 还会触发一次
	// promotePending,确保上次进程残留的 Pending 任务在当前 cap 下被
	// 合理启动(老库可能没有 max_concurrent 列,EffectiveMaxConcurrent
	// 会回退到 DefaultMaxConcurrent)。
	sc.SetMaxConcurrent(cur.EffectiveMaxConcurrent())

	// 冷启动兜底：上次进程崩溃/被 kill -9 时,残留的 Downloading 任务
	// 没有 engine 在跑,必须在 Run 之前把它们回收成 Paused,否则 UI 会
	// 把这些任务显示为「下载中」却永远没有进度,用户无法恢复。
	if err := sc.ReclaimDownloadingTasks(); err != nil {
		log.Printf("scheduler: reclaim downloading tasks: %v", err)
	}
	go sc.Run(ctx)

// 业务进程对 URL 转发的处理：探测 protocol、推导保存路径，
// 然后通过 scheduler 添加任务并启动；与 UI 子进程的 IPC
// 调用走的是同一条路径。
handleDownloadURL := func(rawURL string) {
		req, err := urllauncher.HandleURL(rawURL)
		if err != nil {
			log.Printf("invalid URL: %v", err)
		}
		log.Printf("adding download: %s", req.URL)

		auth := protocol.AuthOptions{
			UserAgent: req.UA,
			Cookies:   req.Cookies,
		}

		tk, err := sc.Add(scheduler.AddTaskInput{
			URL:          req.URL,
			SavePath:     savePathFor(cur, req.URL, req.Name),
			Protocol:     "",
			Auth:         auth,
			ChunkCount:   defaultChunks(cur.DefaultThreads),
			MinChunkSize: cur.MinChunkSize,
		})
		if err != nil {
			log.Printf("failed to add task: %v", err)
			return
		}
		if err := sc.Start(tk.ID); err != nil {
			log.Printf("failed to start task: %v", err)
		}
	}

	// 启动 URL 转发 socket server：供其他实例通过 urllauncher.SendURL
	// 投递新下载请求。
	go func() {
		if err := urllauncher.ListenAndServe(handleDownloadURL); err != nil {
			log.Printf("urllauncher server error: %v", err)
		}
	}()

	// 处理通过 --open-url / 位置参数传入的 URL（主进程本地触发）。
	for _, u := range urls {
		handleDownloadURL(u)
	}

	// UI 管理器：负责拉起/管理 Fyne UI 子进程（自我复刻）。
	uim := uimgr.New(st, sc)
	uim.SetSettings(cur)

	// 退出信号：业务进程同时监听 SIGINT/SIGTERM 和托盘「退出」菜单。
	// 任何一方触发都会走同一条优雅退出路径(cancel → uim.Close → tray.Stop)。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// 托盘:与 UI 子进程解耦,常驻业务进程;Open 拉起/显示 UI,
	// Quit 触发 quit 通道与 SIGINT/SIGTERM 等价的退出。
	go tray.Start(tray.Callbacks{
		Open: uim.Open,
		Quit: func() {
			// 复用与信号相同的 quit 通道,避免自信号/self-kill 的可移植性麻烦。
			// 非阻塞:首次点击后 tray goroutine 已退出,后续重复触发直接丢弃。
			select {
			case quit <- syscall.SIGTERM:
			default:
			}
		},
	})

	// 默认拉起 UI(除非 -no-gui)。
	if !*noGUI {
		if err := uim.Start(); err != nil {
			log.Printf("uimgr: cannot start UI child: %v", err)
		}
	}
	<-quit
	log.Println("shutting down...")
	// 先停掉所有正在下载的 engine job 并落盘 Paused, 再 cancel root ctx。
	// 顺序很重要:scheduler.Run 在 ctx.Done 上调用 flushAll, 会用
	// rj.status(=Downloading)覆盖 store;PauseAll 在 cancel 之前跑完,
	// 那些 job 都已从 s.jobs 删除、状态已是 Paused,flushAll 不会再写回。
	//
	// 阻塞上限 5s:任何 job 超过这个时间还没退出,就不再等,直接 log 后
	// os.Exit(1) 兜底结束进程(残余 Downloading 由下次冷启动的
	// ReclaimDownloadingTasks 回收)。
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	pauseErr := sc.PauseAll(shutdownCtx)
	shutdownCancel()
	if pauseErr != nil {
		log.Printf("scheduler: pause all timed out after 5s, forcing exit: %v", pauseErr)
		os.Exit(1)
	}
	cancel()
	uim.Close()
	tray.Stop()
}

// savePathFor 推导保存路径：URL 中指定的文件名 → 默认下载目录 + 推导名。
func savePathFor(s store.Settings, rawURL, name string) string {
	saveDir := s.DefaultSaveDir
	if saveDir == "" {
		saveDir = defaultSaveDir()
	}
	if name == "" {
		name = filepath.Base(rawURL)
		if i := strings.IndexByte(name, '?'); i != -1 {
			name = name[:i]
		}
	}
	return filepath.Join(saveDir, name)
}

// defaultSaveDir 返回当前平台合适的下载目录。
func defaultSaveDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads")
}

// defaultChunks 根据用户配置的默认线程数推导分块数；并发上下限与
// scheduler.AddTaskInput 默认对齐。
func defaultChunks(threads int) int {
	if threads <= 0 {
		return 4
	}
	if threads > 16 {
		return 16
	}
	return threads
}
