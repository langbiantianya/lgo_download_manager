# 调度器

`internal/scheduler` 管理普通 HTTP/FTP/WebDAV 与 BitTorrent 任务的生命周期、状态
事件、并发槽位和进度落盘。BT 的协议细节见 [BitTorrent](../bittorrent/README.md)。

## 异步启动

`scheduler.Start(id)` 立即返回，不会因目标主机不可达而阻塞 UI。HTTP HEAD、FTP
SIZE 等探测在 `startAsync()` 的后台 goroutine 中执行。失败时任务进入 `Failed`，
错误信息持久化并通过事件总线发送给 UI。所有调度器事件经 `fyne.Do()` 在 Fyne 主
线程应用，保证 widget 线程安全。

## 并发上限与 FIFO

`MaxConcurrent` 默认 3，限制同时占用 engine slot 的任务数；BT 还受
`BTMaxConcurrent` 默认 2 的限制。超过上限时 `Start` 静默返回 nil，任务保持
`Pending` 并显示为「等待中」，不占 slot、不增加 WaitGroup。

`releaseSlot` 和 `startAsync` 的清理 defer 会触发 `promotePending`，按
`created_at ASC` 用 SQL `LIMIT` 读取足够填满空闲槽位的候选，而不是扫描全部历史
任务。被 BT 专用上限跳过的任务不会阻塞后面的 HTTP/FTP 任务。

调大设置中的上限会立即触发 promote；调小只影响后续新增任务，不强制暂停运行中的
任务。`abortPrepare` 必须先持久化 `Paused` 再释放 slot，否则 promote 可能再次启动
刚被取消的任务。

## 冷启动回收

业务进程启动后、`scheduler.Run` 前调用 `ReclaimDownloadingTasks()`，把所有
`Downloading` 任务改为 `Paused`，保留已下载字节和分片进度，并发送 `paused` 事件。
这覆盖进程被 SIGKILL、断电或 OOM kill 后留下「下载中但没有 engine」的情况。

## 正常退出

SIGINT/SIGTERM 或托盘 Quit 触发同一条路径：

1. `PauseAll(5s ctx)`：取消所有带 cancel 的 job，等待各 job 将 `Paused` 落盘。
   5 秒超时后 `os.Exit(1)` 兜底。
2. 取消根 context，使 `scheduler.Run` 退出。
3. `uim.Close()` 发送 `MessageKindClose`，2 秒后强制结束 UI 子进程。
4. `tray.Stop()` 退出 systray 事件循环。

第 1 步必须在根 context 取消前执行：`Run` 在 ctx.Done 上会执行最后一次
`flushAll`，若先取消可能把 job 重新写回 `Downloading`。仍在预分配阶段、尚未拥有
cancel 的任务由下次冷启动回收兜底。

BT 任务走同一生命周期：暂停/退出取消上下文，落盘 `Paused` 并摘除种子；之后
`CloseBittorrent()` 关闭共享客户端，停止所有 peer 和做种，但保留磁盘数据。

## 测试

```sh
go test -v ./internal/scheduler/
```

重点用例包括：

- `TestStartIsAsync`：不可达端口下 `Start` 在 500ms 内返回，任务最终失败；
- `TestMaxConcurrent_QueuesNewTasksBeyondCap`：超过上限时静默排队；
- `TestMaxConcurrent_PromotesFIFOOnCompletion`：槽位释放后按创建时间拉起；
- `TestMaxConcurrent_RaiseCapPromotesPending`：调大上限立即 promote；
- BitTorrent 设置映射、tracker 拆分、任务目录推导和 BT 分片进度持久化。

测试使用 `blockingServer` 稳定停留在 prepare 阶段，以精确验证上限和 promote 时机。
