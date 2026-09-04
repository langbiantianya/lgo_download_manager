// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package ipc

import (
	"encoding/json"

	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/store"
)

// ---- 调用方法名（MsgCall 的 CallData.Method） ----

const (
	// MethodList 查询任务列表。Params: ListParams；Result: []*store.Task。
	MethodList = "list"
	// MethodAdd 添加任务（不自动启动）。Params: AddParams；Result: *store.Task。
	MethodAdd = "add"
	// MethodStart / MethodPause / MethodDelete 操作单个任务。Params: TaskParams。
	MethodStart  = "start"
	MethodPause  = "pause"
	MethodDelete = "delete"
	// MethodSaveSettings 持久化设置（业务侧同时应用代理配置）。
	// Params: store.Settings。
	MethodSaveSettings = "save_settings"
	// MethodProbe 探测 URL 目标大小（新建任务对话框的大小预览）。
	// Params: ProbeParams；Result: ProbeResult。
	MethodProbe = "probe"
)

// ListParams 是 MethodList 的参数，镜像 store.ListTasks 的筛选语义。
type ListParams struct {
	Filter string `json:"filter"` // store.StatusFilter
	Sort   string `json:"sort"`   // store.TaskSort
}

// TaskParams 是 start/pause/delete 的参数。
type TaskParams struct {
	ID string `json:"id"`
}

// AddParams 是 MethodAdd 的参数，镜像 scheduler.AddTaskInput。
type AddParams struct {
	URL          string                `json:"url"`
	SavePath     string                `json:"save_path"`
	Protocol     protocol.ProtocolKind `json:"protocol"`
	Auth         protocol.AuthOptions  `json:"auth"`
	ChunkCount   int                   `json:"chunk_count"`
	MinChunkSize int64                 `json:"min_chunk_size"`
}

// ProbeParams 是 MethodProbe 的参数。
type ProbeParams struct {
	URL string `json:"url"`
}

// ProbeResult 是 MethodProbe 的结果。
type ProbeResult struct {
	TotalSize int64 `json:"total_size"`
}

// InitData 是 MsgInit 的负载：业务进程持有的权威设置快照。
type InitData struct {
	Settings store.Settings `json:"settings"`
}

// EventData 是 MsgEvent 的负载，镜像 scheduler.Event 的 JSON 表示。
// 业务端从 scheduler.Event 转换而来；UI 端转换回 scheduler.Event。
type EventData struct {
	Why      string      `json:"why"`
	Task     *store.Task `json:"task"`
	SpeedBPS float64     `json:"speed_bps"`
}

// ---- 编码辅助 ----

// Encode 把任意值编码为消息的 Data 字段（Type 留空，由调用方设置）。
func Encode(v any) (Message, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return Message{}, err
	}
	return Message{Data: raw}, nil
}

// Call 构造一条 ui.call 信封。
func Call(id, method string, params any) (Message, error) {
	p, err := json.Marshal(params)
	if err != nil {
		return Message{}, err
	}
	call, err := json.Marshal(CallData{Method: method, Params: p})
	if err != nil {
		return Message{}, err
	}
	return Message{ID: id, Type: MsgCall, Data: call}, nil
}

// Reply 构造一条 biz.result 信封。
func Reply(id string, ok bool, errMsg string, result any) (Message, error) {
	raw := json.RawMessage(nil)
	if result != nil {
		b, merr := json.Marshal(result)
		if merr != nil {
			return Message{}, merr
		}
		raw = b
	}
	data, derr := json.Marshal(ResultData{OK: ok, Error: errMsg, Data: raw})
	if derr != nil {
		return Message{}, derr
	}
	return Message{ID: id, Type: MsgResult, Data: data}, nil
}

// Decode 把消息的 Data 解码到 v。
func (m Message) Decode(v any) error {
	return json.Unmarshal(m.Data, v)
}
