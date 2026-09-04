// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"log"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"lgo_download_manager/internal/ipc"
)

// NewService 连接到业务进程的 socket、完成握手并返回 Service 接口。
// 调用方在拿到 Service 后需自行启动后台读循环（一般通过 RunChild）。
func NewService(socketPath, token string) (Service, error) {
	c, _, err := dialAndHandshake(socketPath, token)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// RunChild 是 UI 子进程角色的入口：
//  1. 读取启动环境变量（ipc.EnvUIChild/EnvUISocket/EnvUIToken）。
//  2. 连接业务 socket、握手并接收初始设置（MsgInit）。
//  3. 启动 Fyne app，构造主窗口，把 ipcClient 作为业务服务注入。
//  4. app.Run() 阻塞；窗口关闭（LightMode）或收到业务 MsgClose 时退出。
//     退出后操作系统回收整个进程内存，业务进程不受影响。
//
// 仅在被业务进程以正确环境变量拉起时调用；直接运行会因缺少环境变量而退出。
func RunChild() int {
	if os.Getenv(ipc.EnvUIChild) != "1" {
		log.Println("ui.RunChild: not a UI child process (missing env); exiting")
		return 2
	}
	sock := os.Getenv(ipc.EnvUISocket)
	tok := os.Getenv(ipc.EnvUIToken)
	if sock == "" || tok == "" {
		log.Println("ui.RunChild: missing socket/token env; exiting")
		return 2
	}

	c, _, err := dialAndHandshake(sock, tok)
	if err != nil {
		log.Printf("ui.RunChild: handshake failed: %v", err)
		return 1
	}

	// 关键：必须先启动 IPC 读循环，再构造主窗口。
	// NewMainWindow → taskList.build → refreshEmptyState → svc.List
	// 会发起同步 IPC 请求；若 c.run 还没跑起来，request() 会阻塞等待
	// 应答（最长 30s 后超时），UI 表现为「卡在 building main window」。
	go c.run()

	a := app.NewWithID("com.langbiantianya.LGDM")
	win := NewMainWindow(a, c)

	// 业务托盘「显示窗口」→ 触发主窗口 fyne 事件线程上的 ShowFromTray。
	c.SetOnShow(func() {
		fyne.Do(func() { win.ShowFromTray() })
	})
	// 业务侧退出 / 连接断开 → 通知 fyne 退出。LightMode 下窗口关闭
	// 也会调用 a.Quit()，此时连接由 Close 兜底关闭。
	c.SetOnClose(func() {
		fyne.Do(func() { a.Quit() })
	})

	win.Show()
	a.Run()
	_ = c.Close()
	return 0
}
