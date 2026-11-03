// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import (
	"os/exec"
	"strings"
	"testing"
)

// TestUIDependsOnlyOnContract 断言 UI 包只依赖契约层，不直接依赖
// store / scheduler / engine / protocol / bittorrent / appservice。
//
// 用的是 go list -deps（不含测试文件的 import）：UI 的测试为了造数据需要
// import store / scheduler / appservice，那是刻意允许的例外（见
// testenv_test.go 顶部的说明）；生产代码则必须守住这条边界——它一旦被
// 打破，UI 就重新和业务侧的具体类型耦合，分层改造等于白做。
func TestUIDependsOnlyOnContract(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "lgo_download_manager/internal/ui").Output()
	if err != nil {
		t.Fatalf("go list -deps 失败: %v", err)
	}
	deps := strings.Split(string(out), "\n")
	forbidden := []string{
		"lgo_download_manager/internal/store",
		"lgo_download_manager/internal/scheduler",
		"lgo_download_manager/internal/engine",
		"lgo_download_manager/internal/protocol",
		"lgo_download_manager/internal/bittorrent",
		"lgo_download_manager/internal/appservice",
	}
	for _, d := range deps {
		d = strings.TrimSpace(d)
		for _, f := range forbidden {
			if d == f {
				t.Fatalf("internal/ui 不得依赖 %s", f)
			}
		}
	}
}
