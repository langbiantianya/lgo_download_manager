// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
)

// maxRequestBody 是请求体上限（1 MiB）。本机 socket + token 鉴权之下风险
// 不高，但限制一下比没有强。
const maxRequestBody = 1 << 20

// readHeaderTimeout 是读请求头的超时上限。
const readHeaderTimeout = 10 * time.Second

// Handler 是 httpx 需要的业务侧能力。方法集与 internal/rpc 里的
// rpc.Handler 逐字一致，靠结构化类型与它对接——这里独立声明是为了避开
// httpx → rpc 的包环（rpc → httpx 已经存在）。
type Handler interface {
	// Call 执行一次调用。method 取 api.Method* 常量；in / out 的具体类型
	// 由 api.Spec(method) 决定，传输层分配好再传进来。
	Call(ctx context.Context, method string, in, out any) error

	// Events 返回进度事件流与退订函数。
	Events() (<-chan api.Event, func())

	// Controls 返回控制消息流（显示窗口 / 弹对话框 / 语言变更 / 退出）。
	Controls() <-chan api.Message
}

// Serve 在地址上启动 HTTP 服务。ctx 取消时服务关闭。
func Serve(ctx context.Context, a addr.Addr, token string, h Handler) error {
	ln, cleanup, err := addr.Listen(a)
	if err != nil {
		return err
	}
	defer cleanup()

	srv := &http.Server{
		Handler:           newMux(h, token),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	// done 用来收尾监视 goroutine：Serve 返回后它就退出，不留悬挂 goroutine。
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = srv.Close()
		case <-done:
		}
	}()

	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// newMux 组装路由与中间件。
func newMux(h Handler, token string) *http.ServeMux {
	mux := http.NewServeMux()
	// 文档端点不走 authed：它们只暴露接口形状，不暴露任务数据。
	registerDocs(mux)
	// authed 是鉴权 + 方法检查的中间件。鉴权在前：不该让没 token 的调用方
	// 知道某个端点存不存在。
	authed := func(fn func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !validToken(r.Header.Get("Authorization"), token) {
				writeError(w, http.StatusUnauthorized, "无效的 token")
				return
			}
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				writeError(w, http.StatusMethodNotAllowed, "只接受 POST")
				return
			}
			fn(w, r)
		}
	}
	for _, m := range api.AllMethods {
		if m == api.MethodStreamMessages {
			continue
		}
		mux.HandleFunc(servicePrefix+m, authed(func(w http.ResponseWriter, r *http.Request) {
			serveCall(w, r, h, m)
		}))
	}
	mux.HandleFunc(servicePrefix+api.MethodStreamMessages, authed(func(w http.ResponseWriter, r *http.Request) {
		serveStream(w, r, h)
	}))
	return mux
}

// serveCall 处理一次调用。
func serveCall(w http.ResponseWriter, r *http.Request, h Handler, method string) {
	spec := api.Spec(method)
	if spec == nil {
		writeError(w, http.StatusNotFound, "未知方法 "+method)
		return
	}
	in := spec.NewIn()
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		// 没有请求体（空 POST）不算错误：像 get_settings 这样的方法本来就没有参数。
		if err := json.NewDecoder(r.Body).Decode(in); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "解码请求: "+err.Error())
			return
		}
	}
	out := spec.NewOut()
	if err := h.Call(r.Context(), method, in, out); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		return
	}
}

// serveStream 处理消息流：把事件流与控制消息合并成 NDJSON 逐行输出。
func serveStream(w http.ResponseWriter, r *http.Request, h Handler) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "响应不支持流式输出")
		return
	}
	events, unsub := h.Events()
	defer unsub()
	controls := h.Controls()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	enc := json.NewEncoder(w)
	// 某一侧通道关闭只停掉那一侧：事件流结束不代表控制消息也没了。
	for events != nil || controls != nil {
		select {
		case <-r.Context().Done():
			// 客户端断开：立刻收手，别让 goroutine 挂着。
			return
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if err := enc.Encode(api.Message{Kind: api.MessageKindEvent, Event: &ev}); err != nil {
				return
			}
			flusher.Flush()
		case ctl, ok := <-controls:
			if !ok {
				controls = nil
				continue
			}
			if err := enc.Encode(ctl); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// validToken 校验 Authorization 头里的 Bearer token。
func validToken(header, token string) bool {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.HasPrefix(header, prefix) {
		return false
	}
	return header[len(prefix):] == token
}

// writeError 写出错误响应。错误体形状固定为 {"ok":false,"error":"..."}，
// 客户端按 error 字段解析。
func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}
