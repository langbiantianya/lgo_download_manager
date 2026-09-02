// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build !linux

package ui

func listMountsImpl() []diskInfo {
	free, total, err := diskUsage(".")
	if err != nil || total == 0 {
		return nil
	}
	used := total - free
	if used < 0 {
		used = 0
	}
	frac := float32(used) / float32(total)
	if frac > 1 {
		frac = 1
	}
	return []diskInfo{{Label: ".", Free: free, Total: total, Used: used, Frac: frac}}
}
