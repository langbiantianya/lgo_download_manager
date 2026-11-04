// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// lgdm-ui 是只包含 Fyne UI 与 RPC 客户端的独立进程入口。
//
// 业务主进程优先拉起同目录的 lgdm-ui；找不到它时才回退到业务二进制的
// LDM_UI_CHILD 角色。独立入口不导入 store、scheduler、协议或 BitTorrent，
// 因此 UI 进程不会为业务侧的包级初始化和运行时状态付出内存成本。
package main

import (
	"log/slog"
	"os"

	"lgo_download_manager/internal/logging"
	"lgo_download_manager/internal/ui"
)

func main() {
	level := slog.LevelInfo
	if os.Getenv(logging.EnvDebug) == "1" {
		level = slog.LevelDebug
	}

	logDir := os.Getenv(logging.EnvLogDir)
	if level == slog.LevelDebug && logging.AttachParentConsole() {
		logDir = ""
	}
	if err := logging.InitLevel(level, logDir); err != nil {
		// UI 日志不可用不应阻止窗口启动；退回 stderr 后继续。
		_ = logging.InitLevel(level, "")
		logging.Printf("lgdm-ui: logging init failed: %v", err)
	}

	code := ui.RunChild()
	_ = logging.Close()
	os.Exit(code)
}
