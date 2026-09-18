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
  CI（GitHub Actions）在原生 x64 / arm64 runner 上按架构分别出包，见「持续集成」。

## 构建

```sh
go build -o bin/lgdm.exe .        # Windows,本机架构(手工调试用;打包脚本统一产出 bin\lgdm-<arch>.exe)
go build -o bin/lgdm .            # Linux / macOS(手工调试用;Flatpak 脚本产出 bin/lgo_download_manager-<架构>)
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
| `flatpak` / `flatpak-builder` | 出包与安装；本机用的是 Flatpak 1.18 + flatpak-builder 1.4 |
| Go + C 工具链 | 二进制在**宿主机**上按目标架构编（沙箱内未必连得上 Go module proxy）；Fyne 是 cgo |
| 每个架构的 SDK/runtime | `flatpak install --user --arch=<架构> flathub org.gnome.Sdk//50 org.gnome.Platform//50`；缺失时脚本会直接打出这条命令。发行版自带的 flathub 若是过滤过的（Fedora 就是），非本机架构的 ref 要另加未过滤远端，见「排错」 |
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
- **托盘**：走 `--socket=session-bus`；GNOME 需要 AppIndicator 扩展才会显示托盘图标。
- **路径**：`home` / `host` 授权让沙箱直接看到宿主文件系统，但沙箱里的 `/tmp` 是
  Flatpak 自己挂的私有 tmpfs（`--filesystem=host` 不会覆盖它），所以 manifest 里
  额外授权了 `--filesystem=/tmp`——否则把下载目录填成 `/tmp` 时文件只活在沙箱内，
  应用一退出就没了。

### 排错

| 现象 | 原因 / 处理 |
| --- | --- |
| `本机无法执行 aarch64 的构建步骤` | 缺模拟器：`sudo dnf install -y qemu-user-static`（判据是 `/proc/sys/fs/binfmt_misc/qemu-aarch64` 存在且 enabled） |
| `缺少 aarch64 的构建依赖 runtime/org.gnome.Sdk/aarch64/50` | 按脚本打印的命令装：`flatpak install --user --arch=aarch64 flathub org.gnome.Sdk//50 org.gnome.Platform//50` |
| 装 aarch64 ref 报 `未发现用于"flathub"的远程引用` | 发行版自带的 flathub 是**过滤过**的（Fedora 就是），只提供本机架构的 ref：`flatpak remote-add --user --if-not-exists flathub-all https://dl.flathub.org/repo/flathub.flatpakrepo`，再用 `--arch=aarch64 flathub-all` 装 |
| `找不到 aarch64 的 C 交叉编译器` | `sudo dnf install -y gcc-aarch64-linux-gnu`，或 `--cc-aarch64=/usr/bin/aarch64-linux-gnu-gcc` |
| `bin/lgo_download_manager-aarch64 的架构是 …，与目标 aarch64 不符` | `--skip-build` 复用了别的架构的二进制；删掉重编或换成匹配的二进制 |
| `create: 'lgo_download_manager' not found` / `install: cannot stat` | 打包输入不存在：先跑脚本（不要加 `--skip-build`）把 `bin/lgo_download_manager-<架构>` 编出来 |
| 编译报 `bits/wordsize.h: 没有那个文件或目录` | sysroot 视图没建全（`dist/flatpak/<架构>/sysroot` 被删或 SDK 变了）；重跑脚本会重建 |
| 编译报 `ld: 找不到 -latomic_asneeded` / 一串 `undefined reference`（libxcb、libXext、libffi…） | sysroot 视图少了 Fedora gcc 注入的 `-l*_asneeded` 脚本或缺 `-rpath-link`；脚本已处理，出现说明 SDK 布局变了，重跑脚本重建 sysroot |
| appstream 校验报错 | `flatpak/org.langbiantianya.LGDM.metainfo.xml` 改坏了；单独校验：`appstreamcli validate --no-net <文件>` |

### 已验证 / 已知限制

本机（Fedora 44 / Flatpak 1.18.2 / flatpak-builder 1.4.10 / GNOME 50 runtime /
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

`.github/workflows/` 下两个工作流，按**架构拆成独立 job**，每个 job 在**原生架构**
的 runner 上跑对应的原生打包脚本（不做交叉编译）：

| 工作流 | job | runner | 脚本 | 产物 |
| --- | --- | --- | --- | --- |
| `windows.yml` | `windows-x64` | `windows-latest` | `scripts/package_native.ps1 -Arch x64` | `dist/lgdm-setup-<版本>-x64.{msi,exe}` |
| `windows.yml` | `windows-arm64` | `windows-11-arm` | `scripts/package_native.ps1 -Arch arm64` | `dist/lgdm-setup-<版本>-arm64.{msi,exe}` |
| `linux.yml` | `flatpak-x86_64` | `ubuntu-24.04` | `scripts/build_flatpak_native.sh --arch=x86_64` | `dist/lgdm-<版本>-x86_64.flatpak` |
| `linux.yml` | `flatpak-aarch64` | `ubuntu-24.04-arm` | `scripts/build_flatpak_native.sh --arch=aarch64` | `dist/lgdm-<版本>-aarch64.flatpak` |

触发：push 到 `master`、`v*` tag、PR、手动 `workflow_dispatch`。每个 job 把产物作为
artifact 上传（保留 30 天）；**只有 `v*` tag** 会触发 `release` job，把四个 artifact
汇总发到 GitHub Release（`generate_release_notes`）。

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
| `-autostart`| `false`         | 由 OS 登录启动项触发:静默拉起(只保留调度器 + 托盘,不显示主窗口)。Flatpak 安装下由注册项里的 `flatpak run … --autostart` 触发 |

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

## URL 协议

`lgom://download?url=<encoded>[&name=<encoded>[&ua=<encoded>[&headers=<encoded>[&cookies=<encoded>]]]]`

只有 `url` 是必填项。URL 由操作系统交给新启动的进程：Windows 安装包注册的命令行是
`"<install>\lgdm.exe" "%1"`；Linux 由 `.desktop` 的 `MimeType=x-scheme-handler/lgom`
注册，Flatpak 版由 Flatpak 把同一份 `.desktop` 导出给宿主（导出的 Exec 会被改写成
`flatpak run … @@u %u @@`，`%u` 作为位置参数直接传给主进程）。没有实例在运行时，这个进程就是主实例，
自己把 URL 入队并下载；已有实例在运行时，新进程通过命名管道（Windows）/
Unix socket（其它平台）把 URL 转发给主实例后退出，转发失败（例如主实例刚好在退出）
以非零状态结束并打印原因。

## 代理

设置对话框的 **HTTP/HTTPS 代理模式** 提供三个选项：

- **使用系统代理**（默认）：进程级缓存优先读取 `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY` 等环境变量，
  未命中则调用平台原生 API 探测（Linux GNOME/KDE/macOS `scutil`/Windows WinINET）。
  切换选项会自动让缓存失效，下次请求重新探测。
- **不使用代理**：始终直连，忽略所有环境变量与系统设置。
- **手动设置代理**：填写完整 URL（支持 `http://`、`https://`、`socks5://`）+ 可选绕过列表
  （逗号分隔主机名，glob 模式 `*.lan`、`192.168.*`）。

PAC / WPAD 自动配置脚本不在支持范围内。

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

`--autostart` 由各平台注册项附加，业务进程读到后跳过 `uim.Start()`，
保留调度器 + 托盘常驻。Windows / Linux / macOS 三平台行为对齐。

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
| Disk preallocation  | true             | 在下载前按完整大小预分配磁盘空间           |
| Proxy mode          | system           | 系统 / 禁用 / 手动                         |
| Proxy URL           | ""               | 手动模式下的代理 URL                       |
| Proxy bypass        | ""               | 手动模式下的绕过列表（逗号分隔）           |
| Light mode          | true             | 关闭主窗口时释放 widget 树                 |
| Auto start          | false            | 操作系统登录后是否静默自启（详见「开机自启」） |

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
scripts/build_flatpak.sh       # 打包入口：编译 + 出 Flatpak 安装包（可交叉）
scripts/build_flatpak_native.sh # 同上，但只出宿主架构的包（CI 用，不做交叉编译）
.github/workflows/             # CI：windows.yml / linux.yml，按架构分 job 出包
org.langbiantianya.LGDM.yml    # Flatpak manifest（app id / runtime / 权限 / 模块）
flatpak/                       # Flatpak 用的 AppStream metainfo
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