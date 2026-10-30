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
internal/rpc/httpx/docs.go              /openapi.yaml、/openapi.json、/docs
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

// toAPIAuthOptions 把契约层的凭据转成协议层的凭据。
func toAPIAuthOptions(a api.AuthOptions) protocol.AuthOptions {
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

// toStoreAuthOptions 是 toAPIAuthOptions 的逆变换。
func toStoreAuthOptions(a protocol.AuthOptions) api.AuthOptions {
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
			Auth:         toAPIAuthOptions(p.Auth),
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
		size, err := s.probe(p.URL)
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
func (s *Service) probe(raw string) (int64, error) {
	kind, err := protocol.DetectKind(raw, "")
	if err != nil {
		return 0, err
	}
	if kind == protocol.ProtoBT {
		return s.probeBittorrent(raw)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	caps, err := driver.Probe(ctx)
	if err != nil {
		return 0, err
	}
	return caps.TotalSize, nil
}

// probeBittorrent 解析 .torrent 种子文件的总体积。磁力链接缺元数据，
// 返回 0(体积未知)。任何取种子的失败都当作「未知」而不是错误。
func (s *Service) probeBittorrent(raw string) (int64, error) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "magnet:") {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		data, err := bittorrent.FetchTorrentFile(ctx, raw, protocol.HTTPProxyFunc())
		if err != nil {
			return 0, nil
		}
		if _, total, perr := bittorrent.TorrentFileInfo(data); perr == nil {
			return total, nil
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

	s := New(st, nil)
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

import "testing"

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
	"errors"
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
	tr    Transport
	token string

	settingsMu sync.Mutex
	settings   api.Settings

	subMu sync.Mutex
	subs  []chan api.Event

	hookMu       sync.Mutex
	onShow       func()
	onShowAddTask func(api.ShowAddTaskParams)
	onLanguage   func()
	onClose      func()

	quit   chan struct{}
	closed bool
	once   sync.Once
}

var _ Client = (*client)(nil)

// newClient 建立客户端：先用 get_settings 做一次往返，既校验 token 又拿到
// 初始设置快照，然后拉起消息流。
func newClient(ctx context.Context, tr Transport) (*client, error) {
	c := &client{tr: tr, quit: make(chan struct{})}

	out := &api.Settings{}
	if err := c.call(ctx, api.MethodGetSettings, &api.Empty{}, out); err != nil {
		return nil, fmt.Errorf("rpc: 握手失败: %w", err)
	}
	c.settingsMu.Lock()
	c.settings = *out
	c.settingsMu.Unlock()

	go c.readLoop(ctx)
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
		if h := c.showAddTaskHook(); h != nil && msg.ShowAddTask != nil {
			h(*msg.ShowAddTask)
		}
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

func (c *client) showAddTaskHook() func(api.ShowAddTaskParams) {
	c.hookMu.Lock()
	defer c.hookMu.Unlock()
	return c.onShowAddTask
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
	c.hookMu.Lock()
	c.onShowAddTask = f
	c.hookMu.Unlock()
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
		close(c.quit)
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
func (c *client) Close() error {
	c.notifyClose()
	return nil
}

// errClosed 表示连接已经断开。
var errClosed = errors.New("rpc: 连接已关闭")
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
- Create: `internal/rpc/httpx/server.go`
- Create: `internal/rpc/httpx/client.go`
- Create: `internal/rpc/httpx/httpx_test.go`

**Interfaces:**
- Consumes: `internal/api`、`internal/rpc/addr`、`internal/rpc`（仅用 `api` 与 `addr`；**不得** import `internal/rpc`）
- Produces:
  ```go
  package httpx
  type Client struct { /* ... */ }
  func Dial(ctx context.Context, a addr.Addr, token string) (*Client, error)
  func Serve(ctx context.Context, a addr.Addr, token string, h rpcHandler) error
  // rpcHandler 是 httpx 内部定义的最小接口（结构化满足 rpc.Handler）：
  //   interface { Call(ctx, method string, in, out any) error
  //               Events() (<-chan api.Event, func())
  //               Controls() <-chan api.Message }
  ```

- [ ] **Step 1: 建 `internal/rpc/httpx/client.go`**

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
	"fmt"
	"io"
	"net/http"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
)

// servicePrefix 是所有业务端点的路径前缀。
const servicePrefix = "/lgdm.v1.DownloadService/"

// Client 是 net/http 传输的客户端。
type Client struct {
	base  string
	token string
	http  *http.Client
}

// Dial 按地址建立 HTTP 客户端。token 随每次请求发送。
func Dial(ctx context.Context, a addr.Addr, token string) (*Client, error) {
	host := a.Address
	if a.Network == "unix" {
		// unix socket 没有主机名，用一个占位主机名即可。
		host = "lgdm"
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return addr.Dial(a)
		},
	}
	return &Client{
		base:  "http://" + host + servicePrefix,
		token: token,
		http:  &http.Client{Transport: tr},
	}, nil
}

// Call 执行一次调用。
func (c *Client) Call(ctx context.Context, method string, in, out any) error {
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
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return fmt.Errorf("httpx: %s: %s", method, e.Error)
		}
		return fmt.Errorf("httpx: %s: HTTP %d", method, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("httpx: 解码 %s 响应: %w", method, err)
	}
	return nil
}

// Stream 打开消息流。响应是 NDJSON，每行一个 api.Message。
func (c *Client) Stream(ctx context.Context) (<-chan api.Message, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+api.MethodStreamMessages, nil)
	if err != nil {
		return nil, fmt.Errorf("httpx: 构造消息流请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/x-ndjson")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpx: 打开消息流: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("httpx: 打开消息流: HTTP %d", resp.StatusCode)
	}
	out := make(chan api.Message, 64)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		dec := json.NewDecoder(resp.Body)
		for {
			var msg api.Message
			if err := dec.Decode(&msg); err != nil {
				return
			}
			select {
			case out <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
```

补上 `"net"` 的 import。

- [ ] **Step 2: 建 `internal/rpc/httpx/server.go`**

```go
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
)

// Handler 是 httpx 需要的业务侧能力。结构上与 rpc.Handler 相同，
// 这里独立声明以避免 httpx → rpc 的包环（rpc → httpx 已经存在）。
type Handler interface {
	Call(ctx context.Context, method string, in, out any) error
	Events() (<-chan api.Event, func())
	Controls() <-chan api.Message
}

// Serve 在地址上启动 HTTP 服务。
func Serve(ctx context.Context, a addr.Addr, token string, h Handler) error {
	ln, cleanup, err := addr.Listen(a)
	if err != nil {
		return err
	}
	defer cleanup()

	srv := &http.Server{
		Handler:           newMux(h, token),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	err = srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// newMux 组装路由与中间件。
func newMux(h Handler, token string) *http.ServeMux {
	mux := http.NewServeMux()
	authed := func(fn func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !validToken(r.Header.Get("Authorization"), token) {
				writeError(w, http.StatusUnauthorized, "无效的 token")
				return
			}
			fn(w, r)
		}
	}
	for _, m := range api.AllMethods {
		if m == api.MethodStreamMessages {
			continue
		}
		method := m
		mux.HandleFunc(servicePrefix+method, authed(func(w http.ResponseWriter, r *http.Request) {
			serveCall(w, r, h, method)
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
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if err := enc.Encode(api.Message{Kind: api.MessageKindEvent, Event: &ev}); err != nil {
				return
			}
			flusher.Flush()
		case ctl, ok := <-controls:
			if !ok {
				return
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
	if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		return false
	}
	return header[len(prefix):] == token
}

// writeError 写出错误响应。
func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}
```

若 `net` 未被实际使用，删掉那行 `var _ = net.Dial` 与对应 import。

- [ ] **Step 3: 写失败测试**

创建 `internal/rpc/httpx/httpx_test.go`：

```go
package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		out.(*api.Settings).Language = "en"
	case api.MethodList:
		out.(*api.ListResult).Tasks = []*api.Task{{ID: "t1"}}
	case api.MethodIsPreparing:
		out.(*api.BoolResult).Value = true
	}
	return nil
}
func (s *stubHandler) Events() (<-chan api.Event, func()) { return s.events, func() {} }
func (s *stubHandler) Controls() <-chan api.Message       { return s.controls }

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

func TestServeStream(t *testing.T) {
	h := &stubHandler{events: make(chan api.Event, 4), controls: make(chan api.Message, 4)}
	mux := newMux(h, "secret")

	req := httptest.NewRequest(http.MethodPost, servicePrefix+api.MethodStreamMessages, nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(rec, req)
		close(done)
	}()

	h.events <- api.Event{Why: "progress"}
	h.controls <- api.Message{Kind: api.MessageKindShow}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("消息流没有输出")
		case <-done:
			t.Fatal("消息流提前结束")
		default:
		}
		if rec.Body.Len() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	body := rec.Body.String()
	if body == "" {
		t.Fatal("消息流没有内容")
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/rpc/httpx/`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/rpc/httpx
git commit -m "feat(httpx): 实现基于 net/http 的传输"
```

---

### Task 6: `internal/rpc/httpx` 的 OpenAPI 规范与 Swagger 文档页

**Files:**
- Create: `internal/rpc/httpx/openapi.yaml`
- Create: `internal/rpc/httpx/docs.go`
- Create: `internal/rpc/httpx/openapi_test.go`
- Modify: `internal/rpc/httpx/server.go`（挂上三个文档端点）

**Interfaces:**
- Consumes: `internal/api`（Task 2）
- Produces:
  ```go
  package httpx
  func openAPISpec() []byte            // 嵌入的规范原文
  func openAPIJSON() ([]byte, error)   // 规范转成的 JSON（缓存）
  func registerDocs(mux *http.ServeMux) // 注册 /openapi.yaml、/openapi.json、/docs
  ```

- [ ] **Step 1: 写 `internal/rpc/httpx/openapi.yaml`**

```yaml
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
servers:
  - url: http://lgdm/lgdm.v1.DownloadService
    description: 本机 Unix socket（或 LDM_RPC_LISTEN 指定的 host:port）
security:
  - bearerAuth: []
paths:
  /lgdm.v1.DownloadService/list:
    post:
      summary: 查询任务列表
      operationId: list
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/ListParams" }
      responses:
        "200":
          description: 任务列表
          content:
            application/json:
              schema: { $ref: "#/components/schemas/ListResult" }
        "401": { $ref: "#/components/responses/Unauthorized" }
  /lgdm.v1.DownloadService/add:
    post:
      summary: 添加下载任务
      operationId: add
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/AddTaskInput" }
      responses:
        "200":
          description: 新建的任务
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Task" }
        "401": { $ref: "#/components/responses/Unauthorized" }
  /lgdm.v1.DownloadService/start:
    post:
      summary: 启动任务
      operationId: start
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/TaskRef" }
      responses:
        "200": { description: 已启动 }
        "401": { $ref: "#/components/responses/Unauthorized" }
  /lgdm.v1.DownloadService/pause:
    post:
      summary: 暂停任务
      operationId: pause
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/TaskRef" }
      responses:
        "200": { description: 已暂停 }
        "401": { $ref: "#/components/responses/Unauthorized" }
  /lgdm.v1.DownloadService/delete:
    post:
      summary: 删除任务
      operationId: delete
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/TaskRef" }
      responses:
        "200": { description: 已删除 }
        "401": { $ref: "#/components/responses/Unauthorized" }
  /lgdm.v1.DownloadService/is_preparing:
    post:
      summary: 报告任务是否处于「已预留 slot 但引擎未启动」的准备阶段
      operationId: isPreparing
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/TaskRef" }
      responses:
        "200":
          description: 准备阶段标记
          content:
            application/json:
              schema: { $ref: "#/components/schemas/BoolResult" }
        "401": { $ref: "#/components/responses/Unauthorized" }
  /lgdm.v1.DownloadService/probe:
    post:
      summary: 探测 URL 目标大小
      operationId: probe
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/ProbeParams" }
      responses:
        "200":
          description: 目标大小（未知时为 0）
          content:
            application/json:
              schema: { $ref: "#/components/schemas/ProbeResult" }
        "401": { $ref: "#/components/responses/Unauthorized" }
  /lgdm.v1.DownloadService/get_settings:
    post:
      summary: 读取权威设置快照
      operationId: getSettings
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/Empty" }
      responses:
        "200":
          description: 设置快照
          content:
            application/json:
              schema: { $ref: "#/components/schemas/Settings" }
        "401": { $ref: "#/components/responses/Unauthorized" }
  /lgdm.v1.DownloadService/save_settings:
    post:
      summary: 持久化设置
      operationId: saveSettings
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/Settings" }
      responses:
        "200": { description: 已保存 }
        "401": { $ref: "#/components/responses/Unauthorized" }
  /lgdm.v1.DownloadService/bt_info:
    post:
      summary: 查询 BT 任务实时详情
      operationId: btInfo
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/BTInfoParams" }
      responses:
        "200":
          description: BT 详情快照
          content:
            application/json:
              schema: { $ref: "#/components/schemas/BTDetail" }
        "401": { $ref: "#/components/responses/Unauthorized" }
  /lgdm.v1.DownloadService/stream_messages:
    post:
      summary: 订阅消息流（进度事件 + 控制消息）
      operationId: streamMessages
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/Empty" }
      responses:
        "200":
          description: NDJSON 流，每行一个消息
          content:
            application/x-ndjson:
              schema: { $ref: "#/components/schemas/Message" }
        "401": { $ref: "#/components/responses/Unauthorized" }
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
  responses:
    Unauthorized:
      description: token 缺失或无效
      content:
        application/json:
          schema: { $ref: "#/components/schemas/Error" }
  schemas:
    Empty:
      type: object
    Error:
      type: object
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
        show_add_task:
          type: object
          properties:
            url: { type: string }
            name: { type: string }
            ua: { type: string }
            cookies: { type: string }
        language: { type: string }
```

- [ ] **Step 2: 建 `internal/rpc/httpx/docs.go`**

```go
package httpx

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sync"

	"go.yaml.in/yaml/v3"
)

//go:embed openapi.yaml
var openapiYAML []byte

// openAPIJSON 缓存规范转成的 JSON 形式。
var (
	openAPIJSONOnce sync.Once
	openAPIJSONData []byte
	openAPIJSONErr  error
)

// openAPISpec 返回嵌入的 OpenAPI 规范原文。
func openAPISpec() []byte { return openapiYAML }

// openAPIJSON 返回规范的 JSON 形式。转换只做一次，之后走缓存。
func openAPIJSON() ([]byte, error) {
	openAPIJSONOnce.Do(func() {
		var v any
		if err := yaml.Unmarshal(openapiYAML, &v); err != nil {
			openAPIJSONErr = err
			return
		}
		openAPIJSONData, openAPIJSONErr = json.Marshal(v)
	})
	return openAPIJSONData, openAPIJSONErr
}

// registerDocs 注册文档端点。它们不参与 token 校验——只暴露接口形状，
// 不暴露任务数据。
func registerDocs(mux *http.ServeMux) {
	mux.HandleFunc("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(openAPISpec())
	})
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		raw, err := openAPIJSON()
		if err != nil {
			http.Error(w, "openapi: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(swaggerPage))
	})
}

// swaggerPage 是一个离线的 Swagger UI 页面：UI 资源用 CDN 加载，
// 规范本体从同源的 /openapi.json 取。CDN 不可达时页面仍会提示用户
// 直接打开 /openapi.yaml。
const swaggerPage = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>LGDM Download Service — API 文档</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
window.addEventListener('load', function () {
  if (typeof SwaggerUIBundle === 'undefined') {
    document.getElementById('swagger-ui').innerHTML =
      '<p>无法加载 Swagger UI 资源（离线环境）。' +
      '请直接查看 <a href="/openapi.yaml">/openapi.yaml</a> 或 ' +
      '<a href="/openapi.json">/openapi.json</a>。</p>';
    return;
  }
  window.ui = SwaggerUIBundle({ url: '/openapi.json', dom_id: '#swagger-ui' });
});
</script>
</body>
</html>`
```

- [ ] **Step 3: 在 `server.go` 里挂上文档端点**

在 `newMux` 函数的 `mux := http.NewServeMux()` 之后加一行：

```go
	registerDocs(mux)
```

- [ ] **Step 4: 写规范一致性测试**

创建 `internal/rpc/httpx/openapi_test.go`：

```go
package httpx

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"lgo_download_manager/internal/api"
)

func TestOpenAPIParses(t *testing.T) {
	raw, err := openAPIJSON()
	if err != nil {
		t.Fatalf("规范转 JSON 失败: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["openapi"] != "3.0.3" {
		t.Fatalf("openapi 版本不对: %v", doc["openapi"])
	}
}

func TestOpenAPICoversAllMethods(t *testing.T) {
	raw, err := openAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, m := range api.AllMethods {
		p := servicePrefix + m
		if _, ok := doc.Paths[p]; !ok {
			t.Fatalf("规范缺少方法 %q 的路径 %q", m, p)
		}
	}
}

func TestOpenAPISchemasMatchDTO(t *testing.T) {
	raw, err := openAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		schema string
		dto    any
	}{
		{"Task", api.Task{}},
		{"Settings", api.Settings{}},
		{"Event", api.Event{}},
		{"BTDetail", api.BTDetail{}},
		{"FileProgress", api.FileProgress{}},
		{"AddTaskInput", api.AddTaskInput{}},
		{"AuthOptions", api.AuthOptions{}},
	}
	for _, c := range cases {
		s, ok := doc.Components.Schemas[c.schema]
		if !ok {
			t.Fatalf("规范缺少 schema %q", c.schema)
		}
		want := jsonFields(reflect.TypeOf(c.dto))
		got := make([]string, 0, len(s.Properties))
		for k := range s.Properties {
			got = append(got, k)
		}
		if !sameSet(want, got) {
			t.Fatalf("schema %q 的字段与 DTO 不一致:\nDTO  = %v\n规范 = %v", c.schema, want, got)
		}
	}
}

// jsonFields 返回结构体的 json tag 字段名集合。
func jsonFields(t reflect.Type) []string {
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out = append(out, name)
	}
	return out
}

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
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/rpc/httpx/`
Expected: PASS。若 `TestOpenAPISchemasMatchDTO` 失败，按提示补齐 YAML 里的字段或修正 DTO。

- [ ] **Step 6: 提交**

```bash
git add internal/rpc/httpx
git commit -m "feat(httpx): 增加 OpenAPI 规范与 Swagger 文档页"
```

---

### Task 7: 实现 `internal/rpc/grpcx`（gRPC 传输）

**Files:**
- Create: `proto/lgdm/v1/lgdm.proto`
- Create: `buf.yaml`
- Create: `buf.gen.yaml`
- Create: `scripts/gen_proto.sh`
- Create: `internal/rpc/grpcx/server.go`
- Create: `internal/rpc/grpcx/client.go`
- Create: `internal/rpc/grpcx/convert.go`
- Create: `internal/rpc/grpcx/convert_test.go`
- Create: `internal/rpc/registry.go`
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

- [ ] **Step 1: 写 `proto/lgdm/v1/lgdm.proto`**

```proto
syntax = "proto3";

package lgdm.v1;

option go_package = "lgo_download_manager/internal/rpc/grpcx/pb;pb";

// DownloadService 是业务主进程暴露给 UI 子进程的下载管理服务。
// 与 internal/api 的契约一一对应。
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

enum Status {
  STATUS_UNSPECIFIED = 0;
  STATUS_PENDING = 1;
  STATUS_DOWNLOADING = 2;
  STATUS_PAUSED = 3;
  STATUS_COMPLETED = 4;
  STATUS_FAILED = 5;
  STATUS_FILE_LOST = 6;
}

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

enum TaskSort {
  TASK_SORT_UNSPECIFIED = 0;
  TASK_SORT_CREATED_DESC = 1;
  TASK_SORT_CREATED_ASC = 2;
  TASK_SORT_NAME_ASC = 3;
  TASK_SORT_NAME_DESC = 4;
}

enum ProxyMode {
  PROXY_MODE_UNSPECIFIED = 0;
  PROXY_MODE_SYSTEM = 1;
  PROXY_MODE_DISABLED = 2;
  PROXY_MODE_MANUAL = 3;
}

enum ProtocolKind {
  PROTOCOL_KIND_UNSPECIFIED = 0;
  PROTOCOL_KIND_HTTP = 1;
  PROTOCOL_KIND_HTTPS = 2;
  PROTOCOL_KIND_FTP = 3;
  PROTOCOL_KIND_WEBDAV = 4;
  PROTOCOL_KIND_BT = 5;
}

message Empty {}

message TaskRef {
  string id = 1;
}

message BoolResponse {
  bool value = 1;
}

message ProbeRequest {
  string url = 1;
}

message ProbeResponse {
  int64 total_size = 1;
}

message ListRequest {
  string filter = 1;
  string sort = 2;
}

message ListResponse {
  repeated Task tasks = 1;
}

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

message AddTaskRequest {
  string url = 1;
  string save_path = 2;
  ProtocolKind protocol = 3;
  AuthOptions auth = 4;
  int32 chunk_count = 5;
  int64 min_chunk_size = 6;
}

message AddTaskResponse {
  Task task = 1;
}

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
  string created_at = 21;
  string updated_at = 22;
  string completed_at = 23;
  string bt_file = 24;
}

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

message GetSettingsResponse {
  Settings settings = 1;
}

message SaveSettingsRequest {
  Settings settings = 1;
}

message BTInfoRequest {
  string task_id = 1;
}

message FileProgress {
  string path = 1;
  int64 length = 2;
  int64 completed = 3;
}

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

message BTDetailResponse {
  BTDetail detail = 1;
}

message Event {
  string why = 1;
  Task task = 2;
  double speed_bps = 3;
}

message ShowAddTaskParams {
  string url = 1;
  string name = 2;
  string ua = 3;
  string cookies = 4;
}

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

- [ ] **Step 2: 建 buf 配置与生成脚本**

`buf.yaml`：

```yaml
version: v2
modules:
  - path: proto
lint:
  use:
    - STANDARD
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
set -euo pipefail
cd "$(dirname "$0")/.."
buf generate
gofmt -w internal/rpc/grpcx/pb
```

```bash
chmod +x scripts/gen_proto.sh
```

- [ ] **Step 3: 生成 pb 代码**

Run: `PATH="$(go env GOPATH)/bin:$PATH" ./scripts/gen_proto.sh`
Expected: 生成 `internal/rpc/grpcx/pb/lgdm.pb.go` 与 `internal/rpc/grpcx/pb/lgdm_grpc.pb.go`

若 `buf` 未安装：

```bash
go install github.com/bufbuild/buf/cmd/buf@latest
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

- [ ] **Step 4: 加依赖**

Run: `go get google.golang.org/grpc@latest google.golang.org/protobuf@latest && go mod tidy`
Expected: `go.mod` 里出现这两个直接依赖

- [ ] **Step 5: 建 `internal/rpc/grpcx/convert.go`**

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

package grpcx

import (
	"fmt"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/grpcx/pb"
)

// ---- 状态枚举映射 ----

var statusToPB = map[api.Status]pb.Status{
	api.StatusPending:     pb.Status_STATUS_PENDING,
	api.StatusDownloading: pb.Status_STATUS_DOWNLOADING,
	api.StatusPaused:      pb.Status_STATUS_PAUSED,
	api.StatusCompleted:   pb.Status_STATUS_COMPLETED,
	api.StatusFailed:      pb.Status_STATUS_FAILED,
	api.StatusFileLost:    pb.Status_STATUS_FILE_LOST,
}

var statusFromPB = map[pb.Status]api.Status{
	pb.Status_STATUS_PENDING:     api.StatusPending,
	pb.Status_STATUS_DOWNLOADING: api.StatusDownloading,
	pb.Status_STATUS_PAUSED:      api.StatusPaused,
	pb.Status_STATUS_COMPLETED:   api.StatusCompleted,
	pb.Status_STATUS_FAILED:      api.StatusFailed,
	pb.Status_STATUS_FILE_LOST:   api.StatusFileLost,
}

var filterToPB = map[api.StatusFilter]pb.StatusFilter{
	api.FilterAll:         pb.StatusFilter_STATUS_FILTER_ALL,
	api.FilterPending:     pb.StatusFilter_STATUS_FILTER_PENDING,
	api.FilterDownloading: pb.StatusFilter_STATUS_FILTER_DOWNLOADING,
	api.FilterPaused:      pb.StatusFilter_STATUS_FILTER_PAUSED,
	api.FilterCompleted:   pb.StatusFilter_STATUS_FILTER_COMPLETED,
	api.FilterFailed:      pb.StatusFilter_STATUS_FILTER_FAILED,
	api.FilterFileLost:    pb.StatusFilter_STATUS_FILTER_FILE_LOST,
}

var filterFromPB = map[pb.StatusFilter]api.StatusFilter{
	pb.StatusFilter_STATUS_FILTER_ALL:         api.FilterAll,
	pb.StatusFilter_STATUS_FILTER_PENDING:     api.FilterPending,
	pb.StatusFilter_STATUS_FILTER_DOWNLOADING: api.FilterDownloading,
	pb.StatusFilter_STATUS_FILTER_PAUSED:      api.FilterPaused,
	pb.StatusFilter_STATUS_FILTER_COMPLETED:   api.FilterCompleted,
	pb.StatusFilter_STATUS_FILTER_FAILED:      api.FilterFailed,
	pb.StatusFilter_STATUS_FILTER_FILE_LOST:   api.FilterFileLost,
}

var sortToPB = map[api.TaskSort]pb.TaskSort{
	api.SortCreatedDesc: pb.TaskSort_TASK_SORT_CREATED_DESC,
	api.SortCreatedAsc:  pb.TaskSort_TASK_SORT_CREATED_ASC,
	api.SortNameAsc:     pb.TaskSort_TASK_SORT_NAME_ASC,
	api.SortNameDesc:    pb.TaskSort_TASK_SORT_NAME_DESC,
}

var sortFromPB = map[pb.TaskSort]api.TaskSort{
	pb.TaskSort_TASK_SORT_CREATED_DESC: api.SortCreatedDesc,
	pb.TaskSort_TASK_SORT_CREATED_ASC:  api.SortCreatedAsc,
	pb.TaskSort_TASK_SORT_NAME_ASC:     api.SortNameAsc,
	pb.TaskSort_TASK_SORT_NAME_DESC:    api.SortNameDesc,
}

var proxyToPB = map[api.ProxyMode]pb.ProxyMode{
	api.ProxyModeSystem:   pb.ProxyMode_PROXY_MODE_SYSTEM,
	api.ProxyModeDisabled: pb.ProxyMode_PROXY_MODE_DISABLED,
	api.ProxyModeManual:   pb.ProxyMode_PROXY_MODE_MANUAL,
}

var proxyFromPB = map[pb.ProxyMode]api.ProxyMode{
	pb.ProxyMode_PROXY_MODE_SYSTEM:   api.ProxyModeSystem,
	pb.ProxyMode_PROXY_MODE_DISABLED: api.ProxyModeDisabled,
	pb.ProxyMode_PROXY_MODE_MANUAL:   api.ProxyModeManual,
}

var kindToPB = map[api.ProtocolKind]pb.ProtocolKind{
	api.ProtoHTTP:   pb.ProtocolKind_PROTOCOL_KIND_HTTP,
	api.ProtoHTTPS:  pb.ProtocolKind_PROTOCOL_KIND_HTTPS,
	api.ProtoFTP:    pb.ProtocolKind_PROTOCOL_KIND_FTP,
	api.ProtoWebDAV: pb.ProtocolKind_PROTOCOL_KIND_WEBDAV,
	api.ProtoBT:     pb.ProtocolKind_PROTOCOL_KIND_BT,
}

var kindFromPB = map[pb.ProtocolKind]api.ProtocolKind{
	pb.ProtocolKind_PROTOCOL_KIND_HTTP:   api.ProtoHTTP,
	pb.ProtocolKind_PROTOCOL_KIND_HTTPS:  api.ProtoHTTPS,
	pb.ProtocolKind_PROTOCOL_KIND_FTP:    api.ProtoFTP,
	pb.ProtocolKind_PROTOCOL_KIND_WEBDAV: api.ProtoWebDAV,
	pb.ProtocolKind_PROTOCOL_KIND_BT:     api.ProtoBT,
}

// ---- DTO 映射 ----

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
		ChunkProgress:   t.ChunkProgress,
		ChunkRanges:     t.ChunkRanges,
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

func taskFromPB(t *pb.Task) *api.Task {
	if t == nil {
		return nil
	}
	return &api.Task{
		ID:              t.Id,
		URL:             t.Url,
		SavePath:        t.SavePath,
		Protocol:        t.Protocol,
		TotalSize:       t.TotalSize,
		Downloaded:      t.Downloaded,
		SupportRange:    t.SupportRange,
		ETag:            t.Etag,
		LastModified:    t.LastModified,
		DigestAlgorithm: t.DigestAlgorithm,
		Digest:          t.Digest,
		IsAllocated:     t.IsAllocated,
		ChunkCount:      int(t.ChunkCount),
		MinChunkSize:    t.MinChunkSize,
		ChunkProgress:   t.ChunkProgress,
		ChunkRanges:     t.ChunkRanges,
		Status:          statusFromPB[t.Status],
		AuthData:        t.AuthData,
		ErrorMessage:    t.ErrorMessage,
		ErrorKind:       t.ErrorKind,
		CreatedAt:       parseTime(t.CreatedAt),
		UpdatedAt:       parseTime(t.UpdatedAt),
		CompletedAt:     parseTime(t.CompletedAt),
		BTFile:          t.BtFile,
	}
}

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

func settingsFromPB(s *pb.Settings) api.Settings {
	if s == nil {
		return api.Settings{}
	}
	return api.Settings{
		DefaultSaveDir:     s.DefaultSaveDir,
		DefaultThreads:     int(s.DefaultThreads),
		MinChunkSize:       s.MinChunkSize,
		UserAgent:          s.UserAgent,
		Cookies:            s.Cookies,
		FTPPassive:         s.FtpPassive,
		Prealloc:           s.Prealloc,
		TaskSort:           sortFromPB[s.TaskSort],
		ProxyMode:          proxyFromPB[s.ProxyMode],
		ProxyURL:           s.ProxyUrl,
		ProxyBypass:        s.ProxyBypass,
		ProxyAutoConfigURL: s.ProxyAutoConfigUrl,
		DownloadLimitKiB:   int(s.DownloadLimitKib),
		LightMode:          s.LightMode,
		MaxConcurrent:      int(s.MaxConcurrent),
		AutoStart:          s.AutoStart,
		Language:           s.Language,
		BTPort:             int(s.BtPort),
		BTMaxPeers:         int(s.BtMaxPeers),
		BTDownloadLimitKiB: int(s.BtDownloadLimitKib),
		BTUploadLimitKiB:   int(s.BtUploadLimitKib),
		BTDHT:              s.BtDht,
		BTPEX:              s.BtPex,
		BTSeedRatio:        s.BtSeedRatio,
		BTMaxSeeding:       int(s.BtMaxSeeding),
		BTMaxConcurrent:    int(s.BtMaxConcurrent),
		BTTrackers:         s.BtTrackers,
	}
}

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

func authFromPB(a *pb.AuthOptions) api.AuthOptions {
	if a == nil {
		return api.AuthOptions{}
	}
	return api.AuthOptions{
		Username:           a.Username,
		Password:           a.Password,
		UserAgent:          a.UserAgent,
		Cookies:            a.Cookies,
		Referer:            a.Referer,
		FTPPassive:         a.FtpPassive,
		ProxyMode:          proxyFromPB[a.ProxyMode],
		ProxyURL:           a.ProxyUrl,
		ProxyBypass:        a.ProxyBypass,
		ProxyAutoConfigURL: a.ProxyAutoConfigUrl,
	}
}

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
		Trackers:     d.Trackers,
	}
}

func btDetailFromPB(d *pb.BTDetail) *api.BTDetail {
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
	buckets := make([]uint8, 0, len(d.PieceBuckets))
	for _, b := range d.PieceBuckets {
		buckets = append(buckets, uint8(b))
	}
	return &api.BTDetail{
		Name:         d.Name,
		InfoHash:     d.InfoHash,
		Total:        d.Total,
		Downloaded:   d.Downloaded,
		Uploaded:     d.Uploaded,
		SpeedBPS:     d.SpeedBps,
		UploadBPS:    d.UploadBps,
		Peers:        int(d.Peers),
		TotalPeers:   int(d.TotalPeers),
		Seeders:      int(d.Seeders),
		Leechers:     int(d.Leechers),
		PieceLength:  d.PieceLength,
		Pieces:       int(d.Pieces),
		PiecesDone:   int(d.PiecesDone),
		PieceBuckets: buckets,
		Seeding:      d.Seeding,
		Complete:     d.Complete,
		Files:        files,
		Trackers:     d.Trackers,
	}
}

func eventToPB(e api.Event) *pb.Event {
	return &pb.Event{
		Why:      e.Why,
		Task:     taskToPB(e.Task),
		SpeedBps: e.SpeedBPS,
	}
}

func eventFromPB(e *pb.Event) api.Event {
	if e == nil {
		return api.Event{}
	}
	return api.Event{
		Why:      e.Why,
		Task:     taskFromPB(e.Task),
		SpeedBPS: e.SpeedBps,
	}
}

// messageToPB 把契约层消息转成 proto 消息。
func messageToPB(m api.Message) *pb.Message {
	switch m.Kind {
	case api.MessageKindEvent:
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
	switch p := m.Payload.(type) {
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

// formatTime 把时间转成 RFC3339 字符串。零值返回空串。
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
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

// methodErrorf 构造带方法名的错误。
func methodErrorf(method string, err error) error {
	return fmt.Errorf("grpcx: %s: %w", method, err)
}
```

- [ ] **Step 6: 写转换往返测试**

创建 `internal/rpc/grpcx/convert_test.go`：

```go
package grpcx

import (
	"reflect"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
)

func TestTaskRoundTrip(t *testing.T) {
	now := time.Now()
	in := api.Task{
		ID: "t1", URL: "http://x/a", SavePath: "/tmp/a", Protocol: "HTTP",
		TotalSize: 100, Downloaded: 40, SupportRange: true,
		ETag: "e", LastModified: "m", DigestAlgorithm: "sha256", Digest: "d",
		IsAllocated: true, ChunkCount: 4, MinChunkSize: 1024,
		ChunkProgress: []int64{1, 2}, ChunkRanges: []int64{0, 9},
		Status: api.StatusDownloading, AuthData: "{}", ErrorMessage: "m",
		ErrorKind: "k", CreatedAt: now, UpdatedAt: now, BTFile: "f",
	}
	got := taskFromPB(taskToPB(&in))
	if !reflect.DeepEqual(*got, in) {
		t.Fatalf("Task 往返不一致:\n got = %+v\nwant = %+v", *got, in)
	}
}

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
	if !reflect.DeepEqual(*got, in) {
		t.Fatalf("BTDetail 往返不一致:\n got = %+v\nwant = %+v", *got, in)
	}
}

func TestEnumTablesCoverAllConstants(t *testing.T) {
	for s := range statusToPB {
		if _, ok := statusFromPB[statusToPB[s]]; !ok {
			t.Fatalf("状态 %q 没有反向映射", s)
		}
	}
	for s := range filterToPB {
		if _, ok := filterFromPB[filterToPB[s]]; !ok {
			t.Fatalf("筛选 %q 没有反向映射", s)
		}
	}
	for s := range sortToPB {
		if _, ok := sortFromPB[sortToPB[s]]; !ok {
			t.Fatalf("排序 %q 没有反向映射", s)
		}
	}
	for s := range proxyToPB {
		if _, ok := proxyFromPB[proxyToPB[s]]; !ok {
			t.Fatalf("代理模式 %q 没有反向映射", s)
		}
	}
	for s := range kindToPB {
		if _, ok := kindFromPB[kindToPB[s]]; !ok {
			t.Fatalf("协议 %q 没有反向映射", s)
		}
	}
}
```

- [ ] **Step 7: 跑测试确认通过**

Run: `go test ./internal/rpc/grpcx/`
Expected: PASS

- [ ] **Step 8: 建 `internal/rpc/grpcx/server.go` 与 `client.go`**

`server.go`：

```go
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
//
// Copyright (c) 2026 langbiantianya

// Package grpcx 是基于 gRPC 的传输实现：类型化 RPC + protobuf 载荷。
package grpcx

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
	"lgo_download_manager/internal/rpc/grpcx/pb"
)

// Handler 是 grpcx 需要的业务侧能力。结构上与 rpc.Handler 相同。
type Handler interface {
	Call(ctx context.Context, method string, in, out any) error
	Events() (<-chan api.Event, func())
	Controls() <-chan api.Message
}

// Serve 在地址上启动 gRPC 服务。
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
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	return srv.Serve(ln)
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
	if len(v) <= len(prefix) || v[:len(prefix)] != prefix {
		return false
	}
	return v[len(prefix):] == token
}

// server 把生成的 gRPC 接口转成对业务侧契约的调用。
type server struct {
	pb.UnimplementedDownloadServiceServer
	h Handler
}

func (s *server) List(ctx context.Context, req *pb.ListRequest) (*pb.ListResponse, error) {
	out := &api.ListResult{}
	if err := s.h.Call(ctx, api.MethodList, &api.ListParams{
		Filter: api.StatusFilter(req.GetFilter()),
		Sort:   api.TaskSort(req.GetSort()),
	}, out); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	tasks := make([]*pb.Task, 0, len(out.Tasks))
	for _, t := range out.Tasks {
		tasks = append(tasks, taskToPB(t))
	}
	return &pb.ListResponse{Tasks: tasks}, nil
}

func (s *server) AddTask(ctx context.Context, req *pb.AddTaskRequest) (*pb.AddTaskResponse, error) {
	out := &api.Task{}
	if err := s.h.Call(ctx, api.MethodAdd, &api.AddTaskInput{
		URL:          req.GetUrl(),
		SavePath:     req.GetSavePath(),
		Protocol:     kindFromPB[req.GetProtocol()],
		Auth:         authFromPB(req.GetAuth()),
		ChunkCount:   int(req.GetChunkCount()),
		MinChunkSize: req.GetMinChunkSize(),
	}, out); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.AddTaskResponse{Task: taskToPB(out)}, nil
}

func (s *server) StartTask(ctx context.Context, req *pb.TaskRef) (*pb.Empty, error) {
	if err := s.h.Call(ctx, api.MethodStart, &api.TaskRef{ID: req.GetId()}, &api.Empty{}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.Empty{}, nil
}

func (s *server) PauseTask(ctx context.Context, req *pb.TaskRef) (*pb.Empty, error) {
	if err := s.h.Call(ctx, api.MethodPause, &api.TaskRef{ID: req.GetId()}, &api.Empty{}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.Empty{}, nil
}

func (s *server) DeleteTask(ctx context.Context, req *pb.TaskRef) (*pb.Empty, error) {
	if err := s.h.Call(ctx, api.MethodDelete, &api.TaskRef{ID: req.GetId()}, &api.Empty{}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.Empty{}, nil
}

func (s *server) IsPreparing(ctx context.Context, req *pb.TaskRef) (*pb.BoolResponse, error) {
	out := &api.BoolResult{}
	if err := s.h.Call(ctx, api.MethodIsPreparing, &api.TaskRef{ID: req.GetId()}, out); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.BoolResponse{Value: out.Value}, nil
}

func (s *server) Probe(ctx context.Context, req *pb.ProbeRequest) (*pb.ProbeResponse, error) {
	out := &api.ProbeResult{}
	if err := s.h.Call(ctx, api.MethodProbe, &api.ProbeParams{URL: req.GetUrl()}, out); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.ProbeResponse{TotalSize: out.TotalSize}, nil
}

func (s *server) GetSettings(ctx context.Context, _ *pb.Empty) (*pb.GetSettingsResponse, error) {
	out := &api.Settings{}
	if err := s.h.Call(ctx, api.MethodGetSettings, &api.Empty{}, out); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.GetSettingsResponse{Settings: settingsToPB(*out)}, nil
}

func (s *server) SaveSettings(ctx context.Context, req *pb.SaveSettingsRequest) (*pb.Empty, error) {
	in := settingsFromPB(req.GetSettings())
	if err := s.h.Call(ctx, api.MethodSaveSettings, &in, &api.Empty{}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.Empty{}, nil
}

func (s *server) BTTaskDetail(ctx context.Context, req *pb.BTInfoRequest) (*pb.BTDetailResponse, error) {
	out := &api.BTDetail{}
	if err := s.h.Call(ctx, api.MethodBTInfo, &api.BTInfoParams{TaskID: req.GetTaskId()}, out); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.BTDetailResponse{Detail: btDetailToPB(out)}, nil
}

// StreamMessages 把事件流与控制消息合并后逐条推送。
func (s *server) StreamMessages(_ *pb.Empty, stream pb.DownloadService_StreamMessagesServer) error {
	events, unsub := s.h.Events()
	defer unsub()
	controls := s.h.Controls()
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if err := stream.Send(messageToPB(api.Message{
				Kind:  api.MessageKindEvent,
				Event: &ev,
			})); err != nil {
				return err
			}
		case ctl, ok := <-controls:
			if !ok {
				return nil
			}
			if err := stream.Send(messageToPB(ctl)); err != nil {
				return err
			}
		}
	}
}
```

`client.go`：

```go
package grpcx

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc/addr"
	"lgo_download_manager/internal/rpc/grpcx/pb"
)

// Client 是 gRPC 传输的客户端。
type Client struct {
	conn *grpc.ClientConn
}

// Dial 按地址建立 gRPC 连接。token 随每次调用发送。
func Dial(ctx context.Context, a addr.Addr, token string) (*Client, error) {
	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return addr.Dial(a)
	}
	cc, err := grpc.NewClient("passthrough:///lgdm",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer),
		grpc.WithUnaryInterceptor(clientUnaryToken(token)),
		grpc.WithStreamInterceptor(clientStreamToken(token)),
	)
	if err != nil {
		return nil, fmt.Errorf("grpcx: 建立连接: %w", err)
	}
	return &Client{conn: cc}, nil
}

// clientUnaryToken 给一元调用附加 token。
func clientUnaryToken(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
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
	cli := pb.NewDownloadServiceClient(c.conn)
	switch method {
	case api.MethodList:
		p := in.(*api.ListParams)
		res, err := cli.List(ctx, &pb.ListRequest{Filter: string(p.Filter), Sort: string(p.Sort)})
		if err != nil {
			return methodErrorf(method, err)
		}
		r := out.(*api.ListResult)
		r.Tasks = nil
		for _, t := range res.GetTasks() {
			r.Tasks = append(r.Tasks, taskFromPB(t))
		}
		return nil

	case api.MethodAdd:
		p := in.(*api.AddTaskInput)
		res, err := cli.AddTask(ctx, &pb.AddTaskRequest{
			Url:          p.URL,
			SavePath:     p.SavePath,
			Protocol:     kindToPB[p.Protocol],
			Auth:         authToPB(p.Auth),
			ChunkCount:   int32(p.ChunkCount),
			MinChunkSize: p.MinChunkSize,
		})
		if err != nil {
			return methodErrorf(method, err)
		}
		*(out.(*api.Task)) = *taskFromPB(res.GetTask())
		return nil

	case api.MethodStart:
		p := in.(*api.TaskRef)
		if _, err := cli.StartTask(ctx, &pb.TaskRef{Id: p.ID}); err != nil {
			return methodErrorf(method, err)
		}
		return nil

	case api.MethodPause:
		p := in.(*api.TaskRef)
		if _, err := cli.PauseTask(ctx, &pb.TaskRef{Id: p.ID}); err != nil {
			return methodErrorf(method, err)
		}
		return nil

	case api.MethodDelete:
		p := in.(*api.TaskRef)
		if _, err := cli.DeleteTask(ctx, &pb.TaskRef{Id: p.ID}); err != nil {
			return methodErrorf(method, err)
		}
		return nil

	case api.MethodIsPreparing:
		p := in.(*api.TaskRef)
		res, err := cli.IsPreparing(ctx, &pb.TaskRef{Id: p.ID})
		if err != nil {
			return methodErrorf(method, err)
		}
		out.(*api.BoolResult).Value = res.GetValue()
		return nil

	case api.MethodProbe:
		p := in.(*api.ProbeParams)
		res, err := cli.Probe(ctx, &pb.ProbeRequest{Url: p.URL})
		if err != nil {
			return methodErrorf(method, err)
		}
		out.(*api.ProbeResult).TotalSize = res.GetTotalSize()
		return nil

	case api.MethodGetSettings:
		res, err := cli.GetSettings(ctx, &pb.Empty{})
		if err != nil {
			return methodErrorf(method, err)
		}
		*(out.(*api.Settings)) = settingsFromPB(res.GetSettings())
		return nil

	case api.MethodSaveSettings:
		p := in.(*api.Settings)
		if _, err := cli.SaveSettings(ctx, &pb.SaveSettingsRequest{Settings: settingsToPB(*p)}); err != nil {
			return methodErrorf(method, err)
		}
		return nil

	case api.MethodBTInfo:
		p := in.(*api.BTInfoParams)
		res, err := cli.BTTaskDetail(ctx, &pb.BTInfoRequest{TaskId: p.TaskID})
		if err != nil {
			return methodErrorf(method, err)
		}
		*(out.(*api.BTDetail)) = *btDetailFromPB(res.GetDetail())
		return nil

	default:
		return fmt.Errorf("grpcx: 未知方法 %q", method)
	}
}

// Stream 打开消息流。
func (c *Client) Stream(ctx context.Context) (<-chan api.Message, error) {
	cli := pb.NewDownloadServiceClient(c.conn)
	stream, err := cli.StreamMessages(ctx, &pb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("grpcx: 打开消息流: %w", err)
	}
	out := make(chan api.Message, 64)
	go func() {
		defer close(out)
		for {
			msg, err := stream.Recv()
			if err != nil {
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
```

- [ ] **Step 9: 建 `internal/rpc/registry.go`（传输注册表）**

```go
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
	return newClient(ctx, tr)
}

// 编译期断言：两种传输实现都满足 Transport。
var (
	_ Transport = (*grpcx.Client)(nil)
	_ Transport = (*httpx.Client)(nil)
)
```

- [ ] **Step 10: 跑测试**

Run: `go test ./internal/rpc/... ./internal/appservice/`
Expected: PASS

- [ ] **Step 11: 提交**

```bash
git add proto buf.yaml buf.gen.yaml scripts/gen_proto.sh internal/rpc/grpcx internal/rpc/registry.go go.mod go.sum
git commit -m "feat(grpcx): 实现基于 gRPC 的传输并加入 protobuf 契约"
```

---

### Task 8: 契约测试套件（两种传输各跑一遍）

**Files:**
- Create: `internal/rpc/contract_test.go`

**Interfaces:**
- Consumes: `internal/rpc`（Task 4）、`internal/rpc/httpx`（Task 5）、`internal/rpc/grpcx`（Task 7）
- Produces: 无（只有测试）

- [ ] **Step 1: 写契约测试**

创建 `internal/rpc/contract_test.go`：

```go
package rpc_test

import (
	"context"
	"testing"
	"time"

	"lgo_download_manager/internal/api"
	"lgo_download_manager/internal/rpc"
	"lgo_download_manager/internal/rpc/addr"
	"lgo_download_manager/internal/rpc/grpcx"
	"lgo_download_manager/internal/rpc/httpx"
)

// stubHandler 是契约测试用的最小业务侧实现。
type stubHandler struct {
	events   chan api.Event
	controls chan api.Message
}

func (s *stubHandler) Call(ctx context.Context, method string, in, out any) error {
	switch method {
	case api.MethodGetSettings:
		out.(*api.Settings).Language = "zh-Hans"
	case api.MethodList:
		out.(*api.ListResult).Tasks = []*api.Task{{ID: "t1", Status: api.StatusDownloading}}
	case api.MethodAdd:
		t := out.(*api.Task)
		t.ID = "new"
		t.URL = in.(*api.AddTaskInput).URL
	case api.MethodIsPreparing:
		out.(*api.BoolResult).Value = true
	case api.MethodProbe:
		out.(*api.ProbeResult).TotalSize = 4242
	case api.MethodBTInfo:
		d := out.(*api.BTDetail)
		d.InfoHash = "hash"
	}
	return nil
}

func (s *stubHandler) Events() (<-chan api.Event, func()) { return s.events, func() {} }
func (s *stubHandler) Controls() <-chan api.Message       { return s.controls }

// contract 对两种传输跑同一组用例。
func contract(t *testing.T, transport string) {
	t.Helper()
	h := &stubHandler{
		events:   make(chan api.Event, 8),
		controls: make(chan api.Message, 8),
	}
	a := addr.Addr{Network: "unix", Address: t.TempDir() + "/lgdm.sock"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = rpc.Serve(ctx, rpc.ServerOptions{
			Transport: transport,
			Addr:      "unix:" + a.Address,
			Token:     "secret",
		}, h)
	}()

	// 等监听器就绪。
	var cli rpc.Client
	var err error
	for i := 0; i < 50; i++ {
		cli, err = rpc.Dial(ctx, transport, "unix:"+a.Address, "secret")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer cli.Close()

	t.Run("握手拿到设置", func(t *testing.T) {
		if cli.Settings().Language != "zh-Hans" {
			t.Fatalf("设置不对: %+v", cli.Settings())
		}
	})

	t.Run("列表", func(t *testing.T) {
		tasks, err := cli.List(api.FilterAll, api.SortCreatedDesc)
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) != 1 || tasks[0].ID != "t1" {
			t.Fatalf("列表不对: %+v", tasks)
		}
	})

	t.Run("添加", func(t *testing.T) {
		task, err := cli.AddTask(api.AddTaskInput{URL: "http://x/a"})
		if err != nil {
			t.Fatal(err)
		}
		if task.ID != "new" {
			t.Fatalf("添加结果不对: %+v", task)
		}
	})

	t.Run("启动暂停删除", func(t *testing.T) {
		if err := cli.Start("t1"); err != nil {
			t.Fatal(err)
		}
		if err := cli.Pause("t1"); err != nil {
			t.Fatal(err)
		}
		if err := cli.Delete("t1"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("准备阶段", func(t *testing.T) {
		if !cli.IsPreparing("t1") {
			t.Fatal("应当处于准备阶段")
		}
	})

	t.Run("探测", func(t *testing.T) {
		size, err := cli.Probe("http://x/a")
		if err != nil {
			t.Fatal(err)
		}
		if size != 4242 {
			t.Fatalf("探测结果不对: %d", size)
		}
	})

	t.Run("事件流", func(t *testing.T) {
		ch, unsub := cli.Subscribe()
		defer unsub()
		h.events <- api.Event{Why: "progress", SpeedBPS: 10}
		select {
		case ev := <-ch:
			if ev.Why != "progress" {
				t.Fatalf("事件不对: %+v", ev)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("没有收到事件")
		}
	})

	t.Run("控制消息", func(t *testing.T) {
		shown := make(chan struct{}, 1)
		cli.SetOnShow(func() { shown <- struct{}{} })
		h.controls <- api.Message{Kind: api.MessageKindShow}
		select {
		case <-shown:
		case <-time.After(5 * time.Second):
			t.Fatal("show 回调没有触发")
		}
	})

	t.Run("错误 token 被拒", func(t *testing.T) {
		_, err := rpc.Dial(ctx, transport, "unix:"+a.Address, "wrong")
		if err == nil {
			t.Fatal("错误 token 应当连接失败")
		}
	})
}

func TestContractGRPC(t *testing.T) { contract(t, rpc.TransportGRPC) }
func TestContractHTTP(t *testing.T) { contract(t, rpc.TransportHTTP) }

// 编译期断言：两种传输的客户端都满足 Transport。
var (
	_ rpc.Transport = (*grpcx.Client)(nil)
	_ rpc.Transport = (*httpx.Client)(nil)
)
```

- [ ] **Step 2: 跑测试确认通过**

Run: `go test ./internal/rpc/ -run TestContract -v`
Expected: PASS —— 两种传输各跑一遍全部子用例

- [ ] **Step 3: 提交**

```bash
git add internal/rpc/contract_test.go
git commit -m "test(rpc): 为两种传输补同一套契约测试"
```

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
