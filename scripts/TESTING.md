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
