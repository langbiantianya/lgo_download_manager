// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// lgdm（lgo_download_manager）单一可执行文件承载两种角色：
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
	"fmt"
	"log/slog"
	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/ipc"
	"lgo_download_manager/internal/logging"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/settings"
	"lgo_download_manager/internal/store"
	"lgo_download_manager/internal/tray"
	"lgo_download_manager/internal/ui"
	"lgo_download_manager/internal/uimgr"
	"lgo_download_manager/internal/urllauncher"
	"lgo_download_manager/internal/version"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const defaultDBFile = "lgdm.sqlite"

// envLogDebug 跨进程传递 --debug 选择:业务进程启动时若用户指定了
// --debug,则把该信号写进 LGDM_DEBUG,UI 子进程继承同一份配置。
const envLogDebug = "LGDM_DEBUG"

func main() {
	configPath := flag.String("config", defaultConfigPath(), "directory for lgdm runtime files (db + logs)")
	noGUI := flag.Bool("no-gui", false, "start without spawning the Fyne UI child")
	openURL := flag.String("open-url", "", "download URL (lgom://... format)")
	light := flag.Bool("light", false, "force lightweight mode (destroy UI on close)")
	debug := flag.Bool("debug", false, "log to stderr instead of rotating file")
	autostartSilent := flag.Bool("autostart", false, "invoked by OS login autostart: stay headless (tray only, no main window)")
	flag.Parse()

	// 把 --debug 通过环境变量传给即将拉起的 UI 子进程,
	// 让子进程日志也走 stderr,而不是悄悄落到文件里。
	if *debug {
		os.Setenv(envLogDebug, "1")
	}

	// UI 子进程在 spawn 时也会走到这里(被业务进程自我复刻拉起),
	// 它没有自己的 --debug,但父进程已经把 LGDM_DEBUG 传过来。
	childDebug := os.Getenv(envLogDebug) == "1"
	debugEnabled := childDebug || *debug
	// 日志与 db 落在同一目录:用户传 --config 时跟 --config,否则跟 defaultConfigPath()。
	// 这样运维上 db 与 log 总在同一处,便于打包收集。
	logDir := *configPath
	if logDir == "" {
		logDir = "."
	}
	// Windows 上二进制是用 -H windowsgui 编译的——运行时没有 console。
	// 当用户从 cmd/PowerShell 加 --debug 启动时,我们要把进程挂回父
	// console,让日志直接落到终端而不是凭空消失;若 attach 失败
	// (从 Explorer/浏览器 URL 协议拉起),则回退到文件日志(同样开
	// Debug 级别),保证 debug 信息不丢。非 Windows 平台 AttachParentConsole
	// 永远返回 true,行为不变。
	logLevel := slog.LevelInfo
	logToStderr := false
	if debugEnabled {
		logLevel = slog.LevelDebug
		if logging.AttachParentConsole() {
			logToStderr = true
		}
	}
	if err := logging.InitLevel(logLevel, pickLogTarget(logToStderr, logDir)); err != nil {
		fmt.Fprintf(os.Stderr, "logging init: %v\n", err)
		os.Exit(1)
	}
	defer logging.Close()

	logging.Printf("lgdm %s", version.String())

	// 这样业务子进程即便被误调用也不会去解析业务 flag 或拉起 store。
	if os.Getenv(ipc.EnvUIChild) == "1" {
		os.Exit(ui.RunChild())
	}

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
		logging.Fatalf("urllauncher: %v", err)
	}
	if !isPrimary {
		if len(urls) > 0 {
			// 逐个转发并汇总:首个失败不应丢弃后续 URL。
			var failed int
			for _, u := range urls {
				if err := urllauncher.SendURL(u); err != nil {
					logging.Printf("failed to forward URL %q: %v", u, err)
					failed++
				}
			}
			if failed > 0 {
				logging.Printf("failed to forward %d of %d URL(s) to the primary instance", failed, len(urls))
				os.Exit(1)
			}
			logging.Printf("forwarded %d URL(s) to primary instance", len(urls))
		} else {
			logging.Println("another instance is already running")
		}
		os.Exit(0)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 拼接 db 实际路径:配置目录 + db 文件名。配置目录可能在 lgom://
	// 协议首次拉起时还不存在(冷启动),先建目录再开库。
	dbPath := filepath.Join(*configPath, defaultDBFile)
	if dir := *configPath; dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logging.Fatalf("store: create database directory: %v", err)
		}
	}
	st, err := store.Open(dbPath)
	if err != nil {
		logging.Fatalf("store: %v", err)
	}
	defer st.Close()

	cur, err := settings.Load(st, *light)
	if err != nil {
		logging.Fatalf("settings: %v", err)
	}
	// 启动调和:用户开关位与 OS 真实注册状态可能漂移(第三方工具、
	// 安装/卸载残留),以持久化的 AutoStart 为准重新对齐。
	settings.ReconcileAutoStart(cur.AutoStart)

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
		logging.Printf("scheduler: reclaim downloading tasks: %v", err)
	}
	go sc.Run(ctx)

	// UI 管理器：负责拉起/管理 Fyne UI 子进程（自我复刻）。必须先于
	// handleDownloadURL 创建,后者把 URL 转发给 UI 弹弹对话框。
	uim := uimgr.New(st, sc)
	uim.SetSettings(cur)
	// 把权威 Language 应用到当前进程的 ilocale(托盘、日志等业务侧
	// 本地化都走这里),同时注册变更 hook:UI 在「设置」里切语言时,
	// uim.SetLanguage 会回调这里,刷新 ilocale 与托盘菜单。
	ilocale.Set(cur.Language)
	uim.OnLanguageChanged(func(lang string) {
		ilocale.Set(lang)
		tray.Reload()
	})
	// 同步 curLang 到 uimgr:冷启动场景 InitData 会从 settings 携带
	// Language,这里提前置好,避免 UI 起来后 SetLanguage 因 curLang 与
	// s.Language 相等而误判"无变更"。运行中切语言由 dispatch → SetLanguage
	// 处理,hook 同步刷新本进程本地化界面。
	uim.SetLanguage(cur.Language)

	// 业务进程对 URL 转发的处理：解析 lgom:// 参数并把预填值交给 UI 子进程
	// 的「新建下载任务」对话框,任务提交由用户在该对话框中确认后触发;
	// 与 UI 内点「新建任务」按钮走的是同一条 IPC + Service.AddTask/Start
	// 路径,只在是否预填 URL/Name/UA/Cookies 上不同。
	//
	// --no-gui 模式下没有 UI 实例:此时回退到原先的「业务进程直接 Add+Start」,
	// 服务/CLI 形态仍可独立工作;其它路径只要 UI 子进程拉起失败就只记录
	// warning,由用户从托盘或重新转发 URL 触发。
	handleDownloadURL := func(rawURL string) {
		req, err := urllauncher.HandleURL(rawURL)
		if err != nil {
			logging.Printf("invalid URL: %v", err)
			return
		}
		logging.Printf("queueing URL for dialog: %s", req.URL)

		// --no-gui 与 --autostart 都没有 UI 实例可弹:都走「业务侧
		// 直接 Add+Start」。autostart 路径主要处理 lgom:// 转发协议
		// 在 OS 登录后立即触发 URL 的边角场景(罕见但真实:浏览器
		// 启动后立刻点链接)。
		if *noGUI || *autostartSilent {
			// CLI / 服务 / autostart 场景:业务侧直接创建并启动,无对话框可弹。
			addAndStartFromURL(sc, cur, req)
			return
		}
		if err := uim.SendShowAddTask(ipc.ShowAddTaskParams{
			URL:     req.URL,
			Name:    req.Name,
			UA:      req.UA,
			Cookies: req.Cookies,
		}); err != nil {
			logging.Printf("cannot show add-task dialog for %s: %v", req.URL, err)
		}
	}

	// 启动 URL 转发 socket server：供其他实例通过 urllauncher.SendURL
	// 投递新下载请求。
	go func() {
		if err := urllauncher.ListenAndServe(handleDownloadURL); err != nil {
			logging.Printf("urllauncher server error: %v", err)
		}
	}()

	// 处理通过 --open-url / 位置参数传入的 URL（主进程本地触发）。
	for _, u := range urls {
		handleDownloadURL(u)
	}

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

	// 默认拉起 UI(除非 -no-gui 或 --autostart)。本进程的本地 --open-url / 位置参数
	// URL 在 uim 起来之前就已经被上面循环处理——SendShowAddTask 内部会
	// 等待握手,首条消息不会丢失。
	//
	// --autostart 是 OS 登录启动项触发的静默拉起:用户没有点托盘菜单就
	// 不会有 UI 需求;只保留调度器 + 系统托盘,等用户在托盘「显示窗口」。
	// 这与 Windows 资源管理器/HKCU Run 的「登录后立即拉起」、Linux
	// XDG autostart、macOS launchd RunAtLoad 的语义对齐。
	if !*noGUI && !*autostartSilent {
		if err := uim.Start(); err != nil {
			logging.Printf("uimgr: cannot start UI child: %v", err)
		}
	}
	<-quit
	logging.Println("shutting down...")
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
		logging.Printf("scheduler: pause all timed out after 5s, forcing exit: %v", pauseErr)
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

// addAndStartFromURL 把一个 lgom:// 解析出的 DownloadRequest 直接
// 落地到 scheduler：探测协议、构造 AddTaskInput、Add + Start。
//
// 仅在 --no-gui 这条路径上调用——没有 UI 可弹,业务侧独立完成所有工作;
// 其它路径都走 uim.SendShowAddTask 让用户在对话框里确认。
//
// 行为/错误日志与原先 handleDownloadURL 内联实现保持一致,以便
// --no-gui 用户得到与历史版本兼容的行为。
func addAndStartFromURL(sc *scheduler.Scheduler, cur store.Settings, req *urllauncher.DownloadRequest) {
	kind, err := protocol.DetectKind(req.URL, "")
	if err != nil {
		logging.Printf("unsupported URL %s: %v", req.URL, err)
		return
	}
	logging.Printf("adding download: %s", req.URL)

	auth := protocol.AuthOptions{
		UserAgent: req.UA,
		Cookies:   req.Cookies,
	}

	tk, err := sc.Add(scheduler.AddTaskInput{
		URL:          req.URL,
		SavePath:     savePathFor(cur, req.URL, req.Name),
		Protocol:     kind,
		Auth:         auth,
		ChunkCount:   defaultChunks(cur.DefaultThreads),
		MinChunkSize: cur.MinChunkSize,
	})
	if err != nil {
		logging.Printf("failed to add task: %v", err)
		return
	}
	if err := sc.Start(tk.ID); err != nil {
		logging.Printf("failed to start task: %v", err)
	}
}

// defaultConfigPath 返回默认的配置目录(SQLite 与日志都落在它里面)。
//
// Windows 上默认值必须落在用户级数据目录,不能是相对路径:安装后
// 的 lgdm 会被 lgom:// 协议从任意工作目录拉起(资源管理器/浏览器的
// CWD 可能是 System32,普通用户不可写),相对路径会让冷启动直接
// 开库失败;即使能写,托盘实例与协议实例的工作目录不同也会落到两份
// 不同的库里,任务列表对不上。
//
// 其它平台沿用 XDG 风格的 ~/.config/lgo_download_manager:即便 CWD
// 是任意目录(例如 lgom:// 协议从 System32 拉起),也总能落在用户
// 自己的可写位置上。$HOME 不可用时回退到 ./,留给桌面环境/安装器决定。
func defaultConfigPath() string {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "lgo_download_manager")
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "lgo_download_manager")
	}
	return "."
}

// pickLogTarget 把 logging.InitLevel 的 dir 解析出来:
//   - stderr=true 时传 "",触发 InitLevel 走 stderr handler;
//   - 否则传 logDir,让日志落到配置目录里的 lgdm.log。
// 仅作为日志目标的归一化入口,本身没有副作用。
func pickLogTarget(stderr bool, logDir string) string {
	if stderr {
		return ""
	}
	return logDir
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
