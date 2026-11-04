# 测试指南

## 快速开始

```sh
# 运行所有测试
go test ./...

# 运行指定包的测试
go test -v ./internal/protocol/
go test -v ./internal/engine/
go test -v ./internal/scheduler/
```

## 各包测试说明

### internal/protocol

协议驱动测试：HTTP、HTTPS、FTP、WebDAV。

```sh
go test -v ./internal/protocol/
```

**HTTP/WebDAV 测试**：使用 `net/http/httptest`，无需外部依赖。

**FTP 测试**：
- `TestFTPDial`、`TestFTPProbeNoServer`、`TestFTPDownloadChunkNoServer`、`TestFTPDownloadFallbackNoServer`、`TestFTPClose` 等：使用超时连接测试错误处理，不需真实服务器。
- `TestFTPLiveProbe`、`TestFTPLiveDownloadChunk`、`TestFTPLiveDownloadFallback`：使用纯 Go 编写的内嵌 FTP 测试服务器（`scripts/ftp_test_server/ftpd.go`），自动启动和清理，**无需手动启动服务器**，作为标准 `go test` 流程的一部分运行。

### internal/engine

分片规划与 Range 下载逻辑测试。

```sh
go test -v ./internal/engine/
```

使用 `httptest.Server` 提供本地 HTTP 服务器，验证分片计算和字节级下载正确性。

### internal/scheduler

调度器端到端测试：任务生命周期、状态持久化、断点续传。

```sh
go test -v ./internal/scheduler/
```

### internal/store

SQLite 存储层测试。

```sh
go test -v ./internal/store/
```

### internal/ui

GUI 端到端测试，验证分片进度条在无 Range 服务器下的行为。

```sh
go test -v ./internal/ui/
```

## 测试脚本说明

| 脚本 | 说明 |
|------|------|
| `scripts/ftp_test_server/ftpd.go` | 纯 Go 编写的 FTP 测试服务器，protocol 测试自动使用 |
| `scripts/ftp_server.py` | Python pyftpdlib FTP 服务器，适合手动调试 |
| `scripts/setup_test_env.sh` | 测试环境初始化脚本（安装 Python 依赖） |

## FTP 测试服务器

### 自动化测试（推荐）

所有 FTP 协议测试（包括 `TestFTPLive*`）通过纯 Go FTP 服务器自动运行，无需手动干预：

```sh
go test -v ./internal/protocol/
```

`startFTPServer` / `startFTPServerWithPayload` 辅助函数会在测试开始时编译 `ftpd.go`，启动服务器，测试结束后自动清理。

### 手动调试

如需手动启动 FTP 服务器进行调试：

```sh
# Python 版（需要 pyftpdlib）
./scripts/setup_test_env.sh
/tmp/ftp_pytest_venv/bin/python3 scripts/ftp_server.py --root /tmp/ftp_test_root

# Go 版
go run scripts/ftp_test_server/ftpd.go --root /tmp/ftp_test_root
```

## 常见问题

### 测试超时

调度器端到端测试有 15s 截止时间，确保 HTTP 服务器和存储路径可用。

### 虚拟环境不存在

`scripts/setup_test_env.sh` 用于初始化 Python 测试环境（供手动调试用）。FTP 自动化测试不依赖 Python 虚拟环境。

## 测试报告

详细的测试覆盖报告位于各包目录下：

- `internal/protocol/TEST_REPORT.md`
- `internal/engine/TEST_REPORT.md`

## 值得优先关注的用例

`internal/ui/chunk_details_e2e_test.go` 在不支持 `Accept-Ranges` 的 httptest 服务
上运行真实的调度器和引擎，验证单流回退，并断言每条分片进度条最终
`Value = 1.0`。

`internal/handoff/handoff_test.go` 覆盖浏览器接力端口：扩展来源放行，网页、
`null` 来源和缺少 `X-LGOM-Client` 的请求拒绝；候选端口被占时顺延；全部占用时报错；
只绑定 `127.0.0.1`；请求体校验、错误回传和 `handoff.json` 内容。

`internal/scheduler/scheduler_test.go` 中的 `TestStartIsAsync` 使用不可达端口验证：

- `Start()` 在 500ms 内返回；
- 任务最终进入 `Failed`，且 `ErrorMessage` 非空。

`TestMaxConcurrent_*` 覆盖：

- `_QueuesNewTasksBeyondCap`：达到上限后静默排队，不占 slot；
- `_PromotesFIFOOnCompletion`：槽位释放后按 `created_at` 升序拉起；
- `_RaiseCapPromotesPending`：调大上限后立即 promote。

这些用例使用 `blockingServer` 让 HTTP probe 停留在 prepare 阶段，以便精确断言
上限和拉起时机。

BitTorrent 相关：

- `internal/bittorrent/bittorrent_test.go` 的 `TestDownloadFromLoopbackPeer` 是本机
  做种端到真实 lgdm Manager 下载的端到端用例，验证进度、文件列表、内容、
  `.part` 重命名和实际上传。其它用例覆盖种子解析、重复 infohash 拒绝、缓存元数据
  恢复。
- `internal/scheduler/bittorrent_test.go` 覆盖设置到客户端配置的映射、tracker 拆分、
  任务目录推导、BT/普通任务删除语义、按种子文件换算分片，以及 `flushAll` 不清空
  BT 分片进度的回归。

需要绑定回环端口的测试（BT 端到端、`internal/handoff`、部分 engine 用例）在无法
监听 socket 的环境中会 `t.Skip`，因此受限沙箱中的全量测试仍可运行其它单元用例。

## 其他模块测试

```sh
go test -v ./internal/rpc/...
go test -v ./internal/store/
go test -v ./internal/bittorrent/
```

浏览器扩展测试：

```sh
cd lgom-extension
npm test
npm run check
```
