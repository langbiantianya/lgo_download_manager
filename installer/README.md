# Windows 安装包

一条命令完成「按架构编译 + 生成 MSI / EXE」：

```powershell
pwsh -File scripts\package.ps1          # x64 + arm64，各一份 MSI 与 EXE
```

只生成宿主架构的包（CI 用法）：

```powershell
pwsh -File scripts\package_native.ps1 -Arch x64
pwsh -File scripts\package_native.ps1 -Arch arm64
```

`package_native.ps1` 的 `-Arch` 必须与宿主 CPU 一致，不做交叉编译，因此不需要交叉
工具链。

## 环境要求

| 依赖 | 说明 |
| --- | --- |
| Windows 10/11 x64 打包机 | PowerShell 5.1 与 PowerShell 7+ 都可用 |
| Go | 版本见根目录 `go.mod` |
| C 工具链 | Fyne 在 Windows 上使用 CGO + GLFW/OpenGL，必须提供 gcc 风格的 C 编译器 |
| 打包工具 | WiX CLI（MSI）、Inno Setup 6（EXE），缺失时可自动获取 |

脚本自动获取的工具：

| 工具 | 来源 | 用途 |
| --- | --- | --- |
| WiX Toolset CLI | .NET 工具包，`dotnet tool install --global wix --version 6.0.2` | 编译 `installer/lgdm.wxs` |
| LLVM-MinGW UCRT | 官方 release zip，解压到仓库 `.tools` 并校验 SHA256 | 无可用 GCC/clang 时的 C 工具链 |
| Inno Setup 6 | 已安装则直接使用；开发机缺失时通过 winget 安装 | 编译 `installer/installer.iss` |

`-SkipToolInstall` 关闭自动获取，`-WixPath`、`-IsccPath`、`-CCX64`、
`-CCArm64` 可指定已有工具。

### 为什么自动获取不依赖 winget

`windows-11-arm` runner 镜像没有 winget，而同一镜像预装了 Inno Setup。WiX 通过
RID 无关的 .NET 工具包安装；LLVM-MinGW 下载官方 zip。WiX 官方 MSI 只有 x64，
而且 CI 上的 `msiexec /a` 会以 1603 失败，因此不使用官方 MSI 解包。

winget 清单中的 WiX 版本是四段（`6.0.2.0`），发布标签是三段（`v6.0.2`），不能
按发布标签写死 winget 查询。原生脚本使用 .NET 工具包，避免这个差异。

## 准备编译环境

编译最低需要 Go、Git 和一个 gcc 风格的 C 编译器。打包工具可由脚本自动获取。

### winget

```powershell
winget install --id GoLang.Go --exact --silent
winget install --id Git.Git --exact --silent
winget install --id MartinStorsjo.LLVM-MinGW.UCRT --exact --silent

# 可选：预先安装打包工具
winget install --id WiXToolset.WiXCLI --version 6.0.2.0 --exact --silent --architecture x64
winget install --id JRSoftware.InnoSetup --exact --silent
```

### scoop

```powershell
scoop install go git
scoop install gcc
```

scoop 的 `mingw` 只有 x86_64 / i686，没有 aarch64 target。arm64 请使用
LLVM-MinGW。scoop main bucket 的 `wixtoolset` 是 v7，项目脚本会拒绝，Inno Setup
也不在 main bucket。

新装工具通常要重开终端才会进入 `PATH`。如果 scoop shim 被 Powershell 阻止，可对
当前用户执行：

```powershell
Set-ExecutionPolicy -Scope CurrentUser RemoteSigned
```

## 命令与参数

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `-Format` | `both` | `msi`、`exe` 或 `both` |
| `-Arch` | `x64,arm64` | 逗号分隔，接受 `x64`/`amd64`、`arm64`/`aarch64` |
| `-Version` | `git describe --tags --always --dirty` | 写入二进制与包文件名 |
| `-WinVersion` | 从 `-Version` 推导 | MSI `ProductVersion` / `VersionInfoVersion` 的 `X.Y.Z.W` |
| `-OutputDir` | `<repo>\dist` | 产物目录 |
| `-SkipBuild` | 关 | 复用 `bin\lgdm-<arch>.exe` 与 `bin\lgdm-ui-<arch>.exe`，仍校验 PE 架构 |
| `-SkipToolInstall` | 关 | 工具缺失时直接报错 |
| `-WixPath` / `-IsccPath` | 自动查找 | 指定 `wix.exe` / `ISCC.exe` |
| `-CCX64` / `-CCArm64` | 自动查找 | 指定对应架构的 C 编译器 |

示例：

```powershell
pwsh -File scripts\package.ps1 -Format msi
pwsh -File scripts\package.ps1 -Format exe -Arch arm64
pwsh -File scripts\package.ps1 -SkipBuild
pwsh -File scripts\package.ps1 -Version v1.0.0 -WinVersion 1.0.0.0
```

## 产物

```text
dist\lgdm-setup-<版本>-x64.msi       dist\lgdm-setup-<版本>-arm64.msi
dist\lgdm-setup-<版本>-x64.exe       dist\lgdm-setup-<版本>-arm64.exe
bin\lgdm-x64.exe                     bin\lgdm-arm64.exe
bin\lgdm-ui-x64.exe                  bin\lgdm-ui-arm64.exe
```

MSI 一个包只能承载一种架构，因此每个架构单独出包；同一架构的 MSI 与 EXE 功能对等，
任选一个即可。安装后分别命名为 `lgdm.exe` 与同目录的 `lgdm-ui.exe`：前者承载
业务与托盘，后者只承载 Fyne UI。`dist/`、`bin/` 和中间产物 `*.wixpdb` 已被
Git 忽略。

## C/C++ 工具链

Windows 上 Fyne 必须开启 cgo，BT 依赖的 anacrolix/torrent 在 cgo 下还包含两套 uTP
实现。默认使用 `anacrolix/go-libutp` 的 C++ 源码，由 build tag
`disable_libutp` 切换。项目保留 uTP，不添加这个 tag。

由此有两条硬要求：

1. 需要真正的 C++ 编译器。Go 不会从 `CC` 推导 `CXX`，默认 CXX 是 `g++`。只有
   clang / LLVM-MinGW 而没有 `g++` 时会报 executable not found。
2. C++ 运行时必须静态链接。默认动态链接 `libc++.dll`、`libunwind.dll` 或 MinGW 的
   `libstdc++-6.dll`、`libgcc_s_seh-1.dll`、`libwinpthread-1.dll`，安装包不带
   这些 DLL；业务程序能启动，但一开 BT 就会以 `0xc0000135` 退出。

打包脚本会按 `CC` 同目录、同目标推导 C++ 编译器，例如
`x86_64-w64-mingw32-clang` → `x86_64-w64-mingw32-clang++`，裸 `gcc.exe` →
同目录 `g++.exe`，链接时添加 `-extldflags=-static`。

MSVC 的 `cl.exe` 不能作为 `CC`，因为 cgo 使用 gcc 参数。clang 可用，推荐
LLVM-MinGW；其一个包同时提供 x86_64 与 aarch64 的 mingw 目标。

交叉编译时 Go 会默认关闭 cgo，而 Fyne 需要 cgo。脚本会设置 `CGO_ENABLED=1`。
手工 arm64 构建示例：

```powershell
$env:GOARCH='arm64'
$env:CGO_ENABLED='1'
$env:CC='C:\...\llvm-mingw-<ver>-ucrt-x86_64\bin\aarch64-w64-mingw32-clang.exe'
$env:CXX='C:\...\llvm-mingw-<ver>-ucrt-x86_64\bin\aarch64-w64-mingw32-clang++.exe'
go build -trimpath -ldflags '-extldflags=-static' -o bin\lgdm-arm64.exe .
go build -trimpath -ldflags '-extldflags=-static' -o bin\lgdm-ui-arm64.exe ./cmd/lgdm-ui
```

Windows 绝对路径不能写成 `/c/...`，否则 Go 会报 `CC environment variable is
relative; must be absolute path`。路径带空格时脚本会自动加引号；手工设置时要写成
`"C:\Program Files\...\gcc.exe"`。

出包前脚本读取 PE 头 `Machine` 字段校验架构：x64 为 `0x8664`，arm64 为 `0xAA64`，
防止 `-SkipBuild` 复用错误架构的二进制。非 Windows 主机交叉编译 Windows 二进制
还需要 mingw-w64。

## 安装、升级与卸载

- 用户态安装到 `%LOCALAPPDATA%\Programs\lgo_download_manager`，不需要管理员权限，
  不安装服务，不改 PATH。MSI 使用 `Scope="perUser"`，EXE 固定使用 HKCU。
- 注册 `HKCU\Software\Classes\lgom`，命令行是
  `"<install>\lgdm.exe" "%1"`，`DefaultIcon` 指向 `lgdm.ico`。
- 同时安装同目录的 `lgdm-ui.exe`；业务进程优先拉起它，缺失时才使用自我复刻回退。
- 创建开始菜单快捷方式；EXE 安装包还提供桌面快捷方式选项，MSI 直接创建。
- 安装、升级、卸载前结束正在运行的 `lgdm.exe` 与 `lgdm-ui.exe`。
- 升级保留 `%LOCALAPPDATA%\lgo_download_manager` 中的数据库、日志和 socket；
  卸载会删除安装目录、协议键、快捷方式以及该用户数据目录。

静默安装和卸载：

```powershell
msiexec /i dist\lgdm-setup-v1.0.0-x64.msi /qn
.\dist\lgdm-setup-v1.0.0-x64.exe /VERYSILENT /SUPPRESSMSGBOXES /NORESTART
msiexec /x {ProductCode} /qn
& "$env:LOCALAPPDATA\Programs\lgo_download_manager\unins000.exe" /VERYSILENT
```

安装完成后，浏览器或资源管理器中的 `lgom://download?url=...` 会启动 lgdm 并加入
队列。已有托盘实例时，新进程通过命名管道转交 URL 后退出。

## 已验证与限制

在 x64 Windows 上已经验证：

- x64 MSI/EXE 静默安装、从 `C:\Windows\System32` 冷启动 `lgom://`、运行中转发、
  卸载清理安装目录/数据目录/注册表/快捷方式/ARP，以及同版本覆盖安装保留任务库。
- arm64 MSI 的 `Template=Arm64`，载荷 PE `machine=0xAA64`；在 x64 上安装被 1633
  正确拒绝。
- `-Version v9.9.9 -WinVersion 9.9.9.4` 可把文件版本写入 EXE 和 MSI。

限制：

- arm64 包只验证了架构与拒绝错误架构，尚未在 Windows on ARM 真机运行。
- 产物未签名，首次运行可能触发 SmartScreen。需要签名时给 ISCC 传 `/S`，给
  `wix build` 传 `-sign`。
- WiX v7 要求接受 OSMF EULA，本项目只使用 v6/v5。
- Inno Setup 架构标识没有 `arm64compatible`，只有 `arm64` / `x64compatible` 等。

## 排错

| 现象 | 原因 / 处理 |
| --- | --- |
| `WIX7015 ... OSMF EULA` | PATH 上是 WiX v7；安装 v6 或用 `-WixPath` 指向 v6 |
| `No version found matching: 6.0.2` | winget 使用四段版本号；原生脚本已改用 .NET 工具包 |
| `msiexec 解包 WiX CLI 失败,退出码 1603` | 官方 MSI 管理安装失败；使用 `dotnet tool` |
| `缺少 Win...且系统里没有 winget` | 使用 `package_native.ps1`，它不依赖 winget；也可手工指定工具路径 |
| `找不到 wix.exe,也没有 dotnet` | 安装 .NET SDK，或用 `-WixPath` |
| `下载校验失败 ... 期望 SHA256` | 上游资产或 URL 改变；确认后更新脚本常量 |
| `WIX0103: Cannot find ... bin\lgdm{,-ui}-<arch>.exe` | 对应架构的业务或 UI 程序未构建；去掉 `-SkipBuild` 或先构建 |
| 安装报 1633 | 架构不匹配；改用对应架构产物 |
| `build constraints exclude all Go files ... gles2` | cgo 被关闭；使用脚本或设置 `CGO_ENABLED=1` |
| `CC environment variable is relative` | 改用 `C:\...` 绝对路径 |
| `cgo: C compiler "C:\\Program" not found` | `CC` 路径带空格且未正确引用 |
| `gcc_arm64.S ... stp/ldp` 报错 | 用 x86 汇编器处理 AArch64；使用 aarch64 LLVM-MinGW |
| `exec: "g++": executable file not found` | 配置同目录、同目标的 C++ 编译器 |
| 一开 BT 任务就 `0xc0000135` | 产物动态依赖 C++ 运行时；使用 `-extldflags=-static` |
| `-CC 指向的编译器不是 <arch> 目标` | 检查 `-dumpmachine` 和对应架构工具链 |
| winget 返回 `-1978335189` | 已有其它版本；脚本会继续查找已安装工具 |
| 编辑 `package.ps1` 后 PowerShell 5.1 语法错误 | 文件含中文，保存为 UTF-8 with BOM |

检查动态依赖：

```powershell
pwsh -File scripts\check_imports.ps1 -Exe bin\lgdm-x64.exe
```

传入 `lgdm-<arch>.exe` 时，脚本会同时检查 `lgdm-ui-<arch>.exe`。
