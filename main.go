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
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"lgo_download_manager/internal/ipc"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/settings"
	"lgo_download_manager/internal/store"
	"lgo_download_manager/internal/tray"
	"lgo_download_manager/internal/ui"
	"lgo_download_manager/internal/uimgr"
	"lgo_download_manager/internal/urllauncher"
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
	go sc.Run(ctx)

	// 业务进程对 URL 转发的处理：探测 protocol、推导保存路径，
	// 然后通过 scheduler 添加任务并启动；与 UI 子进程的 IPC
	// 调用走的是同一条路径。
	handleDownloadURL := func(rawURL string) {
		req, err := urllauncher.HandleURL(rawURL)
		if err != nil {
			log.Printf("invalid URL: %v", err)
			return
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

	// 托盘：与 UI 子进程解耦，常驻业务进程；Open 拉起/显示 UI，
	// Quit 结束整个程序。
	go tray.Start(tray.Callbacks{
		Open: uim.Open,
		Quit: func() {
			cancel()
		},
	})

	// 默认拉起 UI（除非 -no-gui）。
	if !*noGUI {
		if err := uim.Start(); err != nil {
			log.Printf("uimgr: cannot start UI child: %v", err)
		}
	}

	// 等待退出信号：SIGINT/SIGTERM。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")
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
