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

// flatpakEnvID 是 Flatpak 运行应用时注入沙箱的应用 ID 环境变量,
// 也是沙箱内唯一能同时回答「在不在 Flatpak 里」和「是哪个应用」的判据
// (沙箱里没有别的进程能看到宿主上安装的应用 ID)。
const flatpakEnvID = "FLATPAK_ID"

// nonFlatpakIcon 是发行版包 / 自编译安装里图标主题应提供的图标名,
// 与 conf/lgo_download_manager.desktop 的 Icon= 一致。
// Flatpak 沙箱里不用它:Flatpak 导出图标时统一改用应用 ID 命名,
// 见 desktopEntryContent。
const nonFlatpakIcon = "org.langbiantianya.LGDM"

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
// 占位符依次是:Exec 命令(可执行文件路径或 flatpak run 前缀)、静默参数、
// 图标名、仅 Flatpak 需要的附加字段行。
//
// 静默自启由业务进程读 --autostart 后跳过 UI 拉起来实现,
// 见 internal/settings.ApplyAutoStart + main.go 启动路径。
const desktopEntry = `[Desktop Entry]
Type=Application
Name=LGo Download Manager
Comment=Download manager (autostart)
Exec=%s %s
Icon=%s
Terminal=false
NoDisplay=true
StartupNotify=false
X-GNOME-Autostart-enabled=true%s
`

// flatpakAppID 返回当前进程所在 Flatpak 沙箱的应用 ID;不在沙箱内时返回空串。
func flatpakAppID() string {
	return os.Getenv(flatpakEnvID)
}

// desktopEntryContent 生成自启 .desktop 的内容。
//
// 非 Flatpak:Exec 直接指向当前二进制,桌面环境登录后原地拉起它。
//
// Flatpak:沙箱里的 /app/bin/lgo_download_manager 在宿主机上并不存在
// (它是挂载进沙箱的 /app 里的文件),写成路径宿主的桌面环境根本起不来;
// 必须写成 `flatpak run <app-id> --autostart`,由宿主上的 flatpak 重新
// 进入沙箱拉起同一个应用。应用 ID 取运行时注入的 FLATPAK_ID,不在这里
// 硬编码——沙箱里的进程只可能是被 flatpak 按某个 ID 拉起来的。
// X-Flatpak 让桌面环境把这条自启项归属到对应 Flatpak 应用
// (GNOME/KDE 用它做启动器归并);Icon 同理用应用 ID,那是 Flatpak
// 往宿主图标主题里导出图标时使用的名字。
func desktopEntryContent() (string, error) {
	if appID := flatpakAppID(); appID != "" {
		return fmt.Sprintf(desktopEntry, "flatpak run "+appID, FlagName, appID, "\nX-Flatpak="+appID), nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("autostart: resolve executable: %w", err)
	}
	return fmt.Sprintf(desktopEntry, exe, FlagName, nonFlatpakIcon, ""), nil
}

// autostartDir 返回桌面环境读取自启项的目录。
//
// 非 Flatpak:$XDG_CONFIG_HOME/autostart/,$XDG_CONFIG_HOME 未设置时按
// XDG Base Directory Specification 回退到 $HOME/.config/autostart,
// 与 conf/lgo_download_manager.desktop 同一层。
//
// Flatpak:沙箱里的 $XDG_CONFIG_HOME 被 Flatpak 指向应用私有目录
// ($HOME/.var/app/<app-id>/config),写在那里宿主桌面环境看不到,自启等于
// 没注册;必须写宿主家目录下的 $HOME/.config/autostart —— 该目录由
// manifest 的 finish-args `--filesystem=xdg-config/autostart` 绑定进沙箱
// (更宽的 --filesystem=home 同样覆盖它)。沙箱里的 $HOME 就是宿主家目录,
// 只有 XDG_* 被重定向,所以这里刻意绕开 $XDG_CONFIG_HOME。
func autostartDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" && flatpakAppID() == "" {
		return filepath.Join(dir, "autostart"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("autostart: resolve home: %w", err)
	}
	return filepath.Join(home, ".config", "autostart"), nil
}

// flatpakWriteErr 在 Flatpak 沙箱里给写入失败补一句权限提示:宿主
// ~/.config/autostart 只有被 finish-args 映射进来才可写,否则只会得到
// EROFS/EACCES,单看错误信息判断不出是缺权限而不是磁盘问题。
func flatpakWriteErr(err error) error {
	if flatpakAppID() == "" {
		return err
	}
	return fmt.Errorf("%w (flatpak: 宿主自启目录未映射,检查 finish-args 的 --filesystem=xdg-config/autostart)", err)
}

// Enable 写入 <autostartDir>/lgo_download_manager.desktop。
// 目标目录不存在时一并创建(0o755),与首次安装兼容;已有文件时覆盖。
func Enable() error {
	content, err := desktopEntryContent()
	if err != nil {
		return err
	}
	dir, err := autostartDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return flatpakWriteErr(fmt.Errorf("autostart: create %s: %w", dir, err))
	}
	path := filepath.Join(dir, desktopFileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return flatpakWriteErr(fmt.Errorf("autostart: write %s: %w", path, err))
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
