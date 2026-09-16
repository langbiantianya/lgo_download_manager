<#
.SYNOPSIS
    为 lgo_download_manager (lgdm) 打包 Windows 安装包:MSI(WiX)或 EXE(Inno Setup),
    x64 与 arm64 各出一份。

.DESCRIPTION
    一条命令完成「按架构编译二进制 → 打包安装包」:

        pwsh -File scripts\package.ps1                    # x64 + arm64 的 MSI 与 EXE
        pwsh -File scripts\package.ps1 -Format msi        # 只出 MSI
        pwsh -File scripts\package.ps1 -Arch x64          # 只出 x64
        pwsh -File scripts\package.ps1 -Format exe -Arch arm64

    产物:

        dist\lgdm-setup-<版本>-x64.msi     dist\lgdm-setup-<版本>-arm64.msi
        dist\lgdm-setup-<版本>-x64.exe     dist\lgdm-setup-<版本>-arm64.exe

    MSI 一个包只能承载一种架构,所以必须按架构分开;两个安装包(MSI / EXE)
    功能对等,同一架构上**任选其一**安装即可(不要同时装两个):

      - 用户态安装到 %LOCALAPPDATA%\Programs\lgo_download_manager,不弹 UAC;
      - 注册 HKCU\Software\Classes\lgom,命令行 "<install>\lgdm.exe" "%1";
      - 装好后浏览器/资源管理器里的 lgom://download?url=... 会拉起 lgdm 并开始下载;
        lgdm 已在运行时,新进程把 URL 经命名管道转发给主实例后退出;
      - 安装/升级/卸载前会结束正在运行的 lgdm 实例,避免 exe 被占用;
      - 卸载时连同用户数据目录一起删除,覆盖安装/升级不动它。

    依赖的工具如果缺失,脚本会尝试用 winget 安装(可用 -SkipToolInstall 关闭,
    或用 -WixPath / -IsccPath / -CCX64 / -CCArm64 指向已有安装):

      - MSI: WiX Toolset CLI(WiXToolset.WiXCLI)。WiX v7 要求接受 OSMF EULA,
        本脚本只用 v6/v5。
      - EXE: Inno Setup 6(JRSoftware.InnoSetup)。
      - C 工具链:cgo 只接受 gcc 风格命令行的编译器 —— GCC 或 clang。
        **MSVC 的 cl.exe 不能用作 CC**(Go 从未支持 MSVC:cgo 用 -o/-c/-I/-D
        这类 gcc 参数直接调编译器,cl.exe 的 /Fo、/c 语法与之不兼容)。
        clang 可以,LLVM-MinGW(MartinStorsjo.LLVM-MinGW.UCRT)是 clang +
        mingw-w64 sysroot 的发行版,一个包同时提供 x86_64 与 aarch64 两个
        target,所以两个架构可以共用同一套工具链。

.PARAMETER Format
    打包格式:msi / exe / both(默认 both)。

.PARAMETER Arch
    要打包的架构,逗号分隔(默认 x64,arm64)。取值:x64 / arm64,
    也接受 amd64 / aarch64。

.PARAMETER Version
    覆盖版本号(默认取 `git describe --tags --always --dirty`),写进二进制的
    version 包与安装包文件名。

.PARAMETER WinVersion
    覆盖 MSI ProductVersion / VersionInfoVersion 用的 X.Y.Z.W 数字版本
    (默认从 Version 推导)。

.PARAMETER OutputDir
    安装包输出目录,默认 <repo>\dist。

.PARAMETER SkipBuild
    跳过 go build,直接使用已有的 bin\lgdm-<arch>.exe(仍会校验 PE 架构)。

.PARAMETER SkipToolInstall
    工具缺失时直接报错,不尝试 winget 安装。

.PARAMETER WixPath
    显式指定 wix.exe(Windows Installer XML 命令行)。

.PARAMETER IsccPath
    显式指定 ISCC.exe(Inno Setup 命令行编译器)。

.PARAMETER CCX64
    x64 的 C 编译器。缺省按 x86_64-w64-mingw32-clang → 主机默认 gcc 解析。

.PARAMETER CCArm64
    arm64 的 C 交叉编译器。缺省按 aarch64-w64-mingw32-clang →
    winget 安装的 LLVM-MinGW 解析。

.EXAMPLE
    pwsh -File scripts\package.ps1

.EXAMPLE
    pwsh -File scripts\package.ps1 -Format msi -Arch arm64 -Version v1.0.0 -WinVersion 1.0.0.0
#>
#Requires -Version 5.1
[CmdletBinding()]
param(
    [ValidateSet('msi', 'exe', 'both')]
    [string]$Format = 'both',

    [string[]]$Arch = @('x64', 'arm64'),

    [string]$Version,
    [string]$WinVersion,
    [string]$OutputDir,
    [switch]$SkipBuild,
    [switch]$SkipToolInstall,
    [string]$WixPath,
    [string]$IsccPath,
    [string]$CCX64,
    [string]$CCArm64
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$RepoRoot = Split-Path -Parent $PSScriptRoot
if (-not $OutputDir) { $OutputDir = Join-Path $RepoRoot 'dist' }

# 装到目标机器上的文件名固定是 lgdm.exe(协议注册表命令行、托盘/UI 自复制都按
# 这个名字找);架构只体现在 bin\ 里待打包的文件名上。
$AppExeName = 'lgdm.exe'
$BinDir = Join-Path $RepoRoot 'bin'
$IconPath = Join-Path $RepoRoot 'assets\lgdm.ico'
$WxsPath = Join-Path $RepoRoot 'installer\lgdm.wxs'
$IssPath = Join-Path $RepoRoot 'installer\installer.iss'

# 不带 OSMF EULA 的最后一个 WiX 大版本;v7 起命令行会直接拒绝运行。
$MaxFreeWixMajor = 6
$WixWingetId = 'WiXToolset.WiXCLI'
$WixWingetVersion = '6.0.2'
$InnoWingetId = 'JRSoftware.InnoSetup'
# clang + mingw-w64 sysroot,同时提供 x86_64 与 aarch64 target。
$MingwWingetId = 'MartinStorsjo.LLVM-MinGW.UCRT'

# PE 头的 Machine 字段。
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
        # | Out-Host 而不是直接调用:否则子进程的 stdout 会混进调用方的
        # 返回值里(PowerShell 函数会把所有未捕获的输出一起 return)。
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
# 架构
# ---------------------------------------------------------------------------

# 规范化 -Arch:接受 x64/amd64 与 arm64/aarch64,去重并保持用户给定顺序。
function Resolve-Architectures {
    $seen = @{}
    $result = @()
    foreach ($item in $Arch) {
        foreach ($part in ($item -split ',')) {
            $slug = $part.Trim().ToLowerInvariant()
            switch ($slug) {
                'x64' { $slug = 'x64' }
                'amd64' { $slug = 'x64' }
                'arm64' { $slug = 'arm64' }
                'aarch64' { $slug = 'arm64' }
                default { throw "不支持的架构「$part」;可用:x64、arm64(也接受 amd64/aarch64)" }
            }
            if (-not $seen.ContainsKey($slug)) {
                $seen[$slug] = $true
                $result += $slug
            }
        }
    }
    if ($result.Count -eq 0) { throw '-Arch 至少需要一个架构' }
    return $result
}

# slug → Go 的 GOARCH、WiX 的平台标识、待打包的二进制路径。
function Get-ArchSpec([string]$Slug) {
    if ($Slug -eq 'arm64') {
        return [pscustomobject]@{
            Slug    = 'arm64'
            Goarch  = 'arm64'
            WixArch = 'arm64'
            PE      = $PeMachineArm64
            ExePath = (Join-Path $BinDir 'lgdm-arm64.exe')
        }
    }
    return [pscustomobject]@{
        Slug    = 'x64'
        Goarch  = 'amd64'
        WixArch = 'x64'
        PE      = $PeMachineX64
        ExePath = (Join-Path $BinDir 'lgdm-x64.exe')
    }
}

# 读 PE 头的 Machine 字段,确认产物确实是目标架构。
# 防的是「-SkipBuild 复用了另一个架构的 bin 文件」和「工具链 target 传错」,
# 这两种情况都会打出架构与载荷不匹配的安装包(装上去直接跑不起来)。
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

function Assert-PEMachine([pscustomobject]$Spec) {
    $machine = Get-PEMachine $Spec.ExePath
    if ($machine -eq $Spec.PE) { return }
    $msg = "{0} 的 PE 架构是 0x{1:X4},期望 0x{2:X4}({3});请检查工具链 target,或去掉 -SkipBuild 重新编译。" -f `
        $Spec.ExePath, $machine, $Spec.PE, $Spec.Slug
    throw $msg
}

# ---------------------------------------------------------------------------
# C 工具链(cgo 需要 gcc 风格命令行:gcc 或 clang;MSVC 的 cl.exe 不可用)
# ---------------------------------------------------------------------------

# 在已安装的工具目录里找带前缀的 mingw 编译器。winget 把 LLVM-MinGW 解到
# %LOCALAPPDATA%\Microsoft\WinGet\Packages\MartinStorsjo.LLVM-MinGW.*\ 下,
# 且不一定进 PATH,所以要显式找;搜索范围限定在几个已知位置,避免全盘递归。
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

# 解析某个架构的 CC。返回编译器的绝对路径,或 $null 表示交给 Go 用自己的默认 CC。
# 找不到 arm64 工具链时会先尝试 winget 安装 LLVM-MinGW。
function Resolve-CC([string]$Slug) {
    $explicit = $null
    if ($Slug -eq 'arm64') { $explicit = $CCArm64 } else { $explicit = $CCX64 }
    if ($explicit) {
        if (-not (Test-Path -LiteralPath $explicit)) { throw "指向的 C 编译器不存在: $explicit" }
        return (Resolve-Path -LiteralPath $explicit).Path
    }

    # clang 优先:同一个工具链包同时覆盖两个架构,且 winget 可自动获取。
    if ($Slug -eq 'arm64') {
        $names = @('aarch64-w64-mingw32-clang.exe', 'aarch64-w64-mingw32-gcc.exe')
    } else {
        $names = @('x86_64-w64-mingw32-clang.exe', 'x86_64-w64-mingw32-gcc.exe')
    }
    foreach ($name in $names) {
        $cmd = Get-Command $name -ErrorAction SilentlyContinue
        if ($cmd) { return $cmd.Source }
    }
    $found = Find-PrefixedMingwCC $names
    if ($found) { return $found }

    if ($Slug -eq 'x64') {
        # 主机上已有 gcc 就让 Go 用默认 CC(与历史构建一致)。
        if (Get-Command 'gcc.exe' -ErrorAction SilentlyContinue) { return $null }
        throw @"
x64 需要 C 编译器(gcc 或 LLVM-MinGW 的 x86_64-w64-mingw32-clang):Fyne 的 GL 绑定是 cgo,没有 C 工具链编不过。
    winget install --id $MingwWingetId --exact --silent
"@
    }

    Write-Step "安装 arm64 C 工具链(LLVM-MinGW:$MingwWingetId)"
    Install-WingetPackage $MingwWingetId $null
    $found = Find-PrefixedMingwCC $names
    if ($found) { return $found }

    throw @"
找不到 aarch64 的 C 交叉编译器(arm64 构建必需:Fyne 的 GL 绑定是 cgo,GOARCH=arm64 时 Go 会关掉 cgo 并编译失败)。
请装 LLVM-MinGW 或用 -CCArm64 指向已有的编译器(如 aarch64-w64-mingw32-clang):

    winget install --id $MingwWingetId --exact --silent
"@
}

# ---------------------------------------------------------------------------
# 版本
# ---------------------------------------------------------------------------

# Git 元数据:拿不到就退回占位符,保证在非 git 目录(源码压缩包)里也能打包。
function Invoke-Git([string[]]$Arguments) {
    try {
        $out = & git @Arguments 2>$null
        if ($LASTEXITCODE -ne 0) { return $null }
        return ($out | Select-Object -First 1)
    } catch {
        return $null
    }
}

function Get-BuildVersion() {
    if ($Version) { return $Version }
    $describe = Invoke-Git @('describe', '--tags', '--always', '--dirty')
    if ($describe) { return $describe }
    return '0.0.0-dev'
}

# MSI ProductVersion / VersionInfoVersion 需要严格的 X.Y.Z.W:
# git describe 的 "v0.1.0-3-gabc1234" 取其前三段与「距上个 tag 的提交数」
# 作为第 4 段;完全解析不出数字时退回 0.0.0.0。
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

function Invoke-Build([pscustomobject]$Spec, [string]$CC) {
    $v = Get-BuildVersion
    $commit = Invoke-Git @('rev-parse', '--short', 'HEAD')
    if (-not $commit) { $commit = 'unknown' }
    $date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')

    New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
    $ldflags = @(
        '-s', '-w',
        '-X', "lgo_download_manager/internal/version.Version=$v",
        '-X', "lgo_download_manager/internal/version.Commit=$commit",
        '-X', "lgo_download_manager/internal/version.Date=$date"
    ) -join ' '

    $env:GOOS = 'windows'
    $env:GOARCH = $Spec.Goarch
    # 交叉编译(GOARCH != 主机架构)时 Go 默认把 cgo 关掉,而 Fyne 的 GL 绑定
    # 只有 cgo 实现,关掉就是 "build constraints exclude all Go files"。
    $env:CGO_ENABLED = '1'
    if ($CC) { $env:CC = $CC }
    try {
        Invoke-Native 'go' @('build', '-trimpath', '-ldflags', $ldflags, '-o', $Spec.ExePath, '.') $RepoRoot
    } finally {
        Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
        if ($CC) { Remove-Item Env:CC -ErrorAction SilentlyContinue }
    }

    $size = [math]::Round((Get-Item -LiteralPath $Spec.ExePath).Length / 1MB, 1)
    $ccLabel = if ($CC) { Split-Path -Leaf $CC } else { 'go default (gcc)' }
    Write-Note ("built {0} ({1} MB, GOARCH={2}, CC={3}, version {4}, commit {5})" -f
        $Spec.ExePath, $size, $Spec.Goarch, $ccLabel, $v, $commit)
}

# ---------------------------------------------------------------------------
# 打包工具
# ---------------------------------------------------------------------------

# 查找 wix.exe:显式指定 → PATH → WiX 官方安装目录(Program Files\WiX Toolset v*)。
# 返回 @{ Path = ...; Major = ... };找不到返回 $null。
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

    # 优先选不带 OSMF EULA 的版本(v6 及以下);只有 v7+ 时才用它,并交给
    # 调用方报错说明。
    $free = $found | Where-Object { $_.Major -gt 0 -and $_.Major -le $MaxFreeWixMajor }
    if ($free) { return ($free | Sort-Object { $_.Major } -Descending | Select-Object -First 1) }
    return ($found | Select-Object -First 1)
}

# wix --version 输出形如 "6.0.2+abc1234";解析失败返回 0。
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

function Find-Iscc {
    if ($IsccPath) {
        if (-not (Test-Path -LiteralPath $IsccPath)) { throw "-IsccPath 指向的文件不存在: $IsccPath" }
        return (Resolve-Path -LiteralPath $IsccPath).Path
    }

    $cmd = Get-Command 'ISCC.exe' -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }

    # Inno Setup 6/7 的官方安装位置:winget 装到用户目录,独立安装包默认
    # 在 Program Files (x86)。
    # NB: 括号不能省 —— `@($a + '\Programs', $b, $c)` 会被解析成「把数组
    # 拼接成一个字符串」,得到一个不存在的路径。
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

function Install-WingetPackage([string]$Id, [string]$RequiredVersion) {
    if ($SkipToolInstall) {
        throw "缺少 $Id,且指定了 -SkipToolInstall(不会尝试 winget 安装)。"
    }
    $wg = Get-Command 'winget.exe' -ErrorAction SilentlyContinue
    if (-not $wg) {
        throw "缺少 $Id,且系统里没有 winget,请手动安装后重试(或用 -SkipToolInstall 之外的显式路径参数)。"
    }

    $wingetArgs = @('install', '--id', $Id, '--exact', '--silent',
        '--accept-package-agreements', '--accept-source-agreements')
    if ($RequiredVersion) { $wingetArgs += @('--version', $RequiredVersion) }
    Write-Note ("winget {0}" -f ($wingetArgs -join ' '))
    & $wg.Source @wingetArgs | Out-Host
    if ($LASTEXITCODE -ne 0) {
        # 已经装了别的版本时 winget 返回 "update not applicable"(负的 CLI 错误码),
        # 这不代表工具不可用:调用方随后会重新查找,找不到才报错。
        Write-Warning ("winget 安装 {0} 返回 {1};继续查找已安装的工具。" -f $Id, $LASTEXITCODE)
    }
}

# ---------------------------------------------------------------------------
# 出包
# ---------------------------------------------------------------------------

function New-MsiPackage {
    param([pscustomobject]$Spec, [string]$BuildVersion, [string]$NumericVersion)

    $wix = Find-Wix
    if (-not $wix) {
        Write-Step "安装 WiX Toolset CLI($WixWingetId $WixWingetVersion)"
        Install-WingetPackage $WixWingetId $WixWingetVersion
        $wix = Find-Wix
    }
    if (-not $wix) {
        throw "找不到 wix.exe。请安装 WiX Toolset CLI($WixWingetId)或用 -WixPath 指定。"
    }
    if ($wix.Major -gt $MaxFreeWixMajor) {
        throw @"
wix.exe 是 v$($wix.Major)($($wix.Path)),它要求接受 OSMF EULA 才能运行,本项目不引入该依赖。
请改用 WiX v$MaxFreeWixMajor 及以下:

    winget install --id $WixWingetId --version $WixWingetVersion --exact --silent

或用 -WixPath 指向已有的 v$MaxFreeWixMajor 及以下 wix.exe。
"@
    }

    $outMsi = Join-Path $OutputDir ("lgdm-setup-{0}-{1}.msi" -f $BuildVersion, $Spec.Slug)
    # -arch 决定 <Package> 的平台,-d Arch 决定打哪个二进制,两者必须一致。
    Invoke-Native $wix.Path @(
        'build', $WxsPath,
        '-arch', $Spec.WixArch,
        '-d', ("ProductVersion={0}" -f $NumericVersion),
        '-d', ("Arch={0}" -f $Spec.Slug),
        '-o', $outMsi
    ) $RepoRoot
    return $outMsi
}

function New-ExePackage {
    param([pscustomobject]$Spec, [string]$BuildVersion, [string]$NumericVersion)

    $iscc = Find-Iscc
    if (-not $iscc) {
        Write-Step "安装 Inno Setup($InnoWingetId)"
        Install-WingetPackage $InnoWingetId $null
        $iscc = Find-Iscc
    }
    if (-not $iscc) {
        throw "找不到 ISCC.exe。请安装 Inno Setup($InnoWingetId)或用 -IsccPath 指定。"
    }

    # .iss 从环境变量读版本号(ISPP 的 GetEnv),/DMyArch 选架构与载荷,
    # 输出目录用 ISCC 的 /O 覆盖 .iss 里的 OutputDir(默认 dist\)。
    $env:LDM_VERSION = $BuildVersion
    $env:LDM_WIN_VERSION = $NumericVersion
    try {
        Invoke-Native $iscc @(('/DMyArch={0}' -f $Spec.Slug), ('/O{0}' -f $OutputDir), $IssPath) $RepoRoot
    } finally {
        Remove-Item Env:LDM_VERSION -ErrorAction SilentlyContinue
        Remove-Item Env:LDM_WIN_VERSION -ErrorAction SilentlyContinue
    }

    # .iss 里 OutputBaseFilename = lgdm-setup-<版本>-<架构>.exe。
    return (Join-Path $OutputDir ("lgdm-setup-{0}-{1}.exe" -f $BuildVersion, $Spec.Slug))
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
$archSlugs = Resolve-Architectures

Write-Step "打包 lgo_download_manager"
Write-Note "repo        $RepoRoot"
Write-Note "format      $Format"
Write-Note "arch        $($archSlugs -join ', ')"
Write-Note "version     $buildVersion (MSI/VersionInfo: $numericVersion)"

New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null

$specs = @()
foreach ($slug in $archSlugs) {
    $spec = Get-ArchSpec $slug

    if (-not $SkipBuild) {
        # 只在真要编译时才解析/安装 C 工具链。
        $cc = Resolve-CC $slug
        Write-Step ("go build ({0})" -f $spec.Slug)
        Invoke-Build $spec $cc
    } elseif (-not (Test-Path -LiteralPath $spec.ExePath)) {
        throw ("-SkipBuild 指定为跳过编译,但 {0} 不存在" -f $spec.ExePath)
    }
    Assert-PEMachine $spec
    $specs += $spec
}

$artifacts = @()
foreach ($spec in $specs) {
    if ($Format -eq 'msi' -or $Format -eq 'both') {
        Write-Step ("MSI(WiX, {0})" -f $spec.Slug)
        $artifacts += (New-MsiPackage $spec $buildVersion $numericVersion)
    }
    if ($Format -eq 'exe' -or $Format -eq 'both') {
        Write-Step ("EXE(Inno Setup, {0})" -f $spec.Slug)
        $artifacts += (New-ExePackage $spec $buildVersion $numericVersion)
    }
}

Write-Step '产物'
foreach ($a in $artifacts) {
    if (-not (Test-Path -LiteralPath $a)) { throw "打包结束但产物不存在: $a" }
    $size = [math]::Round((Get-Item -LiteralPath $a).Length / 1MB, 1)
    Write-Host ("    {0} ({1} MB)" -f $a, $size)
}
Write-Note ''
Write-Note '安装(用户态,不需要管理员): msiexec /i <msi> 或 直接双击 <exe>'
Write-Note '验证协议注册: HKCU\Software\Classes\lgom\shell\open\command'
