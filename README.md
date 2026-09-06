# ldm — 本地下载管理器

单文件可执行程序的下载管理器，提供 Fyne GUI、持久化的 SQLite 状态、
并发的分片下载，以及用于浏览器接力的 `lgom://` URL 协议。

业务进程与 UI 子进程通过 Unix socket + 长度前缀 JSON 帧通信：业务进程
持有 SQLite 与 scheduler、UI 子进程是单独的 Fyne 进程；托盘常驻业务进程，
随时可以重新拉起 UI。

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
- 系统托盘常驻业务进程：菜单提供「显示窗口」与「退出」，托盘 Quit 与 SIGINT/SIGTERM 等价，触发同一条优雅退出路径。
- 进程崩溃/被 kill -9 后的兜底：下次启动会把残留的 `Downloading` 任务回收为 `Paused`（保留字节进度），UI 不会再把没有 engine 在跑的任务显示为「下载中」。
- 正常退出前会调用 `PauseAll` 把所有运行中的 job 暂停并落盘；上限 5s，超时直接 `os.Exit(1)` 兜底结束进程。
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
| Status            | 是         | `Pending` / `Downloading` / `Paused` / `Completed` / `Failed` / `FileLost` |
| Error message     | 是         | 失败原因                                               |

`FileLost` 由周期性文件存在性检查（`uimgr.validate`）与重连触发的检查维护——
目标文件被人为删除后任务会被标记为 `FileLost`，UI 提供「重置 + 重启」入口。

## 进程架构

```
              ┌────────────────────────┐
              │ 业务进程（默认）        │
              │  store / scheduler      │
              │  托盘 + urllauncher     │
              │  uimgr.Manager          │
              └────────────┬───────────┘
                           │  拉起自身 + env 注入
                           │  (EnvUIChild=1)
                           ▼
              ┌────────────────────────┐
              │ UI 子进程              │
              │  Fyne + ipcClient      │
              │  (RunChild)            │
              └────────────┬───────────┘
                           │
                Unix socket + 长度前缀 JSON 帧
                (internal/ipc)
```

`uimgr.Manager` 持有当前 UI 子进程的 socket + token。业务侧的所有方法
（add/pause/start/...）经 IPC 转发；UI 侧的 scheduler 事件经同一条 socket 反向
推回。UI 子进程关闭后业务进程不受影响，托盘可随时重新拉起 UI。

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
internal/settings/             # 首次运行默认值 / --light 覆盖 / 进程级代理同步
internal/ipc/                  # 长度前缀 JSON 帧协议（业务↔UI 共用）
internal/uimgr/                # UI 子进程生命周期与 IPC 会话管理
internal/ui/                   # Fyne 窗口、任务列表、设置对话框、分片视图、系统托盘
internal/tray/                 # fyne.io/systray 业务进程常驻托盘
```

### IPC 协议（业务 ↔ UI 子进程）

`internal/ipc` 用长度前缀 JSON 帧（4 字节 big-endian 长度头 + JSON body，
单条消息上限 1 MiB）序列化所有跨进程消息：

- `MsgHello`（UI→业务）：握手 token
- `MsgInit`（业务→UI）：权威设置快照
- `MsgCall`（UI→业务）：`list` / `add` / `start` / `pause` / `delete` / `probe` /
  `save_settings`，返回 `MsgResult`
- `MsgEvent`（业务→UI）：scheduler 事件（added / started / progress / paused /
  completed / failed / updated）
- `MsgShow`（业务→UI）：托盘「显示窗口」菜单
- `MsgClose`（业务→UI）：业务进程退出，UI 收到后主动 `a.Quit()`

业务侧只用一个进程对应一个 UI 会话；UI 断开时业务侧 `cmd.Wait()` 触发
`acceptLoop` 清理，可由托盘「显示窗口」再次拉起。

### 新建任务对话框

「新建任务」使用独立 Fyne 窗口（不是 modal popup）：用户可以在主窗口
和对话框之间切换，文件大小预览由 `svc.Probe` 异步探测；URL 变化时自动
从路径末段提取文件名填入保存路径。点击「开始下载」会依次调
`svc.AddTask` → `svc.Start`（二者都经 IPC 落到业务进程），成功后关闭
对话框。AddTask / Start 任一返回错误时弹错误对话框，保留窗口供修正。

### 任务生命周期：冷启动回收与优雅退出
**冷启动** — 业务进程启动后、scheduler.Run 启动前，调用
`scheduler.ReclaimDownloadingTasks()`：扫 store 中所有 `Downloading` 行
改成 `Paused`，保留已下载字节与 chunk 进度，并发 `paused` 事件给 UI
同步行状态。这覆盖了「上次进程被 SIGKILL / 断电 / OOM kill」后留下
的「下载中却没有 engine 在跑」的脏数据。

**正常退出** — `<-quit`（SIGINT/SIGTERM 或托盘 Quit）触发：
1. `sc.PauseAll(5s ctx)`：cancel 所有 `cancel != nil` 的 job，
   `WaitGroup.Wait()` 阻塞到每个 per-job goroutine 把 `Paused`
   落盘。5s 超时未完成则 `os.Exit(1)` 兜底结束进程。
2. `cancel()` 根 ctx — scheduler.Run 退出。
3. `uim.Close()` 发 `MsgClose`，等 2s 后强杀 UI 子进程。
4. `tray.Stop()` 退出 systray 事件循环。

第 1 步必须在 `cancel()` 之前：scheduler.Run 在 ctx.Done 上会跑最后一次
`flushAll`，把每个 job 的 `rj.status=Downloading` 写回 store；先让
PauseAll 把那些 job 从 `s.jobs` 移除并落盘 `Paused`，flushAll 就会
被空 jobs map 短路，不会回写。
预分配阶段的 slot（Start 已 reserve 但 `startAsync` 尚未替换为带 cancel
的 runningJob）不会被 PauseAll 命中——这类任务的最终状态由下次冷启动的
ReclaimDownloadingTasks 兜底。

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