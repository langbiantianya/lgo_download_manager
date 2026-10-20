<#
.SYNOPSIS
    校验打包产物 exe **只依赖系统 DLL**:没有动态导入 C/C++ 运行时。

.DESCRIPTION
    安装包只装 lgdm.exe 一个文件(见 installer/lgdm.wxs 与 installer/installer.iss),
    不带任何第三方 DLL。所以产物里只要有一个动态导入的 C/C++ 运行时,装上去就是
    「程序能启动、一开 BT 任务就崩」—— BT 客户端是懒创建的,那些 DLL 只在
    **建客户端时**才被加载,进程以 0xc0000135(STATUS_DLL_NOT_FOUND)退出。

    为什么用字符串扫描而不是解析 PE 导入表:动态导入的 DLL 名会以明文出现在
    二进制里(导入表的 Name 字段),而这些名字(libstdc++-6.dll 等)不会以别的方式
    出现 —— 字符串扫一遍就能判断「有没有动态依赖」,不用手写 PE 解析。
    误报的可能性基本为零。

    打包脚本链接时加了 `-extldflags=-static`,C++ 运行时是静态进 exe 的,这里
    就是给这一条兜底:万一工具链没吃透这个参数,CI 要当场红,而不是发一个
    「装上就崩」的包出去。

.PARAMETER Exe
    要检查的 exe 路径(通常是 bin\lgdm-<arch>.exe)。

.EXAMPLE
    pwsh -File scripts\check_imports.ps1 -Exe bin\lgdm-x64.exe
#>
#Requires -Version 5.1
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Exe
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if (-not (Test-Path -LiteralPath $Exe)) {
    throw "找不到要检查的文件: $Exe"
}

# 动态导入就会缺的文件:MinGW 的 libstdc++/libgcc/winpthread 与
# LLVM-MinGW 的 libc++/libunwind。
$forbidden = @(
    'libstdc++-6.dll',
    'libgcc_s_seh-1.dll',
    'libwinpthread-1.dll',
    'libc++.dll',
    'libc++abi.dll',
    'libunwind.dll'
)

$bytes = [IO.File]::ReadAllBytes((Resolve-Path -LiteralPath $Exe).Path)
# 动态导入的 DLL 名以明文出现在二进制里;静态链进去的不会有这些名字。
$text = [Text.Encoding]::ASCII.GetString($bytes)

$found = @()
foreach ($dll in $forbidden) {
    if ($text.IndexOf($dll, [StringComparison]::OrdinalIgnoreCase) -ge 0) {
        $found += $dll
    }
}

Write-Host "==> 检查 $Exe($([math]::Round($bytes.Length / 1MB, 1)) MB)是否动态依赖 C/C++ 运行时"
# 把二进制里出现过的 DLL 名都列出来(导入表里的系统 DLL 也在其中),
# 方便在 CI 日志里核对产物到底依赖什么。
# 负向前瞻:名字前面必须不是「名字字符」—— 导入表里 DLL 名是独立字符串
# (前面是 NUL),否则会把二进制里的乱码片段也当成 DLL 名。
$all = [regex]::Matches($text, '(?<![A-Za-z0-9_\-\.])[A-Za-z0-9_\-\+]{2,32}\.dll') |
    ForEach-Object { $_.Value.ToLowerInvariant() } |
    Sort-Object -Unique
if ($all) {
    Write-Host "    出现的 DLL 名: $($all -join ', ')"
}

if ($found.Count -gt 0) {
    throw @"
产物动态导入了 C/C++ 运行时: $($found -join ', ')
  安装包只装 lgdm.exe 一个文件,不带这些 DLL —— 装上去一开 BT 任务就会以
  0xc0000135(STATUS_DLL_NOT_FOUND)退出(客户端是懒创建的,平时看不出来)。
  链接参数里要带 -extldflags=-static(C++ 运行时静态进 exe)。
"@
}
Write-Host '    OK:没有动态依赖 C/C++ 运行时,产物只依赖系统 DLL'
