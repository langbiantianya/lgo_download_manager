// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// 本文件放进度相关的纯计算。节流状态（上次采样时间与字节数）留在
// Run 的作用域内——它被多个分片 goroutine 并发访问，靠 CAS 串行化。
package engine

import (
	"time"
)

// speed 计算两次采样之间的平均速度（字节/秒）。
//
// elapsed 非正时返回 0：节流窗口还没走完（或时钟回拨）时不能除零，
// 也不能报一个负速度给 UI。
func speed(delta int64, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(delta) / elapsed.Seconds()
}
