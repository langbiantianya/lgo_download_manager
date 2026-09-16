# lgdm — 本地下载管理器

单文件可执行程序的下载管理器，提供 Fyne GUI、持久化的 SQLite 状态、
并发的分片下载，以及用于浏览器接力的 `lgom://` URL 协议。

业务进程与 UI 子进程通过 Unix socket + 长度前缀 JSON 帧通信：业务进程
持有 SQLite 与 scheduler、UI 子进程是单独的 Fyne 进程；托盘常驻业务进程，
随时可以重新拉起 UI。

## 功能特性

- 基于 Range 的并发下载（HTTP、HTTPS、FTP、WebDAV）。
- 支持断点续传：暂停/恢复后会从已下载的字节偏移处继续，已完成的分片不会重新下载。
- 任务级参数设置（URL、保存路径、并发数、分片大小、UA、Cookies、FTP 模式、代理）。
- 并发任务数量上限：可在「设置」里配置同时下载的最大任务数（默认 3）。超出限额
  的新任务保持 `Pending`（UI 显示为「等待中」），按 FIFO 顺序在有 slot 释放时自动启动；
  上限调小不会强制暂停正在运行的任务（仅影响后续新增）。
- HTTP/HTTPS 代理三种模式：系统代理（自动检测桌面会话代理设置）、
  不使用代理（始终直连）、手动设置代理（自定义 URL + 绕过列表）。
  自动按平台检测：Linux (GNOME `gsettings` / KDE `kioslaverc` / `/etc/environment`)、
  macOS (`scutil --proxy`)、Windows (WinINET 注册表)。
- `lgom://download?url=...&name=...&ua=...&headers=...&cookies=...` URL 协议 — Windows 安装包会把它注册成桌面协议处理程序（`HKCU\Software\Classes\lgom`）：浏览器里的链接直接拉起 lgdm 并开始下载；lgdm 已在运行时，新进程把 URL 转发给主实例（Windows 命名管道 / 其它平台 Unix socket）后退出。
- 任务列表与配置持久化到 SQLite（`lgdm.sqlite`）。
- GUI 中实时显示进度、每个分片的速度条、下载速率与剩余时间（ETA）。
- 系统托盘常驻业务进程：菜单提供「显示窗口」与「退出」，托盘 Quit 与 SIGINT/SIGTERM 等价，触发同一条优雅退出路径。
- 进程崩溃/被 kill -9 后的兜底：下次启动会把残留的 `Downloading` 任务回收为 `Paused`（保留字节进度），UI 不会再把没有 engine 在跑的任务显示为「下载中」。
- 正常退出前会调用 `PauseAll` 把所有运行中的 job 暂停并落盘；上限 5s，超时直接 `os.Exit(1)` 兜底结束进程。
- 异步任务提交：`Start` 立即返回，HTTP 探测在后台 goroutine 中执行，
  UI 线程不会被不可达 URL 阻塞。

## 构建

```sh
go build -o bin/lgdm.exe .        # Windows,本机架构(手工调试用;打包脚本统一产出 bin\lgdm-<arch>.exe)
go build -o bin/lgdm .            # Linux / macOS
```

Windows 上**正式发布**的二进制是 GUI 子系统构建（`go build -ldflags "-H windowsgui"`），
从 lgom:// 协议 / 资源管理器拉起时不会闪出黑色控制台窗口；从 cmd / PowerShell
加 `--debug` 启动时由 `internal/logging.AttachParentConsole` 把进程挂回父
终端，日志照常输出；GUI 子系统下 attach 失败（无父 console）则回退到文件
日志（同样 Debug 级别），保证调试信息不丢。

Windows 上要**安装包**（MSI / EXE，x64 与 arm64）用打包脚本，细节见下一节：

```powershell
pwsh -File scripts\package.ps1
```

### 版本元数据

`internal/version` 包提供 `Version` / `Commit` / `Date` 三个变量，默认
是开发期占位符；`scripts/package.ps1` 通过 `-ldflags -X` 在链接期把它们
覆盖成 `git describe` / `git rev-parse --short HEAD` / `date -u` 的真实结果
（手工 `go build` 同样可以加 `-ldflags`）。启动时 `lgdm` 会在日志中打印
一行 `lgdm <version> (commit <c>, built <d>)`。

## Windows 打包（安装包）

一条命令完成「按架构编译 + 出 MSI / EXE 安装包」：

```powershell
pwsh -File scripts\package.ps1          # x64 + arm64 各一份 MSI 与 EXE
```

### 环境要求

| 依赖 | 说明 |
| --- | --- |
| Windows 10/11（x64 打包机） | 脚本是 PowerShell，Windows PowerShell 5.1 与 PowerShell 7+ 都可用 |
| Go | 版本见 `go.mod` |
| C 工具链 | Fyne 在 Windows 上是 CGO + GLFW/OpenGL，必须有一个 **gcc 风格命令行** 的 C 编译器（GCC 或 clang），见「C 工具链与架构」 |
| 打包工具 | WiX CLI（MSI）、Inno Setup 6（EXE）；缺失时脚本用 winget 自动安装 |

脚本会自动安装的 winget 包：

| 工具 | winget 包 | 用途 |
| --- | --- | --- |
| WiX Toolset CLI | `WiXToolset.WiXCLI`（脚本固定 `6.0.2`） | 编译 `installer/lgdm.wxs` → MSI |
| Inno Setup 6 | `JRSoftware.InnoSetup` | 编译 `installer/installer.iss` → EXE |
| LLVM-MinGW (UCRT) | `MartinStorsjo.LLVM-MinGW.UCRT` | arm64 的 C 交叉工具链（同时含 x64 target） |

`-SkipToolInstall` 关闭自动安装；`-WixPath` / `-IsccPath` / `-CCX64` / `-CCArm64`
可以指向已有的安装。

### 准备编译环境（winget / scoop）

编译只要有 Go、Git 和一个 gcc 风格的 C 编译器；打包工具（WiX / Inno Setup）
交给 `scripts/package.ps1` 自动装即可。两条路任选一条，包名与版本按本机的
winget / scoop 清单核对过（WiX、Inno Setup、LLVM-MinGW 是本机用 winget 装出来跑通的）。

**winget**（Windows 10 1809+ 一般自带 App Installer）

```powershell
winget install --id GoLang.Go --exact --silent                       # Go 1.27
winget install --id Git.Git --exact --silent                         # Git(版本号注入用)
winget install --id MartinStorsjo.LLVM-MinGW.UCRT --exact --silent   # clang + mingw-w64 sysroot:x64 与 arm64 都能编

# 打包工具(脚本会按需自动装,CI 预置时可以显式执行;WiX 必须锁 6.0.2,不指定会拿到 v7)
winget install --id WiXToolset.WiXCLI --version 6.0.2 --exact --silent
winget install --id JRSoftware.InnoSetup --exact --silent
```

**scoop**

```powershell
scoop install go git      # go 1.27 / git 2.55
scoop install gcc         # GCC 15.2 + binutils,target = x86_64-w64-mingw32,够 x64 用
```

scoop 侧的边界（都实测过）：

- `mingw`（niXman mingw-builds）也只有 x86_64 / i686，**没有 aarch64 target**；
  arm64 要的 aarch64 sysroot 只有 LLVM-MinGW 带 —— 出 arm64 包时用上面的
  winget 命令装它。
- 打包工具别走 scoop：main bucket 里的 `wixtoolset` 是 **v7.0.0**（要求接受
  OSMF EULA，本项目的脚本会明确拒绝），Inno Setup 也不在 main bucket。

两条通用注意：

- 新装的工具要**重开终端**才进 `PATH`。`go`/`gcc` 依赖 PATH；打包工具即使没进
  PATH 也没关系——脚本会去 winget 的包目录里兜底查找。
- scoop 的 shim 是 PowerShell 脚本，报「禁止运行脚本」时执行
  `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`（只影响当前用户）。

### 命令与参数

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `-Format` | `both` | `msi` / `exe` / `both` |
| `-Arch` | `x64,arm64` | 逗号分隔；接受 `x64`/`amd64`、`arm64`/`aarch64` |
| `-Version` | `git describe --tags --always --dirty` | 写进二进制 `internal/version` 与安装包文件名 |
| `-WinVersion` | 从 `-Version` 推导 | MSI `ProductVersion` / `VersionInfoVersion` 需要的 `X.Y.Z.W` |
| `-OutputDir` | `<repo>\dist` | 产物目录 |
| `-SkipBuild` | 关 | 复用已有的 `bin\lgdm-<arch>.exe`（仍会校验 PE 架构） |
| `-SkipToolInstall` | 关 | 工具缺失直接报错，不尝试 winget |
| `-WixPath` / `-IsccPath` | 自动查找 | 显式指定 `wix.exe` / `ISCC.exe` |
| `-CCX64` / `-CCArm64` | 自动查找 | 显式指定对应架构的 C 编译器 |

```powershell
pwsh -File scripts\package.ps1 -Format msi            # 只出 MSI
pwsh -File scripts\package.ps1 -Format exe -Arch arm64 # 只出 arm64 的 EXE
pwsh -File scripts\package.ps1 -SkipBuild             # 二进制已编好,只重新出包
pwsh -File scripts\package.ps1 -Version v1.0.0 -WinVersion 1.0.0.0
```

### 产物

```
dist\lgdm-setup-<版本>-x64.msi       dist\lgdm-setup-<版本>-arm64.msi
dist\lgdm-setup-<版本>-x64.exe       dist\lgdm-setup-<版本>-arm64.exe
bin\lgdm-x64.exe                     bin\lgdm-arm64.exe
```

- 每个架构单独一份：**MSI 一个包只能承载一种架构**，无法合并；MSI 与 EXE
  在同一架构上功能对等，任选其一安装即可（不要同时装两个）。
- 打包用的二进制是 `bin\lgdm-<arch>.exe`，装到目标机器上统一改名为
  `lgdm.exe`（协议注册表命令行、快捷方式、`taskkill` 都按 `lgdm.exe` 找）。
- 中间产物 `*.wixpdb` 与 `dist/`、`bin/` 一样在 `.gitignore` 里。

### C 工具链与架构

- **MSVC 的 `cl.exe` 不能当 `CC`**：cgo 直接用 `-c/-o/-I/-D` 这类 gcc 参数调用
  编译器，`cl.exe` 的 `/c`、`/Fo`、`/I` 与之不兼容，Go 也一直没有支持 MSVC
  （golang/go#20982）。
- **clang 可以**：`LLVM-MinGW`（winget 包 `MartinStorsjo.LLVM-MinGW.UCRT`）是
  clang + mingw-w64 sysroot 的发行版，一个包同时提供
  `x86_64-w64-mingw32-clang` 与 `aarch64-w64-mingw32-clang`，两个架构共用。
- 解析顺序：`-CCX64`/`-CCArm64` → PATH 上的 `<triple>-clang` → winget 的
  LLVM-MinGW 安装目录；x64 找不到 clang 时退回主机默认 `gcc`。arm64 没有
  aarch64 工具链就一定编不过（下一条）。
- **交叉编译要显式打开 cgo**：`GOARCH` 与主机不同时 Go 默认把 cgo 关掉，而
  Fyne 的 GL 绑定只有 cgo 实现，表现是 `build constraints exclude all Go files
  ... go-gl/gl/v3.1/gles2`。脚本会设 `CGO_ENABLED=1` 并传入 `CC`；手工编译：

  ```powershell
  $env:GOARCH='arm64'; $env:CGO_ENABLED='1'
  $env:CC='C:\...\llvm-mingw-<ver>-ucrt-x86_64\bin\aarch64-w64-mingw32-clang.exe'
  go build -trimpath -o bin\lgdm-arm64.exe .
  ```

  `-CCArm64` 也要给 Windows 绝对路径（`/c/...` 这种会被 Go 拒绝：
  `CC environment variable is relative; must be absolute path`）。
- 出包前脚本会读 PE 头的 `Machine` 字段校验产物架构（x64=`0x8664`、
  arm64=`0xAA64`），防止 `-SkipBuild` 复用了另一个架构的二进制、或工具链
  target 传错，打出「平台与载荷不匹配」的包。
- 非 Windows 主机交叉编译 `lgdm.exe` 需要 mingw-w64（`x86_64-w64-mingw32-gcc`
  在 PATH 上）。

### 安装、升级与卸载

两种安装包行为一致：

- 用户态安装到 `%LOCALAPPDATA%\Programs\lgo_download_manager`，
  `PrivilegesRequired=lowest` / `Scope="perUser"`：不需要 UAC、不需要管理员，
  不装服务、不动 PATH，协议注册只写 `HKCU`。「应用和功能」里的卸载条目由
  Windows Installer 自行登记（MSI 按安装上下文落 HKCU 或 HKLM，EXE 安装包
  固定在 HKCU）。
- 写 `HKCU\Software\Classes\lgom` 注册 `lgom://` 处理程序，命令行
  `"<install>\lgdm.exe" "%1"`，`DefaultIcon` 指向安装目录里的 `lgdm.ico`。
- 开始菜单快捷方式（EXE 安装包另有「桌面快捷方式」勾选项，MSI 直接创建）。
- 安装/升级/卸载前先结束正在运行的 `lgdm.exe`（MSI 用 immediate 自定义动作
  `StopRunningLgdm` 排在 `InstallValidate` 之前，EXE 用 `[Code]
  PrepareToInstall`），避免 exe 被占用。
- **升级保留数据，卸载删数据**：卸载会删掉安装目录、`lgom` 注册表键、快捷方式，
  以及用户数据目录 `%LOCALAPPDATA%\lgo_download_manager`（任务库
  `lgdm.sqlite`、`-wal`/`-shm`、UI IPC 的 `ui-*.sock`）；覆盖安装/升级不碰它。

静默安装/卸载（脚本化部署用）：

```powershell
msiexec /i dist\lgdm-setup-v1.0.0-x64.msi /qn                     # MSI 静默装
.\dist\lgdm-setup-v1.0.0-x64.exe /VERYSILENT /SUPPRESSMSGBOXES /NORESTART
msiexec /x {ProductCode} /qn                                      # ProductCode 见「应用和功能」
& "$env:LOCALAPPDATA\Programs\lgo_download_manager\unins000.exe" /VERYSILENT
```

装完之后，浏览器/资源管理器里的 `lgom://download?url=...` 会拉起 `lgdm` 并把
该 URL 加进下载队列：程序没在运行时，新进程就是主实例，自己处理 URL；程序已在
运行时（托盘常驻），新进程把 URL 经命名管道转发给主实例后退出。

### 已验证 / 已知限制

在 x64 Windows 上实测过的（`scripts/package.ps1` 全绿）：

- x64 的 MSI 与 EXE：静默安装 → 在 `C:\Windows\System32` 下点 `lgom://`
  冷启动入队 → 再点一次由运行中实例转发 → 卸载后安装目录、数据目录、注册表键、
  快捷方式、ARP 条目全部清空；同版本重建再装（升级路径）任务库保留。
- arm64 的 MSI/EXE：MSI `Template=Arm64`、内部载荷是 ARM64 PE
  （`machine=0xAA64`），在 x64 上安装被正确拒绝（MSI `1633`
  「这个处理器类型不支持该安装程序包」、EXE 非 0 退出）。
- 版本覆盖：`-Version v9.9.9 -WinVersion 9.9.9.4` → EXE `FileVersion=9.9.9.4`、
  MSI `ProductVersion=9.9.9.4`。

限制：

- **arm64 包没有在真机跑过**：本机是 x64，只验证到「平台/载荷是 arm64 + 在
  x64 上被拒绝」。要确认 arm64 上能装能跑，需要一台 Windows on ARM。
- **产物未签名**：没有代码签名步骤，分发后首次运行会触发 SmartScreen 提示；
  需要签名时给 ISCC 传 `/S` 签名工具、给 `wix build` 传 `-sign`。
- WiX v7 起要求接受 OSMF EULA，本项目不引入：脚本只用 v6/v5，检测到 v7 会报错
  并给出降级命令。
- Inno Setup 的架构标识没有 `arm64compatible`（只有 `arm64` / `x64compatible` /
  `arm32compatible` / `x86compatible`），写错直接编译失败。

### 排错

| 现象 | 原因 / 处理 |
| --- | --- |
| `error WIX7015: You must accept the Open Source Maintenance Fee (OSMF) EULA` | PATH 上的 `wix.exe` 是 v7；`winget install --id WiXToolset.WiXCLI --version 6.0.2 --exact --silent` 或 `-WixPath` 指向 v6 |
| `error WIX0103: Cannot find the File file ...\bin\lgdm-<arch>.exe` | 该架构的二进制还没编（`-SkipBuild` 时最容易遇到）；去掉 `-SkipBuild` 或先编译 |
| 安装报 `1633 这个处理器类型不支持该安装程序包` | 装了架构不符的包（如把 arm64 包往 x64 上装）；换对应架构的产物 |
| `build constraints exclude all Go files ... go-gl/gl/v3.1/gles2` | cgo 被关掉了（交叉编译时的默认行为）；用脚本编译或手工设 `CGO_ENABLED=1` |
| `go: CC environment variable is relative; must be absolute path` | `CC` 给了 `/c/...` 形式；改成 `C:\...` |
| `找不到 aarch64 的 C 交叉编译器` | 装工具链：`winget install --id MartinStorsjo.LLVM-MinGW.UCRT --exact --silent`，或 `-CCArm64` 指定 |
| `winget 安装 ... 返回 -1978335189` | winget 认为已装其它版本；脚本会继续查找已安装的工具，找不到再按提示手动装 |
| 编辑 `scripts/package.ps1` 后 Windows PowerShell 5.1 报语法错误 | 脚本含中文，必须存成 **UTF-8 with BOM**（PS 7 不敏感，PS 5.1 会按 ANSI 读） |

## 运行

带 GUI 运行：

```sh
./lgdm
```

无界面模式运行（仅调度器，不启动 Fyne 窗口）：

```sh
./lgdm -no-gui
```

强制开启轻量模式：

```sh
./lgdm --light
```

## 命令行参数

| 参数        | 默认值          | 说明                                          |
| ----------- | --------------- | --------------------------------------------- |
| `-db`       | 见下            | SQLite 数据库文件路径                         |
| `-no-gui`   | `false`         | 启动时不打开 Fyne GUI                         |
| `-open-url` | `""`            | 一条 `lgom://...` URL，加入队列               |
| `-light`    | `false`         | 强制开启轻量模式（关闭主窗口时释放 widget 树） |

`-db` 默认值：Windows 上是
`%LOCALAPPDATA%\lgo_download_manager\lgdm.sqlite`（绝对路径 —— 安装后的
lgdm 会被 `lgom://` 协议从任意工作目录拉起，相对路径会因 CWD 不可写而
开库失败，托盘实例与协议实例也会落到两份不同的库）；其它平台仍是
相对当前工作目录的 `lgdm.sqlite`。

> 库文件名从 `ldm.sqlite` 改成了 `lgdm.sqlite`（安装目录名仍是
> `lgo_download_manager`）：如果本地还留着旧的 `ldm.sqlite`，程序不会去读它，
> 需要的话手工改名即可。

也支持以位置参数的形式传入 `lgom://` URL（某些桌面环境会以位置参数方式传递 URL）。

## URL 协议

`lgom://download?url=<encoded>[&name=<encoded>[&ua=<encoded>[&headers=<encoded>[&cookies=<encoded>]]]]`

只有 `url` 是必填项。URL 由操作系统交给新启动的 `lgdm.exe`（Windows 安装包
注册的命令行是 `"<install>\lgdm.exe" "%1"`）：没有实例在运行时，这个进程就是
主实例，自己把 URL 入队并下载；已有实例在运行时，新进程通过命名管道
（Windows）/ Unix socket（其它平台）把 URL 转发给主实例后退出，转发失败
（例如主实例刚好在退出）以非零状态结束并打印原因。

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

存储在 `lgdm.sqlite` 的 `settings` 表中，对新建的下载生效。可通过 GUI 中的 **设置** 页面编辑。

| 字段                | 默认值           | 说明                                       |
| ------------------- | ---------------- | ------------------------------------------ |
| Default save dir    | `~/Downloads`    | 「新建任务」对话框默认使用                 |
| Default threads     | 4                | 单任务允许的最大并发连接数                 |
| Min chunk size      | 10 MiB           | 引擎对分片尺寸的下限                       |
| Max concurrent      | 3                | 同时下载任务数上限（超额任务 FIFO 排队）   |
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
internal/urllauncher/          # lgom:// URL 解析、单实例锁、URL 转发
                               #   （Windows 命名管道 / 其它平台 Unix socket）
internal/settings/             # 首次运行默认值 / --light 覆盖 / 进程级代理同步
internal/ipc/                  # 长度前缀 JSON 帧协议（业务↔UI 共用）
internal/uimgr/                # UI 子进程生命周期与 IPC 会话管理
internal/ui/                   # Fyne 窗口、任务列表、设置对话框、分片视图、系统托盘
internal/tray/                 # fyne.io/systray 业务进程常驻托盘
assets/                        # 图标（安装包快捷方式 / lgom:// DefaultIcon 都用它）
installer/                     # lgdm.wxs（WiX MSI）、installer.iss（Inno Setup EXE）
scripts/package.ps1            # 打包入口：编译 + 出 MSI / EXE 安装包
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

### 并发任务数量上限与 FIFO 提升

`MaxConcurrent`（默认 3，可通过「设置」调整）限制同时占用 engine slot
的任务数。`scheduler.Start` 在入参任务处于 `Pending` 且 `len(jobs) >= cap`
时静默返回 nil——任务在 store 里保持 `Pending`（UI 渲染为「等待中」），
不占 slot、不增 wg，等待被 `promotePending` 拉起。

`releaseSlot` 与 `startAsync` 的清理 defer 都会触发 `promotePending`：
扫 store 中所有 `Pending` 且无 slot 的任务，按 `created_at ASC` 顺序，
逐个 `Start` 提升到 `Downloading`，直到 `len(jobs) == MaxConcurrent` 或
没有候选。任一 slot 释放都会让排队中的最早一个任务自动接上。

`SetMaxConcurrent(n)` 是运行时调入口：用户在「设置」调大上限时立即
触发 promote，把累积的 Pending 任务按 FIFO 拉起；调小上限只影响后续
新增的任务，正在跑的不会自动暂停。UI 的 `MethodSaveSettings` 经
`uimgr.Manager.SetSettings` 把新值推到 scheduler。

`abortPrepare`（任务在 probe/预分配阶段被 Pause 取消的快速收尾）的
执行顺序为「先写 store 为 Paused，再 `releaseSlot`」：必须先持久化状态，
否则 `releaseSlot` 触发的 promote 会看到自己刚被取消的任务仍显示为
`Pending` 并再次 Start，导致死循环。

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

`internal/scheduler/scheduler_test.go` 中的 `TestMaxConcurrent_*` 覆盖并发上限：
- `_QueuesNewTasksBeyondCap`：`len(jobs) >= cap` 时 `Start` 静默排队、不占 slot
- `_PromotesFIFOOnCompletion`：slot 释放后按 `created_at` 升序拉起 Pending
- `_RaiseCapPromotesPending`：`SetMaxConcurrent` 调大时立即 promote 积压任务
这些测试用 `blockingServer` 让 HTTP probe 稳定停留在 prepare 阶段，
以便精确断言 cap 触发与 promote 时机。

## 许可证

本项目采用 [Mozilla Public License Version 2.0](./LICENSE)（MPL 2.0）。
完整的许可证文本请参见根目录下的 `LICENSE` 文件。