# 下载引擎

`internal/engine` 负责 HTTP/HTTPS/WebDAV/FTP 任务的探测、分片规划、Range
下载、进度记录和错误重试。文件按职责拆为 `engine`、`plan`、`chunk`、
`progress`、`retry`。

## 下载策略

引擎先探测文件大小与 Range 能力，再决定并发分片还是单流回退：

- HTTP/WebDAV 在 HEAD/`PROPFIND` 后发送 1 字节 Range 请求验证真实能力，避免只
  根据 `Accept-Ranges` 误判。分片响应必须是精确的 `206`，且 `Content-Range`
  与请求区间一致。
- HEAD/PROPFIND 使用独立短预算；服务器不支持或迟迟不响应探测方法时，快速降级到
  1 字节 Range 请求。
- 服务端忽略 Range 并返回 `200` 时，从字节 0 开始单流下载，不会把完整响应写到
  中途偏移。短响应、错误区间和长度不符会按失败或可重试错误处理。
- 请求使用 `Accept-Encoding: identity`，保证 Range 偏移对应原始字节。
- 大文件按 worker 数量的 4 倍生成逻辑切片，慢连接释放的后续切片可由其它 worker
  接手。单个分片临时失败不会立即取消其它健康分片。

## 一致性校验

- HTTP/WebDAV 持久化 ETag 与 Last-Modified，并在分片请求中发送 `If-Range`。
  资源版本变化时丢弃旧偏移，从头下载。
- FTP 读取 `MDTM` 并保存为 UTC RFC3339 版本校验器，远程文件时间变化时同样放弃
  旧偏移。
- 来源提供 `Content-Digest`、`Repr-Digest`、`Digest` 或 `Content-MD5` 时，发布
  文件前进行流式整文件校验。不匹配时保留 `.part` 供诊断，清空进度并从 0 重试。
- 同一任务的远端总大小变化时，旧分片偏移失效并自动从头下载。

## 断点续传与发布

- 下载数据写入 `<目标文件>.part`，成功时先 `Sync` 文件，再原子重命名并同步父目录，
  最后把数据库状态更新为 `Completed`。
- 暂停、失败和进程异常退出时保留 `.part`；已完成分片不会重新下载。
- `.part` 被删除或截断到已记录写入位置之前时，恢复会清零旧分片偏移并重新下载。
- 暂停任务的文件缺失会显示为 `FileLost`。没有 Range 的来源不会伪造续传位置。
- 删除任务会同时清理正式文件和 `.part`。

## 超时、重试与资源控制

- 响应体连续 60 秒没有新字节会取消该分片并按可重试网络错误处理；磁盘写入和限速
  造成的处理停顿不计入网络停滞时间。
- 429、408、425 和临时 `5xx` 会退避重试；有 `Retry-After` 时优先遵守。
- HTTP/WebDAV 在相同代理配置下跨任务共享连接池，以最多 4 路启动，并限制每主机
  连接数。网络错误时先降低并发，退避后逐步恢复。
- 下载前检查目标文件系统可用空间。预分配时检查完整文件，续传时只检查剩余字节。
- HTTP/HTTPS/WebDAV/FTP 使用共享全局限速器，设置修改对运行中的分片立即生效。

## 测试

```sh
go test -v ./internal/engine/
```

测试使用 `httptest.Server` 验证分片计算和字节级下载正确性。最完整的单流回退用例
位于 `internal/ui/chunk_details_e2e_test.go`：它在不支持 `Accept-Ranges` 的服务上
运行真实调度器和引擎，并断言每条分片进度条最终达到 `1.0`。

历史覆盖报告见 [TEST_REPORT.md](TEST_REPORT.md)。
