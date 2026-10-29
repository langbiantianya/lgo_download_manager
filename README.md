# lgdm — 本地下载管理器

单文件可执行程序的下载管理器，提供 Fyne GUI、持久化的 SQLite 状态、
并发的分片下载、BitTorrent（磁力链接 / `.torrent`），以及用于浏览器接力的
两条交接通道（本地端口 / `lgom://` URL 协议）。

业务进程与 UI 子进程通过 Unix socket + 长度前缀 JSON 帧通信：业务进程
持有 SQLite 与 scheduler、UI 子进程是单独的 Fyne 进程；托盘常驻业务进程，
随时可以重新拉起 UI。

## 功能特性

- 基于 Range 的并发下载（HTTP、HTTPS、FTP、WebDAV）。
- 支持断点续传：暂停/恢复后会从已下载的字节偏移处继续，已完成的分片不会重新下载；
  Range 响应会经过严格校验，ETag/Last-Modified 会参与跨会话一致性检查，
  不支持 Range 的来源会安全地从 0 重新下载。
- 下载中的数据写入 `<目标文件>.part`，同步完成后才原子重命名为正式文件，避免把
  半成品暴露成可用文件。
- BitTorrent 下载：`magnet:` 磁力链接与 `.torrent` 种子（本地路径或 HTTP 链接），
  底层用 [anacrolix/torrent](https://github.com/anacrolix/torrent)；DHT / PEX /
  tracker、全局限速、做种分享率与同时做种数、BT 专用并发上限、首次启动自动选择并持久化的监听端口；进度按「每个种子文件一块」
  展示；磁力链接的元数据会缓存进库，之后离线也能续传；详见
  「[BitTorrent 下载](#bittorrent-下载)」。
- 任务级参数设置（URL、保存路径、并发数、分片大小、UA、Cookies、FTP 模式、代理）。
- 并发任务数量上限：可在「设置」里配置同时下载的最大任务数（默认 3）。超出限额
  的新任务保持 `Pending`（UI 显示为「等待中」），按 FIFO 顺序在有 slot 释放时自动启动；
  上限调小不会强制暂停正在运行的任务（仅影响后续新增）。BT 任务另有独立并发上限，
  默认同时下载 2 个。
- HTTP/HTTPS 代理三种模式：系统代理（自动检测桌面会话代理设置）、
  不使用代理（始终直连）、手动设置代理（自定义 URL + 绕过列表）。
  自动按平台检测：Linux (GNOME `gsettings` / KDE `kioslaverc` / `/etc/environment`)、
  macOS (`scutil --proxy`)、Windows (WinINET 注册表)。
- 浏览器扩展接力：业务进程在 `127.0.0.1` 的候选端口（`17821`+）上收扩展的 JSON
  交接请求（`internal/handoff`），不弹确认框、不开标签页、也不需要额外安装——
  扩展侧按「本地端口 → `lgom://` URL」顺序尝试，URL 协议是最后手段；
  详见「[浏览器扩展接力](#浏览器扩展接力)」。
- `lgom://download?url=...&name=...&ua=...&headers=...&cookies=...` URL 协议 — 安装包会把它注册成桌面协议处理程序（Windows：`HKCU\Software\Classes\lgom`；Linux / Flatpak：`.desktop` 里的 `MimeType=x-scheme-handler/lgom`）：浏览器里的链接直接拉起 lgdm 并开始下载；lgdm 已在运行时，新进程把 URL 转发给主实例（Windows 命名管道 / 其它平台 Unix socket）后退出。
- 任务列表与配置持久化到 SQLite（`lgdm.sqlite`）。
- GUI 中实时显示进度、每个分片的速度条、下载速率与剩余时间（ETA）。
- 系统托盘常驻业务进程：菜单提供「显示窗口」与「退出」，托盘 Quit 与 SIGINT/SIGTERM 等价，触发同一条优雅退出路径。
- 进程崩溃/被 kill -9 后的兜底：下次启动会把残留的 `Downloading` 任务回收为 `Paused`（保留字节进度），UI 不会再把没有 engine 在跑的任务显示为「下载中」。
- 正常退出前会调用 `PauseAll` 把所有运行中的 job 暂停并落盘；上限 5s，超时直接 `os.Exit(1)` 兜底结束进程。
- 异步任务提交：`Start` 立即返回，HTTP 探测在后台 goroutine 中执行，
  UI 线程不会被不可达 URL 阻塞。
- 各平台都有安装包，都是「一条命令编译 + 出包」：Windows 用 `scripts/package.ps1`
  出 MSI / EXE（x64 + arm64），Linux 用 `scripts/build_flatpak.sh` 出 Flatpak 单文件包
  （x86_64 + aarch64）；后者在同一份包上支持 `lgom://` 与开机自启。
  CI（GitHub Actions）在原生 x64 / arm64 runner 上按架构分别出包，见「持续集成」；
  浏览器扩展 `lgom-extension/` 由 `extension.yml` 出 zip / crx / xpi
  （交接走本地端口，不需要额外安装本地组件）。

## 构建

```sh
go build -o bin/lgdm.exe .   # Windows,本机架构(手工调试用;打包脚本统一产出 bin\lgdm-<arch>.exe)
go build -o bin/lgdm .       # Linux / macOS(手工调试用;Flatpak 脚本产出 bin/lgo_download_manager-<架构>)
```

> **Windows 上要 C 与 C++ 两把编译器**：BT 依赖的 anacrolix/torrent 有两套 uTP
> 实现 —— cgo 下的 `anacrolix/go-libutp`（C++ 源码）与纯 Go 的
> `anacrolix/utp`，由 build tag `disable_libutp` 切换。Windows 上 cgo 必须开
> （Fyne 的 GL 绑定只有 cgo 实现），所以默认走 C++ 那套 —— 本项目**保留 uTP**，
> 不加 `disable_libutp`。于是：
>
> - 编译期要一个 **C++ 编译器**：`g++` 不会从 `CC` 推导
>   （`cmd/go/internal/cfg/zdefaultcc.go` 的 `DefaultCXX` 写死 `"g++"`），
>   只装了 clang / LLVM-MinGW 的机器上会直接报
>   `github.com/anacrolix/go-libutp: exec: "g++": executable file not found in %PATH%`；
>   而且只要依赖里有 `.cpp`，Go 就把 CXX 当作整个二进制的**外部链接器**
>   （`cmd/go/internal/work/gc.go` 的 `ld()`），CXX 必须是真正的 C++ driver，
>   拿 gcc 顶替会在链接阶段失败；
> - 运行期默认会把 C++ 运行时（`libc++.dll` / `libunwind.dll`，或 MinGW 的
>   `libstdc++-6.dll` / `libgcc_s_seh-1.dll` / `libwinpthread-1.dll`）**动态**链进 exe。
>   安装包只装 `lgdm.exe` 一个文件、不带这些 DLL，而 BT 客户端是懒创建的，
>   所以**程序能启动、一开 BT 任务就崩**（进程以 `0xc0000135`
>   STATUS_DLL_NOT_FOUND 退出）。
>
> 打包脚本（`scripts/package.ps1`、`scripts/package_native.ps1`、
> `scripts/build_flatpak*.sh`）已经把这两件事处理掉了：按 `CC` **同目录、
> 同目标**推导 C++ 编译器（`x86_64-w64-mingw32-clang` → `x86_64-w64-mingw32-clang++`，
> 裸 `gcc.exe` → 同目录 `g++.exe`）并显式传给 Go；链接时加
> `-extldflags=-static` 把 C++ 运行时静态进 exe，产物只依赖系统 DLL
> （与 v0.0.4 那个「干净单文件」的基线一致）。

Windows 上**正式发布**的二进制是 GUI 子系统构建（`go build -ldflags "-H windowsgui"`），
从 lgom:// 协议 / 资源管理器拉起时不会闪出黑色控制台窗口；从 cmd / PowerShell
加 `--debug` 启动时由 `internal/logging.AttachParentConsole` 把进程挂回父
终端，日志照常输出；GUI 子系统下 attach 失败（无父 console）则回退到文件
日志（同样 Debug 级别），保证调试信息不丢。

Windows 上要**安装包**（MSI / EXE，x64 与 arm64）用打包脚本，细节见下一节：

```powershell
pwsh -File scripts\package.ps1
```

Linux 上要**安装包**（Flatpak 单文件包，默认出 x86_64 + aarch64）用构建脚本，细节见「Linux 打包（Flatpak 安装包）」：

```sh
./scripts/build_flatpak.sh
```

### 版本元数据

`internal/version` 包提供 `Version` / `Commit` / `Date` 三个变量，默认
是开发期占位符；打包脚本（Windows 的 `scripts/package.ps1` /
`scripts/package_native.ps1`，Linux 的 `scripts/build_flatpak.sh` /
`scripts/build_flatpak_native.sh`）都用 `-ldflags -X` 在链接期把它们覆盖成
`git describe` / `git rev-parse --short HEAD` / `date -u` 的真实结果
（手工 `go build` 同样可以加 `-ldflags`）。启动时 `lgdm` 会在日志中打印
一行 `lgdm <version> (commit <c>, built <d>)`。

## Windows 打包（安装包）

一条命令完成「按架构编译 + 出 MSI / EXE 安装包」：

```powershell
pwsh -File scripts\package.ps1          # x64 + arm64 各一份 MSI 与 EXE
```

只要出**本机架构**的包（CI 的用法）用 `scripts/package_native.ps1 -Arch <x64|arm64>`：
`-Arch` 必须与宿主 CPU 一致（不一致直接报错），不做交叉编译，因此不需要交叉工具链。

### 环境要求

| 依赖 | 说明 |
| --- | --- |
| Windows 10/11（x64 打包机） | 脚本是 PowerShell，Windows PowerShell 5.1 与 PowerShell 7+ 都可用 |
| Go | 版本见 `go.mod` |
| C 工具链 | Fyne 在 Windows 上是 CGO + GLFW/OpenGL，必须有一个 **gcc 风格命令行** 的 C 编译器（GCC 或 clang），见「C 工具链与架构」 |
| 打包工具 | WiX CLI（MSI）、Inno Setup 6（EXE）；缺失时脚本自动获取 |

脚本自动获取的工具（**不依赖 winget**）：

| 工具 | 来源 | 用途 |
| --- | --- | --- |
| WiX Toolset CLI | 官方 .NET 工具包（`dotnet tool install --global wix --version 6.0.2`）→ `%USERPROFILE%\.dotnet\tools\wix.exe` | 编译 `installer/lgdm.wxs` → MSI |
| LLVM-MinGW (UCRT) | 官方 release zip（`llvm-mingw-<release>-ucrt-<aarch64\|x86_64>.zip`），解到 `<repo>\.tools`，校验 SHA256 | 宿主缺可用的 GCC/clang 时的 C 工具链 |
| Inno Setup 6 | 已装则直接用；开发机上缺了才用 winget 装 | 编译 `installer/installer.iss` → EXE |

`-SkipToolInstall` 关闭自动获取；`-WixPath` / `-IsccPath` / `-CC` 可以指向已有的安装。

**为什么不用 winget 取工具**：

1. CI 的 `windows-11-arm` 镜像**根本没带 winget**（实测 `Get-Command winget.exe` 拿不到），
   而同一个镜像预装了 Inno Setup 6.7.1，所以只有 WiX 与 C 工具链要现取。
2. winget 清单里的版本号与上游发布标签**不总是一致**：WiX 在清单里是四段
   （`6.0.2.0`），发布标签是三段（`v6.0.2`），按清单号写死会报
   `No version found matching: 6.0.2`。

**为什么 WiX 不用官方 MSI**：`wix-cli-x64.msi` 只有 x64，而且
`msiexec /a`（管理安装解包）在 CI 上直接以 **1603** 失败（x64 与 arm64 两个 job
都实测过）。NuGet 上的 `wix` 工具包是 **RID 无关**的（`tools/net6.0/any/`，
已核对包内容），arm64 上原样能跑，版本号还与发布标签一致，所以走 `dotnet tool`。

**架构注意**：C 工具链按宿主架构取 LLVM-MinGW 的 zip（aarch64 那份约 181 MB），
解出来的工具名自带三元组（`aarch64-w64-mingw32-clang.exe`），不会像 PATH 上
碰巧存在的 x64 `gcc.exe` 那样用错目标。`wix.exe` 自身的架构不影响产物 ——
MSI 的目标架构由 `wix build -arch` 决定。

### 准备编译环境（winget / scoop）

编译只要有 Go、Git 和一个 gcc 风格的 C 编译器；打包工具（WiX / Inno Setup）
交给 `scripts/package.ps1` 自动获取即可（WiX 按固定 URL 取官方 MSI，
Inno Setup 走 winget），不必手工预置。

**winget**（Windows 10 1809+ 一般自带 App Installer）

```powershell
winget install --id GoLang.Go --exact --silent                       # Go 1.27
winget install --id Git.Git --exact --silent                         # Git(版本号注入用)
winget install --id MartinStorsjo.LLVM-MinGW.UCRT --exact --silent   # clang + mingw-w64 sysroot:x64 与 arm64 都能编

# 打包工具:脚本会按需自动获取,这里只是显式预置的写法。
# WiX 也可以完全不用 winget:scripts/package_native.ps1 直接取官方 MSI(锁 v6.0.2),
# 见「Windows 打包」的「脚本自动获取的工具」。
winget install --id WiXToolset.WiXCLI --version 6.0.2.0 --exact --silent --architecture x64
winget install --id JRSoftware.InnoSetup --exact --silent
```

**scoop**

```powershell
scoop install go git      # go 1.27 / git 2.55
scoop install gcc         # GCC 15.2 + binutils,target = x86_64-w64-mingw32,够 x64 用
```

scoop 侧的边界（都实测过）：

- `mingw`（niXman mingw-builds）也只有 x86_64 / i686，**没有 aarch64 target**；
  arm64 要的 aarch64 工具链只有 LLVM-MinGW 带 —— 用上面的 winget 命令装它，
  或让脚本自己下官方 zip（`scripts/package_native.ps1` 就是这么做的）。
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
  $env:CXX='C:\...\llvm-mingw-<ver>-ucrt-x86_64\bin\aarch64-w64-mingw32-clang++.exe'
  go build -trimpath -ldflags '-extldflags=-static' -o bin\lgdm-arm64.exe .
  ```

  `-CCArm64` 也要给 Windows 绝对路径（`/c/...` 这种会被 Go 拒绝：
  `CC environment variable is relative; must be absolute path`）。
- **C 与 C++ 编译器必须成对提供**：`CXX` 不会从 `CC` 推导（Go 的 `DefaultCXX`
  写死 `"g++"`），而 uTP 的 C++ 源码既要用 CXX 编译、又要让 CXX 当整个二进制
  的外部链接器。脚本按 `CC` 同目录推导 C++ 编译器（`<triplet>-clang` →
  `<triplet>-clang++`，`<triplet>-gcc` → `<triplet>-g++`，裸 `gcc.exe` → 同目录
  `g++.exe`），推导不出来就报错；交叉编译时还会校验目标架构 —— PATH 上的
  `g++` 往往是**宿主**架构的，拿它编目标架构的 C++ 目标文件要到链接阶段才炸
  （CI 的 arm64 job 之前就是这么挂的）。
- **C++ 运行时静态链接**：链接参数带 `-extldflags=-static`，C++ 运行时
  （`libstdc++`/`libc++`、`libgcc`/`libunwind`、winpthread）静态进 exe ——
  安装包只发 `lgdm.exe`，不带那些 DLL（细节见「构建」一节）。
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
| `error WIX7015: You must accept the Open Source Maintenance Fee (OSMF) EULA` | PATH 上的 `wix.exe` 是 v7；装回 v6：`dotnet tool uninstall --global wix` 然后 `dotnet tool install --global wix --version 6.0.2`，或用 `-WixPath` 指向 v6 |
| `No version found matching: 6.0.2` | WiX 在 winget 清单里的版本号是四段（`6.0.2.0`），发布标签才是三段（`v6.0.2`）；写三段永远匹配不到。`scripts/package_native.ps1` 已改为走官方 .NET 工具包（版本号与发布标签一致），不再碰 winget；`scripts/package.ps1` 仍走 winget，它会自己查「主版本 ≤ 6 的最高版本」 |
| `msiexec 解包 WiX CLI 失败,退出码 1603` | `wix-cli-x64.msi` 的管理安装（`msiexec /a`）在 CI 上就是会 1603（x64 与 arm64 都实测过）。改走 `dotnet tool install --global wix --version 6.0.2`（`package_native.ps1` 现在就是这么做的） |
| `缺少 Win...且系统里没有 winget` / `Get-Command winget.exe` 拿不到 | `windows-11-arm` 的 runner 镜像不带 winget。`scripts/package_native.ps1` 取 WiX（dotnet tool）与 LLVM-MinGW（下 zip）已不依赖 winget；Inno Setup 在该镜像里是预装的。若手工指定了 `-SkipToolInstall`，用 `-WixPath` / `-IsccPath` / `-CC` 指路径 |
| `找不到 wix.exe,也没有 dotnet` | 装 .NET SDK（<https://dotnet.microsoft.com/download>），或用 `-WixPath` 指向已有的 `wix.exe` |
| 下载校验失败 `下载校验失败: <url> 期望 SHA256 ...` | 上游 release 资产被替换或 URL 指错。哈希取自 winget 清单，正常情况下与上游一致；确认 URL 后用实际哈希更新脚本里的常量 |
| `error WIX0103: Cannot find the File file ...\bin\lgdm-<arch>.exe` | 该架构的二进制还没编（`-SkipBuild` 时最容易遇到）；去掉 `-SkipBuild` 或先编译 |
| 安装报 `1633 这个处理器类型不支持该安装程序包` | 装了架构不符的包（如把 arm64 包往 x64 上装）；换对应架构的产物 |
| `build constraints exclude all Go files ... go-gl/gl/v3.1/gles2` | cgo 被关掉了（交叉编译时的默认行为）；用脚本编译或手工设 `CGO_ENABLED=1` |
| `go: CC environment variable is relative; must be absolute path` | `CC` 给了 `/c/...` 形式；改成 `C:\...` |
| `cgo: C compiler "C:\\Program" not found: exec: "C:\\Program": ...` | `CC` 指向的路径**带空格**（如 `C:\Program Files\llvm-mingw\...`、`C:\Program Files\LLVM\...`），Go 按空白切词只拿到 `C:\Program`。脚本现在会给带空格的 `CC` 自动加引号（Go 用 `str.SplitQuotedFields` 解析，加引号即可）；手工设 `CC` 时要自己写成 `"C:\Program Files\...\gcc.exe"` |
| `gcc_arm64.S: Error: no such instruction: 'stp x29,x30,[sp,'`（一堆 `stp`/`ldp`/`blr` 报错） | 用的 C 编译器不是 aarch64 目标 —— 这是 **x86 汇编器在读 AArch64 汇编**。`windows-11-arm` 镜像的 PATH 上带着 x64 的 mingw `gcc.exe`，直接拿它配 `GOARCH=arm64` 就会这样。脚本现在用 `-dumpmachine` 校验每个候选编译器，目标不符的会跳过（并打印原因），改用 LLVM-MinGW 的 `aarch64-w64-mingw32-clang` |
| `github.com/anacrolix/go-libutp: exec: "g++": executable file not found in %PATH%` | Go 的默认 C++ 编译器是写死的 `"g++"`、**不从 `CC` 推导**（`cmd/go/internal/cfg/zdefaultcc.go`），而 uTP 是 C++ 源码。装一套 mingw-w64 / LLVM-MinGW（gcc 与 g++ 一起），或手工设 `$env:CXX` 指向与 `CC` 同目录、同目标的 C++ 编译器；打包脚本会自动推导 |
| 装好的程序**一开 BT 任务就退出**（`0xc0000135` STATUS_DLL_NOT_FOUND） | 产物动态依赖了 C/C++ 运行时（`libstdc++-6.dll` / `libc++.dll` 等），而安装包只发 `lgdm.exe` 一个文件。链接要带 `-extldflags=-static`；用 `pwsh -File scripts\check_imports.ps1 -Exe bin\lgdm-x64.exe` 查一下产物（CI 里也会自动查） |
| `-CC 指向的编译器不是 <arch> 目标` | 显式传的 `-CC` / 编译器本身目标不符；错误信息里带 `-dumpmachine` 的实际输出 |
| `winget 安装 ... 返回 -1978335189` | winget 认为已装其它版本；脚本会继续查找已安装的工具，找不到再按提示手动装 |
| 编辑 `scripts/package.ps1` 后 Windows PowerShell 5.1 报语法错误 | 脚本含中文，必须存成 **UTF-8 with BOM**（PS 7 不敏感，PS 5.1 会按 ANSI 读） |

## Linux 打包（Flatpak 安装包）

一条命令完成「按架构编二进制 + 出 Flatpak 安装包」，默认出 **x86_64 与 aarch64** 两份：

```sh
./scripts/build_flatpak.sh                    # x86_64 + aarch64，各一份 .flatpak
./scripts/build_flatpak.sh --arch=x86_64      # 只出本机架构
./scripts/build_flatpak.sh --install          # 出包后把本机架构那份装进用户安装
```

只要出**本机架构**的包（CI 的用法）用 `scripts/build_flatpak_native.sh --arch=<x86_64|aarch64>`：
它不做交叉编译，不需要 qemu / 交叉编译器，`--arch` 与宿主架构不符时在预检阶段直接报错。

### 环境要求

| 依赖 | 说明 |
| --- | --- |
| `flatpak` / `flatpak-builder` | 出包与安装；本机用的是 Flatpak 1.18.4 + flatpak-builder 1.4.12 |
| Go + C 工具链 | 二进制在**宿主机**上按目标架构编（沙箱内未必连得上 Go module proxy）；Fyne 是 cgo |
| 每个架构的 SDK/runtime | `flatpak install --user --arch=<架构> flathub org.freedesktop.Sdk//26.08 org.freedesktop.Platform//26.08`；缺失时脚本会直接打出这条命令（runtime / sdk / 版本号都从 manifest 读，改 manifest 就得跟着改）。**SDK 是构建期的硬依赖**（当 sysroot 用），只有 Platform 装不出包；首次下载约 1 GB，慢网下这一步是主要耗时。发行版自带的 flathub 若是过滤过的（Fedora 就是），非本机架构的 ref 要另加未过滤远端，见「排错」 |
| 跨架构出包 | ① 本机能执行目标架构的构建步骤：`sudo dnf install -y qemu-user-static`（注册 binfmt 后本机即可跑 aarch64 的构建命令；注意 `flatpak --supported-arches` 是静态列表，装了 qemu 也不会变）② 目标架构的 C 交叉编译器：`sudo dnf install -y gcc-aarch64-linux-gnu`，或用 `--cc-aarch64=<路径>` 指定 |

**不需要**在本机装目标架构的 X11/GL/wayland 开发包：编译时用**目标架构的 flatpak SDK**
当 sysroot（生成在 `dist/flatpak/<架构>/sysroot`），那里正好是与运行时配套的完整开发环境
（头文件、库、pkg-config、glibc 全在），链接出来的产物与它要运行的 runtime ABI 一致。

### 命令与参数

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `--arch` | `x86_64,aarch64` | 逗号分隔；接受 `amd64`/`x64`、`arm64`/`aarch64` 别名 |
| `--version` | `git describe --tags --always --dirty` | 写进二进制 `internal/version` 与安装包文件名 |
| `--skip-build` | 关 | 复用已有的 `bin/lgo_download_manager-<架构>`（仍会校验 ELF 架构） |
| `--install` | 关 | 出包后 `flatpak install --user <文件>`（只装本机架构那份） |
| `--cc-x86_64` / `--cc-aarch64` | 自动查找 | 显式指定该架构的 C 编译器（如 `/usr/bin/aarch64-linux-gnu-gcc`） |

### 产物

```
dist/lgdm-<版本>-x86_64.flatpak         # 单文件安装包:flatpak install --user <文件>
dist/lgdm-<版本>-aarch64.flatpak
bin/lgo_download_manager-<架构>          # 打进包里的二进制(打包输入)
dist/flatpak/<架构>/build-dir/           # flatpak-builder 构建目录
dist/flatpak/<架构>/repo/                # 导出仓库（build-bundle 的输入）
dist/flatpak/<架构>/state/               # flatpak-builder 缓存（删掉只是下次变慢）
dist/flatpak/<架构>/sysroot/             # 编译用的 sysroot 视图(指向本架构 SDK)
```

- 每个架构单独一份包：Flatpak bundle 与架构绑定，不能合并。
- manifest 里用一个 `only-arches` 的 module per 架构装对应的二进制，一次
  `flatpak-builder --arch=X` 只会把 X 的那份装进包；缺文件会直接报错，不会串架。
- 二进制与 `dist/`、`bin/` 一样在 `.gitignore` 里；manifest 的每个 module 都用
  `type: file` 精确取文件，不整目录拷贝仓库。
- 装/卸：`flatpak install --user dist/lgdm-<版本>-<架构>.flatpak` /
  `flatpak uninstall --user org.langbiantianya.LGDM`。

### 沙箱内的行为差异

- **开机自启**：见「开机自启」——写的是 `flatpak run org.langbiantianya.LGDM
  --autostart`，入口落在宿主 `~/.config/autostart/`。
- **配置与数据库**：`--filesystem=home` 让沙箱内看到的就是宿主家目录，因此
  `~/.config/lgo_download_manager`（SQLite + 日志）和 `~/.local/share/lgo_download_manager`
  （单实例锁 + URL 转发 socket）与自编译版本共用一份，两者同时启动也只有一个实例。
- **lgom:// 协议**：`.desktop` 的 `MimeType=x-scheme-handler/lgom` 由 Flatpak 导出
  给宿主，浏览器点链接时转给运行中的实例。
- **本地端口通道**：`--share=network` 让沙箱与宿主共用网络命名空间，
  因此扩展按宿主上的 `127.0.0.1:17821` 探测就能发现运行在沙箱里的实例，
  交接路径与自编译版本一致（`handoff.json` 落在沙箱共享的配置目录里）。
- **托盘**：走 `--socket=session-bus`；GNOME 需要 AppIndicator 扩展才会显示托盘图标。
- **路径**：`home` / `host` 授权让沙箱直接看到宿主文件系统，但沙箱里的 `/tmp` 是
  Flatpak 自己挂的私有 tmpfs（`--filesystem=host` 不会覆盖它），所以 manifest 里
  额外授权了 `--filesystem=/tmp`——否则把下载目录填成 `/tmp` 时文件只活在沙箱内，
  应用一退出就没了。

### 排错

| 现象 | 原因 / 处理 |
| --- | --- |
| `本机无法执行 aarch64 的构建步骤` | 缺模拟器：`sudo dnf install -y qemu-user-static`（判据是 `/proc/sys/fs/binfmt_misc/qemu-aarch64` 存在且 enabled） |
| `缺少 aarch64 的构建依赖 runtime/org.freedesktop.Sdk/aarch64/26.08` | 按脚本打印的命令装：`flatpak install --user --arch=aarch64 flathub org.freedesktop.Sdk//26.08 org.freedesktop.Platform//26.08`（本机架构同理，把 `--arch` 去掉；只装 Platform 不够，SDK 才是 sysroot）。若远端只有 Platform / 卡在很慢的下载，可以先装好 SDK 再重跑脚本，脚本不会重复下载已装好的 ref |
| 装 aarch64 ref 报 `未发现用于"flathub"的远程引用` | 发行版自带的 flathub 是**过滤过**的（Fedora 就是），只提供本机架构的 ref：`flatpak remote-add --user --if-not-exists flathub-all https://dl.flathub.org/repo/flathub.flatpakrepo`，再用 `--arch=aarch64 flathub-all` 装 |
| `找不到 aarch64 的 C 交叉编译器` | `sudo dnf install -y gcc-aarch64-linux-gnu`，或 `--cc-aarch64=/usr/bin/aarch64-linux-gnu-gcc` |
| `bin/lgo_download_manager-aarch64 的架构是 …，与目标 aarch64 不符` | `--skip-build` 复用了别的架构的二进制；删掉重编或换成匹配的二进制 |
| `create: 'lgo_download_manager' not found` / `install: cannot stat` | 打包输入不存在：先跑脚本（不要加 `--skip-build`）把 `bin/lgo_download_manager-<架构>` 编出来 |
| 编译报 `bits/wordsize.h: 没有那个文件或目录` | sysroot 视图没建全（`dist/flatpak/<架构>/sysroot` 被删或 SDK 变了）；重跑脚本会重建 |
| 编译报 `ld: 找不到 -latomic_asneeded` / 一串 `undefined reference`（libxcb、libXext、libffi…） | sysroot 视图少了 Fedora gcc 注入的 `-l*_asneeded` 脚本或缺 `-rpath-link`；脚本已处理，出现说明 SDK 布局变了，重跑脚本重建 sysroot |
| appstream 校验报错 | `flatpak/org.langbiantianya.LGDM.metainfo.xml` 改坏了；单独校验：`appstreamcli validate --no-net <文件>` |

### 已验证 / 已知限制

本机（Fedora 44 / Flatpak 1.18.4 / flatpak-builder 1.4.12 /
`qemu-user-static` + `gcc-aarch64-linux-gnu`）实测：

- `./scripts/build_flatpak.sh`（默认两个架构）一条命令出两份包：用目标架构的 flatpak SDK
  当 sysroot 编二进制 → `flatpak-builder --arch=<架构>` → `build-bundle`，产出
  `dist/lgdm-<版本>-x86_64.flatpak`（11 MB）与 `…-aarch64.flatpak`（9.7 MB）；
  AppStream compose 与 `desktop-file-validate` 都不报错。缓存齐了之后一次跑完约 30 秒。
- 编译用的头文件、glibc、X11/GL/wayland 库全部来自目标架构的 SDK（与运行时同源）：
  aarch64 产物在沙箱里 `ldd` 解析到 `/usr/lib/aarch64-linux-gnu/*`、`uname -m` 为 `aarch64`。
- `flatpak install --user <文件>`（脚本的 `--install`）装进用户安装：两份不同架构的 ref
  可以并存于同一个用户安装，`flatpak run` 默认跑本机架构、`flatpak run --arch=aarch64 …`
  跑另一份（本机实测两份都在，默认得 x86_64、加 `--arch=aarch64` 得 aarch64）。
- **两个架构都实测过**沙箱内的开机自启：开关打开 → 宿主
  `~/.config/autostart/lgo_download_manager.desktop` 出现
  `Exec=flatpak run org.langbiantianya.LGDM --autostart` + `X-Flatpak=`；关闭 → 被删除。
  aarch64 那份是在 qemu 下跑的（启动/退出各几秒）。
- `lgom://` 由 Flatpak 导出给宿主，导出的 `.desktop` 里 Exec 被改写成
  `flatpak run … @@u %u @@`（`%u` 作为位置参数直接传给主进程；不要写成
  `--open-url %u`，否则图标点击空 URL 时 `--open-url` 拿不到值会立即退出）。
- 架构守卫：`--skip-build` 传入架构不符的二进制（伪造的 aarch64 ELF）会被直接拒绝，
  不会打进包里。
- **本地端口通道在真实沙箱里实测通过**（打包脚本与 CI 出 26.08 包走的是同一条路径；
  本机这次用的是已装的 `org.gnome.Sdk//50`，因为本机没装 26.08 的 SDK）：
  `--install` 装出的实例（`flatpak run … --no-gui --debug`）在宿主可见的
  `127.0.0.1:17821` 上监听，宿主上的浏览器扩展（Chromium 系与 Firefox 都试过，
  headless + 临时加载构建产物触发真实下载）发起交接后，沙箱内日志出现
  `handoff: request port=17821 origin=chrome-extension://… client=extension`
  与 `handoff accepted`，随即开始下载——全程没落到 `lgom://` 回退。依据是
  `--share=network`：沙箱与宿主共用网络命名空间，所以宿主的回环地址就是沙箱的
  回环地址（见「沙箱内的行为差异」）。
- `./scripts/build_flatpak_native.sh --arch=x86_64`（原生构建，不装 qemu、不用交叉
  编译器）同样一条命令出包，产物 11 MB；`--arch` 与宿主架构不符时在预检阶段直接
  拒绝（实测在 x86_64 上传 `--arch=aarch64` 立即报错退出）。

限制：

- **托盘图标依赖桌面环境**：GNOME 需要 AppIndicator 扩展才能看到托盘菜单。
- **未签名**：产物是单文件 bundle（`flatpak install <文件>` 直接装），没有仓库
  签名/OSTree remote 那套发布流程。
- **aarch64 在 x86_64 上出包需要 qemu**（构建环境要求，与产物无关）：沙箱里的
  `install` / appstream compose 等步骤跑在 qemu 下；在原生 aarch64 机器上不需要 qemu，
  也不需要交叉编译器（`arch == 本机架构` 时脚本走本机 gcc）。

## 持续集成（GitHub Actions）

`.github/workflows/` 下三个工作流：`windows.yml` / `linux.yml` 出桌面端安装包，
按**架构拆成独立 job**，每个 job 在**原生架构**的 runner 上跑对应的原生打包脚本
（不做交叉编译）；`extension.yml` 出浏览器扩展包（独立 JS 工具链，与 Go 构建无关）。

| 工作流 | job | runner | 脚本 | 产物 |
| --- | --- | --- | --- | --- |
| `windows.yml` | `windows-x64` | `windows-latest` | `scripts/package_native.ps1 -Arch x64` | `dist/lgdm-setup-<版本>-x64.{msi,exe}` |
| `windows.yml` | `windows-arm64` | `windows-11-arm` | `scripts/package_native.ps1 -Arch arm64` | `dist/lgdm-setup-<版本>-arm64.{msi,exe}` |
| `linux.yml` | `flatpak-x86_64` | `ubuntu-24.04` | `scripts/build_flatpak_native.sh --arch=x86_64` | `dist/lgdm-<版本>-x86_64.flatpak` |
| `linux.yml` | `flatpak-aarch64` | `ubuntu-24.04-arm` | `scripts/build_flatpak_native.sh --arch=aarch64` | `dist/lgdm-<版本>-aarch64.flatpak` |
| `extension.yml` | `extension-chrome` | `ubuntu-latest` | `npm run package:chrome` | `lgom-extension/dist/lgom-extension-chrome-<版本>.{zip,crx}` |
| `extension.yml` | `extension-firefox` | `ubuntu-latest` | `npm run package:firefox` | `lgom-extension/dist/lgom-extension-firefox-<版本>.{zip,xpi}` |

两个 Windows job 都会先跑一步 **setup C/C++ toolchain (gcc + g++)**：
`scripts/package_native.ps1 -Arch <arch> -PrintToolchain` 按 runner 的宿主架构挑一
套**同架构**的 gcc + g++（`windows-11-arm` 镜像上 PATH 里的 mingw 是 x64 的，
不能用；脚本会改下 LLVM-MinGW 官方 release 的 aarch64 包 —— 那套工具链在
arm64 宿主上跑是**原生编译**，不是交叉工具链），把 `CC` / `CXX`
写进 `GITHUB_ENV` 传给后面的打包步骤，并把版本号打进日志。C++ 编译器为什么
必须显式配见「构建」一节。

触发：push 到 `master`、`v*` tag、PR、手动 `workflow_dispatch`。每个 job 把产物作为
artifact 上传（保留 30 天）；**只有 `v*` tag** 会触发 `release` job，把所有 artifact
汇总发到 GitHub Release（`generate_release_notes`）。扩展包的版本号取 tag 去掉 `v`，
`CHROME_EXTENSION_KEY` / `AMO_API_KEY` / `AMO_API_SECRET` 三个 secret 的语义见
`lgom-extension/README.md` 的「持续集成」。

### 原生脚本与交叉脚本的分工

| | `scripts/package.ps1` / `build_flatpak.sh` | `scripts/package_native.ps1` / `build_flatpak_native.sh` |
| --- | --- | --- |
| 目标架构 | 一次可出多个，可交叉 | 只出宿主架构，`-Arch` / `--arch` 必须与宿主一致 |
| 工具链 | 交叉编译器（LLVM-MinGW 的 `<triplet>-clang`、`gcc-aarch64-linux-gnu`） | 宿主 gcc / clang |
| Linux 额外依赖 | 跨架构时需 `qemu-user-static` | 无 |
| 架构校验 | 有 | 有（不一致时在预检阶段直接报错） |

两者的 SDK sysroot 层相同：**Flatpak 的原生构建同样需要把目标 runtime 的 SDK 当
sysroot**（见 `prepare_toolchain`），因为二进制必须链接它要运行的那个 runtime 的
libc/GL/X11，而不是宿主的同名库。

### Linux job 的 SDK 必须装在**系统**安装里

CI 用 `sudo flatpak remote-add` / `sudo flatpak install` 把 runtime 与 SDK 装进系统安装
（`/var/lib/flatpak`），**不要**改成 `--user`：

- 普通用户没有 `ConfigureRemote` 权限，不带 `sudo` 的 `flatpak remote-add` 会直接报
  `error: Flatpak system operation ConfigureRemote not allowed for user`（CI 第一次跑就是这么挂的）；
- 脚本内部的 `flatpak info` 与 `flatpak-builder` 都**不带** `--user`，默认按系统安装解析
  （`flatpak-builder --system` 是默认值）；SDK 若装在用户安装里，这两处都会找不到。
  要支持用户安装得同时给 `flatpak info` / `flatpak-builder` 加 `--user`，属于另一处改动。

`flathub` 远程用 `dl.flathub.org` 的**未过滤** URL：发行版自带的可能是过滤过的
（Fedora 就是），拿不到 arm64 的 ref。

### Windows arm64 job 的镜像没有 winget

`windows-11-arm` 的镜像**不带 winget**（`Get-Command winget.exe` 拿不到），
`windows-latest` 带。因此 `scripts/package_native.ps1` 取工具**不依赖 winget**：
WiX 走官方 .NET 工具包（`dotnet tool install --global wix`，NuGet 包 RID 无关，
arm64 上原样能跑），LLVM-MinGW 下官方 release zip 并校验 SHA256；只有 Inno Setup
仍然走 winget（两个镜像都预装了它，CI 里根本不会走到那一步）。

镜像里现成的（据 actions/runner-images 的 `Windows11-Arm64-Readme.md`）：
Inno Setup 6.7.1、LLVM 22.1.8、MSYS2、.NET SDK 6–10、Chocolatey 2.7.4。
注意其中那个 LLVM 是 **MSVC 目标的 clang**（`aarch64-pc-windows-msvc`），
脚本会跳过它并改用 LLVM-MinGW —— MSVC ABI 拿不到 mingw 的头/库，也不认
`-l`/`-L`。

代价：arm64 job 每次要下约 181 MB 的 LLVM-MinGW zip（没有做 CI 缓存）。

### 已验证 / 已知限制

- `ubuntu-24.04-arm`、`windows-11-arm` 这两个 arm64 runner **只在公开仓库可用**；
  仓库转私有后 arm64 两个 job 会直接失败，需要重新改回「x64 runner + 交叉编译」。
- `windows-11-arm` 上的 arm64 job 未在 CI 实测过（本地没有 Windows 环境）；
  Windows 两条 job 的首次运行就是它的第一次真实验证。
- Linux 的 `flatpak-x86_64` job 与本地 `--arch=x86_64` 走的是同一条脚本路径，已实测。

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

装了 Flatpak 包的话（见「Linux 打包（Flatpak 安装包）」）：

```sh
flatpak run org.langbiantianya.LGDM                 # 跑已安装的那份(默认本机架构)
flatpak run --arch=aarch64 org.langbiantianya.LGDM  # 已装了 aarch64 那份时指定它
```

## 命令行参数

| 参数        | 默认值          | 说明                                          |
| ----------- | --------------- | --------------------------------------------- |
| `-config`   | 见下            | 运行时目录（SQLite 与日志都落在它里面）       |
| `-no-gui`   | `false`         | 启动时不打开 Fyne GUI                         |
| `-open-url` | `""`            | 一条 `lgom://...` URL，加入队列               |
| `-light`    | `false`         | 强制开启轻量模式（关闭主窗口时释放 widget 树） |
| `-debug`    | `false`         | 把日志写到 stderr 而不是轮转文件（详见「日志」） |
| `-autostart`| `false`         | 由 OS 登录启动项触发:初始只保留调度器 + 托盘，不显示主窗口；之后可从托盘打开，或由扩展请求按需拉起 UI。Flatpak 安装下由注册项里的 `flatpak run … --autostart` 触发 |

`-config` 默认值：Windows 上是 `%LOCALAPPDATA%\lgo_download_manager`（绝对路径
—— 安装后的 lgdm 会被 `lgom://` 协议从任意工作目录拉起，相对路径会因 CWD
不可写而开库失败，托盘实例与协议实例也会落到两份不同的库）；其它平台沿用
XDG 风格的 `~/.config/lgo_download_manager`（`$HOME` 不可用时回退到 `.`）。
SQLite 数据库固定为 `<config>/lgdm.sqlite`。

> 库文件名从 `ldm.sqlite` 改成了 `lgdm.sqlite`（目录名仍是
> `lgo_download_manager`）：如果本地还留着旧的 `ldm.sqlite`，程序不会去读它，
> 需要的话手工改名即可。

也支持以位置参数的形式传入 `lgom://` URL（某些桌面环境会以位置参数方式传递 URL）。

## 日志

所有日志由 `internal/logging` 统一封装，底层是 `log/slog`：

- 默认（非 `--debug`）：写到 `<config>/lgdm.log`，按天轮转成
  `lgdm.log.yyyymmdd`，启动时自动 prune 掉超过 7 天的历史文件。
- `--debug`：写到 stderr，Debug 级别。
- Windows 上二进制是 GUI 子系统构建（`-H windowsgui`），从 cmd / PowerShell
  加 `--debug` 启动时由 `AttachParentConsole(ATTACH_PARENT_PROCESS)`
  把进程挂回父终端，日志直接落到控制台；无父 console（资源管理器/浏览器
  URL 协议拉起）时 attach 失败，回退到 `lgdm.log`（同样 Debug 级别），
  保证调试信息不丢。

业务进程通过环境变量 `LGDM_DEBUG=1` 把 `--debug` 透传给由它自我复刻拉起的
UI 子进程，避免子进程的日志悄悄落到文件里。

## 浏览器扩展接力

`lgom-extension/`（Chrome / Firefox MV3）拦截浏览器下载后，按下表顺序把任务交给
lgdm。首选通道不弹浏览器确认框、也不开标签页，`lgom://` URL 只是最后手段：

| 优先级 | 通道          | 需要什么                      | 能冷启动 lgdm | 确认框 / 标签页 |
| ------ | ------------- | ----------------------------- | ------------- | --------------- |
| 1      | 本地端口      | lgdm 正在运行（托盘常驻即可） | 否            | 无 / 无         |
| 2      | `lgom://` URL | 系统里注册过 `lgom://` 处理器 | 是            | 有 / 复用标签页 |

通道 2 属于外部协议启动，浏览器会在自己的确认对话框后放行（无法在扩展代码里
suppress），但它同时把 lgdm 冷启动起来。通道 1 的短板是要求 lgdm 已经在跑，
所以扩展每次都从头重试整条链：lgdm 没起来时这一步瞬间失败，直接往下走。

### 本地端口

业务进程在 `127.0.0.1` 上监听候选端口中的第一个空闲端口（`17821`、`17822`、
`17823`、`17824`、`17825`；见 `internal/handoff`），扩展并行探测同一份列表来
发现它。`GET /ping` 返回 `{"app":"lgo_download_manager","api":1,"port":…,"version":…}`
作为身份确认，`POST /download` 接收与 URL 协议同样字段的 JSON 正文：

```json
{"url":"https://…","name":"a.zip","ua":"Mozilla/5.0 …","headers":"Referer: …","cookies":"sid=1"}
```

正文走 JSON 而不是查询串，所以不受 URL 长度限制，Cookie / User-Agent 也不会
出现在可被导航、会进历史记录的 URL 里。落地路径与 `lgom://` 完全一致：弹
「新建下载任务」对话框由用户确认；只有显式 `--no-gui` 形态直接 Add + Start
（服务/CLI 语义不变）。`--autostart` 只控制登录启动时是否立即显示主窗口，
之后收到扩展请求仍会按需拉起界面并弹出确认对话框。

`url` 同样接受磁力链接与 `.torrent` 链接（正文是 JSON，不需要 URL 编码），
因此扩展/脚本可以把 BT 任务一并交接给 lgdm。

只监听回环地址、不监听 `0.0.0.0`/`::`。`POST /download` 必须带
`X-LGOM-Client` 头（逼普通网页先走预检），且带 `Origin` 时必须是扩展来源
（`chrome-extension://` / `moz-extension://` / Edge 的 `extension://` /
`safari-web-extension://`），否则 403；不带 `Origin` 的本机调用方
（脚本、CLI）放行。细节与排障见 `lgom-extension/README.md` 的
「本地端口通道」一节。

当前监听端口、pid 与版本写在 `<config>/handoff.json`（`--config` 目录下），
仅供排障与其它本机工具读取；扩展不读它，只探测固定候选端口。
`--debug` 日志会打出监听端口与每条请求的 `origin`。

## URL 协议

`lgom://download?url=<encoded>[&name=<encoded>[&ua=<encoded>[&headers=<encoded>[&cookies=<encoded>]]]]`

只有 `url` 是必填项。URL 由操作系统交给新启动的进程：Windows 安装包注册的命令行是
`"<install>\lgdm.exe" "%1"`；Linux 由 `.desktop` 的 `MimeType=x-scheme-handler/lgom`
注册，Flatpak 版由 Flatpak 把同一份 `.desktop` 导出给宿主（导出的 Exec 会被改写成
`flatpak run … @@u %u @@`，`%u` 作为位置参数直接传给主进程）。没有实例在运行时，这个进程就是主实例，
自己把 URL 入队并下载；已有实例在运行时，新进程通过命名管道（Windows）/
Unix socket（其它平台）把 URL 转发给主实例后退出，转发失败（例如主实例刚好在退出）
以非零状态结束并打印原因。

`url` 不限协议：`https://…`、`ftp://…`、`webdav://…` 之外，`magnet:?xt=urn:btih:…`
（需 URL 编码后放进查询串,例如 `lgom://download?url=magnet%3A%3Fxt%3Durn%3Abtih%3A…`）
与 `.torrent` 链接同样会被识别成 BitTorrent 任务，落到「新建任务」对话框
（显式 `--no-gui` 下则直接 Add + Start；`--autostart` 只在首次启动时静默）。

## 代理

设置对话框的 **HTTP/HTTPS 代理模式** 提供三个选项：

- **使用系统代理**（默认）：优先读取 `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY` /
  `NO_PROXY` 等环境变量；当前协议没有环境代理时，再读取桌面会话设置
  （Linux GNOME/KDE/macOS `scutil`/Windows WinINET）。HTTP 与 HTTPS 会分别选择
  各自的代理端点，系统 bypass / `NO_PROXY` 同时生效。桌面代理快照缓存 30 秒，
  系统设置或应用代理模式变化后无需重启客户端即可重新探测。
- **不使用代理**：始终直连，忽略所有环境变量与系统设置。
- **手动设置代理**：填写完整 URL（支持 `http://`、`https://`、`socks5://`）+ 可选绕过列表
  （逗号分隔主机名、CIDR 或通配符，如 `*.lan`、`192.168.*`、`10.0.0.0/8`）。

PAC / WPAD 自动配置脚本不在支持范围内。

BitTorrent 任务的 tracker 公告、webseed 与 `.torrent` 文件的 HTTP 抓取
同样走这套设置（`internal/protocol.HTTPProxyFunc`）；BT 的 peer 连接不是
HTTP，因此不受这里的代理影响。BT 侧的代理解析函数是动态的，修改代理后
无需重建正在运行的 BT 客户端。

## 开机自启

「设置」里的 **Auto start** 开关启用后，业务主进程会在操作系统登录时
以**静默**方式自启——不显示主窗口，只保留下载调度器与系统托盘；
用户从托盘菜单「显示窗口」恢复 UI 后才能看到任务列表。

各平台的注册位置：

| 平台   | 注册位置                                                                  | 命令/参数 |
| ------ | ------------------------------------------------------------------------- | --------- |
| Windows | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`（无需管理员权限）  | `"<exe>" --autostart` |
| Linux  | `$XDG_CONFIG_HOME/autostart/lgo_download_manager.desktop`（缺省 `~/.config`） | `Exec=<exe> --autostart` + `X-GNOME-Autostart-enabled=true` |
| Linux（Flatpak） | 宿主 `~/.config/autostart/lgo_download_manager.desktop`          | `Exec=flatpak run org.langbiantianya.LGDM --autostart` + `X-Flatpak=org.langbiantianya.LGDM` |
| macOS  | `~/Library/LaunchAgents/org.langbiantianya.LGDM.plist`                    | `ProgramArguments` 数组形式，`RunAtLoad=true`、`ProcessType=Background` |

`--autostart` 由各平台注册项附加，业务进程读到后跳过首次 `uim.Start()`，
保留调度器 + 托盘常驻；之后用户打开托盘或扩展投递下载请求时，仍会按需拉起
UI 并弹出「新建下载任务」对话框。Windows / Linux / macOS 三平台行为对齐。

Flatpak 是 Linux 上的例外：沙箱里的 `/app/bin/lgo_download_manager` 在宿主上
并不存在，注册项必须写成 `flatpak run <app-id> --autostart` 由宿主重新进入
沙箱拉起应用，应用 ID 取自运行时注入的 `FLATPAK_ID`；入口文件也必须落在宿主
`~/.config/autostart`（沙箱里的 `$XDG_CONFIG_HOME` 是应用私有目录
`~/.var/app/<app-id>/config`，写在那里等于没注册）。宿主目录由 manifest 的
`finish-args: --filesystem=xdg-config/autostart` 映射进沙箱。

启动时 `settings.ReconcileAutoStart` 会把持久化的开关与操作系统真实状态
调和：用户在第三方工具（任务管理器「启动」标签、GNOME Tweaks、系统设置）
里手动改过、或者安装/卸载有残留，都以持久化的 AutoStart 为准重新对齐。
注册失败（如 HKCU 权限不足）只记日志、不让 UI 提交整笔失败，便于排障。

Linux 上 `NoDisplay=true` 让 `.desktop` 不出现在应用启动器里，仅作自启用途；
macOS 上 `ProcessType=Background` 标记为后台进程，不进入前台应用的内存
压力管理名单。Windows 上写 HKCU 不需要 UAC，符合「用户态安装」原则。

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
| Disk preallocation  | true             | 在下载前按完整大小预分配 `.part` 空间；关闭后按实际写入增长 |
| Proxy mode          | system           | 系统 / 禁用 / 手动                         |
| Proxy URL           | ""               | 手动模式下的代理 URL                       |
| Proxy bypass        | ""               | 手动模式下的绕过列表（逗号分隔）           |
| Light mode          | true             | 关闭主窗口时释放 widget 树                 |
| Auto start          | false            | 操作系统登录后是否静默自启（详见「开机自启」） |
| BT listen port      | 首次启动自动选择 | BT 客户端监听端口（TCP/uTP）；选择后持久化，0 = 每次启动由系统随机分配 |
| BT max concurrent   | 2                | BT 任务专用的同时下载上限                 |
| BT max seeding      | 2                | 同时上传的做种任务数上限                   |
| BT download limit   | 0（不限）        | BT 客户端全局限速（KiB/s），0 = 不限速     |
| BT upload limit     | 0（不限）        | BT 客户端全局上传限速（KiB/s）             |
| BT max connections  | 50               | 单个种子的最大已连接 peer 数               |
| DHT                 | true             | 启用 DHT（磁力链接没有 tracker 时靠它找 peer） |
| PEX                 | true             | 启用节点交换（与 peer 交换可用节点）       |
| BT seeding          | 2                | 做种分享率：上传量达到下载量的 2 倍后停止；0 = 不做种 |
| Extra trackers      | ""               | 附加 tracker（每行一个），追加到每个 BT 任务 |

每次修改都会立即持久化。

数值项有取值上限（线程数 ≤ 64、分块大小 ≤ 1024 MiB、并发任务数 ≤ 64、
BT 并发/做种数 ≤ 64、BT peer 数 ≤ 1000）：超上限的输入不采纳，保留上一个
有效值。BT 监听端口会在后台探测占用情况（TCP 与 UDP 都要能绑），被别的
程序占着时在输入框下方给出提示——这个故障在客户端启动之前完全看不出来。

「默认保存目录」不允许为空：清空输入框时文本会弹回上一个有效值。空目录
在旧版本里会被误判成「首次运行」，把整套设置重置成默认值。

## HTTP / HTTPS / FTP / WebDAV 下载

下载引擎先探测文件大小和 Range 能力，再决定并发分片还是单流回退：

- HTTP/WebDAV 的 HEAD/`PROPFIND` 探测之后会发一个 1 字节 Range 请求验证真实能力，
  避免只凭 `Accept-Ranges` 头误判；分片响应必须是精确的 `206`，且
  `Content-Range` 与请求区间一致。
- 服务端若忽略 Range 并返回 `200`，不会把完整响应写到中途偏移，而会自动降级为从
  字节 0 开始的单流下载；短响应、错误区间和长度不符会被视为失败或可重试错误。
- HTTP/WebDAV 会持久化 ETag 与 Last-Modified，并在分片请求中发送 `If-Range`；
  资源版本变化时自动丢弃旧偏移并重下，而不是把新旧文件拼接在一起。
- 429、408、425 和临时 `5xx` 会退避重试；服务端提供 `Retry-After` 时优先遵循其
  建议等待时间，探测阶段同样会进行有限重试。
- 暂停、失败和进程异常退出时保留 `.part`；成功下载先 `Sync` 文件，再重命名并同步
  父目录，最后才把数据库状态更新为 `Completed`。删除任务会同时清理正式文件和
  `.part`。
- 同一任务的远端总大小若发生变化，旧分片偏移会失效并自动从头下载，避免新旧内容
  拼接。没有 Range 的来源不会伪造续传位置。
- HTTP 分片下载使用复用的连接池，并以最多 4 路启动；出现网络错误时会先降低并发，
  退避后再逐步恢复，避免故障期间继续放大连接压力。

## BitTorrent 下载

BT 任务与 HTTP 分片任务共用任务列表、状态机与排序，但底层走
`internal/bittorrent`（封装 anacrolix/torrent）：进程内共享一个 BT 客户端
（一个监听端口、一份 DHT 路由表、一份连接池），每个任务只持有自己的种子句柄
与独立的任务目录。BT 客户端是**懒创建**的——没有 BT 任务时不监听端口、
不建 DHT 路由表。

### 怎么加任务

把下面任意一种填进「新建任务」的 URL 一栏（也可以经
`lgom://download?url=<encoded>` 或浏览器扩展的本地端口通道转发过来），
协议按 URL 形态自动识别：

| URL 形态 | 处理方式 |
| -------- | -------- |
| `magnet:?xt=urn:btih:…&dn=…` | 从 DHT / tracker / peer 抓取元数据后下载 |
| `https://…/foo.torrent[?query]` | 先用 HTTP（走代理设置）取种子文件，再下载内容 |
| `/path/to/foo.torrent`、`file:///path/foo.torrent` | 直接读本地种子文件 |

文件大小预览：`.torrent` 能解析出精确的总体积；磁力链接在拿到元数据前
无法预知体积，对话框保持「未知大小」而不是报错，任务会以「下载中 + 未知大小」
的状态等待 peer 提供元数据（期间每 30s 在日志里记一条 `waiting for metadata`，
便于区分「网络/端口不通」与「卡死」）。

### 保存路径 = 任务目录

BT 任务的保存路径是一个**目录**（不是单个文件），默认按磁力链接的 `dn`
参数（没有则用 infohash 前缀）或种子文件名建目录。种子内容落在
`<任务目录>/<种子名>/…` 之下（单文件种子就是 `<任务目录>/<文件名>`）。

- 未完成的文件带 `.part` 后缀，某个文件下载完成后由客户端重命名为正式文件名；
- 「打开文件夹」打开任务目录；「打开文件」在单文件种子下直接打开那个文件，
  多文件种子则打开任务目录；
- **删除任务会连同任务目录一起删除**（含未完成的 `.part` 数据）。删除前会弹出
  确认框（对所有任务生效，不限 BT）。

### 断点续传与恢复

暂停任务会摘除种子（断开所有 peer）；恢复时重新添加同一个种子，已完成的文件
与 `.part` 里的数据都会被识别，只补缺失的部分。磁力链接在首次抓到元数据后会把
info 字典缓存进 `lgdm.sqlite` 的 `bt_metainfo` 表，因此后续恢复不必再依赖
DHT/tracker（离线、tracker 全挂也能续传）。进程异常退出后的冷启动回收
（`ReclaimDownloadingTasks`）对 BT 任务同样生效。

### 做种

默认分享率为 2，同时最多保留 2 条做种任务上传：任务完成后不占用下载并发
槽位，累计上传量达到下载总量的 2 倍后自动停止。超过同时做种上限的任务会
保留种子但暂停上传，等前面的任务达到分享率释放名额后按 FIFO 顺序接替。
把「做种分享率」设为 0 表示下载完成后不做种。删除任务或退出程序时也会
停止对应做种。

### 代理、端口与连通性

- tracker 公告、webseed、种子文件的 HTTP 抓取都走「代理模式」里的同一套设置
  （System / Disabled / Manual + 绕过列表）；BT 的 peer 连接不是 HTTP，
  因此不受 HTTP 代理影响；
- 首次启动会探测一个 TCP/UDP 都可绑定的端口并持久化，后续启动沿用；填 `0`
  则每次启动由系统重新随机分配。要接受入站连接（提升速度）得填一个固定端口
  并在路由器上做映射——lgdm **不会**自动改路由器配置（不启用 UPnP/NAT-PMP）；
- DHT / PEX 默认开启：磁力链接没有 tracker 时几乎完全依赖它们找 peer。

### 「任务详情」：BT 专属信息面板

BT 任务的「任务详情」窗口**不**沿用 HTTP 任务那套通用字段与字节区间马赛克
（BT 没有「字节区间分片」的概念），而是给出一块种子专属的信息面板，字段
组织参照 qBittorrent / aria2；面板底部另有一张按**分片**（piece）画的
热力马赛克图：

- 种子哈希（Infohash）、种子名、来源 URL、保存目录、添加 / 完成时间；
- 状态（下载中 / 做种中 / 已完成 / 会话已结束）、下载与上传速度；
- 总大小、已下载、已上传、分享率；
- 已连接 peer 数、tracker / DHT 报告的节点总数、其中的做种者与下载者；
- 分片大小、分片总数、已完成分片数；
- tracker 地址列表、种子内文件清单（每个文件一行进度条与完成度）。

面板底部还有一张**分片热力图**，把种子全部分片按等宽聚合成桶
（最多 1024 块），每块按「桶内已下载字节的占比」着色：没开始的用输入框
底色，进行中的从主题主色渐变到成功色，下完的用成功色。分片是 BT 真正的
进度单位——每个分片独立校验、独立传输，哪块已经到手与整体百分比无关，
这正是「只显示完整进度」看不出来的东西。桶值跟着 `bt_info` 的快照一起
下发（每桶 1 字节），所以这张图每秒都跟着实际下载进度变化。

面板每秒钟向业务进程取一次实时快照（IPC 方法 `bt_info`），窗口打开期间
持续刷新。这些数据是易变的（peer 数每秒都在变），刻意**不**进
`store.Task`——那是落库的静态快照；BT 任务的句柄由 scheduler 单独登记，
任务暂停 / 结束且不做种时句柄被摘除，面板随之显示「会话已结束」。

### 与 HTTP 任务共用的部分

- 进度、速度、ETA、状态筛选、排序、搜索、并发上限与 FIFO 排队；
- 删除任务前有确认框：删除会连同已下载的数据一起清掉（`.part` 文件也在内），
  手滑点一下就没了；
- 「任务详情」窗口每个任务最多一个：再点一次「详情」按钮会把已经开着的
  那个置前，不会开出第二个一模一样的窗口（BT 面板每秒轮询一次，多开一个
  既重复显示又白费一次 IPC）；不同任务各自一个，互不影响；
- 设置改动：限速立刻生效；监听端口 / DHT / PEX / 连接数上限这类客户端
  构造期参数在没有 BT 任务运行时立即重建客户端生效，否则延后到最后一个
  BT 任务结束（做种中的任务也算「在运行」）。

### 依赖说明

BT 支持引入 `github.com/anacrolix/torrent`。它是纯 Go 实现，但存储层在
CGO 开启时会编入一个 cgo 版 SQLite（用于分片完成状态，与 lgdm 自己用的
`modernc.org/sqlite` 无关）。本项目的正式构建本来就需要 CGO（Fyne 的
GL/GLFW 绑定），所以不额外增加构建依赖；纯 `CGO_ENABLED=0` 的构建本来就
无法产出 GUI 二进制。

## 任务状态字段

| 字段              | 是否持久化 | 说明                                                 |
| ----------------- | ---------- | ---------------------------------------------------- |
| Chunk ranges      | 是         | 引擎实际下发的分片布局 — 分片视图使用                |
| Chunk progress    | 是         | 每个分片自起始的字节偏移量                           |
| Total / downloaded | 是         | 累计字节计数                                         |
| Status            | 是         | `Pending` / `Downloading` / `Paused` / `Completed` / `Failed` / `FileLost` |
| Error message     | 是         | 失败原因                                               |
| bt_file           | 是         | BT 单文件任务的文件相对路径（UI 的「打开文件」用它定位） |

`FileLost` 由周期性文件存在性检查（`uimgr.validate`）与重连触发的检查维护——
目标文件被人为删除后任务会被标记为 `FileLost`，UI 提供「重置 + 重启」入口。
BT 任务的 `SavePath` 是任务目录，检查的是该目录是否存在。

BT 任务的种子元数据（info 字典）存在 `bt_metainfo` 表（`task_id` → `info` BLOB），
与 `tasks` 分表：它变长且可能很大，而 `tasks` 行会在每次进度事件里被序列化后
推给 UI，不能塞进任务结构体。删除任务时同一把锁内级联清理。

## 进程架构

```
              ┌────────────────────────┐
              │ 业务进程（默认）        │
              │  store / scheduler      │
              │  托盘 + urllauncher     │
              │  handoff(本地端口)      │
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
main.go                        # 命令行参数、单实例锁、本地端口服务 + URL 协议处理、退出信号
internal/store/                # SQLite 持久化（任务、设置、分片状态、BT 元数据缓存）
internal/protocol/             # HTTP、HTTPS、FTP、WebDAV 驱动 + 代理 + 系统代理探测
  └── sysproxy/                #   跨平台系统代理检测（linux/darwin/windows/other）
internal/bittorrent/           # BitTorrent 客户端封装（anacrolix/torrent）：共享客户端、
                               #   种子添加/元数据缓存恢复、限速、按任务的存储目录
internal/engine/               # 分片规划、Range 下载、重试
internal/scheduler/            # 单任务生命周期、状态事件、进度刷盘、异步 Start
                               #   （HTTP 分片与 BT 两条下载路径的编排）
internal/prealloc/             # 磁盘预分配辅助
internal/diskstat/            # 磁盘剩余空间查询（无 cgo，可为各平台交叉编译）
internal/urllauncher/          # lgom:// URL 解析、单实例锁、URL 转发
                               #   （Windows 命名管道 / 其它平台 Unix socket）
internal/handoff/              # 浏览器扩展的本地端口通道：127.0.0.1 上的 /ping 与 /download（JSON）
internal/settings/             # 首次运行默认值 / --light 覆盖 / 进程级代理同步 / 开机自启调和
internal/autostart/            # 跨平台开机自启：HKCU Run / XDG autostart / LaunchAgent（含 Flatpak）
internal/logging/              # log/slog 门面：轮转文件 / --debug stderr / Windows console 挂接
internal/ipc/                  # 长度前缀 JSON 帧协议（业务↔UI 共用）
internal/uimgr/                # UI 子进程生命周期与 IPC 会话管理
internal/ui/                   # Fyne 窗口、任务列表、设置对话框、分片视图、系统托盘
internal/tray/                 # fyne.io/systray 业务进程常驻托盘
assets/                        # 图标（安装包快捷方式 / lgom:// DefaultIcon 都用它）
installer/                     # lgdm.wxs（WiX MSI）、installer.iss（Inno Setup EXE）
scripts/package.ps1            # 打包入口：编译 + 出 MSI / EXE 安装包（可交叉）
scripts/package_native.ps1     # 同上，但只出宿主架构的包（CI 用，不做交叉编译）
scripts/check_imports.ps1      # 校验产物没有动态依赖 C/C++ 运行时（安装包只发 lgdm.exe）
scripts/build_flatpak.sh       # 打包入口：编译 + 出 Flatpak 安装包（可交叉）
scripts/build_flatpak_native.sh # 同上，但只出宿主架构的包（CI 用，不做交叉编译）
.github/workflows/             # CI：windows.yml / linux.yml / extension.yml，按架构或平台分 job 出包
lgom-extension/                # 浏览器扩展（Chrome / Firefox，MV3）：独立 npm 工具链，见其 README
                               #   交接顺序：本地端口 → lgom:// URL（最后手段）
org.langbiantianya.LGDM.yml    # Flatpak manifest（app id / runtime / 权限 / 模块）
flatpak/                       # Flatpak 用的 AppStream metainfo
```

### IPC 协议（业务 ↔ UI 子进程）

`internal/ipc` 用长度前缀 JSON 帧（4 字节 big-endian 长度头 + JSON body，
单条消息上限 1 MiB）序列化所有跨进程消息：

- `MsgHello`（UI→业务）：握手 token
- `MsgInit`（业务→UI）：权威设置快照
- `MsgCall`（UI→业务）：`list` / `add` / `start` / `pause` / `delete` / `probe` /
  `bt_info` / `save_settings`，返回 `MsgResult`
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

URL 是磁力链接 / `.torrent` 时对话框会切换语义：标签变成「保存目录」，
默认目录名按 `dn` 参数（没有则 infohash 前缀）或种子文件名推导，并在下方
提示「BT 任务的内容会下载到该目录下」（见「BitTorrent 下载」）。

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

BitTorrent 任务走同一套生命周期：暂停/退出通过取消上下文让进度循环落盘
`Paused` 并摘除种子，退出流程随后调用 `scheduler.CloseBittorrent()` 关掉
BT 客户端（断开所有 peer、停止做种，磁盘数据保留）。做种中的任务不在
`s.jobs` 里（状态已是 `Completed`、不占槽位），由 `CloseBittorrent` 统一收尾。

### 异步 Start 与 UI 线程

`scheduler.Start(id)` 立即返回，不会因为目标主机不可达而阻塞 UI。
实际探测（HTTP HEAD / FTP SIZE 等）放在 `startAsync()` 后台 goroutine 中：
失败时任务被标记为 `Failed`，错误信息持久化并通过事件总线推送给 UI。
所有调度器事件经 `fyne.Do()` 在 Fyne 主线程上应用到 widget，确保线程安全。

### 并发任务数量上限与 FIFO 提升

`MaxConcurrent`（默认 3，可通过「设置」调整）限制同时占用 engine slot
的任务总数；BT 任务还额外受 `BTMaxConcurrent`（默认 2）约束。`scheduler.Start`
在入参任务处于 `Pending` 且全局或 BT 专用上限已满时静默返回 nil——任务在
store 里保持 `Pending`（UI 渲染为「等待中」），不占 slot、不增 wg，等待被
`promotePending` 拉起。

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

`internal/handoff/handoff_test.go` 覆盖浏览器接力的本地端口通道（需要能绑回环端口，
沙箱里跑不了）：来源策略（扩展来源放行，网页 / `null` 来源、缺 `X-LGOM-Client` 头
一律拒绝）、候选端口被占时顺延、全部被占时报错、只绑 `127.0.0.1`（非回环地址连不上）、
请求体校验、handler 错误回传，以及发现文件 `handoff.json` 的内容。

`internal/scheduler/scheduler_test.go` 中 `TestStartIsAsync` 用不可达端口验证：
- `Start()` 在 500ms 内返回
- 任务最终进入 `Failed` 状态且 `ErrorMessage` 非空

`internal/scheduler/scheduler_test.go` 中的 `TestMaxConcurrent_*` 覆盖并发上限：
- `_QueuesNewTasksBeyondCap`：`len(jobs) >= cap` 时 `Start` 静默排队、不占 slot
- `_PromotesFIFOOnCompletion`：slot 释放后按 `created_at` 升序拉起 Pending
- `_RaiseCapPromotesPending`：`SetMaxConcurrent` 调大时立即 promote 积压任务
这些测试用 `blockingServer` 让 HTTP probe 稳定停留在 prepare 阶段，
以便精确断言 cap 触发与 promote 时机。

BitTorrent 相关：

- `internal/bittorrent/bittorrent_test.go` 里的 `TestDownloadFromLoopbackPeer`
  是端到端用例：本机起一个做种端（anacrolix 客户端 + 已就绪的数据目录），
  再用 lgdm 的 `Manager` 下载，断言进度/文件列表、内容一致、`.part` 重命名，
  并检查做种端确实上传了数据（证明是真实的 P2P 传输）。其余用例覆盖
  `.torrent` 解析、重复 infohash 拒绝（`ErrAlreadyAdded`）、缓存元数据恢复。
- `internal/scheduler/bittorrent_test.go` 覆盖设置 → 客户端配置的映射、
  tracker 拆分、默认任务目录名推导、BT/普通任务的删除语义、
  「每个种子文件一块」的分片换算，以及 `flushAll` 不会清空 BT 分片进度
  （回归用例）。

需要绑回环端口的用例（BT 的端到端下载、`internal/handoff`、
`internal/engine` 的部分用例）在无法监听 socket 的环境里会 `t.Skip`，
因此受限沙箱中 `go test ./...` 仍能跑到其余的单元用例。

## 许可证

本项目采用 [Mozilla Public License Version 2.0](./LICENSE)（MPL 2.0）。
完整的许可证文本请参见根目录下的 `LICENSE` 文件。
