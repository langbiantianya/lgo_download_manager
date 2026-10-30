# 下载引擎与 UI 分层 + gRPC/net-http 传输层 设计文档

- 日期：2026-10-30
- 状态：待评审
- 相关提交：基线 `a512d84`

## 1. 背景与目标

### 1.1 现状

lgdm 是单一可执行文件的两种角色：业务主进程（SQLite store + scheduler + 下载引擎 + 托盘）与
Fyne UI 子进程。二者通过 `internal/ipc` 的自定义线协议通信（Unix socket + 长度前缀 JSON 帧）。

当前链路：

```
[Fyne UI 子进程] --ipc(自定义 JSON 帧)--> [uimgr.Manager.dispatch] --> [scheduler]
                                                                        |
                                                                   [engine] --> [protocol 驱动] --> [store]
```

现存三个问题：

1. **边界是漏的**：`internal/ui` 直接 import `internal/store`（`store.Task`、`store.Settings`）、
   `internal/scheduler`（`scheduler.Event`、`scheduler.BittorrentDefaultDir`）、
   `internal/protocol`（`protocol.AuthOptions`、`protocol.ProxyMode`、`protocol.DetectKind`）、
   `internal/bittorrent`（`bittorrent.Detail`）。UI 与业务侧共享同一批结构体，
   任何一侧改字段都会波及另一侧。
2. **线协议写死**：`uimgr.Manager.dispatch` 是一个 `switch(method string)`，
   `ui.ipcClient` 每个操作一个方法。协议只有一种实现（自定义 JSON 帧），
   无法在不重写两侧的前提下换成标准 RPC。
3. **引擎单文件过大**：`internal/engine/engine.go` 856 行，把计划、worker 池、进度节流、
   重试退避、限速混在一个文件里。

### 1.2 目标

1. 把下载引擎逻辑与 UI 逻辑彻底隔离开：UI 只依赖一个**与传输无关的契约**（接口 + DTO），
   不再 import store / scheduler / engine / protocol / bittorrent。
2. 在引擎与 UI 之间插入一层**传输抽象**，并分别实现 **gRPC** 与 **net/http** 两种协议。
   两种协议实现同一份契约，行为等价、可互换。
3. 监听地址**可配置**：默认 Unix socket，也可以指定 `host:port`。
4. HTTP 传输带一份 **OpenAPI（Swagger）规范**与可浏览的文档页。
5. 迁移完成后删除 `internal/ipc` 自定义 JSON 帧协议。
6. 顺带按职责拆分 `internal/engine`，并用测试锁住依赖方向。

### 1.3 非目标

- 不改动浏览器扩展的本地端口通道（`internal/handoff`）与单实例 URL 转发
  （`internal/urllauncher`）。它们与本次分层无关，保持现状。
- 不在「设置」里加传输协议开关。传输由环境变量 `LDM_UI_TRANSPORT` 决定，默认 `grpc`。
- 不把接口默认暴露到网络上。默认地址仍是本机 Unix socket；指定 `host:port` 时
  默认只绑回环地址，且 token 校验是强制的。
- 不改动 `store` 的表结构与 `internal/protocol` 各驱动的下载逻辑。

## 2. 目标架构

```
┌──────────────────────────── 业务主进程 ────────────────────────────┐
│                                                                    │
│  internal/uimgr        internal/appservice      internal/scheduler │
│  (会话与子进程生命周期)  (rpc.Handler 实现)        (任务调度)        │
│        │                      │                        │           │
│        │ 装配                  │ DTO 转换                ▼           │
│        ▼                      ▼              internal/engine        │
│  ┌──────────────┐      ┌────────────┐        (engine/plan/chunk/   │
│  │ internal/rpc │      │ internal/  │         progress/retry)     │
│  │ Transport/   │      │   api      │               │            │
│  │ Handler/     │      │ (DTO 词汇) │               ▼            │
│  │ Client       │      └────────────┘      internal/protocol      │
│  └──────┬───────┘                            (HTTP/FTP/WebDAV/BT)  │
│         │        ┌──────────────┐                  │             │
│         ├───────>│ internal/rpc │                  ▼             │
│         │        │   httpx     │            internal/store        │
│         │        │ (net/http   │                                 │
│         │        │   + JSON    │                                 │
│         │        │  + Swagger) │                                 │
│         │        └──────────────┘                                 │
│         │        ┌──────────────┐                                 │
│         └───────>│ internal/rpc │                                 │
│                  │   grpcx     │  ──> internal/rpc/grpcx/pb      │
│                  │ (gRPC+proto) │      (buf 生成的 pb 代码)        │
│                  └──────────────┘                                 │
└────────────────────────────┬───────────────────────────────────────┘
                             │ Unix socket + token
┌────────────────────────────┴───────────────────────────────────────┐
│  UI 子进程：internal/ui  ──> internal/rpc.Client                   │
│               依赖：internal/api、internal/urlkind、internal/rpc    │
└────────────────────────────────────────────────────────────────────┘
```

依赖方向（由测试锁定）：

- `internal/urlkind`：**叶子包**，不 import 任何内部包。
- `internal/api`：只 import `internal/urlkind`（为了 `ProtocolKind` 类型别名），
  不 import 其它内部包。
- `internal/rpc/grpcx`、`internal/rpc/httpx`：只 import `internal/api`（+ 各自的 pb）。
  **不 import `internal/rpc`**——避免 `rpc → grpcx → rpc` 的环；接口靠结构化类型满足，
  由 `internal/rpc` 里的 `var _ Transport = (*grpcx.Server)(nil)` 断言。
- `internal/rpc`：import `internal/api` + 两个传输实现，负责注册表与共享客户端。
- `internal/appservice`：import `internal/rpc`、`internal/api`、`internal/scheduler`、
  `internal/store`、`internal/bittorrent`。
- `internal/ui`：import `internal/rpc`、`internal/api`、`internal/urlkind`。
  **不得** import `internal/store`、`internal/scheduler`、`internal/engine`、
  `internal/protocol`、`internal/bittorrent`、`internal/appservice`。

### 2.1 包职责表

| 包 | 状态 | 职责 |
|---|---|---|
| `internal/api` | 新增 | 契约词汇表 + DTO。`Task`/`Settings`/`Event`/`BTDetail`/`FileProgress`/`Message`/`AddTaskInput`/`ListParams`/`ShowAddTaskParams`；`Status`/`StatusFilter`/`TaskSort`/`ProxyMode`/`ProtocolKind` 均为 string 类型；方法名常量 |
| `internal/urlkind` | 新增 | 纯 URL 助手：`ProtocolKind` 与常量、`DetectKind`、`IsBitTorrent`、`MagnetName`、`MagnetInfoHash`、`TorrentNameFromPath`、`SafeDirName`、`BittorrentDefaultDir`。从 `internal/protocol` 与 `internal/scheduler` 抽出 |
| `internal/rpc` | 新增 | 接口层：`Transport`（传输能力面）、`Handler`（业务侧契约）、`Client`（UI 侧契约 + 共享实现）、`Serve`/`Dial` 注册表、`Listen`（地址解析与监听）、契约测试套件 |
| `internal/rpc/httpx` | 新增 | HTTP 传输实现：标准库 `net/http` + REST 风格路径 + JSON 正文 + NDJSON 消息流 + OpenAPI 规范与 Swagger UI |
| `internal/rpc/grpcx` | 新增 | gRPC 传输实现：类型化 RPC + protobuf |
| `internal/rpc/grpcx/pb` | 新增（生成） | `buf` 生成的 pb 代码，入库 |
| `internal/appservice` | 新增 | 业务侧 `rpc.Handler` 实现。承接今天 `uimgr` 里的 `dispatch`/`probe`/`forwardEvents`，并做 DTO 转换 |
| `internal/uimgr` | 改造 | 只剩会话与子进程生命周期：建 socket、生成 token、拉起子进程、装配 `rpc.Serve`、推控制消息 |
| `internal/ui` | 改造 | 依赖从 store/scheduler/protocol/bittorrent 换成 api/urlkind/rpc |
| `internal/engine` | 改造 | 856 行单文件按职责拆为 5 个文件（行为不变） |
| `internal/ipc` | **删除** | 自定义 JSON 帧协议整体移除 |
| `internal/protocol` | 微调 | `ProtocolKind`/`DetectKind`/`IsBitTorrent`/`MagnetName` 等改为引用 `urlkind`（保留同名薄封装，避免大面积改调用点） |
| `internal/scheduler` | 微调 | `BittorrentDefaultDir` 移到 `urlkind`，原处保留别名 |

## 3. 契约层：`internal/api`

### 3.1 词汇表

`Status` / `StatusFilter` / `TaskSort` 沿用 `internal/store` 的**现有取值**，
因此跨层转换是一次类型转换而不是查表：

```go
type Status       string   // "Pending" / "Downloading" / "Paused" / "Completed" / "Failed" / "FileLost"
type StatusFilter string   // "all" / "pending" / "downloading" / "paused" / "completed" / "failed" / "filelost"
type TaskSort     string   // "created_desc" / "created_asc" / "name_asc" / "name_desc"
type ProtocolKind = urlkind.ProtocolKind   // 类型别名，"HTTP"/"HTTPS"/"FTP"/"WEBDAV"/"BT"
```

注意 `store.Status` 的取值是**首字母大写**（"Pending" 而非 "pending"），
转换时必须原样搬运——转换测试里逐个断言两侧取值集合相同。

`ProxyMode` 是唯一的例外：`internal/protocol.ProxyMode` 是 **iota 整数枚举**
（`System=0` / `Disabled=1` / `Manual=2`），不是字符串。契约层改成字符串类型，
取值沿用它已有的稳定字符串表示：

```go
type ProxyMode string   // "system" / "disabled" / "manual"
```

转换走 `protocol.ProxyMode.String()` 与 `protocol.ParseProxyMode()`，
两者对三种模式的互转是无损的（往返测试锁定）。

`ProtocolKind` 用**类型别名**而非新类型，是为了让 `api.AddTaskInput.Protocol` 与
`protocol.ProtocolKind` 是同一个类型，`appservice` 转换时无需任何映射。

`Status` / `TaskSort` / `StatusFilter` / `ProxyMode` 是新定义的类型，
`appservice` 用 `api.Status(tk.Status)`、`api.ProxyMode(p.ProxyMode.String())`
这样的显式转换完成，转换测试锁定取值一致。

### 3.2 DTO

`api.Task` 字段与 `store.Task` 一一对应（ID、URL、SavePath、Protocol、TotalSize、
Downloaded、SupportRange、ETag、LastModified、DigestAlgorithm、Digest、IsAllocated、
ChunkCount、MinChunkSize、ChunkProgress、ChunkRanges、Status、AuthData、ErrorMessage、
ErrorKind、CreatedAt、UpdatedAt、CompletedAt、BTFile），全部带 json tag。

`api.Settings` 字段与 `store.Settings` 一一对应（含 `TaskSort`、`ProxyMode` 改为本包类型）。

`api.BTDetail` / `api.FileProgress` 字段与 `internal/bittorrent` 的 `Detail` / `FileProgress` 对应。

`api.Event` 对应 `scheduler.Event`：`Why string` + `Task *Task` + `SpeedBPS float64`。

### 3.3 消息流载荷

事件与控制消息合并为一条流，用 `kind` 区分：

```go
type MessageKind string

const (
	MessageKindEvent       MessageKind = "event"         // 进度/状态事件
	MessageKindShow        MessageKind = "show"          // 业务托盘「显示窗口」
	MessageKindShowAddTask MessageKind = "show_add_task" // lgom:// 转发后弹对话框
	MessageKindLanguage    MessageKind = "language"      // 权威语言变更
	MessageKindClose       MessageKind = "close"         // 业务侧要求 UI 退出
)

type Message struct {
	Kind        MessageKind        `json:"kind"`
	Event       *Event             `json:"event,omitempty"`
	ShowAddTask *ShowAddTaskParams `json:"show_add_task,omitempty"`
	Language    string             `json:"language,omitempty"`
}
```

`ShowAddTaskParams` 沿用今天 `ipc.ShowAddTaskParams` 的字段（URL/Name/UA/Cookies）。

### 3.4 方法名

沿用今天 `ipc.Method*` 的取值，两种协议共用同一张表：

| `api` 方法常量 | 取值 | HTTP 路径 | gRPC 方法 |
|---|---|---|---|
| `MethodList` | `list` | `POST /lgdm.v1.DownloadService/list` | `List` |
| `MethodAdd` | `add` | `POST /lgdm.v1.DownloadService/add` | `AddTask` |
| `MethodStart` | `start` | `POST /lgdm.v1.DownloadService/start` | `StartTask` |
| `MethodPause` | `pause` | `POST /lgdm.v1.DownloadService/pause` | `PauseTask` |
| `MethodDelete` | `delete` | `POST /lgdm.v1.DownloadService/delete` | `DeleteTask` |
| `MethodIsPreparing` | `is_preparing` | `POST /lgdm.v1.DownloadService/is_preparing` | `IsPreparing` |
| `MethodProbe` | `probe` | `POST /lgdm.v1.DownloadService/probe` | `Probe` |
| `MethodGetSettings` | `get_settings` | `POST /lgdm.v1.DownloadService/get_settings` | `GetSettings` |
| `MethodSaveSettings` | `save_settings` | `POST /lgdm.v1.DownloadService/save_settings` | `SaveSettings` |
| `MethodBTInfo` | `bt_info` | `POST /lgdm.v1.DownloadService/bt_info` | `BTTaskDetail` |
| `MethodStreamMessages` | `stream_messages` | `POST /lgdm.v1.DownloadService/stream_messages` | `StreamMessages` |

`get_settings` 是今天没有的新方法：今天的 `MsgInit` 是握手后推一帧初始设置，
改成 RPC 后由客户端显式拉取，语义更直白，也不必把「握手」建模成一个特殊帧。

## 4. 接口层：`internal/rpc`

### 4.1 Transport（传输能力面）

```go
// Transport 是两种协议实现各自要满足的最小能力面。
type Transport interface {
	// Call 执行一次调用。method 取 api.Method* 常量；req/resp 是
	// api 层的 DTO 指针，由调用方分配，传输实现按自己的线格式编解码。
	Call(ctx context.Context, method string, req, resp any) error

	// Stream 打开服务端推送的消息流（进度事件 + 控制消息合并流）。
	// 返回的 channel 在流结束或 ctx 取消后关闭。
	Stream(ctx context.Context) (<-chan api.Message, error)
}
```

方法名与 DTO 类型只出现在 `internal/rpc` 与 `internal/api` 两处；传输实现只负责
「按自己的线格式编解码 + 发包」。新增第三种协议 = 实现这两个方法。

### 4.2 Handler（业务侧契约）

```go
type Handler interface {
	// Call 执行一次调用。method 取 api.Method* 常量；in / out 的具体类型
	// 由 api.Spec(method) 决定，传输层分配好再传进来。
	//
	// 之所以用「方法名 + any」而不是十几个具名方法：业务侧只保留**一处**
	// 方法分派（今天 uimgr.dispatch 的位置），两种传输实现不必各自抄一份
	// 「方法 → 业务调用」的映射表。请求/响应类型仍由 api.Spec(method) 在
	// 契约层钉死，业务侧在分派时对入参做类型断言，类型不符立刻报错。
	Call(ctx context.Context, method string, in, out any) error

	// Events 返回进度事件流与退订函数。
	Events() (<-chan api.Event, func())
	// Controls 返回控制消息流（show / show_add_task / language / close）。
	Controls() <-chan api.Message
}
```

由 `internal/appservice` 实现。

### 4.3 Client（UI 侧契约 + 共享实现）

```go
type Client interface {
	List(filter api.StatusFilter, sort api.TaskSort) ([]*api.Task, error)
	AddTask(in api.AddTaskInput) (*api.Task, error)
	Start(taskID string) error
	Pause(taskID string) error
	Delete(taskID string) error
	IsPreparing(taskID string) bool
	Probe(url string) (int64, error)
	BTTaskDetail(taskID string) (*api.BTDetail, error)
	Settings() api.Settings
	SaveSettings(s api.Settings) error

	Subscribe() (<-chan api.Event, func())
	SetOnShow(f func())
	SetOnShowAddTask(f func(api.ShowAddTaskParams))
	SetOnLanguage(f func())
	SetOnClose(f func())
	Close() error
}
```

方法签名与今天的 `ui.Service` 一一对应（类型换成 `api.*`），因此 UI 侧的改动
基本只是类型改名。`rpc.Client` 是基于 `Transport` 的**唯一**实现，
两种协议共用，因此调用超时、错误包装、事件扇出等语义只有一处。

`Client` 的行为约定（与今天的 `ipcClient` 对齐）：

- 每次调用 30s 超时（今天 `ipcClient.request` 的取值）。
- `Subscribe()` 返回带缓冲的事件通道；退订时关闭通道，与广播互斥（今天 `subsMu` 的语义）。
- 事件投递非阻塞：订阅者消费不过来时丢弃事件，不阻塞读循环。
- `Settings()` 返回连接建立时缓存的权威快照；`SaveSettings` 成功后更新缓存。

### 4.4 注册表

```go
func Serve(ctx context.Context, transport, addr string, h Handler) error
func Dial(ctx context.Context, transport, endpoint, token string) (Client, error)
```

`transport` 取值 `"grpc"`（默认）与 `"http"`。未知取值返回明确错误。

`addr` / `endpoint` 是**统一的监听地址**描述，两种传输通用，格式见 §6.1。

`Dial` 建立连接后先发一次 `get_settings`：这一次往返同时完成两件事——
校验 token 是否正确（不对就立刻失败，UI 不会拿着一个连不上的连接去建窗口），
以及拿到初始设置快照（取代今天 `MsgInit` 那一帧）。此后 `Settings()` 直接读缓存。

## 5. 传输实现一：gRPC

### 5.1 proto 定义

`proto/lgdm/v1/lgdm.proto`：

```proto
syntax = "proto3";
package lgdm.v1;
option go_package = "lgo_download_manager/internal/rpc/grpcx/pb;pb";

service DownloadService {
  rpc List(ListRequest) returns (ListResponse);
  rpc AddTask(AddTaskRequest) returns (AddTaskResponse);
  rpc StartTask(TaskRef) returns (Empty);
  rpc PauseTask(TaskRef) returns (Empty);
  rpc DeleteTask(TaskRef) returns (Empty);
  rpc IsPreparing(TaskRef) returns (BoolResponse);
  rpc Probe(ProbeRequest) returns (ProbeResponse);
  rpc GetSettings(Empty) returns (GetSettingsResponse);
  rpc SaveSettings(SaveSettingsRequest) returns (Empty);
  rpc BTTaskDetail(TaskRef) returns (BTTaskDetailResponse);
  rpc StreamMessages(Empty) returns (stream Message);
}
```

**类型化 RPC**，每个操作一个方法——外部语言客户端可以直接调用，不需要知道
「方法名 + 载荷」这套内部约定。

消息定义与 `internal/api` 一一对应。状态/排序/过滤/代理/协议这几个字段在 proto 里用
**枚举**表达（`Status`、`TaskSort`、`StatusFilter`、`ProxyMode`、`ProtocolKind`），
每个枚举带一个零值 `*_UNSPECIFIED`。

### 5.2 api ↔ pb 映射

`internal/rpc/grpcx/convert.go` 负责双向映射：

- 字符串枚举 ↔ proto 枚举：查表映射。**未知取值一律映射到 `*_UNSPECIFIED`**，
  反向映射回空字符串。映射表由往返测试锁定（对每个已定义常量断言 `api→pb→api` 恒等）。
- 其余字段按同名一一映射。

往返测试：`api` 全字段填充的 `Task` / `Settings` / `BTDetail` / `Event` 经 `api→pb→api`
后必须 `reflect.DeepEqual` 相等，防止新增字段漏映射。

### 5.3 传输与鉴权

- 服务端：`rpc.Listen(addr)`（§6.1 的统一地址解析）→ `grpc.NewServer()` → `Serve(ln)`。
- 客户端：`grpc.NewClient("passthrough:///lgdm", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(dialer(addr)))`。
  `dialer` 按地址类型选 `net.Dial("unix", path)` 或 `net.Dial("tcp", hostport)`。
- 鉴权：**每次调用校验**。token 放 metadata `authorization: Bearer <token>`，
  由 unary interceptor 与 stream interceptor 统一校验，不通过返回 `codes.Unauthenticated`。
- 消息流：`StreamMessages` 是 server-streaming，服务端把 `Events()` 与 `Controls()`
  两条 channel 合并后逐条 `Send`。
- 绑 TCP 时的安全约束与 HTTP 传输一致（见 §6.1）：默认只绑回环地址，
  绑 `0.0.0.0` 时打警告，token 是唯一防线。

## 6. 传输实现二：net/http

这一路只用标准库 `net/http`，**不接 `golang.org/x/net/http2` 的 h2c 手工接线**，
也不为 HTTP 传输引入任何第三方依赖。明文 Unix socket 上的 HTTP/1.1 已经够用：
传输层跑在本机 socket 上，不存在中间设备降级、代理干扰这些 HTTP/2 才要解决的问题。

（Swagger UI 页面所需的静态资产是唯一的例外，见 §6.5。）

### 6.1 监听地址

监听地址是**可配置的**，两种传输通用。格式：

```
unix:<path>          Unix socket，路径为 <path>
tcp:<host:port>      TCP，监听 <host:port>（host 省略即 ":port"，端口 0 由系统分配）
<裸值>               自动判别：含路径分隔符（/ 或 \）→ unix；否则按 host:port → tcp
```

判别规则之所以要区分 `/` 与 `\`，是因为 Windows 上的 socket 路径形如
`C:\Users\...\ui.sock`，里面含 `:`，按「含冒号即 host:port」会误判。

**默认地址**由业务进程生成：Unix socket，路径在用户缓存目录下
（沿用今天 `uimgr.makeSocketPath` 的做法：`<UserCacheDir>/lgo_download_manager/ui-<随机后缀>.sock`），
权限 0600。

**用户可指定**：

- 业务进程命令行参数 `--rpc-listen <addr>`，例如
  `--rpc-listen tcp:127.0.0.1:17830`。
- 业务进程读自己的环境变量 `LDM_RPC_LISTEN`（缺省用生成的 socket 路径）。

业务进程把最终地址原样透传给 UI 子进程（环境变量 `LDM_UI_LISTEN`），
子进程按同一套规则解析。

**安全约束**（TCP 模式尤其重要）：

- TCP 默认只绑 `127.0.0.1`；用户显式写 `0.0.0.0`/`::` 时启动日志必须打一条警告，
  说明该接口已对本机网络可见。
- TCP 模式下 **token 是唯一防线**，每次调用都校验（见 §6.3），不得提供「关闭鉴权」开关。
- Unix socket 保持今天的 0600 权限与「启动前清理残留 socket 文件」逻辑。

地址解析与监听由 `internal/rpc/listen.go` 提供，两种传输共用：

```go
// Listen 按地址描述创建监听器。unix 分支负责清理残留 socket 文件并把
// 权限收紧到 0600；tcp 分支直接监听。cleanup 在关闭时移除 socket 文件。
func Listen(addr string) (net.Listener, func(), error)
```

### 6.2 端点映射

REST 风格路径 + JSON 正文，路径即方法名（对照表见 §3.4）：

```
POST /lgdm.v1.DownloadService/list          → 200 {"tasks":[...]}
POST /lgdm.v1.DownloadService/add           → 200 {"task":{...}}
POST /lgdm.v1.DownloadService/start         → 200 {}
POST /lgdm.v1.DownloadService/is_preparing  → 200 {"preparing":true}
POST /lgdm.v1.DownloadService/probe          → 200 {"total_size":123}
POST /lgdm.v1.DownloadService/get_settings  → 200 {"settings":{...}}
POST /lgdm.v1.DownloadService/stream_messages → 200 application/x-ndjson（流式）

GET  /openapi.yaml                          → 200 application/yaml（OpenAPI 规范原文）
GET  /openapi.json                          → 200 application/json（同一份规范的 JSON 形式）
GET  /docs                                  → 200 text/html（Swagger UI）
```

错误：非 2xx 状态码 + `{"ok":false,"error":"..."}`。状态码语义：
400 参数非法 / 404 任务不存在 / 409 状态冲突 / 500 其它。客户端把非 2xx 统一转成 error。

`/openapi.*` 与 `/docs` **不参与 token 校验**——它们只暴露接口形状，不暴露任务数据。
其余端点全部要求 Bearer token。

### 6.3 传输与鉴权

- 服务端：`net.Listen` → `http.Server{Handler: mux, ReadHeaderTimeout: 10s}`
  → `srv.Serve(ln)`。`http.ServeMux` 按 §3.4 的路径分派，`mux.HandleFunc` 注册各方法。
- 客户端：`http.Client{Transport: &http.Transport{DialContext: dialer(addr)}}`。
  `dialer` 按地址类型选 `net.Dial("unix", path)` 或 `net.Dial("tcp", hostport)`。
- 鉴权：**每次调用校验**。`Authorization: Bearer <token>` 头，由中间件统一校验，
  不通过返回 401。
- 连接复用：`http.Transport` 默认 keep-alive，同一条连接上连续复用；
  消息流是独立的长连接请求，与普通调用不共用。

### 6.4 消息流

`POST /lgdm.v1.DownloadService/stream_messages`，响应 `Content-Type: application/x-ndjson`，
每行一个 `api.Message` 的 JSON。服务端逐行 `Flush`；客户端用 `json.Decoder`
在 `resp.Body` 上循环解码，读到 EOF 即流结束。

### 6.5 Swagger / OpenAPI 文档

HTTP 传输的接口形状必须有一份**可执行的规范**，而不是散落在代码里的注释。

- **规范文件**：`internal/rpc/httpx/openapi.yaml`，OpenAPI 3.0.3 格式，
  用 `go:embed` 打进二进制。它是 HTTP 这一侧接口形状的唯一真相源。
- **内容**：
  - `info`（标题、版本、说明）
  - `components.securitySchemes`：`bearerAuth`（HTTP bearer）
  - 每个方法一个 path：请求体 schema、响应 schema、错误码说明
  - `components.schemas`：`Task` / `Settings` / `Event` / `BTDetail` / `Message` 等，
    字段与 `internal/api` 的 DTO 一一对应
  - `/stream_messages` 标注 `application/x-ndjson` 的流式响应
- **对外暴露**：
  - `GET /openapi.yaml` → 规范原文
  - `GET /openapi.json` → 同一份规范的 JSON 形式（启动时用 `go.yaml.in/yaml/v3`
    转一次并缓存；该 YAML 库已是直接依赖）
  - `GET /docs` → Swagger UI 页面
- **Swagger UI 资产**：用 `github.com/swaggo/http-swagger` + `github.com/swaggo/files`
  把 swagger-ui 静态资源嵌进二进制，**离线可用**——本机工具的文档页不该依赖公网 CDN。
  （若要零依赖，可退化为从 CDN 加载 UI，但离线时页面不可用，不作为默认方案。）
- **与代码同步**：新增/修改端点或 DTO 字段时必须同步改 `openapi.yaml`，
  由 §11.1 的规范一致性测试守住。

## 7. 业务侧改造

### 7.1 `internal/appservice`

新增包，承接今天 `internal/uimgr/manager.go` 里的三段逻辑：

- `dispatch(method, raw)` → 改为 `Handler` 的各个方法 + `api` DTO。
- `probe(raw)` / `probeBittorrent(raw)` → `Handler.Probe`。
- `forwardEvents(conn, ctx)` → `Handler.Events()`：订阅 `scheduler.Subscribe()`，
  把 `scheduler.Event` 转成 `api.Event`。

新增 `convert.go`：`store.Task → api.Task`、`store.Settings → api.Settings`、
`bittorrent.Detail → api.BTDetail`、`scheduler.Event → api.Event`。

新增控制消息通道：`Handler.Controls()` 返回业务侧持有的 `chan api.Message`，
`appservice` 暴露 `PushControl(msg api.Message)` 供 `uimgr` 推
show / show_add_task / language / close。通道有缓冲，推送非阻塞（满了丢弃）。

### 7.2 `internal/uimgr`

保留：地址生成、token 生成、拉起子进程、会话结束清理、关闭时的强杀兜底。

改动：
- 用 `rpc.Serve(ctx, transport, addr, handler)` 取代 `acceptLoop` 里的手写帧循环。
  `ipc.Listen`（unix socket 清理 + 0600）迁到 `internal/rpc/listen.go`，与 TCP 分支共用。
- `SetLanguage` 改成往控制通道推 `MessageKindLanguage` 消息，而不是发 `MsgLanguage` 帧。
- `Open()`（托盘「显示窗口」）在已有会话时推 `MessageKindShow`。
- `SendShowAddTask` 推 `MessageKindShowAddTask`。
- `Close()` 推 `MessageKindClose`，保留强杀兜底。
- 握手改为 `rpc.Dial` 内部的一次 `get_settings` 往返（§4.4），不再有独立的 hello 帧。

### 7.3 环境变量与参数

| 名称 | 含义 | 默认 |
|---|---|---|
| `LDM_UI_CHILD` | `"1"` 表示当前进程以 UI 子进程角色运行（沿用） | 无 |
| `LDM_UI_LISTEN` | UI 子进程要连的地址（§6.1 格式；unix 路径或 host:port） | 无 |
| `LDM_UI_TOKEN` | 握手 token（沿用） | 无 |
| `LDM_UI_TRANSPORT` | 传输协议：`grpc` \| `http`（**新增**） | `grpc` |
| `LDM_RPC_LISTEN` | 业务进程自己的监听地址（**新增**，命令行 `--rpc-listen` 优先） | 生成的 unix socket 路径 |

`LDM_UI_SOCKET` 被 `LDM_UI_LISTEN` 取代：地址不再局限于 socket 路径。

业务进程读**自己的** `LDM_RPC_LISTEN` / `--rpc-listen`（缺省用生成的 socket 路径），
拉起 UI 子进程时把地址与传输一起透传。这样整条链路可以直接切换：

```
LDM_RPC_LISTEN=tcp:127.0.0.1:17830 LDM_UI_TRANSPORT=http ./lgdm
```

UI 子进程不需要自己猜协议或地址。

不在「设置」里暴露传输协议与地址（用户已确认）。

## 8. UI 侧改造

### 8.1 依赖替换

| 现状 | 改为 |
|---|---|
| `store.Task` | `api.Task` |
| `store.Settings` | `api.Settings` |
| `store.Status` / `store.StatusFilter` / `store.TaskSort` 及其常量 | `api.*` 同名常量 |
| `scheduler.Event` | `api.Event` |
| `bittorrent.Detail` / `bittorrent.FileProgress` | `api.BTDetail` / `api.FileProgress` |
| `protocol.AuthOptions` | `api.AuthOptions` |
| `protocol.ProxyMode` 及常量 | `api.ProxyMode` 及常量 |
| `protocol.DetectKind` / `protocol.IsBitTorrent` | `urlkind.DetectKind` / `urlkind.IsBitTorrent` |
| `scheduler.BittorrentDefaultDir` | `urlkind.BittorrentDefaultDir` |
| `ipc.ShowAddTaskParams` | `api.ShowAddTaskParams` |
| `ui.Service` | `rpc.Client` |

`internal/ui/service.go` 删除（`Service` 接口与 `AddTaskInput` 由 `rpc.Client` 与
`api.AddTaskInput` 取代）。

### 8.2 客户端实现

`internal/ui/client.go` 由「自己实现 IPC 帧读写」改为「包一层 `rpc.Client`」：

- 事件订阅：底层 `rpc.Client` 已经把消息流扇出成 `api.Event` 通道，
  `ui` 侧直接透传给 `task_list` / `task_row`。
- 控制消息：`rpc.Client` 的 `SetOnShow` / `SetOnShowAddTask` / `SetOnLanguage` /
  `SetOnClose` 回调由 UI 侧注册，回调实现里 `fyne.Do` 切回事件线程（与今天一致）。

### 8.3 UI 测试

今天的 `localServiceAdapter`（`internal/ui/add_task_dialog_e2e_test.go`）直接包
scheduler/store 实现 `ui.Service`。改造后有两个选择：

- 保留一个本地 `rpc.Client` 实现，直接调 `appservice`（快，不走 socket）；
- 或者用真实 socket 跑端到端。

**两种传输各跑一遍端到端**（gRPC / net-http），这是本次重构的核心收益之一：
UI 的行为不再只在一个自定义协议上被验证过。

## 9. 引擎拆分

`internal/engine/engine.go`（856 行）按职责拆分，行为不变：

| 文件 | 内容 |
|---|---|
| `engine.go` | `Job` 结构、`Options`、`NewJob`、`Run`、worker 池编排、流式回退入口 |
| `plan.go` | 分片规划：`plan`/`planLocked`/`replan`/`Chunks`/`resumedBytes`/`chunkConcurrencyLimit` |
| `chunk.go` | 单分片下载 `downloadChunk`、流式下载 `downloadFallbackWithRetry`、限速 `throttle` |
| `progress.go` | 进度上报与速度计算（`emit` 及其节流状态） |
| `retry.go` | 重试与退避策略：`retryDelay`、`waitRetry`、`jitter`、重试预算常量 |

拆分后 `internal/engine` 仍然不 import 任何 UI 相关包——这条性质由边界测试锁定。

## 10. 迁移步骤

每一步结束时仓库都能编译、测试能通过。

1. **抽 `internal/urlkind`**：把 `ProtocolKind`、协议判定、BT 名称助手、
   `BittorrentDefaultDir` 移入；`internal/protocol` 与 `internal/scheduler` 保留
   同名薄封装（别名），调用点不必大面积改。
2. **建 `internal/api`**：DTO + 词汇表 + 方法名常量。纯新增。
3. **建 `internal/appservice`**：搬 `dispatch`/`probe`/`forwardEvents`，写 `convert.go`，
   补单元测试（转换往返 + 各方法行为）。
4. **建 `internal/rpc`**：`Transport`/`Handler`/`Client`/注册表/共享客户端，
   以及 `listen.go`（地址解析：unix / host:port）。
5. **实现 `internal/rpc/httpx`**：`net/http` 服务端 + 客户端 + 契约测试；
   写 `internal/rpc/httpx/openapi.yaml` 并挂上 `/openapi.yaml`、`/openapi.json`、`/docs`。
6. **写 proto 并实现 `internal/rpc/grpcx`**：`proto/lgdm/v1/lgdm.proto`、
   `buf.yaml`/`buf.gen.yaml`、`scripts/gen_proto.sh`，生成物入库；
   `convert.go` + 服务端 + 客户端 + 契约测试。
7. **接线**：`internal/uimgr` 改用 `rpc.Serve`；`internal/ui` 改用 `rpc.Client`；
   `main.go` 加 `--rpc-listen` 并把地址与传输透传给子进程
   （`LDM_UI_LISTEN` / `LDM_UI_TRANSPORT`）。
8. **删除**：`internal/ipc` 整个包、`internal/ui/service.go`。
9. **拆引擎**：`internal/engine` 按 §9 拆文件。
10. **边界与规范测试**：断言依赖方向（§2）+ OpenAPI 规范与代码一致 +
    proto 生成物与 `.proto` 同步。

## 11. 测试策略与验收标准

### 11.1 新增测试

| 测试 | 位置 | 断言 |
|---|---|---|
| 契约测试套件 | `internal/rpc/contract_test.go` | 同一组用例对 gRPC 与 net/http 各跑一遍：列表/添加/启动/暂停/删除/设置读写/BT 详情/事件流/控制消息/token 错误/断线行为 |
| api↔pb 往返 | `internal/rpc/grpcx/convert_test.go` | 全字段 `api→pb→api` 后 `reflect.DeepEqual` 相等；枚举映射表恒等 |
| api JSON 往返 | `internal/api/types_test.go` | DTO 的 JSON 编解码不丢字段 |
| 业务侧转换 | `internal/appservice/convert_test.go` | `store.Task→api.Task` 等映射正确，`api.Status` 取值与 `store.Status` 一致 |
| 依赖边界 | `internal/ui/boundary_test.go`、`internal/rpc/boundary_test.go` | 用 `go list -deps` 或 AST 扫描断言禁止的 import |
| 地址解析 | `internal/rpc/listen_test.go` | `unix:`/`tcp:`/裸路径/裸 host:port 四种写法解析正确；Windows 风格路径 `C:\...` 判为 unix |
| OpenAPI 规范一致 | `internal/rpc/httpx/openapi_test.go` | 每个 `api.Method*` 常量在规范里都有对应 path；规范里 schema 的 property 集合与对应 DTO 的 json tag 集合一致；规范能被 YAML 解析 |
| 引擎拆分回归 | `internal/engine/*_test.go` | 现有 1131 行测试保持通过（纯重构，不改行为） |

### 11.2 验收标准

- `go build ./...` 通过；`go vet ./...` 无新增告警。
- `go test ./...` 全绿，含 `-race`。
- `internal/ui` 不再 import `store`/`scheduler`/`engine`/`protocol`/`bittorrent`。
- 仓库中不再存在 `internal/ipc`。
- 两种传输都能完成一次完整的「添加任务 → 开始 → 收到进度事件 → 暂停 → 删除」流程。
- HTTP 传输：`GET /docs` 能打开 Swagger UI；`GET /openapi.json` 返回合法 OpenAPI 文档；
  `GET /openapi.yaml` 返回规范原文。
- 监听地址可配置：`--rpc-listen tcp:127.0.0.1:17830` 时业务进程监听该端口，
  UI 子进程能连上；`--rpc-listen unix:/tmp/x.sock` 时 socket 权限为 0600。
- CI 的 cgo-free 交叉编译任务新增 `./internal/rpc/...`、`./internal/api/`、
  `./internal/urlkind/`、`./internal/appservice/`。

## 12. 风险与取舍

| 风险 | 影响 | 缓解 |
|---|---|---|
| 新增 `google.golang.org/grpc` + `protobuf` 依赖 | 二进制变大（估 +2MB）；纯 Go，不影响交叉编译与打包 | 已是既定方案；CI 交叉编译任务覆盖 |
| `buf` 生成物入库 | `.proto` 与生成物可能漂移 | `scripts/gen_proto.sh` + CI 校验 diff；生成物提交进仓库，CI 不需要装 buf |
| DTO 与 `store.Task` 字段漂移 | UI 读不到新字段 | 转换测试 + 边界测试；新增字段时转换函数是唯一需要改的地方 |
| 手写 api↔pb 映射漏字段 | 静默丢数据 | 往返测试（`DeepEqual`）直接覆盖 |
| 事件流从「帧」变成「流」 | 断线重连语义变化 | 契约测试覆盖断线；今天 UI 断线即退出，行为保持一致 |
| 引擎拆文件引入回归 | 下载行为变化 | 现有 1131 行引擎测试保持不变，作为回归网 |
| 薄封装残留 | `protocol.DetectKind`、`scheduler.BittorrentDefaultDir` 等兼容薄封装在迁移后可能没有调用方，成为死代码 | 迁移第 10 步逐个确认引用，零引用的直接删掉，别让兼容层长期挂着 |
| TCP 暴露面 | 监听地址可配成 `0.0.0.0` 后接口对本机网络可见 | 默认只绑回环地址；绑非回环地址时启动打警告；token 每次调用强制校验，不提供关闭鉴权的开关 |
| OpenAPI 规范漂移 | 文档与代码不一致，调用方按文档写就踩坑 | 规范一致性测试：方法常量 ↔ path、DTO 字段 ↔ schema property 双向校验 |
| swagger-ui 资产增大二进制 | 嵌入 UI 静态资源约 +2MB | 已评估可接受（相对 Fyne 桌面二进制的体积可忽略）；如需瘦身可改为 CDN 加载 |

## 13. 附录：依赖与 CI 变更

**新增依赖**（直接）：

```
google.golang.org/grpc
google.golang.org/protobuf
github.com/swaggo/http-swagger    // Swagger UI 页面（离线嵌入）
github.com/swaggo/files           // swagger-ui 静态资源
```

HTTP 传输本身只用标准库 `net/http`，**不新增依赖**、也不接 h2c；
上面两个 swaggo 依赖只为 `/docs` 的 Swagger UI 页面服务。

**开发期工具**（不入 go.mod）：`buf`、`protoc-gen-go`、`protoc-gen-go-grpc`，
用 `go install` 装到 `$(go env GOPATH)/bin`。

**CI 变更**：

- `.github/workflows/linux.yml` 的 cgo-free 交叉编译包列表追加新包。
- 新增一步：安装 buf 与两个插件 → `scripts/gen_proto.sh` → `git diff --exit-code`
  校验生成物与 `.proto` 同步。

**新增脚本**：`scripts/gen_proto.sh`（生成 pb 代码到 `internal/rpc/grpcx/pb/`）。

**新增命令行参数**：

| 参数 | 含义 | 默认 |
|---|---|---|
| `--rpc-listen <addr>` | UI 子进程连接的监听地址（§6.1 格式） | 生成的 unix socket 路径 |

环境变量 `LDM_RPC_LISTEN` 同义，命令行优先。
