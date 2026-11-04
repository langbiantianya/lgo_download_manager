# 设置

设置保存在 `lgdm.sqlite` 的 `settings` 表中，修改后立即持久化，并对之后新建的
下载生效。GUI 的「设置」页面是主要编辑入口。

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| Default save dir | 系统默认下载目录 | Windows Known Folder / Linux XDG / macOS 下载目录 |
| Default threads | 4 | 单任务最大并发连接数 |
| Min chunk size | 10 MiB | 引擎分片尺寸下限 |
| Max concurrent | 3 | 同时下载任务数上限，超额任务 FIFO 排队 |
| HTTP/FTP limit | 0（不限） | HTTP/HTTPS/WebDAV/FTP 共享限速，单位 KiB/s |
| User-Agent | `Wget/1.21.3` | 每次请求附带 |
| Cookies | `""` | Cookie 请求头 |
| FTP passive mode | true | FTP 被动模式；关闭后使用主动模式 |
| Disk preallocation | true | 下载前预分配 `.part`；关闭后按实际写入增长 |
| Proxy mode | `system` | 系统 / 禁用 / 手动 |
| Proxy URL | `""` | 手动模式代理 URL |
| Proxy bypass | `""` | 手动模式绕过列表 |
| PAC/WPAD script URL | `""` | 系统模式 PAC URL；留空用系统配置，`wpad` 自动发现 |
| Light mode | true | 关闭主窗口时释放 widget 树 |
| Auto start | false | 系统登录后是否静默启动，详见 [autostart](../autostart/README.md) |
| BT listen port | 首次启动自动选择 | BT 监听端口；0 表示每次随机分配 |
| BT max concurrent | 2 | BT 同时下载上限 |
| BT max seeding | 2 | 同时做种上限 |
| BT download limit | 0（不限） | BT 全局下载限速，单位 KiB/s |
| BT upload limit | 0（不限） | BT 全局上传限速，单位 KiB/s |
| BT max connections | 50 | 单种子最大 peer 数 |
| DHT | true | 启用 DHT |
| PEX | true | 启用节点交换 |
| BT seeding | 2 | 分享率上限；0 表示下载完成后不做种 |
| Extra trackers | `""` | 每行一个 tracker，追加到每个 BT 任务 |

数值上限：线程数 ≤ 64、分块大小 ≤ 1024 MiB、并发任务数 ≤ 64、BT 并发/做种数
≤ 64、BT peer 数 ≤ 1000。超上限的输入不采纳，保留上一个有效值。

BT 监听端口会在后台同时探测 TCP 与 UDP 占用；被占用时在输入框下方提示。默认保存
目录不允许为空，空目录旧版本曾会被误判为首次运行并重置设置。

代理相关实现见 [protocol](../protocol/README.md)，自启调和见
[autostart](../autostart/README.md)。
