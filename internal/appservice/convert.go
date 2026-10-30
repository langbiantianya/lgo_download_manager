// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package appservice

import (
	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/bittorrent"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/store"
)

// toAPITask 把库里的任务快照转成契约层的任务视图。
func toAPITask(t *store.Task) *api.Task {
	if t == nil {
		return nil
	}
	return &api.Task{
		ID:              t.ID,
		URL:             t.URL,
		SavePath:        t.SavePath,
		Protocol:        t.Protocol,
		TotalSize:       t.TotalSize,
		Downloaded:      t.Downloaded,
		SupportRange:    t.SupportRange,
		ETag:            t.ETag,
		LastModified:    t.LastModified,
		DigestAlgorithm: t.DigestAlgorithm,
		Digest:          t.Digest,
		IsAllocated:     t.IsAllocated,
		ChunkCount:      t.ChunkCount,
		MinChunkSize:    t.MinChunkSize,
		ChunkProgress:   t.ChunkProgress,
		ChunkRanges:     t.ChunkRanges,
		Status:          api.Status(t.Status),
		AuthData:        t.AuthData,
		ErrorMessage:    t.ErrorMessage,
		ErrorKind:       t.ErrorKind,
		CreatedAt:       t.CreatedAt,
		UpdatedAt:       t.UpdatedAt,
		CompletedAt:     t.CompletedAt,
		BTFile:          t.BTFile,
	}
}

// toAPISettings 把库里的设置转成契约层的设置快照。
func toAPISettings(s store.Settings) api.Settings {
	return api.Settings{
		DefaultSaveDir:     s.DefaultSaveDir,
		DefaultThreads:     s.DefaultThreads,
		MinChunkSize:       s.MinChunkSize,
		UserAgent:          s.UserAgent,
		Cookies:            s.Cookies,
		FTPPassive:         s.FTPPassive,
		Prealloc:           s.Prealloc,
		TaskSort:           api.TaskSort(s.TaskSort),
		ProxyMode:          toAPIProxyMode(s.ProxyMode),
		ProxyURL:           s.ProxyURL,
		ProxyBypass:        s.ProxyBypass,
		ProxyAutoConfigURL: s.ProxyAutoConfigURL,
		DownloadLimitKiB:   s.DownloadLimitKiB,
		LightMode:          s.LightMode,
		MaxConcurrent:      s.MaxConcurrent,
		AutoStart:          s.AutoStart,
		Language:           s.Language,
		BTPort:             s.BTPort,
		BTMaxPeers:         s.BTMaxPeers,
		BTDownloadLimitKiB: s.BTDownloadLimitKiB,
		BTUploadLimitKiB:   s.BTUploadLimitKiB,
		BTDHT:              s.BTDHT,
		BTPEX:              s.BTPEX,
		BTSeedRatio:        s.BTSeedRatio,
		BTMaxSeeding:       s.BTMaxSeeding,
		BTMaxConcurrent:    s.BTMaxConcurrent,
		BTTrackers:         s.BTTrackers,
	}
}

// toStoreSettings 是 toAPISettings 的逆变换。
func toStoreSettings(s api.Settings) store.Settings {
	return store.Settings{
		DefaultSaveDir:     s.DefaultSaveDir,
		DefaultThreads:     s.DefaultThreads,
		MinChunkSize:       s.MinChunkSize,
		UserAgent:          s.UserAgent,
		Cookies:            s.Cookies,
		FTPPassive:         s.FTPPassive,
		Prealloc:           s.Prealloc,
		TaskSort:           store.TaskSort(s.TaskSort),
		ProxyMode:          toStoreProxyMode(s.ProxyMode),
		ProxyURL:           s.ProxyURL,
		ProxyBypass:        s.ProxyBypass,
		ProxyAutoConfigURL: s.ProxyAutoConfigURL,
		DownloadLimitKiB:   s.DownloadLimitKiB,
		LightMode:          s.LightMode,
		MaxConcurrent:      s.MaxConcurrent,
		AutoStart:          s.AutoStart,
		Language:           s.Language,
		BTPort:             s.BTPort,
		BTMaxPeers:         s.BTMaxPeers,
		BTDownloadLimitKiB: s.BTDownloadLimitKiB,
		BTUploadLimitKiB:   s.BTUploadLimitKiB,
		BTDHT:              s.BTDHT,
		BTPEX:              s.BTPEX,
		BTSeedRatio:        s.BTSeedRatio,
		BTMaxSeeding:       s.BTMaxSeeding,
		BTMaxConcurrent:    s.BTMaxConcurrent,
		BTTrackers:         s.BTTrackers,
	}
}

// toAPIProxyMode 把协议层的整数代理模式转成契约层的字符串模式。
func toAPIProxyMode(m protocol.ProxyMode) api.ProxyMode {
	return api.ProxyMode(m.String())
}

// toStoreProxyMode 是 toAPIProxyMode 的逆变换。无法识别的取值回退到 System。
func toStoreProxyMode(m api.ProxyMode) protocol.ProxyMode {
	return protocol.ParseProxyMode(string(m))
}

// toAPIAuthOptions 把契约层的凭据转成协议层的凭据。
func toAPIAuthOptions(a api.AuthOptions) protocol.AuthOptions {
	return protocol.AuthOptions{
		Username:           a.Username,
		Password:           a.Password,
		UserAgent:          a.UserAgent,
		Cookies:            a.Cookies,
		Referer:            a.Referer,
		FTPPassive:         a.FTPPassive,
		ProxyMode:          toStoreProxyMode(a.ProxyMode),
		ProxyURL:           a.ProxyURL,
		ProxyBypass:        a.ProxyBypass,
		ProxyAutoConfigURL: a.ProxyAutoConfigURL,
	}
}

// toStoreAuthOptions 是 toAPIAuthOptions 的逆变换。
func toStoreAuthOptions(a protocol.AuthOptions) api.AuthOptions {
	return api.AuthOptions{
		Username:           a.Username,
		Password:           a.Password,
		UserAgent:          a.UserAgent,
		Cookies:            a.Cookies,
		Referer:            a.Referer,
		FTPPassive:         a.FTPPassive,
		ProxyMode:          toAPIProxyMode(a.ProxyMode),
		ProxyURL:           a.ProxyURL,
		ProxyBypass:        a.ProxyBypass,
		ProxyAutoConfigURL: a.ProxyAutoConfigURL,
	}
}

// toAPIEvent 把调度器事件转成契约层事件。
func toAPIEvent(ev scheduler.Event) api.Event {
	return api.Event{
		Why:      ev.Why,
		Task:     toAPITask(ev.Task),
		SpeedBPS: ev.SpeedBPS,
	}
}

// toAPIBTDetail 把 BitTorrent 详情转成契约层详情。
func toAPIBTDetail(d *bittorrent.Detail) *api.BTDetail {
	if d == nil {
		return nil
	}
	files := make([]api.FileProgress, 0, len(d.Files))
	for _, f := range d.Files {
		files = append(files, api.FileProgress{
			Path:      f.Path,
			Length:    f.Length,
			Completed: f.Completed,
		})
	}
	return &api.BTDetail{
		Name:         d.Name,
		InfoHash:     d.InfoHash,
		Total:        d.Total,
		Downloaded:   d.Downloaded,
		Uploaded:     d.Uploaded,
		SpeedBPS:     d.SpeedBPS,
		UploadBPS:    d.UploadBPS,
		Peers:        d.Peers,
		TotalPeers:   d.TotalPeers,
		Seeders:      d.Seeders,
		Leechers:     d.Leechers,
		PieceLength:  d.PieceLength,
		Pieces:       d.Pieces,
		PiecesDone:   d.PiecesDone,
		PieceBuckets: d.PieceBuckets,
		Seeding:      d.Seeding,
		Complete:     d.Complete,
		Files:        files,
		Trackers:     d.Trackers,
	}
}
