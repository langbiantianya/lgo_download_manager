<#
.SYNOPSIS
    lgo_download_manager build driver — Windows-native half of the build.

.DESCRIPTION
    This script is the actual implementation for `make build`,
    `make build-windows`, `make installer`, `make iscc-fetch`,
    `make iscc`, and `make clean` on Windows. The Makefile in the repo
    root simply forwards to it, so users can also call it directly:

        pwsh -File scripts/build.ps1 build
    Targets:
        help              Show this help.
        build             Build the host (Windows) binary into bin/ldm.exe.
        build-windows     Build ldm.exe (windows/amd64) into bin/.
        icon              Regenerate assets/ldm.ico via scripts/gen_icon.py.
        installer         Build the Windows installer (compiles the .iss
                          via a cached ISCC.exe; auto-fetches on first run).
        iscc-fetch        Download the Inno Setup 6.7.3 bootstrap.
        iscc              Extract ISCC.exe from the cached bootstrap.
        vet               Run `go vet ./...`.
        version           Print resolved build version.
        clean             Remove bin/, dist/, and .tools/.
        help              Show this help.
        build             Build the host (Windows) binary into bin/ldm.exe.
        build-windows     Build ldm.exe (windows/amd64) into bin/.
        icon              Regenerate assets/ldm.ico via scripts/gen_icon.py.
        installer         Build the Windows installer (compiles the .iss
                          via a cached ISCC.exe; auto-fetches on first run).
        iscc-fetch        Download the Inno Setup 6.7.3 bootstrap.
        iscc              Extract ISCC.exe from the cached bootstrap.
        vet               Run `go vet ./...`.
        version           Print resolved build version.
        clean             Remove bin/, dist/, and .tools/.

.PARAMETER Target
    The target name. Defaults to 'build' (matches `make build`'s default
    on Windows hosts).

.PARAMETER Go
    Path to the `go` binary. Defaults to whatever is on PATH.

.PARAMETER Python
    Path to `python`. Defaults to whatever is on PATH. Used by the
    `icon` target.

.PARAMETER Curl
    Path to `curl`. Defaults to whatever is on PATH. Used by
    `iscc-fetch`.

.PARAMETER Innounp
    Path to `innounp.exe`. Defaults to whatever is on PATH.

.PARAMETER IsccVersion
    Inno Setup version to fetch. Defaults to 6.7.3.

.PARAMETER ToolsDir
    Directory holding cached, downloaded tools (ISCC.exe, etc).
    Defaults to .tools/inno relative to the repo root.

.EXAMPLE
    pwsh -File scripts/build.ps1 build
    pwsh -File scripts/build.ps1 installer -IsccVersion 6.7.3
    pwsh -File scripts/build.ps1 clean
#>
[CmdletBinding()]
param(
    # Accept the target either as a named parameter or as the first
    # positional argument. PowerShell's `help` is a built-in alias for
    # `Get-Help`, which can swallow the `-Target help` form, so we also
    # recognise the bare positional.
    [Parameter(Position = 0)]
    [ValidateSet('help','build','build-windows','icon','installer','iscc-fetch','iscc','vet','version','clean')]
    [string]$Target = 'build',
    [string]$Go = 'go',
    [string]$Python = 'python',
    [string]$Curl = 'curl',
    [string]$Innounp = 'innounp',
    [string]$IsccVersion = '6.7.3',
    [string]$ToolsDir
)

# ----------------------------------------------------------------------------
# Helpers — defined BEFORE the dispatcher switch. PowerShell resolves
# function names lazily, but on some shells (and under strict mode
# implied by [CmdletBinding()]) functions defined later in the same
# script aren't visible at the call site, so we put them first.
# ----------------------------------------------------------------------------

function Show-Help {
    Write-Host 'lgo_download_manager build driver'
    Write-Host ''
    Write-Host 'Usage: pwsh -File scripts/build.ps1 -Target <target> [options]'
    Write-Host ''
    Write-Host 'Targets:'
    Write-Host '  help         show this help'
    Write-Host '  build        build ldm.exe into bin/'
    Write-Host '  build-windows alias for build'
    Write-Host '  icon         regenerate assets/ldm.ico'
    Write-Host '  installer    fetch ISCC + compile installer/installer.iss'
    Write-Host '  iscc-fetch   download Inno Setup 6.7.3 bootstrap'
    Write-Host '  iscc         extract ISCC.exe from the cached bootstrap'

    Write-Host '  vet          go vet ./...'
    Write-Host ''
    Write-Host 'The Makefile in the repo root simply forwards each of these to'
    Write-Host 'this script, so users on Windows can keep typing `make <target>`.'
}

function Get-ToolsDir {
    if ($ToolsDir) { return $ToolsDir }
    return (Join-Path $RepoRoot '.tools/inno')
}

function Get-BootstrapPath {
    $dir = Get-ToolsDir
    return (Join-Path $dir "innosetup-$IsccVersion.exe")
}

function Get-IsccPath {
    $dir = Get-ToolsDir
    return (Join-Path $dir 'ISCC.exe')
}

function Get-Version {
    # Mirror the Makefile's VERSION / COMMIT / BUILD_DATE resolution so the
    # numbers baked into the binary are identical whether the user calls
    # `make build` or `pwsh -File scripts/build.ps1 build`.
    $version = '0.0.0-dev'
    $commit  = 'unknown'
    $date    = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    try {
        $v = (& git describe --tags --always --dirty 2>$null)
        if ($v) { $version = $v }
    } catch { }
    try {
        $c = (& git rev-parse --short HEAD 2>$null)
        if ($c) { $commit = $c }
    } catch { }
    return [pscustomobject]@{ Version = $version; Commit = $commit; Date = $date }
}

function Show-Version {
    $v = Get-Version
    $goos   = (& $Go env GOOS) | Select-Object -First 1
    $goarch = (& $Go env GOARCH) | Select-Object -First 1
    Write-Host "VERSION=$($v.Version)"
    Write-Host "COMMIT=$($v.Commit)"
    Write-Host "BUILD_DATE=$($v.Date)"
    Write-Host "GOOS=$goos GOARCH=$goarch"
}

function Get-LdFlags {
    $v = Get-Version
    return @(
        '-s','-w',
        "-X","lgo_download_manager/internal/version.Version=$($v.Version)",
        "-X","lgo_download_manager/internal/version.Commit=$($v.Commit)",
        "-X","lgo_download_manager/internal/version.Date=$($v.Date)"
    )
}

function Invoke-Go {
    # NB: parameter is $GocmdArgs (not $Args) because PowerShell's
    # automatic $Args variable shadows explicit params at call sites.
    param(
        [Parameter(Mandatory)] [string]$Cmd,
        [string[]]$GocmdArgs = @()
    )
    Write-Host ">>> $Go $Cmd $($GocmdArgs -join ' ')"
    & $Go $Cmd @GocmdArgs
    if ($LASTEXITCODE -ne 0) {
        throw "$Go $Cmd failed with exit code $LASTEXITCODE"
    }
}

function Invoke-Build {
    param([string]$Goos = 'windows', [string]$Goarch = 'amd64')
    $binDir  = Join-Path $RepoRoot 'bin'
    $outPath = Join-Path $binDir 'ldm.exe'
    New-Item -ItemType Directory -Path $binDir -Force | Out-Null

    $v = Get-Version
    $env:LDM_VERSION = $v.Version
    $ldflags = (Get-LdFlags) -join ' '

    $env:GOOS = $Goos
    $env:GOARCH = $Goarch
    try {
        Write-Host ">>> $Go build -trimpath -ldflags '$ldflags' -o $outPath ."
        & $Go build -trimpath -ldflags "$ldflags" -o $outPath .
        if ($LASTEXITCODE -ne 0) {
            throw "$Go build failed with exit code $LASTEXITCODE"
        }
    } finally {
        Remove-Item Env:GOOS -ErrorAction SilentlyContinue
        Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
    }
    Write-Host "built $outPath"
}

function Invoke-Icon {
    $ico = Join-Path $RepoRoot 'assets/ldm.ico'
    $py  = Join-Path $RepoRoot 'scripts/gen_icon.py'
    New-Item -ItemType Directory -Path (Split-Path $ico) -Force | Out-Null
    Write-Host ">>> $Python $py"
    & $Python $py
    if ($LASTEXITCODE -ne 0) {
        throw "$Python $py failed with exit code $LASTEXITCODE"
    }
}

function Invoke-IsccFetch {
    $dst = Get-BootstrapPath

    # A previous run may have been killed mid-download (timeout / network
    # drop), leaving a corrupt bootstrap on disk. innounp will refuse such
    # files with "setup files are corrupted", so probe with innounp before
    # trusting Test-Path and skipping the fetch.
    if (Test-Path $dst) {
        & $Innounp -l $dst *> $null
        if ($LASTEXITCODE -eq 0) {
            Write-Host "$dst already present; remove it to force a re-download."
            return
        }
        Write-Host "$dst is corrupt (innounp exit=$LASTEXITCODE); re-downloading."
        Remove-Item -Path $dst -Force
    }

    $dir = Get-ToolsDir
    New-Item -ItemType Directory -Path $dir -Force | Out-Null

    $versionSlug = $IsccVersion -replace '\.', '_'
    $url = "https://github.com/jrsoftware/issrc/releases/download/is-$versionSlug/innosetup-$IsccVersion.exe"
    Write-Host "fetching $url"
    try {
        # -C - lets curl resume an interrupted download instead of starting
        # over; a 10 MB file at ~400 KB/s takes ~25 s on a healthy link but
        # can blow past a short harness timeout otherwise. We pair it with
        # an explicit truncate via -O on the same file, so a corrupt
        # partial detected above gets overwritten cleanly instead of
        # having the resumed bytes appended to bad header data.
        if (Test-Path $dst) { Remove-Item -Path $dst -Force }
        & $Curl -fL -C - -A 'Mozilla/5.0' --create-dirs -o $dst $url
        if ($LASTEXITCODE -ne 0) { throw "curl failed ($LASTEXITCODE)" }
        # Verify before declaring success — a 200-from-cache CDN can serve
        # an HTML error page that passes -fL on some redirect chains.
        & $Innounp -l $dst *> $null
        if ($LASTEXITCODE -ne 0) {
            throw "innounp rejected the downloaded file (exit=$LASTEXITCODE); the archive is not a valid Inno Setup setup."
        }
    } catch {
        Write-Error @"
ERROR: Inno Setup bootstrap download failed.

  $_

  GitHub may be blocked by your network (some sandboxes / corporate
  proxies reset the connection mid-stream). To work around this,
  download innosetup-$IsccVersion.exe manually from any browser and
  place it at:

    $dst

  Then re-run `pwsh -File scripts/build.ps1 installer`; the download
  step will be skipped because the file already exists.
"@
        Remove-Item -Path $dst -ErrorAction SilentlyContinue
        throw $_
    }
    Write-Host "downloaded $url"
}

function Invoke-IsccExtract {
    $dst  = Get-IsccPath
    if (Test-Path $dst) {
        Write-Host "$dst already present; remove it to force a re-extract."
        return
    }
    $bootstrap = Get-BootstrapPath
    if (-not (Test-Path $bootstrap)) {
        throw "bootstrap not found: $bootstrap  (run iscc-fetch first)"
    }
    $dir = Get-ToolsDir

    # ISCC.exe is only one of the runtime files in the Inno Setup 6.7.x
    # bootstrap — it also needs ISCmplr.dll, islzma.dll, ISPP.dll, the
    # Languages\*.isl translations, etc. Extract everything under {app}\
    # so the in-place layout is identical to what ISCC expects at compile
    # time.
    #
    # innounp 2.71.x changed the -d semantics: only the joined form
    # "-d<dir>" is honored (space-separated "-d <dir>" is silently
    # interpreted as "input file = <dir>"). Anything older than 2.71
    # would accept "-d <dir>", so the joined form works across versions.
    $tmp = Join-Path $dir '_extract'
    if (Test-Path $tmp) { Remove-Item -Recurse -Force $tmp }
    New-Item -ItemType Directory -Path $tmp -Force | Out-Null

    Write-Host "extracting ISCC runtime from $bootstrap"
    & $Innounp -b -x "-d$tmp" $bootstrap '{app}\*'
    if ($LASTEXITCODE -ne 0) {
        throw "innounp failed with exit code $LASTEXITCODE"
    }

    # Flatten _extract\{app}\* into $dir so ISCC.exe + its DLLs sit side
    # by side (the layout ISCC probes at runtime).
    $appDir = Join-Path $tmp '{app}'
    if (-not (Test-Path $appDir)) {
        throw "innounp did not produce $appDir; aborting"
    }
    Get-ChildItem $appDir | ForEach-Object {
        Move-Item -Force -Path $_.FullName -Destination $dir
    }
    Remove-Item -Recurse -Force $tmp
    Write-Host "ISCC.exe extracted to $dst"
}

function Invoke-Installer {
    # Ensure the binary + icon + ISCC.exe are all available before invoking
    # ISCC on the .iss. Each step is a no-op if its target is up to date.
    Invoke-Build -Goos 'windows' -Goarch 'amd64'
    Invoke-Icon
    Invoke-IsccFetch
    Invoke-IsccExtract

    $distDir = Join-Path $RepoRoot 'dist'
    New-Item -ItemType Directory -Path $distDir -Force | Out-Null

    $iss = Join-Path $RepoRoot 'installer/installer.iss'
    $iscc = Get-IsccPath

    # Compute the 4-part Windows version that VersionInfoVersion demands
    # from the same git-describe string we use for AppVersion. Strip a
    # leading 'v' (git tag convention) and the dirty/commit-suffix, then
    # append ".0" so the installer is always "X.Y.Z.0".
    $v = Get-Version
    $raw = $v.Version -replace '^v', ''
    $semver = ($raw -split '-')[0]
    if ($semver -notmatch '^\d+\.\d+\.\d+$') { $semver = '0.0.0' }
    $env:LDM_WIN_VERSION = "$semver.0"
    Write-Host "LDM_VERSION=$($env:LDM_VERSION)"
    Write-Host "LDM_WIN_VERSION=$env:LDM_WIN_VERSION"

    Write-Host ">>> $iscc $iss"
    & $iscc $iss
    if ($LASTEXITCODE -ne 0) {
        throw "ISCC failed with exit code $LASTEXITCODE"
    }
    Write-Host "installer written to $distDir/"
}

function Invoke-Clean {
    foreach ($sub in @('bin','dist','.tools')) {
        $p = Join-Path $RepoRoot $sub
        if (Test-Path $p) {
            Write-Host "removing $p"
            Remove-Item -Recurse -Force $p
        }
    }
}

# ----------------------------------------------------------------------------
# Dispatcher
# ----------------------------------------------------------------------------

# Resolve repo root (the directory two levels up from this script). Has to
# happen after the helper functions are defined because they read it.
$RepoRoot = (Resolve-Path -Path (Join-Path $PSScriptRoot '..')).Path
Push-Location $RepoRoot
try {
    switch ($Target) {
        'help'          { Show-Help }
        'version'       { Show-Version }
        'build'         { Invoke-Build }
        'build-windows' { Invoke-Build }
        'icon'          { Invoke-Icon }
        'iscc-fetch'    { Invoke-IsccFetch }
        'iscc'          { Invoke-IsccExtract }
        'installer'     { Invoke-Installer }
        'vet'           { Invoke-Go -Cmd 'vet'  -GocmdArgs @('./...') }
        'clean'         { Invoke-Clean }
        default         { throw "unknown target: $Target" }
    }
}
finally {
    Pop-Location
}