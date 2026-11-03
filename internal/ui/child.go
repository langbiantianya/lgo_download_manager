// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"context"
	"os"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/logging"
	"lgo_download_manager/internal/rpc"
)

// applyHandshakeLanguage 把握手时拿到的权威语言同步到本进程的 ilocale。
//
// 权威语言随 get_settings 的设置快照一起回来：运行时切语言由「设置 →
// 语言」在本进程先行切换，冷启动/重启后的语言只能靠这份快照，所以它
// 必须在 NewMainWindow 之前生效。
func applyHandshakeLanguage(c rpc.Client) {
	ilocale.Set(c.Settings().Language)
}

// RunChild 是 UI 子进程角色的入口：
//  1. 读取启动环境变量（rpc.EnvUIChild/EnvUIListen/EnvUIToken/EnvUITransport）。
//  2. 用 rpc.Dial 连上业务进程：握手（get_settings）与消息流都在客户端
//     内部完成，Dial 返回时就已经可用。
//  3. 启动 Fyne app，构造主窗口，把 rpc.Client 作为业务服务注入。
//  4. app.Run() 阻塞；窗口关闭（LightMode）或收到业务侧 close 控制消息时
//     退出。退出后操作系统回收整个进程内存，业务进程不受影响。
//
// 仅在被业务进程以正确环境变量拉起时调用；直接运行会因缺少环境变量而退出。
func RunChild() int {
	if os.Getenv(rpc.EnvUIChild) != "1" {
		logging.Println("ui.RunChild: not a UI child process (missing env); exiting")
		return 2
	}
	listen := strings.TrimSpace(os.Getenv(rpc.EnvUIListen))
	tok := strings.TrimSpace(os.Getenv(rpc.EnvUIToken))
	transport := strings.TrimSpace(os.Getenv(rpc.EnvUITransport))
	if listen == "" || tok == "" {
		logging.Println("ui.RunChild: missing listen/token env; exiting")
		return 2
	}

	// ctx 贯穿整个会话：握手的往返调用与读循环都挂在它上面。这里没有
	// 可用的取消源——UI 进程退出时由 Close 收尾。
	c, err := rpc.Dial(context.Background(), transport, listen, tok)
	if err != nil {
		logging.Printf("ui.RunChild: dial failed: %v", err)
		return 1
	}

	// 必须在构造主窗口之前把权威语言落一次，否则首帧会用默认语言渲染；
	// 运行期的语言变更走 onLanguage 回调。
	applyHandshakeLanguage(c)

	a := app.NewWithID("com.langbiantianya.LGDM")
	win := NewMainWindow(a, c)

	// 控制消息的回调跑在 rpc.Client 的读循环里，UI 操作一律切回 fyne
	// 事件线程再动 widget。
	c.SetOnShow(func() {
		fyne.Do(func() { win.ShowFromTray() })
	})
	// 业务侧收到 lgom:// 转发后 → 显示主窗口并弹出「新建下载任务」对话框,
	// URL/Name/UA/Cookies 由参数携带。
	c.SetOnShowAddTask(func(p api.ShowAddTaskParams) {
		fyne.Do(func() {
			win.ShowFromTray()
			ShowAddTaskDialogForURL(c, p)
		})
	})
	// 语言变更:用户在「设置」里切语言时业务进程推 MessageKindLanguage,
	// 这里只负责让 UI 重译当前可见 widget(标题/工具栏/侧边栏/状态栏/
	// 任务行/设置页)。
	//
	// 本进程的 ilocale 已由「设置 → 语言」在写回前先行切换（见
	// buildLanguageSelect）——契约层的语言控制消息**带着**语言标签，但
	// rpc.Client 的 onLanguage 钩子不接收它，所以等不到。
	// 首启的语言不走这条路径，由握手时的 settings 应用
	// （见 applyHandshakeLanguage）。
	c.SetOnLanguage(func() {
		fyne.Do(func() { win.applyLanguage() })
	})
	// 业务侧退出 / 连接断开 → 通知 fyne 退出。LightMode 下窗口关闭
	// 也会调用 a.Quit()，此时连接由 Close 兜底关闭。
	c.SetOnClose(func() {
		fyne.Do(func() { a.Quit() })
	})

	win.Show()
	a.Run()
	// 事件循环已退出：停掉周期性的后台任务（磁盘空间刷新）并取消订阅。
	win.Close()
	_ = c.Close()
	return 0
}
