// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package logging

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDebugModeWritesToStderr 验证 --debug=true 时日志走 stderr,
// 不产生任何文件。stderr 临时重定向到管道读取以便断言。
func TestDebugModeWritesToStderr(t *testing.T) {
	dir := t.TempDir()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	if err := Init(true, dir); err != nil {
		t.Fatalf("Init(debug): %v", err)
	}
	defer Close()

	Printf("debug-mode-message-%d", time.Now().UnixNano())
	_ = w.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if !strings.Contains(buf.String(), "debug-mode-message-") {
		t.Fatalf("expected debug-mode line, got %q", buf.String())
	}
	// debug 模式不应该在 dir 里建任何文件
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("debug mode leaked files into %s: %v", dir, entries)
	}
}

// TestFileModeCreatesLgdmLog 验证非 debug 模式会在 dir/lgdm.log
// 写入第一条日志。
func TestFileModeCreatesLgdmLog(t *testing.T) {
	dir := t.TempDir()
	if err := Init(false, dir); err != nil {
		t.Fatalf("Init(file): %v", err)
	}
	defer Close()

	Printf("file-mode-line-%d", time.Now().UnixNano())
	if err := Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, logBaseName))
	if err != nil {
		t.Fatalf("read %s: %v", logBaseName, err)
	}
	if !strings.Contains(string(data), "file-mode-line-") {
		t.Fatalf("expected log line, got %q", string(data))
	}
}

// TestPruneRemovesOldFiles 验证 pruneOld 把超过保留天数的文件删掉。
func TestPruneRemovesOldFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// 在 dir 里手工放几个历史日期文件:其中 2 个超过 7 天,1 个未超。
	mkDay := func(off int) string {
		d := startOfDay(now).AddDate(0, 0, off)
		name := fmt.Sprintf("%s.%s", logBaseName, d.Format(dateLayout))
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
		return path
	}
	old1 := mkDay(-10)
	old2 := mkDay(-8)
	recent := mkDay(-3)
	today := mkDay(0)

	pruneOld(dir, now, retentionDays)

	for _, p := range []string{old1, old2} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("expected %s pruned, stat err=%v", p, err)
		}
	}
	for _, p := range []string{recent, today} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s kept, err=%v", p, err)
		}
	}
}

// TestRotateRenamesCurrentAndOpensNew 模拟跨日:Init 打开 sink
// 后,把它的 currentDay 倒回到昨天,然后写一行日志,应触发
// rotate:旧文件改名为 lgdm.log.<昨天>,新 lgdm.log 接收新行。
func TestRotateRenamesCurrentAndOpensNew(t *testing.T) {
	dir := t.TempDir()
	if err := Init(false, dir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	yesterday := startOfDay(time.Now()).AddDate(0, 0, -1)
	SetCurrentDayForTest(yesterday)

	Printf("today-after-rotate-%d", time.Now().UnixNano())
	if err := Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	prevName := logBaseName + "." + yesterday.Format(dateLayout)
	if _, err := os.Stat(filepath.Join(dir, prevName)); err != nil {
		t.Fatalf("expected rotated %s: %v", prevName, err)
	}
	todayData, err := os.ReadFile(filepath.Join(dir, logBaseName))
	if err != nil {
		t.Fatalf("read today's %s: %v", logBaseName, err)
	}
	if !strings.Contains(string(todayData), "today-after-rotate-") {
		t.Fatalf("today file missing new line: %q", string(todayData))
	}
}

// TestInitIdempotent 验证重复调用 Init 不会让旧文件句柄泄漏。
func TestInitIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		if err := Init(false, dir); err != nil {
			t.Fatalf("Init #%d: %v", i, err)
		}
		Printf("iter-%d", i)
	}
	if err := Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}