<#
.SYNOPSIS
    为 lgo_download_manager (ldm) 打包 Windows 安装包:MSI(WiX)或 EXE(Inno Setup)。

.DESCRIPTION
    一条命令完成「编译二进制 → 打包安装包」:

        pwsh -File scripts\package.ps1                 # MSI + EXE
        pwsh -File scripts\package.ps1 -Format msi     # 只出 MSI
        pwsh -File scripts\package.ps1 -Format exe     # 只出 EXE

    两个安装包功能对等,任选其一安装即可(不要同时装两个):

      - 用户态安装到 %LOCALAPPDATA%\Programs\lgo_download_manager,不弹 UAC;
      - 注册 HKCU\Software\Classes\lgom,命令行 "<install>\ldm.exe" "%1";
      - 装好后浏览器/资源管理器里的 lgom://download?url=... 会拉起 ldm 并开始下载;
        ldm 已在运行时,新进程把 URL 经命名管道转发给主实例后退出;
      - 安装/升级/卸载前会结束正在运行的 ldm 实例,避免 exe 被占用。

    依赖的打包工具如果缺失,脚本会尝试用 winget 安装(可用 -SkipToolInstall
    关闭,或用 -WixPath / -IsccPath 指向已有安装):

      - MSI: WiX Toolset CLI(WiXToolset.WiXCLI)。注意 WiX v7 要求接受 OSMF
        EULA,本脚本只用 v6/v5。
      - EXE: Inno Setup 6(JRSoftware.InnoSetup)。

.PARAMETER Format
    打包格式:msi / exe / both(默认 both)。

.PARAMETER Version
    覆盖版本号(默认取 `git describe --tags --always --dirty`),写进二进制的
    version 包与安装包文件名。

.PARAMETER WinVersion
    覆盖 MSI ProductVersion / VersionInfoVersion 用的 X.Y.Z.W 数字版本
    (默认从 Version 推导)。

.PARAMETER OutputDir
    安装包输出目录,默认 <repo>\dist。

.PARAMETER SkipBuild
    跳过 go build,直接使用已有的 bin\ldm.exe。

.PARAMETER SkipToolInstall
    打包工具缺失时直接报错,不尝试 winget 安装。

.PARAMETER WixPath
    显式指定 wix.exe(Windows Installer XML 命令行)。

.PARAMETER IsccPath
    显式指定 ISCC.exe(Inno Setup 命令行编译器)。

.EXAMPLE
    pwsh -File scripts\package.ps1

.EXAMPLE
    pwsh -File scripts\package.ps1 -Format msi -Version v1.0.0 -WinVersion 1.0.0.0
#>
#Requires -Version 5.1
[CmdletBinding()]
param(
    [ValidateSet('msi', 'exe', 'both')]
    [string]$Format = 'both',

    [string]$Version,
    [string]$WinVersion,
    [string]$OutputDir,
    [switch]$SkipBuild,
    [switch]$SkipToolInstall,
    [string]$WixPath,
    [string]$IsccPath
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$RepoRoot = Split-Path -Parent $PSScriptRoot
if (-not $OutputDir) { $OutputDir = Join-Path $RepoRoot 'dist' }

$AppExeName = 'ldm.exe'
$BinPath = Join-Path $RepoRoot ('bin\' + $AppExeName)
$IconPath = Join-Path $RepoRoot 'assets\ldm.ico'
$WxsPath = Join-Path $RepoRoot 'installer\ldm.wxs'
$IssPath = Join-Path $RepoRoot 'installer\installer.iss'

# 不带 OSMF EULA 的最后一个 WiX 大版本;v7 起命令行会直接拒绝运行。
$MaxFreeWixMajor = 6
$WixWingetId = 'WiXToolset.WiXCLI'
$WixWingetVersion = '6.0.2'
$InnoWingetId = 'JRSoftware.InnoSetup'

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

function Invoke-Build {
    $v = Get-BuildVersion
    $commit = Invoke-Git @('rev-parse', '--short', 'HEAD')
    if (-not $commit) { $commit = 'unknown' }
    $date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')

    New-Item -ItemType Directory -Path (Split-Path -Parent $BinPath) -Force | Out-Null
    $ldflags = @(
        '-s', '-w',
        '-X', "lgo_download_manager/internal/version.Version=$v",
        '-X', "lgo_download_manager/internal/version.Commit=$commit",
        '-X', "lgo_download_manager/internal/version.Date=$date"
    ) -join ' '

    Invoke-Native 'go' @('build', '-trimpath', '-ldflags', $ldflags, '-o', $BinPath, '.') $RepoRoot

    $size = [math]::Round((Get-Item -LiteralPath $BinPath).Length / 1MB, 1)
    Write-Note ("built {0} ({1} MB, version {2}, commit {3})" -f $BinPath, $size, $v, $commit)
}

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

function New-MsiPackage {
    param([string]$BuildVersion, [string]$NumericVersion)

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

    $outMsi = Join-Path $OutputDir ("ldm-setup-{0}.msi" -f $BuildVersion)
    Invoke-Native $wix.Path @(
        'build', $WxsPath,
        '-arch', 'x64',
        '-d', ("ProductVersion={0}" -f $NumericVersion),
        '-o', $outMsi
    ) $RepoRoot
    return $outMsi
}

function New-ExePackage {
    param([string]$BuildVersion, [string]$NumericVersion)

    $iscc = Find-Iscc
    if (-not $iscc) {
        Write-Step "安装 Inno Setup($InnoWingetId)"
        Install-WingetPackage $InnoWingetId $null
        $iscc = Find-Iscc
    }
    if (-not $iscc) {
        throw "找不到 ISCC.exe。请安装 Inno Setup($InnoWingetId)或用 -IsccPath 指定。"
    }

    # .iss 从环境变量读版本号(ISPP 的 GetEnv),避免在脚本里拼字符串;
    # 输出目录用 ISCC 的 /O 覆盖 .iss 里的 OutputDir(默认 dist\)。
    $env:LDM_VERSION = $BuildVersion
    $env:LDM_WIN_VERSION = $NumericVersion
    try {
        Invoke-Native $iscc @(('/O{0}' -f $OutputDir), $IssPath) $RepoRoot
    } finally {
        Remove-Item Env:LDM_VERSION -ErrorAction SilentlyContinue
        Remove-Item Env:LDM_WIN_VERSION -ErrorAction SilentlyContinue
    }

    # OutputBaseFilename 在 .iss 里固定为 ldm-setup-<版本>.exe。
    return (Join-Path $OutputDir ("ldm-setup-{0}.exe" -f $BuildVersion))
}

# ---------------------------------------------------------------------------
# 主流程
# ---------------------------------------------------------------------------

foreach ($required in @($WxsPath, $IssPath, $IconPath)) {
    if (-not (Test-Path -LiteralPath $required)) {
        throw "缺少打包所需文件: $required(图标可用 scripts/gen_icon.py 重新生成)"
    }
}

$buildVersion = Get-BuildVersion
$numericVersion = Get-WindowsVersion $buildVersion

Write-Step "打包 lgo_download_manager"
Write-Note "repo        $RepoRoot"
Write-Note "format      $Format"
Write-Note "version     $buildVersion (MSI/VersionInfo: $numericVersion)"

if (-not $SkipBuild) {
    Write-Step 'go build'
    Invoke-Build
} elseif (-not (Test-Path -LiteralPath $BinPath)) {
    throw "-SkipBuild 指定为跳过编译,但 $BinPath 不存在。"
}

New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null

$artifacts = @()
if ($Format -eq 'msi' -or $Format -eq 'both') {
    Write-Step 'MSI(WiX)'
    $artifacts += (New-MsiPackage $buildVersion $numericVersion)
}
if ($Format -eq 'exe' -or $Format -eq 'both') {
    Write-Step 'EXE(Inno Setup)'
    $artifacts += (New-ExePackage $buildVersion $numericVersion)
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
