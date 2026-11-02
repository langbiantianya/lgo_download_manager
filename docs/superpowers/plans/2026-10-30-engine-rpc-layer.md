# 下载引擎与 UI 分层 + gRPC/net-http 传输层 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把业务主进程与 UI 子进程之间的自定义 JSON 帧协议拆成四层（契约 / 接口 / 两种传输实现 / 业务侧应用服务），让 UI 与下载引擎彻底隔离，并分别用 gRPC 与 net/http 实现同一份契约。

**Architecture:** 新增 `internal/api`（契约 DTO 与词汇表，叶子包）、`internal/urlkind`（纯 URL 助手，叶子包）、`internal/rpc`（`Transport`/`Handler`/`Client` 接口层 + 共享客户端 + 地址解析）、`internal/rpc/httpx`（net/http 实现）、`internal/rpc/grpcx`（gRPC + protobuf 实现）、`internal/appservice`（业务侧 `Handler` 实现）。`internal/uimgr` 退化为会话生命周期管理，`internal/ui` 只依赖 `internal/rpc` + `internal/api` + `internal/urlkind`，`internal/ipc` 整体删除。

**Tech Stack:** Go 1.26、`net/http`（HTTP 传输）、`google.golang.org/grpc` + `google.golang.org/protobuf`（gRPC 传输）、`buf` + `protoc-gen-go` + `protoc-gen-go-grpc`（代码生成）、`github.com/swaggo/http-swagger` + `github.com/swaggo/files`（Swagger UI 离线嵌入）、`go.yaml.in/yaml/v3`（OpenAPI YAML→JSON）。

**设计文档：** `docs/superpowers/specs/2026-10-30-engine-rpc-layer-design.md`（已提交 `cf76202`）。本计划与该文档一致；两者冲突时以设计文档为准并回头修计划。

## Global Constraints

- 每个任务结束时仓库必须 `go build ./... && go vet ./... && go test ./...` 全绿。
- 注释与文档字符串一律用中文，风格跟随现有代码（见 `internal/engine/engine.go`、`internal/protocol/protocol.go`）。
- 每个新文件（**包括 `_test.go`**）都要有 MPL-2.0 许可证头 + `Copyright (c) 2026 langbiantianya`。
  本计划里的代码块如果省略了文件头，**以仓库既有文件为准补上**
  （参考 `internal/protocol/protocol_test.go:1-5`）。
- `internal/api` 与 `internal/urlkind` 是叶子包：**不得** import 任何其它 `internal/*` 包（`internal/api` 只允许 import `internal/urlkind`）。
- `internal/rpc/grpcx` 与 `internal/rpc/httpx` **不得** import `internal/rpc`（避免 `rpc → httpx → rpc` 的环）；接口满足靠结构化类型，由 `internal/rpc` 里的 `var _ Transport = (*httpx.Client)(nil)` 断言。
- `internal/ui` **不得** import `internal/store`、`internal/scheduler`、`internal/engine`、`internal/protocol`、`internal/bittorrent`、`internal/appservice`。
- 不改动 `internal/store` 的表结构，不改动 `internal/protocol` 各驱动的下载逻辑。
- 不在「设置」里暴露传输协议或监听地址；两者只走命令行参数与环境变量。
- 端口与地址：默认监听 Unix socket（0600 权限）；TCP 模式默认只绑回环地址。
- 新增第三方依赖只允许：`google.golang.org/grpc`、`google.golang.org/protobuf`、`github.com/swaggo/http-swagger`、`github.com/swaggo/files`。开发期工具（`buf` 等）不进 `go.mod`。
- 提交信息用中文，遵循仓库现有风格（`feat:` / `fix:` / `refactor:` / `docs:` / `test:` 前缀）。

---

## 文件结构总览

**新增：**

```
internal/urlkind/urlkind.go            纯 URL 助手（协议判定 / BT 名称 / 目录名推导）
internal/urlkind/urlkind_test.go
internal/api/vocab.go                   Status / StatusFilter / TaskSort / ProxyMode / ProtocolKind
internal/api/types.go                   Task / Settings / Event / BTDetail / FileProgress / 请求响应体
internal/api/message.go                 Message / MessageKind / ShowAddTaskParams
internal/api/methods.go                 方法名常量 + 方法规格表 api.Spec
internal/api/api_test.go
internal/appservice/service.go          业务侧 Handler 实现（dispatch + probe + 事件）
internal/appservice/convert.go          store/bittorrent → api 的 DTO 转换
internal/appservice/convert_test.go
internal/rpc/addr/addr.go               监听地址解析与监听（unix / tcp）
internal/rpc/addr/addr_test.go
internal/rpc/transport.go               Transport 接口
internal/rpc/handler.go                 Handler 接口
internal/rpc/client.go                  Client 接口 + 共享实现
internal/rpc/registry.go                Serve / Dial 注册表
internal/rpc/client_test.go
internal/rpc/contract_test.go           契约测试套件（两种传输各跑一遍）
internal/rpc/httpx/server.go            net/http 服务端
internal/rpc/httpx/client.go            net/http 客户端
internal/rpc/httpx/openapi.yaml         OpenAPI 3.0.3 规范
internal/rpc/httpx/docs.go              /openapi.yaml、/openapi.json、/docs、/docs/
internal/rpc/httpx/httpx_test.go
internal/rpc/httpx/openapi_test.go      规范与代码一致性测试
internal/rpc/grpcx/server.go            gRPC 服务端
internal/rpc/grpcx/client.go            gRPC 客户端
internal/rpc/grpcx/convert.go           api ↔ pb 映射
internal/rpc/grpcx/convert_test.go
internal/rpc/grpcx/pb/lgdm.pb.go        生成物（buf）
internal/rpc/grpcx/pb/lgdm_grpc.pb.go   生成物（buf）
proto/lgdm/v1/lgdm.proto                protobuf 契约
buf.yaml                                buf 配置
buf.gen.yaml                            buf 生成配置
scripts/gen_proto.sh                    生成 pb 代码
internal/ui/boundary_test.go            UI 依赖边界测试
internal/rpc/boundary_test.go           rpc 层依赖边界测试
```

**修改：**

```
internal/protocol/factory.go        ProtocolKind 改为 urlkind 别名 + 薄封装
internal/protocol/bittorrent.go     MagnetName 等改为 urlkind 薄封装
internal/scheduler/bittorrent.go    BittorrentDefaultDir 改为 urlkind 薄封装
internal/uimgr/manager.go           改用 rpc.Serve / 控制消息通道
internal/ui/client.go               删除（能力并入 rpc.Client）
internal/ui/service.go              删除（Service → rpc.Client）
internal/ui/child.go                改用 rpc.Dial
internal/ui/add_task_dialog.go      store/scheduler/protocol → api/urlkind
internal/ui/bt_details.go           store/bittorrent → api
internal/ui/chunk_details.go        store/scheduler → api
internal/ui/main_window.go          store/scheduler → api
internal/ui/piece_mosaic.go         bittorrent → api
internal/ui/settings_dialog.go      store/protocol → api
internal/ui/task_list.go            store/scheduler → api
internal/ui/task_row.go             store/scheduler → api
internal/ui/*_test.go               测试适配新接口
main.go                             --rpc-listen 参数 + 环境变量改名
go.mod / go.sum                     新增依赖
.github/workflows/linux.yml         交叉编译包列表 + 生成物校验
README.md                           补充新架构与参数说明
```

**删除：**

```
internal/ipc/            整个包（channel.go / net.go / payload.go / protocol.go）
internal/engine/engine.go 拆成 engine.go / plan.go / chunk.go / progress.go / retry.go
```

---

### Task 1: 抽出 `internal/urlkind`（纯 URL 助手）

**Files:**
- Create: `internal/urlkind/urlkind.go`
- Create: `internal/urlkind/urlkind_test.go`
- Modify: `internal/protocol/factory.go`
- Modify: `internal/protocol/bittorrent.go`
- Modify: `internal/scheduler/bittorrent.go`

**Interfaces:**
- Consumes: 无（叶子包）
- Produces:
  ```go
  package urlkind
  type ProtocolKind string
  const ProtoHTTP, ProtoHTTPS, ProtoFTP, ProtoWebDAV, ProtoBT ProtocolKind
  func DetectKind(raw string, hint ProtocolKind) (ProtocolKind, error)
  func IsBitTorrent(raw string) bool
  func ResolveURL(raw string, kind ProtocolKind) string
  func MagnetName(raw string) string
  func MagnetInfoHash(raw string) string
  func TorrentNameFromPath(raw string) string
  func SafeDirName(name, fallback string) string
  func BittorrentDefaultDir(rawURL string) string
  ```

- [ ] **Step 1: 写失败测试**

创建 `internal/urlkind/urlkind_test.go`：

```go
package urlkind

import "testing"

func TestDetectKind(t *testing.T) {
	cases := []struct {
		raw  string
		hint ProtocolKind
		want ProtocolKind
		ok   bool
	}{
		{raw: "http://x/a.zip", want: ProtoHTTP, ok: true},
		{raw: "https://x/a.zip", want: ProtoHTTPS, ok: true},
		{raw: "ftp://x/a.zip", want: ProtoFTP, ok: true},
		{raw: "webdav://x/a", want: ProtoWebDAV, ok: true},
		{raw: "dav://x/a", want: ProtoWebDAV, ok: true},
		{raw: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", want: ProtoBT, ok: true},
		{raw: "http://x/a.torrent", want: ProtoBT, ok: true},
		{raw: "http://x/a.torrent?token=1", want: ProtoBT, ok: true},
		{raw: "http://x/a", hint: ProtoHTTP, want: ProtoHTTP, ok: true},
		{raw: "gopher://x/a", ok: false},
	}
	for _, c := range cases {
		got, err := DetectKind(c.raw, c.hint)
		if c.ok {
			if err != nil {
				t.Fatalf("DetectKind(%q,%q) 报错: %v", c.raw, c.hint, err)
			}
			if got != c.want {
				t.Fatalf("DetectKind(%q,%q) = %q, want %q", c.raw, c.hint, got, c.want)
			}
		} else if err == nil {
			t.Fatalf("DetectKind(%q,%q) 应当报错", c.raw, c.hint)
		}
	}
}

func TestIsBitTorrent(t *testing.T) {
	if !IsBitTorrent("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567") {
		t.Fatal("磁力链接应识别为 BT")
	}
	if !IsBitTorrent("/tmp/a.torrent") {
		t.Fatal(".torrent 应识别为 BT")
	}
	if IsBitTorrent("http://x/a.zip") {
		t.Fatal("普通 URL 不应识别为 BT")
	}
}

func TestResolveURL(t *testing.T) {
	if got := ResolveURL("webdav://h/p", ProtoWebDAV); got != "https://h/p" {
		t.Fatalf("ResolveURL webdav = %q", got)
	}
	if got := ResolveURL("dav://h/p", ProtoWebDAV); got != "http://h/p" {
		t.Fatalf("ResolveURL dav = %q", got)
	}
	if got := ResolveURL("https://h/p", ProtoHTTPS); got != "https://h/p" {
		t.Fatalf("ResolveURL https = %q", got)
	}
}

func TestBittorrentDefaultDir(t *testing.T) {
	if got := BittorrentDefaultDir("http://x/a.zip"); got != "" {
		t.Fatalf("非 BT 应返回空串, got %q", got)
	}
	if got := BittorrentDefaultDir("http://x/movie.torrent"); got != "movie" {
		t.Fatalf("torrent 目录名 = %q", got)
	}
	if got := BittorrentDefaultDir("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=My%20Movie"); got != "My Movie" {
		t.Fatalf("磁力目录名 = %q", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/urlkind/`
Expected: FAIL —— `no Go files in .../internal/urlkind` 或 `undefined: DetectKind`

- [ ] **Step 3: 建 `internal/urlkind/urlkind.go`**

把 `internal/protocol/factory.go` 的 `ProtocolKind`、常量、`DetectKind`、`detectBitTorrent`、`IsBitTorrent`、`ResolveURL`，
`internal/protocol/bittorrent.go` 的 `MagnetName`、`MagnetInfoHash`、`TorrentNameFromPath`、`SafeDirName`，
以及 `internal/scheduler/bittorrent.go` 的 `BittorrentDefaultDir` 原样搬过来，改包名为 `urlkind`，
并把 `BittorrentDefaultDir` 里对 `protocol.IsBitTorrent` 的调用换成同包的 `IsBitTorrent`。
所有函数与常量的注释原样保留（它们描述的是协议判定规则，不是某个包的实现细节）。

文件头：

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package urlkind 收纳与传输协议无关的纯 URL 助手：协议判定、
// BitTorrent 资源识别、以及 BT 任务目录名推导。
//
// 这些函数过去散落在 internal/protocol 与 internal/scheduler 里，
// 而 UI 也需要用到它们。抽到这里之后 UI 与业务侧共享同一份实现，
// 且本包不依赖任何其它内部包，可以被任意层安全引用。
package urlkind
```

- [ ] **Step 4: 在 `internal/protocol` 留薄封装**

`internal/protocol/factory.go` 改成：

```go
import (
	"fmt"

	"lgo_download_manager/internal/urlkind"
)

// ProtocolKind 是便于枚举的字符串类型，用于表示所支持的传输协议。
// 定义在 internal/urlkind，这里保留别名以便既有调用点不必改动。
type ProtocolKind = urlkind.ProtocolKind

const (
	ProtoHTTP   = urlkind.ProtoHTTP
	ProtoHTTPS  = urlkind.ProtoHTTPS
	ProtoFTP    = urlkind.ProtoFTP
	ProtoWebDAV = urlkind.ProtoWebDAV
	// ProtoBT 是 BitTorrent(磁力链接 / .torrent 种子文件)。BT 不使用
	// ProtocolDriver 接口——它由 internal/bittorrent 直接驱动调度器的
	// 任务生命周期,这里只作为任务上的协议标记与分发依据。
	ProtoBT = urlkind.ProtoBT
)

// DetectKind 解析给定 URL 应使用的协议。映射规则见 urlkind.DetectKind。
func DetectKind(raw string, hint ProtocolKind) (ProtocolKind, error) {
	return urlkind.DetectKind(raw, hint)
}

// IsBitTorrent 报告 raw 是否应交给 BitTorrent 引擎处理。
func IsBitTorrent(raw string) bool { return urlkind.IsBitTorrent(raw) }

// ResolveURL 返回一个真正可用的传输 URL。
func ResolveURL(raw string, kind ProtocolKind) string {
	return urlkind.ResolveURL(raw, kind)
}
```

`internal/protocol/bittorrent.go` 里 `MagnetName` / `MagnetInfoHash` / `TorrentNameFromPath` / `SafeDirName`
各自改成一行转发：

```go
// MagnetName 提取磁力链接里的 dn 参数。
func MagnetName(raw string) string { return urlkind.MagnetName(raw) }
```

`internal/scheduler/bittorrent.go`：

```go
// BittorrentDefaultDir 推导 BT 任务的默认目录名。实现见 urlkind。
func BittorrentDefaultDir(rawURL string) string { return urlkind.BittorrentDefaultDir(rawURL) }
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/urlkind/ ./internal/protocol/ ./internal/scheduler/`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/urlkind internal/protocol internal/scheduler
git commit -m "refactor(urlkind): 抽出与传输协议无关的纯 URL 助手"
```

---

### Task 2: 建 `internal/api`（契约 DTO 与词汇表）

**Files:**
- Create: `internal/api/vocab.go`
- Create: `internal/api/types.go`
- Create: `internal/api/message.go`
- Create: `internal/api/methods.go`
- Create: `internal/api/api_test.go`

**Interfaces:**
- Consumes: `urlkind.ProtocolKind`（Task 1）
- Produces: 见下方完整类型定义；后续所有任务都引用这些名字。

- [ ] **Step 1: 写失败测试**

创建 `internal/api/api_test.go`：

```go
package api

import (
	"encoding/json"
	"testing"
)

func TestSpecCoversAllMethods(t *testing.T) {
	for _, m := range AllMethods {
		s := Spec(m)
		if s == nil {
			t.Fatalf("方法 %q 缺少规格", m)
		}
		if s.NewIn == nil || s.NewOut == nil {
			t.Fatalf("方法 %q 的规格不完整", m)
		}
	}
	if Spec("nope") != nil {
		t.Fatal("未知方法应返回 nil")
	}
}

func TestTaskJSONRoundTrip(t *testing.T) {
	in := Task{
		ID: "t1", URL: "http://x/a", SavePath: "/tmp/a", Protocol: "HTTP",
		TotalSize: 100, Downloaded: 40, SupportRange: true,
		ETag: "e", LastModified: "m", DigestAlgorithm: "sha256", Digest: "d",
		IsAllocated: true, ChunkCount: 4, MinChunkSize: 1024,
		ChunkProgress: []int64{1, 2}, ChunkRanges: []int64{0, 9, 10, 19},
		Status: StatusDownloading, AuthData: "{}", ErrorMessage: "e", ErrorKind: "k",
		BTFile: "f",
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Task
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if in.ID != out.ID || in.Status != out.Status || in.TotalSize != out.TotalSize {
		t.Fatalf("往返后字段不一致: %+v", out)
	}
	if len(out.ChunkProgress) != 2 || out.ChunkProgress[1] != 2 {
		t.Fatalf("切片字段丢失: %+v", out.ChunkProgress)
	}
}

func TestStatusValuesMatchStore(t *testing.T) {
	// 与 internal/store 的取值保持一致（首字母大写）。
	want := map[Status]string{
		StatusPending:     "Pending",
		StatusDownloading: "Downloading",
		StatusPaused:      "Paused",
		StatusCompleted:   "Completed",
		StatusFailed:      "Failed",
		StatusFileLost:    "FileLost",
	}
	for st, v := range want {
		if string(st) != v {
			t.Fatalf("状态 %q 的取值应为 %q", st, v)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/api/`
Expected: FAIL —— 包不存在

- [ ] **Step 3: 建 `internal/api/vocab.go`**

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package api

import "lgo_download_manager/internal/urlkind"

// ProtocolKind 是任务所属的传输协议。类型别名让 api 与 internal/protocol
// 共用同一个类型，跨层转换时不需要任何映射。
type ProtocolKind = urlkind.ProtocolKind

const (
	ProtoHTTP   = urlkind.ProtoHTTP
	ProtoHTTPS  = urlkind.ProtoHTTPS
	ProtoFTP    = urlkind.ProtoFTP
	ProtoWebDAV = urlkind.ProtoWebDAV
	ProtoBT     = urlkind.ProtoBT
)

// Status 反映 Task 的生命周期状态。取值与 internal/store 完全一致
// （首字母大写），appservice 的转换测试锁定两侧取值集合相同。
type Status string

const (
	StatusPending     Status = "Pending"
	StatusDownloading Status = "Downloading"
	StatusPaused      Status = "Paused"
	StatusCompleted   Status = "Completed"
	StatusFailed      Status = "Failed"
	StatusFileLost    Status = "FileLost"
)

// StatusFilter 是 UI 侧边栏的任务筛选条件。
type StatusFilter string

const (
	FilterAll         StatusFilter = "all"
	FilterPending     StatusFilter = "pending"
	FilterDownloading StatusFilter = "downloading"
	FilterPaused      StatusFilter = "paused"
	FilterCompleted   StatusFilter = "completed"
	FilterFailed      StatusFilter = "failed"
	FilterFileLost    StatusFilter = "filelost"
)

// TaskSort 是任务列表的排序方式。
type TaskSort string

const (
	SortCreatedDesc TaskSort = "created_desc" // 按添加时间倒序（默认，最新的在前）
	SortCreatedAsc  TaskSort = "created_asc"  // 按添加时间正序（最老的在前）
	SortNameAsc     TaskSort = "name_asc"     // 按文件名正序（A-Z）
	SortNameDesc    TaskSort = "name_desc"    // 按文件名倒序（Z-A）
)

// AllTaskSorts 列出所有可选排序选项，用于 UI 渲染。
func AllTaskSorts() []TaskSort {
	return []TaskSort{SortCreatedDesc, SortCreatedAsc, SortNameAsc, SortNameDesc}
}

// ProxyMode 是代理模式。internal/protocol 那边是 iota 整数枚举，
// 这里用它的稳定字符串表示：字符串在 JSON 与 proto 枚举之间更好映射。
type ProxyMode string

const (
	ProxyModeSystem   ProxyMode = "system"
	ProxyModeDisabled ProxyMode = "disabled"
	ProxyModeManual   ProxyMode = "manual"
)
```

- [ ] **Step 4: 建 `internal/api/types.go`**

```go
package api

import "time"

// Task 是任务的对外视图，字段与 internal/store.Task 一一对应。
type Task struct {
	ID              string    `json:"id"`
	URL             string    `json:"url"`
	SavePath        string    `json:"save_path"`
	Protocol        string    `json:"protocol"`
	TotalSize       int64     `json:"total_size"`
	Downloaded      int64     `json:"downloaded"`
	SupportRange    bool      `json:"support_range"`
	ETag            string    `json:"etag"`
	LastModified    string    `json:"last_modified"`
	DigestAlgorithm string    `json:"digest_algorithm,omitempty"`
	Digest          string    `json:"digest,omitempty"`
	IsAllocated     bool      `json:"is_allocated"`
	ChunkCount      int       `json:"chunk_count"`
	MinChunkSize    int64     `json:"min_chunk_size"`
	ChunkProgress   []int64   `json:"chunk_progress"`
	ChunkRanges     []int64   `json:"chunk_ranges"`
	Status          Status    `json:"status"`
	AuthData        string    `json:"auth_data"`
	ErrorMessage    string    `json:"error_message"`
	ErrorKind       string    `json:"error_kind,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	CompletedAt     time.Time `json:"completed_at"`
	BTFile          string    `json:"bt_file,omitempty"`
}

// Settings 是权威设置快照，字段与 internal/store.Settings 一一对应。
type Settings struct {
	DefaultSaveDir     string    `json:"default_save_dir"`
	DefaultThreads     int       `json:"default_threads"`
	MinChunkSize       int64     `json:"min_chunk_size"`
	UserAgent          string    `json:"user_agent"`
	Cookies            string    `json:"cookies"`
	FTPPassive         bool      `json:"ftp_passive"`
	Prealloc           bool      `json:"prealloc"`
	TaskSort           TaskSort  `json:"task_sort"`
	ProxyMode          ProxyMode `json:"proxy_mode"`
	ProxyURL           string    `json:"proxy_url"`
	ProxyBypass        string    `json:"proxy_bypass"`
	ProxyAutoConfigURL string    `json:"proxy_auto_config_url"`
	DownloadLimitKiB   int       `json:"download_limit_kib"`
	LightMode          bool      `json:"light_mode"`
	MaxConcurrent      int       `json:"max_concurrent"`
	AutoStart          bool      `json:"auto_start"`
	Language           string    `json:"language"`
	BTPort             int       `json:"bt_port"`
	BTMaxPeers         int       `json:"bt_max_peers"`
	BTDownloadLimitKiB int       `json:"bt_download_limit_kib"`
	BTUploadLimitKiB   int       `json:"bt_upload_limit_kib"`
	BTDHT              bool      `json:"bt_dht"`
	BTPEX              bool      `json:"bt_pex"`
	BTSeedRatio        float64   `json:"bt_seed_ratio"`
	BTMaxSeeding       int       `json:"bt_max_seeding"`
	BTMaxConcurrent    int       `json:"bt_max_concurrent"`
	BTTrackers         string    `json:"bt_trackers"`
}

// AuthOptions 是随任务一起传递的凭据。字段与 internal/protocol.AuthOptions
// 对齐；ProxyMode 在契约层是字符串，见 vocab.go。
type AuthOptions struct {
	Username           string    `json:"username"`
	Password           string    `json:"password"`
	UserAgent          string    `json:"user_agent"`
	Cookies            string    `json:"cookies"`
	Referer            string    `json:"referer"`
	FTPPassive         bool      `json:"ftp_passive"`
	ProxyMode          ProxyMode `json:"proxy_mode"`
	ProxyURL           string    `json:"proxy_url"`
	ProxyBypass        string    `json:"proxy_bypass"`
	ProxyAutoConfigURL string    `json:"proxy_auto_config_url"`
}

// Event 是一条任务事件，语义与 internal/scheduler.Event 一致。
type Event struct {
	Why      string  `json:"why"`
	Task     *Task   `json:"task"`
	SpeedBPS float64 `json:"speed_bps"`
}

// FileProgress 是种子内单个文件的进度。
type FileProgress struct {
	Path      string `json:"path"`
	Length    int64  `json:"length"`
	Completed int64  `json:"completed"`
}

// BTDetail 是 BT 任务的实时详情快照。字段与 internal/bittorrent.Detail 对齐。
type BTDetail struct {
	Name         string         `json:"name"`
	InfoHash     string         `json:"info_hash"`
	Total        int64          `json:"total"`
	Downloaded   int64          `json:"downloaded"`
	Uploaded     int64          `json:"uploaded"`
	SpeedBPS     float64        `json:"speed_bps"`
	UploadBPS    float64        `json:"upload_bps"`
	Peers        int            `json:"peers"`
	TotalPeers   int            `json:"total_peers"`
	Seeders      int            `json:"seeders"`
	Leechers     int            `json:"leechers"`
	PieceLength  int64          `json:"piece_length"`
	Pieces       int            `json:"pieces"`
	PiecesDone   int            `json:"pieces_done"`
	PieceBuckets []uint8        `json:"piece_buckets"`
	Seeding      bool           `json:"seeding"`
	Complete     bool           `json:"complete"`
	Files        []FileProgress `json:"files"`
	Trackers     []string       `json:"trackers"`
}

// AddTaskInput 描述一次「添加下载任务」。
type AddTaskInput struct {
	URL          string       `json:"url"`
	SavePath     string       `json:"save_path"`
	Protocol     ProtocolKind `json:"protocol"`
	Auth         AuthOptions  `json:"auth"`
	ChunkCount   int          `json:"chunk_count"`
	MinChunkSize int64        `json:"min_chunk_size"`
}

// ListParams 是列表查询参数，语义与 store.ListTasks 的筛选一致。
type ListParams struct {
	Filter StatusFilter `json:"filter"`
	Sort   TaskSort     `json:"sort"`
}

// ListResult 是列表查询的结果。
type ListResult struct {
	Tasks []*Task `json:"tasks"`
}

// TaskRef 引用单个任务。
type TaskRef struct {
	ID string `json:"id"`
}

// ProbeParams 是体积探测的请求。
type ProbeParams struct {
	URL string `json:"url"`
}

// ProbeResult 是体积探测的结果。
type ProbeResult struct {
	TotalSize int64 `json:"total_size"`
}

// BoolResult 是布尔结果。
type BoolResult struct {
	Value bool `json:"value"`
}

// BTInfoParams 是 BT 详情查询的请求。
type BTInfoParams struct {
	TaskID string `json:"task_id"`
}

// Empty 是无负载请求/响应的占位类型。
type Empty struct{}
```

- [ ] **Step 5: 建 `internal/api/message.go`**

```go
package api

// MessageKind 区分消息流里的消息类别。
type MessageKind string

const (
	MessageKindEvent       MessageKind = "event"         // 进度/状态事件
	MessageKindShow        MessageKind = "show"          // 业务托盘「显示窗口」
	MessageKindShowAddTask MessageKind = "show_add_task" // lgom:// 转发后弹对话框
	MessageKindLanguage    MessageKind = "language"      // 权威语言变更
	MessageKindClose       MessageKind = "close"         // 业务侧要求 UI 退出
)

// Message 是服务端推送的一条消息。事件与控制消息合并在同一条流里，
// 用 Kind 区分。
type Message struct {
	Kind        MessageKind        `json:"kind"`
	Event       *Event             `json:"event,omitempty"`
	ShowAddTask *ShowAddTaskParams `json:"show_add_task,omitempty"`
	Language    string             `json:"language,omitempty"`
}

// ShowAddTaskParams 描述一次「请 UI 弹出新建下载任务对话框」的请求。
type ShowAddTaskParams struct {
	URL     string `json:"url"`
	Name    string `json:"name,omitempty"`
	UA      string `json:"ua,omitempty"`
	Cookies string `json:"cookies,omitempty"`
}
```

- [ ] **Step 6: 建 `internal/api/methods.go`**

```go
package api

// 方法名。沿用旧 IPC 协议（internal/ipc）的取值，两种传输共用同一张表。
const (
	MethodList          = "list"
	MethodAdd           = "add"
	MethodStart         = "start"
	MethodPause         = "pause"
	MethodDelete        = "delete"
	MethodIsPreparing   = "is_preparing"
	MethodProbe         = "probe"
	MethodGetSettings   = "get_settings"
	MethodSaveSettings  = "save_settings"
	MethodBTInfo        = "bt_info"
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
func Spec(method string) *MethodSpec {
	return specs[method]
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
```

- [ ] **Step 7: 跑测试确认通过**

Run: `go test ./internal/api/`
Expected: PASS

- [ ] **Step 8: 提交**

```bash
git add internal/api
git commit -m "feat(api): 新增引擎与 UI 之间的契约 DTO 与词汇表"
```

---

### Task 3: 抽出 `internal/appservice`（业务侧 Handler 实现）

**Files:**
- Create: `internal/appservice/service.go`
- Create: `internal/appservice/convert.go`
- Create: `internal/appservice/convert_test.go`
- Create: `internal/appservice/service_test.go`
- Modify: `internal/uimgr/manager.go`（暂时**不动**，只是把逻辑复制过去；Task 8 再删旧代码）

**Interfaces:**
- Consumes: `internal/api`（Task 2）、`internal/scheduler`、`internal/store`、`internal/bittorrent`
- Produces:
  ```go
  package appservice
  type Service struct { /* ... */ }
  func New(st *store.Store, sc *scheduler.Scheduler) *Service
  func (s *Service) SetSettings(cur store.Settings)
  func (s *Service) Settings() store.Settings
  func (s *Service) Call(ctx context.Context, method string, in, out any) error
  func (s *Service) Events() (<-chan api.Event, func())
  func (s *Service) Controls() <-chan api.Message
  func (s *Service) PushControl(msg api.Message)
  // 字段（由 uimgr 赋值）：
  // OnLanguageChanged func(lang string)
  ```

- [ ] **Step 1: 写失败测试（转换层）**

创建 `internal/appservice/convert_test.go`：

```go
package appservice

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/bittorrent"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/store"
)

func TestToAPITask(t *testing.T) {
	now := time.Now()
	src := &store.Task{
		ID: "t1", URL: "http://x/a", SavePath: "/tmp/a", Protocol: "HTTP",
		TotalSize: 100, Downloaded: 40, SupportRange: true,
		ETag: "e", LastModified: "m", DigestAlgorithm: "sha256", Digest: "d",
		IsAllocated: true, ChunkCount: 4, MinChunkSize: 1024,
		ChunkProgress: []int64{1, 2}, ChunkRanges: []int64{0, 9},
		Status: store.TaskStatus.Downloading, AuthData: "{}", ErrorMessage: "msg",
		ErrorKind: "net", CreatedAt: now, UpdatedAt: now, BTFile: "f",
	}
	got := toAPITask(src)
	if got.ID != "t1" || got.Status != api.StatusDownloading || got.TotalSize != 100 {
		t.Fatalf("转换结果不对: %+v", got)
	}
	if len(got.ChunkProgress) != 2 || got.ChunkProgress[1] != 2 {
		t.Fatalf("分片进度丢失: %+v", got.ChunkProgress)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("时间字段丢失")
	}
	if got.BTFile != "f" {
		t.Fatal("BTFile 丢失")
	}
}

func TestStatusValuesMatchStore(t *testing.T) {
	pairs := []struct{ api api.Status; store store.Status }{
		{api.StatusPending, store.TaskStatus.Pending},
		{api.StatusDownloading, store.TaskStatus.Downloading},
		{api.StatusPaused, store.TaskStatus.Paused},
		{api.StatusCompleted, store.TaskStatus.Completed},
		{api.StatusFailed, store.TaskStatus.Failed},
		{api.StatusFileLost, store.TaskStatus.FileLost},
	}
	for _, p := range pairs {
		if string(p.api) != string(p.store) {
			t.Fatalf("状态取值不一致: api=%q store=%q", p.api, p.store)
		}
	}
}

func TestProxyModeRoundTrip(t *testing.T) {
	for _, m := range []protocol.ProxyMode{
		protocol.ProxyModeSystem, protocol.ProxyModeDisabled, protocol.ProxyModeManual,
	} {
		got := toAPIProxyMode(m)
		if toStoreProxyMode(got) != m {
			t.Fatalf("代理模式往返失败: %v → %q → %v", m, got, toStoreProxyMode(got))
		}
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	src := store.Settings{
		DefaultSaveDir: "/tmp", DefaultThreads: 8, MinChunkSize: 4096,
		UserAgent: "UA", Cookies: "C", FTPPassive: true, Prealloc: true,
		TaskSort: store.SortNameAsc, ProxyMode: protocol.ProxyModeManual,
		ProxyURL: "http://p", ProxyBypass: "b", ProxyAutoConfigURL: "u",
		DownloadLimitKiB: 512, LightMode: true, MaxConcurrent: 3,
		AutoStart: true, Language: "en", BTPort: 6881, BTMaxPeers: 50,
		BTDownloadLimitKiB: 100, BTUploadLimitKiB: 50, BTDHT: true, BTPEX: true,
		BTSeedRatio: 1.5, BTMaxSeeding: 10, BTMaxConcurrent: 2,
		BTTrackers: "udp://tracker.example:1337",
	}
	got := toAPISettings(src)
	if got.DefaultSaveDir != "/tmp" || got.TaskSort != api.SortNameAsc ||
		got.ProxyMode != api.ProxyModeManual || got.BTSeedRatio != 1.5 ||
		got.BTTrackers != src.BTTrackers {
		t.Fatalf("设置转换不对: %+v", got)
	}
	back := toStoreSettings(got)
	if back.DefaultSaveDir != src.DefaultSaveDir || back.ProxyMode != src.ProxyMode ||
		back.TaskSort != src.TaskSort || back.BTMaxPeers != src.BTMaxPeers ||
		back.BTTrackers != src.BTTrackers {
		t.Fatalf("设置往返失败: %+v", back)
	}
}

func TestToAPIBTDetail(t *testing.T) {
	src := &bittorrent.Detail{
		Name: "n", InfoHash: "h", Total: 10, Downloaded: 5, Uploaded: 1,
		SpeedBPS: 2, UploadBPS: 3, Peers: 4, TotalPeers: 5, Seeders: 6,
		Leechers: 7, PieceLength: 8, Pieces: 9, PiecesDone: 10,
		PieceBuckets: []uint8{1, 2}, Seeding: true, Complete: true,
		Files:    []bittorrent.FileProgress{{Path: "p", Length: 1, Completed: 2}},
		Trackers: []string{"t"},
	}
	got := toAPIBTDetail(src)
	if got.InfoHash != "h" || got.PieceBuckets[1] != 2 || got.Files[0].Path != "p" {
		t.Fatalf("BT 详情转换不对: %+v", got)
	}
}

// TestDTOFieldParity 用反射逐字段核对契约层 DTO 与来源结构体的字段集合。
//
// 这是「静默丢数据」的最后一道护栏：将来给 store.Task 加了字段却忘了在
// toAPITask 里搬运，契约层就会少一个字段，而其它测试照样全绿——用户
// 的设置或任务信息就那样消失了，没有任何报错。
func TestDTOFieldParity(t *testing.T) {
	cases := []struct {
		name   string
		api    any
		source any
	}{
		{"Task", api.Task{}, store.Task{}},
		{"Settings", api.Settings{}, store.Settings{}},
		{"BTDetail", api.BTDetail{}, bittorrent.Detail{}},
		{"AuthOptions", api.AuthOptions{}, protocol.AuthOptions{}},
	}
	for _, c := range cases {
		want := fieldNames(reflect.TypeOf(c.source))
		got := fieldNames(reflect.TypeOf(c.api))
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%s 的字段集合与来源不一致:\n来源 = %v\n契约 = %v", c.name, want, got)
		}
	}
}

// fieldNames 返回结构体的导出字段名集合（排序）。
func fieldNames(t reflect.Type) []string {
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // 非导出字段
		}
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/appservice/`
Expected: FAIL —— 包不存在

- [ ] **Step 3: 建 `internal/appservice/convert.go`**

```go
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

// toProtocolAuthOptions 把契约层的凭据转成协议层的凭据。
func toProtocolAuthOptions(a api.AuthOptions) protocol.AuthOptions {
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

// toAPIAuthOptions 把协议层的凭据转成契约层的凭据。
func toAPIAuthOptions(a protocol.AuthOptions) api.AuthOptions {
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
```

- [ ] **Step 4: 建 `internal/appservice/service.go`**

把 `internal/uimgr/manager.go` 里的 `dispatch`、`probe`、`probeBittorrent` 搬过来，改写成基于
`api` DTO 的实现：

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package appservice 是业务侧的应用服务：把 scheduler / store / bittorrent
// 的能力包装成与传输无关的契约（api 包）。传输层（gRPC / net/http）只认
// 这层契约，不直接接触数据库与调度器内部。
package appservice

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/bittorrent"
	"lgo_download_manager/internal/protocol"
	"lgo_download_manager/internal/scheduler"
	"lgo_download_manager/internal/settings"
	"lgo_download_manager/internal/store"
)

// Service 实现契约层的 Handler。
type Service struct {
	st *store.Store
	sc *scheduler.Scheduler

	mu       sync.Mutex
	settings store.Settings

	// controls 是控制消息通道（显示窗口 / 弹对话框 / 语言变更 / 退出）。
	controls chan api.Message

	// OnLanguageChanged 在保存设置导致语言变化时被调用。由 uimgr 注册，
	// 用来把语言变更推给 UI。
	OnLanguageChanged func(lang string)
}

// New 创建应用服务。settings 由调用方通过 SetSettings 注入。
func New(st *store.Store, sc *scheduler.Scheduler) *Service {
	return &Service{
		st:       st,
		sc:       sc,
		controls: make(chan api.Message, 16),
	}
}

// SetSettings 更新服务持有的权威设置快照。
func (s *Service) SetSettings(cur store.Settings) {
	s.mu.Lock()
	s.settings = cur
	s.mu.Unlock()
}

// Settings 返回当前权威设置快照。
func (s *Service) Settings() store.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// PushControl 往控制消息通道推一条消息。通道满时丢弃——控制消息是
// 「尽力而为」的通知，丢了不该阻塞业务侧。
func (s *Service) PushControl(msg api.Message) {
	select {
	case s.controls <- msg:
	default:
	}
}

// Controls 返回控制消息通道。
func (s *Service) Controls() <-chan api.Message { return s.controls }

// Events 订阅调度器事件流。退订函数由调度器提供。
func (s *Service) Events() (<-chan api.Event, func()) {
	ch, unsub := s.sc.Subscribe()
	out := make(chan api.Event, cap(ch))
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(out)
		for {
			select {
			case <-stop:
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- toAPIEvent(ev):
				default:
				}
			}
		}
	}()
	return out, func() {
		once.Do(func() {
			close(stop)
			unsub()
		})
	}
}

// Call 执行一次契约层调用。in / out 的具体类型由 api.Spec(method) 决定。
func (s *Service) Call(ctx context.Context, method string, in, out any) error {
	switch method {
	case api.MethodList:
		p, ok := in.(*api.ListParams)
		if !ok {
			return fmt.Errorf("appservice: list 参数类型不对")
		}
		tasks, err := s.sc.List(store.StatusFilter(p.Filter), store.TaskSort(p.Sort))
		if err != nil {
			return err
		}
		outTasks := make([]*api.Task, 0, len(tasks))
		for _, t := range tasks {
			outTasks = append(outTasks, toAPITask(t))
		}
		res, ok := out.(*api.ListResult)
		if !ok {
			return fmt.Errorf("appservice: list 结果类型不对")
		}
		res.Tasks = outTasks
		return nil

	case api.MethodAdd:
		p, ok := in.(*api.AddTaskInput)
		if !ok {
			return fmt.Errorf("appservice: add 参数类型不对")
		}
		kind := p.Protocol
		if kind == "" {
			var derr error
			kind, derr = protocol.DetectKind(p.URL, "")
			if derr != nil {
				return derr
			}
		}
		tk, err := s.sc.Add(scheduler.AddTaskInput{
			URL:          p.URL,
			SavePath:     p.SavePath,
			Protocol:     kind,
			Auth:         toProtocolAuthOptions(p.Auth),
			ChunkCount:   p.ChunkCount,
			MinChunkSize: p.MinChunkSize,
		})
		if err != nil {
			return err
		}
		res, ok := out.(*api.Task)
		if !ok {
			return fmt.Errorf("appservice: add 结果类型不对")
		}
		*res = *toAPITask(tk)
		return nil

	case api.MethodStart:
		p, ok := in.(*api.TaskRef)
		if !ok {
			return fmt.Errorf("appservice: start 参数类型不对")
		}
		return s.sc.Start(p.ID)

	case api.MethodPause:
		p, ok := in.(*api.TaskRef)
		if !ok {
			return fmt.Errorf("appservice: pause 参数类型不对")
		}
		return s.sc.Pause(p.ID)

	case api.MethodDelete:
		p, ok := in.(*api.TaskRef)
		if !ok {
			return fmt.Errorf("appservice: delete 参数类型不对")
		}
		return s.sc.Delete(p.ID)

	case api.MethodIsPreparing:
		p, ok := in.(*api.TaskRef)
		if !ok {
			return fmt.Errorf("appservice: is_preparing 参数类型不对")
		}
		res, ok := out.(*api.BoolResult)
		if !ok {
			return fmt.Errorf("appservice: is_preparing 结果类型不对")
		}
		res.Value = s.sc.IsPreparing(p.ID)
		return nil

	case api.MethodProbe:
		p, ok := in.(*api.ProbeParams)
		if !ok {
			return fmt.Errorf("appservice: probe 参数类型不对")
		}
		size, err := s.probe(ctx, p.URL)
		if err != nil {
			return err
		}
		res, ok := out.(*api.ProbeResult)
		if !ok {
			return fmt.Errorf("appservice: probe 结果类型不对")
		}
		res.TotalSize = size
		return nil

	case api.MethodGetSettings:
		res, ok := out.(*api.Settings)
		if !ok {
			return fmt.Errorf("appservice: get_settings 结果类型不对")
		}
		*res = toAPISettings(s.Settings())
		return nil

	case api.MethodSaveSettings:
		p, ok := in.(*api.Settings)
		if !ok {
			return fmt.Errorf("appservice: save_settings 参数类型不对")
		}
		return s.saveSettings(*p)

	case api.MethodBTInfo:
		p, ok := in.(*api.BTInfoParams)
		if !ok {
			return fmt.Errorf("appservice: bt_info 参数类型不对")
		}
		d, err := s.sc.BTTaskDetail(p.TaskID)
		if err != nil {
			return err
		}
		res, ok := out.(*api.BTDetail)
		if !ok {
			return fmt.Errorf("appservice: bt_info 结果类型不对")
		}
		*res = *toAPIBTDetail(d)
		return nil

	case api.MethodStreamMessages:
		// 消息流不走通用分派——传输层直接读 Events() 与 Controls() 两条流。
		// 这里显式分支，免得它掉进下面的「未知方法」默认分支，让排障时
		// 误以为契约里没有这个方法。
		return fmt.Errorf("appservice: 方法 %q 不走通用分派", method)

	default:
		return fmt.Errorf("appservice: 未知方法 %q", method)
	}
}

// saveSettings 持久化设置并同步权威快照。语言变化时通知调用方。
func (s *Service) saveSettings(in api.Settings) error {
	st := toStoreSettings(in)
	if err := settings.Save(s.st, st); err != nil {
		return err
	}
	prev := s.Settings().Language
	s.SetSettings(st)
	if st.Language != prev && s.OnLanguageChanged != nil {
		s.OnLanguageChanged(st.Language)
	}
	return nil
}

// probe 在业务进程内探测 URL 目标大小（复用业务侧代理配置）。
// ctx 透传给底层驱动：调用方取消后探测要跟着中止，不能占住处理协程
// 直到自己的超时到点。
func (s *Service) probe(ctx context.Context, raw string) (int64, error) {
	kind, err := protocol.DetectKind(raw, "")
	if err != nil {
		return 0, err
	}
	if kind == protocol.ProtoBT {
		return s.probeBittorrent(ctx, raw)
	}
	cur := s.Settings()
	driver, err := protocol.New(raw, kind, protocol.Auth{AuthOptions: protocol.AuthOptions{
		UserAgent:          cur.UserAgent,
		Cookies:            cur.Cookies,
		FTPPassive:         cur.FTPPassive,
		ProxyMode:          cur.ProxyMode,
		ProxyURL:           cur.ProxyURL,
		ProxyBypass:        cur.ProxyBypass,
		ProxyAutoConfigURL: cur.ProxyAutoConfigURL,
	}})
	if err != nil {
		return 0, err
	}
	defer driver.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	caps, err := driver.Probe(ctx)
	if err != nil {
		return 0, err
	}
	return caps.TotalSize, nil
}

// probeBittorrent 解析 .torrent 种子文件的总体积。磁力链接缺元数据，
// 返回 0(体积未知)。任何取种子的失败都当作「未知」而不是错误——
// 体积预览不该拦住用户添加任务。但要留下日志，否则用户看到的只是
// 「体积未知」，无从判断是网络问题还是种子本身损坏。
func (s *Service) probeBittorrent(ctx context.Context, raw string) (int64, error) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "magnet:") {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		data, err := bittorrent.FetchTorrentFile(ctx, raw, protocol.HTTPProxyFunc())
		if err != nil {
			logging.Printf("appservice: probe torrent file: %v", err)
			return 0, nil
		}
		if _, total, perr := bittorrent.TorrentFileInfo(data); perr == nil {
			return total, nil
		} else {
			logging.Printf("appservice: parse torrent file: %v", perr)
		}
	}
	return 0, nil
}
```

- [ ] **Step 5: 写失败测试（服务层）**

创建 `internal/appservice/service_test.go`：

```go
package appservice

import (
	"context"
	"testing"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/store"
)

func TestCallUnknownMethod(t *testing.T) {
	s := New(nil, nil)
	if err := s.Call(context.Background(), "nope", &api.Empty{}, &api.Empty{}); err == nil {
		t.Fatal("未知方法应当报错")
	}
}

func TestCallListAndSettings(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	s := New(st, scheduler.New(st))
	s.SetSettings(store.Settings{Language: "en"})

	res := &api.ListResult{}
	if err := s.Call(context.Background(), api.MethodList, &api.ListParams{Filter: api.FilterAll, Sort: api.SortCreatedDesc}, res); err != nil {
		t.Fatal(err)
	}
	if res.Tasks == nil {
		t.Fatal("任务列表不应为 nil")
	}

	out := &api.Settings{}
	if err := s.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, out); err != nil {
		t.Fatal(err)
	}
	if out.Language != "en" {
		t.Fatalf("设置读取不对: %+v", out)
	}
}

func TestPushControl(t *testing.T) {
	s := New(nil, nil)
	s.PushControl(api.Message{Kind: api.MessageKindShow})
	select {
	case m := <-s.Controls():
		if m.Kind != api.MessageKindShow {
			t.Fatalf("控制消息不对: %+v", m)
		}
	default:
		t.Fatal("控制消息没有送达")
	}
}
```

在文件头补上 `"path/filepath"` 的 import。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/appservice/`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add internal/appservice
git commit -m "feat(appservice): 抽出业务侧应用服务并做 DTO 转换"
```

---

### Task 4: 建 `internal/rpc`（接口层 + 共享客户端 + 地址解析）

**Files:**
- Create: `internal/rpc/addr/addr.go`
- Create: `internal/rpc/addr/addr_test.go`
- Create: `internal/rpc/transport.go`
- Create: `internal/rpc/handler.go`
- Create: `internal/rpc/client.go`
- Create: `internal/rpc/client_test.go`

**Interfaces:**
- Consumes: `internal/api`（Task 2）
- Produces:
  ```go
  package rpc
  type Transport interface {
      Call(ctx context.Context, method string, in, out any) error
      Stream(ctx context.Context) (<-chan api.Message, error)
  }
  type Handler interface {
      Call(ctx context.Context, method string, in, out any) error
      Events() (<-chan api.Event, func())
      Controls() <-chan api.Message
  }
  type Client interface { /* 见下 */ }
  // newClient 供本包测试用；对外的 Serve/Dial 在 Task 7 随注册表一起提供。

  package addr
  type Addr struct { Network, Address string }
  func Parse(s string) (Addr, error)
  func Listen(a Addr) (net.Listener, func(), error)
  func Dial(a Addr) (net.Conn, error)
  ```

- [ ] **Step 1: 写失败测试（地址解析）**

创建 `internal/rpc/addr/addr_test.go`：

```go
package addr

import (
	"os"
	"path/filepath"
	"testing"
)
func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		network string
		address string
		bad     bool
	}{
		{in: "unix:/tmp/lgdm.sock", network: "unix", address: "/tmp/lgdm.sock"},
		{in: "tcp:127.0.0.1:17830", network: "tcp", address: "127.0.0.1:17830"},
		{in: "tcp::17830", network: "tcp", address: ":17830"},
		{in: "/tmp/lgdm.sock", network: "unix", address: "/tmp/lgdm.sock"},
		{in: `./ui.sock`, network: "unix", address: `./ui.sock`},
		// Windows 路径含冒号，不能被当成 host:port。
		{in: `C:\Users\lbty\AppData\Local\ui.sock`, network: "unix", address: `C:\Users\lbty\AppData\Local\ui.sock`},
		{in: "127.0.0.1:17830", network: "tcp", address: "127.0.0.1:17830"},
		{in: "unix:", bad: true},
		{in: "tcp:", bad: true},
		{in: "nonsense", bad: true},
		{in: "", bad: true},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if c.bad {
			if err == nil {
				t.Fatalf("Parse(%q) 应当报错", c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("Parse(%q) 报错: %v", c.in, err)
		}
		if got.Network != c.network || got.Address != c.address {
			t.Fatalf("Parse(%q) = %+v, want %s/%s", c.in, got, c.network, c.address)
		}
	}
}

// TestListenUnixAndDial 验证 unix socket 的监听与连接：socket 文件会被创建、
// 权限收紧到 0600，cleanup 之后文件被删掉。
func TestListenUnixAndDial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lgdm.sock")
	a := Addr{Network: "unix", Address: path}
	ln, cleanup, err := Listen(a)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("socket 文件没创建: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket 权限 = %o, want 600", perm)
	}

	conn, err := Dial(a)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = conn.Close()

	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cleanup 后 socket 文件仍在: %v", err)
	}
	// cleanup 幂等：再调一次不能 panic。
	cleanup()
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/rpc/addr/`
Expected: FAIL —— 包不存在

- [ ] **Step 3: 建 `internal/rpc/addr/addr.go`**

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package addr 解析与监听传输层的网络地址。传输层既可以监听 Unix socket
// （默认，仅本机可达），也可以监听 TCP 的 host:port。
//
// 本包是叶子包：gRPC 与 net/http 两种传输实现都依赖它，但它不依赖任何
// 传输实现，因此不会形成包环。
package addr

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// Addr 描述一个监听或连接地址。
type Addr struct {
	// Network 是 "unix" 或 "tcp"。
	Network string
	// Address 是 unix socket 路径，或 tcp 的 host:port。
	Address string
}

// Parse 解析地址描述。支持三种写法：
//
//	unix:<path>       显式 Unix socket
//	tcp:<host:port>   显式 TCP
//	<裸值>            自动判别：含路径分隔符（/ 或 \）视为 unix，
//	                  否则按 host:port 视为 tcp
//
// 裸值判别必须区分 "/" 与 "\"：Windows 上的 socket 路径形如
// C:\Users\...\ui.sock，里面含冒号，按「含冒号即 host:port」会误判。
func Parse(s string) (Addr, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return Addr{}, fmt.Errorf("addr: 地址为空")
	case strings.HasPrefix(s, "unix:"):
		p := strings.TrimPrefix(s, "unix:")
		if p == "" {
			return Addr{}, fmt.Errorf("addr: unix 地址缺少路径: %q", s)
		}
		return Addr{Network: "unix", Address: p}, nil
	case strings.HasPrefix(s, "tcp:"):
		h := strings.TrimPrefix(s, "tcp:")
		if h == "" {
			return Addr{}, fmt.Errorf("addr: tcp 地址缺少 host:port: %q", s)
		}
		return Addr{Network: "tcp", Address: h}, nil
	case strings.ContainsAny(s, `/\`):
		return Addr{Network: "unix", Address: s}, nil
	case strings.Contains(s, ":"):
		return Addr{Network: "tcp", Address: s}, nil
	default:
		return Addr{}, fmt.Errorf("addr: 无法识别的地址 %q（示例：unix:/tmp/lgdm.sock 或 tcp:127.0.0.1:17830）", s)
	}
}

// Listen 按地址创建监听器。cleanup 负责关闭监听器；unix 分支还会删除
// socket 文件。
func Listen(a Addr) (net.Listener, func(), error) {
	if a.Network == "tcp" {
		ln, err := net.Listen("tcp", a.Address)
		if err != nil {
			return nil, nil, fmt.Errorf("addr: listen %s: %w", a.Address, err)
		}
		return ln, func() { _ = ln.Close() }, nil
	}
	// 启动前清理残留 socket 文件，否则上次异常退出留下的路径会让
	// net.Listen 报 "address already in use"。
	if err := os.Remove(a.Address); err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("addr: remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", a.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("addr: listen %s: %w", a.Address, err)
	}
	// 只允许当前用户读写。
	if err := os.Chmod(a.Address, 0o600); err != nil {
		_ = ln.Close()
		return nil, nil, fmt.Errorf("addr: chmod %s: %w", a.Address, err)
	}
	return ln, func() {
		_ = ln.Close()
		_ = os.Remove(a.Address)
	}, nil
}

// Dial 按地址建立连接。
func Dial(a Addr) (net.Conn, error) {
	conn, err := net.Dial(a.Network, a.Address)
	if err != nil {
		return nil, fmt.Errorf("addr: dial %s %s: %w", a.Network, a.Address, err)
	}
	return conn, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/rpc/addr/`
Expected: PASS

- [ ] **Step 5: 建 `internal/rpc/transport.go` 与 `internal/rpc/handler.go`**

`transport.go`：

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package rpc 是业务进程与 UI 子进程之间的传输抽象层：定义与协议无关的
// Transport / Handler / Client 三组契约，两种协议实现（gRPC、net/http）
// 都挂在 Transport 上，UI 只认 Client。
package rpc

import (
	"context"

	"lgo_download_manager/internal/api"
)

// Transport 是协议实现需要满足的最小能力面。新增一种协议只需实现这两个
// 方法，调用语义（超时、错误包装、事件扇出）由本包的共享实现统一处理。
type Transport interface {
	// Call 执行一次调用。method 取 api.Method* 常量；in / out 是 api 层的
	// DTO 指针，类型由 api.Spec(method) 决定，由传输实现按自己的线格式编解码。
	Call(ctx context.Context, method string, in, out any) error

	// Stream 打开服务端推送的消息流（进度事件与控制消息合并流）。
	// 返回的 channel 在流结束或 ctx 取消后关闭。
	Stream(ctx context.Context) (<-chan api.Message, error)

	// Close 释放底层连接与流。实现必须幂等——Close 可能被重复调用。
	// 没有它，客户端无法关闭连接：读循环会一直挂着，goroutine 与套接字
	// 都会泄漏。
	Close() error
}
```

`handler.go`：

```go
package rpc

import (
	"context"

	"lgo_download_manager/internal/api"
)

// Handler 是业务侧要实现的契约。由 internal/appservice 提供实现。
type Handler interface {
	// Call 执行一次调用。method 取 api.Method* 常量；in / out 的具体类型
	// 由 api.Spec(method) 决定，传输层分配好再传进来。
	Call(ctx context.Context, method string, in, out any) error

	// Events 返回进度事件流与退订函数。
	Events() (<-chan api.Event, func())

	// Controls 返回控制消息流（显示窗口 / 弹对话框 / 语言变更 / 退出）。
	Controls() <-chan api.Message
}
```

- [ ] **Step 6: 建 `internal/rpc/client.go`**

```go
package rpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"lgo_download_manager/internal/api"
)

// callTimeout 是单次 RPC 的超时上限。
const callTimeout = 30 * time.Second

// Client 是 UI 侧看到的全部能力。方法签名与旧的 ui.Service 一一对应，
// 只是类型换成了契约层的 api 包。
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

	// Subscribe 订阅进度事件；unsub 取消订阅。
	Subscribe() (<-chan api.Event, func())
	// SetOnShow 注册「显示窗口」回调。
	SetOnShow(f func())
	// SetOnShowAddTask 注册「弹出新建任务对话框」回调。
	SetOnShowAddTask(f func(api.ShowAddTaskParams))
	// SetOnLanguage 注册「语言变更」回调。
	SetOnLanguage(f func())
	// SetOnClose 注册「业务侧要求退出 / 连接断开」回调。
	SetOnClose(f func())

	Close() error
}

// client 是基于 Transport 的共享客户端实现。两种协议共用它，
// 因此超时、错误包装、事件扇出等语义只有一处。
type client struct {
	tr Transport

	// cancel 停掉读循环。由 Close 调用。
	cancel context.CancelFunc

	settingsMu sync.Mutex
	settings   api.Settings

	subMu sync.Mutex
	subs  []chan api.Event

	hookMu       sync.Mutex
	onShow       func()
	onShowAddTask func(api.ShowAddTaskParams)
	onLanguage   func()
	onClose      func()

	// showAddTaskQ 缓存「弹出新建任务对话框」的请求，直到 UI 注册回调。
	//
	// 业务进程的 lgom:// 转发可能在 UI 子进程还没注册回调时就发过来了。
	// 不缓存就直接丢弃，主窗口被拉起后却看不到对话框——这是旧 IPC
	// 客户端修过的一个坑，契约层必须保住这条保证。
	//
	// 由 hookMu 保护，与 onShowAddTask 同锁：「读钩子/入队」与
	// 「设钩子/排空」必须互斥，否则会出现「读到钩子为空 → 对方设好
	// 钩子并排空空队列 → 才入队」的窗口，消息就永远送不出去。
	showAddTaskQ []api.ShowAddTaskParams

	closed bool
	once   sync.Once
}

var _ Client = (*client)(nil)

// newClient 建立客户端：先用 get_settings 做一次往返完成握手
// （鉴权由 Transport 负责，握手失败即等同鉴权失败），并拿到初始设置快照，
// 然后拉起消息流。
//
// ctx 应当是**贯穿整个会话**的 ctx。读循环不能直接跟着它走——调用方可能
// 只给握手或单次调用用一个短超时的 ctx，那会让读循环跟着超时，把
// 「一次调用超时」误报成「业务进程已退出」。
func newClient(ctx context.Context, tr Transport) (*client, error) {
	c := &client{tr: tr}

	out := &api.Settings{}
	if err := c.call(ctx, api.MethodGetSettings, &api.Empty{}, out); err != nil {
		return nil, fmt.Errorf("rpc: 握手失败: %w", err)
	}
	c.settingsMu.Lock()
	c.settings = *out
	c.settingsMu.Unlock()

	// WithoutCancel 摘掉调用方 ctx 的取消传播，再用 WithCancel 拿到一个
	// 由 client.Close() 控制的 cancel：读循环活到整个会话结束。
	streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.cancel = cancel
	go c.readLoop(streamCtx)
	return c, nil
}

// call 执行一次 RPC，带 30s 超时。
func (c *client) call(ctx context.Context, method string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return c.tr.Call(ctx, method, in, out)
}

// readLoop 消费服务端推送的消息流。
func (c *client) readLoop(ctx context.Context) {
	ch, err := c.tr.Stream(ctx)
	if err != nil {
		c.notifyClose()
		return
	}
	for {
		select {
		case <-ctx.Done():
			c.notifyClose()
			return
		case msg, ok := <-ch:
			if !ok {
				c.notifyClose()
				return
			}
			c.dispatch(msg)
		}
	}
}

// dispatch 把一条消息分发到事件订阅者或控制回调。
func (c *client) dispatch(msg api.Message) {
	switch msg.Kind {
	case api.MessageKindEvent:
		if msg.Event != nil {
			c.broadcast(*msg.Event)
		}
	case api.MessageKindShow:
		if h := c.showHook(); h != nil {
			h()
		}
	case api.MessageKindShowAddTask:
		if msg.ShowAddTask == nil {
			return
		}
		// 钩子与队列同锁：读钩子与入队要么一起发生，要么都不发生。
		c.hookMu.Lock()
		h := c.onShowAddTask
		if h == nil {
			if len(c.showAddTaskQ) < 1 {
				c.showAddTaskQ = append(c.showAddTaskQ, *msg.ShowAddTask)
			}
			c.hookMu.Unlock()
			return
		}
		c.hookMu.Unlock()
		h(*msg.ShowAddTask)
	case api.MessageKindLanguage:
		if h := c.languageHook(); h != nil {
			h()
		}
	case api.MessageKindClose:
		c.notifyClose()
	}
}

func (c *client) showHook() func() {
	c.hookMu.Lock()
	defer c.hookMu.Unlock()
	return c.onShow
}

func (c *client) languageHook() func() {
	c.hookMu.Lock()
	defer c.hookMu.Unlock()
	return c.onLanguage
}

// broadcast 向所有订阅者非阻塞派发事件。订阅者消费不过来时丢弃事件，
// 绝不阻塞读循环。
func (c *client) broadcast(ev api.Event) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for _, ch := range c.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe 订阅进度事件。退订时关闭通道，与广播互斥。
func (c *client) Subscribe() (<-chan api.Event, func()) {
	ch := make(chan api.Event, 64)
	c.subMu.Lock()
	c.subs = append(c.subs, ch)
	c.subMu.Unlock()
	return ch, func() {
		c.subMu.Lock()
		defer c.subMu.Unlock()
		for i, s := range c.subs {
			if s == ch {
				c.subs = append(c.subs[:i], c.subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
}

func (c *client) SetOnShow(f func()) {
	c.hookMu.Lock()
	c.onShow = f
	c.hookMu.Unlock()
}

func (c *client) SetOnShowAddTask(f func(api.ShowAddTaskParams)) {
	// 钩子与队列同一把锁——与 dispatch 的「读钩子/入队」互斥，
	// 保证注册前到达的请求要么被立即回调，要么被排空补发，不会丢。
	c.hookMu.Lock()
	c.onShowAddTask = f
	pending := c.showAddTaskQ
	c.showAddTaskQ = nil
	c.hookMu.Unlock()
	if f != nil {
		for _, p := range pending {
			f(p)
		}
	}
}

func (c *client) SetOnLanguage(f func()) {
	c.hookMu.Lock()
	c.onLanguage = f
	c.hookMu.Unlock()
}

// SetOnClose 注册「业务侧要求退出 / 连接断开」回调。若连接在注册之前
// 就已经断开，立刻补一次回调——否则 UI 会一直以为业务进程还在。
func (c *client) SetOnClose(f func()) {
	c.hookMu.Lock()
	c.onClose = f
	closed := c.closed
	c.hookMu.Unlock()
	if closed && f != nil {
		f()
	}
}

// notifyClose 标记连接已断开并触发一次 onClose 回调。
func (c *client) notifyClose() {
	c.once.Do(func() {
		c.hookMu.Lock()
		c.closed = true
		h := c.onClose
		c.hookMu.Unlock()
		if h != nil {
			h()
		}
	})
}

// List 查询任务列表。
func (c *client) List(filter api.StatusFilter, sort api.TaskSort) ([]*api.Task, error) {
	out := &api.ListResult{}
	if err := c.call(context.Background(), api.MethodList, &api.ListParams{Filter: filter, Sort: sort}, out); err != nil {
		return nil, err
	}
	return out.Tasks, nil
}

// AddTask 添加一个任务（不自动启动）。
func (c *client) AddTask(in api.AddTaskInput) (*api.Task, error) {
	out := &api.Task{}
	if err := c.call(context.Background(), api.MethodAdd, &in, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Start 启动任务。
func (c *client) Start(taskID string) error {
	return c.call(context.Background(), api.MethodStart, &api.TaskRef{ID: taskID}, &api.Empty{})
}

// Pause 暂停任务。
func (c *client) Pause(taskID string) error {
	return c.call(context.Background(), api.MethodPause, &api.TaskRef{ID: taskID}, &api.Empty{})
}

// Delete 删除任务。
func (c *client) Delete(taskID string) error {
	return c.call(context.Background(), api.MethodDelete, &api.TaskRef{ID: taskID}, &api.Empty{})
}

// IsPreparing 报告任务是否处于「Start 已预留 slot 但引擎尚未启动」的
// 准备阶段。查询失败时按 false 处理（与旧实现一致）。
func (c *client) IsPreparing(taskID string) bool {
	out := &api.BoolResult{}
	if err := c.call(context.Background(), api.MethodIsPreparing, &api.TaskRef{ID: taskID}, out); err != nil {
		return false
	}
	return out.Value
}

// Probe 探测 URL 目标大小。
func (c *client) Probe(url string) (int64, error) {
	out := &api.ProbeResult{}
	if err := c.call(context.Background(), api.MethodProbe, &api.ProbeParams{URL: url}, out); err != nil {
		return 0, err
	}
	return out.TotalSize, nil
}

// BTTaskDetail 返回 BT 任务的实时详情。
func (c *client) BTTaskDetail(taskID string) (*api.BTDetail, error) {
	out := &api.BTDetail{}
	if err := c.call(context.Background(), api.MethodBTInfo, &api.BTInfoParams{TaskID: taskID}, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Settings 返回权威设置快照。
func (c *client) Settings() api.Settings {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	return c.settings
}

// SaveSettings 持久化设置。
func (c *client) SaveSettings(s api.Settings) error {
	if err := c.call(context.Background(), api.MethodSaveSettings, &s, &api.Empty{}); err != nil {
		return err
	}
	c.settingsMu.Lock()
	c.settings = s
	c.settingsMu.Unlock()
	return nil
}

// Close 关闭连接。
// Close 关闭连接：停掉读循环、释放底层传输，并触发一次 onClose 通知。
// 可以重复调用。
func (c *client) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	if c.tr != nil {
		_ = c.tr.Close()
	}
	c.notifyClose()
	return nil
}
```

- [ ] **Step 7: 写失败测试（共享客户端）**

创建 `internal/rpc/client_test.go`：

```go
package rpc

import (
	"context"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
)

// fakeTransport 是用于测试共享客户端行为的假传输。
type fakeTransport struct {
	calls   []string
	events  chan api.Message
	failErr error
	closed  bool
}

func (f *fakeTransport) Close() error {
	f.closed = true
	return nil
}

func (f *fakeTransport) Call(ctx context.Context, method string, in, out any) error {
	f.calls = append(f.calls, method)
	if f.failErr != nil {
		return f.failErr
	}
	switch method {
	case api.MethodGetSettings:
		s := out.(*api.Settings)
		s.Language = "en"
	case api.MethodList:
		r := out.(*api.ListResult)
		r.Tasks = []*api.Task{{ID: "t1"}}
	case api.MethodIsPreparing:
		b := out.(*api.BoolResult)
		b.Value = true
	}
	return nil
}

func (f *fakeTransport) Stream(ctx context.Context) (<-chan api.Message, error) {
	return f.events, nil
}

func TestClientList(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := c.List(api.FilterAll, api.SortCreatedDesc)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t1" {
		t.Fatalf("列表结果不对: %+v", tasks)
	}
	if c.Settings().Language != "en" {
		t.Fatalf("握手设置不对: %+v", c.Settings())
	}
	// 握手恰好发一次 get_settings，随后才是 list。
	if len(tr.calls) != 2 || tr.calls[0] != api.MethodGetSettings || tr.calls[1] != api.MethodList {
		t.Fatalf("调用序列不对: %v", tr.calls)
	}
}

func TestClientCloseClosesTransport(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !tr.closed {
		t.Fatal("Close 应当关闭底层传输")
	}
}

func TestClientLateShowAddTaskReplay(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	// 回调还没注册时到达的请求不能丢——业务进程的 lgom:// 转发可能
	// 在 UI 子进程还在启动时就发过来了。
	tr.events <- api.Message{
		Kind:        api.MessageKindShowAddTask,
		ShowAddTask: &api.ShowAddTaskParams{URL: "https://x/a"},
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.hookMu.Lock()
		n := len(c.showAddTaskQ)
		c.hookMu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("消息没有进队列")
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := make(chan api.ShowAddTaskParams, 1)
	c.SetOnShowAddTask(func(p api.ShowAddTaskParams) { got <- p })
	select {
	case p := <-got:
		if p.URL != "https://x/a" {
			t.Fatalf("补发的参数不对: %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("迟到的 show_add_task 没有被补发")
	}
}

func TestClientSubscribeAndBroadcast(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	ch, unsub := c.Subscribe()
	tr.events <- api.Message{Kind: api.MessageKindEvent, Event: &api.Event{Why: "progress"}}
	select {
	case ev := <-ch:
		if ev.Why != "progress" {
			t.Fatalf("事件不对: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("没有收到事件")
	}
	unsub()
}

func TestClientControlHooks(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	shown := make(chan struct{}, 1)
	c.SetOnShow(func() { shown <- struct{}{} })
	tr.events <- api.Message{Kind: api.MessageKindShow}
	select {
	case <-shown:
	case <-time.After(2 * time.Second):
		t.Fatal("show 回调没有触发")
	}
}

func TestClientHandshakeFailure(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message), failErr: context.Canceled}
	if _, err := newClient(context.Background(), tr); err == nil {
		t.Fatal("握手失败时应当报错")
	}
}

func TestClientCloseNotifiesHook(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{}, 1)
	c.SetOnClose(func() { closed <- struct{}{} })
	close(tr.events) // 模拟消息流结束
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("连接断开没有触发 onClose")
	}
}

func TestClientCloseBeforeHookRegistration(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	close(tr.events)
	time.Sleep(100 * time.Millisecond)
	closed := make(chan struct{}, 1)
	c.SetOnClose(func() { closed <- struct{}{} })
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("断开之后才注册的 hook 应当被立刻补发")
	}
}

func TestClientUnsubscribeThenBroadcast(t *testing.T) {
	tr := &fakeTransport{events: make(chan api.Message, 4)}
	c, err := newClient(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	_, unsub := c.Subscribe()
	unsub()
	// 退订之后再广播不能 panic（向已关闭的通道发送会 panic）。
	tr.events <- api.Message{Kind: api.MessageKindEvent, Event: &api.Event{Why: "x"}}
	time.Sleep(100 * time.Millisecond)
}
```

- [ ] **Step 8: 跑测试**

Run: `go test ./internal/rpc/...`
Expected: PASS —— 本任务不引入任何传输实现，仓库保持全绿

- [ ] **Step 9: 提交**

```bash
git add internal/rpc
git commit -m "feat(rpc): 新增传输抽象层、共享客户端与地址解析"
```

> 说明：`Serve` / `Dial` 注册表放在 Task 7（grpcx 落地时）一并提供——它要引用
> 两种传输实现，提前写会让 Task 4 到 Task 7 之间仓库编译不过，
> 与「每个任务结束时仓库全绿」的全局约束冲突。

---

### Task 5: 实现 `internal/rpc/httpx`（net/http 传输）

**Files:**
- Create: `internal/rpc/httpx/client.go`
- Create: `internal/rpc/httpx/server.go`
- Create: `internal/rpc/httpx/httpx_test.go`（`package httpx`）
- Create: `internal/rpc/httpx/contract_test.go`（`package httpx_test`，编译期断言）
- Modify: `internal/rpc/addr/addr.go`（补 `DialContext`）
- Modify: `internal/rpc/addr/addr_test.go`（补 `DialContext` 的测试）

**Interfaces:**
- Consumes: `internal/api`、`internal/rpc/addr` + 标准库。
  **不得** import `internal/rpc`（会形成 `rpc → httpx → rpc` 的包环），
  也不得 import `internal/store`、`internal/scheduler`、`internal/engine`、
  `internal/ui`、`internal/uimgr`、`internal/appservice`。
- Produces:
  ```go
  package httpx
  type Client struct { /* ... */ }
  func Dial(ctx context.Context, a addr.Addr, token string) (*Client, error)
  func Serve(ctx context.Context, a addr.Addr, token string, h Handler) error
  // Handler 是 httpx 内部定义的最小接口（方法集与 rpc.Handler 逐字一致，
  // 靠结构化类型与它对接）：
  //   interface { Call(ctx, method string, in, out any) error
  //               Events() (<-chan api.Event, func())
  //               Controls() <-chan api.Message }
  ```

> 线格式：REST 风格路径 `/lgdm.v1.DownloadService/{method}` + JSON 正文；
> 消息流用 NDJSON 逐行推送；每次请求都带 `Authorization: Bearer <token>`，
> 服务端每次调用强制校验；只接受 POST。
>
> 服务端可能返回的状态码：`200` 成功、`400` 请求体解析失败（含超限）、
> `401` token 缺失或无效、`404` 未知路径、`405` 非 POST 方法、`500` 业务侧调用失败。
> 除 `200` 外一律是 `{"ok":false,"error":"..."}` 形状，客户端按 `error` 字段解析。
> （另有 ServeMux 自己的 `307`：请求路径带 `//` 或 `..` 时它先做路径规范化重定向，
> 发生在任何 handler 之前，不走鉴权也不是 JSON 形状。）
>
> 未注册的路径由兜底路由 `"/"` 兜住：它先校验 token（不过就回 `401`），过了才回
> `404`「未知路径 `<path>`」。这样没 token 的调用方分不出「端点不存在」与「token 不对」，
> `401` 与 `404` 的差别不会泄露端点存不存在；错误体形状也与业务端点一致，
> 客户端不会退化成裸串「HTTP 404」。
>
> 文档端点（`/openapi.yaml`、`/openapi.json`、`/docs`）按设计不参与 token 校验：
> Go 1.22+ 的 ServeMux 里未以 `/` 结尾的 pattern 是精确匹配、`"/"` 是兜底子树，
> 更精确的 pattern 优先命中，因此兜底路由不会把它们圈进去。

> **与控制器补充说明（A1–A11）的对照**：本节代码是按 A1–A11 修正、再经修复轮 1
> 补上兜底路由与零值客户端保护后的最终实现，与最初的草稿有出入的地方见文末
> 「与初稿的差异」。

- [ ] **Step 1: 给 `internal/rpc/addr` 补 `DialContext`**

传输层要用请求自己的 ctx 建连（A2、A3），addr 包是叶子包，加这个函数不破坏
任何依赖边界。`Dial` 保留，改成薄封装以兼容已有调用方。

```go
// Dial 按地址建立连接。它不感知 ctx，等价于用 context.Background() 调用
// DialContext——保留它是为了兼容已有的调用方（gRPC 传输等）。
func Dial(a Addr) (net.Conn, error) {
	return DialContext(context.Background(), a)
}

// DialContext 按地址建立连接，ctx 取消会中断连接过程。
//
// 传输层要用请求自己的 ctx 建连：一次超时的调用不该留下一个还在建连的
// socket goroutine。
func DialContext(ctx context.Context, a Addr) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, a.Network, a.Address)
	if err != nil {
		return nil, fmt.Errorf("addr: dial %s %s: %w", a.Network, a.Address, err)
	}
	return conn, nil
}
```

- [ ] **Step 2: 建 `internal/rpc/httpx/client.go`**

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package httpx 是基于标准库 net/http 的传输实现：REST 风格路径 +
// JSON 正文，消息流用 NDJSON 逐行推送。
//
// 只用 net/http，不接 x/net/http2 的 h2c 手工接线——传输层跑在本机
// socket 上，不需要 HTTP/2 的多路复用。
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"sync"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
)

// servicePrefix 是所有业务端点的路径前缀。
const servicePrefix = "/lgdm.v1.DownloadService/"

// maxErrorBody 是错误响应体最多读取的字节数。
const maxErrorBody = 4096

// streamBuffer 是消息流 channel 的缓冲条数。
const streamBuffer = 64

// Client 是 net/http 传输的客户端。
type Client struct {
	base  string
	token string
	http  *http.Client

	// transport 是底层的 http.Transport，Close 时用来掐断空闲连接。
	transport *http.Transport

	// mu 保护 closed 与 streams。
	mu     sync.Mutex
	closed bool
	// streams 是当前活动消息流。Close 靠它们把流停掉。
	streams map[*stream]struct{}

	closeOnce sync.Once
}

// stream 是一次活动消息流的状态。cancel 停掉对应的 HTTP 请求。
type stream struct {
	cancel context.CancelFunc
}

// Dial 按地址建立 HTTP 客户端。token 随每次请求发送。
//
// Dial 会用 ctx 预连接一次再立刻断开。HTTP 是惰性连接——不预连接的话
// 「业务进程没起来」要等到第一次调用才暴露，调用方只会看到一个含义模糊的
// 握手失败。预连接让失败时机与 gRPC 传输一致。
func Dial(ctx context.Context, a addr.Addr, token string) (*Client, error) {
	conn, err := addr.DialContext(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("httpx: 连接 %s 失败: %w", a.Address, err)
	}
	_ = conn.Close()

	host := a.Address
	if a.Network == "unix" {
		// unix socket 没有主机名，用一个占位主机名即可。
		host = "lgdm"
	}
	tr := &http.Transport{
		// 用请求自己的 ctx 建连：一次超时的调用不该留下还在建连的 socket。
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return addr.DialContext(ctx, a)
		},
	}
	return &Client{
		base:      "http://" + host + servicePrefix,
		token:     token,
		http:      &http.Client{Transport: tr},
		transport: tr,
	}, nil
}

// Call 执行一次调用。
//
// 调用前先按 api.Spec 校验入参与出参的 Go 类型。HTTP 走 JSON，类型信息在
// 编码时被擦掉：入参类型不对时，服务端照样会按规格解码出一个零值对象；
// 出参类型不对时，服务端的响应会被解码进一个形状不合的对象里。两种情况
// 都是「静默成功」——调用方看不出自己传错了类型，错误要到业务逻辑不对的
// 时候才暴露。gRPC 侧靠 Go 类型断言当场报错（grpcx.asPtr），这里在序列化
// 之前补上同样的检查，让两种传输的行为一致。
func (c *Client) Call(ctx context.Context, method string, in, out any) error {
	if err := c.checkOpen(); err != nil {
		return err
	}
	if err := checkCallTypes(method, in, out); err != nil {
		return err
	}
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("httpx: 编码 %s 请求: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("httpx: 构造 %s 请求: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("httpx: %s 请求失败: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return fmt.Errorf("httpx: %s: %s", method, describeError(resp.StatusCode, raw))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("httpx: 解码 %s 响应: %w", method, err)
	}
	return nil
}

// checkCallTypes 按 api.Spec 校验入参与出参的 Go 类型。
//
// 错误信息带方法名、期望类型与实际类型，方便直接定位是哪个方法的哪个
// 参数传错了。
//
// 两个边界都是有意这么定的：
//
//   - out == nil 表示「调用方不关心结果」，不是「传错了类型」。调用照常
//     发出，响应体读了就扔（Call 里 out == nil 的提前返回分支）。没有类型
//     可比，所以不做校验。
//   - 未知方法（api.Spec 返回 nil）没有规格可比对，这里**不**报错，放行
//     给服务端判——服务端的兜底路由会回 404「未知路径」，错误照样传回
//     调用方。路由的真相在服务端，客户端不替它猜。
func checkCallTypes(method string, in, out any) error {
	spec := api.Spec(method)
	if spec == nil || spec.NewIn == nil || spec.NewOut == nil {
		// 未知方法：没有规格可比对，放行给服务端判（它会回 404）。
		return nil
	}
	wantIn := reflect.TypeOf(spec.NewIn())
	if reflect.TypeOf(in) != wantIn {
		return fmt.Errorf("httpx: %s: 请求类型错误: 期望 %s, 实际 %T", method, wantIn, in)
	}
	if out == nil {
		return nil
	}
	wantOut := reflect.TypeOf(spec.NewOut())
	if reflect.TypeOf(out) != wantOut {
		return fmt.Errorf("httpx: %s: 响应类型错误: 期望 %s, 实际 %T", method, wantOut, out)
	}
	return nil
}

// Stream 打开消息流。响应是 NDJSON，每行一个 api.Message。
func (c *Client) Stream(ctx context.Context) (<-chan api.Message, error) {
	if err := c.checkOpen(); err != nil {
		return nil, err
	}
	// 流的 ctx 单独派生：Close 要能只停掉流而不影响调用方自己的 ctx。
	streamCtx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(streamCtx, http.MethodPost, c.base+api.MethodStreamMessages, nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("httpx: 构造消息流请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/x-ndjson")
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("httpx: 打开消息流: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("httpx: 打开消息流: %s", describeError(resp.StatusCode, raw))
	}

	st := &stream{cancel: cancel}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		resp.Body.Close()
		return nil, errors.New("httpx: 客户端已关闭")
	}
	if c.streams == nil {
		c.streams = make(map[*stream]struct{})
	}
	c.streams[st] = struct{}{}
	c.mu.Unlock()

	out := make(chan api.Message, streamBuffer)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		defer c.clearStream(st)
		defer cancel()
		dec := json.NewDecoder(resp.Body)
		for {
			var msg api.Message
			if err := dec.Decode(&msg); err != nil {
				return
			}
			select {
			case out <- msg:
			case <-streamCtx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Close 释放底层连接与流。必须幂等——Close 可能被重复调用。
//
// 除了掐断空闲连接，还要取消活动消息流：正在跑的 NDJSON 响应不会因为
// 连接池被清空而结束，读循环会一直挂着。
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		sts := make([]*stream, 0, len(c.streams))
		for st := range c.streams {
			sts = append(sts, st)
		}
		c.streams = nil
		c.mu.Unlock()
		for _, st := range sts {
			st.cancel()
		}
		if c.transport != nil {
			c.transport.CloseIdleConnections()
		}
	})
	return nil
}

// checkOpen 检查客户端是否已关闭或未初始化。
func (c *Client) checkOpen() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("httpx: 客户端已关闭")
	}
	// 零值客户端的 http 字段是 nil，直接 Do 会 nil 指针解引用 panic。
	// Client 是导出类型，零值是被允许的构造物，这里要回错误而不是炸进程。
	if c.http == nil {
		return errors.New("httpx: 客户端未初始化，请用 Dial 创建")
	}
	return nil
}

// clearStream 清掉已结束的活动流，避免 Close 去取消一个已经结束的流。
func (c *Client) clearStream(st *stream) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.streams, st)
}

// describeError 把错误响应体翻译成一句人话。服务端的错误体形状固定为
// {"ok":false,"error":"..."}，这里按 error 字段解析；解析不出来就退回
// HTTP 状态码。
func describeError(status int, raw []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	return fmt.Sprintf("HTTP %d", status)
}
```

- [ ] **Step 3: 建 `internal/rpc/httpx/server.go`**

```go
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
	// 知道某个端点存不存在。没注册的路径由下面的兜底路由兜住，它同样先过
	// 鉴权，这条前提才成立。
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
	// 兜底路由：未注册的路径也要先过鉴权。否则 401 与 404 的差别会泄露
	// 端点存不存在——没 token 的调用方能靠状态码枚举出有哪些端点；而且
	// ServeMux 内置的 404 是纯文本，错误体形状与业务端点对不上，客户端
	// 只能退化成裸串「HTTP 404」，错误原因丢失。
	//
	// Go 1.22+ 的 ServeMux：未以 / 结尾的 pattern 是精确匹配，"/" 是兜底
	// 子树，更精确的 pattern 优先命中。业务端点与文档端点都不受影响。
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !validToken(r.Header.Get("Authorization"), token) {
			writeError(w, http.StatusUnauthorized, "无效的 token")
			return
		}
		// 不检查方法：对不存在的路径谈「只接受 POST」是误导。
		writeError(w, http.StatusNotFound, "未知路径 "+r.URL.Path)
	})
	return mux
}

// serveCall 处理一次调用。
func serveCall(w http.ResponseWriter, r *http.Request, h Handler, method string) {
	// method 一定来自 api.AllMethods，api.Spec 对它们必有 spec。
	spec := api.Spec(method)
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
```

- [ ] **Step 4: 写测试**

`internal/rpc/httpx/httpx_test.go`（`package httpx`）：

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package httpx

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
)

// stubHandler 是最小业务侧实现。
type stubHandler struct {
	events   chan api.Event
	controls chan api.Message

	// unsubed 在退订时被关闭，用来观察服务端有没有收手。
	unsubed   chan struct{}
	unsubOnce sync.Once
}

func (s *stubHandler) Call(ctx context.Context, method string, in, out any) error {
	switch method {
	case api.MethodGetSettings:
		out.(*api.Settings).Language = "en"
	case api.MethodList:
		out.(*api.ListResult).Tasks = []*api.Task{{ID: "t1"}}
	case api.MethodIsPreparing:
		out.(*api.BoolResult).Value = true
	case api.MethodAdd:
		t := out.(*api.Task)
		t.ID = "new"
		t.URL = in.(*api.AddTaskInput).URL
	}
	return nil
}

func (s *stubHandler) Events() (<-chan api.Event, func()) {
	return s.events, func() {
		s.unsubOnce.Do(func() {
			if s.unsubed != nil {
				close(s.unsubed)
			}
		})
	}
}
func (s *stubHandler) Controls() <-chan api.Message { return s.controls }

// startServer 在临时目录里的 unix socket 上启动服务，返回监听地址。
func startServer(t *testing.T, h Handler, token string) addr.Addr {
	t.Helper()
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "lgdm.sock")}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = Serve(ctx, a, token, h) }()
	// socket 文件在 net.Listen 绑定成功时创建，文件出现即表示能连。
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(a.Address); err == nil {
			return a
		}
		if time.Now().After(deadline) {
			t.Fatalf("服务没有在超时前就绪: %v", a)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestValidToken(t *testing.T) {
	if !validToken("Bearer secret", "secret") {
		t.Fatal("正确的 token 应当通过")
	}
	if validToken("Bearer wrong", "secret") {
		t.Fatal("错误的 token 应当被拒")
	}
	if validToken("secret", "secret") {
		t.Fatal("缺少 Bearer 前缀应当被拒")
	}
	if validToken("Bearer ", "secret") {
		t.Fatal("空 token 应当被拒")
	}
}

func TestServeCallAndAuth(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	mux := newMux(h, "secret")

	// 无 token → 401
	req := httptest.NewRequest(http.MethodPost, servicePrefix+api.MethodGetSettings, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("缺 token 应当 401, got %d", rec.Code)
	}

	// 错误 token → 401
	req = httptest.NewRequest(http.MethodPost, servicePrefix+api.MethodGetSettings, nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误 token 应当 401, got %d", rec.Code)
	}

	// 带 token → 200 + 设置
	req = httptest.NewRequest(http.MethodPost, servicePrefix+api.MethodGetSettings, nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("应当 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var s api.Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Language != "en" {
		t.Fatalf("设置不对: %+v", s)
	}
}

// TestServeRejectsInvalidJSON 验证坏请求体返回 400。
func TestServeRejectsInvalidJSON(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	mux := newMux(h, "secret")

	req := httptest.NewRequest(http.MethodPost, servicePrefix+api.MethodList, strings.NewReader("{"))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("坏 JSON 应当 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var e struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("错误体不是 JSON: %v (%s)", err, rec.Body.String())
	}
	if e.OK || e.Error == "" {
		t.Fatalf("错误体形状不对: %s", rec.Body.String())
	}
}

// TestServeUnknownMethod 验证 api.AllMethods 之外的方法返回 404，且错误体
// 形状与业务端点一致（{"ok":false,"error":"..."}）。
func TestServeUnknownMethod(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	mux := newMux(h, "secret")

	req := httptest.NewRequest(http.MethodPost, servicePrefix+"nope", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知方法应当 404, got %d", rec.Code)
	}
	assertErrorBody(t, rec, "未知路径")
}

// TestServeUnknownPathNeedsToken 验证未注册的路径也要过鉴权：没 token 时回
// 401，而不是 ServeMux 内置的纯文本 404。
//
// 这一条守住「不该让没 token 的调用方知道某个端点存不存在」：没有它，
// 401 与 404 的差别会让任何调用方枚举出端点。
func TestServeUnknownPathNeedsToken(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	mux := newMux(h, "secret")

	// 完全不带 token。
	req := httptest.NewRequest(http.MethodPost, "/nope", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未知路径不带 token 应当 401, got %d: %s", rec.Code, rec.Body.String())
	}
	assertErrorBody(t, rec, "无效的 token")

	// 带错误的 token。
	req = httptest.NewRequest(http.MethodPost, "/nope", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未知路径带错 token 应当 401, got %d: %s", rec.Code, rec.Body.String())
	}
	assertErrorBody(t, rec, "无效的 token")
}

// TestServeUnknownPathWithToken 验证带对 token 时未知路径回 404，且错误体
// 是统一的 JSON 形状。
func TestServeUnknownPathWithToken(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	mux := newMux(h, "secret")

	req := httptest.NewRequest(http.MethodPost, "/nope", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知路径带对 token 应当 404, got %d: %s", rec.Code, rec.Body.String())
	}
	assertErrorBody(t, rec, "未知路径 /nope")
}

// TestServeFallbackRouteKeepsDocsEndpointsOpen 验证兜底路由没有把文档端点
// 圈进去：/openapi.yaml、/openapi.json、/docs 仍按设计不参与 token 校验。
//
// 兜底路由是 "/"（兜底子树），文档端点是更精确的 pattern，更精确的优先
// 命中。这条测试守着这个前提——哪天有人把兜底路由改成「所有请求都先过
// 鉴权」的整体包装，文档页就跟着挂。
func TestServeFallbackRouteKeepsDocsEndpointsOpen(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	mux := newMux(h, "secret")

	for _, path := range []string{"/openapi.yaml", "/openapi.json", "/docs"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 不带 token 应当 200, got %d", path, rec.Code)
		}
	}
}

// TestServeRejectsNonPost 验证只接受 POST。
func TestServeRejectsNonPost(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	mux := newMux(h, "secret")

	req := httptest.NewRequest(http.MethodGet, servicePrefix+api.MethodGetSettings, nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应当 405, got %d", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow 头不对: %q", got)
	}
}

// TestServeStream 用真实 HTTP 服务验证 NDJSON 逐行的线格式。
//
// 这里不用 httptest.NewRecorder：它的 Body 是 *bytes.Buffer，不是并发安全的，
// 在 handler 还在写的时候读会触发数据竞争。
func TestServeStream(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event, 4), controls: make(chan api.Message, 4)}
	srv := httptest.NewServer(newMux(h, "secret"))
	defer srv.Close()

	resp := openStream(t, srv.URL)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应当 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("Content-Type 不对: %q", ct)
	}

	sc := bufio.NewScanner(resp.Body)

	// 先推事件，客户端读到之后再推控制消息——两个通道同时可读时服务端
	// 选中哪个是不确定的，测试不能依赖顺序。
	h.events <- api.Event{Why: "progress"}
	msg := scanMessage(t, sc)
	if msg.Kind != api.MessageKindEvent || msg.Event == nil {
		t.Fatalf("事件消息不对: %+v", msg)
	}
	if msg.Event.Why != "progress" {
		t.Fatalf("事件内容不对: %+v", msg.Event)
	}

	h.controls <- api.Message{Kind: api.MessageKindShow}
	msg = scanMessage(t, sc)
	if msg.Kind != api.MessageKindShow {
		t.Fatalf("控制消息不对: %+v", msg)
	}
}

// TestServeStreamContinuesAfterEventsClosed 验证事件流关闭后控制消息仍然
// 推送（事件通道结束不该顺带掐掉控制通道）。
func TestServeStreamContinuesAfterEventsClosed(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message, 4)}
	close(h.events)
	srv := httptest.NewServer(newMux(h, "secret"))
	defer srv.Close()

	resp := openStream(t, srv.URL)
	defer resp.Body.Close()

	sc := bufio.NewScanner(resp.Body)
	h.controls <- api.Message{Kind: api.MessageKindShow}
	msg := scanMessage(t, sc)
	if msg.Kind != api.MessageKindShow {
		t.Fatalf("控制消息不对: %+v", msg)
	}
}

// TestServeStreamStopsOnClientDisconnect 验证客户端断开后服务端收手：
// 请求 ctx 取消后消息流处理器要退出并退订事件。
func TestServeStreamStopsOnClientDisconnect(t *testing.T) {
	h := &stubHandler{
		events:   make(chan api.Event),
		controls: make(chan api.Message),
		unsubed:  make(chan struct{}),
	}
	srv := httptest.NewServer(newMux(h, "secret"))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+servicePrefix+api.MethodStreamMessages, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// 断开连接：服务端的请求 ctx 随之取消。
	cancel()
	resp.Body.Close()

	select {
	case <-h.unsubed:
	case <-time.After(5 * time.Second):
		t.Fatal("客户端断开后服务端没有退出消息流")
	}
}

// TestClientCallRoundTrip 用真实服务验证一次调用的往返。
func TestClientCallRoundTrip(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	a := startServer(t, h, "secret")

	ctx := context.Background()
	c, err := Dial(ctx, a, "secret")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	out := &api.Settings{}
	if err := c.Call(ctx, api.MethodGetSettings, &api.Empty{}, out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.Language != "en" {
		t.Fatalf("设置不对: %+v", out)
	}

	list := &api.ListResult{}
	if err := c.Call(ctx, api.MethodList, &api.ListParams{}, list); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(list.Tasks) != 1 || list.Tasks[0].ID != "t1" {
		t.Fatalf("列表不对: %+v", list.Tasks)
	}
}

// TestClientCallRejectsBadToken 验证 token 不对时调用失败。
func TestClientCallRejectsBadToken(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	a := startServer(t, h, "secret")

	c, err := Dial(context.Background(), a, "wrong")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	err = c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{})
	if err == nil {
		t.Fatal("错误 token 应当调用失败")
	}
	if !strings.Contains(err.Error(), "无效的 token") {
		t.Fatalf("错误信息应当带上服务端的原因: %v", err)
	}
}

// TestClientCallUnknownMethodKeepsErrorReason 验证客户端能从 404 的响应体
// 里读出错误原因。服务端的错误体形状固定为 {"ok":false,"error":"..."}，
// 客户端按 error 字段解析——解析不出来就退化成裸串「HTTP 404」。
func TestClientCallUnknownMethodKeepsErrorReason(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	a := startServer(t, h, "secret")

	c, err := Dial(context.Background(), a, "secret")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	err = c.Call(context.Background(), "nope", &api.Empty{}, &api.Empty{})
	if err == nil {
		t.Fatal("未知方法应当调用失败")
	}
	if !strings.Contains(err.Error(), "未知路径") {
		t.Fatalf("错误信息应当带上服务端的原因: %v", err)
	}
	if strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("错误原因不该退化成裸状态码: %v", err)
	}
}

// countingRoundTripper 记录自己被调用的次数。
//
// 它是「假」的：任何走到这里的请求都直接失败。类型校验如果漏了，测试会
// 同时看到「发了请求」与「错误信息不是类型错误」两个信号。
type countingRoundTripper struct {
	calls atomic.Int64
}

// RoundTrip 记录一次调用并返回错误。
func (rt *countingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	rt.calls.Add(1)
	return nil, errors.New("不该发出请求")
}

// wrongCallType 是一个不属于任何方法规格的类型，用来模拟「调用方传错了
// Go 类型」。它的字段刻意为空：只要 Go 类型不是规格里的那个，就算错。
type wrongCallType struct{}

// TestCallTypeMismatchDoesNotSendRequest 验证类型校验失败时客户端压根不发
// 请求。
//
// 这是 httpx 侧的护栏。JSON 会把类型信息擦掉：入参类型不对时，服务端照样
// 会按规格解码出一个零值对象；出参类型不对时，服务端的响应会被解码进一个
// 形状不合的对象里。两种情况都是「静默成功」，调用方看不出自己传错了
// 类型。所以校验必须发生在序列化之前——请求一旦发出去，错误就被吞掉了。
//
// 断言两件事：错误里带着方法名（方便定位是哪个方法传错了），以及假的
// RoundTripper 一次都没被调用。
func TestCallTypeMismatchDoesNotSendRequest(t *testing.T) {
	rt := &countingRoundTripper{}
	c := &Client{http: &http.Client{Transport: rt}}
	ctx := context.Background()

	for _, m := range api.AllMethods {
		t.Run(m, func(t *testing.T) {
			spec := api.Spec(m)
			if spec == nil {
				t.Fatalf("方法 %q 缺少规格", m)
			}

			// 入参类型不对（出参给对）：必须报「请求类型错误」。
			err := c.Call(ctx, m, &wrongCallType{}, spec.NewOut())
			if err == nil {
				t.Fatal("入参类型不对应当报错")
			}
			if !strings.Contains(err.Error(), "请求类型错误") {
				t.Fatalf("应当报请求类型错误: %v", err)
			}
			if !strings.Contains(err.Error(), m) {
				t.Fatalf("错误信息应当带上方法名 %q: %v", m, err)
			}

			// 出参类型不对（入参给对）：必须报「响应类型错误」。
			err = c.Call(ctx, m, spec.NewIn(), &wrongCallType{})
			if err == nil {
				t.Fatal("出参类型不对应当报错")
			}
			if !strings.Contains(err.Error(), "响应类型错误") {
				t.Fatalf("应当报响应类型错误: %v", err)
			}
			if !strings.Contains(err.Error(), m) {
				t.Fatalf("错误信息应当带上方法名 %q: %v", m, err)
			}
		})
	}

	if n := rt.calls.Load(); n != 0 {
		t.Fatalf("类型校验失败时不该发出请求，实际发了 %d 次", n)
	}
}

// TestCallUnknownMethodDoesNotPanic 验证未知方法返回错误而不是 panic。
//
// api.Spec 对未知方法返回 nil，校验逻辑不能因此解引用 nil。未知方法由
// 服务端判（兜底路由回 404），这里用假 RoundTripper 代替服务端：客户端
// 要么报错，要么把请求发出去让服务端拒——两种都不许 panic。
func TestCallUnknownMethodDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("未知方法不该 panic: %v", r)
		}
	}()

	rt := &countingRoundTripper{}
	c := &Client{http: &http.Client{Transport: rt}}
	err := c.Call(context.Background(), "no_such_method", &api.Empty{}, &api.Empty{})
	if err == nil {
		t.Fatal("未知方法应当返回错误")
	}
	if !strings.Contains(err.Error(), "no_such_method") {
		t.Fatalf("错误信息应当带上方法名: %v", err)
	}
}

// TestZeroValueClientReturnsError 验证零值客户端的 Call 与 Stream 返回错误
// 而不是 panic。
//
// Client 是导出类型，零值是被允许的构造物（contract_test.go 就用它验证
// Close 不炸）。零值客户端的 http 字段是 nil，直接 Do 会 nil 指针解引用。
func TestZeroValueClientReturnsError(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("零值客户端不该 panic: %v", r)
		}
	}()

	c := &Client{}
	err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{})
	if err == nil {
		t.Fatal("零值客户端的 Call 应当返回错误")
	}
	if !strings.Contains(err.Error(), "未初始化") {
		t.Fatalf("错误信息应当说明客户端未初始化: %v", err)
	}

	ch, err := c.Stream(context.Background())
	if err == nil {
		t.Fatal("零值客户端的 Stream 应当返回错误")
	}
	if !strings.Contains(err.Error(), "未初始化") {
		t.Fatalf("错误信息应当说明客户端未初始化: %v", err)
	}
	if ch != nil {
		t.Fatal("零值客户端的 Stream 不该返回消息通道")
	}
}

// TestClientStreamReceivesMessages 验证客户端能收到事件与控制消息两种。
func TestClientStreamReceivesMessages(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event, 4), controls: make(chan api.Message, 4)}
	a := startServer(t, h, "secret")

	c, err := Dial(context.Background(), a, "secret")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	ch, err := c.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	h.events <- api.Event{Why: "progress", SpeedBPS: 10}
	msg := recvMessage(t, ch)
	if msg.Kind != api.MessageKindEvent || msg.Event == nil {
		t.Fatalf("事件消息不对: %+v", msg)
	}
	if msg.Event.Why != "progress" || msg.Event.SpeedBPS != 10 {
		t.Fatalf("事件内容不对: %+v", msg.Event)
	}

	h.controls <- api.Message{Kind: api.MessageKindShow}
	msg = recvMessage(t, ch)
	if msg.Kind != api.MessageKindShow {
		t.Fatalf("控制消息不对: %+v", msg)
	}
}

// TestClientCloseIsIdempotent 验证 Close 可以重复调用。
func TestClientCloseIsIdempotent(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	a := startServer(t, h, "secret")

	c, err := Dial(context.Background(), a, "secret")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("第一次 Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("第二次 Close: %v", err)
	}
}

// TestClientCloseStopsStream 验证 Close 能停掉在跑的 NDJSON 流——只关空闲
// 连接是不够的，读循环会一直挂着。
func TestClientCloseStopsStream(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	a := startServer(t, h, "secret")

	c, err := Dial(context.Background(), a, "secret")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	ch, err := c.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("Close 之后不该再收到消息")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close 之后消息流没有结束")
	}
}

// TestDialFailsWhenServerAbsent 验证服务端没起来时 Dial 直接失败，而不是
// 等到第一次调用才炸。
func TestDialFailsWhenServerAbsent(t *testing.T) {
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "missing.sock")}
	if _, err := Dial(context.Background(), a, "secret"); err == nil {
		t.Fatal("服务端没起来时 Dial 应当失败")
	}
}

// assertErrorBody 断言响应是统一的错误信封：Content-Type 是 application/json，
// body 是 {"ok":false,"error":"..."}。wantContains 非空时，error 字段还要包含
// 它。
func assertErrorBody(t *testing.T, rec *httptest.ResponseRecorder, wantContains string) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("错误体的 Content-Type 不对: %q", ct)
	}
	var e struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("错误体不是 JSON: %v (%s)", err, rec.Body.String())
	}
	if e.OK {
		t.Fatalf("错误体的 ok 应当是 false: %s", rec.Body.String())
	}
	if e.Error == "" {
		t.Fatalf("错误体缺少 error 字段: %s", rec.Body.String())
	}
	if wantContains != "" && !strings.Contains(e.Error, wantContains) {
		t.Fatalf("错误信息应当包含 %q，实际是 %q", wantContains, e.Error)
	}
}

// openStream 打开一条消息流。
func openStream(t *testing.T, base string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+servicePrefix+api.MethodStreamMessages, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Accept", "application/x-ndjson")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// scanMessage 从 NDJSON 响应里读一行并解析成 api.Message。
func scanMessage(t *testing.T, sc *bufio.Scanner) api.Message {
	t.Helper()
	if !sc.Scan() {
		t.Fatalf("消息流没有输出: %v", sc.Err())
	}
	var msg api.Message
	if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
		t.Fatalf("消息不是合法 JSON: %v (%s)", err, sc.Text())
	}
	return msg
}

// recvMessage 从客户端的消息流里收一条消息。
func recvMessage(t *testing.T, ch <-chan api.Message) api.Message {
	t.Helper()
	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("消息流提前结束")
		}
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("没有收到消息")
	}
	return api.Message{}
}
```

`internal/rpc/httpx/contract_test.go`（`package httpx_test`）：外部测试包可以
import 被测包的上层包，Go 不会因此成环（A6、A7）。

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// 这是外部测试包：httpx 自己不能 import internal/rpc（会形成
// rpc → httpx → rpc 的包环），但外部测试包可以 import 上层包，Go 不会
// 因此成环。用它在这里就把「httpx.Client 满足 rpc.Transport」这件事钉死，
// 不用等到注册表落地时才发现签名对不上。
package httpx_test

import (
	"testing"

	"lgo_download_manager/internal/rpc"
	"lgo_download_manager/internal/rpc/httpx"
)

// 编译期断言。
var (
	// httpx.Client 满足 rpc.Transport。
	_ rpc.Transport = (*httpx.Client)(nil)
	// rpc.Handler 的方法集与 httpx.Handler 结构兼容：业务侧拿 rpc.Handler
	// 的实现就能直接交给 httpx.Serve。方向是 rpc.Handler → httpx.Handler。
	_ httpx.Handler = (rpc.Handler)(nil)
)

// TestTransportContract 是上面断言的运行时版本：接口方法集对得上，零值
// 客户端的 Close 也不会炸。
func TestTransportContract(t *testing.T) {
	var tr rpc.Transport = &httpx.Client{}
	if err := tr.Close(); err != nil {
		t.Fatalf("零值客户端 Close 应当返回 nil: %v", err)
	}
}
```

- [ ] **Step 5: 跑测试确认通过**

Run:
```bash
gofmt -l internal/rpc/httpx internal/rpc/addr   # 必须无输出
go build ./... && go vet ./... && go test ./...
go test -race ./internal/rpc/...
```
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/rpc/httpx internal/rpc/addr
git commit -m "feat(httpx): 实现基于 net/http 的传输"
```

#### 与初稿的差异（A1–A12 与修复轮 1、2 的落法）

| 条目 | 初稿的问题 | 最终实现 |
| --- | --- | --- |
| A1 | `Dial` 的 ctx 是死参数，HTTP 惰性连接让「业务进程没起来」拖到第一次调用才暴露 | `Dial` 用 ctx 预连接一次再立刻断开，失败直接返回错误 |
| A2 | `addr.Dial` 不支持 ctx 取消 | 新增 `addr.DialContext`，`addr.Dial` 变成薄封装 |
| A3 | `Transport.DialContext` 里丢了请求级 ctx | 闭包改用 `addr.DialContext(ctx, a)` |
| A4 | `Close()` 只关空闲连接，在跑的 NDJSON 流停不掉 | 客户端自己持有活动流的 cancel，`Close()` 先取消全部活动流，再掐断空闲连接，整体用 `sync.Once` 包住保证幂等 |
| A5 | `TestServeStream` 裸读 `httptest.ResponseRecorder.Body`，有数据竞争 | 改用真实的 `httptest.NewServer`，按行读 NDJSON，顺带验证了「逐行」的线格式 |
| A6 | 要等 Task 7 才发现签名对不上 | 新增外部测试包 `contract_test.go`，编译期断言 `var _ rpc.Transport = (*httpx.Client)(nil)` |
| A7 | 两份 Handler 接口可能对不上 | `httpx.Handler` 与 `rpc.Handler` 方法集逐字一致，断言方向是 `rpc.Handler → httpx.Handler`（`var _ httpx.Handler = (rpc.Handler)(nil)`） |
| A8 | `Serve` 的 ctx 监视 goroutine 会泄漏 | 加 `done` channel，`Serve` 返回后监视 goroutine 退出 |
| A9 | 事件通道关闭会顺带掐掉控制消息流 | 某一侧通道关闭只停那一侧（置 nil channel），两侧都结束才收尾 |
| A10 | 请求体没有上限 | `http.MaxBytesReader` 限制在 1 MiB |
| A11 | GET 也会命中路由，与 OpenAPI 只声明 POST 对不上 | 中间件里校验方法，不是 POST 一律 405（带 `Allow` 头） |
| A12 | 客户端不校验入参/出参的 Go 类型。JSON 会把类型信息擦掉：入参类型不对时，服务端照样按规格解码出一个零值对象；出参类型不对时，服务端的响应被解码进一个形状不合的对象里——两种都是「静默成功」，调用方看不出自己传错了类型（Task 8 的契约测试实测出来的分歧③④，与 gRPC 侧的 Go 类型断言行为不一致） | `Call` 在序列化之前按 `api.Spec(method)` 校验 `in` 与 `out` 的 Go 类型，不符就带**方法名、期望类型、实际类型**报错，且**不发请求**。两个边界有意这么定：`out == nil` 是「调用方不关心结果」（保持原语义，不校验）；未知方法（`api.Spec` 返回 nil）没有规格可比对，不在客户端判，放行给服务端的兜底路由回 404「未知路径」——路由的真相在服务端 |
| I1 | 未注册的路径落到 ServeMux 内置的 NotFound，`authed` 中间件根本不执行：没 token 的调用方靠 401/404 的差别就能枚举端点（与「鉴权在前」的注释自相矛盾），而且那个 404 是纯文本，错误体形状与业务端点对不上，客户端只能退化成裸串「HTTP 404」，错误原因丢失 | `newMux` 里注册兜底路由 `"/"`：先校验 token，不过就回 401，过了再回 404「未知路径 `<path>`」；错误体沿用统一的 `{"ok":false,"error":"..."}` 形状。兜底路由只做 token 校验，不做 POST 方法检查（对不存在的路径谈「只接受 POST」是误导）。更精确的 pattern 优先命中，业务端点与 Task 6 的文档端点都不受影响。同时删掉 `serveCall` 里 `spec == nil` 的死分支——`api.Spec` 对已注册方法必有 spec |
| I3 | 零值 `httpx.Client` 的 `http` 字段是 nil，`Call`/`Stream` 走到 `c.http.Do` 会 nil 指针解引用 panic。`Client` 是导出类型，零值是被允许的构造物（`contract_test.go` 就用它验证 `Close()` 不炸），而 `rpc.Transport` 契约里 `Call`/`Stream` 都返回 error，panic 会带走整个进程 | `checkOpen` 并进「未初始化」判断：`c.http == nil` 时返回「客户端未初始化，请用 Dial 创建」；`Call` 与 `Stream` 都先过 `checkOpen`，覆盖到位 |
| 状态码 | 服务端实际返回 200/400/401/404/405/500 | **Task 6 已补齐 400/401/405/500**：规范里每个业务端点声明 200/400/401/405/500，错误响应在 `components.responses` 里复用（见 Task 6 的 B3）。`404` 只由兜底路由产生（未知路径），不对应任何业务端点，因此规范里不声明——修复轮 1 之前那条 404 是 ServeMux 内置的纯文本，同样不属于业务端点 |

初稿里的 `client.go` import 了 `"time"` 但没用得到，已删；`"net"` 在
`DialContext` 闭包的签名里要用到，保留。初稿末尾的 `var _ = net.Dial` 占位
没有采用——Go 会因未使用的 import 直接编译失败。

修复轮 1 之后，本节 `server.go` 代码块里多了一行 `registerDocs(mux)`：它来自
Task 6（见 Task 6 的 B6 与 Step 3）。代码块按「计划 == 实现」的要求与当前实现
保持逐行一致，Task 6 的说明见那一节。

---

### Task 6: `internal/rpc/httpx` 的 OpenAPI 规范与 Swagger 文档页

**Files:**
- Create: `internal/rpc/httpx/openapi.yaml`
- Create: `internal/rpc/httpx/docs.go`
- Create: `internal/rpc/httpx/openapi_test.go`
- Modify: `internal/rpc/httpx/server.go`（挂上文档端点）
- Modify: `go.mod` / `go.sum`（新增 swaggo 依赖）

**Interfaces:**
- Consumes: `internal/api`（Task 2）
- Produces:
  ```go
  package httpx
  func openAPISpec() []byte              // 嵌入的规范原文
  func openAPIJSON() ([]byte, error)     // 规范转成的 JSON（缓存）
  func yamlToJSON(data []byte) ([]byte, error) // YAML→JSON 转换，失败时返回错误
  func registerDocs(mux *http.ServeMux)  // 注册 /openapi.yaml、/openapi.json、/docs、/docs/
  ```

> **与控制器补充说明（B1–B11）的对照**：本节代码是按 B1–B11 修正后的最终实现，
> 与最初的草稿有出入的地方见文末「与初稿的差异」。
>
> **文档端点契约**（设计文档 §6.2、§6.4）：
>
> | 端点 | 方法 | 响应 | 参与 token 校验 |
> |---|---|---|---|
> | `/openapi.yaml` | GET | 200 `application/yaml` | 否 |
> | `/openapi.json` | GET | 200 `application/json` | 否 |
> | `/docs` | GET | 200 `text/html; charset=utf-8` | 否 |
> | `/docs/` | GET | 200 `text/html; charset=utf-8`（与 `/docs` 是同一个页面） | 否 |
> | `/docs/<白名单里的资源>` | GET | 200 静态资源（离线嵌入的 swagger-ui） | 否 |
> | `/docs/<其它>` | GET | 404 `{"ok":false,"error":"..."}` | 否 |
>
> 只接受 GET；其他方法回 405 并带上 `Allow: GET`，错误体沿用全站统一的
> `{"ok":false,"error":"..."}` 形状。
>
> **静态资源的白名单**只有 4 个：`swagger-ui.css`、`swagger-ui-bundle.js`、
> `swagger-ui-standalone-preset.js`、`favicon-32x32.png`——正好是页面自己引用的
> 那几个。swagger-ui 自带的 `index.html` 与 `swagger-initializer.js` 不在其中：
> 它们指向公网的 `petstore.swagger.io`，本机工具的文档页不该依赖公网，
> 也不该把上游的首页当成本项目的首页。
>
> **`/docs/` 的前缀是写死的常量**，不由任何请求决定。修复轮 1 之前，静态资源
> 由 `httpSwagger.WrapHandler` 提供：它是**包级单例**，内部的 `sync.Once` 会把
> **进程级**的 `swaggerFiles.Handler.Prefix` 锁成「首个到达请求」解析出的前缀。
> 而 http-swagger 的路径正则 `^(.*/)([^?].*)?[?|.]*$` 在「目录路径 + 查询串」上
> 会把前缀回退成 `/`，于是**一次 `GET /docs/?x=1` 就能让本进程内
> `/docs/swagger-ui.css` 之类的资源永久 404**。现在改成自己按固定前缀 +
> 白名单从 `swaggo/files` 的内存文件系统里取资源，谁先打都不影响。
>
> **修复轮 1（评审 FAIL 后的 4 条 Important）**：本节代码是评审之后的最终实现。
> 与上一轮的差别：
>
> | 条目 | 问题 | 落法 |
> |---|---|---|
> | I1 | 规范里的「路径 → schema」映射无人看守：把 `list` 的请求体改成 `TaskRef`、把 `add` 的 200 响应改成 `BoolResult`，全部测试仍绿 | 新增 `TestOpenAPIPathSchemasMatchAPISpec`：逐个方法比对 `requestBody` 与 200 响应的 `$ref` 末段，与 `api.Spec(method).NewIn()`/`NewOut()` 的类型名对上（`stream_messages` 显式特判为 `Message`） |
> | I2 | 「防 CDN 回潮」的护栏形同虚设：协议相对 URL、大写 scheme、`data:` URI、相对路径都能绕过；正向断言还能被一行 HTML 注释满足 | `TestDocsPageReferencesLocalAssets` 改成用 `regexp` 抽 `<script src>` / `<link href>` 的**属性值**（跳过 HTML 注释），逐个判定必须是本地绝对路径且不带 scheme；正向断言改成「引用集合**恰好**等于期望集合」 |
> | I3 | `httpSwagger.WrapHandler` 是包级单例，`sync.Once` 把进程级的 `swaggerFiles.Handler.Prefix` 锁成首个请求的前缀：一次 `GET /docs/?x=1` 就让静态资源永久 404 | 弃用 `httpSwagger.WrapHandler`，改为自己按**固定前缀 `/docs/` + 白名单**从 `swaggo/files` 的内存文件系统取资源；`/docs/` 与 `/docs` 返回同一个页面；白名单外的路径一律 404 |
> | I4 | 文档端点契约与实测对不上：`/docs/` 实际是 301，`/docs/doc.json` 稳定吐 500 | `/docs/` 改成 200 页面、`/docs/doc.json` 改成 404；本节的端点契约表同步更新 |
>
> 顺带修了 3 条 Minor：整数的 `format` 也纳入类型护栏（`int64` ↔ `int64`、
> `int32` ↔ `int32`）；`Message.kind` 的 enum 与 `api.MessageKind*` 常量集合
> 对上（测试直接解析 `internal/api/message.go` 的源码，不靠手抄清单）；
> `requestBody` 从 `required: true` 改成 `required: false`——服务端容忍空请求体
> （EOF 不算错），`get_settings`/`stream_messages` 的入参本来就是 `api.Empty`。

- [ ] **Step 1: 写 `internal/rpc/httpx/openapi.yaml`**

规范是 HTTP 这一侧接口形状的唯一真相源：路径、方法名、DTO 字段都必须与
`internal/api` 对齐。**每个业务端点都要声明实现里真实存在的状态码**
（200 / 400 / 401 / 405 / 500，见 B3），错误响应用 `components.responses` 复用。

```yaml
# lgdm HTTP 传输的接口规范。
#
# 这是 HTTP 这一侧接口形状的唯一真相源：路径、方法名、DTO 字段都必须与
# internal/api 的常量与结构体一一对应。internal/rpc/httpx/openapi_test.go
# 里的规范一致性测试负责在两边漂移时失败。
#
# 同步规则：新增/修改端点或 DTO 字段时必须同步改本文件。
openapi: 3.0.3
info:
  title: LGDM Download Service
  version: "1.0.0"
  description: |
    lgdm 业务主进程暴露给 UI 子进程的下载管理接口。

    调用方需要带上 Authorization: Bearer <token> 头；token 由业务主进程
    在拉起 UI 子进程时通过环境变量下发。

    消息流端点 /lgdm.v1.DownloadService/stream_messages 返回 NDJSON，
    每行一个消息对象，事件与控制消息合并在同一条流里。

    请求体一律可选：空请求体等价于 {}，服务端不因为 body 为空而报错。

    文档端点（/openapi.yaml、/openapi.json、/docs）不参与 token 校验：
    它们只暴露接口形状，不暴露任务数据。
servers:
  - url: /
    description: |
      同源相对地址。默认部署在本机 Unix socket 上，浏览器无法直连；
      若通过 LDM_RPC_LISTEN 指定 host:port 监听，文档页与接口同源。
security:
  - bearerAuth: []
paths:
  /lgdm.v1.DownloadService/list:
    post:
      summary: 查询任务列表
      operationId: list
      requestBody:
        required: false
        content:
          application/json:
            schema: { $ref: "#/components/schemas/ListParams" }
      responses:
        "200":
          description: 任务列表
          content:
            application/json:
              schema: { $ref: "#/components/schemas/ListResult" }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "405": { $ref: "#/components/responses/MethodNotAllowed" }
        "500": { $ref: "#/components/responses/InternalError" }
  /lgdm.v1.DownloadService/add:
    post:
      summary: 添加下载任务
      operationId: add
      requestBody:
        required: false
        content:
          application/json:
            schema: { $ref: "#/components/schemas/AddTaskInput" }
      responses:
        "200":
          description: 新建的任务
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Task" }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "405": { $ref: "#/components/responses/MethodNotAllowed" }
        "500": { $ref: "#/components/responses/InternalError" }
  /lgdm.v1.DownloadService/start:
    post:
      summary: 启动任务
      operationId: start
      requestBody:
        required: false
        content:
          application/json:
            schema: { $ref: "#/components/schemas/TaskRef" }
      responses:
        "200":
          description: 已启动
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Empty" }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "405": { $ref: "#/components/responses/MethodNotAllowed" }
        "500": { $ref: "#/components/responses/InternalError" }
  /lgdm.v1.DownloadService/pause:
    post:
      summary: 暂停任务
      operationId: pause
      requestBody:
        required: false
        content:
          application/json:
            schema: { $ref: "#/components/schemas/TaskRef" }
      responses:
        "200":
          description: 已暂停
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Empty" }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "405": { $ref: "#/components/responses/MethodNotAllowed" }
        "500": { $ref: "#/components/responses/InternalError" }
  /lgdm.v1.DownloadService/delete:
    post:
      summary: 删除任务
      operationId: delete
      requestBody:
        required: false
        content:
          application/json:
            schema: { $ref: "#/components/schemas/TaskRef" }
      responses:
        "200":
          description: 已删除
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Empty" }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "405": { $ref: "#/components/responses/MethodNotAllowed" }
        "500": { $ref: "#/components/responses/InternalError" }
  /lgdm.v1.DownloadService/is_preparing:
    post:
      summary: 报告任务是否处于「已预留 slot 但引擎未启动」的准备阶段
      operationId: isPreparing
      requestBody:
        required: false
        content:
          application/json:
            schema: { $ref: "#/components/schemas/TaskRef" }
      responses:
        "200":
          description: 准备阶段标记
          content:
            application/json:
              schema: { $ref: "#/components/schemas/BoolResult" }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "405": { $ref: "#/components/responses/MethodNotAllowed" }
        "500": { $ref: "#/components/responses/InternalError" }
  /lgdm.v1.DownloadService/probe:
    post:
      summary: 探测 URL 目标大小
      operationId: probe
      requestBody:
        required: false
        content:
          application/json:
            schema: { $ref: "#/components/schemas/ProbeParams" }
      responses:
        "200":
          description: 目标大小（未知时为 0）
          content:
            application/json:
              schema: { $ref: "#/components/schemas/ProbeResult" }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "405": { $ref: "#/components/responses/MethodNotAllowed" }
        "500": { $ref: "#/components/responses/InternalError" }
  /lgdm.v1.DownloadService/get_settings:
    post:
      summary: 读取权威设置快照
      operationId: getSettings
      requestBody:
        required: false
        content:
          application/json:
            schema: { $ref: "#/components/schemas/Empty" }
      responses:
        "200":
          description: 设置快照
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Settings" }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "405": { $ref: "#/components/responses/MethodNotAllowed" }
        "500": { $ref: "#/components/responses/InternalError" }
  /lgdm.v1.DownloadService/save_settings:
    post:
      summary: 持久化设置
      operationId: saveSettings
      requestBody:
        required: false
        content:
          application/json:
            schema: { $ref: "#/components/schemas/Settings" }
      responses:
        "200":
          description: 已保存
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Empty" }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "405": { $ref: "#/components/responses/MethodNotAllowed" }
        "500": { $ref: "#/components/responses/InternalError" }
  /lgdm.v1.DownloadService/bt_info:
    post:
      summary: 查询 BT 任务实时详情
      operationId: btInfo
      requestBody:
        required: false
        content:
          application/json:
            schema: { $ref: "#/components/schemas/BTInfoParams" }
      responses:
        "200":
          description: BT 详情快照
          content:
            application/json:
              schema: { $ref: "#/components/schemas/BTDetail" }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "405": { $ref: "#/components/responses/MethodNotAllowed" }
        "500": { $ref: "#/components/responses/InternalError" }
  /lgdm.v1.DownloadService/stream_messages:
    post:
      summary: 订阅消息流（进度事件 + 控制消息）
      operationId: streamMessages
      requestBody:
        required: false
        content:
          application/json:
            schema: { $ref: "#/components/schemas/Empty" }
      responses:
        "200":
          description: NDJSON 流，每行一个消息
          content:
            application/x-ndjson:
              schema: { $ref: "#/components/schemas/Message" }
        "400": { $ref: "#/components/responses/BadRequest" }
        "401": { $ref: "#/components/responses/Unauthorized" }
        "405": { $ref: "#/components/responses/MethodNotAllowed" }
        "500": { $ref: "#/components/responses/InternalError" }
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
  responses:
    BadRequest:
      description: 请求体不是合法 JSON
      content:
        application/json:
          schema: { $ref: "#/components/schemas/Error" }
    Unauthorized:
      description: token 缺失或无效
      content:
        application/json:
          schema: { $ref: "#/components/schemas/Error" }
    MethodNotAllowed:
      description: 只接受 POST
      content:
        application/json:
          schema: { $ref: "#/components/schemas/Error" }
    InternalError:
      description: 业务侧调用失败
      content:
        application/json:
          schema: { $ref: "#/components/schemas/Error" }
  schemas:
    Empty:
      type: object
    Error:
      type: object
      required: [ok, error]
      properties:
        ok: { type: boolean }
        error: { type: string }
    TaskRef:
      type: object
      required: [id]
      properties:
        id: { type: string }
    BoolResult:
      type: object
      properties:
        value: { type: boolean }
    ProbeParams:
      type: object
      required: [url]
      properties:
        url: { type: string }
    ProbeResult:
      type: object
      properties:
        total_size: { type: integer, format: int64 }
    ListParams:
      type: object
      properties:
        filter: { type: string }
        sort: { type: string }
    ListResult:
      type: object
      properties:
        tasks:
          type: array
          items: { $ref: "#/components/schemas/Task" }
    AddTaskInput:
      type: object
      required: [url]
      properties:
        url: { type: string }
        save_path: { type: string }
        protocol: { type: string }
        chunk_count: { type: integer }
        min_chunk_size: { type: integer, format: int64 }
        auth: { $ref: "#/components/schemas/AuthOptions" }
    AuthOptions:
      type: object
      properties:
        username: { type: string }
        password: { type: string }
        user_agent: { type: string }
        cookies: { type: string }
        referer: { type: string }
        ftp_passive: { type: boolean }
        proxy_mode: { type: string }
        proxy_url: { type: string }
        proxy_bypass: { type: string }
        proxy_auto_config_url: { type: string }
    Settings:
      type: object
      properties:
        default_save_dir: { type: string }
        default_threads: { type: integer }
        min_chunk_size: { type: integer, format: int64 }
        user_agent: { type: string }
        cookies: { type: string }
        ftp_passive: { type: boolean }
        prealloc: { type: boolean }
        task_sort: { type: string }
        proxy_mode: { type: string }
        proxy_url: { type: string }
        proxy_bypass: { type: string }
        proxy_auto_config_url: { type: string }
        download_limit_kib: { type: integer }
        light_mode: { type: boolean }
        max_concurrent: { type: integer }
        auto_start: { type: boolean }
        language: { type: string }
        bt_port: { type: integer }
        bt_max_peers: { type: integer }
        bt_download_limit_kib: { type: integer }
        bt_upload_limit_kib: { type: integer }
        bt_dht: { type: boolean }
        bt_pex: { type: boolean }
        bt_seed_ratio: { type: number }
        bt_max_seeding: { type: integer }
        bt_max_concurrent: { type: integer }
        bt_trackers: { type: string }
    Task:
      type: object
      properties:
        id: { type: string }
        url: { type: string }
        save_path: { type: string }
        protocol: { type: string }
        total_size: { type: integer, format: int64 }
        downloaded: { type: integer, format: int64 }
        support_range: { type: boolean }
        etag: { type: string }
        last_modified: { type: string }
        digest_algorithm: { type: string }
        digest: { type: string }
        is_allocated: { type: boolean }
        chunk_count: { type: integer }
        min_chunk_size: { type: integer, format: int64 }
        chunk_progress:
          type: array
          items: { type: integer, format: int64 }
        chunk_ranges:
          type: array
          items: { type: integer, format: int64 }
        status: { type: string }
        auth_data: { type: string }
        error_message: { type: string }
        error_kind: { type: string }
        created_at: { type: string, format: date-time }
        updated_at: { type: string, format: date-time }
        completed_at: { type: string, format: date-time }
        bt_file: { type: string }
    BTInfoParams:
      type: object
      required: [task_id]
      properties:
        task_id: { type: string }
    FileProgress:
      type: object
      properties:
        path: { type: string }
        length: { type: integer, format: int64 }
        completed: { type: integer, format: int64 }
    BTDetail:
      type: object
      properties:
        name: { type: string }
        info_hash: { type: string }
        total: { type: integer, format: int64 }
        downloaded: { type: integer, format: int64 }
        uploaded: { type: integer, format: int64 }
        speed_bps: { type: number }
        upload_bps: { type: number }
        peers: { type: integer }
        total_peers: { type: integer }
        seeders: { type: integer }
        leechers: { type: integer }
        piece_length: { type: integer, format: int64 }
        pieces: { type: integer }
        pieces_done: { type: integer }
        piece_buckets:
          type: array
          items: { type: integer }
        seeding: { type: boolean }
        complete: { type: boolean }
        files:
          type: array
          items: { $ref: "#/components/schemas/FileProgress" }
        trackers:
          type: array
          items: { type: string }
    Event:
      type: object
      properties:
        why: { type: string }
        task: { $ref: "#/components/schemas/Task" }
        speed_bps: { type: number }
    Message:
      type: object
      properties:
        kind:
          type: string
          enum: [event, show, show_add_task, language, close]
        event: { $ref: "#/components/schemas/Event" }
        show_add_task: { $ref: "#/components/schemas/ShowAddTaskParams" }
        language: { type: string }
    ShowAddTaskParams:
      type: object
      properties:
        url: { type: string }
        name: { type: string }
        ua: { type: string }
        cookies: { type: string }
```

- [ ] **Step 2: 建 `internal/rpc/httpx/docs.go`**

Swagger UI **离线嵌入**（B1）：`github.com/swaggo/files` 把 swagger-ui 静态资源
打进二进制，页面不引任何公网 CDN。`/docs` 自己渲染 HTML 并用**绝对路径**引资源
（B2：页面挂在 `/docs`（无尾斜杠）时，相对路径 `./swagger-ui.css` 会被浏览器
解析到 `/swagger-ui.css` → 404）；静态资源本体由 `/docs/` 提供——**固定前缀 +
白名单**，白名单只收页面自己引用的那 4 个资源（修复轮 1 的落法，见本节的
「文档端点契约」）。

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package httpx

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"

	swaggerFiles "github.com/swaggo/files"
	"go.yaml.in/yaml/v3"
)

//go:embed openapi.yaml
var openapiYAML []byte

// openAPIJSON 缓存规范转成的 JSON 形式。转换只做一次：规范是嵌入的常量，
// 结果不会变，重复解析纯属浪费。
var (
	openAPIJSONOnce sync.Once
	openAPIJSONData []byte
	openAPIJSONErr  error
)

// openAPISpec 返回嵌入的 OpenAPI 规范原文（YAML）。
func openAPISpec() []byte { return openapiYAML }

// openAPIJSON 返回规范的 JSON 形式。转换失败时返回错误，调用方据此回 500。
func openAPIJSON() ([]byte, error) {
	openAPIJSONOnce.Do(func() {
		openAPIJSONData, openAPIJSONErr = yamlToJSON(openapiYAML)
	})
	return openAPIJSONData, openAPIJSONErr
}

// yamlToJSON 把 YAML 文本转成 JSON 字节。转换失败时返回错误而不是 panic：
// 规范是嵌入的常量，正常不会失败，但一旦失败也不该把请求打崩。
func yamlToJSON(data []byte) ([]byte, error) {
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// docsPrefix 是文档页静态资源的前缀。
//
// 它是**写死**的常量，不看请求里的任何东西。这一点很关键：先前用
// httpSwagger.WrapHandler 提供的静态资源，它是包级单例，内部用 sync.Once
// 把**进程级**的 swaggerFiles.Handler.Prefix 锁成「首个到达请求」解析出的
// 前缀。http-swagger 的路径正则 ^(.*/)([^?].*)?[?|.]*$ 在「目录路径 + 查询串」
// 上会把前缀回退成 "/"，于是一次 GET /docs/?x=1 就能让本进程内
// /docs/swagger-ui.css 之类的资源**永久** 404。
//
// 现在前缀固定、白名单固定，谁先打都不影响。
const docsPrefix = "/docs/"

// docsAsset 描述一个允许从 /docs/ 提供的静态资源。
type docsAsset struct {
	// name 是资源名：请求路径去掉 docsPrefix 之后剩下的部分。
	name string
	// contentType 是响应的 Content-Type。
	contentType string
}

// docsAssets 是文档页静态资源的白名单：只有这里列出的资源能从 /docs/ 拿到，
// 其余一律 404。
//
// 清单只收本页自己真正引用的资源。swagger-ui 发行版自带的 index.html 与
// swagger-initializer.js 不在其中——它们指向公网的 petstore.swagger.io，
// 本机工具的文档页不该依赖公网，也不该把上游的首页当成本项目的首页。
var docsAssets = []docsAsset{
	{name: "swagger-ui.css", contentType: "text/css; charset=utf-8"},
	{name: "swagger-ui-bundle.js", contentType: "text/javascript; charset=utf-8"},
	{name: "swagger-ui-standalone-preset.js", contentType: "text/javascript; charset=utf-8"},
	{name: "favicon-32x32.png", contentType: "image/png"},
}

// registerDocs 注册文档端点。它们不参与 token 校验——只暴露接口形状，
// 不暴露任务数据。文档端点只接受 GET，其他方法回 405 并带上 Allow 头。
func registerDocs(mux *http.ServeMux) {
	mux.Handle("/openapi.yaml", getOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(openAPISpec())
	})))
	mux.Handle("/openapi.json", getOnly(serveOpenAPIJSON(openAPIJSON)))
	// /docs 自己渲染页面：静态资源用绝对路径引，避免 /docs 被当成「文件」
	// 时相对路径解析到 / 下面去。资源本体由 /docs/ 提供。
	mux.Handle("/docs", getOnly(http.HandlerFunc(writeSwaggerPage)))
	// /docs/ 既是静态资源的挂载点，也是页面本身的第二个入口（/docs/ 与
	// /docs 返回同一个页面，避免尾斜杠差异带来 301 契约漂移）。
	mux.Handle(docsPrefix, getOnly(http.HandlerFunc(serveDocsSubtree)))
}

// serveDocsSubtree 处理 /docs/ 下的请求：
//
//   - 路径正好是 /docs/ → 返回本页自己的页面（与 /docs 相同）；
//   - 路径命中白名单 → 返回对应的静态资源；
//   - 其余 → 404。
//
// 前缀取自写死的 docsPrefix，与「谁先到」无关。
func serveDocsSubtree(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, docsPrefix)
	if !ok {
		writeError(w, http.StatusNotFound, "未知路径 "+r.URL.Path)
		return
	}
	if rest == "" {
		writeSwaggerPage(w, r)
		return
	}
	for _, a := range docsAssets {
		if a.name == rest {
			serveDocsAsset(w, r, a)
			return
		}
	}
	writeError(w, http.StatusNotFound, "没有这个文档资源 "+rest)
}

// serveDocsAsset 把白名单里的静态资源吐出来。资源本体在 swaggo/files 的
// 内存文件系统里（编译进二进制，不依赖公网）。用 http.ServeContent 而不是
// 直接 Write，是为了顺带带上 Content-Length、Last-Modified 与 Range 支持。
func serveDocsAsset(w http.ResponseWriter, r *http.Request, a docsAsset) {
	f, err := swaggerFiles.FS.OpenFile(swaggerFiles.CTX, "/"+a.name, os.O_RDONLY, 0o644)
	if err != nil {
		// 资源读不出来是打包时出的问题，不是调用方的错。
		writeError(w, http.StatusInternalServerError, "读不出文档资源 "+a.name)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读不出文档资源 "+a.name)
		return
	}
	w.Header().Set("Content-Type", a.contentType)
	http.ServeContent(w, r, a.name, info.ModTime(), f)
}

// writeSwaggerPage 写出离线的 Swagger UI 页面。
func writeSwaggerPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerPage))
}

// serveOpenAPIJSON 把规范以 JSON 形式吐出来。取 JSON 的方式由参数传入，
// 方便测试注入「转换失败」的情形。
func serveOpenAPIJSON(load func() ([]byte, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := load()
		if err != nil {
			http.Error(w, "openapi: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}
}

// getOnly 把 handler 限制在 GET 上：其他方法回 405 并带上 Allow: GET。
// 错误体沿用全站统一的 {"ok":false,"error":"..."} 形状。
func getOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeError(w, http.StatusMethodNotAllowed, "只接受 GET")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// swaggerPage 是离线的 Swagger UI 页面：静态资源从同源的 /docs/ 取，
// 规范本体从同源的 /openapi.json 取。整页不引用任何外链资源——本机工具的
// 文档页不该依赖公网。
//
// 资源用绝对路径（/docs/swagger-ui.css）而不是相对路径（./swagger-ui.css）：
// 页面挂在 /docs（无尾斜杠）时，浏览器会把 /docs 当成「文件」，其父目录是
// /，相对路径会解析到 /swagger-ui.css —— 404。
//
// 页面引用的资源必须与 docsAssets 白名单一一对应：openapi_test.go 里的
// TestDocsPageReferencesLocalAssets 与 TestDocsAssetsMatchPageReferences
// 守着这条不变量。
const swaggerPage = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>LGDM Download Service — API 文档</title>
<link rel="stylesheet" type="text/css" href="/docs/swagger-ui.css">
<link rel="icon" type="image/png" href="/docs/favicon-32x32.png" sizes="32x32">
</head>
<body>
<div id="swagger-ui"></div>
<script src="/docs/swagger-ui-bundle.js"></script>
<script src="/docs/swagger-ui-standalone-preset.js"></script>
<script>
window.onload = function () {
  if (typeof SwaggerUIBundle === 'undefined') {
    document.getElementById('swagger-ui').innerHTML =
      '<p>无法加载 Swagger UI 资源。请直接查看 ' +
      '<a href="/openapi.yaml">/openapi.yaml</a> 或 ' +
      '<a href="/openapi.json">/openapi.json</a>。</p>';
    return;
  }
  window.ui = SwaggerUIBundle({
    url: '/openapi.json',
    deepLinking: true,
    docExpansion: 'list',
    dom_id: '#swagger-ui',
    persistAuthorization: true,
    validatorUrl: null,
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
    plugins: [SwaggerUIBundle.plugins.DownloadUrl],
    layout: 'StandaloneLayout'
  });
};
</script>
</body>
</html>`
```

- [ ] **Step 3: 在 `server.go` 里挂上文档端点**

在 `newMux` 函数的 `mux := http.NewServeMux()` 之后加两行——文档端点**不进**
`authed` 中间件（设计文档 §6.4：它们不参与 token 校验）：

```go
	mux := http.NewServeMux()
	// 文档端点不走 authed：它们只暴露接口形状，不暴露任务数据。
	registerDocs(mux)
```

- [ ] **Step 4: 写规范一致性测试**

创建 `internal/rpc/httpx/openapi_test.go`：方法 ↔ 路径双向核对、DTO 字段 ↔
schema property 双向核对（B4）、字段类型 ↔ OpenAPI type 核对（B9）、`$ref`
完整性、以及文档端点的行为（免鉴权、只收 GET、不引外链）。

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package httpx

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"lgo_download_manager/internal/api"
)

// dtoSchemas 列出「在规范里有 schema 的 DTO」与 schema 名的对应关系。
// 规范里每个 schema 都必须在这里出现，DTO 加字段时同步改规范就会被
// TestOpenAPISchemasMatchDTO 逮住。
var dtoSchemas = []struct {
	schema string
	dto    any
}{
	{"Task", api.Task{}},
	{"Settings", api.Settings{}},
	{"AuthOptions", api.AuthOptions{}},
	{"AddTaskInput", api.AddTaskInput{}},
	{"Event", api.Event{}},
	{"Message", api.Message{}},
	{"ShowAddTaskParams", api.ShowAddTaskParams{}},
	{"BTDetail", api.BTDetail{}},
	{"FileProgress", api.FileProgress{}},
	{"ListParams", api.ListParams{}},
	{"ListResult", api.ListResult{}},
	{"TaskRef", api.TaskRef{}},
	{"BoolResult", api.BoolResult{}},
	{"ProbeParams", api.ProbeParams{}},
	{"ProbeResult", api.ProbeResult{}},
	{"BTInfoParams", api.BTInfoParams{}},
	{"Empty", api.Empty{}},
}

// TestOpenAPIParses 验证规范能转成 JSON，且版本号是 3.0.3。
func TestOpenAPIParses(t *testing.T) {
	doc := parseSpec(t)
	if got := doc["openapi"]; got != "3.0.3" {
		t.Fatalf("openapi 版本不对: %v", got)
	}
}

// TestOpenAPICoversAllMethods 双向核对方法与路径：api.AllMethods 里的每个
// 方法都要有路径，规范里也不能有多余的业务路径。
func TestOpenAPICoversAllMethods(t *testing.T) {
	doc := parseSpec(t)
	paths := specPaths(t, doc)

	for _, m := range api.AllMethods {
		p := servicePrefix + m
		if _, ok := paths[p]; !ok {
			t.Fatalf("规范缺少方法 %q 的路径 %q", m, p)
		}
	}
	for p := range paths {
		if !strings.HasPrefix(p, servicePrefix) {
			t.Fatalf("规范里有非业务路径 %q", p)
		}
		m := strings.TrimPrefix(p, servicePrefix)
		found := false
		for _, known := range api.AllMethods {
			if known == m {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("规范里有多余的业务路径 %q（api.AllMethods 里没有 %q）", p, m)
		}
	}
}

// TestOpenAPIEndpointsDeclareErrorResponses 验证每个业务端点都声明了实现里
// 真实存在的状态码：200 / 400 / 401 / 405 / 500。
func TestOpenAPIEndpointsDeclareErrorResponses(t *testing.T) {
	doc := parseSpec(t)
	paths := specPaths(t, doc)
	want := []string{"200", "400", "401", "405", "500"}
	for _, m := range api.AllMethods {
		p := servicePrefix + m
		item, ok := paths[p].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 的定义不是对象", p)
		}
		post, ok := item["post"].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 缺少 post 定义", p)
		}
		responses, ok := post["responses"].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 缺少 responses 定义", p)
		}
		for _, code := range want {
			if _, ok := responses[code]; !ok {
				t.Fatalf("路径 %q 的响应里缺少 %s", p, code)
			}
		}
	}
}

// TestOpenAPIPathSchemasMatchAPISpec 核对「路径 → schema」的映射：每个方法
// 的请求体与 200 响应必须指向 api.Spec 里声明的那个 DTO。
//
// 只核对路径集合是不够的——路径挂错 schema（把 list 的请求体写成 TaskRef、
// 把 add 的响应写成 BoolResult）一样没人管。这里把两头对上。
func TestOpenAPIPathSchemasMatchAPISpec(t *testing.T) {
	doc := parseSpec(t)
	paths := specPaths(t, doc)
	for _, m := range api.AllMethods {
		spec := api.Spec(m)
		if spec == nil {
			t.Fatalf("api.Spec(%q) 是 nil", m)
		}
		p := servicePrefix + m
		item, ok := paths[p].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 的定义不是对象", p)
		}
		post, ok := item["post"].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 缺少 post 定义", p)
		}

		// 请求体 → api.Spec(method).NewIn()
		rb, ok := post["requestBody"].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 缺少 requestBody 定义", p)
		}
		wantIn := dtoTypeName(spec.NewIn())
		gotIn := contentSchemaNames(t, rb["content"])
		if !sameSet([]string{wantIn}, gotIn) {
			t.Fatalf("路径 %q 的请求体 schema 不对：规范里是 %v，api.Spec 说是 %q", p, gotIn, wantIn)
		}

		// 200 响应 → api.Spec(method).NewOut()
		responses, ok := post["responses"].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 缺少 responses 定义", p)
		}
		ok200, ok := responses["200"].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 缺少 200 响应定义", p)
		}
		wantOut := dtoTypeName(spec.NewOut())
		if m == api.MethodStreamMessages {
			// 流式端点的响应是 NDJSON 的消息行（api.Message），不是
			// api.Spec 里的 NewOut（那是 api.Empty）。显式特判。
			wantOut = "Message"
		}
		gotOut := contentSchemaNames(t, ok200["content"])
		if !sameSet([]string{wantOut}, gotOut) {
			t.Fatalf("路径 %q 的 200 响应 schema 不对：规范里是 %v，应当是 %q", p, gotOut, wantOut)
		}
	}
}

// contentSchemaNames 收集 content 下各媒体类型的 schema 名（$ref 的末段）。
func contentSchemaNames(t *testing.T, content any) []string {
	t.Helper()
	m, ok := content.(map[string]any)
	if !ok {
		t.Fatalf("content 不是对象: %#v", content)
	}
	out := make([]string, 0, len(m))
	for _, v := range m {
		cm, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("content 的成员不是对象: %#v", v)
		}
		sm, ok := cm["schema"].(map[string]any)
		if !ok {
			t.Fatalf("content 的成员没有 schema: %#v", cm)
		}
		ref, ok := sm["$ref"].(string)
		if !ok {
			t.Fatalf("schema 不是 $ref: %#v", sm)
		}
		out = append(out, ref[strings.LastIndex(ref, "/")+1:])
	}
	sort.Strings(out)
	return out
}

// dtoTypeName 返回 DTO 指针的类型名。
func dtoTypeName(v any) string {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return ""
	}
	return t.Name()
}

// TestOpenAPIRequestBodiesAreOptional 验证规范没有把请求体标成必填：
// 服务端容忍空请求体（EOF 不算错），get_settings / stream_messages 的
// 入参本来就是 api.Empty。
func TestOpenAPIRequestBodiesAreOptional(t *testing.T) {
	doc := parseSpec(t)
	paths := specPaths(t, doc)
	for _, m := range api.AllMethods {
		p := servicePrefix + m
		item, _ := paths[p].(map[string]any)
		post, _ := item["post"].(map[string]any)
		rb, ok := post["requestBody"].(map[string]any)
		if !ok {
			t.Fatalf("路径 %q 缺少 requestBody 定义", p)
		}
		raw, ok := rb["required"]
		if !ok {
			continue // 缺省即 false
		}
		if b, ok := raw.(bool); !ok || b {
			t.Fatalf("路径 %q 的 requestBody 不该是必填（规范里是 %v）", p, raw)
		}
	}
}

// TestOpenAPIMessageKindEnumMatchesAPI 验证规范里 Message.kind 的 enum
// 集合与 internal/api 的 MessageKind* 常量集合完全相同。
//
// 常量没法用反射枚举，这里直接解析 internal/api/message.go 的源码——
// 这样 api 那边新增或改掉一个 MessageKind 常量时，这条测试会红。
func TestOpenAPIMessageKindEnumMatchesAPI(t *testing.T) {
	doc := parseSpec(t)
	schemas := specSchemas(t, doc)
	msg, ok := schemas["Message"].(map[string]any)
	if !ok {
		t.Fatal("规范缺少 schema \"Message\"")
	}
	props, ok := msg["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema \"Message\" 没有 properties")
	}
	kind, ok := props["kind"].(map[string]any)
	if !ok {
		t.Fatal("schema \"Message\" 没有 kind 字段")
	}
	rawEnum, ok := kind["enum"].([]any)
	if !ok {
		t.Fatal("schema \"Message\" 的 kind 没有 enum")
	}
	got := make([]string, 0, len(rawEnum))
	for _, v := range rawEnum {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("enum 的成员不是字符串: %#v", v)
		}
		got = append(got, s)
	}
	sort.Strings(got)

	want := apiMessageKindConsts(t)
	if len(want) == 0 {
		t.Fatal("没从 internal/api/message.go 里解析出 MessageKind 常量")
	}
	if !sameSet(want, got) {
		t.Fatalf("Message.kind 的 enum 与 api 的 MessageKind 常量不一致:\n规范 = %v\napi  = %v", got, want)
	}
}

// apiMessageKindConsts 解析 internal/api/message.go，取出全部 MessageKind*
// 常量的取值。
func apiMessageKindConsts(t *testing.T) []string {
	t.Helper()
	const src = "../../api/message.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", src, err)
	}
	var out []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != len(vs.Names) {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "MessageKind") {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("常量 %s 的值不是字符串字面量", name.Name)
				}
				out = append(out, strings.Trim(lit.Value, `"`))
			}
		}
	}
	sort.Strings(out)
	return out
}

// TestOpenAPISchemasMatchDTO 双向核对 schema 的属性与 DTO 的 JSON 字段。
func TestOpenAPISchemasMatchDTO(t *testing.T) {
	doc := parseSpec(t)
	schemas := specSchemas(t, doc)
	for _, c := range dtoSchemas {
		s, ok := schemas[c.schema].(map[string]any)
		if !ok {
			t.Fatalf("规范缺少 schema %q", c.schema)
		}
		props, ok := s["properties"].(map[string]any)
		if !ok {
			if len(jsonFields(reflect.TypeOf(c.dto))) != 0 {
				t.Fatalf("schema %q 没有 properties，但 DTO 有字段", c.schema)
			}
			continue
		}
		got := make([]string, 0, len(props))
		for k := range props {
			got = append(got, k)
		}
		want := jsonFields(reflect.TypeOf(c.dto))
		if !sameSet(want, got) {
			t.Fatalf("schema %q 的字段与 DTO 不一致:\nDTO  = %v\n规范 = %v", c.schema, want, got)
		}
	}
}

// TestOpenAPISchemasCoverAllDTOs 验证规范里的 schema 没有漏网的 DTO：
// 规范里每个 schema 都必须在 dtoSchemas 里登记过。
func TestOpenAPISchemasCoverAllDTOs(t *testing.T) {
	doc := parseSpec(t)
	schemas := specSchemas(t, doc)
	known := map[string]bool{}
	for _, c := range dtoSchemas {
		known[c.schema] = true
	}
	// Error 是错误信封，不是 DTO：它不对应任何请求/响应结构体。
	known["Error"] = true
	for name := range schemas {
		if !known[name] {
			t.Fatalf("规范里的 schema %q 没有对应的 DTO 登记", name)
		}
	}
}

// TestOpenAPISchemaTypesMatchDTO 逐字段核对 OpenAPI 的 type 与 Go 类型。
// 只比字段名不够：字段名对但类型写错（比如把 int64 写成 string）一样会坑人。
func TestOpenAPISchemaTypesMatchDTO(t *testing.T) {
	doc := parseSpec(t)
	schemas := specSchemas(t, doc)
	for _, c := range dtoSchemas {
		s, ok := schemas[c.schema].(map[string]any)
		if !ok {
			t.Fatalf("规范缺少 schema %q", c.schema)
		}
		props, _ := s["properties"].(map[string]any)
		typ := reflect.TypeOf(c.dto)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := jsonFieldName(f)
			if name == "" {
				continue
			}
			prop, ok := props[name].(map[string]any)
			if !ok {
				t.Fatalf("schema %q 缺少字段 %q", c.schema, name)
			}
			got := parseShape(prop)
			want := expectShape(f.Type)
			if want.ref == "" && want.kind == "" {
				// 映射不了的类型（map、interface 等）不比。
				continue
			}
			checkShape(t, c.schema+"."+name, want, got)
		}
	}
}

// TestOpenAPIReferencesResolve 验证规范里所有 $ref 都指得到东西。
func TestOpenAPIReferencesResolve(t *testing.T) {
	doc := parseSpec(t)
	refs := map[string]bool{}
	collectRefs(doc, refs)
	for ref := range refs {
		parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
		if len(parts) != 3 || parts[0] != "components" {
			t.Fatalf("引用 %q 的形式不对", ref)
		}
		var cur any = doc
		for _, p := range parts {
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("引用 %q 指向的不是对象", ref)
			}
			cur, ok = m[p]
			if !ok {
				t.Fatalf("引用 %q 指向 %q，但那里没有东西", ref, strings.Join(parts, "."))
			}
		}
	}
}

// TestOpenAPIJSONIsValid 验证 /openapi.json 返回的是合法 JSON。
func TestOpenAPIJSONIsValid(t *testing.T) {
	mux := docsMux()
	rec := doGet(mux, "/openapi.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("应当 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type 不对: %q", ct)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("不是合法 JSON: %v (%s)", err, rec.Body.String())
	}
	if doc["openapi"] != "3.0.3" {
		t.Fatalf("openapi 版本不对: %v", doc["openapi"])
	}
}

// TestOpenAPIYAMLMatchesJSON 验证 /openapi.yaml 与 /openapi.json 是同一份规范。
func TestOpenAPIYAMLMatchesJSON(t *testing.T) {
	mux := docsMux()
	rec := doGet(mux, "/openapi.yaml")
	if rec.Code != http.StatusOK {
		t.Fatalf("应当 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
		t.Fatalf("Content-Type 不对: %q", ct)
	}
	if rec.Body.String() != string(openAPISpec()) {
		t.Fatal("/openapi.yaml 的内容与嵌入的规范原文不一致")
	}
	var fromYAML map[string]any
	if err := yaml.Unmarshal(rec.Body.Bytes(), &fromYAML); err != nil {
		t.Fatalf("/openapi.yaml 不是合法 YAML: %v", err)
	}
	// 归一化：YAML 与 JSON 的数字类型不同，两边都过一遍 JSON 再比。
	normalized, err := json.Marshal(fromYAML)
	if err != nil {
		t.Fatal(err)
	}
	var a, b map[string]any
	if err := json.Unmarshal(normalized, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(mustJSON(t), &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("/openapi.yaml 与 /openapi.json 描述的不是同一份规范")
	}
}

// TestDocsEndpointsNeedNoToken 验证文档端点不参与 token 校验。
func TestDocsEndpointsNeedNoToken(t *testing.T) {
	mux := docsMux()
	cases := []struct {
		path        string
		contentType string
	}{
		{"/openapi.yaml", "application/yaml"},
		{"/openapi.json", "application/json"},
		{"/docs", "text/html"},
		{"/docs/swagger-ui.css", "text/css"},
		{"/docs/swagger-ui-bundle.js", "javascript"},
	}
	for _, c := range cases {
		rec := doGet(mux, c.path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 应当 200（不带 token）, got %d", c.path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, c.contentType) {
			t.Fatalf("%s 的 Content-Type 不对: %q（期望含 %q）", c.path, ct, c.contentType)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s 的响应体是空的", c.path)
		}
	}
}

// TestDocsEndpointsRejectNonGet 验证文档端点只接受 GET。
func TestDocsEndpointsRejectNonGet(t *testing.T) {
	mux := docsMux()
	for _, path := range []string{"/openapi.yaml", "/openapi.json", "/docs", "/docs/swagger-ui.css"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s 应当 405, got %d", path, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != http.MethodGet {
			t.Fatalf("POST %s 的 Allow 头不对: %q", path, got)
		}
	}
}

// pageRefRe 抽出文档页里的资源引用：<script ... src="..."> 与
// <link ... href="..."> 的**属性值**。
//
// 只认属性值，不做整页子串匹配：一行 HTML 注释不该能满足断言。匹配时
// 跳过 HTML 注释（注释里的内容不算页面引用）。
var pageRefRe = regexp.MustCompile(`(?is)<(?:script|link)\b[^>]*?\b(?:src|href)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// pageSpecURLRe 抽出 SwaggerUIBundle 配置里的 url 选项：规范本体的地址。
// 它不是 HTML 属性，但同样是页面引用的资源，必须一起守住。
var pageSpecURLRe = regexp.MustCompile(`(?m)\burl\s*:\s*(?:"([^"]*)"|'([^']*)')`)

// htmlCommentRe 匹配 HTML 注释。
var htmlCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)

// pageLocalPrefixes 是页面里允许引用的本地路径前缀。
var pageLocalPrefixes = []string{"/docs/", "/openapi.json", "/openapi.yaml"}

// TestDocsPageReferencesLocalAssets 验证文档页不引外链资源：静态资源走
// 同源绝对路径 /docs/...，规范本体走同源 /openapi.json。
//
// 判定只看属性值，不做子串匹配；协议相对 URL（//cdn...）、大写 scheme、
// data: URI、相对路径都必须被逮住。
func TestDocsPageReferencesLocalAssets(t *testing.T) {
	mux := docsMux()
	rec := doGet(mux, "/docs")
	if rec.Code != http.StatusOK {
		t.Fatalf("应当 200, got %d", rec.Code)
	}
	page := rec.Body.String()

	// 兜底：整页不该出现任何 http(s) 链接。这一条挡不住协议相对 URL，
	// 但能挡住最常见的 CDN 回潮。
	if strings.Contains(page, "http://") || strings.Contains(page, "https://") {
		t.Fatalf("文档页里有外链资源:\n%s", page)
	}

	refs := pageRefs(page)
	if len(refs) == 0 {
		t.Fatalf("文档页里没解析出任何资源引用:\n%s", page)
	}
	for _, ref := range refs {
		checkLocalRef(t, ref)
	}

	// 正向断言：页面引用的资源集合必须**恰好**是期望的那些。
	// 少一个说明功能坏了，多一个说明有人塞进了没登记的资源。
	want := []string{
		"/docs/swagger-ui.css",
		"/docs/swagger-ui-bundle.js",
		"/docs/swagger-ui-standalone-preset.js",
		"/docs/favicon-32x32.png",
		"/openapi.json",
	}
	got := append([]string(nil), refs...)
	sort.Strings(got)
	if !sameSet(want, got) {
		t.Fatalf("文档页引用的资源不对:\n实际 = %v\n期望 = %v", got, want)
	}
}

// pageRefs 抽出页面引用的全部资源地址：script/link 的 src/href 属性值，
// 外加 SwaggerUIBundle 配置里的规范地址。
func pageRefs(page string) []string {
	stripped := htmlCommentRe.ReplaceAllString(page, "")
	var out []string
	for _, m := range pageRefRe.FindAllStringSubmatch(stripped, -1) {
		out = append(out, firstNonEmpty(m[1], m[2]))
	}
	for _, m := range pageSpecURLRe.FindAllStringSubmatch(stripped, -1) {
		out = append(out, firstNonEmpty(m[1], m[2]))
	}
	return out
}

// checkLocalRef 判定一个资源引用必须是同源的本地绝对路径。
func checkLocalRef(t *testing.T, ref string) {
	t.Helper()
	switch {
	case ref == "":
		t.Fatal("页面里有空的资源引用")
	case !strings.HasPrefix(ref, "/"):
		t.Fatalf("资源引用 %q 不是绝对路径（相对路径在 /docs 下会解析到 / 下面去）", ref)
	case strings.HasPrefix(ref, "//"):
		t.Fatalf("资源引用 %q 是协议相对 URL", ref)
	case strings.Contains(ref, ":"):
		t.Fatalf("资源引用 %q 带 scheme（http:/https:/data: 等）", ref)
	}
	for _, p := range pageLocalPrefixes {
		if strings.HasPrefix(ref, p) {
			return
		}
	}
	t.Fatalf("资源引用 %q 不在允许的本地路径前缀里（允许：%v）", ref, pageLocalPrefixes)
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// TestDocsAssetsMatchPageReferences 双向核对白名单与页面引用：页面引用的
// 每个静态资源都要在白名单里，白名单里的每个资源都要被页面引用。
func TestDocsAssetsMatchPageReferences(t *testing.T) {
	page := doGet(docsMux(), "/docs").Body.String()
	referenced := map[string]bool{}
	for _, ref := range pageRefs(page) {
		if !strings.HasPrefix(ref, docsPrefix) {
			continue
		}
		referenced[strings.TrimPrefix(ref, docsPrefix)] = true
	}
	for _, a := range docsAssets {
		if !referenced[a.name] {
			t.Fatalf("白名单里的资源 %q 没有被文档页引用", a.name)
		}
	}
	for name := range referenced {
		found := false
		for _, a := range docsAssets {
			if a.name == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("文档页引用了白名单外的资源 %q", name)
		}
	}
}

// TestDocsStaticAssetsHaveFixedPrefix 是静态资源前缀的回归测试。
//
// 先前静态资源由 httpSwagger.WrapHandler 提供，它是包级单例，内部用
// sync.Once 把**进程级**的 swaggerFiles.Handler.Prefix 锁成「首个到达请求」
// 解析出的前缀。一次 GET /docs/?x=1 就能把前缀锁成 "/" ，此后本进程内的
// 静态资源全部 404。现在前缀是写死的，谁先打都不影响。
func TestDocsStaticAssetsHaveFixedPrefix(t *testing.T) {
	mux := docsMux()
	// 先打带查询串的目录请求。
	rec := doGet(mux, "/docs/?x=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /docs/?x=1 应当 200, got %d", rec.Code)
	}
	// 再打静态资源：必须仍然是 200 且有内容。
	for _, a := range docsAssets {
		rec := doGet(mux, docsPrefix+a.name)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s 应当 200, got %d", docsPrefix+a.name, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("GET %s 的响应体是空的", docsPrefix+a.name)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, a.contentType) {
			t.Fatalf("GET %s 的 Content-Type 不对: %q（期望 %q）", docsPrefix+a.name, ct, a.contentType)
		}
	}
	// 换个顺序再来一遍：资源先到，结果必须一样。
	mux2 := docsMux()
	for _, a := range docsAssets {
		if rec := doGet(mux2, docsPrefix+a.name); rec.Code != http.StatusOK {
			t.Fatalf("GET %s 应当 200, got %d", docsPrefix+a.name, rec.Code)
		}
	}
	if rec := doGet(mux2, "/docs/?x=2"); rec.Code != http.StatusOK {
		t.Fatalf("GET /docs/?x=2 应当 200, got %d", rec.Code)
	}
}

// TestDocsPathContract 核对文档端点的实际行为与端点契约一致。
func TestDocsPathContract(t *testing.T) {
	mux := docsMux()
	cases := []struct {
		path        string
		wantCode    int
		contentType string
	}{
		{"/docs", http.StatusOK, "text/html"},
		{"/docs/", http.StatusOK, "text/html"},
		{"/openapi.yaml", http.StatusOK, "application/yaml"},
		{"/openapi.json", http.StatusOK, "application/json"},
		{"/docs/swagger-ui.css", http.StatusOK, "text/css"},
		{"/docs/swagger-ui-bundle.js", http.StatusOK, "javascript"},
		// 白名单外的路径一律 404。/docs/doc.json 先前由 http-swagger 内建
		// 路径处理，稳定吐 500；/docs/index.html 与 /docs/swagger-initializer.js
		// 是 swagger-ui 自带的资源，不该被端出去。
		{"/docs/doc.json", http.StatusNotFound, ""},
		{"/docs/index.html", http.StatusNotFound, ""},
		{"/docs/swagger-initializer.js", http.StatusNotFound, ""},
		{"/docs/nope.txt", http.StatusNotFound, ""},
	}
	for _, c := range cases {
		rec := doGet(mux, c.path)
		if rec.Code != c.wantCode {
			t.Fatalf("GET %s 应当 %d, got %d", c.path, c.wantCode, rec.Code)
		}
		if c.contentType != "" {
			if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, c.contentType) {
				t.Fatalf("GET %s 的 Content-Type 不对: %q（期望含 %q）", c.path, ct, c.contentType)
			}
		}
	}
	// /docs 与 /docs/ 必须是同一个页面：尾斜杠差异不该造成契约漂移。
	if a, b := doGet(mux, "/docs").Body.String(), doGet(mux, "/docs/").Body.String(); a != b {
		t.Fatal("/docs 与 /docs/ 返回的页面不一致")
	}
}

// TestOpenAPIJSONFailsGracefully 验证 YAML→JSON 转换失败时端点回 500，
// 而不是把请求打崩。
func TestOpenAPIJSONFailsGracefully(t *testing.T) {
	// 坏 YAML：转换必须返回错误。
	if _, err := yamlToJSON([]byte("\tbad: [unclosed")); err == nil {
		t.Fatal("坏 YAML 应当转换失败")
	}
	// 端点在转换失败时回 500。
	h := serveOpenAPIJSON(func() ([]byte, error) {
		return nil, errors.New("炸了")
	})
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("转换失败应当 500, got %d", rec.Code)
	}
}

// docsMux 造一个带文档端点的 mux。文档端点不参与鉴权，这里照常给业务端点
// 设 token，确保鉴权没被误挂到文档端点上。
func docsMux() *http.ServeMux {
	h := &stubHandler{events: make(chan api.Event), controls: make(chan api.Message)}
	return newMux(h, "secret")
}

// doGet 发起一次不带 Authorization 头的 GET 请求。
func doGet(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// parseSpec 解析规范（转成 JSON 之后的形态）。
func parseSpec(t *testing.T) map[string]any {
	t.Helper()
	raw, err := openAPIJSON()
	if err != nil {
		t.Fatalf("规范转 JSON 失败: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("规范不是合法 JSON: %v", err)
	}
	return doc
}

// mustJSON 返回规范的 JSON 形态。
func mustJSON(t *testing.T) []byte {
	t.Helper()
	raw, err := openAPIJSON()
	if err != nil {
		t.Fatalf("规范转 JSON 失败: %v", err)
	}
	return raw
}

// specPaths 取规范里的 paths。
func specPaths(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatal("规范里没有 paths")
	}
	return paths
}

// specSchemas 取规范里的 components.schemas。
func specSchemas(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	components, ok := doc["components"].(map[string]any)
	if !ok {
		t.Fatal("规范里没有 components")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("规范里没有 components.schemas")
	}
	return schemas
}

// jsonFieldName 返回结构体字段的 JSON 名字；没有 json tag 的字段返回空串。
func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	name := strings.Split(tag, ",")[0]
	if name == "-" {
		return ""
	}
	return name
}

// jsonFields 返回结构体的 json tag 字段名集合（排序后）。
func jsonFields(t reflect.Type) []string {
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name := jsonFieldName(t.Field(i))
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// sameSet 比较两个字符串集合是否相同（多重集语义）。
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]int, len(a))
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

// timeType 是 time.Time 的类型，用来把时间戳字段识别成 date-time 字符串。
var timeType = reflect.TypeOf(time.Time{})

// schemaShape 描述一个 OpenAPI schema 节点的类型信息。
type schemaShape struct {
	ref    string       // $ref 指向的 schema 名
	kind   string       // type：string/boolean/integer/number/array/object
	format string       // format
	items  *schemaShape // array 的元素类型
}

// parseShape 从规范的 JSON 形态里读出一个 schema 节点的类型信息。
func parseShape(v any) schemaShape {
	m, ok := v.(map[string]any)
	if !ok {
		return schemaShape{}
	}
	var s schemaShape
	if ref, ok := m["$ref"].(string); ok {
		s.ref = ref[strings.LastIndex(ref, "/")+1:]
		return s
	}
	s.kind, _ = m["type"].(string)
	s.format, _ = m["format"].(string)
	if it, ok := m["items"]; ok {
		child := parseShape(it)
		s.items = &child
	}
	return s
}

// expectShape 按 Go 类型推出应有的 OpenAPI 类型。映射规则：
// string→string、bool→boolean、整数→integer、浮点→number、
// 切片→array、time.Time→string(date-time)、嵌套结构体→$ref。
func expectShape(t reflect.Type) schemaShape {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == timeType {
		return schemaShape{kind: "string", format: "date-time"}
	}
	switch t.Kind() {
	case reflect.String:
		return schemaShape{kind: "string"}
	case reflect.Bool:
		return schemaShape{kind: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return schemaShape{kind: "integer", format: integerFormat(t)}
	case reflect.Float32, reflect.Float64:
		return schemaShape{kind: "number"}
	case reflect.Slice, reflect.Array:
		item := expectShape(t.Elem())
		return schemaShape{kind: "array", items: &item}
	case reflect.Struct:
		return schemaShape{ref: t.Name()}
	default:
		return schemaShape{}
	}
}

// integerFormat 返回整数类型在 OpenAPI 里应当声明的 format。
//
// int64 必须写 int64、int32 必须写 int32——把 int64 写成 int32 会让生成
// 出来的客户端按 32 位宽度截断。Go 的 int 与更窄的整型宽度不稳定（int
// 随平台变），不强制声明 format。
func integerFormat(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Int64:
		return "int64"
	case reflect.Int32:
		return "int32"
	default:
		return ""
	}
}

// checkShape 比对期望类型与规范里的类型。
func checkShape(t *testing.T, field string, want, got schemaShape) {
	t.Helper()
	if want.ref != "" {
		if got.ref != want.ref {
			t.Fatalf("字段 %s 应当 $ref %q，规范里是 %q", field, want.ref, got.ref)
		}
		return
	}
	if got.ref != "" {
		t.Fatalf("字段 %s 应当是 %s，规范里却是 $ref %q", field, want.kind, got.ref)
	}
	if got.kind != want.kind {
		t.Fatalf("字段 %s 的 type 应当是 %q，规范里是 %q", field, want.kind, got.kind)
	}
	if want.format == "date-time" {
		if got.format != "date-time" {
			t.Fatalf("字段 %s 应当声明 format: date-time，规范里是 %q", field, got.format)
		}
	} else if got.format == "date-time" {
		t.Fatalf("字段 %s 不是时间戳，不该声明 format: date-time", field)
	}
	if want.kind == "integer" {
		switch {
		case want.format != "":
			if got.format != want.format {
				t.Fatalf("字段 %s 应当声明 format: %s，规范里是 %q", field, want.format, got.format)
			}
		case got.format != "" && got.format != "int32" && got.format != "int64":
			t.Fatalf("字段 %s 的 format %q 不是合法的整数 format", field, got.format)
		}
	}
	if want.kind == "array" {
		if got.items == nil {
			t.Fatalf("字段 %s 是数组，规范里没有 items", field)
		}
		checkShape(t, field+"[]", *want.items, *got.items)
	}
}

// collectRefs 递归收集文档里所有的 $ref。
func collectRefs(v any, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if k == "$ref" {
				if s, ok := val.(string); ok {
					out[s] = true
				}
				continue
			}
			collectRefs(val, out)
		}
	case []any:
		for _, item := range x {
			collectRefs(item, out)
		}
	}
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/rpc/httpx/`
Expected: PASS。若 `TestOpenAPISchemasMatchDTO` 失败，按提示补齐 YAML 里的字段或修正 DTO。

- [ ] **Step 6: 提交**

```bash
git add internal/rpc/httpx go.mod go.sum
git commit -m "feat(httpx): 增加 OpenAPI 规范与 Swagger 文档页"
```

#### 与初稿的差异（B1–B11 的落法）

| 条目 | 初稿的问题 | 最终实现 |
| --- | --- | --- |
| B1 | `docs.go` 用 `https://unpkg.com/swagger-ui-dist@5/...` 的 CDN 链接，与设计文档 §6.5「离线嵌入」冲突 | 改用 `github.com/swaggo/files`（修复轮 1 起不再用 `http-swagger`），swagger-ui 静态资源打进二进制；页面里没有任何外链资源（有测试守着） |
| B2 | 直接用 `httpSwagger.Handler` 渲染 `/docs`，其模板用相对路径 `./swagger-ui.css` 引资源；页面挂在 `/docs`（无尾斜杠）时浏览器会解析到 `/swagger-ui.css` → 404 | `/docs` 自己渲染 HTML，资源用绝对路径 `/docs/...` 引；`/docs/` 按**固定前缀 + 白名单**提供离线静态资源（修复轮 1 之前是 `httpSwagger.WrapHandler`，它的包级单例会污染进程级前缀，见「文档端点契约」）；不引 `swagger-initializer.js`（它默认指向 petstore 的远程 URL） |
| B3 | 规范只声明 200 与 401，与实现对不上 | 每个业务端点都声明 200 / 400 / 401 / 405 / 500，错误响应在 `components.responses` 里复用 `BadRequest` / `Unauthorized` / `MethodNotAllowed` / `InternalError`；有测试逐个端点核对 |
| B4 | 一致性测试只覆盖 7 个 DTO | 覆盖全部 17 个有 schema 的 DTO（多出 `ShowAddTaskParams`），字段名双向核对；另加一条「规范里的 schema 都必须在 DTO 登记表里」的反向断言 |
| B5 | YAML→JSON 用 `go.yaml.in/yaml/v3` | 不变——它本来就是项目的直接依赖，不算新增依赖 |
| B6 | `newMux` 还没有文档端点 | 在 `newMux` 里 `mux := http.NewServeMux()` 之后调 `registerDocs(mux)`；四个文档端点都不进 `authed` 中间件 |
| B7 | `registerDocs` 用 `mux.HandleFunc` 不限方法 | 文档端点只接受 GET，其他方法 405 + `Allow: GET`（`getOnly` 中间件），错误体沿用统一形状 |
| B8 | `openAPIJSON` 用 `sync.Once` 缓存 | 不变；转换抽成 `yamlToJSON`，失败时返回错误，端点回 500 而不是 panic（有测试注入失败验证） |
| B9 | 只比字段名集合 | 增加类型级护栏：按 `reflect` 推出的 Go 类型逐字段比对 OpenAPI 的 `type`（string / boolean / integer / number / array / `$ref`），`time.Time` 额外要求 `format: date-time`（修复轮 1 起，整数也校验 `format`：`int64` ↔ `int64`、`int32` ↔ `int32`） |
| B10 | 依赖与许可证头约束 | 只 import `internal/api`、`internal/rpc/addr`、标准库与两个 swaggo 包；新文件都带 MPL-2.0 许可证头 |
| B11 | 测试清单不全 | 补齐 8 类测试：解析、方法双向覆盖、错误码声明、字段名双向核对、schema 覆盖面、类型核对、`$ref` 完整性、文档端点免鉴权 / 只收 GET / 不引外链 / 转换失败回 500。修复轮 1 又补了：路径↔`api.Spec` 的 schema 映射、`requestBody` 可选、`Message.kind` 的 enum ↔ `api.MessageKind*` 常量、文档页引用的**属性值**判定、静态资源固定前缀、白名单 ↔ 页面引用双向核对、`/docs/*` 的 404 契约 |

除 B1–B11 外还有两处与初稿不同：

- **`servers[].url` 改成 `/`**（初稿是 `http://lgdm/lgdm.v1.DownloadService`）。
  OpenAPI 的最终 URL 是 `servers[].url` 拼上 `paths` 的键，初稿那样拼会把
  `/lgdm.v1.DownloadService` 重复两遍。改成同源相对地址后，
  「Try it out」在 TCP 部署下正好打到文档页的同源。
- **`Message.show_add_task` 改成 `$ref: ShowAddTaskParams`**（初稿是内联对象）。
  这样「嵌套结构体 → `$ref`」的映射规则对所有 DTO 一致，类型级护栏也能
  统一处理；`ShowAddTaskParams` 因此也进了 DTO 登记表。

#### 依赖说明

新增两个直接依赖：

```
github.com/swaggo/http-swagger v1.3.4   // Swagger UI 页面与静态资源（离线嵌入）
github.com/swaggo/files v1.0.1          // swagger-ui 静态资源本体（间接）
```

> **修复轮 1 的变化**：`internal/rpc/httpx` 不再 import
> `github.com/swaggo/http-swagger` 了。静态资源原先由它的包级单例
> `httpSwagger.WrapHandler` 提供，而那个单例会把进程级的
> `swaggerFiles.Handler.Prefix` 锁死成首个请求解析出的前缀（详见 Task 6
> 章节的「文档端点契约」）。改成自己按固定前缀 + 白名单读
> `github.com/swaggo/files` 的内存文件系统之后，`http-swagger` 就没用了：
> `github.com/swaggo/files v1.0.1` 成了**直接依赖**，
> `github.com/swaggo/http-swagger v1.3.4` 成了**没被引用的直接依赖**。
>
> 按本轮的范围约定，`go.mod`/`go.sum` 没有动——这两行的收口（删掉没用的
> `http-swagger`、把 `files` 从 `// indirect` 升为直接依赖）留给后续的
> `go mod tidy`。

它们经 `swaggo/swag` 间接用到 `golang.org/x/tools`。`golang.org/x/text v0.43.0`
要求 `x/tools v0.51.0`，而 `x/tools v0.51.0` 要求 `x/net v0.59.0`——`x/net`
从 v0.56 起改了 `httpproxy` 的环境变量优先级（小写 `http_proxy` 优先于大写
`HTTP_PROXY`），会打破 `internal/protocol` 里 `TestProxyFuncSystemModeReturnsEnv`
的预期。为免得把 `x/net`/`x/crypto`/`x/sys` 一起抬版本，`go.mod` 里排除了
`x/tools` 的两个新版本：

```
exclude (
	golang.org/x/tools v0.47.0
	golang.org/x/tools v0.51.0
)
```

这样 `x/net` 仍是 v0.47.0、`x/crypto` 仍是 v0.44.0、`x/sys` 仍是 v0.47.0，
`x/tools` 取 v0.39.0（`swaggo/swag` 只用到它的 `go/loader`）。

---

### Task 7: 实现 `internal/rpc/grpcx`（gRPC 传输）

**Files:**
- Create: `proto/lgdm/v1/lgdm.proto`
- Create: `buf.yaml`
- Create: `buf.gen.yaml`
- Create: `scripts/gen_proto.sh`
- Create: `internal/rpc/grpcx/convert.go`
- Create: `internal/rpc/grpcx/server.go`
- Create: `internal/rpc/grpcx/client.go`
- Create: `internal/rpc/grpcx/convert_test.go`
- Create: `internal/rpc/grpcx/grpcx_test.go`
- Create: `internal/rpc/grpcx/contract_test.go`
- Create: `internal/rpc/registry.go`
- Create: `internal/rpc/registry_test.go`
- Create (generated): `internal/rpc/grpcx/pb/lgdm.pb.go`、`internal/rpc/grpcx/pb/lgdm_grpc.pb.go`

**Interfaces:**
- Consumes: `internal/api`、`internal/rpc/addr`
- Produces:
  ```go
  package grpcx
  type Client struct { /* ... */ }
  func Dial(ctx context.Context, a addr.Addr, token string) (*Client, error)
  func Serve(ctx context.Context, a addr.Addr, token string, h Handler) error

  package rpc   // 本任务新增
  type ServerOptions struct { Transport, Addr, Token string }
  func Serve(ctx context.Context, opts ServerOptions, h Handler) error
  func Dial(ctx context.Context, transport, endpoint, token string) (Client, error)
  const TransportGRPC, TransportHTTP string
  ```

#### 与初稿的差异（E1–E8 的落法）

控制器在派发前通读初稿代码块 + 实测 grpc-go v1.85.0 源码后指出 8 处必修缺陷。
初稿的 grpcx 代码块整体作废，本表逐条记录最终实现怎么落的：

| 条目 | 初稿的问题 | 最终实现 |
| --- | --- | --- |
| E1 | `grpcx.Dial` 的 dialer 写的是 `addr.Dial(a)`，gRPC 专门给的 ctx 被丢掉 | dialer 改成 `addr.DialContext(ctx, a)`（用 gRPC 传进来的请求级 ctx） |
| E2 | `Dial` 的 ctx 是死参数，且 `grpc.NewClient` 是惰性连接，「业务进程没起来」要等到第一次调用才暴露 | `Dial` 先用 ctx 预连接一次再立刻断开，失败直接返回错误——与 httpx 的 A1 对齐，两种传输的失败时机一致 |
| E3 | `messageToPB` 里 `*m.Event` 直接解引用，`api.Message.Event` 为 nil 时 panic | `messageToPB` 先判 `m.Event == nil`，回一个空事件并保留消息类别，不炸进程 |
| E4 | `StreamMessages` 里事件通道一关就 `return nil`，把控制消息流一起掐掉 | 改成 `events = nil` / `controls = nil`：一侧关闭只停那一侧，与 httpx 的 A9 一致 |
| E5 | `Close()` 直接转调 `conn.Close()`，第二次调用返回 `ErrClientConnClosing`（非 nil），违反 `rpc.Transport` 的幂等契约 | 用 `sync.Once` 包住 `Close`，重复调用一律返回 nil |
| E6 | `Call` 里 `in.(*api.ListParams)` 之类裸断言，类型不对直接 panic | 全部改成带 ok 检查的两值断言（泛型小助手 `asPtr[T]`），类型不对返回错误 |
| E7 | ctx 取消时调 `srv.GracefulStop()`，它会等所有在跑的 RPC 结束，而 `StreamMessages` 是长连接流 → `Serve` 永久阻塞 | ctx 取消时调 `srv.Stop()`（立即取消所有活跃 RPC/流），与 httpx 的 `srv.Close()` 语义一致；另加 `done` channel 收尾监视 goroutine，修掉 httpx 的 A8 泄漏 |
| E8 | proto 里 `ListRequest.filter` / `sort` 是裸 string，`filterToPB` / `filterFromPB` 两张表因此没有任何调用点，是死代码 | proto 改成 `StatusFilter` / `TaskSort` 枚举；六张映射表全部双向显式写出（含 `UNSPECIFIED → ""`），并由 `TestEnumTablesCoverAllConstants` 断言「每张表都有真实调用点」 |

除以上 8 条外，实现还额外做了这些对齐与加固：

- `grpcx` 自己声明一份 `Handler` 接口（方法集与 `rpc.Handler` 逐字一致），
  靠结构化类型对接，避开 `grpcx → rpc` 的包环（E16）。
- 时间字段统一用 RFC3339 字符串（`time.RFC3339Nano`），零值 `time.Time{}`
  格式化成 `"0001-01-01T00:00:00Z"` 而不是空串——空串会让「零值」与
  「没设置」混成一团（E14）。
- `Client.Call` 在发 RPC **之前**就校验请求与响应的指针类型，类型不对
  不会白跑一趟服务端。
- `grpcx` 的 `Client` 额外处理了 nil 接收者与零值客户端：回错误而不是 panic。
- 鉴权 metadata 的 key 一律小写 `authorization`（E10）；gRPC 的 metadata 值
  只允许可打印 ASCII，实际部署的 token 是随机数的十六进制编码，纯 ASCII，
  这个差异在 `TestNonASCIITokenIsRejected` 里钉死。

#### 依赖钉版：为什么 exclude 压不住、必须用 replace

新增 `google.golang.org/grpc v1.85.0` 会把 `golang.org/x/net` 抬到
**v0.60.0**——不是经由 `x/tools` 间接抬上来的，而是 grpc 自己的 go.mod
**直接 require** 的：

```
google.golang.org/grpc v1.85.0
  require golang.org/x/net v0.60.0
```

x/net v0.56 起 `httpproxy` 改了大小写环境变量的优先级（小写 `http_proxy`
优先于大写 `HTTP_PROXY`），与 Go 标准库 `http.ProxyFromEnvironment` 的
**大写优先**行为不一致，会悄悄改变 `internal/protocol` 的代理环境变量解析
语义，打破 `TestProxyFuncSystemModeReturnsEnv`。

- Task 6 用的 `exclude golang.org/x/tools` **压不住**：x/tools 只被
  `swaggo/swag` 间接用到，跟 grpc 没有关系；而且 exclude 的语义是「改选
  下一个可用版本」，实测会把 x/net 选到 v0.58.0——照样在 v0.56 以上。
- 所以只能用 `replace` 硬钉：

```
replace golang.org/x/net => golang.org/x/net v0.47.0
```

grpc 实际 import 的 x/net 包只有 `http2`、`http2/hpack`、`http/httpproxy`、
`trace`，v0.47.0 全都有，实测构建与全部测试通过。

- [ ] **Step 1: 写 `proto/lgdm/v1/lgdm.proto`**

```proto
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

syntax = "proto3";

package lgdm.v1;

option go_package = "lgo_download_manager/internal/rpc/grpcx/pb;pb";

// DownloadService 是业务主进程暴露给 UI 子进程的下载管理服务。
// 与 internal/api 的契约一一对应：每个 rpc 对应一个 api.Method* 常量，
// 请求/响应消息与 api 层的 DTO 一一对应。
//
// 服务名与 httpx 传输的 URL 前缀（/lgdm.v1.DownloadService/）保持一致，
// 两种传输对外暴露的方法集合因此完全相同。
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
  rpc BTTaskDetail(BTInfoRequest) returns (BTDetailResponse);
  rpc StreamMessages(Empty) returns (stream Message);
}

// Status 反映 Task 的生命周期状态。取值与 internal/store 完全一致。
enum Status {
  STATUS_UNSPECIFIED = 0;
  STATUS_PENDING = 1;
  STATUS_DOWNLOADING = 2;
  STATUS_PAUSED = 3;
  STATUS_COMPLETED = 4;
  STATUS_FAILED = 5;
  STATUS_FILE_LOST = 6;
}

// StatusFilter 是任务列表的筛选条件。UNSPECIFIED 表示不过滤。
enum StatusFilter {
  STATUS_FILTER_UNSPECIFIED = 0;
  STATUS_FILTER_ALL = 1;
  STATUS_FILTER_PENDING = 2;
  STATUS_FILTER_DOWNLOADING = 3;
  STATUS_FILTER_PAUSED = 4;
  STATUS_FILTER_COMPLETED = 5;
  STATUS_FILTER_FAILED = 6;
  STATUS_FILTER_FILE_LOST = 7;
}

// TaskSort 是任务列表的排序方式。UNSPECIFIED 表示默认排序。
enum TaskSort {
  TASK_SORT_UNSPECIFIED = 0;
  TASK_SORT_CREATED_DESC = 1;
  TASK_SORT_CREATED_ASC = 2;
  TASK_SORT_NAME_ASC = 3;
  TASK_SORT_NAME_DESC = 4;
}

// ProxyMode 是代理模式。
enum ProxyMode {
  PROXY_MODE_UNSPECIFIED = 0;
  PROXY_MODE_SYSTEM = 1;
  PROXY_MODE_DISABLED = 2;
  PROXY_MODE_MANUAL = 3;
}

// ProtocolKind 是任务所属的传输协议。
enum ProtocolKind {
  PROTOCOL_KIND_UNSPECIFIED = 0;
  PROTOCOL_KIND_HTTP = 1;
  PROTOCOL_KIND_HTTPS = 2;
  PROTOCOL_KIND_FTP = 3;
  PROTOCOL_KIND_WEBDAV = 4;
  PROTOCOL_KIND_BT = 5;
}

// Empty 是无负载请求/响应的占位类型。
message Empty {}

// TaskRef 引用单个任务。
message TaskRef {
  string id = 1;
}

// BoolResponse 是布尔结果。
message BoolResponse {
  bool value = 1;
}

// ProbeRequest 是体积探测的请求。
message ProbeRequest {
  string url = 1;
}

// ProbeResponse 是体积探测的结果。
message ProbeResponse {
  int64 total_size = 1;
}

// ListRequest 是列表查询参数。filter 与 sort 用枚举而不是裸字符串：
// gRPC 传输的价值就在类型安全，用字符串会浪费 protobuf 的类型系统。
// UNSPECIFIED 分别对应「不过滤」与「默认排序」，与 HTTP 传输传空字符串
// 的语义一致。
message ListRequest {
  StatusFilter filter = 1;
  TaskSort sort = 2;
}

// ListResponse 是列表查询的结果。
message ListResponse {
  repeated Task tasks = 1;
}

// AuthOptions 是随任务一起传递的凭据。
message AuthOptions {
  string username = 1;
  string password = 2;
  string user_agent = 3;
  string cookies = 4;
  string referer = 5;
  bool ftp_passive = 6;
  ProxyMode proxy_mode = 7;
  string proxy_url = 8;
  string proxy_bypass = 9;
  string proxy_auto_config_url = 10;
}

// AddTaskRequest 是「添加下载任务」的请求。
message AddTaskRequest {
  string url = 1;
  string save_path = 2;
  ProtocolKind protocol = 3;
  AuthOptions auth = 4;
  int32 chunk_count = 5;
  int64 min_chunk_size = 6;
}

// AddTaskResponse 返回新建的任务。
message AddTaskResponse {
  Task task = 1;
}

// Task 是任务的对外视图。
message Task {
  string id = 1;
  string url = 2;
  string save_path = 3;
  string protocol = 4;
  int64 total_size = 5;
  int64 downloaded = 6;
  bool support_range = 7;
  string etag = 8;
  string last_modified = 9;
  string digest_algorithm = 10;
  string digest = 11;
  bool is_allocated = 12;
  int32 chunk_count = 13;
  int64 min_chunk_size = 14;
  repeated int64 chunk_progress = 15;
  repeated int64 chunk_ranges = 16;
  Status status = 17;
  string auth_data = 18;
  string error_message = 19;
  string error_kind = 20;
  // 时间字段用 RFC3339 字符串，而不是 google.protobuf.Timestamp：
  // 避免为一个时间戳引入额外的 proto 依赖。
  string created_at = 21;
  string updated_at = 22;
  string completed_at = 23;
  string bt_file = 24;
}

// Settings 是权威设置快照。
message Settings {
  string default_save_dir = 1;
  int32 default_threads = 2;
  int64 min_chunk_size = 3;
  string user_agent = 4;
  string cookies = 5;
  bool ftp_passive = 6;
  bool prealloc = 7;
  TaskSort task_sort = 8;
  ProxyMode proxy_mode = 9;
  string proxy_url = 10;
  string proxy_bypass = 11;
  string proxy_auto_config_url = 12;
  int32 download_limit_kib = 13;
  bool light_mode = 14;
  int32 max_concurrent = 15;
  bool auto_start = 16;
  string language = 17;
  int32 bt_port = 18;
  int32 bt_max_peers = 19;
  int32 bt_download_limit_kib = 20;
  int32 bt_upload_limit_kib = 21;
  bool bt_dht = 22;
  bool bt_pex = 23;
  double bt_seed_ratio = 24;
  int32 bt_max_seeding = 25;
  int32 bt_max_concurrent = 26;
  string bt_trackers = 27;
}

// GetSettingsResponse 返回设置快照。
message GetSettingsResponse {
  Settings settings = 1;
}

// SaveSettingsRequest 是保存设置的请求。
message SaveSettingsRequest {
  Settings settings = 1;
}

// BTInfoRequest 是 BT 详情查询的请求。
message BTInfoRequest {
  string task_id = 1;
}

// FileProgress 是种子内单个文件的进度。
message FileProgress {
  string path = 1;
  int64 length = 2;
  int64 completed = 3;
}

// BTDetail 是 BT 任务的实时详情快照。
message BTDetail {
  string name = 1;
  string info_hash = 2;
  int64 total = 3;
  int64 downloaded = 4;
  int64 uploaded = 5;
  double speed_bps = 6;
  double upload_bps = 7;
  int32 peers = 8;
  int32 total_peers = 9;
  int32 seeders = 10;
  int32 leechers = 11;
  int64 piece_length = 12;
  int32 pieces = 13;
  int32 pieces_done = 14;
  repeated uint32 piece_buckets = 15;
  bool seeding = 16;
  bool complete = 17;
  repeated FileProgress files = 18;
  repeated string trackers = 19;
}

// BTDetailResponse 返回 BT 详情。
message BTDetailResponse {
  BTDetail detail = 1;
}

// Event 是一条任务事件。
message Event {
  string why = 1;
  Task task = 2;
  double speed_bps = 3;
}

// ShowAddTaskParams 描述一次「请 UI 弹出新建下载任务对话框」的请求。
message ShowAddTaskParams {
  string url = 1;
  string name = 2;
  string ua = 3;
  string cookies = 4;
}

// Message 是服务端推送的一条消息。事件与控制消息合并在同一条流里。
message Message {
  oneof payload {
    Event event = 1;
    bool show = 2;
    ShowAddTaskParams show_add_task = 3;
    string language = 4;
    bool close = 5;
  }
}
```


时间字段用 RFC3339 字符串（而不是 `google.protobuf.Timestamp`）——避免为时间戳引入额外的
proto 依赖，转换时统一 `time.Time.Format(time.RFC3339Nano)`。

`ListRequest` 的 `filter` / `sort` 用枚举而不是裸字符串（E8）：gRPC 传输的价值
就在类型安全，用字符串会浪费 protobuf 的类型系统。`UNSPECIFIED` 分别对应
「不过滤」与「默认排序」，与 HTTP 传输传空字符串的语义一致
（`store.StatusFilter("")` 走 default 不过滤，`store.TaskSort("")` 走 default
即 `created_at DESC`）。

- [ ] **Step 2: 建 buf 配置与生成脚本**

`buf.yaml`：

```yaml
version: v2
modules:
  - path: proto
lint:
  use:
    - STANDARD
  # 这三条规则要求「每个 RPC 用自己专属的请求/响应消息，且消息名带上
  # 方法名」。这里刻意不遵守，代价是 proto 里少一堆只有一个字段的样板
  # 消息：
  #   - TaskRef 被 start/pause/delete/is_preparing 四个方法共用；
  #   - Empty 是无负载请求/响应的占位类型，被 get_settings、
  #     save_settings、stream_messages 等方法共用。
  # 契约层的形状由 internal/api 决定，proto 只是它的线格式投影，
  # 不该在这里再复制一遍方法名。其余 STANDARD 规则（字段名、枚举名、
  # 枚举零值必须带 _UNSPECIFIED、包名与目录路径一致等）照常生效。
  except:
    - RPC_REQUEST_STANDARD_NAME
    - RPC_RESPONSE_STANDARD_NAME
    - RPC_REQUEST_RESPONSE_UNIQUE
breaking:
  use:
    - FILE
```


`buf.gen.yaml`：

```yaml
version: v2
managed:
  enabled: false
plugins:
  - local: protoc-gen-go
    out: .
    opt: module=lgo_download_manager
  - local: protoc-gen-go-grpc
    out: .
    opt: module=lgo_download_manager
```


`scripts/gen_proto.sh`：

```bash
#!/usr/bin/env bash
# 生成 protobuf 代码。生成物入库，CI 只校验同步性。
#
# 用 buf generate 而不是直接调 protoc：本机没装 protoc，而 buf 自带
# 解析器，还能顺手跑 lint（buf lint）。
#
# protoc-gen-go / protoc-gen-go-grpc 默认装在 $(go env GOPATH)/bin，
# 不一定在 PATH 上，这里显式补上。
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$(go env GOPATH)/bin:$PATH"
buf generate
gofmt -w internal/rpc/grpcx/pb
```


```bash
chmod +x scripts/gen_proto.sh
```

- [ ] **Step 3: 生成 pb 代码**

Run: `PATH="$(go env GOPATH)/bin:$PATH" ./scripts/gen_proto.sh`
Expected: 生成 `internal/rpc/grpcx/pb/lgdm.pb.go` 与 `internal/rpc/grpcx/pb/lgdm_grpc.pb.go`

本机没装 `protoc`，用 `buf generate` 代替（脚本里已经补上
`$(go env GOPATH)/bin` 到 PATH，`protoc-gen-go` / `protoc-gen-go-grpc` 默认装在那里）。

生成物入库，CI 只校验同步性——重新生成后 `git status` 必须干净。

`buf lint` 也能过（`buf.yaml` 里排除了三条规则并写明理由：请求/响应消息
共用会让 proto 里多出一堆只有一个字段的样板消息）。

- [ ] **Step 4: 加依赖**

Run: `go get google.golang.org/grpc@v1.85.0 google.golang.org/protobuf@v1.36.12 && go mod tidy`
Expected: `go.mod` 里出现这两个直接依赖，且 `golang.org/x/net` 被
`replace` 钉回 v0.47.0（见上面的「依赖钉版」）。

- [ ] **Step 5: 建 `internal/rpc/grpcx/convert.go`**

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package grpcx

import (
	"slices"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/grpcx/pb"
)

// 本文件是契约层（internal/api）与 protobuf 之间的唯一转换点：客户端与
// 服务端都走这里的函数，两边不会各自写一份而慢慢走偏。
//
// 枚举映射表一律显式写出 UNSPECIFIED 条目，不依赖「map 缺失键返回零值」
// 这类隐式行为——隐式行为在重构或换语言实现时很容易碎。
//
// 每张映射表都有真实调用点：状态走 Task，筛选与排序走 ListRequest，
// 代理模式走 Settings 与 AuthOptions，协议种类走 AddTaskRequest。

// ---- 状态枚举映射 ----

var statusToPB = map[api.Status]pb.Status{
	"":                    pb.Status_STATUS_UNSPECIFIED,
	api.StatusPending:     pb.Status_STATUS_PENDING,
	api.StatusDownloading: pb.Status_STATUS_DOWNLOADING,
	api.StatusPaused:      pb.Status_STATUS_PAUSED,
	api.StatusCompleted:   pb.Status_STATUS_COMPLETED,
	api.StatusFailed:      pb.Status_STATUS_FAILED,
	api.StatusFileLost:    pb.Status_STATUS_FILE_LOST,
}

var statusFromPB = map[pb.Status]api.Status{
	pb.Status_STATUS_UNSPECIFIED: "",
	pb.Status_STATUS_PENDING:     api.StatusPending,
	pb.Status_STATUS_DOWNLOADING: api.StatusDownloading,
	pb.Status_STATUS_PAUSED:      api.StatusPaused,
	pb.Status_STATUS_COMPLETED:   api.StatusCompleted,
	pb.Status_STATUS_FAILED:      api.StatusFailed,
	pb.Status_STATUS_FILE_LOST:   api.StatusFileLost,
}

// ---- 筛选枚举映射 ----

// 契约层的零值（空串）表示「不过滤」，与 HTTP 传输传空字符串的语义一致。
var filterToPB = map[api.StatusFilter]pb.StatusFilter{
	"":                    pb.StatusFilter_STATUS_FILTER_UNSPECIFIED,
	api.FilterAll:         pb.StatusFilter_STATUS_FILTER_ALL,
	api.FilterPending:     pb.StatusFilter_STATUS_FILTER_PENDING,
	api.FilterDownloading: pb.StatusFilter_STATUS_FILTER_DOWNLOADING,
	api.FilterPaused:      pb.StatusFilter_STATUS_FILTER_PAUSED,
	api.FilterCompleted:   pb.StatusFilter_STATUS_FILTER_COMPLETED,
	api.FilterFailed:      pb.StatusFilter_STATUS_FILTER_FAILED,
	api.FilterFileLost:    pb.StatusFilter_STATUS_FILTER_FILE_LOST,
}

var filterFromPB = map[pb.StatusFilter]api.StatusFilter{
	pb.StatusFilter_STATUS_FILTER_UNSPECIFIED: "",
	pb.StatusFilter_STATUS_FILTER_ALL:         api.FilterAll,
	pb.StatusFilter_STATUS_FILTER_PENDING:     api.FilterPending,
	pb.StatusFilter_STATUS_FILTER_DOWNLOADING: api.FilterDownloading,
	pb.StatusFilter_STATUS_FILTER_PAUSED:      api.FilterPaused,
	pb.StatusFilter_STATUS_FILTER_COMPLETED:   api.FilterCompleted,
	pb.StatusFilter_STATUS_FILTER_FAILED:      api.FilterFailed,
	pb.StatusFilter_STATUS_FILTER_FILE_LOST:   api.FilterFileLost,
}

// ---- 排序枚举映射 ----

// 契约层的零值（空串）表示「默认排序」，与 HTTP 传输传空字符串的语义一致。
var sortToPB = map[api.TaskSort]pb.TaskSort{
	"":                  pb.TaskSort_TASK_SORT_UNSPECIFIED,
	api.SortCreatedDesc: pb.TaskSort_TASK_SORT_CREATED_DESC,
	api.SortCreatedAsc:  pb.TaskSort_TASK_SORT_CREATED_ASC,
	api.SortNameAsc:     pb.TaskSort_TASK_SORT_NAME_ASC,
	api.SortNameDesc:    pb.TaskSort_TASK_SORT_NAME_DESC,
}

var sortFromPB = map[pb.TaskSort]api.TaskSort{
	pb.TaskSort_TASK_SORT_UNSPECIFIED:  "",
	pb.TaskSort_TASK_SORT_CREATED_DESC: api.SortCreatedDesc,
	pb.TaskSort_TASK_SORT_CREATED_ASC:  api.SortCreatedAsc,
	pb.TaskSort_TASK_SORT_NAME_ASC:     api.SortNameAsc,
	pb.TaskSort_TASK_SORT_NAME_DESC:    api.SortNameDesc,
}

// ---- 代理模式映射 ----

var proxyToPB = map[api.ProxyMode]pb.ProxyMode{
	"":                    pb.ProxyMode_PROXY_MODE_UNSPECIFIED,
	api.ProxyModeSystem:   pb.ProxyMode_PROXY_MODE_SYSTEM,
	api.ProxyModeDisabled: pb.ProxyMode_PROXY_MODE_DISABLED,
	api.ProxyModeManual:   pb.ProxyMode_PROXY_MODE_MANUAL,
}

var proxyFromPB = map[pb.ProxyMode]api.ProxyMode{
	pb.ProxyMode_PROXY_MODE_UNSPECIFIED: "",
	pb.ProxyMode_PROXY_MODE_SYSTEM:      api.ProxyModeSystem,
	pb.ProxyMode_PROXY_MODE_DISABLED:    api.ProxyModeDisabled,
	pb.ProxyMode_PROXY_MODE_MANUAL:      api.ProxyModeManual,
}

// ---- 协议种类映射 ----

var kindToPB = map[api.ProtocolKind]pb.ProtocolKind{
	"":              pb.ProtocolKind_PROTOCOL_KIND_UNSPECIFIED,
	api.ProtoHTTP:   pb.ProtocolKind_PROTOCOL_KIND_HTTP,
	api.ProtoHTTPS:  pb.ProtocolKind_PROTOCOL_KIND_HTTPS,
	api.ProtoFTP:    pb.ProtocolKind_PROTOCOL_KIND_FTP,
	api.ProtoWebDAV: pb.ProtocolKind_PROTOCOL_KIND_WEBDAV,
	api.ProtoBT:     pb.ProtocolKind_PROTOCOL_KIND_BT,
}

var kindFromPB = map[pb.ProtocolKind]api.ProtocolKind{
	pb.ProtocolKind_PROTOCOL_KIND_UNSPECIFIED: "",
	pb.ProtocolKind_PROTOCOL_KIND_HTTP:        api.ProtoHTTP,
	pb.ProtocolKind_PROTOCOL_KIND_HTTPS:       api.ProtoHTTPS,
	pb.ProtocolKind_PROTOCOL_KIND_FTP:         api.ProtoFTP,
	pb.ProtocolKind_PROTOCOL_KIND_WEBDAV:      api.ProtoWebDAV,
	pb.ProtocolKind_PROTOCOL_KIND_BT:          api.ProtoBT,
}

// ---- DTO 映射 ----

// listParamsToPB 把列表查询参数转成 proto 请求。
func listParamsToPB(p *api.ListParams) *pb.ListRequest {
	if p == nil {
		return &pb.ListRequest{}
	}
	return &pb.ListRequest{
		Filter: filterToPB[p.Filter],
		Sort:   sortToPB[p.Sort],
	}
}

// listParamsFromPB 把 proto 请求转回列表查询参数。
func listParamsFromPB(r *pb.ListRequest) *api.ListParams {
	if r == nil {
		return &api.ListParams{}
	}
	return &api.ListParams{
		Filter: filterFromPB[r.GetFilter()],
		Sort:   sortFromPB[r.GetSort()],
	}
}

// taskToPB 把任务转成 proto 消息。
func taskToPB(t *api.Task) *pb.Task {
	if t == nil {
		return nil
	}
	return &pb.Task{
		Id:              t.ID,
		Url:             t.URL,
		SavePath:        t.SavePath,
		Protocol:        t.Protocol,
		TotalSize:       t.TotalSize,
		Downloaded:      t.Downloaded,
		SupportRange:    t.SupportRange,
		Etag:            t.ETag,
		LastModified:    t.LastModified,
		DigestAlgorithm: t.DigestAlgorithm,
		Digest:          t.Digest,
		IsAllocated:     t.IsAllocated,
		ChunkCount:      int32(t.ChunkCount),
		MinChunkSize:    t.MinChunkSize,
		ChunkProgress:   slices.Clone(t.ChunkProgress),
		ChunkRanges:     slices.Clone(t.ChunkRanges),
		Status:          statusToPB[t.Status],
		AuthData:        t.AuthData,
		ErrorMessage:    t.ErrorMessage,
		ErrorKind:       t.ErrorKind,
		CreatedAt:       formatTime(t.CreatedAt),
		UpdatedAt:       formatTime(t.UpdatedAt),
		CompletedAt:     formatTime(t.CompletedAt),
		BtFile:          t.BTFile,
	}
}

// taskFromPB 把 proto 消息转回任务。
func taskFromPB(t *pb.Task) *api.Task {
	if t == nil {
		return nil
	}
	return &api.Task{
		ID:              t.GetId(),
		URL:             t.GetUrl(),
		SavePath:        t.GetSavePath(),
		Protocol:        t.GetProtocol(),
		TotalSize:       t.GetTotalSize(),
		Downloaded:      t.GetDownloaded(),
		SupportRange:    t.GetSupportRange(),
		ETag:            t.GetEtag(),
		LastModified:    t.GetLastModified(),
		DigestAlgorithm: t.GetDigestAlgorithm(),
		Digest:          t.GetDigest(),
		IsAllocated:     t.GetIsAllocated(),
		ChunkCount:      int(t.GetChunkCount()),
		MinChunkSize:    t.GetMinChunkSize(),
		ChunkProgress:   nilIfEmpty(t.GetChunkProgress()),
		ChunkRanges:     nilIfEmpty(t.GetChunkRanges()),
		Status:          statusFromPB[t.GetStatus()],
		AuthData:        t.GetAuthData(),
		ErrorMessage:    t.GetErrorMessage(),
		ErrorKind:       t.GetErrorKind(),
		CreatedAt:       parseTime(t.GetCreatedAt()),
		UpdatedAt:       parseTime(t.GetUpdatedAt()),
		CompletedAt:     parseTime(t.GetCompletedAt()),
		BTFile:          t.GetBtFile(),
	}
}

// settingsToPB 把设置转成 proto 消息。
func settingsToPB(s api.Settings) *pb.Settings {
	return &pb.Settings{
		DefaultSaveDir:     s.DefaultSaveDir,
		DefaultThreads:     int32(s.DefaultThreads),
		MinChunkSize:       s.MinChunkSize,
		UserAgent:          s.UserAgent,
		Cookies:            s.Cookies,
		FtpPassive:         s.FTPPassive,
		Prealloc:           s.Prealloc,
		TaskSort:           sortToPB[s.TaskSort],
		ProxyMode:          proxyToPB[s.ProxyMode],
		ProxyUrl:           s.ProxyURL,
		ProxyBypass:        s.ProxyBypass,
		ProxyAutoConfigUrl: s.ProxyAutoConfigURL,
		DownloadLimitKib:   int32(s.DownloadLimitKiB),
		LightMode:          s.LightMode,
		MaxConcurrent:      int32(s.MaxConcurrent),
		AutoStart:          s.AutoStart,
		Language:           s.Language,
		BtPort:             int32(s.BTPort),
		BtMaxPeers:         int32(s.BTMaxPeers),
		BtDownloadLimitKib: int32(s.BTDownloadLimitKiB),
		BtUploadLimitKib:   int32(s.BTUploadLimitKiB),
		BtDht:              s.BTDHT,
		BtPex:              s.BTPEX,
		BtSeedRatio:        s.BTSeedRatio,
		BtMaxSeeding:       int32(s.BTMaxSeeding),
		BtMaxConcurrent:    int32(s.BTMaxConcurrent),
		BtTrackers:         s.BTTrackers,
	}
}

// settingsFromPB 把 proto 消息转回设置。
func settingsFromPB(s *pb.Settings) api.Settings {
	if s == nil {
		return api.Settings{}
	}
	return api.Settings{
		DefaultSaveDir:     s.GetDefaultSaveDir(),
		DefaultThreads:     int(s.GetDefaultThreads()),
		MinChunkSize:       s.GetMinChunkSize(),
		UserAgent:          s.GetUserAgent(),
		Cookies:            s.GetCookies(),
		FTPPassive:         s.GetFtpPassive(),
		Prealloc:           s.GetPrealloc(),
		TaskSort:           sortFromPB[s.GetTaskSort()],
		ProxyMode:          proxyFromPB[s.GetProxyMode()],
		ProxyURL:           s.GetProxyUrl(),
		ProxyBypass:        s.GetProxyBypass(),
		ProxyAutoConfigURL: s.GetProxyAutoConfigUrl(),
		DownloadLimitKiB:   int(s.GetDownloadLimitKib()),
		LightMode:          s.GetLightMode(),
		MaxConcurrent:      int(s.GetMaxConcurrent()),
		AutoStart:          s.GetAutoStart(),
		Language:           s.GetLanguage(),
		BTPort:             int(s.GetBtPort()),
		BTMaxPeers:         int(s.GetBtMaxPeers()),
		BTDownloadLimitKiB: int(s.GetBtDownloadLimitKib()),
		BTUploadLimitKiB:   int(s.GetBtUploadLimitKib()),
		BTDHT:              s.GetBtDht(),
		BTPEX:              s.GetBtPex(),
		BTSeedRatio:        s.GetBtSeedRatio(),
		BTMaxSeeding:       int(s.GetBtMaxSeeding()),
		BTMaxConcurrent:    int(s.GetBtMaxConcurrent()),
		BTTrackers:         s.GetBtTrackers(),
	}
}

// authToPB 把凭据转成 proto 消息。
func authToPB(a api.AuthOptions) *pb.AuthOptions {
	return &pb.AuthOptions{
		Username:           a.Username,
		Password:           a.Password,
		UserAgent:          a.UserAgent,
		Cookies:            a.Cookies,
		Referer:            a.Referer,
		FtpPassive:         a.FTPPassive,
		ProxyMode:          proxyToPB[a.ProxyMode],
		ProxyUrl:           a.ProxyURL,
		ProxyBypass:        a.ProxyBypass,
		ProxyAutoConfigUrl: a.ProxyAutoConfigURL,
	}
}

// authFromPB 把 proto 消息转回凭据。
func authFromPB(a *pb.AuthOptions) api.AuthOptions {
	if a == nil {
		return api.AuthOptions{}
	}
	return api.AuthOptions{
		Username:           a.GetUsername(),
		Password:           a.GetPassword(),
		UserAgent:          a.GetUserAgent(),
		Cookies:            a.GetCookies(),
		Referer:            a.GetReferer(),
		FTPPassive:         a.GetFtpPassive(),
		ProxyMode:          proxyFromPB[a.GetProxyMode()],
		ProxyURL:           a.GetProxyUrl(),
		ProxyBypass:        a.GetProxyBypass(),
		ProxyAutoConfigURL: a.GetProxyAutoConfigUrl(),
	}
}

// addTaskInputToPB 把「添加下载任务」的入参转成 proto 请求。
func addTaskInputToPB(in *api.AddTaskInput) *pb.AddTaskRequest {
	if in == nil {
		return &pb.AddTaskRequest{}
	}
	return &pb.AddTaskRequest{
		Url:          in.URL,
		SavePath:     in.SavePath,
		Protocol:     kindToPB[in.Protocol],
		Auth:         authToPB(in.Auth),
		ChunkCount:   int32(in.ChunkCount),
		MinChunkSize: in.MinChunkSize,
	}
}

// addTaskInputFromPB 把 proto 请求转回「添加下载任务」的入参。
func addTaskInputFromPB(r *pb.AddTaskRequest) *api.AddTaskInput {
	if r == nil {
		return &api.AddTaskInput{}
	}
	return &api.AddTaskInput{
		URL:          r.GetUrl(),
		SavePath:     r.GetSavePath(),
		Protocol:     kindFromPB[r.GetProtocol()],
		Auth:         authFromPB(r.GetAuth()),
		ChunkCount:   int(r.GetChunkCount()),
		MinChunkSize: r.GetMinChunkSize(),
	}
}

// btDetailToPB 把 BT 详情转成 proto 消息。
func btDetailToPB(d *api.BTDetail) *pb.BTDetail {
	if d == nil {
		return nil
	}
	files := make([]*pb.FileProgress, 0, len(d.Files))
	for _, f := range d.Files {
		files = append(files, &pb.FileProgress{
			Path:      f.Path,
			Length:    f.Length,
			Completed: f.Completed,
		})
	}
	buckets := make([]uint32, 0, len(d.PieceBuckets))
	for _, b := range d.PieceBuckets {
		buckets = append(buckets, uint32(b))
	}
	return &pb.BTDetail{
		Name:         d.Name,
		InfoHash:     d.InfoHash,
		Total:        d.Total,
		Downloaded:   d.Downloaded,
		Uploaded:     d.Uploaded,
		SpeedBps:     d.SpeedBPS,
		UploadBps:    d.UploadBPS,
		Peers:        int32(d.Peers),
		TotalPeers:   int32(d.TotalPeers),
		Seeders:      int32(d.Seeders),
		Leechers:     int32(d.Leechers),
		PieceLength:  d.PieceLength,
		Pieces:       int32(d.Pieces),
		PiecesDone:   int32(d.PiecesDone),
		PieceBuckets: buckets,
		Seeding:      d.Seeding,
		Complete:     d.Complete,
		Files:        files,
		Trackers:     slices.Clone(d.Trackers),
	}
}

// btDetailFromPB 把 proto 消息转回 BT 详情。
func btDetailFromPB(d *pb.BTDetail) *api.BTDetail {
	if d == nil {
		return nil
	}
	files := make([]api.FileProgress, 0, len(d.GetFiles()))
	for _, f := range d.GetFiles() {
		files = append(files, api.FileProgress{
			Path:      f.GetPath(),
			Length:    f.GetLength(),
			Completed: f.GetCompleted(),
		})
	}
	buckets := make([]uint8, 0, len(d.GetPieceBuckets()))
	for _, b := range d.GetPieceBuckets() {
		buckets = append(buckets, uint8(b))
	}
	return &api.BTDetail{
		Name:         d.GetName(),
		InfoHash:     d.GetInfoHash(),
		Total:        d.GetTotal(),
		Downloaded:   d.GetDownloaded(),
		Uploaded:     d.GetUploaded(),
		SpeedBPS:     d.GetSpeedBps(),
		UploadBPS:    d.GetUploadBps(),
		Peers:        int(d.GetPeers()),
		TotalPeers:   int(d.GetTotalPeers()),
		Seeders:      int(d.GetSeeders()),
		Leechers:     int(d.GetLeechers()),
		PieceLength:  d.GetPieceLength(),
		Pieces:       int(d.GetPieces()),
		PiecesDone:   int(d.GetPiecesDone()),
		PieceBuckets: buckets,
		Seeding:      d.GetSeeding(),
		Complete:     d.GetComplete(),
		Files:        files,
		Trackers:     slices.Clone(d.GetTrackers()),
	}
}

// eventToPB 把事件转成 proto 消息。
func eventToPB(e api.Event) *pb.Event {
	return &pb.Event{
		Why:      e.Why,
		Task:     taskToPB(e.Task),
		SpeedBps: e.SpeedBPS,
	}
}

// eventFromPB 把 proto 消息转回事件。
func eventFromPB(e *pb.Event) api.Event {
	if e == nil {
		return api.Event{}
	}
	return api.Event{
		Why:      e.GetWhy(),
		Task:     taskFromPB(e.GetTask()),
		SpeedBPS: e.GetSpeedBps(),
	}
}

// messageToPB 把契约层消息转成 proto 消息。
func messageToPB(m api.Message) *pb.Message {
	switch m.Kind {
	case api.MessageKindEvent:
		// api.Message.Event 是指针，契约上允许为 nil（rpc 共享客户端的
		// dispatch 就显式判过 msg.Event != nil），这里不能直接解引用。
		if m.Event == nil {
			return &pb.Message{Payload: &pb.Message_Event{Event: &pb.Event{}}}
		}
		return &pb.Message{Payload: &pb.Message_Event{Event: eventToPB(*m.Event)}}
	case api.MessageKindShow:
		return &pb.Message{Payload: &pb.Message_Show{Show: true}}
	case api.MessageKindShowAddTask:
		p := m.ShowAddTask
		if p == nil {
			p = &api.ShowAddTaskParams{}
		}
		return &pb.Message{Payload: &pb.Message_ShowAddTask{ShowAddTask: &pb.ShowAddTaskParams{
			Url:     p.URL,
			Name:    p.Name,
			Ua:      p.UA,
			Cookies: p.Cookies,
		}}}
	case api.MessageKindLanguage:
		return &pb.Message{Payload: &pb.Message_Language{Language: m.Language}}
	case api.MessageKindClose:
		return &pb.Message{Payload: &pb.Message_Close{Close: true}}
	default:
		return &pb.Message{}
	}
}

// messageFromPB 把 proto 消息转回契约层消息。
func messageFromPB(m *pb.Message) api.Message {
	if m == nil {
		return api.Message{}
	}
	switch p := m.GetPayload().(type) {
	case *pb.Message_Event:
		ev := eventFromPB(p.Event)
		return api.Message{Kind: api.MessageKindEvent, Event: &ev}
	case *pb.Message_Show:
		return api.Message{Kind: api.MessageKindShow}
	case *pb.Message_ShowAddTask:
		return api.Message{
			Kind: api.MessageKindShowAddTask,
			ShowAddTask: &api.ShowAddTaskParams{
				URL:     p.ShowAddTask.GetUrl(),
				Name:    p.ShowAddTask.GetName(),
				UA:      p.ShowAddTask.GetUa(),
				Cookies: p.ShowAddTask.GetCookies(),
			},
		}
	case *pb.Message_Language:
		return api.Message{Kind: api.MessageKindLanguage, Language: p.Language}
	case *pb.Message_Close:
		return api.Message{Kind: api.MessageKindClose}
	default:
		return api.Message{}
	}
}

// formatTime 把时间转成 RFC3339 字符串。
//
// 零值时间会格式化成 "0001-01-01T00:00:00Z"，而不是空串：proto3 的
// string 字段本来就没有「有值 / 没值」之分，用空串表达零值会让「零值」
// 和「没设置」混成一团。
func formatTime(t time.Time) string {
	return t.Format(time.RFC3339Nano)
}

// parseTime 解析 RFC3339 字符串；空串或非法值返回零值。
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// nilIfEmpty 把空切片归一成 nil。
//
// proto3 的 repeated 字段在空的时候不上线传输，反序列化后必然是 nil。
// 归一之后「空切片」和「nil」在契约层就是同一件事，只在这一处处理。
func nilIfEmpty(v []int64) []int64 {
	if len(v) == 0 {
		return nil
	}
	return slices.Clone(v)
}
```


- [ ] **Step 6: 写转换往返测试**

`internal/rpc/grpcx/convert_test.go`：

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package grpcx

import (
	"reflect"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/grpcx/pb"
)

// 测试用的固定时间点。用 time.Date 而不是 time.Now()：time.Now() 带单调
// 时钟读数，格式化后就没了，往返比较必然不相等。
var testTime = time.Date(2026, 3, 4, 5, 6, 7, 890123456, time.UTC)

// normTask 把任务里的空切片归一成 nil。
//
// proto3 的 repeated 字段为空时不上线传输，反序列化后必然是 nil。
// 归一之后「空切片」与「nil」在契约层就是同一件事。
func normTask(t *api.Task) *api.Task {
	if t == nil {
		return nil
	}
	c := *t
	if len(c.ChunkProgress) == 0 {
		c.ChunkProgress = nil
	}
	if len(c.ChunkRanges) == 0 {
		c.ChunkRanges = nil
	}
	return &c
}

// normBTDetail 把详情里的空切片归一成 nil。
func normBTDetail(d *api.BTDetail) *api.BTDetail {
	if d == nil {
		return nil
	}
	c := *d
	if len(c.PieceBuckets) == 0 {
		c.PieceBuckets = nil
	}
	if len(c.Files) == 0 {
		c.Files = nil
	}
	if len(c.Trackers) == 0 {
		c.Trackers = nil
	}
	return &c
}

// TestTaskRoundTrip 覆盖任务的完整字段往返。
func TestTaskRoundTrip(t *testing.T) {
	in := api.Task{
		ID: "t1", URL: "http://x/a", SavePath: "/tmp/a", Protocol: "HTTP",
		TotalSize: 100, Downloaded: 40, SupportRange: true,
		ETag: "e", LastModified: "m", DigestAlgorithm: "sha256", Digest: "d",
		IsAllocated: true, ChunkCount: 4, MinChunkSize: 1024,
		ChunkProgress: []int64{1, 2}, ChunkRanges: []int64{0, 9},
		Status: api.StatusDownloading, AuthData: "{}", ErrorMessage: "m",
		ErrorKind: "k", CreatedAt: testTime, UpdatedAt: testTime,
		CompletedAt: testTime, BTFile: "f",
	}
	got := taskFromPB(taskToPB(&in))
	if !reflect.DeepEqual(normTask(got), normTask(&in)) {
		t.Fatalf("Task 往返不一致:\n got = %+v\nwant = %+v", *got, in)
	}
}

// TestTaskRoundTripZeroTimes 覆盖零值时间字段。
//
// 零值 time.Time{} 格式化后是 "0001-01-01T00:00:00Z"，必须能原样解析
// 回零值——用空串表达零值会让「零值」与「没设置」混成一团。
func TestTaskRoundTripZeroTimes(t *testing.T) {
	in := api.Task{ID: "t0"}
	got := taskFromPB(taskToPB(&in))
	if !reflect.DeepEqual(normTask(got), normTask(&in)) {
		t.Fatalf("零值时间字段往返不一致:\n got = %+v\nwant = %+v", *got, in)
	}
	if !got.CreatedAt.IsZero() || !got.UpdatedAt.IsZero() || !got.CompletedAt.IsZero() {
		t.Fatalf("零值时间字段应当仍是零值: %+v", got)
	}
}

// TestTaskRoundTripNil 覆盖 nil 任务。
func TestTaskRoundTripNil(t *testing.T) {
	if got := taskToPB(nil); got != nil {
		t.Fatalf("nil 任务应当转成 nil: %+v", got)
	}
	if got := taskFromPB(nil); got != nil {
		t.Fatalf("nil proto 任务应当转成 nil: %+v", got)
	}
}

// TestSettingsRoundTrip 覆盖设置的完整字段往返。
func TestSettingsRoundTrip(t *testing.T) {
	in := api.Settings{
		DefaultSaveDir: "/tmp", DefaultThreads: 8, MinChunkSize: 4096,
		UserAgent: "UA", Cookies: "C", FTPPassive: true, Prealloc: true,
		TaskSort: api.SortNameAsc, ProxyMode: api.ProxyModeManual,
		ProxyURL: "http://p", ProxyBypass: "b", ProxyAutoConfigURL: "u",
		DownloadLimitKiB: 512, LightMode: true, MaxConcurrent: 3,
		AutoStart: true, Language: "en", BTPort: 6881, BTMaxPeers: 50,
		BTDownloadLimitKiB: 100, BTUploadLimitKiB: 50, BTDHT: true, BTPEX: true,
		BTSeedRatio: 1.5, BTMaxSeeding: 10, BTMaxConcurrent: 2,
		BTTrackers: "udp://tracker.example:1337",
	}
	got := settingsFromPB(settingsToPB(in))
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("Settings 往返不一致:\n got = %+v\nwant = %+v", got, in)
	}
}

// TestSettingsRoundTripZero 覆盖设置的零值。
func TestSettingsRoundTripZero(t *testing.T) {
	in := api.Settings{}
	got := settingsFromPB(settingsToPB(in))
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("零值 Settings 往返不一致:\n got = %+v\nwant = %+v", got, in)
	}
}

// TestAuthRoundTrip 覆盖凭据的完整字段往返。
func TestAuthRoundTrip(t *testing.T) {
	in := api.AuthOptions{
		Username: "u", Password: "p", UserAgent: "UA", Cookies: "C",
		Referer: "r", FTPPassive: true, ProxyMode: api.ProxyModeDisabled,
		ProxyURL: "http://p", ProxyBypass: "b", ProxyAutoConfigURL: "u",
	}
	got := authFromPB(authToPB(in))
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("AuthOptions 往返不一致:\n got = %+v\nwant = %+v", got, in)
	}
}

// TestAuthRoundTripZero 覆盖凭据的零值。
func TestAuthRoundTripZero(t *testing.T) {
	in := api.AuthOptions{}
	got := authFromPB(authToPB(in))
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("零值 AuthOptions 往返不一致:\n got = %+v\nwant = %+v", got, in)
	}
}

// TestBTDetailRoundTrip 覆盖 BT 详情的完整字段往返。
func TestBTDetailRoundTrip(t *testing.T) {
	in := api.BTDetail{
		Name: "n", InfoHash: "h", Total: 10, Downloaded: 5, Uploaded: 1,
		SpeedBPS: 2, UploadBPS: 3, Peers: 4, TotalPeers: 5, Seeders: 6,
		Leechers: 7, PieceLength: 8, Pieces: 9, PiecesDone: 10,
		PieceBuckets: []uint8{1, 2}, Seeding: true, Complete: true,
		Files:    []api.FileProgress{{Path: "p", Length: 1, Completed: 2}},
		Trackers: []string{"t"},
	}
	got := btDetailFromPB(btDetailToPB(&in))
	if !reflect.DeepEqual(normBTDetail(got), normBTDetail(&in)) {
		t.Fatalf("BTDetail 往返不一致:\n got = %+v\nwant = %+v", *got, in)
	}
}

// TestBTDetailRoundTripZero 覆盖 BT 详情的零值。
func TestBTDetailRoundTripZero(t *testing.T) {
	in := api.BTDetail{}
	got := btDetailFromPB(btDetailToPB(&in))
	if !reflect.DeepEqual(normBTDetail(got), normBTDetail(&in)) {
		t.Fatalf("零值 BTDetail 往返不一致:\n got = %+v\nwant = %+v", *got, in)
	}
}

// TestEventRoundTrip 覆盖事件的往返。
func TestEventRoundTrip(t *testing.T) {
	in := api.Event{
		Why:      "progress",
		Task:     &api.Task{ID: "t1", Status: api.StatusDownloading, CreatedAt: testTime},
		SpeedBPS: 1024,
	}
	got := eventFromPB(eventToPB(in))
	if !reflect.DeepEqual(normTask(got.Task), normTask(in.Task)) {
		t.Fatalf("Event 往返不一致:\n got = %+v\nwant = %+v", got, in)
	}
	if got.Why != in.Why || got.SpeedBPS != in.SpeedBPS {
		t.Fatalf("Event 字段往返不一致: %+v", got)
	}
}

// TestEventRoundTripNilTask 覆盖事件里的 nil 任务。
func TestEventRoundTripNilTask(t *testing.T) {
	in := api.Event{Why: "w"}
	got := eventFromPB(eventToPB(in))
	if got.Task != nil {
		t.Fatalf("nil 任务应当仍是 nil: %+v", got.Task)
	}
}

// TestMessageRoundTrip 覆盖消息流里每一种消息的往返。
func TestMessageRoundTrip(t *testing.T) {
	cases := []api.Message{
		{Kind: api.MessageKindEvent, Event: &api.Event{Why: "w", SpeedBPS: 1}},
		{Kind: api.MessageKindShow},
		{Kind: api.MessageKindShowAddTask, ShowAddTask: &api.ShowAddTaskParams{
			URL: "http://x", Name: "n", UA: "ua", Cookies: "c",
		}},
		{Kind: api.MessageKindLanguage, Language: "en"},
		{Kind: api.MessageKindClose},
	}
	for _, in := range cases {
		got := messageFromPB(messageToPB(in))
		if got.Kind != in.Kind {
			t.Fatalf("消息类别往返不一致: got = %q, want = %q", got.Kind, in.Kind)
		}
		switch in.Kind {
		case api.MessageKindEvent:
			if got.Event == nil || got.Event.Why != in.Event.Why {
				t.Fatalf("事件往返不一致: %+v", got.Event)
			}
		case api.MessageKindShowAddTask:
			if got.ShowAddTask == nil || *got.ShowAddTask != *in.ShowAddTask {
				t.Fatalf("弹窗参数往返不一致: %+v", got.ShowAddTask)
			}
		case api.MessageKindLanguage:
			if got.Language != in.Language {
				t.Fatalf("语言往返不一致: %q", got.Language)
			}
		}
	}
}

// TestMessageToPBHandlesNilEvent 是 E3 的回归测试。
//
// api.Message.Event 是指针，契约上允许为 nil；messageToPB 直接解引用会
// panic。
func TestMessageToPBHandlesNilEvent(t *testing.T) {
	got := messageToPB(api.Message{Kind: api.MessageKindEvent})
	if got == nil {
		t.Fatal("nil 事件也应当转成一个 proto 消息")
	}
	if got.GetEvent() == nil {
		t.Fatal("nil 事件应当转成空事件，而不是丢掉消息类别")
	}
	back := messageFromPB(got)
	if back.Kind != api.MessageKindEvent {
		t.Fatalf("消息类别应当保留: %q", back.Kind)
	}
	if back.Event == nil {
		t.Fatal("回转后的事件指针不应为 nil")
	}
}

// TestMessageFromPBNil 覆盖 nil proto 消息。
func TestMessageFromPBNil(t *testing.T) {
	got := messageFromPB(nil)
	if got.Kind != "" {
		t.Fatalf("nil 消息应当转成零值消息: %+v", got)
	}
}

// TestListParamsRoundTrip 覆盖筛选与排序枚举的往返（E8）。
func TestListParamsRoundTrip(t *testing.T) {
	cases := []api.ListParams{
		{Filter: api.FilterAll, Sort: api.SortCreatedDesc},
		{Filter: api.FilterPending, Sort: api.SortCreatedAsc},
		{Filter: api.FilterDownloading, Sort: api.SortNameAsc},
		{Filter: api.FilterPaused, Sort: api.SortNameDesc},
		{Filter: api.FilterCompleted, Sort: api.SortCreatedDesc},
		{Filter: api.FilterFailed, Sort: api.SortCreatedDesc},
		{Filter: api.FilterFileLost, Sort: api.SortCreatedDesc},
		// 零值表示「不过滤」与「默认排序」。
		{Filter: "", Sort: ""},
	}
	for _, in := range cases {
		got := listParamsFromPB(listParamsToPB(&in))
		if *got != in {
			t.Fatalf("ListParams 往返不一致: got = %+v, want = %+v", *got, in)
		}
	}
}

// TestListParamsFromPBUnspecified 覆盖 UNSPECIFIED 的语义（E8）。
//
// UNSPECIFIED 必须映射回空串，而不是映射成某个具体枚举——空串在存储层
// 表示「不过滤 / 默认排序」。
func TestListParamsFromPBUnspecified(t *testing.T) {
	got := listParamsFromPB(&pb.ListRequest{
		Filter: pb.StatusFilter_STATUS_FILTER_UNSPECIFIED,
		Sort:   pb.TaskSort_TASK_SORT_UNSPECIFIED,
	})
	if got.Filter != "" {
		t.Fatalf("UNSPECIFIED 筛选应当映射回空串: %q", got.Filter)
	}
	if got.Sort != "" {
		t.Fatalf("UNSPECIFIED 排序应当映射回空串: %q", got.Sort)
	}
}

// TestAddTaskInputRoundTrip 覆盖「添加下载任务」入参的往返。
func TestAddTaskInputRoundTrip(t *testing.T) {
	in := api.AddTaskInput{
		URL: "http://x", SavePath: "/tmp", Protocol: api.ProtoBT,
		Auth: api.AuthOptions{
			Username: "u", Password: "p", UserAgent: "UA", Cookies: "C",
			Referer: "r", FTPPassive: true, ProxyMode: api.ProxyModeManual,
			ProxyURL: "http://p", ProxyBypass: "b", ProxyAutoConfigURL: "u",
		},
		ChunkCount: 8, MinChunkSize: 1024,
	}
	got := addTaskInputFromPB(addTaskInputToPB(&in))
	if !reflect.DeepEqual(*got, in) {
		t.Fatalf("AddTaskInput 往返不一致:\n got = %+v\nwant = %+v", *got, in)
	}
}

// TestFormatParseTime 覆盖时间的格式化与解析（E14）。
func TestFormatParseTime(t *testing.T) {
	// 零值时间格式化成 "0001-01-01T00:00:00Z"，并且能原样解析回零值。
	if got := formatTime(time.Time{}); got != "0001-01-01T00:00:00Z" {
		t.Fatalf("零值时间应当格式化成 RFC3339 零值: %q", got)
	}
	if got := parseTime("0001-01-01T00:00:00Z"); !got.IsZero() {
		t.Fatalf("零值时间应当解析回零值: %v", got)
	}
	// 空串与非法值都退化成零值，不能炸。
	if got := parseTime(""); !got.IsZero() {
		t.Fatalf("空串应当解析成零值: %v", got)
	}
	if got := parseTime("不是时间"); !got.IsZero() {
		t.Fatalf("非法值应当解析成零值: %v", got)
	}
	// 正常时间往返无损。
	if got := parseTime(formatTime(testTime)); !got.Equal(testTime) {
		t.Fatalf("时间往返不一致: got = %v, want = %v", got, testTime)
	}
}

// TestEnumTablesCoverAllConstants 校验每张枚举映射表都双向闭合、覆盖
// UNSPECIFIED，并且都有真实调用点。
//
// 「有调用点」这一条是 E8 的要害：初稿里 ListRequest 用的是裸 string，
// filterToPB / filterFromPB 两张表因此是死代码，而只检查反向映射存在的
// 测试照样能过——测不出「枚举压根没被用」。
func TestEnumTablesCoverAllConstants(t *testing.T) {
	t.Run("状态", func(t *testing.T) {
		for s, p := range statusToPB {
			back, ok := statusFromPB[p]
			if !ok {
				t.Fatalf("状态 %q 没有反向映射", s)
			}
			if back != s {
				t.Fatalf("状态 %q 反向映射成了 %q", s, back)
			}
		}
		if _, ok := statusFromPB[pb.Status_STATUS_UNSPECIFIED]; !ok {
			t.Fatal("statusFromPB 缺少 UNSPECIFIED 的显式条目")
		}
		if got := statusFromPB[pb.Status_STATUS_UNSPECIFIED]; got != "" {
			t.Fatalf("UNSPECIFIED 应当映射回空串: %q", got)
		}
		// 调用点：状态走 Task。
		for s := range statusToPB {
			pbTask := taskToPB(&api.Task{Status: s})
			if pbTask.GetStatus() != statusToPB[s] {
				t.Fatalf("状态 %q 没有出现在 Task 的转换结果里", s)
			}
			if got := taskFromPB(pbTask).Status; got != s {
				t.Fatalf("状态 %q 经 Task 往返后变成了 %q", s, got)
			}
		}
	})

	t.Run("筛选", func(t *testing.T) {
		for s, p := range filterToPB {
			back, ok := filterFromPB[p]
			if !ok {
				t.Fatalf("筛选 %q 没有反向映射", s)
			}
			if back != s {
				t.Fatalf("筛选 %q 反向映射成了 %q", s, back)
			}
		}
		if _, ok := filterFromPB[pb.StatusFilter_STATUS_FILTER_UNSPECIFIED]; !ok {
			t.Fatal("filterFromPB 缺少 UNSPECIFIED 的显式条目")
		}
		// 调用点：筛选走 ListRequest。
		for s := range filterToPB {
			req := listParamsToPB(&api.ListParams{Filter: s})
			if req.GetFilter() != filterToPB[s] {
				t.Fatalf("筛选 %q 没有出现在 ListRequest 的转换结果里", s)
			}
			if got := listParamsFromPB(req).Filter; got != s {
				t.Fatalf("筛选 %q 经 ListRequest 往返后变成了 %q", s, got)
			}
		}
	})

	t.Run("排序", func(t *testing.T) {
		for s, p := range sortToPB {
			back, ok := sortFromPB[p]
			if !ok {
				t.Fatalf("排序 %q 没有反向映射", s)
			}
			if back != s {
				t.Fatalf("排序 %q 反向映射成了 %q", s, back)
			}
		}
		if _, ok := sortFromPB[pb.TaskSort_TASK_SORT_UNSPECIFIED]; !ok {
			t.Fatal("sortFromPB 缺少 UNSPECIFIED 的显式条目")
		}
		// 调用点：排序走 ListRequest。
		for s := range sortToPB {
			req := listParamsToPB(&api.ListParams{Sort: s})
			if req.GetSort() != sortToPB[s] {
				t.Fatalf("排序 %q 没有出现在 ListRequest 的转换结果里", s)
			}
			if got := listParamsFromPB(req).Sort; got != s {
				t.Fatalf("排序 %q 经 ListRequest 往返后变成了 %q", s, got)
			}
		}
	})

	t.Run("代理模式", func(t *testing.T) {
		for s, p := range proxyToPB {
			back, ok := proxyFromPB[p]
			if !ok {
				t.Fatalf("代理模式 %q 没有反向映射", s)
			}
			if back != s {
				t.Fatalf("代理模式 %q 反向映射成了 %q", s, back)
			}
		}
		if _, ok := proxyFromPB[pb.ProxyMode_PROXY_MODE_UNSPECIFIED]; !ok {
			t.Fatal("proxyFromPB 缺少 UNSPECIFIED 的显式条目")
		}
		// 调用点一：代理模式走 Settings。
		for s := range proxyToPB {
			pbSet := settingsToPB(api.Settings{ProxyMode: s})
			if pbSet.GetProxyMode() != proxyToPB[s] {
				t.Fatalf("代理模式 %q 没有出现在 Settings 的转换结果里", s)
			}
			if got := settingsFromPB(pbSet).ProxyMode; got != s {
				t.Fatalf("代理模式 %q 经 Settings 往返后变成了 %q", s, got)
			}
		}
		// 调用点二：代理模式走 AuthOptions。
		for s := range proxyToPB {
			pbAuth := authToPB(api.AuthOptions{ProxyMode: s})
			if pbAuth.GetProxyMode() != proxyToPB[s] {
				t.Fatalf("代理模式 %q 没有出现在 AuthOptions 的转换结果里", s)
			}
			if got := authFromPB(pbAuth).ProxyMode; got != s {
				t.Fatalf("代理模式 %q 经 AuthOptions 往返后变成了 %q", s, got)
			}
		}
	})

	t.Run("协议种类", func(t *testing.T) {
		for s, p := range kindToPB {
			back, ok := kindFromPB[p]
			if !ok {
				t.Fatalf("协议 %q 没有反向映射", s)
			}
			if back != s {
				t.Fatalf("协议 %q 反向映射成了 %q", s, back)
			}
		}
		if _, ok := kindFromPB[pb.ProtocolKind_PROTOCOL_KIND_UNSPECIFIED]; !ok {
			t.Fatal("kindFromPB 缺少 UNSPECIFIED 的显式条目")
		}
		// 调用点：协议种类走 AddTaskRequest。
		for s := range kindToPB {
			req := addTaskInputToPB(&api.AddTaskInput{Protocol: s})
			if req.GetProtocol() != kindToPB[s] {
				t.Fatalf("协议 %q 没有出现在 AddTaskRequest 的转换结果里", s)
			}
			if got := addTaskInputFromPB(req).Protocol; got != s {
				t.Fatalf("协议 %q 经 AddTaskRequest 往返后变成了 %q", s, got)
			}
		}
	})
}

// TestEnumTablesCoverEveryConstant 确认映射表没有漏掉契约层的常量。
//
// 只查「表里的项都有反向映射」不够：漏掉一个常量（比如新增了状态却忘了
// 加映射）那样查不出来。这边把契约层的常量逐个过一遍。
func TestEnumTablesCoverEveryConstant(t *testing.T) {
	for _, s := range []api.Status{
		api.StatusPending, api.StatusDownloading, api.StatusPaused,
		api.StatusCompleted, api.StatusFailed, api.StatusFileLost,
	} {
		if _, ok := statusToPB[s]; !ok {
			t.Fatalf("状态 %q 缺少 proto 映射", s)
		}
	}
	for _, f := range []api.StatusFilter{
		api.FilterAll, api.FilterPending, api.FilterDownloading,
		api.FilterPaused, api.FilterCompleted, api.FilterFailed,
		api.FilterFileLost,
	} {
		if _, ok := filterToPB[f]; !ok {
			t.Fatalf("筛选 %q 缺少 proto 映射", f)
		}
	}
	for _, s := range api.AllTaskSorts() {
		if _, ok := sortToPB[s]; !ok {
			t.Fatalf("排序 %q 缺少 proto 映射", s)
		}
	}
	for _, p := range []api.ProxyMode{
		api.ProxyModeSystem, api.ProxyModeDisabled, api.ProxyModeManual,
	} {
		if _, ok := proxyToPB[p]; !ok {
			t.Fatalf("代理模式 %q 缺少 proto 映射", p)
		}
	}
	for _, k := range []api.ProtocolKind{
		api.ProtoHTTP, api.ProtoHTTPS, api.ProtoFTP, api.ProtoWebDAV, api.ProtoBT,
	} {
		if _, ok := kindToPB[k]; !ok {
			t.Fatalf("协议 %q 缺少 proto 映射", k)
		}
	}
}
```


- [ ] **Step 7: 建 `internal/rpc/grpcx/server.go`**

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package grpcx 是基于 gRPC 的传输实现：类型化 RPC + protobuf 载荷。
//
// 与 httpx 并列，两者是同一个契约层（internal/api）的两种线格式实现。
// 本包只依赖 internal/api 与 internal/rpc/addr，不依赖 internal/rpc
// 本身——否则会形成 rpc → grpcx → rpc 的包环。业务侧能力的接口在这里
// 独立声明一份，方法集与 rpc.Handler 逐字一致，靠结构化类型对接。
package grpcx

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
	"lgo_download_manager/internal/rpc/grpcx/pb"
)

// Handler 是 grpcx 需要的业务侧能力。方法集与 internal/rpc 里的
// rpc.Handler 逐字一致，靠结构化类型与它对接——这里独立声明是为了避开
// grpcx → rpc 的包环（rpc → grpcx 已经存在）。
type Handler interface {
	// Call 执行一次调用。method 取 api.Method* 常量；in / out 的具体类型
	// 由 api.Spec(method) 决定，传输层分配好再传进来。
	Call(ctx context.Context, method string, in, out any) error

	// Events 返回进度事件流与退订函数。
	Events() (<-chan api.Event, func())

	// Controls 返回控制消息流（显示窗口 / 弹对话框 / 语言变更 / 退出）。
	Controls() <-chan api.Message
}

// Serve 在地址上启动 gRPC 服务。ctx 取消时服务关闭。
func Serve(ctx context.Context, a addr.Addr, token string, h Handler) error {
	ln, cleanup, err := addr.Listen(a)
	if err != nil {
		return err
	}
	defer cleanup()

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(unaryAuth(token)),
		grpc.StreamInterceptor(streamAuth(token)),
	)
	pb.RegisterDownloadServiceServer(srv, &server{h: h})

	// done 用来收尾监视 goroutine：Serve 返回后它就退出，不留悬挂 goroutine。
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			// 必须用 Stop 而不是 GracefulStop：GracefulStop 会等所有在跑
			// 的 RPC 结束，而 StreamMessages 是长连接流，客户端不主动断开
			// 它就永远不会结束——那样 Serve 会永久阻塞。Stop 立即取消所有
			// 活跃的 RPC 与流，与 httpx 的 srv.Close() 语义一致。
			srv.Stop()
		case <-done:
		}
	}()

	err = srv.Serve(ln)
	if errors.Is(err, grpc.ErrServerStopped) {
		return nil
	}
	return err
}

// unaryAuth 校验一元调用的 token。
func unaryAuth(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !checkToken(ctx, token) {
			return nil, status.Error(codes.Unauthenticated, "无效的 token")
		}
		return handler(ctx, req)
	}
}

// streamAuth 校验流式调用的 token。
func streamAuth(token string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !checkToken(ss.Context(), token) {
			return status.Error(codes.Unauthenticated, "无效的 token")
		}
		return handler(srv, ss)
	}
}

// checkToken 从 metadata 里取 Bearer token 并比对。
//
// metadata 的 key 会被 gRPC 规范化为小写，所以这里必须写小写
// "authorization"——写 "Authorization" 会取不到。
func checkToken(ctx context.Context, token string) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return false
	}
	const prefix = "Bearer "
	v := vals[0]
	if len(v) <= len(prefix) || !strings.HasPrefix(v, prefix) {
		return false
	}
	return v[len(prefix):] == token
}

// server 把生成的 gRPC 接口转成对业务侧契约的调用。
type server struct {
	pb.UnimplementedDownloadServiceServer
	h Handler
}

// internalErr 把业务侧错误转成 gRPC 状态错误。
//
// gRPC 的 status 里放不进 Go 的错误链，业务错误的原因只能靠 message
// 传递——这与 httpx 把错误写进响应体的做法等价。
func internalErr(err error) error {
	return status.Error(codes.Internal, err.Error())
}

func (s *server) List(ctx context.Context, req *pb.ListRequest) (*pb.ListResponse, error) {
	out := &api.ListResult{}
	if err := s.h.Call(ctx, api.MethodList, listParamsFromPB(req), out); err != nil {
		return nil, internalErr(err)
	}
	tasks := make([]*pb.Task, 0, len(out.Tasks))
	for _, t := range out.Tasks {
		tasks = append(tasks, taskToPB(t))
	}
	return &pb.ListResponse{Tasks: tasks}, nil
}

func (s *server) AddTask(ctx context.Context, req *pb.AddTaskRequest) (*pb.AddTaskResponse, error) {
	out := &api.Task{}
	if err := s.h.Call(ctx, api.MethodAdd, addTaskInputFromPB(req), out); err != nil {
		return nil, internalErr(err)
	}
	return &pb.AddTaskResponse{Task: taskToPB(out)}, nil
}

func (s *server) StartTask(ctx context.Context, req *pb.TaskRef) (*pb.Empty, error) {
	if err := s.h.Call(ctx, api.MethodStart, &api.TaskRef{ID: req.GetId()}, &api.Empty{}); err != nil {
		return nil, internalErr(err)
	}
	return &pb.Empty{}, nil
}

func (s *server) PauseTask(ctx context.Context, req *pb.TaskRef) (*pb.Empty, error) {
	if err := s.h.Call(ctx, api.MethodPause, &api.TaskRef{ID: req.GetId()}, &api.Empty{}); err != nil {
		return nil, internalErr(err)
	}
	return &pb.Empty{}, nil
}

func (s *server) DeleteTask(ctx context.Context, req *pb.TaskRef) (*pb.Empty, error) {
	if err := s.h.Call(ctx, api.MethodDelete, &api.TaskRef{ID: req.GetId()}, &api.Empty{}); err != nil {
		return nil, internalErr(err)
	}
	return &pb.Empty{}, nil
}

func (s *server) IsPreparing(ctx context.Context, req *pb.TaskRef) (*pb.BoolResponse, error) {
	out := &api.BoolResult{}
	if err := s.h.Call(ctx, api.MethodIsPreparing, &api.TaskRef{ID: req.GetId()}, out); err != nil {
		return nil, internalErr(err)
	}
	return &pb.BoolResponse{Value: out.Value}, nil
}

func (s *server) Probe(ctx context.Context, req *pb.ProbeRequest) (*pb.ProbeResponse, error) {
	out := &api.ProbeResult{}
	if err := s.h.Call(ctx, api.MethodProbe, &api.ProbeParams{URL: req.GetUrl()}, out); err != nil {
		return nil, internalErr(err)
	}
	return &pb.ProbeResponse{TotalSize: out.TotalSize}, nil
}

func (s *server) GetSettings(ctx context.Context, _ *pb.Empty) (*pb.GetSettingsResponse, error) {
	out := &api.Settings{}
	if err := s.h.Call(ctx, api.MethodGetSettings, &api.Empty{}, out); err != nil {
		return nil, internalErr(err)
	}
	return &pb.GetSettingsResponse{Settings: settingsToPB(*out)}, nil
}

func (s *server) SaveSettings(ctx context.Context, req *pb.SaveSettingsRequest) (*pb.Empty, error) {
	in := settingsFromPB(req.GetSettings())
	if err := s.h.Call(ctx, api.MethodSaveSettings, &in, &api.Empty{}); err != nil {
		return nil, internalErr(err)
	}
	return &pb.Empty{}, nil
}

func (s *server) BTTaskDetail(ctx context.Context, req *pb.BTInfoRequest) (*pb.BTDetailResponse, error) {
	out := &api.BTDetail{}
	if err := s.h.Call(ctx, api.MethodBTInfo, &api.BTInfoParams{TaskID: req.GetTaskId()}, out); err != nil {
		return nil, internalErr(err)
	}
	return &pb.BTDetailResponse{Detail: btDetailToPB(out)}, nil
}

// StreamMessages 把事件流与控制消息合并后逐条推送。
func (s *server) StreamMessages(_ *pb.Empty, stream pb.DownloadService_StreamMessagesServer) error {
	events, unsub := s.h.Events()
	defer unsub()
	controls := s.h.Controls()

	// 某一侧通道关闭只停掉那一侧：事件流结束不代表控制消息也没了。
	// 这与 httpx 的处理一致，否则同一套业务侧实现在两种传输下行为不同。
	for events != nil || controls != nil {
		select {
		case <-stream.Context().Done():
			// 客户端断开：立刻收手，别让 goroutine 挂着。
			return nil
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if err := stream.Send(messageToPB(api.Message{
				Kind:  api.MessageKindEvent,
				Event: &ev,
			})); err != nil {
				return err
			}
		case ctl, ok := <-controls:
			if !ok {
				controls = nil
				continue
			}
			if err := stream.Send(messageToPB(ctl)); err != nil {
				return err
			}
		}
	}
	return nil
}

// methodErrorf 构造带方法名的错误。
func methodErrorf(method string, err error) error {
	return fmt.Errorf("grpcx: %s: %w", method, err)
}
```


- [ ] **Step 8: 建 `internal/rpc/grpcx/client.go`**

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package grpcx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
	"lgo_download_manager/internal/rpc/grpcx/pb"
)

// streamBuffer 是消息流 channel 的缓冲条数。
const streamBuffer = 64

// Client 是 gRPC 传输的客户端。
type Client struct {
	// conn 是底层连接，cli 是它的 gRPC 桩。
	conn *grpc.ClientConn
	cli  pb.DownloadServiceClient

	// closed 标记客户端是否已关闭。
	closed atomic.Bool

	// closeOnce 保证底层连接只关一次。
	//
	// grpc.ClientConn.Close() 第二次调用返回 ErrClientConnClosing（非 nil
	// 错误），而 rpc.Transport 的契约明确要求 Close 幂等，所以必须用
	// sync.Once 包住。
	closeOnce sync.Once
}

// Dial 按地址建立 gRPC 连接。token 随每次调用发送。
//
// Dial 会用 ctx 预连接一次再立刻断开。gRPC 的连接是惰性的
// （grpc.NewClient 不建连，要等第一次 RPC），不预连接的话
// 「业务进程没起来」要等到第一次调用才暴露，调用方只会看到一个含义模糊
// 的握手失败。预连接让失败时机与 httpx 传输一致。
func Dial(ctx context.Context, a addr.Addr, token string) (*Client, error) {
	conn, err := addr.DialContext(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("grpcx: 连接 %s 失败: %w", a.Address, err)
	}
	_ = conn.Close()

	// dialer 用 gRPC 传进来的 ctx 建连：一次超时的调用不该留下一个还在
	// 建连的 socket goroutine。
	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return addr.DialContext(ctx, a)
	}
	// 显式用 passthrough 解析器：grpc.NewClient 默认走 dns 解析器，
	// 而我们的地址是 unix socket / host:port，由上面的 dialer 负责解析。
	cc, err := grpc.NewClient("passthrough:///lgdm",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer),
		grpc.WithUnaryInterceptor(clientUnaryToken(token)),
		grpc.WithStreamInterceptor(clientStreamToken(token)),
	)
	if err != nil {
		return nil, fmt.Errorf("grpcx: 建立连接: %w", err)
	}
	return &Client{conn: cc, cli: pb.NewDownloadServiceClient(cc)}, nil
}

// clientUnaryToken 给一元调用附加 token。
func clientUnaryToken(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		// metadata 的 key 会被 gRPC 规范化为小写，服务端也按小写取，
		// 两边必须一致。
		//
		// 注意 gRPC 的 metadata 值只允许可打印 ASCII：token 里有非 ASCII
		// 字符时 grpc-go 会直接报错。实际部署不受影响——业务侧的握手
		// token 是随机数的十六进制编码，纯 ASCII。
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// clientStreamToken 给流式调用附加 token。
func clientStreamToken(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return streamer(ctx, desc, cc, method, opts...)
	}
}

// Call 执行一次调用。
func (c *Client) Call(ctx context.Context, method string, in, out any) error {
	if err := c.checkOpen(); err != nil {
		return err
	}
	switch method {
	case api.MethodList:
		p, ok := asPtr[api.ListParams](in)
		if !ok {
			return badIn(method, in, "*api.ListParams")
		}
		r, ok := asPtr[api.ListResult](out)
		if !ok {
			return badOut(method, out, "*api.ListResult")
		}
		res, err := c.cli.List(ctx, listParamsToPB(p))
		if err != nil {
			return methodErrorf(method, err)
		}
		r.Tasks = nil
		for _, t := range res.GetTasks() {
			r.Tasks = append(r.Tasks, taskFromPB(t))
		}
		return nil

	case api.MethodAdd:
		p, ok := asPtr[api.AddTaskInput](in)
		if !ok {
			return badIn(method, in, "*api.AddTaskInput")
		}
		r, ok := asPtr[api.Task](out)
		if !ok {
			return badOut(method, out, "*api.Task")
		}
		res, err := c.cli.AddTask(ctx, addTaskInputToPB(p))
		if err != nil {
			return methodErrorf(method, err)
		}
		t := taskFromPB(res.GetTask())
		if t == nil {
			return fmt.Errorf("grpcx: %s: 服务端返回的任务为空", method)
		}
		*r = *t
		return nil

	case api.MethodStart:
		p, ok := asPtr[api.TaskRef](in)
		if !ok {
			return badIn(method, in, "*api.TaskRef")
		}
		if _, ok := asPtr[api.Empty](out); !ok {
			return badOut(method, out, "*api.Empty")
		}
		if _, err := c.cli.StartTask(ctx, &pb.TaskRef{Id: p.ID}); err != nil {
			return methodErrorf(method, err)
		}
		return nil

	case api.MethodPause:
		p, ok := asPtr[api.TaskRef](in)
		if !ok {
			return badIn(method, in, "*api.TaskRef")
		}
		if _, ok := asPtr[api.Empty](out); !ok {
			return badOut(method, out, "*api.Empty")
		}
		if _, err := c.cli.PauseTask(ctx, &pb.TaskRef{Id: p.ID}); err != nil {
			return methodErrorf(method, err)
		}
		return nil

	case api.MethodDelete:
		p, ok := asPtr[api.TaskRef](in)
		if !ok {
			return badIn(method, in, "*api.TaskRef")
		}
		if _, ok := asPtr[api.Empty](out); !ok {
			return badOut(method, out, "*api.Empty")
		}
		if _, err := c.cli.DeleteTask(ctx, &pb.TaskRef{Id: p.ID}); err != nil {
			return methodErrorf(method, err)
		}
		return nil

	case api.MethodIsPreparing:
		p, ok := asPtr[api.TaskRef](in)
		if !ok {
			return badIn(method, in, "*api.TaskRef")
		}
		r, ok := asPtr[api.BoolResult](out)
		if !ok {
			return badOut(method, out, "*api.BoolResult")
		}
		res, err := c.cli.IsPreparing(ctx, &pb.TaskRef{Id: p.ID})
		if err != nil {
			return methodErrorf(method, err)
		}
		r.Value = res.GetValue()
		return nil

	case api.MethodProbe:
		p, ok := asPtr[api.ProbeParams](in)
		if !ok {
			return badIn(method, in, "*api.ProbeParams")
		}
		r, ok := asPtr[api.ProbeResult](out)
		if !ok {
			return badOut(method, out, "*api.ProbeResult")
		}
		res, err := c.cli.Probe(ctx, &pb.ProbeRequest{Url: p.URL})
		if err != nil {
			return methodErrorf(method, err)
		}
		r.TotalSize = res.GetTotalSize()
		return nil

	case api.MethodGetSettings:
		if _, ok := asPtr[api.Empty](in); !ok {
			return badIn(method, in, "*api.Empty")
		}
		r, ok := asPtr[api.Settings](out)
		if !ok {
			return badOut(method, out, "*api.Settings")
		}
		res, err := c.cli.GetSettings(ctx, &pb.Empty{})
		if err != nil {
			return methodErrorf(method, err)
		}
		*r = settingsFromPB(res.GetSettings())
		return nil

	case api.MethodSaveSettings:
		p, ok := asPtr[api.Settings](in)
		if !ok {
			return badIn(method, in, "*api.Settings")
		}
		if _, ok := asPtr[api.Empty](out); !ok {
			return badOut(method, out, "*api.Empty")
		}
		if _, err := c.cli.SaveSettings(ctx, &pb.SaveSettingsRequest{Settings: settingsToPB(*p)}); err != nil {
			return methodErrorf(method, err)
		}
		return nil

	case api.MethodBTInfo:
		p, ok := asPtr[api.BTInfoParams](in)
		if !ok {
			return badIn(method, in, "*api.BTInfoParams")
		}
		r, ok := asPtr[api.BTDetail](out)
		if !ok {
			return badOut(method, out, "*api.BTDetail")
		}
		res, err := c.cli.BTTaskDetail(ctx, &pb.BTInfoRequest{TaskId: p.TaskID})
		if err != nil {
			return methodErrorf(method, err)
		}
		d := btDetailFromPB(res.GetDetail())
		if d == nil {
			return fmt.Errorf("grpcx: %s: 服务端返回的详情为空", method)
		}
		*r = *d
		return nil

	default:
		return fmt.Errorf("grpcx: 未知方法 %q", method)
	}
}

// Stream 打开消息流。
func (c *Client) Stream(ctx context.Context) (<-chan api.Message, error) {
	if err := c.checkOpen(); err != nil {
		return nil, err
	}
	stream, err := c.cli.StreamMessages(ctx, &pb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("grpcx: 打开消息流: %w", err)
	}
	out := make(chan api.Message, streamBuffer)
	go func() {
		defer close(out)
		for {
			msg, err := stream.Recv()
			if err != nil {
				// 流结束（服务端收手、连接断开、ctx 取消）都在这里收尾：
				// 关闭 out，读循环随之知道消息流没了。
				return
			}
			select {
			case out <- messageFromPB(msg):
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Close 关闭底层 gRPC 连接。必须幂等——Close 可能被重复调用。
//
// 连接一关，所有还在跑的 RPC 与流都会随之取消，所以不需要像 httpx 那样
// 单独维护活动流列表。
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		if c.conn != nil {
			_ = c.conn.Close()
		}
	})
	return nil
}

// checkOpen 检查客户端是否已关闭或未初始化。
func (c *Client) checkOpen() error {
	if c == nil {
		return errors.New("grpcx: 客户端未初始化，请用 Dial 创建")
	}
	if c.closed.Load() {
		return errors.New("grpcx: 客户端已关闭")
	}
	// 零值客户端的 conn 是 nil，直接发 RPC 会 nil 指针解引用 panic。
	// Client 是导出类型，零值是被允许的构造物，这里要回错误而不是炸进程。
	if c.conn == nil {
		return errors.New("grpcx: 客户端未初始化，请用 Dial 创建")
	}
	return nil
}

// asPtr 把 any 转成指定类型的指针，并报告是否转换成功。
//
// 类型不对必须回错误而不是 panic：httpx 那边是 JSON 编解码，类型不对会
// 在服务端返回 400；gRPC 这边直接解引用就是 panic。
func asPtr[T any](v any) (*T, bool) {
	p, ok := v.(*T)
	return p, ok
}

// badIn 构造「请求类型不对」的错误。
func badIn(method string, got any, want string) error {
	return fmt.Errorf("grpcx: %s: 请求类型错误: 期望 %s, 实际 %T", method, want, got)
}

// badOut 构造「响应类型不对」的错误。
func badOut(method string, got any, want string) error {
	return fmt.Errorf("grpcx: %s: 响应类型错误: 期望 %s, 实际 %T", method, want, got)
}
```


- [ ] **Step 9: 写服务端与客户端的行为测试**

`internal/rpc/grpcx/grpcx_test.go`：

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package grpcx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
)

const testToken = "tok-123"

// mockHandler 是测试用的业务侧实现。
type mockHandler struct {
	events   chan api.Event
	controls chan api.Message

	// settings 是 get_settings 的返回值。
	settings api.Settings
	// tasks 是 list 的返回值。
	tasks []*api.Task
	// callErr 非 nil 时 Call 直接返回它。
	callErr error

	// eventsCalled 在服务端进入消息流处理函数时关闭。
	eventsCalled chan struct{}
	eventsOnce   sync.Once

	// unsubed 在服务端的消息流处理函数返回（退订）时关闭。
	unsubed   chan struct{}
	unsubOnce sync.Once
}

func newMockHandler() *mockHandler {
	return &mockHandler{
		events:       make(chan api.Event, 8),
		controls:     make(chan api.Message, 8),
		eventsCalled: make(chan struct{}),
		unsubed:      make(chan struct{}),
	}
}

func (m *mockHandler) Call(ctx context.Context, method string, in, out any) error {
	if m.callErr != nil {
		return m.callErr
	}
	switch method {
	case api.MethodGetSettings:
		o, ok := out.(*api.Settings)
		if !ok {
			return errors.New("mock: 响应类型不对")
		}
		*o = m.settings
	case api.MethodList:
		o, ok := out.(*api.ListResult)
		if !ok {
			return errors.New("mock: 响应类型不对")
		}
		o.Tasks = m.tasks
	case api.MethodIsPreparing:
		o, ok := out.(*api.BoolResult)
		if !ok {
			return errors.New("mock: 响应类型不对")
		}
		o.Value = true
	case api.MethodProbe:
		o, ok := out.(*api.ProbeResult)
		if !ok {
			return errors.New("mock: 响应类型不对")
		}
		o.TotalSize = 4096
	case api.MethodBTInfo:
		o, ok := out.(*api.BTDetail)
		if !ok {
			return errors.New("mock: 响应类型不对")
		}
		*o = api.BTDetail{Name: "种子"}
	case api.MethodAdd:
		o, ok := out.(*api.Task)
		if !ok {
			return errors.New("mock: 响应类型不对")
		}
		p, ok := in.(*api.AddTaskInput)
		if !ok {
			return errors.New("mock: 请求类型不对")
		}
		o.ID = "new"
		o.URL = p.URL
		o.SavePath = p.SavePath
		o.Protocol = string(p.Protocol)
		o.Status = api.StatusPending
	case api.MethodStart, api.MethodPause, api.MethodDelete, api.MethodSaveSettings:
		// 没有返回值的方法。
	default:
		return errors.New("mock: 未知方法 " + method)
	}
	return nil
}

func (m *mockHandler) Events() (<-chan api.Event, func()) {
	m.eventsOnce.Do(func() { close(m.eventsCalled) })
	return m.events, func() {
		m.unsubOnce.Do(func() { close(m.unsubed) })
	}
}

func (m *mockHandler) Controls() <-chan api.Message { return m.controls }

// startServer 在临时目录里的 unix socket 上启动服务，返回监听地址。
func startServer(t *testing.T, h Handler, token string) addr.Addr {
	t.Helper()
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "lgdm.sock")}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = Serve(ctx, a, token, h) }()
	// socket 文件在 net.Listen 绑定成功时创建，文件出现即表示能连。
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(a.Address); err == nil {
			return a
		}
		if time.Now().After(deadline) {
			t.Fatalf("服务没有在超时前就绪: %v", a)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// dialClient 连上服务并返回一个客户端。
func dialClient(t *testing.T, a addr.Addr, token string) *Client {
	t.Helper()
	c, err := Dial(context.Background(), a, token)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestEndToEndCallAndStream 是端到端冒烟：真起服务、连一次、调一次、
// 收一条消息。
func TestEndToEndCallAndStream(t *testing.T) {
	h := newMockHandler()
	h.settings = api.Settings{Language: "en", DefaultThreads: 8}
	h.tasks = []*api.Task{{ID: "t1", Status: api.StatusDownloading}}
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	// 一元调用：拿设置。
	out := &api.Settings{}
	if err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, out); err != nil {
		t.Fatalf("get_settings 失败: %v", err)
	}
	if out.Language != "en" || out.DefaultThreads != 8 {
		t.Fatalf("设置不对: %+v", out)
	}

	// 一元调用：列任务。
	list := &api.ListResult{}
	if err := c.Call(context.Background(), api.MethodList,
		&api.ListParams{Filter: api.FilterAll, Sort: api.SortCreatedDesc}, list); err != nil {
		t.Fatalf("list 失败: %v", err)
	}
	if len(list.Tasks) != 1 || list.Tasks[0].ID != "t1" {
		t.Fatalf("任务列表不对: %+v", list.Tasks)
	}

	// 流式调用：收一条事件。
	stream, err := c.Stream(context.Background())
	if err != nil {
		t.Fatalf("打开消息流失败: %v", err)
	}
	h.events <- api.Event{Why: "progress", SpeedBPS: 512}
	select {
	case msg := <-stream:
		if msg.Kind != api.MessageKindEvent || msg.Event == nil {
			t.Fatalf("收到的事件不对: %+v", msg)
		}
		if msg.Event.Why != "progress" || msg.Event.SpeedBPS != 512 {
			t.Fatalf("事件内容不对: %+v", msg.Event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("没有收到事件")
	}
}

// TestClientCloseStopsStreamAndServerStream 验证 Close 之后连接真的断掉：
// 服务端的消息流处理函数必须返回。
func TestClientCloseStopsStreamAndServerStream(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c, err := Dial(context.Background(), a, testToken)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	if _, err := c.Stream(context.Background()); err != nil {
		t.Fatalf("打开消息流失败: %v", err)
	}
	// 等服务端真的进了消息流处理函数，再关客户端。
	select {
	case <-h.eventsCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("服务端没有进入消息流处理函数")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}
	select {
	case <-h.unsubed:
	case <-time.After(5 * time.Second):
		t.Fatal("客户端关闭后，服务端的消息流没有结束")
	}
}

// TestClientCloseIsIdempotent 是 E5 的回归测试。
//
// grpc.ClientConn.Close() 第二次调用返回 ErrClientConnClosing（非 nil），
// 而 rpc.Transport 的契约要求 Close 幂等。
func TestClientCloseIsIdempotent(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	for i := 0; i < 3; i++ {
		if err := c.Close(); err != nil {
			t.Fatalf("第 %d 次 Close 应当返回 nil，实际: %v", i+1, err)
		}
	}
}

// TestClientCallAfterClose 验证关闭之后调用返回错误而不是 panic。
func TestClientCallAfterClose(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)
	if err := c.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}
	out := &api.Settings{}
	err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, out)
	if err == nil {
		t.Fatal("关闭之后的调用应当返回错误")
	}
	if _, err := c.Stream(context.Background()); err == nil {
		t.Fatal("关闭之后的开流应当返回错误")
	}
}

// TestZeroValueClientReturnsError 验证零值客户端回错误而不是 panic。
func TestZeroValueClientReturnsError(t *testing.T) {
	c := &Client{}
	out := &api.Settings{}
	if err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, out); err == nil {
		t.Fatal("零值客户端的调用应当返回错误")
	}
	if _, err := c.Stream(context.Background()); err == nil {
		t.Fatal("零值客户端的开流应当返回错误")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("零值客户端的 Close 应当返回 nil: %v", err)
	}
}

// TestCallRejectsWrongInType 是 E6 的回归测试：类型不对要回错误，不能 panic。
func TestCallRejectsWrongInType(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	// 请求类型不对。
	err := c.Call(context.Background(), api.MethodList, "不是 ListParams", &api.ListResult{})
	if err == nil {
		t.Fatal("请求类型不对时应当返回错误")
	}
	// 响应类型不对。
	err = c.Call(context.Background(), api.MethodList, &api.ListParams{}, "不是 ListResult")
	if err == nil {
		t.Fatal("响应类型不对时应当返回错误")
	}
	// 未知方法。
	err = c.Call(context.Background(), "没有这个方法", &api.Empty{}, &api.Empty{})
	if err == nil {
		t.Fatal("未知方法应当返回错误")
	}
}

// TestServerRejectsBadToken 覆盖一元与流式两条鉴权路径。
func TestServerRejectsBadToken(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, "wrong-token")

	// 一元调用。
	err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{})
	if err == nil {
		t.Fatal("错的 token 应当被拒绝")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("应当是 Unauthenticated，实际: %v", err)
	}

	// 流式调用。服务端流式 RPC 的拒绝要等第一次收消息才浮现，对客户端
	// 表现为「流立刻结束」。另外，服务端的鉴权拦截器在业务处理函数之前
	// 就返回了，所以 mock 的 Events() 压根不会被调用——用它来确认请求
	// 真的被挡在了门外，而不是进了业务层才失败。
	stream, err := c.Stream(context.Background())
	if err == nil {
		select {
		case msg, ok := <-stream:
			if ok {
				t.Fatalf("错的 token 应当被拒绝，却收到了消息: %+v", msg)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("错的 token 应当被拒绝")
		}
	}
	select {
	case <-h.eventsCalled:
		t.Fatal("错的 token 不该进到业务侧的消息流处理函数")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestNonASCIITokenIsRejected 记录 gRPC 传输与 net/http 传输的一个已知
// 差异：gRPC 的 metadata 值只允许可打印 ASCII，带非 ASCII 字符的 token
// 根本发不出去（grpc-go 直接报错），而 httpx 的 Authorization 头收任意
// UTF-8。
//
// 实际部署不受影响：业务侧的握手 token 是 32 字节随机数的十六进制编码
// （见 internal/uimgr 的 randomToken），纯 ASCII。这里把行为钉死，免得
// 以后有人踩到时不知道是哪儿来的。
func TestNonASCIITokenIsRejected(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, "带中文的 token")
	err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{})
	if err == nil {
		t.Fatal("带非 ASCII 字符的 token 应当发不出去")
	}
	if !strings.Contains(err.Error(), "non-printable ASCII") {
		t.Fatalf("应当是 metadata 的可打印 ASCII 限制，实际: %v", err)
	}
}

// TestServerAcceptsGoodToken 确认对的 token 能过。
func TestServerAcceptsGoodToken(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)
	if err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{}); err != nil {
		t.Fatalf("对的 token 应当被接受: %v", err)
	}
}

// TestServerRejectsEmptyToken 确认空 token 也会被拒。
//
// 服务端要求 "Bearer " 后面还有内容（长度必须大于前缀长度），所以空
// token 发过去就是 "Bearer "，一律拒绝。
func TestServerRejectsEmptyToken(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	empty := dialClient(t, a, "")
	err := empty.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{})
	if err == nil {
		t.Fatal("空 token 应当被拒绝")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("应当是 Unauthenticated，实际: %v", err)
	}
}

// TestServeStopsOnContextCancelWithActiveStream 是 E7 的回归测试。
//
// 服务端停机用的是 Stop 而不是 GracefulStop：GracefulStop 会等所有在跑
// 的 RPC 结束，而 StreamMessages 是长连接流，客户端不主动断开它就永远
// 不结束——那样 Serve 会永久阻塞。
func TestServeStopsOnContextCancelWithActiveStream(t *testing.T) {
	h := newMockHandler()
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "lgdm.sock")}
	ctx, cancel := context.WithCancel(context.Background())

	served := make(chan error, 1)
	go func() { served <- Serve(ctx, a, testToken, h) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(a.Address); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("服务没有在超时前就绪")
		}
		time.Sleep(10 * time.Millisecond)
	}

	c, err := Dial(context.Background(), a, testToken)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer c.Close()
	if _, err := c.Stream(context.Background()); err != nil {
		t.Fatalf("打开消息流失败: %v", err)
	}
	// 等服务端真的进了消息流处理函数——这时候 GracefulStop 必然挂死。
	select {
	case <-h.eventsCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("服务端没有进入消息流处理函数")
	}

	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve 应当正常返回，实际: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ctx 取消后 Serve 没有返回（有活跃流时会挂死）")
	}
}

// TestServeStopsOnContextCancelWithoutStream 覆盖没有活跃流的停机。
func TestServeStopsOnContextCancelWithoutStream(t *testing.T) {
	h := newMockHandler()
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "lgdm.sock")}
	ctx, cancel := context.WithCancel(context.Background())

	served := make(chan error, 1)
	go func() { served <- Serve(ctx, a, testToken, h) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(a.Address); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("服务没有在超时前就绪")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve 应当正常返回，实际: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ctx 取消后 Serve 没有返回")
	}
}

// TestStreamMessagesContinuesAfterEventsClosed 是 E4 的回归测试。
//
// 事件通道关闭只停掉那一侧，控制消息流要继续——否则同一套业务侧实现在
// 两种传输下行为不一致。
func TestStreamMessagesContinuesAfterEventsClosed(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	stream, err := c.Stream(context.Background())
	if err != nil {
		t.Fatalf("打开消息流失败: %v", err)
	}
	// 事件侧关闭。
	close(h.events)
	// 控制侧仍然有消息。
	h.controls <- api.Message{Kind: api.MessageKindLanguage, Language: "en"}

	select {
	case msg := <-stream:
		if msg.Kind != api.MessageKindLanguage || msg.Language != "en" {
			t.Fatalf("应当收到控制消息，实际: %+v", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("事件通道关闭后控制消息流没有继续")
	}
}

// TestStreamMessagesStopsAfterBothSidesClosed 覆盖两侧都关闭的情形。
func TestStreamMessagesStopsAfterBothSidesClosed(t *testing.T) {
	h := newMockHandler()
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	stream, err := c.Stream(context.Background())
	if err != nil {
		t.Fatalf("打开消息流失败: %v", err)
	}
	close(h.events)
	close(h.controls)
	// 两侧都关掉之后，服务端收手，客户端的流会结束。
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case _, ok := <-stream:
			if !ok {
				return
			}
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("两侧都关闭之后消息流没有结束")
		}
	}
}

// TestDialFailsWhenServerAbsent 覆盖 E2：预连接让「业务进程没起来」在
// Dial 阶段就暴露，而不是等到第一次调用。
func TestDialFailsWhenServerAbsent(t *testing.T) {
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "missing.sock")}
	if _, err := Dial(context.Background(), a, testToken); err == nil {
		t.Fatal("服务没起来时 Dial 应当失败")
	}
}

// TestDialRespectsContext 覆盖 E2：ctx 不是死参数。
func TestDialRespectsContext(t *testing.T) {
	a := addr.Addr{Network: "unix", Address: filepath.Join(t.TempDir(), "missing.sock")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Dial(ctx, a, testToken); err == nil {
		t.Fatal("ctx 取消之后 Dial 应当失败")
	}
}

// TestServerPropagatesBusinessError 确认业务侧的错误能传到客户端。
func TestServerPropagatesBusinessError(t *testing.T) {
	h := newMockHandler()
	h.callErr = errors.New("业务侧出错了")
	a := startServer(t, h, testToken)
	c := dialClient(t, a, testToken)

	err := c.Call(context.Background(), api.MethodGetSettings, &api.Empty{}, &api.Settings{})
	if err == nil {
		t.Fatal("业务侧的错误应当传回客户端")
	}
	if !strings.Contains(err.Error(), "业务侧出错了") {
		t.Fatalf("错误里应当带上业务侧的原因，实际: %v", err)
	}
}
```


外部测试包 `internal/rpc/grpcx/contract_test.go`（放编译期断言，Go 允许
外部测试包 import 上层包，不会成环）：

```go
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
```


- [ ] **Step 10: 建 `internal/rpc/registry.go`（传输注册表）**

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package rpc

import (
	"context"
	"fmt"

	"lgo_download_manager/internal/rpc/addr"
	"lgo_download_manager/internal/rpc/grpcx"
	"lgo_download_manager/internal/rpc/httpx"
)

// 传输协议名。
const (
	TransportGRPC = "grpc"
	TransportHTTP = "http"
)

// ServerOptions 描述一次服务端的启动参数。
type ServerOptions struct {
	// Transport 是传输协议名；为空时按 TransportGRPC 处理。
	Transport string
	// Addr 是监听地址，见 addr.Parse。
	Addr string
	// Token 是握手 token，每次调用都会校验。
	Token string
}

// Serve 按 opts 启动服务。handler 的实现由业务侧提供。
func Serve(ctx context.Context, opts ServerOptions, h Handler) error {
	a, err := addr.Parse(opts.Addr)
	if err != nil {
		return err
	}
	switch opts.Transport {
	case "", TransportGRPC:
		return grpcx.Serve(ctx, a, opts.Token, h)
	case TransportHTTP:
		return httpx.Serve(ctx, a, opts.Token, h)
	default:
		return fmt.Errorf("rpc: 未知传输协议 %q（可选：%s、%s）", opts.Transport, TransportGRPC, TransportHTTP)
	}
}

// Dial 连接到 endpoint 并返回客户端。endpoint 的格式与 addr.Parse 相同。
//
// ctx 应当是贯穿整个会话的 ctx：握手那一次调用用的就是它。两种传输都在
// Dial 阶段预连接一次，所以「业务进程没起来」在这里就暴露，而不是等到
// 第一次调用。
func Dial(ctx context.Context, transport, endpoint, token string) (Client, error) {
	a, err := addr.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	var tr Transport
	switch transport {
	case "", TransportGRPC:
		tr, err = grpcx.Dial(ctx, a, token)
	case TransportHTTP:
		tr, err = httpx.Dial(ctx, a, token)
	default:
		return nil, fmt.Errorf("rpc: 未知传输协议 %q（可选：%s、%s）", transport, TransportGRPC, TransportHTTP)
	}
	if err != nil {
		return nil, err
	}
	c, err := newClient(ctx, tr)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// 编译期断言：两种传输实现都满足 Transport。
var (
	_ Transport = (*grpcx.Client)(nil)
	_ Transport = (*httpx.Client)(nil)
)
```


`internal/rpc/registry_test.go`：

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package rpc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
)

// stubHandler 是最小业务侧实现。
type stubHandler struct {
	events   chan api.Event
	controls chan api.Message
}

func (s *stubHandler) Call(ctx context.Context, method string, in, out any) error {
	switch method {
	case api.MethodGetSettings:
		o, ok := out.(*api.Settings)
		if !ok {
			return errors.New("stub: 响应类型不对")
		}
		o.Language = "en"
	case api.MethodList:
		o, ok := out.(*api.ListResult)
		if !ok {
			return errors.New("stub: 响应类型不对")
		}
		o.Tasks = []*api.Task{{ID: "t1"}}
	default:
		return errors.New("stub: 未实现 " + method)
	}
	return nil
}

func (s *stubHandler) Events() (<-chan api.Event, func()) {
	return s.events, func() {}
}

func (s *stubHandler) Controls() <-chan api.Message { return s.controls }

// startServer 在临时目录里的 unix socket 上启动服务，返回地址与停服函数。
func startServer(t *testing.T, transport, token string) (string, func()) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "lgdm.sock")
	ctx, cancel := context.WithCancel(context.Background())
	h := &stubHandler{
		events:   make(chan api.Event, 4),
		controls: make(chan api.Message, 4),
	}
	served := make(chan error, 1)
	go func() {
		served <- Serve(ctx, ServerOptions{Transport: transport, Addr: "unix:" + sock, Token: token}, h)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("服务没有在超时前就绪")
		}
		time.Sleep(10 * time.Millisecond)
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

// TestRegistryBothTransports 确认注册表在两种传输下都能起服务、连上、
// 调一次。
func TestRegistryBothTransports(t *testing.T) {
	for _, transport := range []string{TransportGRPC, TransportHTTP} {
		t.Run(transport, func(t *testing.T) {
			sock, _ := startServer(t, transport, "tok")
			c, err := Dial(context.Background(), transport, "unix:"+sock, "tok")
			if err != nil {
				t.Fatalf("连接失败: %v", err)
			}
			defer c.Close()

			tasks, err := c.List(api.FilterAll, api.SortCreatedDesc)
			if err != nil {
				t.Fatalf("list 失败: %v", err)
			}
			if len(tasks) != 1 || tasks[0].ID != "t1" {
				t.Fatalf("任务列表不对: %+v", tasks)
			}
			if got := c.Settings().Language; got != "en" {
				t.Fatalf("设置不对: %q", got)
			}
		})
	}
}

// TestRegistryDefaultTransport 确认不写传输名时默认走 gRPC。
func TestRegistryDefaultTransport(t *testing.T) {
	sock, _ := startServer(t, "", "tok")
	c, err := Dial(context.Background(), "", "unix:"+sock, "tok")
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer c.Close()
	if _, err := c.List(api.FilterAll, api.SortCreatedDesc); err != nil {
		t.Fatalf("list 失败: %v", err)
	}
}

// TestRegistryUnknownTransport 确认未知传输名会报错。
func TestRegistryUnknownTransport(t *testing.T) {
	err := Serve(context.Background(), ServerOptions{Transport: "quic", Addr: "unix:/tmp/x.sock"}, &stubHandler{})
	if err == nil {
		t.Fatal("未知传输名应当报错")
	}
	if _, err := Dial(context.Background(), "quic", "unix:/tmp/x.sock", ""); err == nil {
		t.Fatal("未知传输名应当报错")
	}
}

// TestRegistryBadAddr 确认地址解析失败会报错。
func TestRegistryBadAddr(t *testing.T) {
	err := Serve(context.Background(), ServerOptions{Addr: ""}, &stubHandler{})
	if err == nil {
		t.Fatal("空地址应当报错")
	}
	if _, err := Dial(context.Background(), TransportGRPC, "", ""); err == nil {
		t.Fatal("空地址应当报错")
	}
}
```


- [ ] **Step 11: 跑测试**

Run: `go test ./internal/rpc/... ./internal/appservice/`
Expected: PASS

端到端另外要真起一次服务（unix socket）确认：客户端 `Call` 一次拿到结果、
`Stream` 收到一条消息、`Close()` 之后服务端的消息流处理函数真的返回。
这两条分别由 `TestEndToEndCallAndStream` 与
`TestClientCloseStopsStreamAndServerStream` 覆盖。

- [ ] **Step 12: 提交**

```bash
git add proto buf.yaml buf.gen.yaml scripts/gen_proto.sh internal/rpc/grpcx internal/rpc/registry.go internal/rpc/registry_test.go go.mod go.sum
git commit -m "feat(grpcx): 实现基于 gRPC 的传输并加入 protobuf 契约"
```

---



### Task 8: 契约测试套件（两种传输各跑一遍）

**Files:**
- Create: `internal/rpc/contract_test.go`
- Modify: `internal/rpc/client.go`（握手失败时关掉传输层，见 F5）
- Modify（修复轮 2）：`internal/rpc/httpx/client.go`（入参/出参类型校验，见 Task 5 的 A12）、`internal/rpc/httpx/httpx_test.go`（护栏测试）

**Interfaces:**
- Consumes: `internal/rpc`（Task 4）、`internal/rpc/httpx`（Task 5）、`internal/rpc/grpcx`（Task 7）
- Produces: 无（只有测试；外加 `client.go` 的一处泄漏修复）

- [ ] **Step 1: 写契约测试**

创建 `internal/rpc/contract_test.go`（下面是与实现逐行一致的完整内容）：

```go
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

// caseProbe 断言体积探测。
func caseProbe(t *testing.T, env *contractEnv) {
	size, err := env.cli.Probe("http://example.com/a")
	if err != nil {
		t.Fatal(err)
	}
	if size != 4242 {
		t.Fatalf("探测结果不对: %d", size)
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

	size, err := env.cli.Probe("http://example.com/x")
	if err != nil {
		t.Fatal(err)
	}
	out["probe"] = fmt.Sprintf("%d", size)

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
```

- [ ] **Step 2: 跑测试确认通过**

Run: `go test ./internal/rpc/ -run TestContract -v`
Expected: PASS —— 两种传输各跑一遍全部子用例

- [ ] **Step 3: 提交**

```bash
git add internal/rpc/contract_test.go internal/rpc/client.go
git commit -m "test(rpc): 为两种传输补同一套契约测试"
```

#### 与初稿的差异（F1–F13 的落法）

| 条目 | 初稿的问题 | 最终实现 |
| --- | --- | --- |
| F1 | 初稿的用例里没有「注册前补发」 | 新增 `show_add_task 注册前补发` 用例。用 **show 控制消息当顺序哨兵**：show_add_task 与 show 走同一条控制通道、通道内保序，show 回调触发即证明排在它前面的 show_add_task 已被读循环处理过；此时再注册钩子，断言钩子最终被调用且参数正确。全程不靠 sleep |
| F2 | 初稿没有 Close 幂等用例 | 新增 `Close 幂等`（客户端层）与 `传输层 Close 幂等`（直接打 `rpc.Transport`）两条。传输层那条是必需的：`rpc.Client.Close` 会把 Close 转给传输层，客户端层幂等不代表传输层幂等 |
| F3 | 初稿没有「事件通道关闭后控制流继续」 | 新增该用例。**时序不能想当然**：先关事件通道再发控制消息是不确定的——服务端 select 同时盯着两个通道，已关闭的事件通道与已缓冲的控制消息都是就绪的，Go 随机挑一个，同一个用例有时红有时绿。改成先发一条控制消息当「消息流是通的」探针，收到后再关事件通道，服务端必然重新醒一次，行为才确定 |
| F4 | 初稿漏了 `save_settings` | 新增 `保存设置往返` 用例，27 个字段全部填非零值。断言方式是「保存后**另起一个连接**读回」——`client.SaveSettings` 会顺手更新本地缓存，从本地缓存读不算往返 |
| F5 | `newClient` 握手失败时不关传输层 | 修 `internal/rpc/client.go`：握手失败时 `tr.Close()`。实测每次失败的握手漏 2 个 fd（net/http 的空闲连接池、gRPC 的 ClientConn），gRPC 还会漏 8 个 goroutine。契约测试新增 `握手失败不泄漏传输层` 用例守住它 |
| F6 | 初稿只有「错误 token 被拒」 | 补齐四条：错误 token 被拒、未知方法报错、入参类型不对报错、业务侧错误传回客户端（错误原因不能丢） |
| F7 | 同一组用例分别跑两遍，抓不住「两边都过但结果不同」 | 新增 `TestContractTransportsAgree`：两边跑同一组固定调用，逐项比较 JSON 编码结果（设置、列表、添加、准备阶段、探测、BT 详情、保存后读回的设置） |
| F8 | 初稿用固定 sleep 断言时序 | 全部改成「等到条件成立」：`waitFor` 轮询助手 + 顺序哨兵，不依赖固定时长 |
| F9 | `t.TempDir() + "/lgdm.sock"` | 改成 `filepath.Join(t.TempDir(), "lgdm.sock")` |
| F10 | 初稿写 `package rpc`（内部测试包） | 用 `package rpc_test`（外部测试包），并加编译期断言 `var _ rpc.Transport = (*grpcx.Client)(nil)` 等四条 |
| F11 | 地址格式 | 全部走 `unix:` + 临时目录里的 socket，不碰真实网络 |
| F12 | `rpc.Dial` 的 ctx 必须是会话级的 | 用 `context.WithCancel(context.Background())`，测试结束时 `cancel()`；不用短超时 |
| F13 | 修复轮 1 只把分歧③④**钉住**，没有修 | 修复轮 2 补上 httpx 侧的入参/出参类型校验（Task 5 的 A12）：分歧③的用例改名 `TestContractInputTypeMismatchRejected`、分歧④改名 `TestContractOutputTypeMismatchRejected`，两条都从「钉住分歧」改成「守住一致性」——两种传输都必须报错，且都**不许把请求发出去**（断言服务端没收到这次调用）。分歧①②保持钉桩，注释里写明是线格式固有差异、不追求统一。httpx 侧另加护栏测试 `TestCallTypeMismatchDoesNotSendRequest`（假 RoundTripper 断言一次都没被调用）与 `TestCallUnknownMethodDoesNotPanic`（未知方法不 panic） |

初稿的用例（握手/列表/添加/启动暂停删除/准备阶段/探测/事件流/控制消息/错误
token）全部保留，另外补了 BT 详情、退订、语言与 close 控制消息、关闭之后调用
报错等用例。

#### 契约测试发现的两种传输分歧

契约测试的价值一半在「一致」，另一半在「不一致」。下面是**实测**出来的
真实分歧，各自都有专门的用例把当前行为钉住（改动任何一侧都会让用例变红，
提醒人来统一）：

| # | 场景 | gRPC | net/http | 用例 | 性质 |
| --- | --- | --- | --- | --- | --- |
| 1 | 词汇表里没有的枚举取值（`Settings.TaskSort` 等） | 归一成空串（protobuf 枚举只能映射到 UNSPECIFIED） | 原样保留（JSON 字符串） | `TestContractDivergenceUnknownEnum` | **固有差异**：线格式不同，不追求统一 |
| 2 | 空切片（`BTDetail.Files` / `PieceBuckets`） | 非 nil 的空切片 | nil（JSON `null`） | `TestContractDivergenceEmptySlice` | **固有差异**：线格式不同，不追求统一 |
| 3 | 入参 Go 类型不对但 JSON 恰好兼容 | 报错（Go 类型断言失败） | ~~静默接受，服务端收到零值~~ → **已统一**：报错 | `TestContractInputTypeMismatchRejected` | **缺陷**：已修（见 Task 5 的 A12） |
| 4 | 出参 Go 类型不对 | 报错（Go 类型断言失败） | ~~静默返回零值对象~~ → **已统一**：报错 | `TestContractOutputTypeMismatchRejected` | **缺陷**：已修（见 Task 5 的 A12） |

第 1、2 条是 JSON 与 protobuf 的线格式固有差异，**不是缺陷，不追求统一**：
protobuf 的枚举在 wire 上是整数，词表外的取值没有对应的整数，只能落回
UNSPECIFIED；proto3 的 repeated 字段没有「null」状态，只有「空」；JSON 两样
都有。强行统一就得让一边丢信息或让另一边凭空猜值——两头都不对。契约层的
比较因此一律走 JSON 归一（见 `sameJSON`），别拿 `reflect.DeepEqual` 当契约。

第 3、4 条对调用方最危险：类型传错了却「成功」，错误要到业务逻辑不对的时候
才会暴露出来。修复轮 2 补上了 httpx 侧的入参/出参类型校验（Task 5 的 A12），
两条用例从「钉住分歧」改成「守住一致性」，并新增 httpx 侧的护栏测试
`TestCallTypeMismatchDoesNotSendRequest`：类型不对时客户端压根不发请求。

#### 证伪自检（契约测试确实抓得住坏实现）

改坏实现 → 看用例是否变红 → 还原。逐条实测：

| 改坏的地方 | 变红的用例 |
| --- | --- |
| 去掉 `SetOnShowAddTask` 的补发逻辑 | `show_add_task 注册前补发`（两种传输都红） |
| 把「事件通道关闭只停那一侧」改回「整体收手」（httpx + grpcx） | `事件通道关闭后控制流继续`（两种传输都红，4 次重跑 8/8 红） |
| 去掉 grpcx `Close` 的 `sync.Once` | `传输层 Close 幂等`（gRPC 红） |
| 让 httpx 第二次 `Close` 返回错误 | `传输层 Close 幂等`（HTTP 红） |
| 撤销 F5 的握手失败关传输层 | `握手失败不泄漏传输层`（两种传输都红，并打出 goroutine/fd 的涨落） |
| gRPC 侧 `settingsToPB` 漏掉两个字段 | `保存设置往返` 与 `TestContractTransportsAgree` 都红 |
| httpx 服务端解码后丢掉 list 的筛选/排序 | `列表`（HTTP 红） |
| 去掉 httpx 的入参/出参类型校验（修复轮 2） | `TestContractInputTypeMismatchRejected`、`TestContractOutputTypeMismatchRejected`（HTTP 红），以及 httpx 的 `TestCallTypeMismatchDoesNotSendRequest`（红，并打出「实际发了 11 次」） |

第一条证伪（事件通道关闭）最初**没有**变红——初版用例是「先关事件通道、
再发控制消息」，服务端 select 在两个就绪分支之间随机挑，时好时坏。改成
「先发控制消息探活、再关事件通道」之后才变成确定性的。

---

### Task 9: 接线业务侧（`internal/uimgr` 改用 `rpc.Serve`）

**Files:**
- Modify: `internal/uimgr/manager.go`
- Modify: `internal/uimgr/manager_test.go`
- Modify: `internal/uimgr/bt_info_test.go`

**Interfaces:**
- Consumes: `internal/rpc`（Task 4/5/7）、`internal/appservice`（Task 3）
- Produces:
  ```go
  package uimgr
  func New(svc *appservice.Service) *Manager
  func (m *Manager) Start() error
  func (m *Manager) Open()
  func (m *Manager) Close()
  func (m *Manager) SetLanguage(lang string)
  func (m *Manager) SendShowAddTask(p api.ShowAddTaskParams) error
  func (m *Manager) OnLanguageChanged(f func(lang string))
  func (m *Manager) OnExit(f func())
  func (m *Manager) Running() bool
  func (m *Manager) Settings() store.Settings
  func (m *Manager) SetSettings(s store.Settings)
  ```

- [ ] **Step 1: 改 `internal/uimgr/manager.go`**

改动要点（逐处替换，不要整体重写文件）：

1. **结构体**：把 `st *store.Store` / `sc *scheduler.Scheduler` 换成 `svc *appservice.Service`，
   `conn *ipc.Conn` 换成 `cancel context.CancelFunc` + `waitCh chan struct{}`（保留），
   新增 `transport string`、`addr string`。

   ```go
   type Manager struct {
   	svc *appservice.Service

   	mu       sync.Mutex
   	settings store.Settings
   	curLang  string

   	addr     string // 监听地址（unix 路径或 host:port）
   	transport string
   	token    string

   	cmd    *exec.Cmd
   	cancel context.CancelFunc
   	waitCh chan struct{}
   	gen    uint64

   	// sessionActive 表示 UI 会话已经建立（子进程已拉起且未结束）。
   	// 控制消息在会话不存在时必须报错——main.go 依赖这个错误把
   	// 「扩展请求没被接收」如实告诉浏览器，让它退到下一条通道。
   	sessionActive bool

   	onExit   func()
   	exitOnce *sync.Once

   	onLanguageChanged func(lang string)
   }
   ```

2. **构造函数**：

   ```go
   // New 创建 UI 管理器。svc 是业务侧应用服务。
   func New(svc *appservice.Service) *Manager {
   	return &Manager{
   		svc: svc,
   		onExit: func() {
   			logging.Println("uimgr: UI session ended; business keeps running")
   		},
   	}
   }
   ```

3. **`SetSettings` / `Settings`**：改成读写 `appservice`：

   ```go
   func (m *Manager) SetSettings(s store.Settings) {
   	m.mu.Lock()
   	m.settings = s
   	m.mu.Unlock()
   	m.svc.SetSettings(s)
   }

   func (m *Manager) Settings() store.Settings {
   	m.mu.Lock()
   	defer m.mu.Unlock()
   	return m.settings
   }
   ```

4. **`SetLanguage`**：改成推控制消息。

   ```go
   // SetLanguage 把业务进程的权威 Language 推给 UI 子进程。
   func (m *Manager) SetLanguage(lang string) {
   	m.mu.Lock()
   	changed := m.curLang != lang
   	m.curLang = lang
   	hook := m.onLanguageChanged
   	m.mu.Unlock()
   	if changed && hook != nil {
   		hook(lang)
   	}
   	if !changed {
   		return
   	}
   	m.svc.PushControl(api.Message{Kind: api.MessageKindLanguage, Language: lang})
   }
   ```

5. **`Start()`**：地址与 token 的生成、spawn 子进程、`rpc.Serve`：

   ```go
   func (m *Manager) Start() error {
   	m.mu.Lock()
   	defer m.mu.Unlock()
   	if m.cancel != nil {
   		return fmt.Errorf("uimgr: UI already running")
   	}
   	addr, err := m.resolveAddr()
   	if err != nil {
   		return err
   	}
   	token, err := randomToken()
   	if err != nil {
   		return err
   	}
   	self, err := os.Executable()
   	if err != nil {
   		return fmt.Errorf("uimgr: resolve self executable: %w", err)
   	}
   	ctx, cancel := context.WithCancel(context.Background())
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
   		return fmt.Errorf("uimgr: spawn UI child: %w", err)
   	}
   	m.gen++
   	gen := m.gen
   	m.addr = addr
   	m.token = token
   	m.cancel = cancel
   	m.cmd = cmd
   	m.exitOnce = &sync.Once{}
   	go func() {
   		err := cmd.Wait()
   		logging.Printf("uimgr: UI child exited: %v", err)
   		cancel()
   	}()
   	go m.serve(ctx, gen, addr, token)
   	return nil
   }
   ```

6. **`serve`**：替代原来的 `acceptLoop`：

   ```go
   // serve 在监听地址上启动传输层服务，并把事件与控制消息合并推给 UI。
   func (m *Manager) serve(ctx context.Context, gen uint64, addr, token string) {
   	defer m.finishSession(gen)
   	err := rpc.Serve(ctx, rpc.ServerOptions{
   		Transport: m.transport,
   		Addr:      addr,
   		Token:     token,
   	}, m.svc)
   	if err != nil {
   		logging.Printf("uimgr: rpc serve: %v", err)
   	}
   }
   ```

7. **`resolveAddr`**：默认生成 unix socket 路径，命令行/环境变量可覆盖。
   TCP 且不是回环地址时打一条警告——接口会被本机网络上的其它进程看到。

   ```go
   // EnvRPCListen 是业务进程自己的监听地址环境变量。
   const EnvRPCListen = "LDM_RPC_LISTEN"

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
   	return "unix:" + filepath.Join(sub, "ui-"+suffix+".sock"), nil
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
   ```

   `addrpkg` 是 `"lgo_download_manager/internal/rpc/addr"` 的 import 别名（`addr` 已被
   本函数的局部变量名占用，故起别名）。

8. **`Open()`**：已有会话时推 `show` 消息。

   ```go
   func (m *Manager) Open() {
   	if m.Running() {
   		m.svc.PushControl(api.Message{Kind: api.MessageKindShow})
   		return
   	}
   	if err := m.Start(); err != nil {
   		logging.Printf("uimgr: cannot open UI: %v", err)
   	}
   }
   ```

9. **`SendShowAddTask`**：推控制消息。**会话不存在时必须返回错误**——
   `main.go` 依赖这个错误把「扩展请求没被接收」如实告诉浏览器，浏览器据此
   退到 lgom:// URL 通道。旧实现里这个错误来自「连接为空」，新实现改用
   `sessionActive`（子进程已拉起且未结束）判断。

   ```go
   // SendShowAddTask 让 UI 弹出「新建下载任务」对话框。
   // 没有活跃的 UI 会话时返回错误——调用方据此告诉浏览器「没收到」。
   func (m *Manager) SendShowAddTask(p api.ShowAddTaskParams) error {
   	m.mu.Lock()
   	active := m.sessionActive
   	m.mu.Unlock()
   	if !active {
   		return fmt.Errorf("uimgr: 没有活跃的 UI 会话")
   	}
   	m.svc.PushControl(api.Message{Kind: api.MessageKindShowAddTask, ShowAddTask: &p})
   	return nil
   }
   ```

   `sessionActive` 在 `Start()` 里置 true、在 `finishSession()` 里置 false。

10. **`Close()`**：推 `close` 消息后强杀子进程。

    ```go
    func (m *Manager) Close() {
    	m.mu.Lock()
    	gen := m.gen
    	cmd := m.cmd
    	m.mu.Unlock()

    	m.svc.PushControl(api.Message{Kind: api.MessageKindClose})
    	if cmd != nil && cmd.Process != nil {
    		var killTimer *time.Timer
    		killTimer = time.AfterFunc(closeGrace, func() { _ = cmd.Process.Kill() })
    		select {
    		case <-time.After(closeGrace):
    		case <-m.waitDone():
    		}
    		killTimer.Stop()
    		_ = cmd.Process.Kill()
    	}
    	m.finishSession(gen)
    }
    ```

11. **删除**：`acceptLoop`、`handshake`、`serveCalls`、`forwardEvents`、`send`、`makeSocketPath`，
    以及 `ipc` 的所有 import。`validateLoop`（文件存在性校验）保留，改为调用 `m.svc` 的
    调度器能力——若 `appservice` 没有暴露 `ValidateFileExistence`，则在 `appservice` 上加一个
    方法：

    ```go
    // ValidateFileExistence 校验运行中任务的落地文件是否还在。
    func (s *Service) ValidateFileExistence() { s.sc.ValidateFileExistence() }
    ```

12. **行为对照清单**：`internal/uimgr` 的旧 `dispatch` 与新的 `appservice.Call`
    是同一套逻辑的两份拷贝，接线换入时必须逐项核对，别只看「能编译」：

    | 旧 `uimgr.dispatch` 方法 | 新位置 | 核对点 |
    |---|---|---|
    | `list` | `appservice.Call` 的 `api.MethodList` | 筛选与排序参数的类型转换一致 |
    | `add` | `api.MethodAdd` | 协议探测、凭据转换、chunk 参数 |
    | `start` / `pause` / `delete` | 对应的三个分支 | 返回的错误语义一致 |
    | `is_preparing` | `api.MethodIsPreparing` | 准备中的判定口径 |
    | `probe` / `probe_bittorrent` | `Service.probe` / `Service.probeBittorrent` | 超时、代理配置、失败降级与日志 |
    | `bt_info` | `api.MethodBTInfo` | 详情快照的字段 |
    | `get_settings` / `save_settings` | 对应的两个分支 | 语言变更回调的触发时机 |

    另外确认旧 `forwardEvents`（事件转发）与新的 `appservice.Events()` 在
    「事件丢弃」「退订」两处的行为一致。

- [ ] **Step 2: 改 `main.go` 的装配**

`main.go` 里：

```go
	// 业务侧应用服务：把 scheduler/store 的能力包装成契约层。
	svc := appservice.New(st, sc)
	svc.SetSettings(cur)

	// UI 管理器：负责拉起/管理 Fyne UI 子进程（自我复刻）。
	uim := uimgr.New(svc)
	uim.SetSettings(cur)
```

命令行参数解析处新增：

```go
	rpcListen := flag.String("rpc-listen", "", "UI channel listen address (unix:<path> or tcp:<host:port>)")
```

并在 `flag.Parse()` 之后、`uim.Start()` 之前把地址写进环境变量（`uimgr` 在 `Start()`
时读取它）：

```go
	if *rpcListen != "" {
		os.Setenv(uimgr.EnvRPCListen, *rpcListen)
	}
```

`uim.OnLanguageChanged` 的 hook 保留不变（它负责刷新 ilocale 与托盘）。

- [ ] **Step 3: 改 `internal/uimgr/manager_test.go` 与 `bt_info_test.go`**

这两个测试直接依赖 `ipc` 与 `Manager` 的内部结构，逐个改写：

**`bt_info_test.go`**：把 `m.dispatch(ipc.MethodBTInfo, raw)` 换成
`svc.Call(ctx, api.MethodBTInfo, &api.BTInfoParams{...}, &api.BTDetail{})`，
返回类型从 `*bittorrent.Detail` 换成 `*api.BTDetail`：

```go
// TestBTInfoEndToEnd 验证「BT 任务详情」这条链路的端到端可用:
// scheduler 启动 BT 任务 → 句柄登记 → appservice 的 bt_info 方法 → 结果。
//
// 这是 UI「任务详情」面板的数据来源:面板每秒钟问一次业务进程要详情,
// 问不到就显示「会话已结束」。这条用例守住「问得到」这一半。
func TestBTInfoEndToEnd(t *testing.T) {
	requireLoopback(t)

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "lgdm.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	sc := scheduler.New(st)
	svc := appservice.New(st, sc)
	t.Cleanup(func() {
		sc.CloseBittorrent()
		_ = st.Close()
	})

	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=lgdm-e2e"
	tk, err := sc.Add(scheduler.AddTaskInput{
		URL:      magnet,
		SavePath: filepath.Join(dir, "task"),
		Protocol: protocol.ProtoBT,
	})
	if err != nil {
		t.Fatalf("scheduler.Add: %v", err)
	}
	if err := sc.Start(tk.ID); err != nil {
		t.Fatalf("scheduler.Start: %v", err)
	}

	// BT 客户端是懒创建的,句柄登记要等它起来——轮询等一小会儿。
	var detail *api.BTDetail
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out := &api.BTDetail{}
		if err := svc.Call(context.Background(), api.MethodBTInfo,
			&api.BTInfoParams{TaskID: tk.ID}, out); err == nil {
			detail = out
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if detail == nil {
		t.Fatal("bt_info never returned a detail for a running BT task")
	}
	if detail.Name != "lgdm-e2e" {
		t.Errorf("detail name = %q, want lgdm-e2e", detail.Name)
	}
	// 元数据还没到(磁力链接没有 peer),总体积应为 0 而不是别的数。
	if detail.Total != 0 {
		t.Errorf("detail total = %d, want 0 before metadata arrives", detail.Total)
	}
	// 结果要能原样过 JSON:UI 进程收到的就是这个编码。
	blob, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	var back api.BTDetail
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if back.Name != detail.Name {
		t.Errorf("round-tripped name = %q, want %q", back.Name, detail.Name)
	}
	// 分片热力图的桶也要能过 JSON:UI 进程收到的就是这个编码。
	if len(back.PieceBuckets) != len(detail.PieceBuckets) {
		t.Errorf("round-tripped piece buckets = %d, want %d",
			len(back.PieceBuckets), len(detail.PieceBuckets))
	}
}

// TestBTInfoUnknownTask 验证任务不在运行时 bt_info 返回错误而不是空
// 详情——UI 据此把面板切到「会话已结束」。
func TestBTInfoUnknownTask(t *testing.T) {
	svc := appservice.New(nil, scheduler.New(nil))
	out := &api.BTDetail{}
	if err := svc.Call(context.Background(), api.MethodBTInfo,
		&api.BTInfoParams{TaskID: "nope"}, out); err == nil {
		t.Fatal("bt_info for an unknown task should fail")
	}
}
```

import 相应调整：去掉 `ipc`，加上 `context`、`encoding/json`、`api`、`appservice`。

**`manager_test.go`**：三个用例的改法。

`TestSendShowAddTask_ConnLive` → 起一个本地服务，连上客户端，断言控制消息送达：

```go
// TestSendShowAddTask_LiveSession 验证「弹新建任务对话框」的请求能通过
// 控制消息通道送到 UI 客户端。
func TestSendShowAddTask_LiveSession(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "lgdm.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	sc := scheduler.New(st)
	svc := appservice.New(st, sc)

	m := New(svc)
	// 单元测试里不拉起子进程，直接把会话标记为活跃。
	m.sessionActive = true

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := filepath.Join(dir, "ui.sock")
	go func() {
		_ = rpc.Serve(ctx, rpc.ServerOptions{
			Transport: rpc.TransportHTTP,
			Addr:      "unix:" + sock,
			Token:     "test-token",
		}, svc)
	}()

	cli, err := dialWithRetry(ctx, rpc.TransportHTTP, "unix:"+sock, "test-token")
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer cli.Close()

	got := make(chan api.ShowAddTaskParams, 1)
	cli.SetOnShowAddTask(func(p api.ShowAddTaskParams) { got <- p })

	want := api.ShowAddTaskParams{URL: "https://example.com/x", Name: "x"}
	if err := m.SendShowAddTask(want); err != nil {
		t.Fatalf("SendShowAddTask: %v", err)
	}
	select {
	case p := <-got:
		if p.URL != want.URL || p.Name != want.Name {
			t.Fatalf("收到的参数不对: %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("show_add_task 没有送达客户端")
	}
}

// dialWithRetry 反复连接直到服务就绪。
func dialWithRetry(ctx context.Context, transport, endpoint, token string) (rpc.Client, error) {
	var cli rpc.Client
	var err error
	for i := 0; i < 50; i++ {
		cli, err = rpc.Dial(ctx, transport, endpoint, token)
		if err == nil {
			return cli, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, err
}
```

`TestSendShowAddTask_NoConn_ReturnsError` → 没有会话时必须报错：

```go
// TestSendShowAddTask_NoSession_ReturnsError 验证没有 UI 会话时如实报错——
// main.go 依赖这个错误告诉浏览器「请求没被接收」。
func TestSendShowAddTask_NoSession_ReturnsError(t *testing.T) {
	m := New(appservice.New(nil, scheduler.New(nil)))
	if err := m.SendShowAddTask(api.ShowAddTaskParams{URL: "https://example.com/x"}); err == nil {
		t.Fatal("没有会话时应当报错")
	}
}
```

`TestSetLanguage_OnLanguageChangedFires` 与 `TestSetLanguage_NoConnStillFiresHook` →
构造方式改成 `New(appservice.New(nil, scheduler.New(nil)))`，其余断言不变
（语言变更的 hook 与有没有连接无关，这正是新实现保留的语义）。

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/uimgr/ ./internal/appservice/`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/uimgr main.go internal/appservice
git commit -m "refactor(uimgr): 用 rpc.Serve 取代手写 IPC 帧循环"
```

---

### Task 10: 接线 UI 侧（`internal/ui` 改用 `rpc.Client`）

**Files:**
- Modify: `internal/ui/child.go`
- Modify: `internal/ui/add_task_dialog.go`
- Modify: `internal/ui/bt_details.go`
- Modify: `internal/ui/chunk_details.go`
- Modify: `internal/ui/main_window.go`
- Modify: `internal/ui/piece_mosaic.go`
- Modify: `internal/ui/settings_dialog.go`
- Modify: `internal/ui/task_list.go`
- Modify: `internal/ui/task_row.go`
- Delete: `internal/ui/client.go`
- Delete: `internal/ui/service.go`
- Modify: `internal/ui/*_test.go`

**Interfaces:**
- Consumes: `internal/rpc`（`rpc.Client`）、`internal/api`、`internal/urlkind`
- Produces: 无（UI 内部结构）

- [ ] **Step 1: 改 `internal/ui/child.go`**

```go
// RunChild 是 UI 子进程角色的入口：
//  1. 读取启动环境变量（rpc.EnvUIChild/EnvUIListen/EnvUIToken/EnvUITransport）。
//  2. 连接业务进程、握手并拿到初始设置。
//  3. 启动 Fyne app，构造主窗口，把 rpc.Client 作为业务服务注入。
//  4. app.Run() 阻塞；窗口关闭（LightMode）或收到业务 close 消息时退出。
func RunChild() int {
	if os.Getenv(rpc.EnvUIChild) != "1" {
		logging.Println("ui.RunChild: not a UI child process (missing env); exiting")
		return 2
	}
	addr := os.Getenv(rpc.EnvUIListen)
	tok := os.Getenv(rpc.EnvUIToken)
	transport := os.Getenv(rpc.EnvUITransport)
	if addr == "" || tok == "" {
		logging.Println("ui.RunChild: missing listen/token env; exiting")
		return 2
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := rpc.Dial(ctx, transport, addr, tok)
	if err != nil {
		logging.Printf("ui.RunChild: connect failed: %v", err)
		return 1
	}

	// 权威语言在握手时就拿到了，直接同步到 ilocale。
	ilocale.Set(c.Settings().Language)

	a := app.NewWithID("com.langbiantianya.LGDM")
	win := NewMainWindow(a, c)

	c.SetOnShow(func() {
		fyne.Do(func() { win.ShowFromTray() })
	})
	c.SetOnShowAddTask(func(p api.ShowAddTaskParams) {
		fyne.Do(func() {
			win.ShowFromTray()
			ShowAddTaskDialogForURL(c, p)
		})
	})
	c.SetOnLanguage(func() {
		fyne.Do(func() { win.applyLanguage() })
	})
	c.SetOnClose(func() {
		fyne.Do(func() { a.Quit() })
	})

	win.Show()
	a.Run()
	win.Close()
	_ = c.Close()
	return 0
}
```

`internal/ui/child.go` 里的 `NewService(socketPath, token)` 删掉——改由 `rpc.Dial` 直接返回客户端。

- [ ] **Step 2: 全量替换 UI 里的类型引用**

用下面的映射逐文件替换（`gofmt -r` 或手工编辑均可）：

| 旧 | 新 |
|---|---|
| `store.Task` | `api.Task` |
| `store.Settings` | `api.Settings` |
| `store.Status` | `api.Status` |
| `store.StatusFilter` | `api.StatusFilter` |
| `store.TaskSort` | `api.TaskSort` |
| `store.TaskStatus.X` | `api.StatusX` |
| `store.FilterAll` | `api.FilterAll` |
| `store.FilterPending` / `FilterDownloading` / `FilterPaused` / `FilterCompleted` / `FilterFailed` / `FilterFileLost` | `api.Filter*` |
| `store.SortCreatedAsc` / `SortCreatedDesc` / `SortNameAsc` / `SortNameDesc` | `api.Sort*` |
| `store.AllTaskSorts()` | `api.AllTaskSorts()` |
| `scheduler.Event` | `api.Event` |
| `scheduler.AddTaskInput` | `api.AddTaskInput` |
| `scheduler.IsBittorrentTask(t)` | `isBittorrentTask(t)`（见下） |
| `scheduler.BittorrentDefaultDir(u)` | `urlkind.BittorrentDefaultDir(u)` |
| `bittorrent.Detail` | `api.BTDetail` |
| `bittorrent.FileProgress` | `api.FileProgress` |
| `protocol.AuthOptions` | `api.AuthOptions` |
| `protocol.ProxyMode` | `api.ProxyMode` |
| `protocol.ProxyModeSystem` / `Disabled` / `Manual` | `api.ProxyModeSystem` / `ProxyModeDisabled` / `ProxyModeManual` |
| `protocol.DetectKind(u, "")` | `urlkind.DetectKind(u, "")` |
| `protocol.IsBitTorrent(u)` | `urlkind.IsBitTorrent(u)` |
| `ipc.ShowAddTaskParams` | `api.ShowAddTaskParams` |
| `Service`（本包接口） | `rpc.Client` |
| `AddTaskInput`（本包） | `api.AddTaskInput` |

**`isBittorrentTask` 的本地实现**：原来在 `internal/scheduler`（依赖 `store.Task`）。UI 侧改成
按协议判定，落到 `internal/ui/task_row.go` 附近的辅助函数：

```go
// isBittorrentTask 报告任务是否走 BT 下载路径。历史数据里 protocol 列为空
// 的任务按 URL 形态兜底判定。
func isBittorrentTask(t *api.Task) bool {
	if t == nil {
		return false
	}
	if api.ProtocolKind(t.Protocol) == api.ProtoBT {
		return true
	}
	if t.Protocol != "" {
		return false
	}
	return urlkind.IsBitTorrent(t.URL)
}
```

- [ ] **Step 3: 删掉 `internal/ui/service.go` 与 `internal/ui/client.go`**

- `service.go`：`Service` 接口与 `AddTaskInput` 被 `rpc.Client` / `api.AddTaskInput` 取代。
- `client.go`：`ipcClient` 的全部能力（请求-应答、事件广播、控制回调、握手）都由
  `internal/rpc` 的共享客户端提供。**其中事件广播、退订、连接断开通知的用例已经在
  Task 4 的 `internal/rpc/client_test.go` 里覆盖**（`TestClientSubscribeAndBroadcast`、
  `TestClientUnsubscribeThenBroadcast`、`TestClientCloseNotifiesHook`、
  `TestClientCloseBeforeHookRegistration`），这里不需要重复写。

- [ ] **Step 4: 改 `internal/ui` 各文件的 import**

- 需要 `api` 的文件加 `"lgo_download_manager/internal/api"`；
- 需要纯 URL 助手的文件加 `"lgo_download_manager/internal/urlkind"`；
- 需要客户端接口的文件加 `"lgo_download_manager/internal/rpc"`；
- **删掉** `"lgo_download_manager/internal/store"`、`"lgo_download_manager/internal/scheduler"`、
  `"lgo_download_manager/internal/protocol"`、`"lgo_download_manager/internal/bittorrent"`、
  `"lgo_download_manager/internal/ipc"` 这些 import。

- [ ] **Step 5: 改 UI 测试**

`internal/ui/add_task_dialog_e2e_test.go` 里的 `localServiceAdapter` 改为实现 `rpc.Client`：

```go
// localClient 是直连业务侧服务的本地客户端——跳过传输层，跑 UI 逻辑。
type localClient struct {
	inner *rpc.Client // 不可用时退化为 nil
}
```

更简单的做法：让测试用**真实的本地传输**（`httpx` 或 `grpcx` + 临时 socket），
这样 UI e2e 同时覆盖了传输层。做法：

```go
// newTestClient 起一个本地服务并返回连好的客户端。
func newTestClient(t *testing.T, sc *scheduler.Scheduler, st *store.Store, transport string) rpc.Client {
	t.Helper()
	svc := appservice.New(st, sc)
	sock := filepath.Join(t.TempDir(), "lgdm.sock")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = rpc.Serve(ctx, rpc.ServerOptions{Transport: transport, Addr: "unix:" + sock, Token: "t"}, svc)
	}()
	var cli rpc.Client
	var err error
	for i := 0; i < 50; i++ {
		cli, err = rpc.Dial(ctx, transport, "unix:"+sock, "t")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	return cli
}
```

然后把每个 UI e2e 测试的传输参数化：

```go
for _, transport := range []string{rpc.TransportHTTP, rpc.TransportGRPC} {
	t.Run(transport, func(t *testing.T) {
		// 原来的测试体
	})
}
```

`bt_details_test.go` 里的 `btStubService` 改成实现 `rpc.Client`（只实现 `BTTaskDetail`
与被调用到的方法，其余返回零值）。

- [ ] **Step 6: 跑测试**

Run: `go test ./internal/ui/`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add internal/ui
git commit -m "refactor(ui): 改用 rpc.Client 与契约 DTO，解除对业务侧包的依赖"
```

---

### Task 11: 删除 `internal/ipc` 与接线 `main.go`

**Files:**
- Delete: `internal/ipc/`（整个包）
- Modify: `main.go`
- Modify: `internal/rpc/registry.go`（补上环境变量常量）

**Interfaces:**
- Consumes: `internal/rpc`
- Produces:
  ```go
  package rpc
  const EnvUIChild = "LDM_UI_CHILD"
  const EnvUIListen = "LDM_UI_LISTEN"
  const EnvUIToken = "LDM_UI_TOKEN"
  const EnvUITransport = "LDM_UI_TRANSPORT"
  ```

- [ ] **Step 1: 在 `internal/rpc/registry.go` 补环境变量常量**

```go
// UI 子进程的启动环境变量（单二进制自我复刻角色切换）。
const (
	EnvUIChild     = "LDM_UI_CHILD"     // "1" 表示当前进程以 UI 子进程角色运行
	EnvUIListen    = "LDM_UI_LISTEN"    // 监听地址（unix 路径或 host:port）
	EnvUIToken     = "LDM_UI_TOKEN"     // 握手 token
	EnvUITransport = "LDM_UI_TRANSPORT" // 传输协议：grpc | http
)
```

- [ ] **Step 2: 改 `main.go`**

1. 新增命令行参数：

```go
	rpcListen := flag.String("rpc-listen", "", "listen address for the UI channel (unix:<path> or tcp:<host:port>)")
```

2. 把地址传给 UI 管理器。`main.go` 里在 `flag.Parse()` 之后：

```go
	if *rpcListen != "" {
		os.Setenv(uimgr.EnvRPCListen, *rpcListen)
	}
```

`uimgr.EnvRPCListen` 就是 `"LDM_RPC_LISTEN"`（Task 9 里导出），不要在 `main.go`
里再定义一份。

3. `main.go` 里所有 `ipc.EnvUIChild` / `ipc.ShowAddTaskParams` 改为
   `rpc.EnvUIChild` / `api.ShowAddTaskParams`；import 换成
   `"lgo_download_manager/internal/rpc"` 与 `"lgo_download_manager/internal/api"`。

4. `handleRequest` 里的 `uim.SendShowAddTask(ipc.ShowAddTaskParams{...})` 改为
   `uim.SendShowAddTask(api.ShowAddTaskParams{...})`。

5. `if os.Getenv(ipc.EnvUIChild) == "1"` 改为 `if os.Getenv(rpc.EnvUIChild) == "1"`。

- [ ] **Step 3: 删除 `internal/ipc`**

```bash
git rm -r internal/ipc
```

- [ ] **Step 4: 确认没有残留引用**

Run: `grep -rn "internal/ipc" --include=*.go .`
Expected: 无输出

- [ ] **Step 5: 跑构建与测试**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全部通过

- [ ] **Step 6: 提交**

```bash
git add -A
git commit -m "refactor(ipc): 删除自定义 JSON 帧协议"
```

---

### Task 12: 拆分 `internal/engine` 并补边界测试

**Files:**
- Modify: `internal/engine/engine.go` → 拆成 `engine.go` / `plan.go` / `chunk.go` / `progress.go` / `retry.go`
- Create: `internal/ui/boundary_test.go`
- Create: `internal/rpc/boundary_test.go`

**Interfaces:**
- Consumes: 无（纯重构 + 测试）
- Produces: 无（公开 API 不变）

- [ ] **Step 1: 拆 `internal/engine`**

按下面的归属把 `internal/engine/engine.go` 的内容移动过去，**只移动、不改逻辑**：

| 目标文件 | 从 engine.go 移过去的内容 |
|---|---|
| `engine.go` | 包注释、`Progress`、`Options`、`defaults`、`silentLogger`、`Job`、`chunk` 结构、`NewJob`、`Run`、`isStreaming` 与 `runStreaming` 闭包、`streamingChunk`、`Close`、`sanityCheck` |
| `plan.go` | `plan`、`planLocked`、`resumedBytes`、`chunkConcurrencyLimit`、`unfinishedIndexes`、`replan`、`Chunks`、`ChunkSnapshot` |
| `chunk.go` | `downloadChunk`、`downloadFallbackWithRetry`、`throttle` |
| `progress.go` | `emit` 闭包及其节流状态（`bytesDone` / `chunksDone` / `lastTickAt` / `lastTickVal` 的节流部分） |
| `retry.go` | `maxChunkRetries`、`maxReplanRounds`、`errJobStopped`、`jitter`、`retryDelay`、`waitRetry` |

`Run` 里的进度节流状态（`bytesDone` 等）同时被 `chunk.go` 使用——**保留在 `engine.go` 的
`Run` 作用域内**，通过参数传给 `downloadChunk`（它已经是参数形式：`bytesDone`、`chunksDone`、
`emit`），无需额外调整。`emit` 闭包定义在 `Run` 里，`progress.go` 里只放纯函数：

```go
// speed 计算两次采样之间的平均速度（字节/秒）。
func speed(delta int64, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(delta) / elapsed.Seconds()
}
```

`Run` 里原来的速度计算改为调用它，行为等价。

拆分后 `internal/engine` 的文件职责：

```
engine.go    Job 生命周期与 worker 池编排
plan.go      分片规划与重规划
chunk.go     单分片/流式下载与限速
progress.go  速度计算
retry.go     重试与退避
```

- [ ] **Step 2: 跑引擎测试确认无回归**

Run: `go test ./internal/engine/ -v`
Expected: PASS（现有 1131 行测试全部保持通过）

- [ ] **Step 3: 建 `internal/ui/boundary_test.go`**

```go
package ui

import (
	"os/exec"
	"strings"
	"testing"
)

// TestUIDependsOnlyOnContract 断言 UI 包只依赖契约层，不直接依赖
// store / scheduler / engine / protocol / bittorrent。
func TestUIDependsOnlyOnContract(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "lgo_download_manager/internal/ui").Output()
	if err != nil {
		t.Fatalf("go list -deps 失败: %v", err)
	}
	deps := strings.Split(string(out), "\n")
	forbidden := []string{
		"lgo_download_manager/internal/store",
		"lgo_download_manager/internal/scheduler",
		"lgo_download_manager/internal/engine",
		"lgo_download_manager/internal/protocol",
		"lgo_download_manager/internal/bittorrent",
		"lgo_download_manager/internal/appservice",
		"lgo_download_manager/internal/ipc",
	}
	for _, d := range deps {
		d = strings.TrimSpace(d)
		for _, f := range forbidden {
			if d == f {
				t.Fatalf("internal/ui 不得依赖 %s", f)
			}
		}
	}
}
```

- [ ] **Step 4: 建 `internal/rpc/boundary_test.go`**

```go
package rpc

import (
	"os/exec"
	"strings"
	"testing"
)

// TestRPCLayerIsIndependent 断言传输层与契约层不反向依赖业务侧包。
func TestRPCLayerIsIndependent(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "lgo_download_manager/internal/rpc").Output()
	if err != nil {
		t.Fatalf("go list -deps 失败: %v", err)
	}
	deps := strings.Split(string(out), "\n")
	forbidden := []string{
		"lgo_download_manager/internal/store",
		"lgo_download_manager/internal/scheduler",
		"lgo_download_manager/internal/engine",
		"lgo_download_manager/internal/ui",
		"lgo_download_manager/internal/uimgr",
	}
	for _, d := range deps {
		d = strings.TrimSpace(d)
		for _, f := range forbidden {
			if d == f {
				t.Fatalf("internal/rpc 不得依赖 %s", f)
			}
		}
	}
}

// TestTransportsDoNotImportRPC 保证两个传输实现不反向 import 传输层，
// 否则会形成 rpc → httpx → rpc 的包环。
func TestTransportsDoNotImportRPC(t *testing.T) {
	for _, pkg := range []string{
		"lgo_download_manager/internal/rpc/httpx",
		"lgo_download_manager/internal/rpc/grpcx",
	} {
		out, err := exec.Command("go", "list", "-deps", pkg).Output()
		if err != nil {
			t.Fatalf("go list -deps %s 失败: %v", pkg, err)
		}
		for _, d := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(d) == "lgo_download_manager/internal/rpc" {
				t.Fatalf("%s 不得 import internal/rpc", pkg)
			}
		}
	}
}
```

- [ ] **Step 5: 跑测试**

Run: `go test ./internal/ui/ ./internal/rpc/ -run "TestUIDepends|TestRPCLayer|TestTransports"`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/engine internal/ui/boundary_test.go internal/rpc/boundary_test.go
git commit -m "refactor(engine): 按职责拆分下载引擎并补依赖边界测试"
```

---

### Task 13: CI、文档与收尾清理

**Files:**
- Modify: `.github/workflows/linux.yml`
- Modify: `README.md`
- Create: `scripts/check_openapi.sh`（可选：本地校验规范同步）
- Modify: 各个薄封装文件（删掉零引用的兼容层）

**Interfaces:**
- Consumes: 全部前置任务
- Produces: 无

- [ ] **Step 1: 清理零引用的薄封装**

逐个确认下面这些兼容层是否还有人用；没人用的直接删掉：

```bash
grep -rn "protocol.DetectKind\|protocol.IsBitTorrent\|protocol.ResolveURL\|protocol.MagnetName\|protocol.MagnetInfoHash\|protocol.TorrentNameFromPath\|protocol.SafeDirName\|scheduler.BittorrentDefaultDir" --include=*.go .
```

- `protocol.DetectKind` / `protocol.IsBitTorrent` / `protocol.ResolveURL`：`internal/protocol`
  内部（`factory.go` 的 `New`、`scheduler`、`store`）与 `main.go` 都还在用 → **保留**。
- `protocol.MagnetName` / `MagnetInfoHash` / `TorrentNameFromPath` / `SafeDirName`：
  如果 `protocol` 包内部已无调用（迁移到 `urlkind` 后）且外部也不用 → **删掉这几个薄封装**。
- `scheduler.BittorrentDefaultDir`：如果只剩 `internal/ui` 在用（已改成 `urlkind.BittorrentDefaultDir`）
  → **删掉**。

删完后重新跑一遍上面的 grep，确认没有残留。

- [ ] **Step 2: 更新 CI**

`.github/workflows/linux.yml` 的 cgo-free 交叉编译包列表追加：

```
            ./internal/api/
            ./internal/appservice/
            ./internal/rpc/
            ./internal/rpc/addr/
            ./internal/rpc/grpcx/
            ./internal/rpc/httpx/
            ./internal/urlkind/
```

并新增一个校验生成物同步性的 job：

```yaml
  proto-sync:
    name: protobuf generated code is up to date
    runs-on: ubuntu-24.04
    steps:
      - name: checkout
        uses: actions/checkout@v4
      - name: setup Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.26.6'
          cache: true
      - name: install protobuf toolchain
        run: |
          set -euo pipefail
          go install github.com/bufbuild/buf/cmd/buf@latest
          go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
          go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
      - name: regenerate and diff
        run: |
          set -euo pipefail
          PATH="$(go env GOPATH)/bin:$PATH" ./scripts/gen_proto.sh
          git diff --exit-code internal/rpc/grpcx/pb
```

- [ ] **Step 3: 更新 README**

在 README 里补三段：

1. **架构**：说明业务进程与 UI 子进程之间现在走 `internal/rpc` 这一层，
   提供 gRPC 与 net/http 两种传输，默认 gRPC。
2. **参数**：

```markdown
### UI 通道参数

| 参数 | 环境变量 | 说明 |
|---|---|---|
| `--rpc-listen <addr>` | `LDM_RPC_LISTEN` | UI 通道的监听地址。`unix:<path>`（默认）或 `tcp:<host:port>` |
| — | `LDM_UI_TRANSPORT` | 传输协议：`grpc`（默认）或 `http` |
```

3. **HTTP 通道文档**：说明 `--rpc-listen tcp:127.0.0.1:17830` 之后，
   浏览器打开 `http://127.0.0.1:17830/docs` 可以看 Swagger UI，
   `http://127.0.0.1:17830/openapi.yaml` 是规范原文。

同时把 README 里描述「Unix socket + 长度前缀 JSON 帧」的段落改成新的分层描述。

- [ ] **Step 4: 跑全量验证**

Run: `go build ./... && go vet ./... && go test ./... && go test -race ./internal/rpc/... ./internal/appservice/ ./internal/ui/`
Expected: 全部通过

- [ ] **Step 5: 手工验收**

```bash
# 默认 gRPC 传输
go run . --no-gui &
# 切到 HTTP 传输并监听 TCP
LDM_RPC_LISTEN=tcp:127.0.0.1:17830 LDM_UI_TRANSPORT=http go run . --no-gui &
```

第二条命令起来后：

```bash
curl -s http://127.0.0.1:17830/openapi.yaml | head -5
curl -s -X POST http://127.0.0.1:17830/lgdm.v1.DownloadService/list \
  -H 'Authorization: Bearer <token>' -d '{"filter":"all","sort":"created_desc"}'
```

第二条会返回 401（没有正确的 token）——这正是期望行为。

- [ ] **Step 6: 提交**

```bash
git add -A
git commit -m "ci: 覆盖新包的交叉编译并校验 protobuf 生成物；docs: 更新架构说明"
```

---

## 附：执行顺序与依赖

```
Task 1  urlkind
  ↓
Task 2  api
  ↓
Task 3  appservice
  ↓
Task 4  rpc 核心（addr / 接口 / 共享客户端）
  ↓
Task 5  httpx
  ↓
Task 6  httpx 的 OpenAPI + Swagger
  ↓
Task 7  grpcx（proto + 生成 + 映射）+ rpc 注册表 Serve/Dial
  ↓
Task 8  契约测试（两种传输）
  ↓
Task 9  uimgr 接线
  ↓
Task 10 UI 接线
  ↓
Task 11 删除 internal/ipc + main.go
  ↓
Task 12 引擎拆分 + 边界测试
  ↓
Task 13 CI + 文档 + 清理
```

每个任务结束时仓库都能编译、测试全绿（与全局约束一致）。注册表 `Serve`/`Dial` 放在
Task 7，因为它是唯一同时引用两种传输实现的地方。
