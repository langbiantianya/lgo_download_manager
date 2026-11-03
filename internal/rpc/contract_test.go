// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// 这是 rpc 包的外部测试包（package rpc_test）。
//
// 契约测试要同时 import internal/rpc 与它的两个子包 httpx/grpcx：
// internal/rpc 自己 import 它的子包（rpc → httpx、rpc → grpcx），
// 子包不 import 父包，所以外部测试包 import internal/rpc 不会成环。
//
// 这个文件是整个分层改造的验收关口：**gRPC 与 net/http 两种传输必须对
// 同一输入给出同样的结果**。下面的用例全部参数化——同一份代码跑两种
// 传输，不是给 gRPC 与 net/http 各写一套。
package rpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc"
	"lgo_download_manager/internal/rpc/addr"
	"lgo_download_manager/internal/rpc/grpcx"
	"lgo_download_manager/internal/rpc/httpx"
)

// 编译期断言：两种传输的客户端都满足 Transport 契约，业务侧契约与两个
// 传输侧的 Handler 接口结构兼容。
var (
	_ rpc.Transport = (*grpcx.Client)(nil)
	_ rpc.Transport = (*httpx.Client)(nil)
	_ httpx.Handler = (rpc.Handler)(nil)
	_ grpcx.Handler = (rpc.Handler)(nil)
)

// contractToken 是契约测试用的握手 token。
const contractToken = "contract-token"

// 资源泄漏检测的容忍度。失败的握手应当把资源全部还回来，容忍度只用来
// 挡住「收尾是异步的」这种时序抖动。
const (
	goroutineSlack = 4
	fdSlack        = 2
)

// ---- 业务侧桩 ----

// stubHandler 是契约测试用的最小业务侧实现。
//
// 它同时是「服务端收到什么」的记录者：调用序列、调用参数、存下来的
// 设置都能被用例断言。契约测试要验证的不只是「返回值对不对」，还有
// 「请求有没有真的送到服务端、参数有没有原样送到」。
type stubHandler struct {
	mu       sync.Mutex
	settings api.Settings
	btDetail api.BTDetail
	calls    []stubCall
	errs     map[string]error

	events   chan api.Event
	controls chan api.Message

	// eventsOnce 保证事件通道只被关一次（关两次会 panic）。
	eventsOnce sync.Once
}

// stubCall 是一次调用在服务端留下的快照。
//
// 存的是拷贝过的值而不是原始指针：调用方的结构体之后可能被改写，
// 断言时看到的必须是「当时收到的」。
type stubCall struct {
	Method   string
	TaskID   string
	URL      string
	Filter   api.StatusFilter
	Sort     api.TaskSort
	Settings api.Settings
}

// Call 是一次服务端调用。
func (s *stubHandler) Call(ctx context.Context, method string, in, out any) error {
	s.mu.Lock()
	if err := s.errs[method]; err != nil {
		s.mu.Unlock()
		return err
	}
	call := stubCall{Method: method}
	switch p := in.(type) {
	case *api.TaskRef:
		call.TaskID = p.ID
	case *api.ProbeParams:
		call.URL = p.URL
	case *api.AddTaskInput:
		call.URL = p.URL
	case *api.ListParams:
		call.Filter, call.Sort = p.Filter, p.Sort
	case *api.BTInfoParams:
		call.TaskID = p.TaskID
	case *api.Settings:
		call.Settings = *p
	}
	s.calls = append(s.calls, call)
	s.mu.Unlock()

	switch method {
	case api.MethodGetSettings:
		s.mu.Lock()
		*out.(*api.Settings) = s.settings
		s.mu.Unlock()
	case api.MethodSaveSettings:
		s.mu.Lock()
		s.settings = *(in.(*api.Settings))
		s.mu.Unlock()
	case api.MethodList:
		out.(*api.ListResult).Tasks = contractTasks()
	case api.MethodAdd:
		t := out.(*api.Task)
		t.ID = "new"
		t.URL = in.(*api.AddTaskInput).URL
		t.Status = api.StatusPending
	case api.MethodIsPreparing:
		out.(*api.BoolResult).Value = true
	case api.MethodProbe:
		out.(*api.ProbeResult).TotalSize = 4242
		out.(*api.ProbeResult).Filename = "server-name.zip"
	case api.MethodBTInfo:
		s.mu.Lock()
		*out.(*api.BTDetail) = s.btDetail
		s.mu.Unlock()
	case api.MethodStart, api.MethodPause, api.MethodDelete:
		// 无负载：调用序列本身就是断言对象。
	}
	return nil
}

// Events 返回进度事件流。退订函数是空实现——桩没有订阅管理。
func (s *stubHandler) Events() (<-chan api.Event, func()) { return s.events, func() {} }

// Controls 返回控制消息流。
func (s *stubHandler) Controls() <-chan api.Message { return s.controls }

// closeEvents 关掉服务端的事件通道（模拟进度事件流结束）。
func (s *stubHandler) closeEvents() {
	s.eventsOnce.Do(func() { close(s.events) })
}

// setSettings 改服务端持有的设置（握手时返回的就是它）。
func (s *stubHandler) setSettings(fn func(*api.Settings)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.settings)
}

// setBTDetail 改服务端返回的 BT 详情。
func (s *stubHandler) setBTDetail(d api.BTDetail) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.btDetail = d
}

// setError 让某个方法在服务端直接失败。
func (s *stubHandler) setError(method string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errs == nil {
		s.errs = map[string]error{}
	}
	s.errs[method] = err
}

// callsOf 返回某个方法收到的全部调用。
func (s *stubHandler) callsOf(method string) []stubCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []stubCall
	for _, c := range s.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// methodSequence 返回服务端收到的调用序列（去掉握手那次 get_settings）。
func (s *stubHandler) methodSequence() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for i, c := range s.calls {
		if i == 0 && c.Method == api.MethodGetSettings {
			continue // 握手
		}
		out = append(out, c.Method)
	}
	return out
}

// ---- 固定数据 ----

// contractTasks 是契约测试用的固定任务列表。
//
// 字段刻意铺满：状态、协议、分块进度、时间戳都要能穿过两种线格式。
// 少填一个字段，就少一处可能悄悄走样的地方。
func contractTasks() []*api.Task {
	created := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	return []*api.Task{{
		ID:              "t1",
		URL:             "http://example.com/a",
		SavePath:        "/data/lgdm",
		Protocol:        string(api.ProtoHTTP),
		Status:          api.StatusDownloading,
		TotalSize:       1024,
		Downloaded:      512,
		SupportRange:    true,
		ETag:            "\"etag\"",
		LastModified:    "Wed, 04 Mar 2026 05:06:07 GMT",
		Digest:          "sha256:deadbeef",
		DigestAlgorithm: "sha256",
		IsAllocated:     true,
		ChunkCount:      4,
		MinChunkSize:    1024,
		ChunkProgress:   []int64{1, 2, 3, 4},
		ChunkRanges:     []int64{0, 100, 200, 300},
		AuthData:        "user:pass",
		ErrorMessage:    "",
		ErrorKind:       "",
		CreatedAt:       created,
		UpdatedAt:       created,
		CompletedAt:     time.Time{},
		BTFile:          "",
	}}
}

// contractSettings 是契约测试用的固定设置。
//
// 27 个字段全部填上非零值：空值字段在两种线格式下都是「零」，填上值
// 才能看出某个字段是不是被漏掉了。枚举字段用的是词汇表里的合法取值
// （未知取值的分歧另有用例单独钉）。
func contractSettings() api.Settings {
	return api.Settings{
		DefaultSaveDir:     "/data/lgdm",
		DefaultThreads:     8,
		MinChunkSize:       1048576,
		UserAgent:          "lgdm/1.0",
		Cookies:            "a=b",
		FTPPassive:         true,
		Prealloc:           true,
		TaskSort:           api.SortNameAsc,
		ProxyMode:          api.ProxyModeManual,
		ProxyURL:           "http://127.0.0.1:7890",
		ProxyBypass:        "localhost",
		ProxyAutoConfigURL: "http://example.com/pac",
		DownloadLimitKiB:   1024,
		LightMode:          true,
		MaxConcurrent:      5,
		AutoStart:          true,
		Language:           "zh-Hans",
		BTPort:             6881,
		BTMaxPeers:         50,
		BTDownloadLimitKiB: 2048,
		BTUploadLimitKiB:   4096,
		BTDHT:              true,
		BTPEX:              true,
		BTSeedRatio:        1.5,
		BTMaxSeeding:       3,
		BTMaxConcurrent:    2,
		BTTrackers:         "udp://tracker.example.com:6969",
	}
}

// contractBTDetail 是契约测试用的固定 BT 详情。
//
// 切片字段刻意非空：空切片在两种线格式下的表现不一样（见
// TestContractDivergenceEmptySlice），这条用例只管「非空时能不能原样
// 穿过两种线格式」。
func contractBTDetail() api.BTDetail {
	return api.BTDetail{
		Name:         "ubuntu.iso",
		InfoHash:     "0123456789abcdef",
		Total:        4096,
		Downloaded:   1024,
		Uploaded:     512,
		SpeedBPS:     1024.5,
		UploadBPS:    512.25,
		Peers:        3,
		TotalPeers:   9,
		Seeders:      2,
		Leechers:     1,
		PieceLength:  262144,
		Pieces:       16,
		PiecesDone:   4,
		PieceBuckets: []uint8{1, 2, 3},
		Seeding:      true,
		Complete:     false,
		Files: []api.FileProgress{
			{Path: "ubuntu.iso", Length: 4096, Completed: 1024},
		},
		Trackers: []string{"udp://tracker.example.com:6969"},
	}
}

// ---- 运行环境 ----

// contractEnv 是契约用例的运行环境：一个业务侧桩 + 一个连上的客户端。
type contractEnv struct {
	t         *testing.T
	transport string
	h         *stubHandler
	cli       rpc.Client
	endpoint  string
	ctx       context.Context
}

// newContractEnv 起一份服务并连上客户端，返回用例专属的环境。
//
// beforeDial 在客户端连上之前跑，用来布置「连上之前就该生效」的场景
// （比如先关掉事件通道）。它是 nil 时什么都不做。
func newContractEnv(t *testing.T, transport string, beforeDial func(*stubHandler)) *contractEnv {
	t.Helper()
	h := &stubHandler{
		settings: contractSettings(),
		btDetail: contractBTDetail(),
		events:   make(chan api.Event, 16),
		controls: make(chan api.Message, 16),
	}
	endpoint, _ := startContractServer(t, transport, contractToken, h)
	if beforeDial != nil {
		beforeDial(h)
	}
	// Dial 的 ctx 必须是会话级的：握手用的就是它，短超时会让握手超时，
	// 读循环也可能被误杀。
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cli, err := rpc.Dial(ctx, transport, endpoint, contractToken)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return &contractEnv{
		t:         t,
		transport: transport,
		h:         h,
		cli:       cli,
		endpoint:  endpoint,
		ctx:       ctx,
	}
}

// startContractServer 在临时目录里的 unix socket 上起一份服务。
//
// 契约测试不碰真实网络：全部走 unix socket。
func startContractServer(t *testing.T, transport, token string, h *stubHandler) (string, func()) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "lgdm.sock")
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- rpc.Serve(ctx, rpc.ServerOptions{
			Transport: transport,
			Addr:      "unix:" + sock,
			Token:     token,
		}, h)
	}()

	// 等监听器就绪：socket 文件出现即说明已经 listen。
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("服务没有在超时前就绪")
		}
		time.Sleep(5 * time.Millisecond)
	}

	stop := func() {
		cancel()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Error("服务没有在超时前停下")
		}
	}
	t.Cleanup(stop)
	return sock, stop
}

// dialTransport 直接建立底层传输，不经 rpc.Dial。
//
// 有些契约只属于传输层（未知方法、入参类型不对），共享客户端没有暴露
// 任意方法的入口，只能从传输层打。
func dialTransport(t *testing.T, transport, endpoint, token string) rpc.Transport {
	t.Helper()
	a, err := addr.Parse(endpoint)
	if err != nil {
		t.Fatalf("地址解析失败: %v", err)
	}
	ctx := context.Background()
	switch transport {
	case rpc.TransportGRPC:
		c, err := grpcx.Dial(ctx, a, token)
		if err != nil {
			t.Fatalf("gRPC 连接失败: %v", err)
		}
		return c
	case rpc.TransportHTTP:
		c, err := httpx.Dial(ctx, a, token)
		if err != nil {
			t.Fatalf("HTTP 连接失败: %v", err)
		}
		return c
	default:
		t.Fatalf("未知传输: %q", transport)
		return nil
	}
}

// ---- 断言助手 ----

// sameJSON 比较两个值的 JSON 编码是否一致。
//
// 用 JSON 而不是 reflect.DeepEqual：契约关心的是「线格式上等不等」，
// 而 DeepEqual 会把 nil 切片与空切片、时区的墙钟表示也算进去——那些是
// 表示差异，不是契约差异。
func sameJSON(t *testing.T, what string, want, got any) bool {
	t.Helper()
	w, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("%s：期望值编码失败: %v", what, err)
	}
	g, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("%s：实际值编码失败: %v", what, err)
	}
	if string(w) != string(g) {
		t.Errorf("%s 不一致:\n  期望 %s\n  实际 %s", what, w, g)
		return false
	}
	return true
}

// mustJSON 把值编码成 JSON，失败就让用例挂掉。
func mustJSON(t *testing.T, what string, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s 编码失败: %v", what, err)
	}
	return string(b)
}

// waitFor 轮询等待条件成立，最多等 timeout；到点还不成立就报失败。
//
// 契约测试要「等到条件成立」而不是「睡固定时长」：睡固定时长要么太短
// （偶发红），要么太长（白等）。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(msg)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// countOpenFDs 数当前进程打开的文件描述符。
//
// 只在 Linux 上有意义（/proc/self/fd）；别的平台返回 -1，调用方跳过
// 这项断言。goroutine 数是跨平台的替代信号。
func countOpenFDs() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(entries)
}

// ---- 契约用例 ----

// contractCase 是一个契约用例。
type contractCase struct {
	name string
	// beforeDial 在客户端连上之前布置服务端状态；nil 表示不布置。
	beforeDial func(*stubHandler)
	// run 跑这个用例。env 是本用例专属的服务端与客户端。
	run func(t *testing.T, env *contractEnv)
}

// contractCases 列出全部契约用例。
//
// 两种传输跑的是同一份列表——这是「同一套用例」的落法：不为 gRPC 与
// net/http 各写一套，只把传输名当参数传进去。
func contractCases() []contractCase {
	return []contractCase{
		{"握手拿到设置", nil, caseHandshakeSettings},
		{"列表", nil, caseList},
		{"添加", nil, caseAddTask},
		{"启动暂停删除", nil, caseStartPauseDelete},
		{"准备阶段", nil, caseIsPreparing},
		{"探测", nil, caseProbe},
		{"BT 详情", nil, caseBTInfo},
		{"保存设置往返", nil, caseSaveSettings},
		{"事件流", nil, caseEventStream},
		{"退订后不再收事件", nil, caseUnsubscribe},
		{"show 控制消息", nil, caseShowMessage},
		{"语言控制消息", nil, caseLanguageMessage},
		{"close 控制消息", nil, caseCloseMessage},
		{"show_add_task 注册后直接投递", nil, caseShowAddTaskDirect},
		{"show_add_task 注册前补发", nil, caseShowAddTaskReplay},
		{"事件通道关闭后控制流继续", nil, caseEventsClosedControlsContinue},
		{"Close 幂等", nil, caseCloseIdempotent},
		{"传输层 Close 幂等", nil, caseTransportCloseIdempotent},
		{"关闭之后调用报错", nil, caseCallAfterClose},
		{"错误 token 被拒", nil, caseWrongTokenRejected},
		{"未知方法报错", nil, caseUnknownMethod},
		{"入参类型不对报错", nil, caseBadInputType},
		{"业务侧错误传回客户端", nil, caseBusinessError},
		{"握手失败不泄漏传输层", nil, caseHandshakeFailureNoLeak},
	}
}

// runContract 让一种传输跑一遍全部契约用例。
func runContract(t *testing.T, transport string) {
	for _, c := range contractCases() {
		t.Run(c.name, func(t *testing.T) {
			c.run(t, newContractEnv(t, transport, c.beforeDial))
		})
	}
}

// TestContractGRPC 让 gRPC 传输跑一遍全部契约用例。
func TestContractGRPC(t *testing.T) { runContract(t, rpc.TransportGRPC) }

// TestContractHTTP 让 net/http 传输跑一遍全部契约用例。
func TestContractHTTP(t *testing.T) { runContract(t, rpc.TransportHTTP) }

// caseHandshakeSettings 断言握手拿到的是服务端的权威设置。
func caseHandshakeSettings(t *testing.T, env *contractEnv) {
	got := env.cli.Settings()
	if got.Language != "zh-Hans" {
		t.Fatalf("握手拿到的设置不对: %+v", got)
	}
	if n := len(env.h.callsOf(api.MethodGetSettings)); n != 1 {
		t.Fatalf("握手应当只发一次 get_settings，实际 %d 次", n)
	}
}

// caseList 断言列表查询的入参与返回值都原样穿过线格式。
func caseList(t *testing.T, env *contractEnv) {
	tasks, err := env.cli.List(api.FilterDownloading, api.SortNameAsc)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("列表长度不对: %+v", tasks)
	}
	sameJSON(t, "任务", contractTasks()[0], tasks[0])

	// 入参也要原样送到服务端：筛选与排序是枚举，最容易被线格式吃掉。
	calls := env.h.callsOf(api.MethodList)
	if len(calls) != 1 {
		t.Fatalf("服务端应当只收到一次 list，实际 %d 次", len(calls))
	}
	if calls[0].Filter != api.FilterDownloading || calls[0].Sort != api.SortNameAsc {
		t.Fatalf("列表参数没原样送到服务端: %+v", calls[0])
	}
}

// caseAddTask 断言添加任务的入参与返回值。
func caseAddTask(t *testing.T, env *contractEnv) {
	task, err := env.cli.AddTask(api.AddTaskInput{
		URL:          "http://example.com/b",
		SavePath:     "/data/lgdm",
		Protocol:     api.ProtoHTTP,
		ChunkCount:   4,
		MinChunkSize: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "new" || task.URL != "http://example.com/b" {
		t.Fatalf("添加结果不对: %+v", task)
	}
	calls := env.h.callsOf(api.MethodAdd)
	if len(calls) != 1 || calls[0].URL != "http://example.com/b" {
		t.Fatalf("添加参数没原样送到服务端: %+v", calls)
	}
}

// caseStartPauseDelete 断言三个控制类调用都送达服务端且顺序不变。
func caseStartPauseDelete(t *testing.T, env *contractEnv) {
	if err := env.cli.Start("t1"); err != nil {
		t.Fatal(err)
	}
	if err := env.cli.Pause("t1"); err != nil {
		t.Fatal(err)
	}
	if err := env.cli.Delete("t1"); err != nil {
		t.Fatal(err)
	}
	want := []string{api.MethodStart, api.MethodPause, api.MethodDelete}
	got := env.h.methodSequence()
	if len(got) != len(want) {
		t.Fatalf("调用序列不对: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("调用序列不对: %v", got)
		}
	}
	for _, m := range want {
		calls := env.h.callsOf(m)
		if len(calls) != 1 || calls[0].TaskID != "t1" {
			t.Fatalf("%s 没把任务 id 原样送到服务端: %+v", m, calls)
		}
	}
}

// caseIsPreparing 断言准备阶段查询。
func caseIsPreparing(t *testing.T, env *contractEnv) {
	if !env.cli.IsPreparing("t1") {
		t.Fatal("应当处于准备阶段")
	}
}

// caseProbe 断言体积与文件名探测。
func caseProbe(t *testing.T, env *contractEnv) {
	info, err := env.cli.Probe("http://example.com/a")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || info.TotalSize != 4242 || info.Filename != "server-name.zip" {
		t.Fatalf("探测结果不对: %+v", info)
	}
	calls := env.h.callsOf(api.MethodProbe)
	if len(calls) != 1 || calls[0].URL != "http://example.com/a" {
		t.Fatalf("探测参数没原样送到服务端: %+v", calls)
	}
}

// caseBTInfo 断言 BT 详情。
func caseBTInfo(t *testing.T, env *contractEnv) {
	d, err := env.cli.BTTaskDetail("t1")
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "BT 详情", contractBTDetail(), *d)
	calls := env.h.callsOf(api.MethodBTInfo)
	if len(calls) != 1 || calls[0].TaskID != "t1" {
		t.Fatalf("BT 查询参数没原样送到服务端: %+v", calls)
	}
}

// caseSaveSettings 覆盖 save_settings。
//
// 这是 DTO 最重的一个方法（27 个字段），两种线格式的序列化差异最容易
// 在这里露出来。断言方式是「保存后另起一个连接读回」——共享客户端的
// SaveSettings 会顺手更新本地缓存，从本地缓存读不算往返。
func caseSaveSettings(t *testing.T, env *contractEnv) {
	want := contractSettings()
	want.Language = "en-US"
	if err := env.cli.SaveSettings(want); err != nil {
		t.Fatal(err)
	}

	// 另起一个连接：它的握手会重新拉一次设置，那才是真正过了一遍线的值。
	other, err := rpc.Dial(env.ctx, env.transport, env.endpoint, contractToken)
	if err != nil {
		t.Fatalf("第二个连接失败: %v", err)
	}
	defer other.Close()
	sameJSON(t, "保存后读回的设置", want, other.Settings())

	// 服务端确实收到了保存请求，收到的就是这份设置。
	calls := env.h.callsOf(api.MethodSaveSettings)
	if len(calls) != 1 {
		t.Fatalf("服务端应当只收到一次 save_settings，实际 %d 次", len(calls))
	}
	sameJSON(t, "服务端收到的设置", want, calls[0].Settings)
}

// caseEventStream 断言进度事件能送到订阅者。
func caseEventStream(t *testing.T, env *contractEnv) {
	ch, unsub := env.cli.Subscribe()
	defer unsub()
	task := contractTasks()[0]
	env.h.events <- api.Event{Why: "progress", SpeedBPS: 10, Task: task}
	select {
	case ev := <-ch:
		if ev.Why != "progress" || ev.SpeedBPS != 10 {
			t.Fatalf("事件不对: %+v", ev)
		}
		if ev.Task == nil {
			t.Fatal("事件里的任务丢了")
		}
		sameJSON(t, "事件里的任务", *task, *ev.Task)
	case <-time.After(10 * time.Second):
		t.Fatal("没有收到事件")
	}
}

// caseUnsubscribe 断言退订之后不再收到事件，且事件通道被关闭。
func caseUnsubscribe(t *testing.T, env *contractEnv) {
	ch, unsub := env.cli.Subscribe()
	env.h.events <- api.Event{Why: "first"}
	select {
	case ev := <-ch:
		if ev.Why != "first" {
			t.Fatalf("事件不对: %+v", ev)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("没有收到事件")
	}
	unsub()
	// 退订之后：通道关闭；再来的事件不能 panic（向已关闭的通道发送会 panic）。
	env.h.events <- api.Event{Why: "second"}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("退订之后不应再收到事件")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("退订之后事件通道没有关闭")
	}
}

// caseShowMessage 断言 show 控制消息能触发回调。
func caseShowMessage(t *testing.T, env *contractEnv) {
	shown := make(chan struct{}, 1)
	env.cli.SetOnShow(func() { shown <- struct{}{} })
	env.h.controls <- api.Message{Kind: api.MessageKindShow}
	select {
	case <-shown:
	case <-time.After(10 * time.Second):
		t.Fatal("show 回调没有触发")
	}
}

// caseLanguageMessage 断言语言变更控制消息能触发回调。
func caseLanguageMessage(t *testing.T, env *contractEnv) {
	changed := make(chan struct{}, 1)
	env.cli.SetOnLanguage(func() { changed <- struct{}{} })
	env.h.controls <- api.Message{Kind: api.MessageKindLanguage, Language: "en-US"}
	select {
	case <-changed:
	case <-time.After(10 * time.Second):
		t.Fatal("语言回调没有触发")
	}
}

// caseCloseMessage 断言业务侧要求退出时 onClose 回调被触发。
func caseCloseMessage(t *testing.T, env *contractEnv) {
	closed := make(chan struct{}, 1)
	env.cli.SetOnClose(func() { closed <- struct{}{} })
	env.h.controls <- api.Message{Kind: api.MessageKindClose}
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("close 回调没有触发")
	}
}

// caseShowAddTaskDirect 断言钩子已注册时，show_add_task 直接投递。
func caseShowAddTaskDirect(t *testing.T, env *contractEnv) {
	got := make(chan api.ShowAddTaskParams, 1)
	env.cli.SetOnShowAddTask(func(p api.ShowAddTaskParams) { got <- p })
	env.h.controls <- api.Message{
		Kind:        api.MessageKindShowAddTask,
		ShowAddTask: &api.ShowAddTaskParams{URL: "http://example.com/c", Name: "n", UA: "ua", Cookies: "c"},
	}
	select {
	case p := <-got:
		if p.URL != "http://example.com/c" || p.Name != "n" || p.UA != "ua" || p.Cookies != "c" {
			t.Fatalf("show_add_task 参数不对: %+v", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("show_add_task 回调没有触发")
	}
}

// caseShowAddTaskReplay 断言「注册前到达的 show_add_task 会被补发」。
//
// 这是契约里最容易悄悄坏掉的一条：业务进程的 lgom:// 转发可能在 UI
// 子进程还没注册回调时就发过来了，注册之后必须补发，否则主窗口被拉起
// 却看不到对话框（旧 IPC 客户端修过这个坑，契约层必须保住它）。
//
// 时序怎么保证「消息确实先到了」：show_add_task 与 show 走的是同一条
// 控制通道，通道内保序。show 的回调触发，就说明排在它前面的
// show_add_task 已经被读循环处理过了——这时它只能进了补发队列。
// 于是可以「等到 show 触发」再注册 show_add_task 钩子，全程不靠 sleep。
func caseShowAddTaskReplay(t *testing.T, env *contractEnv) {
	// show 钩子与 show_add_task 钩子互相独立，先注册 show 当顺序哨兵。
	shown := make(chan struct{}, 1)
	env.cli.SetOnShow(func() { shown <- struct{}{} })

	env.h.controls <- api.Message{
		Kind:        api.MessageKindShowAddTask,
		ShowAddTask: &api.ShowAddTaskParams{URL: "http://example.com/d"},
	}
	env.h.controls <- api.Message{Kind: api.MessageKindShow}

	// 等哨兵：show_add_task 此时必然已经被处理过。
	select {
	case <-shown:
	case <-time.After(10 * time.Second):
		t.Fatal("show 哨兵没有触发")
	}

	// 现在才注册 show_add_task 钩子：注册前到达的请求必须被补发。
	got := make(chan api.ShowAddTaskParams, 1)
	env.cli.SetOnShowAddTask(func(p api.ShowAddTaskParams) { got <- p })
	select {
	case p := <-got:
		if p.URL != "http://example.com/d" {
			t.Fatalf("补发的参数不对: %+v", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("注册之前到达的 show_add_task 没有被补发")
	}
}

// caseEventsClosedControlsContinue 断言「事件通道关闭后控制消息流继续」。
//
// 两侧的消息流在服务端是分开 select 的：事件流结束不代表控制消息也没
// 了。两种传输在这条上曾经不一样（httpx 先改，grpcx 后跟），所以契约
// 测试要把它钉住。
//
// 时序不能想当然：「先关事件通道、再发控制消息」是**不确定**的——服务端
// 的 select 同时盯着两个通道，已关闭的事件通道与已缓冲的控制消息都是
// 就绪的，Go 会随机挑一个。挑中事件通道就说明实现是坏的（流被掐掉），
// 挑中控制消息就说明实现是好的（消息先送出去了）——同一个用例有时红
// 有时绿，抓不住任何东西。
//
// 所以这里先发一条控制消息当「消息流是通的」探针：它到达客户端，就证明
// 服务端已经进了 select 循环；这时再关事件通道，服务端必然重新醒一次，
// 行为就是确定的——好的实现把事件侧摘掉继续收控制消息，坏的实现整体
// 收手，第二条控制消息永远送不到。
func caseEventsClosedControlsContinue(t *testing.T, env *contractEnv) {
	shown := make(chan struct{}, 4)
	env.cli.SetOnShow(func() { shown <- struct{}{} })

	// 探针：消息流本身是通的。
	env.h.controls <- api.Message{Kind: api.MessageKindShow}
	waitShow(t, shown, "第一条控制消息没有到达：消息流本身就不通")

	// 现在关掉服务端的事件通道。
	env.h.closeEvents()

	// 事件流结束了，控制消息流必须还在。
	env.h.controls <- api.Message{Kind: api.MessageKindShow}
	waitShow(t, shown, "事件通道关闭后，控制消息流断了")
}

// waitShow 等 show 回调被触发一次。
func waitShow(t *testing.T, shown <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-shown:
	case <-time.After(10 * time.Second):
		t.Fatal(msg)
	}
}

// caseCloseIdempotent 断言客户端 Close 幂等。
//
// rpc.Transport 的契约明确写着 Close 必须幂等。客户端的 Close 会把
// Close 转给传输层，所以这里同时守住两层。
func caseCloseIdempotent(t *testing.T, env *contractEnv) {
	if err := env.cli.Close(); err != nil {
		t.Fatalf("第一次 Close 失败: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := env.cli.Close(); err != nil {
			t.Fatalf("第 %d 次重复 Close 应当返回 nil，实际: %v", i+2, err)
		}
	}
}

// caseTransportCloseIdempotent 直接对底层传输调 Close。
//
// 客户端层幂等不代表传输层幂等：grpc 的 ClientConn.Close() 第二次会
// 返回 ErrClientConnClosing（非 nil 错误），所以传输层必须自己用
// sync.Once 包住。这一条直接打在传输层上。
func caseTransportCloseIdempotent(t *testing.T, env *contractEnv) {
	tr := dialTransport(t, env.transport, env.endpoint, contractToken)
	defer tr.Close()
	for i := 0; i < 3; i++ {
		if err := tr.Close(); err != nil {
			t.Fatalf("传输层第 %d 次 Close 应当返回 nil，实际: %v", i+1, err)
		}
	}
}

// caseCallAfterClose 断言关闭之后调用返回错误而不是 panic。
func caseCallAfterClose(t *testing.T, env *contractEnv) {
	if err := env.cli.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	if _, err := env.cli.List(api.FilterAll, api.SortCreatedDesc); err == nil {
		t.Fatal("关闭之后调用应当报错")
	}
}

// caseWrongTokenRejected 断言错误 token 连接失败。
func caseWrongTokenRejected(t *testing.T, env *contractEnv) {
	cli, err := rpc.Dial(env.ctx, env.transport, env.endpoint, "wrong-token")
	if err == nil {
		_ = cli.Close()
		t.Fatal("错误 token 应当连接失败")
	}
}

// caseUnknownMethod 断言未知方法返回错误（不是 panic，也不是成功）。
func caseUnknownMethod(t *testing.T, env *contractEnv) {
	tr := dialTransport(t, env.transport, env.endpoint, contractToken)
	defer tr.Close()
	err := tr.Call(env.ctx, "no_such_method", &api.Empty{}, &api.Empty{})
	if err == nil {
		t.Fatal("未知方法应当返回错误")
	}
}

// caseBadInputType 断言入参类型不对时返回错误。
//
// 这里用一个「JSON 上也不兼容」的入参：gRPC 侧是类型断言失败，HTTP 侧
// 是 JSON 解码失败（400）。两种传输都必须是错误。
//
// 注意：「Go 类型不对但 JSON 恰好兼容」的情形由
// TestContractInputTypeMismatchRejected 单独覆盖——那种情况两种传输都
// 必须在发请求之前就报错。
func caseBadInputType(t *testing.T, env *contractEnv) {
	tr := dialTransport(t, env.transport, env.endpoint, contractToken)
	defer tr.Close()
	err := tr.Call(env.ctx, api.MethodList, struct {
		Filter int `json:"filter"`
	}{Filter: 1}, &api.ListResult{})
	if err == nil {
		t.Fatal("入参类型不对应当返回错误")
	}
}

// caseBusinessError 断言业务侧返回的错误能传回客户端，错误原因不丢。
func caseBusinessError(t *testing.T, env *contractEnv) {
	env.h.setError(api.MethodProbe, errors.New("业务侧炸了"))
	_, err := env.cli.Probe("http://example.com/a")
	if err == nil {
		t.Fatal("业务侧报错应当传回客户端")
	}
	if !strings.Contains(err.Error(), "业务侧炸了") {
		t.Fatalf("错误原因丢了: %v", err)
	}
}

// caseHandshakeFailureNoLeak 断言握手失败时不泄漏传输层。
//
// 握手失败时调用方拿不到客户端，也就没办法调 Close——这次 Dial 建立的
// 连接必须由 rpc 层自己关掉。实测每次失败的握手会漏 2 个 fd（net/http
// 的空闲连接池、gRPC 的 ClientConn），gRPC 还会漏一串 goroutine。
// 这里连错 20 次，把泄漏放大到测得出来的程度。
func caseHandshakeFailureNoLeak(t *testing.T, env *contractEnv) {
	const rounds = 20
	baseGoroutines := runtime.NumGoroutine()
	baseFDs := countOpenFDs()
	for i := 0; i < rounds; i++ {
		if _, err := rpc.Dial(env.ctx, env.transport, env.endpoint, "wrong-token"); err == nil {
			t.Fatalf("第 %d 次：错误 token 应当连接失败", i+1)
		}
	}
	// 收尾是异步的（gRPC 在别的 goroutine 里关连接），等它回落。
	waitFor(t, 10*time.Second, func() bool {
		if runtime.NumGoroutine() > baseGoroutines+goroutineSlack {
			return false
		}
		if baseFDs >= 0 && countOpenFDs() > baseFDs+fdSlack {
			return false
		}
		return true
	}, fmt.Sprintf("失败的握手没有把传输层关掉：goroutine %d→%d，fd %d→%d",
		baseGoroutines, runtime.NumGoroutine(), baseFDs, countOpenFDs()))
}

// ---- 跨传输一致性 ----

// TestContractTransportsAgree 让两种传输对同一组调用给出同样的结果。
//
// 前面那些用例只能抓「某传输在某用例上失败」；这一条抓的是「两边都
// 过了，但结果不一样」——那才是契约真的裂开了。逐项比较两种传输的
// JSON 编码结果。
func TestContractTransportsAgree(t *testing.T) {
	grpcRes := collectContractResults(t, rpc.TransportGRPC)
	httpRes := collectContractResults(t, rpc.TransportHTTP)
	for _, key := range []string{
		"settings", "list", "add", "is_preparing", "probe", "bt_info", "saved_settings",
	} {
		if grpcRes[key] != httpRes[key] {
			t.Errorf("%s 两种传输结果不一致:\n  grpc: %s\n  http: %s", key, grpcRes[key], httpRes[key])
		}
	}
}

// collectContractResults 对一种传输跑一组固定调用，把结果编码成 JSON。
func collectContractResults(t *testing.T, transport string) map[string]string {
	t.Helper()
	env := newContractEnv(t, transport, nil)
	out := map[string]string{}

	out["settings"] = mustJSON(t, "握手设置", env.cli.Settings())

	tasks, err := env.cli.List(api.FilterAll, api.SortCreatedDesc)
	if err != nil {
		t.Fatal(err)
	}
	out["list"] = mustJSON(t, "列表", tasks)

	task, err := env.cli.AddTask(api.AddTaskInput{
		URL:        "http://example.com/x",
		Protocol:   api.ProtoHTTP,
		ChunkCount: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	out["add"] = mustJSON(t, "添加", task)

	out["is_preparing"] = fmt.Sprintf("%v", env.cli.IsPreparing("t1"))

	info, err := env.cli.Probe("http://example.com/x")
	if err != nil {
		t.Fatal(err)
	}
	out["probe"] = mustJSON(t, "探测", info)

	detail, err := env.cli.BTTaskDetail("t1")
	if err != nil {
		t.Fatal(err)
	}
	out["bt_info"] = mustJSON(t, "BT 详情", detail)

	// 保存设置 → 另起一个连接读回，验证真正过了线的值。
	want := contractSettings()
	want.Language = "en-US"
	if err := env.cli.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	other, err := rpc.Dial(env.ctx, transport, env.endpoint, contractToken)
	if err != nil {
		t.Fatalf("第二个连接失败: %v", err)
	}
	defer other.Close()
	out["saved_settings"] = mustJSON(t, "保存后读回的设置", other.Settings())

	return out
}

// ---- 已知分歧与类型一致性 ----
//
// 契约测试的价值一半在「一致」，另一半在「不一致」。下面把已知的分歧
// 钉死：它们现在是绿灯的，但它们是真实的行为差异——改动任何一侧都会
// 让用例变红，提醒人来统一。

// TestContractDivergenceUnknownEnum 记录分歧①：词汇表里没有的枚举取值，
// 两种传输的处理不一样。
//
// gRPC 侧走 protobuf 枚举，未知取值只能归一成 UNSPECIFIED（再映射回
// 空串）；HTTP 侧走 JSON 字符串，未知取值原样保留。
//
// 这是 JSON 与 protobuf 的线格式固有差异，不是缺陷，不追求统一：protobuf
// 的枚举在 wire 上是一个整数，词表外的取值没有对应的整数，只能落回
// UNSPECIFIED；JSON 走的是字符串，词表外的字符串照原样传过去。要统一，
// 就得让一边丢信息或让另一边凭空猜一个枚举值——两头都不对。
func TestContractDivergenceUnknownEnum(t *testing.T) {
	got := map[string]string{}
	for _, transport := range []string{rpc.TransportGRPC, rpc.TransportHTTP} {
		// 设置必须在客户端连上之前改：握手那一刻拉到的就是这份设置。
		env := newContractEnv(t, transport, func(h *stubHandler) {
			h.setSettings(func(s *api.Settings) { s.TaskSort = api.TaskSort("bogus") })
		})
		got[transport] = string(env.cli.Settings().TaskSort)
	}
	if got[rpc.TransportGRPC] != "" {
		t.Errorf("gRPC 侧当前行为是「未知枚举归一成空串」，实际 %q", got[rpc.TransportGRPC])
	}
	if got[rpc.TransportHTTP] != "bogus" {
		t.Errorf("HTTP 侧当前行为是「未知枚举原样保留」，实际 %q", got[rpc.TransportHTTP])
	}
}

// TestContractDivergenceEmptySlice 记录分歧②：空切片在两种线格式下形态
// 不同。
//
// gRPC 侧 proto3 的 repeated 字段空时不上线，反序列化后是非 nil 的空
// 切片；HTTP 侧 JSON 编出来是 null，解回来是 nil。调用方用
// reflect.DeepEqual 比对就会看到差异。
//
// 这是 JSON 与 protobuf 的线格式固有差异，不是缺陷，不追求统一：proto3
// 的 repeated 字段没有「null」这个状态，只有「空」；JSON 有 null 与 []
// 两种形态。契约层的比较一律走 JSON 归一（见 sameJSON），别拿
// reflect.DeepEqual 当契约——它会把这些表示差异也算成契约差异。
func TestContractDivergenceEmptySlice(t *testing.T) {
	got := map[string]string{}
	for _, transport := range []string{rpc.TransportGRPC, rpc.TransportHTTP} {
		// 切片字段刻意留空，才能看出「空」在两种线格式下长什么样。
		env := newContractEnv(t, transport, func(h *stubHandler) {
			h.setBTDetail(api.BTDetail{Name: "x"})
		})
		detail, err := env.cli.BTTaskDetail("t1")
		if err != nil {
			t.Fatal(err)
		}
		// 用 JSON 归一比，别用 reflect.DeepEqual：分歧本来就发生在
		// 线格式上——protobuf 侧的空 repeated 字段上线是「[]」，JSON
		// 侧的 null 上线是「null」。
		//
		// 比的是 Files 而不是 PieceBuckets：PieceBuckets 是 []uint8，
		// JSON 会把它编成 base64 字符串（空的是 ""，nil 的是 null），
		// 看不出「空数组」与「null」的区别；Files 是普通结构体切片，
		// 编出来就是数组。
		got[transport] = mustJSON(t, "空切片", detail.Files)
	}
	if got[rpc.TransportGRPC] != "[]" {
		t.Errorf("gRPC 侧当前行为是「空切片非 nil」，线上是 %s", got[rpc.TransportGRPC])
	}
	if got[rpc.TransportHTTP] != "null" {
		t.Errorf("HTTP 侧当前行为是「空切片是 nil」，线上是 %s", got[rpc.TransportHTTP])
	}
}

// TestContractStreamMethodNotCallable 断言流式方法不能走通用 Call，且两种
// 传输的失败形态一致（都立刻报错，不发请求）。
//
// 这条守的是终审发现的一条跨任务累积出来的分歧：
//
//   - 契约层曾把 stream_messages 登记成普通方法（api.Spec 给了它一个假的
//     &api.Empty{} 占位），于是「按 api.Spec 通用驱动 Transport.Call」的
//     调用方会写出跑不通的代码；
//   - gRPC 侧立刻回「未知方法」（秒失败）；
//   - http 侧没有特判，把请求真发出去，然后卡在解码 NDJSON 上，**挂满整个
//     RPC 超时**（默认 30s）。
//
// 两种传输的形态与时间特征完全不可互换：一个通用调用方在 gRPC 上秒失败、
// 在 http 上挂满超时。修法是契约层正确表达「流式方法不是普通 RPC」
// （api.StreamMethods / api.IsStreamMethod），两个传输的 Call 都立刻报错。
//
// 这条用例断言：两种传输都**立刻**失败、都不发请求、错误信息都带方法名
// 与「用 Stream()」的指引。
func TestContractStreamMethodNotCallable(t *testing.T) {
	for _, transport := range []string{rpc.TransportGRPC, rpc.TransportHTTP} {
		t.Run(transport, func(t *testing.T) {
			env := newContractEnv(t, transport, nil)
			tr := dialTransport(t, env.transport, env.endpoint, contractToken)
			defer func() { _ = tr.Close() }()

			// 给一个短 deadline：没有守卫时 http 侧会把请求发出去后卡在
			// 解码 NDJSON 上，靠这个 deadline 才能**快速**失败——否则
			// 整个测试会挂死，而不是变红。
			ctx, cancel := context.WithTimeout(env.ctx, 2*time.Second)
			defer cancel()

			start := time.Now()
			err := tr.Call(ctx, api.MethodStreamMessages, &api.Empty{}, &api.Empty{})
			elapsed := time.Since(start)

			if err == nil {
				t.Fatalf("%s：流式方法走 Call 应当报错", transport)
			}
			if !strings.Contains(err.Error(), api.MethodStreamMessages) {
				t.Errorf("%s：错误信息应当带上方法名: %v", transport, err)
			}
			if !strings.Contains(err.Error(), "Stream()") {
				t.Errorf("%s：错误信息应当指引改用 Stream(): %v", transport, err)
			}
			// 关键：**立刻**失败。http 侧曾经把请求发出去后卡在解码 NDJSON
			// 上，耗满整个 RPC 超时——那与「秒失败」对调用方是完全不同的
			// 语义，通用调用方在两种传输下的表现不可互换。
			//
			// 判据用 deadline 的一半：有守卫时是毫秒级，远小于 1s；没有
			// 守卫时至少耗满 2s 的 deadline。
			if elapsed > time.Second {
				t.Errorf("%s：应当立刻失败，实际耗时 %v（错误是 %v）", transport, elapsed, err)
			}
		})
	}
}

// TestContractInputTypeMismatchRejected 断言入参的 Go 类型不对时，两种
// 传输都报错。
//
// 这条用例原来钉的是分歧③：gRPC 侧做 Go 类型断言，类型不对当场报错；
// HTTP 侧把入参编码成 JSON 再由服务端解码，类型「碰巧兼容」时（例如拿
// TaskRef 去调 list）会被静默接受，服务端收到的是零值——调用方看不出
// 自己传错了类型。
//
// 现在 httpx 在序列化之前按 api.Spec 校验入参类型，两边的行为一致了，
// 这条用例从「钉住分歧」改成「守住一致性」：任何一边改成静默接受，用例
// 都会变红。
func TestContractInputTypeMismatchRejected(t *testing.T) {
	for _, transport := range []string{rpc.TransportGRPC, rpc.TransportHTTP} {
		env := newContractEnv(t, transport, nil)
		tr := dialTransport(t, env.transport, env.endpoint, contractToken)
		// 拿 TaskRef 去调 list：JSON 上恰好兼容（TaskRef 只有 id 字段，
		// ListParams 的 filter/sort 解不出来就是零值），所以它正是「静默
		// 接受」最容易发生的那一类。
		err := tr.Call(env.ctx, api.MethodList, &api.TaskRef{ID: "t1"}, &api.ListResult{})
		_ = tr.Close()
		if err == nil {
			t.Errorf("%s：入参类型不对应当报错", transport)
			continue
		}
		if !strings.Contains(err.Error(), api.MethodList) {
			t.Errorf("%s：错误信息应当带上方法名: %v", transport, err)
		}
		// 类型不对就不该把请求发出去：服务端不该收到这次调用。
		if calls := env.h.callsOf(api.MethodList); len(calls) != 0 {
			t.Errorf("%s：入参类型不对时不该发出请求，服务端却收到 %d 次", transport, len(calls))
		}
	}
}

// TestContractOutputTypeMismatchRejected 断言出参的 Go 类型不对时，两种
// 传输都报错。
//
// 这条用例原来钉的是分歧④：gRPC 侧做 Go 类型断言并报错；HTTP 侧把服务端
// 的响应体解码进出参类型，解不出来的字段被丢掉，调用方拿到一个零值对象，
// 没有错误。
//
// 现在 httpx 在发请求之前就按 api.Spec 校验出参类型，两边的行为一致了，
// 这条用例从「钉住分歧」改成「守住一致性」。
func TestContractOutputTypeMismatchRejected(t *testing.T) {
	for _, transport := range []string{rpc.TransportGRPC, rpc.TransportHTTP} {
		env := newContractEnv(t, transport, nil)
		tr := dialTransport(t, env.transport, env.endpoint, contractToken)
		// 拿 Task 去接 list 的响应：JSON 上解得动（Task 的字段都是
		// 字符串/数字），但那不是 list 的响应类型。
		err := tr.Call(env.ctx, api.MethodList, &api.ListParams{}, &api.Task{})
		_ = tr.Close()
		if err == nil {
			t.Errorf("%s：出参类型不对应当报错", transport)
			continue
		}
		if !strings.Contains(err.Error(), api.MethodList) {
			t.Errorf("%s：错误信息应当带上方法名: %v", transport, err)
		}
		// 出参类型不对同样不该发出请求。
		if calls := env.h.callsOf(api.MethodList); len(calls) != 0 {
			t.Errorf("%s：出参类型不对时不该发出请求，服务端却收到 %d 次", transport, len(calls))
		}
	}
}
