// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

// 本文件仅测试用：允许 import store/scheduler/appservice 来构造真实业务侧，
// 生产代码不得引入。

import (
	"testing"

	"lgo_download_manager/internal/ilocale"
	"lgo_download_manager/internal/rpc"
	"lgo_download_manager/internal/store"
)

// TestRunChildAppliesHandshakeLanguage 回归测试：握手拿到的权威语言必须
// 在构造主窗口之前同步到 ilocale。
//
// 旧实现只在 IPC 读循环的 MsgInit 分支里调 ilocale.Set，而业务侧握手时
// 只推一次初始快照、读循环随后阻塞在下一轮 Recv 上——结果语言永远停在
// 默认 zh-Hans，用户选了英文每次开窗还是中文。现在权威语言随 get_settings
// 快照一起回来，RunChild 用 applyHandshakeLanguage 同步落一次。
//
// 这里用真实传输（握手 → appservice 的 get_settings）复现同一条路径，
// 只跳过 Fyne app 的启动：RunChild 剩下的部分只是构造窗口与注册回调。
func TestRunChildAppliesHandshakeLanguage(t *testing.T) {
	prev := ilocale.Current()
	defer ilocale.Set(prev)
	// 先切到一个确定不是目标值的语言，确保握手真的把语言改掉了。
	ilocale.Set("zh-Hans")

	for _, transport := range testTransports {
		t.Run(transport, func(t *testing.T) {
			st := openTestStore(t)
			if err := st.SaveSettings(store.Settings{Language: "en"}); err != nil {
				t.Fatalf("SaveSettings: %v", err)
			}
			env := startTestService(t, st, transport)
			ilocale.Set("zh-Hans")

			applyHandshakeLanguage(env.client)
			if got := ilocale.Current(); got != "en" {
				t.Fatalf("握手后 ilocale.Current() = %q，want en", got)
			}
			// 翻译也要跟着切换：NewMainWindow 会读 ilocale.T 拉标题与工具栏。
			if got := ilocale.T("main.toolbar.new"); got == "新建任务" {
				t.Fatal("握手后翻译仍是 zh-Hans")
			}
		})
	}
}

// 编译期断言：测试替身必须满足 UI 依赖的接口。
var _ rpc.Client = nopClient{}
