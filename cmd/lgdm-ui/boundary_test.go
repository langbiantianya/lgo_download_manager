// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestUIDoesNotLinkBusinessPackages 守住独立 UI 的核心收益：它的依赖图里
// 不能出现 store、scheduler、协议、BitTorrent 或业务服务。
func TestUIDoesNotLinkBusinessPackages(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "lgo_download_manager/cmd/lgdm-ui").Output()
	if err != nil {
		t.Fatalf("go list -deps 失败: %v", err)
	}
	forbidden := map[string]bool{
		"lgo_download_manager/internal/store":       true,
		"lgo_download_manager/internal/scheduler":   true,
		"lgo_download_manager/internal/engine":      true,
		"lgo_download_manager/internal/protocol":    true,
		"lgo_download_manager/internal/bittorrent":  true,
		"lgo_download_manager/internal/appservice":  true,
		"lgo_download_manager/internal/settings":    true,
		"lgo_download_manager/internal/urllauncher": true,
		"lgo_download_manager/internal/handoff":     true,
		"lgo_download_manager/internal/tray":        true,
	}
	for _, dep := range strings.Split(string(out), "\n") {
		if forbidden[strings.TrimSpace(dep)] {
			t.Fatalf("独立 lgdm-ui 不得依赖 %s", strings.TrimSpace(dep))
		}
	}
}
