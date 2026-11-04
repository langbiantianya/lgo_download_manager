# UI RPC

业务进程与 UI 子进程之间的跨进程通信统一走 `internal/rpc`，不再维护自定义帧协议。
整体进程模型见 [架构说明](../../docs/architecture.md)。

## 分层与传输

| 层 | 位置 | 职责 |
| --- | --- | --- |
| 契约层 | `internal/api` | DTO 与词汇表，定义每个方法的请求/响应类型 |
| 接口层 | `internal/rpc` | `Transport` / `Handler` / `Client` 与注册表 |
| 传输实现 | `internal/rpc/httpx`、`internal/rpc/grpcx` | net/http REST + JSON，或 gRPC server-streaming |
| 业务侧 | `internal/appservice` | 契约 Handler，把调用转给 scheduler/store |

`internal/ui` 只依赖契约层和接口层，不认识 `store`、`scheduler`、`protocol` 的
具体类型。`internal/uimgr` 负责进程和通道装配。边界测试位于
`internal/ui/boundary_test.go` 与 `internal/rpc/boundary_test.go`。

默认传输为 gRPC。HTTP 传输适合 curl、Swagger UI 和协议调试。gRPC 契约权威源是
`proto/lgdm/v1/lgdm.proto`，生成物位于 `internal/rpc/grpcx/pb`，使用
`scripts/gen_proto.sh` 更新。

## 三方消息

同一通道承载三类内容：

- **调用**：UI → 业务。共 11 个方法：`list`、`add`、`start`、`pause`、`delete`、
  `is_preparing`、`probe`、`get_settings`、`save_settings`、`bt_info`、
  `stream_messages`。请求/响应由 `api.Spec(method)` 固定，两种传输调用前都做类型
  校验。
- **事件流**：业务 → UI。scheduler 事件（added、started、progress、paused、
  completed、failed、updated）在 HTTP 下是 NDJSON 长连接，在 gRPC 下是
  server-streaming。
- **控制消息**：业务 → UI。包括托盘「显示窗口」、语言变更和业务退出。

握手使用 token，HTTP 为 `Authorization: Bearer <token>`，gRPC 放在 metadata 中。
每次调用都校验 token。

## 子进程参数

业务进程拉起 UI 子进程时通过环境变量传入通道参数：

| 环境变量 | 含义 |
| --- | --- |
| `LDM_UI_CHILD` | `1` 表示当前进程是 UI 子进程 |
| `LDM_UI_LISTEN` | 业务监听地址，`unix:<path>` 或 `tcp:<host:port>` |
| `LDM_UI_TOKEN` | 握手 token |
| `LDM_UI_TRANSPORT` | `grpc` 或 `http` |

监听地址也可通过 `--rpc-listen` 或 `LDM_RPC_LISTEN` 配置，命令行优先。留空时使用
默认 Unix socket，权限 `0600`，路径位于
`$XDG_CACHE_HOME/lgo_download_manager/ui-<随机后缀>.sock` 一带。

TCP 默认只绑回环地址。绑定到非回环地址时程序会告警，因为此时 token 是唯一防线。

```sh
# TCP，仅本机
lgdm --rpc-listen tcp:127.0.0.1:17830

# 切换到 HTTP 传输
LDM_UI_TRANSPORT=http lgdm
```

## HTTP 文档端点

HTTP 传输会同时提供离线内嵌的 OpenAPI 文档：

```sh
LDM_UI_TRANSPORT=http lgdm --rpc-listen tcp:127.0.0.1:17830
# 浏览器打开 http://127.0.0.1:17830/docs/
```

Swagger UI 的静态资源已打进二进制，不引用外链。规范文件是
`internal/rpc/httpx/openapi.yaml`，与 `internal/api` DTO 的一致性由测试保障。
文档端点不需要 token；业务端点仍需要。

```sh
curl -i -X POST http://127.0.0.1:17830/lgdm.v1.DownloadService/list \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  -d '{}'
```

缺少正确 token 时返回 401 是预期行为。

## 故障处理

业务 handler 的 panic 由传输层捕获，避免连带结束仍在执行下载的业务进程。
gRPC 侧将 panic 转为 `Internal` 错误，HTTP 侧转为 500 和
`{"ok":false,"error":…}`。UI 回调中的 panic 也会捕获并标记会话断开。

业务进程每个时刻只对应一个 UI 会话。UI 断开时 `cmd.Wait()` 触发会话清理，
托盘可再次拉起 UI。

## 已知限制

- NDJSON / gRPC server-streaming 断开后不会自动重连，需要重启 UI 子进程才能重新
  获取事件流。
- HTTP 绑定到非回环地址时，token 是唯一防线。
- 业务错误统一映射为 500，不细分业务错误码。
- proto3 枚举无法表达未知值，gRPC 侧可能降级为 `UNSPECIFIED`；空切片和未设置
  也无法区分。差异表位于 `contract_test.go`。
- 两种传输的响应外壳不同：gRPC 的 `add`、`get_settings`、`bt_info` 会使用具名
  外层 message，HTTP 返回裸 DTO。跨传输客户端不能假设响应结构相同。

## 测试

```sh
go test -v ./internal/rpc/...
```

边界、契约、恢复、gRPC/HTTP 一致性以及 OpenAPI 同步都有独立测试。
