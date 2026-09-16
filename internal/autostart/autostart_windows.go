// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build windows

package autostart

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

// runKeyPath 是 HKCU 下的 Run 注册表项路径,Windows 登录后,
// explorer.exe 会枚举该项下所有 Value 并执行 ValueData 指定的命令。
// 用 HKCU 而非 HKLM 是为了「用户态安装、不需要管理员权限」。
//
// 见 https://learn.microsoft.com/windows/win32/setup/run-and-runonce
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// Enable 在 HKCU Run 里写入 LGDM -> "<exe> --autostart"。
// 已存在同名 Value 时覆盖,确保参数变化(例如未来加 --config)立即生效。
func Enable() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("autostart: resolve executable: %w", err)
	}
	// 用 REG_EXPAND_SZ 而非 REG_SZ 让 %PATH%/%USERPROFILE% 这类环境变量
	// 在登录阶段被正常展开——此时 USERPROFILE 已是新登录用户的 profile,
	// 不存在循环依赖。
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("autostart: open HKCU Run: %w", err)
	}
	defer k.Close()
	// 双引号包住 exe 路径:Windows 路径可能含空格/Program Files,
	// 命令行解析时需要保留为单个 token。
	cmd := `"` + exe + `" ` + FlagName
	if err := k.SetStringValue(AppID, cmd); err != nil {
		return fmt.Errorf("autostart: write Run value: %w", err)
	}
	return nil
}

// Disable 删除 HKCU Run 下的 LGDM Value;不存在不报错。
func Disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return fmt.Errorf("autostart: open HKCU Run: %w", err)
	}
	defer k.Close()
	// DeleteValue 在目标不存在时返回 ErrNotExist,吞掉以满足「幂等关闭」。
	if err := k.DeleteValue(AppID); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("autostart: delete Run value: %w", err)
	}
	return nil
}

// IsEnabled 返回 HKCU Run 下是否存在 LGDM Value。
// 用于 UI 显示当前真实状态(用户可能用第三方工具直接改注册表)。
func IsEnabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return false, nil
		}
		return false, fmt.Errorf("autostart: open HKCU Run: %w", err)
	}
	defer k.Close()
	_, _, err = k.GetStringValue(AppID)
	if err == registry.ErrNotExist {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("autostart: query Run value: %w", err)
	}
	return true, nil
}