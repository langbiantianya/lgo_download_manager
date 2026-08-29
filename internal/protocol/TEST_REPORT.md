# Protocol 包测试报告

**包**: `internal/protocol`
**测试文件**: `internal/protocol/protocol_test.go`
**日期**: 2026-08-30
**结果**: ✅ 通过（33 个测试全部通过）

---

## 测试覆盖

### 辅助函数（4 个测试）

| 测试 | 描述 | 方法 |
|------|------|------|
| `TestHasPort` | `hasPort` 对 `host:port` 返回 true，对纯 host 返回 false | 表格驱动：7 个用例，含 `localhost:21`、`192.168.1.1:21`、`ftp.example.com`、`localhost`、`[::1]:21`、`""` |
| `TestParseInt64` | `parseInt64` 解析十进制 int64 | 表格驱动：8 个用例，含合法数字、零、空（`-1`）、非数字、空白修剪、最大 int64 |
| `TestParseTotalFromContentRange` | `parseTotalFromContentRange` 从 `bytes N-N/TOTAL` 提取总大小 | 表格驱动：9 个用例，含合法范围、`-1`、空、无斜杠、尾部斜杠 |
| `TestSupportsRanges` | `supportsRanges` 大小写不敏感检查 `Accept-Ranges: bytes|1` | 表格驱动：6 个用例，含 `bytes`、`Bytes`、`1`、`none`、空 |

### 工厂函数（3 个测试）

| 测试 | 描述 | 方法 |
|------|------|------|
| `TestDetectKind` | `DetectKind` 将 URL scheme 解析为 `ProtocolKind` | 表格驱动：11 个用例，含全部 5 种 scheme、大小写不敏感、hint 覆盖、不支持 scheme、空 URL |
| `TestResolveURL` | `ResolveURL` 将 `webdav://`/`dav://` 重写为 `https://`/`http://` | 表格驱动：5 个用例 |
| `TestNew` | `New` 工厂创建驱动，空 URL 返回错误 | httptest 服务器，3 个用例 |

### HTTP Driver（13 个测试）

| 测试 | 描述 | 方法 |
|------|------|------|
| `TestHTTPProbeHEAD` | HEAD 响应含 `Content-Length` + `Accept-Ranges: bytes` → 正确 caps | httptest，验证 `TotalSize` 和 `SupportRange` |
| `TestHTTPProbeHEADNoAcceptRanges` | HEAD 无 `Accept-Ranges` → `SupportRange=false` | httptest |
| `TestHTTPProbeHEADContentLengthMissing` | HEAD 无 `Content-Length` → 回退到 `probeWithRange`（206） | httptest，原子 flag 验证 Range GET 被调用 |
| `TestHTTPProbeHEADErrorStatus` | HEAD 返回 404 → 错误 | httptest |
| `TestHTTPProbeContextCanceled` | Context 超时 → context 错误 | httptest，10ms 超时 |
| `TestHTTPDownloadChunk` | 全范围 GET 206 → 字节正确的 payload 写入文件 | httptest，64 KiB payload，验证 `onData` 回调 |
| `TestHTTPDownloadChunkInvalidRange` | `start > end` → 请求前返回错误 | httptest |
| `TestHTTPDownloadChunkNon206` | 非 206 状态（404）→ 错误 | httptest |
| `TestHTTPDownloadFallback` | 流式下载全文件 → 字节正确的 payload | httptest，32 KiB，验证 `onData` 回调 |
| `TestHTTPDownloadFallbackOffset` | `offset > 0` → 字节从正确位置写入 | httptest，16 KiB 文件，offset 4096 |
| `TestHTTPClose` | `Close` 是幂等的 | httptest |
| `TestHTTPDriverConcurrentDownloads` | 4 个 goroutine 并发下载 4 个 chunk → 正确拼接 payload | httptest，256 KiB |
| `TestAllDriversImplementProtocolDriver` | 编译时断言 `*httpDriver` 满足 `ProtocolDriver` | — |

### WebDAV Driver（5 个测试）

| 测试 | 描述 | 方法 |
|------|------|------|
| `TestWebDAVProbePROPFIND` | PROPFIND 200 含 `getcontentlength` → 正确大小 | httptest，XML 响应 |
| `TestWebDAVProbePROPFINDFallbackHEAD` | PROPFIND 403 → 回退到 HEAD | httptest |
| `TestWebDAVDownloadChunk` | Range GET 206 → 字节正确的 payload | httptest，32 KiB |
| `TestWebDAVDownloadFallback` | 全量流式下载 → 字节正确 | httptest，8 KiB |
| `TestWebDAVClose` | `Close` 是幂等的 | httptest |
| `TestWebDAVDriverConcurrentDownloads` | 4 个 goroutine 并发下载 4 个 chunk → 正确拼接 payload | httptest，128 KiB |

### FTP Driver（8 个测试）

| 测试 | 描述 | 方法 |
|------|------|------|
| `TestFTPDial` | `newFTPDriver` 拒绝空 URL、错误 scheme、空 host；接受合法 `ftp://` URL | 表格驱动，4 个错误用例 + 1 个成功用例 |
| `TestFTPProbeNoServer` | 连接不存在的服务器 → context 错误 | 500ms 超时 |
| `TestFTPDownloadChunkNoServer` | 在已关闭的服务器上下载 → 连接错误 | 500ms 超时 |
| `TestFTPDownloadFallbackNoServer` | 在已关闭的服务器上回退 → 连接错误 | 500ms 超时 |
| `TestFTPLiveProbe` | 真实 FTP 服务器：SIZE + REST 0 探测 → 正确 caps | 纯 Go 内嵌 FTP 服务器（`scripts/ftp_test_server/ftpd.go`） |
| `TestFTPLiveDownloadChunk` | 真实 FTP 服务器：PASV + RETR 带 offset → 字节正确的 payload | 纯 Go 内嵌 FTP 服务器 |
| `TestFTPLiveDownloadFallback` | 真实 FTP 服务器：全量 RETR → 字节正确的 payload | 纯 Go 内嵌 FTP 服务器 |
| `TestFTPClose` | `Close` 对 FTP driver 始终为 nil | — |

---

## FTP 测试服务器实现

所有 `TestFTPLive*` 测试使用 `scripts/ftp_test_server/ftpd.go`，一个纯 Go FTP 服务器：

- **支持命令**: USER/PASS、TYPE I、PWD、CWD、LIST、SIZE、REST、RETR、PASV
- **进程模型**: 预编译二进制文件（避免 `go run` 进程组残留），每连接一个 goroutine
- **PASV 同步**: 使用 `sync.Cond` 协调数据连接的接受，确保 RETR/LIST 执行前连接已就绪
- **集成方式**: `startFTPServer` / `startFTPServerWithPayload` 辅助函数在每个测试前编译并启动二进制，defer 清理

Python pyftpdlib（`scripts/ftp_server.py`）保留用于手动调试。

---

## 环境

- **Go**: `go1.26.7`
- **httptest**: 所有 HTTP 和 WebDAV 测试使用（无外部依赖）
- **Context 超时**: "无服务器"测试 500ms，取消测试 10ms

## 测试执行

```bash
go test -v -count=1 ./internal/protocol/
```
