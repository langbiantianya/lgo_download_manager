# ldm — 本地下载管理器

单文件可执行程序的下载管理器，提供 Fyne GUI、持久化的 SQLite 状态、
并发的分片下载，以及用于浏览器接力的 `lgom://` URL 协议。

## 功能特性

- 基于 Range 的并发下载（HTTP、HTTPS、FTP、WebDAV）。
- 支持断点续传：暂停/恢复后会从已下载的字节偏移处继续，已完成的分片不会重新下载。
- 任务级参数设置（URL、保存路径、并发数、分片大小、UA、Cookies、FTP 模式、代理）。
- HTTP/HTTPS 代理三种模式：系统代理（自动检测桌面会话代理设置）、
  不使用代理（始终直连）、手动设置代理（自定义 URL + 绕过列表）。
  自动按平台检测：Linux (GNOME `gsettings` / KDE `kioslaverc` / `/etc/environment`)、
  macOS (`scutil --proxy`)、Windows (WinINET 注册表)。
- `lgom://download?url=...&name=...&ua=...&headers=...&cookies=...` URL 协议 — 将其注册为桌面协议处理程序，即可通过 Unix socket 将 URL 从浏览器转发到正在运行的程序。
- 任务列表与配置持久化到 SQLite（`ldm.sqlite`）。
- GUI 中实时显示进度、每个分片的速度条、下载速率与剩余时间（ETA）。
- 系统托盘：关闭窗口后保留在托盘，托盘菜单支持重新打开窗口与退出。
- 轻量模式：关闭主窗口时释放 widget tree 与 GL 渲染缓存，下次打开时重建。
- 单实例锁：第二次启动会把 URL 转发给主实例后退出。
- 异步任务提交：`Start` 立即返回，HTTP 探测在后台 goroutine 中执行，
  UI 线程不会被不可达 URL 阻塞。

## 编译

```sh
go build -o ldm .
```

带 GUI 运行：

```sh
./ldm
```

无界面模式运行（仅调度器，不启动 Fyne 窗口）：

```sh
./ldm -no-gui
```

强制开启轻量模式：

```sh
./ldm --light
```

## 命令行参数

| 参数        | 默认值          | 说明                                          |
| ----------- | --------------- | --------------------------------------------- |
| `-db`       | `ldm.sqlite`    | SQLite 数据库文件路径                         |
| `-no-gui`   | `false`         | 启动时不打开 Fyne GUI                         |
| `-open-url` | `""`            | 一条 `lgom://...` URL，加入队列               |
| `-light`    | `false`         | 强制开启轻量模式（关闭主窗口时释放 widget 树） |

也支持以位置参数的形式传入 `lgom://` URL（某些桌面环境会以位置参数方式传递 URL）。

## URL 协议

`lgom://download?url=<encoded>[&name=<encoded>[&ua=<encoded>[&headers=<encoded>[&cookies=<encoded>]]]]`

只有 `url` 是必填项。如果程序未运行，第二次启动会把该 URL 转发给主实例后退出。

## 代理

设置对话框的 **HTTP/HTTPS 代理模式** 提供三个选项：

- **使用系统代理**（默认）：进程级缓存优先读取 `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY` 等环境变量，
  未命中则调用平台原生 API 探测（Linux GNOME/KDE/macOS `scutil`/Windows WinINET）。
  切换选项会自动让缓存失效，下次请求重新探测。
- **不使用代理**：始终直连，忽略所有环境变量与系统设置。
- **手动设置代理**：填写完整 URL（支持 `http://`、`https://`、`socks5://`）+ 可选绕过列表
  （逗号分隔主机名，glob 模式 `*.lan`、`192.168.*`）。

PAC / WPAD 自动配置脚本不在支持范围内。

## 设置

存储在 `ldm.sqlite` 的 `settings` 表中，对新建的下载生效。可通过 GUI 中的 **设置** 页面编辑。

| 字段                | 默认值           | 说明                                       |
| ------------------- | ---------------- | ------------------------------------------ |
| Default save dir    | `~/Downloads`    | 「新建任务」对话框默认使用                 |
| Default threads     | 4                | 单任务允许的最大并发连接数                 |
| Min chunk size      | 10 MiB           | 引擎对分片尺寸的下限                       |
| User-Agent          | `Wget/1.21.3`    | 每次请求都会附带                           |
| Cookies             | ""               | Cookie 请求头字符串                        |
| FTP passive mode    | true             | FTP 被动模式 / 主动模式                    |
| Disk preallocation  | true             | 在下载前按完整大小预分配磁盘空间           |
| Proxy mode          | system           | 系统 / 禁用 / 手动                         |
| Proxy URL           | ""               | 手动模式下的代理 URL                       |
| Proxy bypass        | ""               | 手动模式下的绕过列表（逗号分隔）           |
| Light mode          | true             | 关闭主窗口时释放 widget 树                 |

每次修改都会立即持久化。

## 任务状态字段

| 字段              | 是否持久化 | 说明                                                 |
| ----------------- | ---------- | ---------------------------------------------------- |
| Chunk ranges      | 是         | 引擎实际下发的分片布局 — 分片视图使用                |
| Chunk progress    | 是         | 每个分片自起始的字节偏移量                           |
| Total / downloaded | 是         | 累计字节计数                                         |
| Status            | 是         | `Pending` / `Downloading` / `Paused` / `Completed` / `Failed` |
| Error message     | 是         | 失败原因                                               |

`chunk_ranges` 列是后来加入的；老任务首次渲染时会按线程数平均切分，并在下一次启动 Start 后落盘为真实布局。

## 架构

```
main.go                        # 命令行参数、单实例锁、URL 协议处理、退出信号
internal/store/                # SQLite 持久化（任务、设置、分片状态）
internal/protocol/             # HTTP、HTTPS、FTP、WebDAV 驱动 + 代理 + 系统代理探测
  └── sysproxy/                #   跨平台系统代理检测（linux/darwin/windows/other）
internal/engine/               # 分片规划、Range 下载、重试
internal/scheduler/            # 单任务生命周期、状态事件、进度刷盘、异步 Start
internal/prealloc/             # 磁盘预分配辅助
internal/urllauncher/          # lgom:// URL 解析、Unix socket 转发
internal/ui/                   # Fyne 窗口、任务列表、设置对话框、分片视图、系统托盘
```

分片规划规则（若服务器不支持 `Accept-Ranges` 则回退为单流）：

- 文件 < 1 MiB：不分块，1 个 chunk。
- 1 MiB ≤ 文件 < MinChunkSize：按配置的线程数（ChunkCount）等分。
- 文件 ≥ MinChunkSize：每个 chunk 至少 MinChunkSize，由 ChunkCount 限制上限。

### 异步 Start 与 UI 线程

`scheduler.Start(id)` 立即返回，不会因为目标主机不可达而阻塞 UI。
实际探测（HTTP HEAD / FTP SIZE 等）放在 `startAsync()` 后台 goroutine 中：
失败时任务被标记为 `Failed`，错误信息持久化并通过事件总线推送给 UI。
所有调度器事件经 `fyne.Do()` 在 Fyne 主线程上应用到 widget，确保线程安全。

### 代理配置层

`protocol.SetProxyConfig()` 在进程内设置全局代理模式；每个任务可通过
`AuthOptions{ProxyMode, ProxyURL, ProxyBypass}` 覆盖。`effectiveAuth()`
合并 per-call 与全局配置；`proxyFunc()` 根据模式选择：
`ProxyFromEnvironment` → `cachedSystemProxy()`（平台 API 探测）→ 手动 URL。

## 测试

```sh
go test ./...
```

最有参考价值的一条测试是 `internal/ui/chunk_details_e2e_test.go` —
它在没有 `Accept-Ranges` 的 httptest 服务上（强制走单流回退）跑真实的调度器加引擎，
等待下载完成，并断言每一条 Range 进度条都达到 `Value = 1.0`。

`internal/scheduler/scheduler_test.go` 中 `TestStartIsAsync` 用不可达端口验证：
- `Start()` 在 500ms 内返回
- 任务最终进入 `Failed` 状态且 `ErrorMessage` 非空

## 许可证

本项目采用 [Mozilla Public License Version 2.0](./LICENSE)（MPL 2.0）。
完整的许可证文本请参见根目录下的 `LICENSE` 文件。