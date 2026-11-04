# UI 进程

`internal/ui` 使用 Fyne 实现主窗口、任务列表、设置、任务详情和新建任务对话框。
正式安装包通过 `cmd/lgdm-ui` 构建独立的 `lgdm-ui` 进程，经
[RPC](../rpc/README.md) 访问业务进程。它不链接 store、scheduler、协议或
BitTorrent；业务进程找不到同目录的 `lgdm-ui` 时，才回退到业务二进制自我复刻。

## 新建任务

「新建任务」使用独立 Fyne 窗口而不是 modal popup，用户可在主窗口和对话框间切换。
文件大小由 `svc.Probe` 异步探测；URL 变化时自动从路径末段提取文件名并填入保存
路径。点击「开始下载」后依次调用 `svc.AddTask` 和 `svc.Start`，成功后关闭窗口。
任一调用失败都会弹出错误并保留窗口供修正。

URL 为磁力链接或 `.torrent` 时，对话框切换为「保存目录」语义，默认目录名来自
`dn` 参数、infohash 前缀或种子文件名。

## 轻量与自启

- 轻量模式关闭主窗口时释放 widget 树。
- `--autostart` 首次启动只保留托盘和调度器，按需拉起 UI。
- `--no-gui` 不启动 UI，浏览器和 URL 请求直接入队。

## 测试

```sh
go test -v ./internal/ui/
```

`chunk_details_e2e_test.go` 在无 Range 服务的回退路径上运行真实 engine 和 scheduler，
验证每条分片进度条最终完成。UI 还覆盖批量 URL、任务详情、设置和 BT 对话框。
