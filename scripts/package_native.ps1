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

    依赖(缺失时用 winget 安装,-SkipToolInstall 可关闭):
      - MSI: WiX Toolset CLI 6.x(v7 起要求接受 OSMF EULA,本项目不引入)
      - EXE: Inno Setup 6
      - C 编译器:cgo 需要 gcc 风格命令行(GCC 或 clang;**MSVC 的 cl.exe 不可用**,
        Go 从未支持 MSVC)。宿主缺 GCC 时脚本会装 LLVM-MinGW,它自带的
        <triplet>-clang 在**同架构**宿主上跑就是原生编译,不是交叉编译。

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
    工具缺失时直接报错,不尝试 winget 安装。

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

# 不带 OSMF EULA 的最后一个 WiX 大版本;v7 起命令行会直接拒绝运行。
$MaxFreeWixMajor = 6
$WixWingetId = 'WiXToolset.WiXCLI'
$WixWingetVersion = '6.0.2'
$InnoWingetId = 'JRSoftware.InnoSetup'
$MingwWingetId = 'MartinStorsjo.LLVM-MinGW.UCRT'

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
    $mingwClangName = 'aarch64-w64-mingw32-clang.exe'
} else {
    $goarch = 'amd64'
    $wixArch = 'x64'
    $peMachine = $PeMachineX64
    $exePath = Join-Path $BinDir 'lgdm-x64.exe'
    $mingwClangName = 'x86_64-w64-mingw32-clang.exe'
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

function Install-WingetPackage([string]$Id, [string]$RequiredVersion) {
    if ($SkipToolInstall) {
        throw "缺少 $Id,且指定了 -SkipToolInstall(不会尝试 winget 安装)。"
    }
    $wg = Get-Command 'winget.exe' -ErrorAction SilentlyContinue
    if (-not $wg) {
        throw "缺少 $Id,且系统里没有 winget;请手动安装后重试。"
    }
    $wingetArgs = @('install', '--id', $Id, '--exact', '--silent',
        '--accept-package-agreements', '--accept-source-agreements')
    if ($RequiredVersion) { $wingetArgs += @('--version', $RequiredVersion) }
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

# 解析宿主 C 编译器。返回绝对路径,或 $null 表示交给 Go 用自己的默认 CC。
# 这里找的都是「宿主架构」的编译器(含 LLVM-MinGW 的宿主 triplet),不涉及交叉。
function Resolve-CC {
    if ($CC) {
        if (-not (Test-Path -LiteralPath $CC)) { throw "指向的 C 编译器不存在: $CC" }
        return (Resolve-Path -LiteralPath $CC).Path
    }

    foreach ($name in @('gcc.exe', 'clang.exe', $mingwClangName)) {
        $cmd = Get-Command $name -ErrorAction SilentlyContinue
        if ($cmd) { return $cmd.Source }
    }
    $found = Find-PrefixedMingwCC @($mingwClangName, 'clang.exe')
    if ($found) { return $found }

    # 宿主 x64 上 Go 的默认 CC 就是 gcc;没有就走 winget 装 LLVM-MinGW。
    Write-Step "安装 C 工具链(LLVM-MinGW:$MingwWingetId)"
    Install-WingetPackage $MingwWingetId $null
    $found = Find-PrefixedMingwCC @($mingwClangName, 'clang.exe')
    if ($found) { return $found }

    throw @"
找不到 ${slug} 的 C 编译器(Fyne 的 GL 绑定是 cgo,没有 C 工具链编不过)。
    winget install --id $MingwWingetId --exact --silent
或用 -CC 指向已有编译器。
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
    if ($CCPath) { $env:CC = $CCPath }
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
        Install-WingetPackage $InnoWingetId $null
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
