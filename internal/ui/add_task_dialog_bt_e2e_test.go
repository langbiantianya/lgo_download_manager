// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// 注意：本文件是测试代码，为了构造「有任务、有设置」的初始状态而 import
// 了 internal/store（以及 internal/scheduler / internal/appservice 之一）。
// 这是**仅测试用**的例外：internal/ui 的非测试代码必须只依赖契约层
// （internal/api + internal/rpc + internal/urlkind），不得 import 业务侧包。
// 见 internal/ui/boundary_test.go 与 Task 10 的 G4/G9 说明。

package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc"
	"lgo_download_manager/internal/store"
)

// TestAddTaskDialog_BittorrentFolderSemantics 验证磁力链接在「新建任务」
// 对话框里的目录语义:URL 变化后保存路径推导成 <默认目录>/<dn>,
// 标签变成「保存目录」并显示提示,提交后任务以 BT 协议落库。
//
// 这个用例不需要网络:磁力链接的体积预览按「未知」处理,提交由
// scheduler 的 BT 路径接管(此处只断言落库字段,不启动下载)。
func TestAddTaskDialog_BittorrentFolderSemantics(t *testing.T) {
	a := test.NewApp()
	defer test.NewApp()

	dir := t.TempDir()
	saveDir := filepath.Join(dir, "dl")
	// 全零设置的 BT 开关是「未配置」状态,这里显式给一份与 settings.Load
	// 首启默认一致的配置,模拟真实启动路径。
	env := startUIE2E(t, rpc.TransportHTTP, store.Settings{
		DefaultSaveDir: saveDir,
		DefaultThreads: 2,
		BTDHT:          true,
		BTPEX:          true,
	})
	st := env.store
	svc := env.client

	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Ubuntu%2024.04"
	ShowAddTaskDialogForURL(svc, api.ShowAddTaskParams{URL: magnet})

	wAdd := findAppWindow(a, "新建下载任务")
	if wAdd == nil {
		t.Fatalf("dialog window was not opened")
	}
	content := wAdd.Content()
	saveEntry := findNthEntry(content, 1)
	if saveEntry == nil {
		t.Fatalf("could not find the save-path entry")
	}
	wantSave := filepath.Join(saveDir, "Ubuntu 24.04")
	if saveEntry.Text != wantSave {
		t.Fatalf("save path = %q, want %q", saveEntry.Text, wantSave)
	}
	// 标签与提示:BT 任务的保存路径是目录。
	if findLabelContaining(content, "保存目录") == nil {
		t.Errorf("save-path label was not switched to 保存目录")
	}
	if findLabelContaining(content, "BitTorrent") == nil {
		t.Errorf("bt hint label not shown")
	}
	// 磁力链接没有体积可预览:不应该出现大小文本。
	if findLabelContaining(content, "GB") != nil || findLabelContaining(content, "MB") != nil {
		t.Errorf("unexpected size preview for a magnet link")
	}

	confirmBtn := findButtonByText(content, "开始下载")
	if confirmBtn == nil {
		t.Fatalf("could not find 开始下载 button")
	}
	test.Tap(confirmBtn)

	deadline := time.Now().Add(3 * time.Second)
	var tk *store.Task
	for time.Now().Before(deadline) {
		tasks, _ := st.ListTasks("", store.SortCreatedDesc)
		if len(tasks) == 1 {
			tk = tasks[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if tk == nil {
		t.Fatalf("task was not added")
	}
	if tk.Protocol != "BT" {
		t.Errorf("task protocol = %q, want BT", tk.Protocol)
	}
	if tk.URL != magnet {
		t.Errorf("task url = %q, want the magnet link", tk.URL)
	}
	if tk.SavePath != wantSave {
		t.Errorf("task save path = %q, want %q", tk.SavePath, wantSave)
	}
}

// findLabelContaining 在 widget 树里找包含 substr 的第一个 Label。
func findLabelContaining(root fyne.CanvasObject, substr string) *widget.Label {
	var found *widget.Label
	walk(root, func(o fyne.CanvasObject) {
		if found != nil {
			return
		}
		if l, ok := o.(*widget.Label); ok && strings.Contains(l.Text, substr) {
			found = l
		}
	})
	return found
}
