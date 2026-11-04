# 任务存储

`internal/store` 使用 SQLite 保存任务、设置、分片状态和 BT 元数据。数据库固定为
`<config>/lgdm.sqlite`，与日志位于同一运行时目录。任务状态字段如下：

| 字段 | 持久化 | 说明 |
| --- | --- | --- |
| Chunk ranges | 是 | 引擎实际下发的分片布局 |
| Chunk progress | 是 | 每个分片自起始的字节偏移 |
| Total / downloaded | 是 | 累计字节计数 |
| Status | 是 | `Pending` / `Downloading` / `Paused` / `Completed` / `Failed` / `FileLost` |
| Error message | 是 | 失败原因 |
| `bt_file` | 是 | BT 单文件任务的相对路径，用于「打开文件」 |

`FileLost` 由周期性文件存在检查和重连检查维护。目标文件被删除后任务标记为
`FileLost`，UI 提供「重置 + 重启」。BT 的 `SavePath` 是任务目录，检查目录是否存在。

磁力链接的 info 字典保存在 `bt_metainfo` 表（`task_id` → `info` BLOB），与
`tasks` 分表。元数据可能很大且会变化，而 `tasks` 行会在每次进度事件中序列化推给
UI，不适合把元数据塞进任务结构体。删除任务时在同一把锁内级联清理。

```sh
go test -v ./internal/store/
```
