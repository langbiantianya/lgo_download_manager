// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build darwin

package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

// launchAgentPath 是 per-user 域的 LaunchAgent 路径;macOS launchd 登录后
// 会自动加载 ~/Library/LaunchAgents/ 下的 plist(无需 sudo)。
//
// Label 必须是 reverse-DNS,与其他系统/全局 agent 区分;我们用
// org.langbiantianya.LGDM,与 conf/lgo_download_manager.desktop
// 的 Icon 字段对齐。
const launchAgentFileName = "org.langbiantianya.LGDM.plist"

// plistTemplate 关键字段:
//
//   - Label:launchd 用作进程唯一标识(多个 plist 共用同一 Label 时后者获胜)
//   - ProgramArguments:数组形式比字符串形式更安全——避免 shell 转义,
//     路径含空格/引号不会被错误解析。argv[0] 是可执行文件绝对路径,
//     argv[1..] 是参数列表。
//   - RunAtLoad:登录后立即拉起,符合「开机自启」语义。
//   - KeepAlive=false:业务进程正常退出后不再被自动重启——
//     我们希望用户主动在托盘「退出」,或进程崩溃后再等下次登录。
//     崩溃恢复交给 keepAlive 字段反而会掩盖真实问题。
//   - ProcessType=Background:把进程标记为后台进程,降低调度优先级,
//     不进入前台应用的内存压力管理名单。
//
// 静默自启的实现见业务进程:读到 --autostart 后跳过 UI 子进程拉起,
// 只保留调度器 + 系统托盘(系统托盘在 macOS 上是 menu bar extra,
// 无 Dock 图标,不打扰用户)。
const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>org.langbiantianya.LGDM</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
    <key>ProcessType</key>
    <string>Background</string>
</dict>
</plist>
`

// launchAgentsDir 返回 ~/Library/LaunchAgents。
// macOS 的 user LaunchAgent 目录固定路径,无 XDG 式回退。
func launchAgentsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("autostart: resolve home: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

// Enable 写入 LaunchAgent plist。~/Library/LaunchAgents 不存在时一并创建。
func Enable() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("autostart: resolve executable: %w", err)
	}
	dir, err := launchAgentsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("autostart: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, launchAgentFileName)
	content := fmt.Sprintf(plistTemplate, exe, FlagName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("autostart: write %s: %w", path, err)
	}
	return nil
}

// Disable 删除 LaunchAgent plist;不存在不报错(幂等)。
func Disable() error {
	dir, err := launchAgentsDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, launchAgentFileName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("autostart: remove %s: %w", path, err)
	}
	return nil
}

// IsEnabled 报告 LaunchAgent plist 是否存在。
// 注:macOS 的 launchd 缓存 plist,删除后可能要等下次登录才生效;
// IsEnabled 只反映磁盘状态,不反映 launchd 进程内视图。
func IsEnabled() (bool, error) {
	dir, err := launchAgentsDir()
	if err != nil {
		return false, err
	}
	path := filepath.Join(dir, launchAgentFileName)
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("autostart: stat %s: %w", path, err)
}