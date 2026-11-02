// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package uimgr 管理 Fyne UI 子进程的生命周期，并在业务主进程里对外
// 提供业务接口服务。
//
// 它运行在业务主进程中：负责生成监听地址与 token、spawn UI 子进程
// （自我复刻）、用 rpc.Serve 起传输层服务（gRPC 或 net/http），并把
// 控制消息（显示窗口 / 弹对话框 / 语言变更 / 退出）推进业务服务的控制
// 通道。业务调用本身由 internal/appservice 承担——本包不再自己实现
// 线协议，只做「进程与通道」的装配。
package uimgr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/appservice"
	"lgo_download_manager/internal/logging"
	"lgo_download_manager/internal/rpc"
	addrpkg "lgo_download_manager/internal/rpc/addr"
	"lgo_download_manager/internal/store"
)

const (
	// validateEvery 为文件存在性轮询间隔（原 UI 窗口聚焦 ticker 的周期）。
	validateEvery = 10 * time.Second
	// closeGrace 为收到退出指令后等待子进程自行退出的时间。
	closeGrace = 2 * time.Second
	// listenTimeout 是等待传输层监听就绪的上限。
	listenTimeout = 3 * time.Second
	// spawnGrace 是拉起 UI 子进程后，观察它「有没有立刻死掉」的窗口。
	//
	// 旧实现靠等握手（上限 30s）判断 UI 到底起没起来。新架构里没有
	// 「会话就绪」信号，但启动即崩是最常见的失败形态；用一个小窗口
	// 覆盖它，别在这类失败上骗调用方「消息已送达」。
	spawnGrace = 500 * time.Millisecond
	// socketSuffixLen 是 unix socket 文件名里随机后缀的长度（hex 字符）。
	//
	// macOS 的 sockaddr_un 只有 104 字节，socket 路径放不下完整的长
	// 随机串；16 个 hex 字符（8 字节随机数）足够区分同机上的多个实例。
	socketSuffixLen = 16
)

// EnvRPCListen 是业务进程自己的监听地址环境变量。命令行 --rpc-listen
// 会写进这个变量，uimgr 在 Start() 时读取它；缺省时自动生成 unix
// socket 路径。
//
// 它不在 internal/rpc 里：那是「通道协议」的约定，而这是业务进程侧的
// 配置项（业务进程读自己的地址，再把地址透传给 UI 子进程）。
const EnvRPCListen = "LDM_RPC_LISTEN"

// Manager 拥有一个（至多一个）UI 子进程会话。
type Manager struct {
	// svc 是业务侧应用服务：契约层的 Handler 实现，也是传输层服务的
	// 业务后端。控制消息经它推给 UI。
	svc *appservice.Service

	mu       sync.Mutex
	settings store.Settings
	curLang  string // 上次推给 UI 的语言，用于 SetLanguage 判变更

	addr      string // 监听地址（unix 路径或 host:port）
	transport string // 传输协议名（grpc / http）
	token     string // 握手 token

	cmd    *exec.Cmd
	cancel context.CancelFunc
	waitCh chan struct{} // 会话结束通知
	gen    uint64        // 会话代号：清理路径据此识别自己是否仍属当前会话

	// sessionActive 表示 UI 会话已经建立（子进程已拉起且未结束）。
	// 控制消息在会话不存在时必须报错——main.go 依赖这个错误把
	// 「扩展请求没被接收」如实告诉浏览器，让它退到下一条通道。
	sessionActive bool

	onExit   func()     // UI 会话结束后回调（业务侧日志/状态）
	exitOnce *sync.Once // 每个会话一个，保证 onExit 只触发一次

	// onLanguageChanged 在 UI 端保存语言变更后被调用,供业务侧同步刷新
	// 其它本地化的 UI(托盘菜单/标题等)。仅在语言真正变化时触发。
	onLanguageChanged func(lang string)

	// startSession 拉起 UI 子进程。生产上指向 Start；测试注入一个假的，
	// 以便覆盖「无会话 → 拉起 → 推消息」这条语义而不用真的 fork。
	startSession func() error
}

// New 创建 UI 管理器。svc 是业务侧应用服务。
//
// 构造时把 appservice 的语言变更回调接到本管理器的 SetLanguage 上：UI
// 在「设置」里保存新语言时，appservice 先落库再回调这里，由它触发本地
// 化刷新并把权威语言推给 UI 子进程——与旧实现（dispatch 的 save_settings
// 分支 → SetSettings → SetLanguage）等价。
func New(svc *appservice.Service) *Manager {
	m := &Manager{
		svc:       svc,
		transport: transportFromEnv(),
		onExit: func() {
			logging.Println("uimgr: UI session ended; business keeps running")
		},
	}
	if svc != nil {
		svc.OnLanguageChanged = m.SetLanguage
	}
	m.startSession = m.Start
	return m
}

// transportFromEnv 决定用哪种传输协议服务 UI 通道：环境变量
// LDM_UI_TRANSPORT 优先（grpc / http），缺省是 grpc。
//
// 取值非法时原样返回，由 Start() 报错——静默回退会让「我明明指定了
// http 却跑成 grpc」这类问题很难排查。
func transportFromEnv() string {
	v := strings.TrimSpace(os.Getenv(rpc.EnvUITransport))
	if v == "" {
		return rpc.TransportGRPC
	}
	return v
}

// SetSettings 更新管理器持有的权威设置（业务进程内共享）。
//
// 权威快照同时进 appservice：它会把设置应用到调度器（并发上限、
// 预分配、限速、BT 配置），与旧实现一致。
func (m *Manager) SetSettings(s store.Settings) {
	m.mu.Lock()
	m.settings = s
	m.mu.Unlock()
	if m.svc != nil {
		m.svc.SetSettings(s)
	}
}

// Settings 返回当前权威设置的副本。
func (m *Manager) Settings() store.Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

// SetLanguage 把业务进程的权威 Language 推给 UI 子进程。当 Language
// 变化时(用户在「设置」里切语言)推一条 MessageKindLanguage 控制消息,
// 让 UI 立即重译;UI 子进程还没起来的场景由握手时的 get_settings 快照
// 处理,本方法对没有会话的情况照样推消息——控制通道有缓冲,UI 连上来
// 之后会收到。
//
// 变化时除了推控制消息,还会回调 onLanguageChanged——供业务侧
// 同步刷新托盘等不在 UI 进程内的本地化界面。
func (m *Manager) SetLanguage(lang string) {
	m.mu.Lock()
	changed := m.curLang != lang
	m.curLang = lang
	hook := m.onLanguageChanged
	m.mu.Unlock()
	if !changed {
		return
	}
	// 本地先刷新(ilocale.Set 在 main.go 的 hook 里做),再让 UI
	// 子进程跟着刷新——两条路径独立,先后顺序对正确性没影响,
	// 但先本地后 UI 让窗口重新可见时已经显示新语言。
	if hook != nil {
		hook(lang)
	}
	m.pushControl(api.Message{Kind: api.MessageKindLanguage, Language: lang})
}

// OnExit 注册 UI 会话结束回调。
func (m *Manager) OnExit(f func()) {
	m.mu.Lock()
	m.onExit = f
	m.mu.Unlock()
}

// OnLanguageChanged 注册语言变更回调。f 在 SetLanguage 检测到实际
// 切换时调用,lang 参数是新的 BCP-47 标签。仅供业务侧把托盘等非 Fyne
// 通道的本地化 UI 同步刷新——UI 子进程自身的翻译走控制消息。
func (m *Manager) OnLanguageChanged(f func(lang string)) {
	m.mu.Lock()
	m.onLanguageChanged = f
	m.mu.Unlock()
}

// Running 报告 UI 子进程会话是否存活（已拉起且未结束）。
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessionActive
}

// Open 确保 UI 可见：已有会话则发送 show 控制消息；否则 spawn 一个新
// UI 子进程。
func (m *Manager) Open() {
	if m.Running() {
		m.pushControl(api.Message{Kind: api.MessageKindShow})
		return
	}
	if err := m.Start(); err != nil {
		logging.Printf("uimgr: cannot open UI: %v", err)
	}
}

// Start 生成监听地址与 token、起传输层服务并拉起 UI 子进程（同一可
// 执行文件的自我复刻）。
//
// 顺序很关键：先让传输层监听就绪，再拉子进程。UI 子进程连不上业务
// 进程就直接退出（rpc.Dial 不重试），先拉子进程的话它会「出生即死」。
func (m *Manager) Start() error {
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return fmt.Errorf("uimgr: UI already running")
	}
	if m.svc == nil {
		m.mu.Unlock()
		return fmt.Errorf("uimgr: 缺少业务服务（appservice）")
	}
	addr, err := m.resolveAddr()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	token, err := randomToken()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	switch m.transport {
	case rpc.TransportGRPC, rpc.TransportHTTP:
	default:
		m.mu.Unlock()
		return fmt.Errorf("uimgr: 未知传输协议 %q（可选：%s、%s）", m.transport, rpc.TransportGRPC, rpc.TransportHTTP)
	}
	self, err := os.Executable()
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("uimgr: resolve self executable: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.gen++
	gen := m.gen
	m.addr = addr
	m.token = token
	m.cancel = cancel
	m.cmd = nil
	m.sessionActive = false
	m.exitOnce = &sync.Once{}
	m.waitCh = make(chan struct{})
	m.mu.Unlock()

	// 传输层服务在后台跑：它绑定监听、接管业务调用与消息流。
	go m.serve(ctx, gen, addr, token, m.transport)
	a, err := addrpkg.Parse(addr)
	if err != nil {
		cancel()
		m.finishSession(gen)
		return fmt.Errorf("uimgr: 解析监听地址 %q: %w", addr, err)
	}
	if err := waitListening(ctx, a, listenTimeout); err != nil {
		cancel()
		m.finishSession(gen)
		return fmt.Errorf("uimgr: 监听 %s 未就绪: %w", addr, err)
	}

	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(),
		rpc.EnvUIChild+"=1",
		rpc.EnvUIListen+"="+addr,
		rpc.EnvUIToken+"="+token,
		rpc.EnvUITransport+"="+m.transport,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		m.finishSession(gen)
		return fmt.Errorf("uimgr: spawn UI child: %w", err)
	}

	m.mu.Lock()
	m.cmd = cmd
	m.sessionActive = true
	m.mu.Unlock()

	// 子进程的唯一回收点：Wait 必须被且仅被调用一次。无论服务是否正常，
	// 进程消亡都会在这里被回收（失败路径另行 Kill），避免子进程变成无主
	// 进程、带着窗口一直存活。
	// 这里只捕获局部 cmd，不再读写 m.cmd，消除与 finishSession 的竞态。
	go func() {
		err := cmd.Wait()
		logging.Printf("uimgr: UI child exited: %v", err)
		cancel()
	}()

	return nil
}

// serve 在监听地址上启动传输层服务。它是会话 gen 的唯一结束者：
// rpc.Serve 返回（服务出错或 ctx 取消）时结束会话。
func (m *Manager) serve(ctx context.Context, gen uint64, addr, token, transport string) {
	defer m.finishSession(gen)
	// 业务侧文件存在性轮询（原 UI 焦点 ticker，随会话启停）。
	go m.validateLoop(ctx)
	err := rpc.Serve(ctx, rpc.ServerOptions{
		Transport: transport,
		Addr:      addr,
		Token:     token,
	}, m.svc)
	if err != nil {
		logging.Printf("uimgr: rpc serve: %v", err)
		// 服务起不来时子进程连不上：把它收掉，别留一个孤儿窗口。
		m.killChild(gen)
	}
}

// validateLoop 在 UI 会话存活期间定期做文件存在性校验。
func (m *Manager) validateLoop(ctx context.Context) {
	t := time.NewTicker(validateEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if m.svc != nil {
				m.svc.ValidateFileExistence()
			}
		}
	}
}

// killChild 终止会话 gen 的 UI 子进程（若它仍属于该会话）。被 Kill 的
// 进程仍由 Start 中的回收 goroutine 负责 Wait，不会留下僵尸进程。
func (m *Manager) killChild(gen uint64) {
	m.mu.Lock()
	cmd := m.cmd
	cur := m.gen == gen
	m.mu.Unlock()
	if !cur || cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

// finishSession 结束会话 gen：清空子进程/取消函数/监听地址并置
// sessionActive 为 false。只有仍是当前会话的调用会生效——重复清理、
// 已被新会话取代的清理都是空操作；onExit 因此每个会话只触发一次。
func (m *Manager) finishSession(gen uint64) {
	m.mu.Lock()
	if m.gen != gen {
		m.mu.Unlock()
		return
	}
	m.gen++ // 使本会话的后续清理失效
	waitCh := m.waitCh
	once := m.exitOnce
	onExit := m.onExit
	m.waitCh = nil
	m.sessionActive = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.cmd = nil
	m.addr = ""
	m.token = ""
	m.mu.Unlock()

	if waitCh != nil {
		close(waitCh)
	}
	if once != nil && onExit != nil {
		once.Do(onExit)
	}
}

// Close 关闭 UI 子进程：先推 close 控制消息，再安排超时强杀。
func (m *Manager) Close() {
	m.mu.Lock()
	gen := m.gen
	cmd := m.cmd
	m.mu.Unlock()

	m.pushControl(api.Message{Kind: api.MessageKindClose})

	if cmd != nil && cmd.Process != nil {
		// 兜底 Kill 必须先于等待排上定时器:子进程僵死时可能永不退出,
		// 只有定时器能保证退出一定发生,不至于吃掉 main 的 5s 退出预算。
		killTimer := time.AfterFunc(closeGrace, func() { _ = cmd.Process.Kill() })
		select {
		case <-time.After(closeGrace):
		case <-m.waitDone():
		}
		killTimer.Stop()
		_ = cmd.Process.Kill()
	}
	m.finishSession(gen)
}

// waitDone 返回会话结束通知通道。会话不在时返回一个已关闭的通道。
func (m *Manager) waitDone() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.waitCh == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return m.waitCh
}

// SendShowAddTask 让 UI 弹出「新建下载任务」对话框。
//
// 行为:
//   - UI 会话已在:立刻把参数推进控制消息通道,UI 端读到后显示主窗口并
//     打开对话框;
//   - UI 会话不在:先拉起一个 UI 子进程再推消息——与旧实现一致。托盘
//     常驻(--autostart)与冷启动两种场景下,lgom:// 转发都要能弹对话框;
//     控制通道有缓冲,消息会等 UI 连上来之后送达。
//
// 拉不起 UI 时返回错误——调用方(main.go)据此告诉浏览器「请求没被接收」,
// 浏览器退到 lgom:// URL 通道。
func (m *Manager) SendShowAddTask(p api.ShowAddTaskParams) error {
	if err := m.ensureSession(); err != nil {
		return fmt.Errorf("uimgr: cannot start UI for show_add_task: %w", err)
	}
	m.pushControl(api.Message{Kind: api.MessageKindShowAddTask, ShowAddTask: &p})
	return nil
}

// ensureSession 确保 UI 会话已建立：已在就直接返回，否则拉起一个并观察它
// 有没有立刻死掉。
//
// 走 startSession（而不是直接 m.Start）是为了让「无会话 → 拉起 → 推消息」
// 这条语义可测：测试注入一个假的拉起函数，不必真的 fork 一个子进程。
func (m *Manager) ensureSession() error {
	if m.Running() {
		return nil
	}
	if err := m.startSession(); err != nil {
		return err
	}
	// 旧实现拉起后还要等握手完成（上限 30s），握不上就报错。新架构
	// 里没有「会话就绪」这个信号，但最常见的失败形态是子进程启动即
	// 崩（Fyne 初始化失败、缺图形库、权限问题）——用一个小窗口盯住它，
	// 至少不会在这类失败上骗浏览器「已接收」。
	return m.waitSpawnAlive()
}

// waitSpawnAlive 观察刚拉起的子进程一小会儿，看它是不是立刻就退出了。
//
// 正常启动要走几秒，本窗口过后把消息留在控制通道的缓冲里即可——UI 连上
// 之后会收到。返回错误表示会话在窗口内就结束了，这条消息不可能送达。
//
// 「会话结束」除了子进程退出，也包括 rpc.Serve 提前返回与并发 Close，
// 所以文案只说会话结束，不臆断原因。
func (m *Manager) waitSpawnAlive() error {
	select {
	case <-m.waitDone():
		return fmt.Errorf("uimgr: UI 会话在启动窗口内结束，show_add_task 未送达")
	case <-time.After(spawnGrace):
		return nil
	}
}

// pushControl 往业务服务的控制通道推一条消息。
func (m *Manager) pushControl(msg api.Message) {
	if m.svc == nil {
		return
	}
	m.svc.PushControl(msg)
}

// resolveAddr 决定监听地址：环境变量 LDM_RPC_LISTEN 优先，
// 最后是自动生成的 unix socket 路径。
func (m *Manager) resolveAddr() (string, error) {
	if v := strings.TrimSpace(os.Getenv(EnvRPCListen)); v != "" {
		warnIfExposed(v)
		return v, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	sub := filepath.Join(dir, "lgo_download_manager")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		return "", fmt.Errorf("uimgr: mkdir %s: %w", sub, err)
	}
	suffix, err := randomToken()
	if err != nil {
		return "", err
	}
	return "unix:" + filepath.Join(sub, "ui-"+suffix[:socketSuffixLen]+".sock"), nil
}

// warnIfExposed 在监听地址不是回环地址时告警。TCP 模式下 token 是
// 唯一防线，用户应当知道接口对谁可见。
func warnIfExposed(addr string) {
	a, err := addrpkg.Parse(addr)
	if err != nil || a.Network != "tcp" {
		return
	}
	host, _, err := net.SplitHostPort(a.Address)
	if err != nil {
		return
	}
	if host == "" || host == "localhost" {
		return
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return
	}
	logging.Printf("uimgr: 监听地址 %s 不是回环地址，UI 接口对本机网络可见（凭 token 鉴权）", addr)
}

// waitListening 反复尝试连接监听地址，直到连上或超时。
//
// 传输层的 Serve 在自己的 goroutine 里绑定端口，而 UI 子进程连不上就
// 直接退出（rpc.Dial 不重试）。所以拉子进程之前必须确认监听已经就绪。
// 探测用的连接立刻关掉：服务端会当作一个空连接收掉，不影响业务。
func waitListening(ctx context.Context, a addrpkg.Addr, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		conn, err := addrpkg.DialContext(ctx, a)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		last = err
		if time.Now().After(deadline) {
			return last
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// randomToken 生成 32 字节随机 hex 串。
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("uimgr: random token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
