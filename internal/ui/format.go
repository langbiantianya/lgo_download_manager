// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"fmt"

	"lgo_download_manager/internal/ilocale"
)

// formatBytes 把字节数格式化为人类可读字符串（1024 进制）。
//
// 这段逻辑之前散在三个地方各写了一份，精度还不一样：任务列表用
// 「%.2f TB / %.1f MB」，状态栏与设置页的磁盘空间用「%.1f」，新建任务
// 对话框用「%.2f」而且没有 TB——同一个应用里两种写法。现在只有这一份。
//
// n <= 0 统一显示 "0 B"：「大小未知」是任务级的语义（有些任务拿不到
// 总大小），由调用方 taskSizeText 根据 TotalSize 判断，不该由格式化
// 函数替它决定。
func formatBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	const (
		KB = 1 << 10
		MB = 1 << 20
		GB = 1 << 30
		TB = 1 << 40
	)
	switch {
	case n >= TB:
		return fmt.Sprintf("%.2f TB", float64(n)/TB)
	case n >= GB:
		return fmt.Sprintf("%.2f GB", float64(n)/GB)
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/KB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// taskSizeText 返回任务大小的显示文本：拿不到总大小时显示本地化的
// 「未知大小」，否则用标准的人类可读形式。
func taskSizeText(total int64) string {
	if total <= 0 {
		return ilocale.T("taskRow.size.unknown")
	}
	return formatBytes(total)
}
