// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"sync/atomic"
	"time"
)

// debouncer 把连续触发合并成一次延迟执行。
//
// 典型用途是「用户还在输入」的场景:每敲一个字符就发一次请求会在网络
// 上打出一串无用的请求,这里等到用户停下来(delay 内没有新的触发)才真正
// 执行一次。
//
// 每次触发都会拿到一个递增的序号,调用方可以据此判断「回来的结果是不是
// 最后一次触发的」——异步请求早发晚归时,旧结果不应该覆盖新结果。
type debouncer struct {
	delay time.Duration
	seq   atomic.Uint64
	timer *time.Timer
}

// Trigger 记录一次触发,并(重新)安排一次延迟执行。delay 内的重复触发
// 只保留最后一次。返回本次触发的序号,供 IsCurrent 判断新旧。
//
// fn 在定时器线程上被调用(不是 UI 线程),需要更新界面的话自己用
// fyne.Do 切回去。
func (d *debouncer) Trigger(fn func(seq uint64)) uint64 {
	seq := d.seq.Add(1)
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.delay, func() { fn(seq) })
	return seq
}

// IsCurrent 判断 seq 是不是最近一次触发的序号。
func (d *debouncer) IsCurrent(seq uint64) bool {
	return d.seq.Load() == seq
}

// Stop 取消尚未执行的触发。界面关闭时调用,免得对话框没了还在发请求。
func (d *debouncer) Stop() {
	if d.timer != nil {
		d.timer.Stop()
	}
}
