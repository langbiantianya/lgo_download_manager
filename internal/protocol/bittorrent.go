// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package protocol

import (
	"lgo_download_manager/internal/urlkind"
)

// MagnetName 提取磁力链接里的 dn 参数。
func MagnetName(raw string) string { return urlkind.MagnetName(raw) }

// MagnetInfoHash 提取磁力链接里的 infohash。
func MagnetInfoHash(raw string) string { return urlkind.MagnetInfoHash(raw) }

// TorrentNameFromPath 从 .torrent 的 URL 或本地路径推导种子名。
func TorrentNameFromPath(raw string) string { return urlkind.TorrentNameFromPath(raw) }

// SafeDirName 把任意种子名/文件名清洗成可以安全用作目录名的字符串。
func SafeDirName(name, fallback string) string { return urlkind.SafeDirName(name, fallback) }
