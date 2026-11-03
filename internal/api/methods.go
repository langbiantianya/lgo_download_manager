// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package api

// 方法名。沿用已删除的旧 IPC 协议的取值，两种传输共用同一张表。
const (
	MethodList           = "list"
	MethodAdd            = "add"
	MethodStart          = "start"
	MethodPause          = "pause"
	MethodDelete         = "delete"
	MethodIsPreparing    = "is_preparing"
	MethodProbe          = "probe"
	MethodGetSettings    = "get_settings"
	MethodSaveSettings   = "save_settings"
	MethodBTInfo         = "bt_info"
	MethodStreamMessages = "stream_messages"
)

// AllMethods 列出全部方法名，供契约测试与 OpenAPI 一致性测试遍历。
//
// 它包含流式方法（见 StreamMethods）——那是契约的完整清单。
// 只想遍历「能走 Transport.Call 的方法」时用 CallMethods。
var AllMethods = []string{
	MethodList, MethodAdd, MethodStart, MethodPause, MethodDelete,
	MethodIsPreparing, MethodProbe, MethodGetSettings, MethodSaveSettings,
	MethodBTInfo, MethodStreamMessages,
}

// MethodSpec 描述一个方法在契约层的请求与响应类型。
type MethodSpec struct {
	NewIn  func() any
	NewOut func() any
}

// Spec 返回方法名对应的请求/响应类型构造器；未知方法返回 nil。
//
// 返回的是**副本**：specs 是包级共享状态，直接返回 map 里的指针等于把全局
// 可变对象交到调用方手里——谁改一下 NewIn，全进程的类型校验就都变了，
// 而且这种改动极难排查。副本里装的是构造函数（本身不可变），拷贝成本可以
// 忽略。
func Spec(method string) *MethodSpec {
	spec, ok := specs[method]
	if !ok {
		return nil
	}
	cp := *spec
	return &cp
}

var specs = map[string]*MethodSpec{
	MethodList: {
		NewIn:  func() any { return &ListParams{} },
		NewOut: func() any { return &ListResult{} },
	},
	MethodAdd: {
		NewIn:  func() any { return &AddTaskInput{} },
		NewOut: func() any { return &Task{} },
	},
	MethodStart: {
		NewIn:  func() any { return &TaskRef{} },
		NewOut: func() any { return &Empty{} },
	},
	MethodPause: {
		NewIn:  func() any { return &TaskRef{} },
		NewOut: func() any { return &Empty{} },
	},
	MethodDelete: {
		NewIn:  func() any { return &TaskRef{} },
		NewOut: func() any { return &Empty{} },
	},
	MethodIsPreparing: {
		NewIn:  func() any { return &TaskRef{} },
		NewOut: func() any { return &BoolResult{} },
	},
	MethodProbe: {
		NewIn:  func() any { return &ProbeParams{} },
		NewOut: func() any { return &ProbeResult{} },
	},
	MethodGetSettings: {
		NewIn:  func() any { return &Empty{} },
		NewOut: func() any { return &Settings{} },
	},
	MethodSaveSettings: {
		NewIn:  func() any { return &Settings{} },
		NewOut: func() any { return &Empty{} },
	},
	MethodBTInfo: {
		NewIn:  func() any { return &BTInfoParams{} },
		NewOut: func() any { return &BTDetail{} },
	},
}

// StreamMethods 列出**只能走 Transport.Stream** 的流式方法。
//
// 流式方法不是普通 RPC：没有「请求类型 → 响应类型」的一对一，响应是
// 一条条消息组成的长连接（http 传输下是 NDJSON，gRPC 传输下是
// server-streaming）。
//
// 它们被登记在 AllMethods 里（那是契约的完整清单，OpenAPI 与契约测试
// 都要遍历），但**不登记在 specs 里**——给它们一个假的 NewOut（比如
// &Empty{}）等于让契约层撒谎：按 api.Spec 通用驱动 Transport.Call 的
// 调用方会以为「请求空对象、拿回一个空对象」，而实际上
//   - gRPC 传输会立刻回「未知方法」；
//   - http 传输会把请求发出去，然后卡在解码 NDJSON 上直到调用方超时。
//
// 两种传输的形态完全不同、时间特征也不同（秒失败 vs 挂满超时）。
//
// 正确用法是走 Transport.Stream（rpc.Client.Stream）。两个传输的
// Call 对流式方法都立刻返回错误，见 IsStreamMethod。
var StreamMethods = []string{
	MethodStreamMessages,
}

// CallMethods 列出可以走 Transport.Call 的方法（AllMethods 去掉流式的）。
//
// 「按契约表通用驱动」的场景（契约测试、未来的第三种传输）应当遍历这个
// 列表，而不是 AllMethods。
var CallMethods = []string{
	MethodList, MethodAdd, MethodStart, MethodPause, MethodDelete,
	MethodIsPreparing, MethodProbe, MethodGetSettings, MethodSaveSettings,
	MethodBTInfo,
}

// IsStreamMethod 报告该方法是不是流式方法。
//
// 流式方法不能走 Transport.Call——两个传输对它的处理必须一致（立刻
// 报错），否则同一个调用方在两种传输下的表现会天差地别。
func IsStreamMethod(method string) bool {
	for _, m := range StreamMethods {
		if m == method {
			return true
		}
	}
	return false
}
