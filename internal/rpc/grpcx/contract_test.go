// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// 这是外部测试包：grpcx 自己不能 import internal/rpc（会形成
// rpc → grpcx → rpc 的包环），但外部测试包可以 import 上层包，Go 不会
// 因此成环。用它在这里就把「grpcx.Client 满足 rpc.Transport」这件事钉死，
// 不用等到注册表落地时才发现签名对不上。
package grpcx_test

import (
	"testing"

	"lgo_download_manager/internal/rpc"
	"lgo_download_manager/internal/rpc/grpcx"
)

// 编译期断言。
var (
	// grpcx.Client 满足 rpc.Transport。
	_ rpc.Transport = (*grpcx.Client)(nil)
	// rpc.Handler 的方法集与 grpcx.Handler 结构兼容：业务侧拿 rpc.Handler
	// 的实现就能直接交给 grpcx.Serve。方向是 rpc.Handler → grpcx.Handler。
	_ grpcx.Handler = (rpc.Handler)(nil)
)

// TestTransportContract 是上面断言的运行时版本：接口方法集对得上，零值
// 客户端的 Close 也不会炸。
func TestTransportContract(t *testing.T) {
	var tr rpc.Transport = &grpcx.Client{}
	if err := tr.Close(); err != nil {
		t.Fatalf("零值客户端 Close 应当返回 nil: %v", err)
	}
}
