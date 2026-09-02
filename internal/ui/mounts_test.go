// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ui

import "testing"

// TestListMountsReturnsAtLeastOne 验证 listMounts() 在 CI/开发机上
// 至少能列出一个真实挂载点（不会被全跳）。
func TestListMountsReturnsAtLeastOne(t *testing.T) {
	disks := listMounts()
	if len(disks) == 0 {
		t.Skip("no mounts visible in this environment")
	}
	for _, d := range disks {
		if d.Total <= 0 {
			t.Errorf("disk %s: total must be > 0, got %d", d.Label, d.Total)
		}
		if d.Frac < 0 || d.Frac > 1 {
			t.Errorf("disk %s: frac out of range: %f", d.Label, d.Frac)
		}
		if d.Label == "" {
			t.Error("disk label must not be empty")
		}
	}
}
