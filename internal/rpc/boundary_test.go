// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package rpc

import (
	"os/exec"
	"strings"
	"testing"
)

// TestRPCLayerIsIndependent 断言传输层与契约层不反向依赖业务侧包。
//
// internal/rpc 是引擎与 UI 之间的接口层：它只认识契约 DTO 与传输抽象，
// 不该知道 store、scheduler、engine 这些业务侧的具体类型。一旦反向依赖，
// 传输层就没法在别的业务场景里复用，分层也就失去意义。
func TestRPCLayerIsIndependent(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "lgo_download_manager/internal/rpc").Output()
	if err != nil {
		t.Fatalf("go list -deps 失败: %v", err)
	}
	deps := strings.Split(string(out), "\n")
	forbidden := []string{
		"lgo_download_manager/internal/store",
		"lgo_download_manager/internal/scheduler",
		"lgo_download_manager/internal/engine",
		"lgo_download_manager/internal/ui",
		"lgo_download_manager/internal/uimgr",
	}
	for _, d := range deps {
		d = strings.TrimSpace(d)
		for _, f := range forbidden {
			if d == f {
				t.Fatalf("internal/rpc 不得依赖 %s", f)
			}
		}
	}
}

// TestTransportsDoNotImportRPC 保证两个传输实现不反向 import 传输层，
// 否则会形成 rpc → httpx → rpc 的包环。
//
// httpx 与 grpcx 各自声明了一份与 rpc.Transport/Handler 同方法集的本地接口，
// 靠结构化类型满足——这正是为了让它们不依赖 internal/rpc。这条测试守住
// 那个「靠结构化类型满足」的约定不被悄悄改回 import。
//
// 注意：查的是 go list -deps（不含测试文件）。两个传输的**测试**可以 import
// internal/rpc（Go 允许外部测试包这么做，不构成包环）。
func TestTransportsDoNotImportRPC(t *testing.T) {
	for _, pkg := range []string{
		"lgo_download_manager/internal/rpc/httpx",
		"lgo_download_manager/internal/rpc/grpcx",
	} {
		out, err := exec.Command("go", "list", "-deps", pkg).Output()
		if err != nil {
			t.Fatalf("go list -deps %s 失败: %v", pkg, err)
		}
		for _, d := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(d) == "lgo_download_manager/internal/rpc" {
				t.Fatalf("%s 不得 import internal/rpc", pkg)
			}
		}
	}
}

// TestAPIIsLeafPackage 断言契约层是叶子包：只允许依赖 internal/urlkind
// 与标准库。
//
// api 是两种传输共用的 DTO 与词汇表。它越纯，两种传输的契约就越不可能
// 走偏；一旦它引进了业务侧的类型，契约测试里的类型断言就会开始依赖
// 具体实现，而不是依赖契约本身。
func TestAPIIsLeafPackage(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "lgo_download_manager/internal/api").Output()
	if err != nil {
		t.Fatalf("go list -deps 失败: %v", err)
	}
	allowed := map[string]bool{
		"lgo_download_manager/internal/urlkind": true,
	}
	const (
		prefix = "lgo_download_manager/internal/"
		self   = "lgo_download_manager/internal/api"
	)
	for _, d := range strings.Split(string(out), "\n") {
		d = strings.TrimSpace(d)
		if !strings.HasPrefix(d, prefix) {
			continue // 标准库与第三方
		}
		if d == self {
			continue // go list -deps 会把包自身也列进来
		}
		if !allowed[d] {
			t.Fatalf("internal/api 只能依赖 internal/urlkind，却依赖了 %s", d)
		}
	}
}
