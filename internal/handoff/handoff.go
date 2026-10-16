// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package handoff 在 127.0.0.1 上暴露一个仅本机可达的 HTTP 端点，让浏览器
// 扩展把拦截到的下载直接投递给业务进程。
//
// 通道优先级（扩展侧实现）：本地端口 → lgom:// URL 协议。端口通道既不弹
// 浏览器的「打开 LGOM？」确认框，也不开转发标签页，还不依赖任何本地组件的
// 安装；代价是要求业务进程已在运行（托盘常驻），进程没起来时扩展会退到
// URL 通道（那条通道同时负责把进程冷启动起来）。
//
// 线上格式：
//
//	GET  /ping     → 200 {"app":"lgo_download_manager","api":1,"port":17821,"version":"…"}
//	POST /download → 200 {"ok":true} / 4xx|5xx {"ok":false,"error":"…"}
//
// 请求体是 JSON：{"url":"https://…","name":"…","ua":"…","headers":"…","cookies":"…"}，
// 字段与 lgom://download?… 的查询参数一一对应，但不受 URL 长度限制，也不必
// 把 Cookie/User-Agent 塞进一个可被导航、会进历史记录的 URL 里。
//
// 安全边界：
//
//   - 只监听 127.0.0.1（不监听 0.0.0.0/::），同机以外的连接进不来；
//   - POST 必须带 X-LGOM-Client 头：Web 页面要带自定义头就得先发预检
//     （OPTIONS），而预检只对扩展来源放行，于是普通网页既发不出请求、也读不到响应；
//   - 带 Origin 的请求必须是扩展来源（chrome-extension:// / moz-extension:// /
//     extension:// / safari-web-extension://），否则 403；不带 Origin 的请求
//     （curl、脚本）放行 —— 那些进程本来就与业务进程同权限。
//   - 请求在业务进程里的落地路径与 lgom:// URL 完全一致（弹「新建下载任务」
//     对话框，无 UI 的 --no-gui / --autostart 形态则直接 Add+Start）。
package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"lgo_download_manager/internal/logging"
	"lgo_download_manager/internal/version"
)

// PortCandidates 是本地端口通道的候选端口。扩展侧硬编码同一份列表
// （lgom-extension/src/lib/constants.js 的 LOCAL_PORT_CANDIDATES），
// 两边必须保持一致：扩展只探测这几个端口来发现业务进程。
//
// 从 17821 起、不落在临时端口段（Linux 默认 32768+）：监听 socket 落在
// 临时端口段里会和本机出向连接的源端口分配抢端口。
var PortCandidates = []int{17821, 17822, 17823, 17824, 17825}

const (
	// appID 是 /ping 返回的应用标识，扩展用它确认对面是 LGOM，而不是
	// 恰好占了同一端口的别的服务。
	appID = "lgo_download_manager"
	// apiVersion 是端口通道的协议版本，随线上格式的不兼容变更递增。
	apiVersion = 1
	// clientHeader 是扩展必须携带的自定义头（见包注释：逼 Web 页面预检）。
	clientHeader = "X-LGOM-Client"
	// maxBodyBytes 限制请求体大小；扩展发来的元数据只有几十 KB。
	maxBodyBytes = 1 << 20
)

// extensionOriginPrefixes 是浏览器扩展页面的来源前缀。Chromium 系（含
// Edge、Brave）是 chrome-extension://，Edge 自研包是 extension://，
// Firefox 是 moz-extension://，Safari 是 safari-web-extension://。
var extensionOriginPrefixes = []string{
	"chrome-extension://",
	"moz-extension://",
	"extension://",
	"safari-web-extension://",
}

// Request 是 /download 的请求体，字段与 lgom://download?… 的查询参数一一对应。
type Request struct {
	URL     string `json:"url"`
	Name    string `json:"name,omitempty"`
	UA      string `json:"ua,omitempty"`
	Headers string `json:"headers,omitempty"`
	Cookies string `json:"cookies,omitempty"`
}

// Server 是运行中的本地端口服务。
type Server struct {
	handler func(Request) error
	ln      net.Listener
	http    *http.Server
	port    int
}

// Info 是 <config>/handoff.json 的内容：给排障与其它本机工具用的发现文件。
type Info struct {
	App     string `json:"app"`
	API     int    `json:"api"`
	Port    int    `json:"port"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

// Start 在 PortCandidates 里挑第一个可绑定的端口启动服务。
func Start(handler func(Request) error) (*Server, error) {
	return StartOn(PortCandidates, handler)
}

// StartOn 用给定的候选端口启动服务：端口被占用（EADDRINUSE 等）时顺延到
// 下一个候选，全部占用才返回错误并列出每个端口的失败原因。
//
// handler 在 HTTP 处理 goroutine 上被同步调用，返回的错误会作为
// {"ok":false,"error":…} 回给调用方（扩展据此决定是否退到下一个通道）。
func StartOn(ports []int, handler func(Request) error) (*Server, error) {
	if handler == nil {
		return nil, errors.New("handoff: nil handler")
	}
	var failures []string
	for _, port := range ports {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			failures = append(failures, fmt.Sprintf("%d: %v", port, err))
			continue
		}
		s := &Server{handler: handler, ln: ln, port: port}
		s.http = &http.Server{
			Handler:           s.routes(),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      15 * time.Second,
			IdleTimeout:       60 * time.Second,
			// 默认 ErrorLog 会写到 stderr，落到 GUI 子系统构建里等于丢失；
			// 接到 logging 上，和其余进程日志同源。
			ErrorLog: slog.NewLogLogger(logging.L().Handler(), slog.LevelWarn),
		}
		go func() {
			if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logging.Printf("handoff: serve 127.0.0.1:%d stopped: %v", port, err)
			}
		}()
		return s, nil
	}
	return nil, fmt.Errorf("handoff: no free port in %v (%s)", ports, strings.Join(failures, "; "))
}

// Port 返回实际监听的端口（扩展探测到它之后把后续请求都发到这里）。
func (s *Server) Port() int { return s.port }

// Close 停止监听。进程退出后端口自然释放，这个方法主要给优雅退出与测试用。
//
// 除了 Shutdown 还要显式关掉 listener：Serve 在「Shutdown 已开始」时
// 会直接返回 ErrServerClosed 而不接管监听器，只靠 Shutdown 会漏掉这种
// 竞态下的 socket（端口被一直占着，重启/测试都会撞 EADDRINUSE）。
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := s.http.Shutdown(ctx)
	if cerr := s.ln.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) && err == nil {
		err = cerr
	}
	return err
}

// WriteInfo 把监听信息写到 path（业务进程的配置目录下）。写失败不致命：
// 端口通道本身不依赖这个文件，它只为排障与其它本机工具服务。
func (s *Server) WriteInfo(path string) error {
	data, err := json.Marshal(Info{
		App:     appID,
		API:     apiVersion,
		Port:    s.port,
		PID:     os.Getpid(),
		Version: version.String(),
	})
	if err != nil {
		return fmt.Errorf("handoff: encode info: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", s.handlePing)
	mux.HandleFunc("OPTIONS /download", s.handlePreflight)
	mux.HandleFunc("POST /download", s.handleDownload)
	return mux
}

// handlePing 是通道探测：扩展用它确认「这个端口上真的是 LGOM」。
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeCORS(w, r)
	if !originAllowed(r.Header.Get("Origin")) {
		writeJSON(w, http.StatusForbidden, errorBody("origin not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, Info{
		App:     appID,
		API:     apiVersion,
		Port:    s.port,
		PID:     os.Getpid(),
		Version: version.String(),
	})
}

// handlePreflight 回答 CORS 预检。只有扩展来源会拿到放行头：普通网页的
// 预检在这里被拒，浏览器因此不会把它那条带自定义头的 POST 发出来。
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	writeCORS(w, r)
	if !originAllowed(r.Header.Get("Origin")) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+clientHeader)
	w.Header().Set("Access-Control-Max-Age", "600")
	if strings.EqualFold(r.Header.Get("Access-Control-Request-Private-Network"), "true") {
		// Chrome 的 Private Network Access：扩展页面本身算「本地」来源，
		// 通常不会走到这里；显式放行让策略收紧后也不会把通道掐掉。
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDownload 接收一条交接请求，交给业务进程的落地逻辑（弹对话框 /
// 无 UI 时直接 Add+Start）。
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	writeCORS(w, r)
	origin := r.Header.Get("Origin")
	// Origin 只记在 debug 上：装主机/换浏览器时排查「来源被拒」全指望它
	// （--debug 下能看到浏览器到底送的是 chrome-extension:// 还是别的）。
	logging.L().Debug("handoff: request", "port", s.port, "origin", origin, "client", r.Header.Get(clientHeader))
	if !originAllowed(origin) {
		writeJSON(w, http.StatusForbidden, errorBody("origin not allowed: "+origin))
		return
	}
	if r.Header.Get(clientHeader) == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("missing "+clientHeader))
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("read body: "+err.Error()))
		return
	}

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("decode body: "+err.Error()))
		return
	}
	if err := validate(req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}

	if err := s.handler(req); err != nil {
		logging.Printf("handoff: 127.0.0.1:%d rejected %s: %v", s.port, req.URL, err)
		writeJSON(w, http.StatusInternalServerError, errorBody(err.Error()))
		return
	}
	logging.L().Debug("handoff accepted", "port", s.port, "url", req.URL)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// validate 检查请求体最小可用性：必须有绝对 URL。
func validate(req Request) error {
	if strings.TrimSpace(req.URL) == "" {
		return errors.New("missing url")
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return errors.New("url must be absolute")
	}
	return nil
}

// originAllowed 判断请求来源是否为浏览器扩展页面。空 Origin（curl、脚本）
// 放行：那些进程本来就与业务进程同权限，拦不住也没有意义。
func originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	for _, prefix := range extensionOriginPrefixes {
		if strings.HasPrefix(origin, prefix) {
			return true
		}
	}
	return false
}

// writeCORS 回显扩展来源，让扩展的 fetch 能读到响应体（Firefox/Chromium
// 对扩展页面是否豁免 CORS 并不一致，两边都照顾好）。
func writeCORS(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Origin")
	if origin := r.Header.Get("Origin"); origin != "" && originAllowed(origin) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"ok":false,"error":"encode response"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

func errorBody(msg string) map[string]any {
	return map[string]any{"ok": false, "error": msg}
}
