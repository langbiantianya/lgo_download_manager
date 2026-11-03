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
	MethodStreamMessages: {
		NewIn:  func() any { return &Empty{} },
		NewOut: func() any { return &Empty{} },
	},
}
