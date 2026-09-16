// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

// openFolder 让操作系统打开 dir(资源管理器 / Finder / 文件管理器)。
// openFile 让操作系统用默认应用打开 file。
//
// 这两个函数是 task_row.go 里「打开文件夹」「打开文件」按钮的入口;
// 原来在 task_row.go 直接 exec.Command("xdg-open", ...) 在 Windows
// 上必然失败(xdg-open 不存在),且 macOS 上同样不可用。改为平台拆分
// 的实现:Linux → xdg-open,macOS → open,Windows → ShellExecuteW。
//
// 错误一律吞掉:打开类按钮的最佳行为是失败时不弹错(用户会重试),
// 否则每次点一个不存在的路径都跳错窗,体验差。
func openFolder(dir string)  { platformOpenFolder(dir) }
func openFile(file string)  { platformOpenFile(file) }