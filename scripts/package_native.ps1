<#
.SYNOPSIS
    lgo_download_manager (lgdm) 的**原生架构**打包脚本:在当前机器自己的架构上
    编译并出 Windows 安装包(MSI / EXE),不做任何交叉编译。

.DESCRIPTION
    与 scripts\package.ps1 的区别 —— 后者按需交叉编译(x64 上出 arm64 包),
    本脚本只出**当前 CPU 架构**的包,并要求 -Arch 与宿主架构一致:

        pwsh -File scripts\package_native.ps1 -Arch arm64    # 只能在 arm64 Windows 上跑

    为什么单独一份给 CI 用:
      - GitHub Actions 的 windows-latest 是原生 amd64、windows-11-arm 是原生
        arm64。GOARCH == 宿主 CPU 时 cgo 不需要任何交叉工具链,Go 直接用本机
        gcc;省掉 LLVM-MinGW 的 target 选择与 sysroot 切换。
      - 交叉编译的失败模式(工具链 target 传错、PE 架构与载荷不一致)在这里
        从根上不存在,脚本因此更短、更少分支。
      - 架构仍然显式传入并在运行时校验:runner 被贴错标签(或本地想在 x64 上
        执行 -Arch arm64)会立刻报错,而不是安静地出一个跑不起来的包。

    依赖(缺失时自动获取,-SkipToolInstall 可关闭):
      - MSI: WiX Toolset CLI 6.x —— 按固定 URL 取官方 MSI 并用管理安装解出
        wix.exe(v7 起二进制发布物要求在遵守 OSMF EULA 的前提下使用,本项目不引入)。
      - EXE: Inno Setup 6 —— CI 的 windows-latest / windows-11-arm 镜像都预装了,
        开发机上缺了才用 winget 装。
      - C 编译器:cgo 需要 gcc 风格命令行(GCC 或 clang;**MSVC 的 cl.exe 不可用**,
        Go 从未支持 MSVC)。宿主缺可用的 GCC/clang 时脚本会取 LLVM-MinGW 的
        官方 release zip(aarch64 / x86_64 两份),它自带的 <triplet>-clang 在
        **同架构**宿主上跑就是原生编译,不是交叉编译。
      获取工具**不依赖 winget**:CI 的 windows-11-arm 镜像没有 winget,而且
      winget 清单里的版本号与上游发布标签并不总是一致。

.PARAMETER Arch
    目标架构,必须与当前宿主架构一致:x64 / arm64(也接受 amd64 / aarch64)。
    CI 里显式传,用来把「runner 架构」这件事钉死。

.PARAMETER Format
    打包格式:msi / exe / both(默认 both)。

.PARAMETER Version
    覆盖版本号(默认 `git describe --tags --always --dirty`)。

.PARAMETER WinVersion
    覆盖 MSI ProductVersion / VersionInfoVersion 用的 X.Y.Z.W 数字版本。

.PARAMETER OutputDir
    安装包输出目录,默认 <repo>\dist。

.PARAMETER SkipBuild
    跳过 go build,直接使用已有的 bin\lgdm-<arch>.exe(仍校验 PE 架构)。

.PARAMETER SkipToolInstall
    工具缺失时直接报错,不尝试自动获取(下载 / winget)。

.PARAMETER WixPath
    显式指定 wix.exe。

.PARAMETER IsccPath
    显式指定 ISCC.exe。

.PARAMETER CC
    显式指定 C 编译器(默认按 gcc → clang → LLVM-MinGW 的宿主 triplet 解析)。

.EXAMPLE
    pwsh -File scripts\package_native.ps1 -Arch x64

.EXAMPLE
    pwsh -File scripts\package_native.ps1 -Arch arm64 -Format msi -Version v1.0.0
#>
#Requires -Version 5.1
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Arch,

    [ValidateSet('msi', 'exe', 'both')]
    [string]$Format = 'both',

    [string]$Version,
    [string]$WinVersion,
    [string]$OutputDir,
    [switch]$SkipBuild,
    [switch]$SkipToolInstall,
    [string]$WixPath,
    [string]$IsccPath,
    [string]$CC
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$RepoRoot = Split-Path -Parent $PSScriptRoot
if (-not $OutputDir) { $OutputDir = Join-Path $RepoRoot 'dist' }

$BinDir = Join-Path $RepoRoot 'bin'
$IconPath = Join-Path $RepoRoot 'assets\lgdm.ico'
$WxsPath = Join-Path $RepoRoot 'installer\lgdm.wxs'
$IssPath = Join-Path $RepoRoot 'installer\installer.iss'

# 不带 OSMF EULA 的最后一个 WiX 大版本;v7 起二进制发布物要求在遵守 OSMF EULA
# 的前提下使用(见 wixtoolset/wix v7.0.0 release notes),本项目不引入该依赖。
$MaxFreeWixMajor = 6

# 工具获取**不依赖 winget**。两个原因:
#   1. CI 的 windows-11-arm 镜像根本没带 winget(实测 Get-Command winget.exe 找不到);
#   2. winget 清单里的版本号与上游发布标签不总是一致 —— WiX 在清单里是四段
#      (6.0.2.0),发布标签是三段(v6.0.2),写错就报 "No version found matching"。
# 所以按固定 URL 取上游发布物并校验 SHA256(哈希取自 winget 清单,与它指向的是
# 同一个 release)。找不到本地安装时才下载。
#
# WiX CLI MSI 只有 x64(上游 v6/v7 都只发 wix-cli-x64.msi);arm64 上走 x64 模拟层,
# 不影响产物 —— MSI 的目标架构由 `wix build -arch` 决定,与 wix.exe 自身架构无关。
$WixCliVersion = '6.0.2'
$WixCliMsiUrl = "https://github.com/wixtoolset/wix/releases/download/v$WixCliVersion/wix-cli-x64.msi"
$WixCliMsiSha256 = 'A8A5CC7443353CEF3AB900C60CD7A3A5EE601746319D104AC7B12AD0CED2345C'

# LLVM-MinGW:release 标签 + 各架构 zip 的哈希。zip 内目录名与工具名都带
# 架构三元组(aarch64-w64-mingw32-clang.exe),不会拿错目标。
$LlmMingwRelease = '20260616'
$LlmMingwSha256 = @{
    aarch64 = '312593669435BD0BFC1A43AC3FBA23C8B27E0610BADE88B2738E5A01702A99BA'
    x86_64  = 'B9B68A4D276E16FA25802AABA458E4638F64B3884C290AACCDC2D87083B6CA35'
}

# Inno Setup 只在开发机上需要装(CI 的两个 Windows 镜像都预装了 6.7.1)。
$InnoWingetId = 'JRSoftware.InnoSetup'

$PeMachineX64 = 0x8664
$PeMachineArm64 = 0xAA64

function Write-Step([string]$Message) {
    Write-Host ''
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Write-Note([string]$Message) {
    Write-Host "    $Message" -ForegroundColor DarkGray
}

function Invoke-Native([string]$FilePath, [string[]]$Arguments, [string]$WorkingDirectory) {
    Write-Note (("{0} {1}" -f $FilePath, ($Arguments -join ' ')).Trim())
    $cwd = (Get-Location).Path
    try {
        if ($WorkingDirectory) { Set-Location -LiteralPath $WorkingDirectory }
        # | Out-Host:否则子进程 stdout 会混进调用方的返回值里。
        & $FilePath @Arguments | Out-Host
        $code = $LASTEXITCODE
    } finally {
        Set-Location -LiteralPath $cwd
    }
    if ($code -ne 0) {
        throw ("{0} 退出码为 {1}" -f (Split-Path -Leaf $FilePath), $code)
    }
}

# ---------------------------------------------------------------------------
# 架构:只认宿主架构
# ---------------------------------------------------------------------------

# 宿主 CPU 架构。用 RuntimeInformation 而不是 $env:PROCESSOR_ARCHITECTURE:
# 后者在 ARM64 上以 x64 模拟态运行 PowerShell 时会报 AMD64,正好把「是不是
# 原生 arm64」这个判断搞反。
function Get-HostSlug {
    $osArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    switch ("$osArch") {
        'X64'   { return 'x64' }
        'Arm64' { return 'arm64' }
        default { throw "不支持的宿主架构: $osArch(本脚本只支持 x64 / arm64)" }
    }
}

function ConvertTo-Slug([string]$Value) {
    switch ($Value.Trim().ToLowerInvariant()) {
        'x64'     { return 'x64' }
        'amd64'   { return 'x64' }
        'arm64'   { return 'arm64' }
        'aarch64' { return 'arm64' }
        default   { throw "不支持的架构「$Value」;可用:x64、arm64(也接受 amd64/aarch64)" }
    }
}

$hostSlug = Get-HostSlug
$slug = ConvertTo-Slug $Arch
if ($slug -ne $hostSlug) {
    throw @"
-Arch $slug 与当前宿主架构 $hostSlug 不符:本脚本是原生构建脚本,不做交叉编译。
      在 $hostSlug 机器上请用 -Arch $hostSlug,或到 $slug 机器 / runner 上执行。
      (要交叉出包请用 scripts\package.ps1。)
"@
}

if ($slug -eq 'arm64') {
    $goarch = 'arm64'
    $wixArch = 'arm64'
    $peMachine = $PeMachineArm64
    $exePath = Join-Path $BinDir 'lgdm-arm64.exe'
    # 宿主(arm64)的 mingw 目标三元组前缀;确认过的目标架构由它写死在名字里。
    $mingwPrefix = 'aarch64-w64-mingw32'
    # 必须是 mingw-w64 / GNU 目标的编译器:Fyne 的 cgo 依赖走 gcc 风格命令行,
    # Windows 上 MSVC 目标的 clang(LLVM 官方 Windows 版不带 -target 时默认就是
    # x86_64-pc-windows-msvc / aarch64-pc-windows-msvc)拿不到 mingw 的头与库,
    # 也不认 -l/-L 那套链接参数,选中它只会在链接阶段失败。只按架构匹配的话
    # 它会混进来,所以这里连 ABI 一起限死。
    $ccArchPattern = '^aarch64.*(mingw|gnu)'
} else {
    $goarch = 'amd64'
    $wixArch = 'x64'
    $peMachine = $PeMachineX64
    $exePath = Join-Path $BinDir 'lgdm-x64.exe'
    $mingwPrefix = 'x86_64-w64-mingw32'
    $ccArchPattern = '^(x86_64|amd64).*(mingw|gnu)'
}

# 读 PE 头的 Machine 字段。防的是「-SkipBuild 复用了另一个架构的 bin 文件」——
# 那种情况会打出架构与载荷不匹配的安装包,装上去直接跑不起来。
function Get-PEMachine([string]$Path) {
    $fs = [System.IO.File]::OpenRead($Path)
    try {
        $br = New-Object System.IO.BinaryReader($fs)
        $fs.Position = 0x3C
        $peOffset = $br.ReadInt32()
        $fs.Position = $peOffset
        if ($br.ReadUInt32() -ne 0x4550) { return 0 }   # "PE\0\0"
        return [int]$br.ReadUInt16()
    } finally {
        $fs.Dispose()
    }
}

function Assert-PEMachine {
    $machine = Get-PEMachine $exePath
    if ($machine -eq $peMachine) { return }
    throw ("{0} 的 PE 架构是 0x{1:X4},期望 0x{2:X4}({3});请去掉 -SkipBuild 重新编译。" -f `
        $exePath, $machine, $peMachine, $slug)
}

# ---------------------------------------------------------------------------
# C 工具链:cgo 需要 gcc 风格命令行(GCC 或 clang;MSVC 的 cl.exe 不可用)
# ---------------------------------------------------------------------------

# 下载 + SHA256 校验。PS 5.1 的默认安全协议可能是 TLS 1.0,GitHub 只收 TLS 1.2+。
function Get-RemoteFile([string]$Url, [string]$OutFile, [string]$Sha256) {
    try {
        [Net.ServicePointManager]::SecurityProtocol =
            [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    } catch { }

    Write-Note "下载 $Url"
    $wc = New-Object System.Net.WebClient
    try { $wc.DownloadFile($Url, $OutFile) } finally { $wc.Dispose() }

    if ($Sha256) {
        $actual = (Get-FileHash -LiteralPath $OutFile -Algorithm SHA256).Hash
        if ($actual -ne $Sha256.ToUpperInvariant()) {
            Remove-Item -LiteralPath $OutFile -Force -ErrorAction SilentlyContinue
            throw "下载校验失败: $Url`n  期望 SHA256 $Sha256`n  实际 SHA256 $actual"
        }
    }
}

# 取 WiX CLI,返回 wix.exe 的路径,失败返回 $null。
#
# 固定版本的官方 MSI + 管理安装(msiexec /a)把文件摊到 <repo>\.tools\wix:
# 不写系统状态、不碰 PATH、不需要 winget。MSI 内部的目录层级不写死,
# 摊完直接递归找 wix.exe。
function Install-WixCli {
    if ($SkipToolInstall) { throw "缺少 WiX CLI,且指定了 -SkipToolInstall。" }

    $dest = Join-Path $RepoRoot '.tools\wix'
    if (Test-Path -LiteralPath $dest) {
        $cached = Get-ChildItem -LiteralPath $dest -Recurse -Filter 'wix.exe' -File -ErrorAction SilentlyContinue |
            Select-Object -First 1
        if ($cached) { return $cached.FullName }
    }

    New-Item -ItemType Directory -Path $dest -Force | Out-Null
    $msi = Join-Path $dest 'wix-cli-x64.msi'
    Get-RemoteFile $WixCliMsiUrl $msi $WixCliMsiSha256

    # /a = 管理安装:只把文件摊到 TARGETDIR,不装进系统(也不需要额外权限)。
    # msiexec 会把活交给子进程,必须 -Wait 等它跑完。
    $cmdline = '/a "{0}" /qn TARGETDIR="{1}"' -f $msi, $dest
    Write-Note "msiexec $cmdline"
    $proc = Start-Process -FilePath 'msiexec.exe' -ArgumentList $cmdline -Wait -PassThru
    if ($proc.ExitCode -ne 0) {
        throw ("msiexec 解包 WiX CLI 失败,退出码 {0};来源 {1}" -f $proc.ExitCode, $WixCliMsiUrl)
    }
    Remove-Item -LiteralPath $msi -Force -ErrorAction SilentlyContinue

    $exe = Get-ChildItem -LiteralPath $dest -Recurse -Filter 'wix.exe' -File -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if (-not $exe) { return $null }
    return $exe.FullName
}

# 取宿主架构的 LLVM-MinGW,返回可用的编译器路径,失败返回 $null。
#
# 直接下官方 release zip(与 winget 清单指向同一个 release)解到 <repo>\.tools。
# 不必担心选错目标:zip 内目录名与工具名都带架构三元组。
function Install-LlmMingw([string[]]$Names) {
    if ($SkipToolInstall) { return $null }

    $distroArch = ($mingwPrefix -split '-')[0]          # aarch64 / x86_64
    $dest = Join-Path $RepoRoot '.tools'
    $root = Join-Path $dest "llvm-mingw-$LlmMingwRelease-ucrt-$distroArch"

    if (-not (Test-Path -LiteralPath $root)) {
        New-Item -ItemType Directory -Path $dest -Force | Out-Null
        $zip = Join-Path $dest "llvm-mingw-$LlmMingwRelease-ucrt-$distroArch.zip"
        $url = "https://github.com/mstorsjo/llvm-mingw/releases/download/$LlmMingwRelease/llvm-mingw-$LlmMingwRelease-ucrt-$distroArch.zip"
        Get-RemoteFile $url $zip $LlmMingwSha256[$distroArch]
        Expand-Archive -LiteralPath $zip -DestinationPath $dest -Force
        Remove-Item -LiteralPath $zip -Force -ErrorAction SilentlyContinue
    }

    foreach ($name in $Names) {
        $candidate = Join-Path $root "bin\$name"
        if ((Test-Path -LiteralPath $candidate) -and (Test-CCMatchesArch $candidate $name)) {
            return $candidate
        }
    }
    return $null
}

# 只在 Inno Setup 上用到 winget:CI 的两个 Windows 镜像都预装了它(6.7.1),
# Find-Iscc 直接命中,这条路径只在开发机上跑到。
function Install-WingetPackage([string]$Id, [string]$RequiredVersion, [string]$Architecture) {
    if ($SkipToolInstall) {
        throw "缺少 $Id,且指定了 -SkipToolInstall(不会尝试安装)。"
    }
    $wg = Get-Command 'winget.exe' -ErrorAction SilentlyContinue
    if (-not $wg) {
        throw "缺少 $Id,系统里也没有 winget(CI 的 windows-11-arm 镜像就没带 winget)。
      请手动安装,或用显式路径参数指给脚本(-IsccPath / -WixPath / -CC)。"
    }
    $wingetArgs = @('install', '--id', $Id, '--exact', '--silent',
        '--accept-package-agreements', '--accept-source-agreements')
    if ($RequiredVersion) { $wingetArgs += @('--version', $RequiredVersion) }
    if ($Architecture) { $wingetArgs += @('--architecture', $Architecture) }
    Write-Note ("winget {0}" -f ($wingetArgs -join ' '))
    & $wg.Source @wingetArgs | Out-Host
    if ($LASTEXITCODE -ne 0) {
        # 已装别的版本时 winget 返回负的错误码,不代表工具不可用:调用方随后
        # 会重新查找,找不到才报错。
        Write-Warning ("winget 安装 {0} 返回 {1};继续查找已安装的工具。" -f $Id, $LASTEXITCODE)
    }
}

# winget 的 LLVM-MinGW 解到 %LOCALAPPDATA%\Microsoft\WinGet\Packages\ 下,
# 不一定进 PATH。搜索范围限定在已知位置,避免全盘递归。
function Find-PrefixedMingwCC([string[]]$Names) {
    $roots = @()
    if (${env:LOCALAPPDATA}) {
        $pkgs = Join-Path ${env:LOCALAPPDATA} 'Microsoft\WinGet\Packages'
        if (Test-Path -LiteralPath $pkgs) {
            $roots += @(Get-ChildItem -LiteralPath $pkgs -Directory -Filter '*LLVM-MinGW*' -ErrorAction SilentlyContinue |
                ForEach-Object { $_.FullName })
        }
    }
    foreach ($rel in @('llvm-mingw', 'Program Files\llvm-mingw', 'Program Files (x86)\llvm-mingw')) {
        $roots += (Join-Path $env:SystemDrive $rel)
    }

    foreach ($root in $roots) {
        if (-not (Test-Path -LiteralPath $root)) { continue }
        $hit = Get-ChildItem -LiteralPath $root -Recurse -Depth 4 -File -ErrorAction SilentlyContinue |
            Where-Object { $Names -contains $_.Name } |
            Select-Object -First 1
        if ($hit) { return $hit.FullName }
    }
    return $null
}

# 编译器声称的目标三元组(gcc 与 clang 都支持 -dumpmachine),取不到返回 $null。
function Get-CCTriple([string]$CCPath) {
    try {
        $out = & $CCPath -dumpmachine 2>$null
        if ($LASTEXITCODE -ne 0) { return $null }
        $first = ($out | Select-Object -First 1)
        if (-not $first) { return $null }
        return ([string]$first).Trim()
    } catch {
        return $null
    }
}

# 这个编译器编出来的东西是不是目标架构的。
#
# 必须查,不能只看有没有 gcc.exe:GitHub 的 windows-11-arm 镜像 PATH 上带着
# x64 的 mingw gcc,拿它配 GOARCH=arm64 会在汇编 runtime/cgo 的 gcc_arm64.S
# 时炸(下面这些错误就是 x86 汇编器看到 AArch64 指令):
#   gcc_arm64.S:30: Error: no such instruction: `stp x29,x30,[sp,'
#   gcc_arm64.S:56: Error: no such instruction: `blr x20'
# 名字带目标三元组的(-CC 或 aarch64-w64-mingw32-clang.exe)按名字判断就已经
# 确定;不带前缀的必须问编译器自己。
function Test-CCMatchesArch([string]$CCPath, [string]$Name) {
    $triple = Get-CCTriple $CCPath
    if ($triple) { return ($triple -match $ccArchPattern) }
    # -dumpmachine 跑不起来:只有文件名自带正确三元组时才认。
    return ($Name -match [regex]::Escape($mingwPrefix))
}

# 交给 Go 的 CC 值。
#
# Go 用 str.SplitQuotedFields 解析 CC,按空白切词,所以路径带空格时必须加引号,
# 否则它只拿到第一段,报:
#   cgo: C compiler "C:\\Program" not found: exec: "C:\\Program": executable file not found
# (Program Files 下的 LLVM/MinGW、以及 -CC 传进来的带空格路径都会踩到。)
function Format-CCForGo([string]$CCPath) {
    if ($CCPath -match '\s') { return '"' + $CCPath + '"' }
    return $CCPath
}

# 解析宿主 C 编译器,返回绝对路径。这里找的都是**宿主架构**的编译器
# (含 LLVM-MinGW 的宿主 triplet)—— 本脚本不做交叉编译,拿错目标的编译器不会
# 报「架构不对」,而是先在 cgo 的汇编写死,所以每个候选都要验目标。
function Resolve-CC {
    if ($CC) {
        if (-not (Test-Path -LiteralPath $CC)) { throw "指向的 C 编译器不存在: $CC" }
        $resolved = (Resolve-Path -LiteralPath $CC).Path
        if (-not (Test-CCMatchesArch $resolved (Split-Path -Leaf $resolved))) {
            throw ("-CC 指向的编译器不是 ${slug} 目标: {0}(-dumpmachine: {1});期望 {2}" -f `
                $resolved, (Get-CCTriple $resolved), $ccArchPattern)
        }
        return $resolved
    }

    # 名字带三元组的优先:它们的目标由名字确定,不依赖 PATH 上碰巧是什么。
    $prefixed = @("$mingwPrefix-clang.exe", "$mingwPrefix-gcc.exe")
    $plain = @('gcc.exe', 'clang.exe')

    foreach ($name in ($prefixed + $plain)) {
        $cmd = Get-Command $name -ErrorAction SilentlyContinue
        if (-not $cmd) { continue }
        if (Test-CCMatchesArch $cmd.Source $name) { return $cmd.Source }
        Write-Note ("跳过 {0}:目标不是 {1}(-dumpmachine: {2})" -f `
            $name, $slug, (Get-CCTriple $cmd.Source))
    }

    $found = Find-PrefixedMingwCC $prefixed
    if ($found -and (Test-CCMatchesArch $found (Split-Path -Leaf $found))) { return $found }

    # PATH 上没有能用的宿主编译器:下 LLVM-MinGW 的官方 release zip
    # (不依赖 winget —— CI 的 windows-11-arm 镜像没有 winget)。
    Write-Step "获取 C 工具链(LLVM-MinGW $LlmMingwRelease,$mingwPrefix)"
    $found = Install-LlmMingw $prefixed
    if ($found) { return $found }

    throw @"
找不到 ${slug} 目标的 C 编译器(Fyne 的 GL 绑定是 cgo,没有 C 工具链编不过;
      装了目标不符的编译器更糟 —— 会在汇编 gcc_${goarch}.S 时才失败)。
可以用 -CC 指向已有的 ${slug} 编译器,或手动装 LLVM-MinGW(同时含 x86_64 与 aarch64):

    https://github.com/mstorsjo/llvm-mingw/releases
"@
}

# ---------------------------------------------------------------------------
# 版本
# ---------------------------------------------------------------------------

function Invoke-Git([string[]]$Arguments) {
    try {
        $out = & git @Arguments 2>$null
        if ($LASTEXITCODE -ne 0) { return $null }
        return ($out | Select-Object -First 1)
    } catch {
        return $null
    }
}

function Get-BuildVersion {
    if ($Version) { return $Version }
    $describe = Invoke-Git @('describe', '--tags', '--always', '--dirty')
    if ($describe) { return $describe }
    return '0.0.0-dev'
}

# MSI 要求严格的 X.Y.Z.W:git describe 的 "v0.1.0-3-gabc1234" 取前三段与
# 「距上个 tag 的提交数」作为第 4 段;解析不出数字时退回 0.0.0.0。
function Get-WindowsVersion([string]$BuildVersion) {
    if ($WinVersion) { return $WinVersion }
    $m = [regex]::Match($BuildVersion, '^v?(\d+)\.(\d+)\.(\d+)(?:-(\d+)-g[0-9a-fA-F]+)?')
    if (-not $m.Success) { return '0.0.0.0' }
    $major = [int]$m.Groups[1].Value
    $minor = [int]$m.Groups[2].Value
    $patch = [int]$m.Groups[3].Value
    $rev = 0
    if ($m.Groups[4].Success) { $rev = [int]$m.Groups[4].Value }
    # MSI 每段上限 65535,超了会被拒绝。
    foreach ($n in @($major, $minor, $patch, $rev)) {
        if ($n -gt 65535) { throw "版本段超出 MSI 允许的 65535: $BuildVersion" }
    }
    return ("{0}.{1}.{2}.{3}" -f $major, $minor, $patch, $rev)
}

# ---------------------------------------------------------------------------
# 编译
# ---------------------------------------------------------------------------

function Invoke-Build($CCPath) {
    $v = Get-BuildVersion
    $commit = Invoke-Git @('rev-parse', '--short', 'HEAD')
    if (-not $commit) { $commit = 'unknown' }
    $date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')

    New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
    # -H windowsgui 让 Windows 不再为 lgdm 弹 console 窗口(浏览器 lgom://
    # 协议/资源管理器拉起时不再闪黑窗);用户加 --debug 时内部 AttachConsole
    # 把日志挂回终端,行为不变。
    $ldflags = @(
        '-s', '-w',
        '-H', 'windowsgui',
        '-X', "lgo_download_manager/internal/version.Version=$v",
        '-X', "lgo_download_manager/internal/version.Commit=$commit",
        '-X', "lgo_download_manager/internal/version.Date=$date"
    ) -join ' '

    # GOARCH 显式给出并与宿主一致;CGO_ENABLED=1 是必须的 —— GOARCH 一旦被
    # 显式设置,Go 就不再认为它是「宿主默认值」,会把 cgo 关掉,而 Fyne 的 GL
    # 绑定只有 cgo 实现(报 "build constraints exclude all Go files")。
    $env:GOOS = 'windows'
    $env:GOARCH = $goarch
    $env:CGO_ENABLED = '1'
    if ($CCPath) { $env:CC = (Format-CCForGo $CCPath) }
    try {
        Invoke-Native 'go' @('build', '-trimpath', '-ldflags', $ldflags, '-o', $exePath, '.') $RepoRoot
    } finally {
        Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
        if ($CCPath) { Remove-Item Env:CC -ErrorAction SilentlyContinue }
    }

    $size = [math]::Round((Get-Item -LiteralPath $exePath).Length / 1MB, 1)
    $ccLabel = if ($CCPath) { Split-Path -Leaf $CCPath } else { 'go default (gcc)' }
    Write-Note ("built {0} ({1} MB, GOARCH={2}, CC={3}, version {4}, commit {5})" -f
        $exePath, $size, $goarch, $ccLabel, $v, $commit)
}

# ---------------------------------------------------------------------------
# 打包工具
# ---------------------------------------------------------------------------

function Get-WixMajor([string]$WixExe) {
    try {
        $out = & $WixExe --version 2>$null
        if ($LASTEXITCODE -ne 0) { return 0 }
        $text = ($out | Select-Object -First 1)
        $m = [regex]::Match([string]$text, '^\s*(\d+)\.')
        if ($m.Success) { return [int]$m.Groups[1].Value }
    } catch {
        return 0
    }
    return 0
}

# 查找 wix.exe:显式指定 → PATH → Program Files\WiX Toolset v*。
function Find-Wix {
    if ($WixPath) {
        if (-not (Test-Path -LiteralPath $WixPath)) { throw "-WixPath 指向的文件不存在: $WixPath" }
        return @{ Path = (Resolve-Path -LiteralPath $WixPath).Path; Major = 0 }
    }

    $cmd = Get-Command 'wix.exe' -ErrorAction SilentlyContinue
    if ($cmd) { return @{ Path = $cmd.Source; Major = (Get-WixMajor $cmd.Source) } }

    $found = @()
    foreach ($root in @(${env:ProgramFiles}, ${env:ProgramFiles(x86)})) {
        if (-not $root -or -not (Test-Path -LiteralPath $root)) { continue }
        foreach ($dir in (Get-ChildItem -LiteralPath $root -Directory -Filter 'WiX Toolset v*' -ErrorAction SilentlyContinue)) {
            $exe = Join-Path $dir.FullName 'bin\wix.exe'
            if (Test-Path -LiteralPath $exe) {
                $found += @{ Path = $exe; Major = (Get-WixMajor $exe) }
            }
        }
    }
    if ($found.Count -eq 0) { return $null }

    # 优先不带 OSMF EULA 的 v6 及以下。
    $free = $found | Where-Object { $_.Major -gt 0 -and $_.Major -le $MaxFreeWixMajor }
    if ($free) { return ($free | Sort-Object { $_.Major } -Descending | Select-Object -First 1) }
    return ($found | Select-Object -First 1)
}

function Find-Iscc {
    if ($IsccPath) {
        if (-not (Test-Path -LiteralPath $IsccPath)) { throw "-IsccPath 指向的文件不存在: $IsccPath" }
        return (Resolve-Path -LiteralPath $IsccPath).Path
    }

    $cmd = Get-Command 'ISCC.exe' -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }

    # NB: 括号不能省 —— `@($a + '\Programs', $b)` 会被解析成「把数组拼成
    # 一个字符串」,得到一个不存在的路径。
    $localPrograms = $null
    if (${env:LOCALAPPDATA}) { $localPrograms = Join-Path ${env:LOCALAPPDATA} 'Programs' }

    $candidates = @()
    foreach ($base in @($localPrograms, ${env:ProgramFiles(x86)}, ${env:ProgramFiles})) {
        if (-not $base) { continue }
        foreach ($ver in @('7', '6')) {
            $candidates += (Join-Path $base ("Inno Setup {0}\ISCC.exe" -f $ver))
        }
    }
    foreach ($c in $candidates) {
        if (Test-Path -LiteralPath $c) { return (Resolve-Path -LiteralPath $c).Path }
    }
    return $null
}

# ---------------------------------------------------------------------------
# 出包
# ---------------------------------------------------------------------------

function New-MsiPackage([string]$BuildVersion, [string]$NumericVersion) {
    $wix = Find-Wix
    if (-not $wix) {
        Write-Step "获取 WiX Toolset CLI v$WixCliVersion"
        $wixExe = Install-WixCli
        if ($wixExe) { $wix = @{ Path = $wixExe; Major = 0 } }
    }
    if (-not $wix) {
        throw "找不到 wix.exe,也没能装上 WiX CLI v$WixCliVersion($WixCliMsiUrl)。请用 -WixPath 指定。"
    }
    if ($wix.Major -gt $MaxFreeWixMajor) {
        throw @"
wix.exe 是 v$($wix.Major)($($wix.Path)),它的二进制发布物要求在遵守 OSMF EULA 的前提下
使用,本项目不引入该依赖。请改用 WiX v$MaxFreeWixMajor 及以下 —— 删掉那个安装,
脚本会自动取 v${WixCliVersion} 版:

    $WixCliMsiUrl

或用 -WixPath 指向已有的 v$MaxFreeWixMajor 及以下 wix.exe。
"@
    }

    $outMsi = Join-Path $OutputDir ("lgdm-setup-{0}-{1}.msi" -f $BuildVersion, $slug)
    # -arch 决定 <Package> 的平台,-d Arch 决定打哪个二进制,两者必须一致。
    Invoke-Native $wix.Path @(
        'build', $WxsPath,
        '-arch', $wixArch,
        '-d', ("ProductVersion={0}" -f $NumericVersion),
        '-d', ("Arch={0}" -f $slug),
        '-o', $outMsi
    ) $RepoRoot
    return $outMsi
}

function New-ExePackage([string]$BuildVersion, [string]$NumericVersion) {
    $iscc = Find-Iscc
    if (-not $iscc) {
        Write-Step "安装 Inno Setup($InnoWingetId)"
        # Inno Setup 的 winget 清单只有 x86 安装包(innosetup-<版本>.exe,机器级/用户级
        # 两条都是 x86);显式指定,免得 arm64 机器上依赖 winget 的架构回退。
        Install-WingetPackage $InnoWingetId $null 'x86'
        $iscc = Find-Iscc
    }
    if (-not $iscc) {
        throw "找不到 ISCC.exe。请安装 Inno Setup($InnoWingetId)或用 -IsccPath 指定。"
    }

    # .iss 从环境变量读版本号(ISPP 的 GetEnv),/DMyArch 选架构与载荷,
    # 输出目录用 ISCC 的 /O 覆盖 .iss 里的 OutputDir。
    $env:LDM_VERSION = $BuildVersion
    $env:LDM_WIN_VERSION = $NumericVersion
    try {
        Invoke-Native $iscc @(('/DMyArch={0}' -f $slug), ('/O{0}' -f $OutputDir), $IssPath) $RepoRoot
    } finally {
        Remove-Item Env:LDM_VERSION -ErrorAction SilentlyContinue
        Remove-Item Env:LDM_WIN_VERSION -ErrorAction SilentlyContinue
    }

    return (Join-Path $OutputDir ("lgdm-setup-{0}-{1}.exe" -f $BuildVersion, $slug))
}

# ---------------------------------------------------------------------------
# 主流程
# ---------------------------------------------------------------------------

foreach ($required in @($WxsPath, $IssPath, $IconPath)) {
    if (-not (Test-Path -LiteralPath $required)) {
        throw "缺少打包所需文件: $required"
    }
}

$buildVersion = Get-BuildVersion
$numericVersion = Get-WindowsVersion $buildVersion

Write-Step "打包 lgo_download_manager(原生 ${slug})"
Write-Note "repo        $RepoRoot"
Write-Note "host         $hostSlug"
Write-Note "arch         $slug (GOARCH=$goarch)"
Write-Note "format       $Format"
Write-Note "version      $buildVersion (MSI/VersionInfo: $numericVersion)"

New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null

if (-not $SkipBuild) {
    $ccPath = Resolve-CC
    Write-Step "go build ($slug)"
    Invoke-Build $ccPath
} elseif (-not (Test-Path -LiteralPath $exePath)) {
    throw ("-SkipBuild 指定为跳过编译,但 {0} 不存在" -f $exePath)
}
Assert-PEMachine

$artifacts = @()
if ($Format -eq 'msi' -or $Format -eq 'both') {
    Write-Step "MSI(WiX, $slug)"
    $artifacts += (New-MsiPackage $buildVersion $numericVersion)
}
if ($Format -eq 'exe' -or $Format -eq 'both') {
    Write-Step "EXE(Inno Setup, $slug)"
    $artifacts += (New-ExePackage $buildVersion $numericVersion)
}

Write-Step '产物'
foreach ($a in $artifacts) {
    if (-not (Test-Path -LiteralPath $a)) { throw "打包结束但产物不存在: $a" }
    $size = [math]::Round((Get-Item -LiteralPath $a).Length / 1MB, 1)
    Write-Host ("    {0} ({1} MB)" -f $a, $size)
}
