// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package logging 是 lgdm 进程内唯一的日志门面。它在底层使用
// log/slog,对外暴露与标准库 "log" 兼容的 Printf/Println/Fatalf 等
// 便捷函数,供旧 callsite 平滑迁移,又允许新代码直接使用
// *slog.Logger 做结构化日志。
//
// 输出目的地由 Init 决定:
//   - debug=true:直接写到 os.Stderr(控制台可见,无文件)。
//   - debug=false:写到 dir 目录下,当日文件为 lgdm.log,
//     历史文件按 yyyymmdd 后缀命名;启动时清掉超过 7 天的旧文件。
//
// 进程生命周期内每天 0 点(或下一次跨日写入)自动 rotate。
package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// 日志文件名相关常量。
const (
	logBaseName  = "lgdm.log"          // 当日文件名
	retentionDays = 7                  // 历史日志保留天数
	dateLayout   = "20060102"          // 历史文件名后缀的日期格式
	timeFormat   = "2006-01-02T15:04:05.000Z07:00" // slog HandlerOptions 默认 time 键格式
)

var logFilePattern = regexp.MustCompile(`^lgdm\.log\.(\d{8})$`)

// globalState 在进程内保存当前 logger 与文件 sink。Init 可被多次调用
// (例如测试),每次调用都重新安装默认 logger。
type globalState struct {
	mu     sync.Mutex
	logger *slog.Logger
	sink   *rotatingSink // 非 nil 时表示正在写文件
}

var state globalState

// rotatingSink 把 slog.Handler 包到可自动 rotate 的文件写入器上。
// 每次写入时核对当前日期:跨日就关闭旧文件、改名为 lgdm.log.yyyymmdd,
// 再打开新的 lgdm.log 继续写。rotate 与 pruneOld 由 Init 启动时调用。
type rotatingSink struct {
	dir        string
	mu         sync.Mutex
	current    *os.File
	currentDay time.Time // 文件对应的本地日期 00:00
}

// Init 配置进程全局 slog logger。重复调用会替换之前的 logger。
//
//   - debug=true: 所有日志走 os.Stderr(无文件,无历史)。
//   - debug=false: 日志落到 dir 目录下;若 dir 为空,使用
//     os.TempDir()/lgo_download_manager。
//
// dir 通常由调用方传入 defaultConfigPath()(或显式 --config 参数),
// 这样日志与业务库在同一用户级数据目录,便于运维统一收集。
func Init(debug bool, dir string) error {
	state.mu.Lock()
	defer state.mu.Unlock()

	if state.sink != nil {
		_ = state.sink.closeLocked()
		state.sink = nil
	}

	if debug {
		h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})
		state.logger = slog.New(h)
		slog.SetDefault(state.logger)
		return nil
	}

	if dir == "" {
		dir = filepath.Join(os.TempDir(), "lgo_download_manager")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("logging: create log dir %q: %w", dir, err)
	}

	sink, err := newRotatingSink(dir)
	if err != nil {
		return err
	}
	pruneOld(dir, time.Now(), retentionDays)

	h := slog.NewTextHandler(sink, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	state.sink = sink
	state.logger = slog.New(h)
	slog.SetDefault(state.logger)
	return nil
}

// L 返回当前默认 *slog.Logger,供需要结构化日志的代码使用。
// 调用前必须先 Init,否则返回 slog.Default()(通常是 stderr 输出)。
func L() *slog.Logger {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.logger != nil {
		return state.logger
	}
	return slog.Default()
}

// Close 关闭当前文件 sink;debug 模式下无操作。
// main 退出前调用一次,确保最后几行 flush 到磁盘。
func Close() error {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.sink == nil {
		return nil
	}
	err := state.sink.closeLocked()
	state.sink = nil
	return err
}

// ---------------------------------------------------------------------------
// stdlib "log" 兼容的便捷函数。
// 这些函数让旧 callsite 用最小改动从 "log" 迁到本包:
//   log.Printf("...")  ->  logging.Printf("...")
//   log.Println("...") ->  logging.Println("...")
//   log.Fatalf("...") ->  logging.Fatalf("...")
// ---------------------------------------------------------------------------

// Printf 用 Info 级别写一条日志。format/args 语义同 fmt.Sprintf。
func Printf(format string, args ...any) {
	L().Info(formatMsg(format, args...))
}

// Println 用 Info 级别写一条日志,各参数以空格拼接。
func Println(args ...any) {
	L().Info(strings.TrimSuffix(fmt.Sprintln(args...), "\n"))
}

// Fatalf 用 Error 级别写一条日志后 os.Exit(1)。
func Fatalf(format string, args ...any) {
	L().Error(formatMsg(format, args...))
	os.Exit(1)
}

// Fatal 用 Error 级别写一条日志后 os.Exit(1)。
func Fatal(v ...any) {
	L().Error(strings.TrimSuffix(fmt.Sprintln(v...), "\n"))
	os.Exit(1)
}

// formatMsg 让 %v 之类的 verb 仍然按 fmt 规则展开;最终 slog 记录的就是
// 拼好的字符串,既保留了原 log.Printf 的可读性,又用 Info/Error 把
// 严重程度带到了结构化字段里。
func formatMsg(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// ---------------------------------------------------------------------------
// 旋转文件 sink 实现。
// ---------------------------------------------------------------------------

func newRotatingSink(dir string) (*rotatingSink, error) {
	s := &rotatingSink{dir: dir}
	if err := s.openForDayLocked(time.Now()); err != nil {
		return nil, err
	}
	return s, nil
}

// Write 实现 io.Writer。slog.Handler 在每次记录时会调用本方法,
// 我们借此机会检查日期是否变更 —— 因为文件被 rename 后再 reopen
// 是少数几次写,绝大多数调用都是同一个文件,所以这里取一次系统时钟
// 的成本可以忽略。
func (s *rotatingSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.rotateIfNeededLocked(time.Now()); err != nil {
		return 0, err
	}
	return s.current.Write(p)
}

// rotateIfNeededLocked 若跨日则关闭当前文件、改名为 lgdm.log.yyyymmdd,
// 然后打开新一天的 lgdm.log。锁由调用方负责持有。
func (s *rotatingSink) rotateIfNeededLocked(now time.Time) error {
	day := startOfDay(now)
	if s.current != nil && day.Equal(s.currentDay) {
		return nil
	}
	if s.current != nil {
		// 把"上一日"的文件改名为 lgdm.log.<yyyymmdd> 再 reopen。
		prevDay := s.currentDay
		oldPath := filepath.Join(s.dir, logBaseName)
		newPath := filepath.Join(s.dir, logBaseName+"."+prevDay.Format(dateLayout))
		if err := s.current.Close(); err != nil {
			return fmt.Errorf("logging: close current log: %w", err)
		}
		s.current = nil
		// 重命名时如果目标已存在(罕见,例如上次进程崩溃在 rotate 那一刻),
		// 覆盖即可 —— 旧文件没写满,丢了也不影响当天数据。
		if err := os.Rename(oldPath, newPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("logging: rotate %s -> %s: %w", oldPath, newPath, err)
		}
	}
	if err := s.openForDayLocked(now); err != nil {
		return err
	}
	// 当 rotate 真正发生时,顺便清一遍超过 retentionDays 的旧文件;
	// 24h 写一次 prune,频度足够,无需独立定时器。
	pruneOld(s.dir, now, retentionDays)
	return nil
}

func (s *rotatingSink) openForDayLocked(now time.Time) error {
	path := filepath.Join(s.dir, logBaseName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("logging: open %s: %w", path, err)
	}
	s.current = f
	s.currentDay = startOfDay(now)
	return nil
}

func (s *rotatingSink) closeLocked() error {
	if s.current == nil {
		return nil
	}
	err := s.current.Close()
	s.current = nil
	return err
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// pruneOld 删除 dir 下日期早于 (now - retentionDays) 的 lgdm.log.yyyymmdd。
// "今天"对应的 lgdm.log.yyyymmdd 是不存在的(date 文件不带今天),
// 所以这里只看过去 N 天。
func pruneOld(dir string, now time.Time, days int) {
	cutoff := startOfDay(now).AddDate(0, 0, -days)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		m := logFilePattern.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		t, err := time.ParseInLocation(dateLayout, m[1], time.Local)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// 用于测试或运行时打印当前日志配置。
func LogDir() string {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.sink == nil {
		return ""
	}
	return state.sink.dir
}

// SetCurrentDayForTest 只供测试使用:把当前 sink 的 currentDay
// 设为给定日期的下一次跨日 rotate 会触发的目标,模拟「跨日」。
// 返回的 day 即下一次 rotate 检测时认为的「今天」。
// 生产代码禁止调用。
func SetCurrentDayForTest(d time.Time) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.sink == nil {
		return
	}
	state.sink.mu.Lock()
	state.sink.currentDay = d
	state.sink.mu.Unlock()
}

// 防止 io.Discard 在 build tag 变化时未使用的编译告警。
var _ = io.Discard
var _ = runtime.GOOS