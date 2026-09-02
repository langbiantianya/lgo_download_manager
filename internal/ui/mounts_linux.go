// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

//go:build linux

package ui

import (
	"bufio"
	"os"
	"strings"
)

// pseudoFS 是不应显示的虚拟文件系统类型。
var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "devpts": true, "devtmpfs": true,
	"tmpfs": true, "cgroup": true, "cgroup2": true, "sysctl": true,
	"bpf": true, "autofs": true, "mqueue": true, "pstore": true,
	"fusectl": true, "configfs": true, "debugfs": true, "tracefs": true,
	"hugetlbfs": true, "ramfs": true, "binfmt_misc": true,
	"securityfs": true, "selinuxfs": true, "efivarfs": true,
	"nsfs": true, "overlay": true, "squashfs": true, "fuse.gvfsd-fuse": true,
}

// listMountsImpl 读 /proc/mounts 返回真实挂载的物理文件系统。
//
// Linux 策略：去重（同 device 只保留一次） + 跳 pseudo FS。
func listMountsImpl() []diskInfo {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return fallbackSingle()
	}
	defer f.Close()

	type entry struct {
		dev, mount, fs string
	}
	var raw []entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		raw = append(raw, entry{dev: fields[0], mount: fields[1], fs: fields[2]})
	}

	seen := map[string]bool{}
	var out []diskInfo
	for _, e := range raw {
		// Linux 上只显示主挂载点 "/"——避免子挂载（/home、/boot 等）
		// 让 UI 看起来重复。
		if e.mount != "/" {
			continue
		}
		if pseudoFS[e.fs] {
			continue
		}
		seen[e.mount] = true
		free, total, err := diskUsage(e.mount)
		if err != nil || total == 0 {
			continue
		}
		used := total - free
		if used < 0 {
			used = 0
		}
		var frac float32
		if total > 0 {
			frac = float32(used) / float32(total)
			if frac > 1 {
				frac = 1
			}
		}
		label := e.mount
		// 把 / 改为 "root" 便于阅读。
		if label == "/" {
			label = "root"
		}
		out = append(out, diskInfo{
			Label: label,
			Free:  free,
			Total: total,
			Used:  used,
			Frac:  frac,
		})
	}
	return out
}

func fallbackSingle() []diskInfo {
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
