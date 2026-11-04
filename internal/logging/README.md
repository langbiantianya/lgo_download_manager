# 日志

日志统一由 `internal/logging` 封装，底层使用 `log/slog`。

- 默认写入 `<config>/lgdm.log`，按天轮转为 `lgdm.log.yyyymmdd`，启动时清理超过
  7 天的历史文件。
- `--debug` 输出 Debug 级别日志。Unix 下写 stderr。
- Windows 正式构建是 GUI 子系统。从 cmd/PowerShell 加 `--debug` 时，
  `AttachParentConsole(ATTACH_PARENT_PROCESS)` 会挂回父终端；从资源管理器或
  URL 协议拉起且没有父 console 时，回退写 `lgdm.log`，仍保持 Debug 级别。
- 业务进程通过 `LGDM_DEBUG=1` 和 `LGDM_LOG_DIR=<config>` 把日志设置传给独立
  `lgdm-ui`；自我复刻回退路径同样继承这两个变量。

日志目录与 SQLite 数据库共用 `-config` 目录，便于一起收集和排障。
