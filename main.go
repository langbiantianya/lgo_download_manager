// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// ldm（Local Download Manager）是运行整套下载管理器的单一可执行文件：
// SQLite store、scheduler 以及 Gio GUI。
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

	"gioui.org/app"

	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
	"lgo_download_manager/internal/ui"
	"lgo_download_manager/internal/urllauncher"
)

const defaultDBPath = "ldm.sqlite"

// globalUIApp 是 GUI 模式下的共享 App，供 systray 调用。
var globalUIApp *ui.App

func main() {
	dbPath := flag.String("db", defaultDBPath, "path to SQLite database")
	noGUI := flag.Bool("no-gui", false, "start without the Gio GUI")
	openURL := flag.String("open-url", "", "download URL (lgom://... format)")
	flag.Parse()

	var urls []string
	if *openURL != "" {
		urls = append(urls, *openURL)
	}
	for _, arg := range flag.Args() {
		if strings.HasPrefix(arg, "lgom://") {
			urls = append(urls, arg)
		}
	}

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

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	sc := scheduler.New(st)
	go sc.Run(ctx)

	handleDownloadURL := func(rawURL string) {
		req, err := urllauncher.HandleURL(rawURL)
		if err != nil {
			log.Printf("invalid URL: %v", err)
			return
		}
		log.Printf("adding download: %s", req.URL)

		proto, err := protocol.DetectKind(req.URL, "")
		if err != nil {
			log.Printf("invalid URL: %v", err)
			return
		}

		saveDir := defaultSaveDir()
		if ui.GlobalSettings.DefaultSaveDir != "" {
			saveDir = ui.GlobalSettings.DefaultSaveDir
		}

		filename := req.Name
		if filename == "" {
			filename = filepath.Base(req.URL)
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
		if err := sc.Start(tk.ID); err != nil {
			log.Printf("failed to start task: %v", err)
		}
	}

	go func() {
		if err := urllauncher.ListenAndServe(handleDownloadURL); err != nil {
			log.Printf("urllauncher server error: %v", err)
		}
	}()

	for _, u := range urls {
		handleDownloadURL(u)
	}

	if !*noGUI {
		a := ui.NewApp(st, sc)
		globalUIApp = a
		mw := ui.RunMainWindow(a)
		mw.SetOpenNewTask(func() { ui.OpenNewTaskWindow(globalUIApp) })
		go startTray(a)
		// app.Main 在所有 window 关闭后返回。
		app.Main()
		cancel()
		os.Exit(0)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")
	cancel()
}

func defaultSaveDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads")
}

// startTray 启动系统托盘（独立 goroutine）。
func startTray(a *ui.App) {
	start, _ := ui.StartTray(a, func() {
		a.QuitMainWindow()
	})
	start()
}
