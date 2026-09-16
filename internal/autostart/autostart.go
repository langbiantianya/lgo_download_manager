// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package autostart 把「开机自启」抽象为三个跨平台原语:
//
//	Enable()    — 在操作系统的登录启动项里注册本程序,带上静默参数
//	Disable()   — 撤销注册;若本程序从未注册过,返回 nil
//	IsEnabled() — 报告本程序当前是否已注册(允许外部工具直接修改后回读)
//
// 静默自启要求被拉起的进程不主动显示主窗口。各平台的具体注册方式:
//
//   - Windows:HKCU\Software\Microsoft\Windows\CurrentVersion\Run,
//     ValueName=LGDM,ValueData="<exe> --autostart"。注册到 HKCU 不需
//     提权,卸载/迁移用户态即可生效。Registry 路径常量见 autostart_windows.go。
//   - Linux:$XDG_CONFIG_HOME/autostart/lgo_download_manager.desktop
//     (缺省 $XDG_CONFIG_HOME 时回退到 ~/.config)。Type=Application +
//     X-GNOME-Autostart-enabled=true,GNOME/KDE/XFCE 等遵循 Freedesktop
//     自启动规范的桌面环境都会拉起。
//   - macOS:~/Library/LaunchAgents/org.langbiantianya.LGDM.plist,
//     Label=org.langbiantianya.LGDM,ProgramArguments 带 --autostart;
//     RunAtLoad=true。launchd 登录后由 per-user agent 域拉起。
//
// 本包不修改 SQLite,也不动 settings.AutoStart 字段;开关位与「系统侧是否
// 真正注册」由 settings.ApplyAutoStart 在 Save 时调和(磁盘写入失败只
// 记日志,不让 UI 提交整笔失败)。
package autostart

// FlagName 是业务进程识别「这次是被自启拉起的」命令行参数。
// 各平台在注册时把 FlagName 追加到可执行文件路径后;main.go 读到它
// 就跳过 UI 子进程的拉起,只保留调度器 + 系统托盘。
const FlagName = "--autostart"

// AppID 是跨平台共用的应用标识。
//   - Windows:HKCU Run 的 ValueName
//   - Linux:autostart .desktop 文件名(去掉 .desktop 后缀与 AppID 等价)
//   - macOS:LaunchAgent plist 文件名与 Label
//
// 改这里意味着迁移旧的注册项;首次发布固定不变。
const AppID = "lgo_download_manager"