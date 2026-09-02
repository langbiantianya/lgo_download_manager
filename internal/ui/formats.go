// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"fmt"
	"time"

	"lgo_download_manager/internal/store"
)

// formatSize 将字节数格式化为人类可读字符串。
func formatSize(n int64) string {
	const KB = 1 << 10
	const MB = 1 << 20
	const GB = 1 << 30
	switch {
	case n >= GB:
		return fmt.Sprintf("%.2f GB", float64(n)/GB)
	case n >= MB:
		return fmt.Sprintf("%.2f MB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.2f KB", float64(n)/KB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// humanBytes 简短的人类可读字符串。
func humanBytes(n int64) string {
	const (
		KB = 1 << 10
		MB = 1 << 20
		GB = 1 << 30
		TB = 1 << 40
	)
	switch {
	case n >= TB:
		return fmt.Sprintf("%.1f TB", float64(n)/TB)
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/GB)
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/KB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// formatBytes 二进制单位后缀格式化 n（用于详情面板）。
func formatBytes(n int64) string {
	if n <= 0 {
		return "未知大小"
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

// formatBPS 以字节/秒为单位格式化速度。
func formatBPS(bps float64) string {
	if bps <= 0 {
		return "--"
	}
	return formatBytes(int64(bps)) + "/s"
}

// etaText 估算正在运行任务的剩余时间。
func etaText(t *store.Task, bps float64) string {
	if bps <= 0 || t.TotalSize <= 0 {
		return "ETA --"
	}
	remaining := t.TotalSize - t.Downloaded
	if remaining <= 0 {
		return "ETA 0 秒"
	}
	secs := int(float64(remaining) / bps)
	if secs < 0 {
		secs = 0
	}
	return "ETA " + formatRemainingTime(secs)
}

// formatRemainingTime 将剩余秒数渲染为简短的中文字符串。
func formatRemainingTime(secs int) string {
	if secs <= 0 {
		return "--"
	}
	if secs < 60 {
		return fmt.Sprintf("剩 %d 秒", secs)
	}
	m := secs / 60
	s := secs % 60
	if m < 60 {
		return fmt.Sprintf("剩 %dm %ds", m, s)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("剩 %dh %dm", h, m)
}

// formatTime 把 time.Time 渲染为本地时区的 YYYY-MM-DD HH:MM:SS 字符串。
// 零值返回 "-" 表示尚未发生。
func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// displayName 返回任务的人类可读文件名。
func displayName(t *store.Task) string {
	if t.SavePath != "" {
		return filepathBase(t.SavePath)
	}
	return t.URL
}

// taskName 返回任务保存路径中的文件名部分。
func taskName(t *store.Task) string {
	return filepathBase(t.SavePath)
}

// uaForTask 返回应用于该任务下载的 User-Agent 字符串。
func uaForTask(t *store.Task) string {
	if t.AuthData == "" {
		return "UA: Wget/1.21.3 (default)"
	}
	// 这里只显示一个摘要——UA 串直接嵌入 authData 字段。
	return "UA: " + t.AuthData
}

// filepathBase 是 filepath.Base 的薄封装，避免在多个文件里反复 import。
func filepathBase(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
