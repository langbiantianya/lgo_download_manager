// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build linux

package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

const desktopFileName = AppID + ".desktop"

// desktopEntry 严格遵循 freedesktop Desktop Entry Specification 1.5:
//
//   - Type=Application + Exec 触发登录后由桌面环境拉起
//   - X-GNOME-Autostart-enabled=true 是 GNOME 兼容字段;
//     KDE/XFCE/Cinnamon 等遵循 XDG Autostart Spec 的环境只关心文件位于
//     $XDG_CONFIG_HOME/autostart/,该字段被忽略(无害)
//   - NoDisplay=true 让 .desktop 不出现在应用菜单/启动器里,
//     因为这是「自启用途」而非「普通应用快捷方式」
//   - Terminal=false:不需要终端窗口(自启进程无控制台,会立刻退出)
//   - StartupNotify=false:启动后没有主窗口,无 startup notification 意义
//
// 静默自启由业务进程读 --autostart 后跳过 UI 拉起来实现,
// 见 internal/settings.ApplyAutoStart + main.go 启动路径。
const desktopEntry = `[Desktop Entry]
Type=Application
Name=LGo Download Manager
Comment=Download manager (autostart)
Exec=%s %s
Icon=org.langbiantianya.LGDM
Terminal=false
NoDisplay=true
StartupNotify=false
X-GNOME-Autostart-enabled=true
`

// autostartDir 返回 $XDG_CONFIG_HOME/autostart/(缺省 ~/.config/autostart)。
// $XDG_CONFIG_HOME 未设置时按 XDG Base Directory Specification
// 回退到 $HOME/.config,与 conf/lgo_download_manager.desktop 同一层。
func autostartDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "autostart"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("autostart: resolve home: %w", err)
	}
	return filepath.Join(home, ".config", "autostart"), nil
}

// Enable 写入 $XDG_CONFIG_HOME/autostart/lgo_download_manager.desktop。
// 目标目录不存在时一并创建(0o755),与首次安装兼容;已有文件时覆盖。
func Enable() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("autostart: resolve executable: %w", err)
	}
	dir, err := autostartDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("autostart: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, desktopFileName)
	content := fmt.Sprintf(desktopEntry, exe, FlagName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("autostart: write %s: %w", path, err)
	}
	return nil
}

// Disable 删除自启 .desktop 文件;不存在不报错(幂等)。
func Disable() error {
	dir, err := autostartDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, desktopFileName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("autostart: remove %s: %w", path, err)
	}
	return nil
}

// IsEnabled 报告自启 .desktop 是否存在。
// 注意:文件存在不代表内容正确——若用户手工编辑过,业务侧 ApplyAutoStart
// 在 Save 时仍会重写一次以确保 Exec 与当前二进制匹配。
func IsEnabled() (bool, error) {
	dir, err := autostartDir()
	if err != nil {
		return false, err
	}
	path := filepath.Join(dir, desktopFileName)
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("autostart: stat %s: %w", path, err)
}