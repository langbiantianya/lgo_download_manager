// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// ldm（Local Download Manager）是运行整套下载管理器的单一可执行文件：
// SQLite store、scheduler 以及 Fyne GUI。
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

	"fyne.io/fyne/v2/app"

	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
	"lgo_download_manager/internal/ui"
	"lgo_download_manager/internal/urllauncher"
)

const (
	defaultDBPath = "ldm.sqlite"
)

func main() {
	dbPath := flag.String("db", defaultDBPath, "path to SQLite database")
	noGUI := flag.Bool("no-gui", false, "start without the Fyne GUI")
	openURL := flag.String("open-url", "", "download URL (lgom://... format)")
	light := flag.Bool("light", false, "force lightweight mode (destroy UI on close)")
	flag.Parse()

	// 从 --open-url flag 与位置参数中收集 URL。
	// 部分桌面环境会以位置参数的形式传入 URL。
	var urls []string
	if *openURL != "" {
		urls = append(urls, *openURL)
	}
	for _, arg := range flag.Args() {
		if strings.HasPrefix(arg, "lgom://") {
			urls = append(urls, arg)
		}
	}

	// 单实例锁
	isPrimary, release, err := urllauncher.AcquireLock()
	if err != nil {
		log.Fatalf("urllauncher: %v", err)
	}
	if !isPrimary {
		// 已有其他实例运行；转发所有 URL 后退出
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

	// 存储层
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	// 调度器
	sc := scheduler.New(st)
	go sc.Run(ctx) // 后台刷盘协程；通过 ctx 取消

	// URL handler：从 URL 请求添加下载任务
	handleDownloadURL := func(rawURL string) {
		req, err := urllauncher.HandleURL(rawURL)
		if err != nil {
			log.Printf("invalid URL: %v", err)
			return
		}
		log.Printf("adding download: %s", req.URL)

		// 探测 protocol
		proto, err := protocol.DetectKind(req.URL, "")
		if err != nil {
			log.Printf("invalid URL: %v", err)
			return
		}

		// 优先使用已配置的保存目录，否则回退到默认
		saveDir := defaultSaveDir()
		if ui.GlobalSettings.DefaultSaveDir != "" {
			saveDir = ui.GlobalSettings.DefaultSaveDir
		}

		// 若未提供文件名则从 URL 中推导
		filename := req.Name
		if filename == "" {
			filename = filepath.Base(req.URL)
			// 从文件名中移除 query string
			if idx := strings.IndexByte(filename, '?'); idx != -1 {
				filename = filename[:idx]
			}
		}
		savePath := filepath.Join(saveDir, filename)

		auth := protocol.AuthOptions{
			UserAgent: req.UA,
			Cookies:   req.Cookies,
		}

		tk, err := sc.Add(scheduler.AddTaskInput{
			URL:          req.URL,
			SavePath:     savePath,
			Protocol:     proto,
			Auth:         auth,
			ChunkCount:   4,
			MinChunkSize: ui.GlobalSettings.MinChunkSize,
		})
		if err != nil {
			log.Printf("failed to add task: %v", err)
			return
		}

		// 启动下载
		if err := sc.Start(tk.ID); err != nil {
			log.Printf("failed to start task: %v", err)
		}
	}

	// 启动 Unix socket server 用于 URL 转发（非阻塞）
	go func() {
		if err := urllauncher.ListenAndServe(handleDownloadURL); err != nil {
			log.Printf("urllauncher server error: %v", err)
		}
	}()

	// 处理通过 --open-url 或位置参数传入的 URL
	for _, u := range urls {
		handleDownloadURL(u)
	}

	// GUI —— 必须在 Fyne Run() 所在的 main goroutine 中执行。
	// GUI —— 必须在 Fyne Run() 所在的 main goroutine 中执行。
	if !*noGUI {
		if *light {
			ui.SetForceLightMode()
		}
		a := app.NewWithID("com.langbiantianya.LGDM")
		// 在 app 创建之后再构造窗口，这样 widget 构造时可以解析
		// fyne.CurrentApp()（list.go 在初始化阶段会调用它）。
		win := ui.NewMainWindow(a, st, sc)
		win.Show()
		a.Run()
		cancel()
		os.Exit(0)
	}

	// 等待关闭信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")
	cancel()
}

// defaultSaveDir 返回当前平台合适的下载目录。
func defaultSaveDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads")
}
