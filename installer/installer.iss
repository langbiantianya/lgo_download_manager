; Inno Setup script for lgo_download_manager (ldm).
;
; Build with:    iscc installer/installer.iss
; Or via make:  make installer
;
; Designed for a per-user (non-admin) install under
; %LOCALAPPDATA%\Programs\lgo_download_manager, matching the
; recommendation in Microsoft's Desktop Bridge / WinGet conventions.
; Because we only touch HKCU and the user's profile, no UAC prompt
; is required (PrivilegesRequired=lowest).
;
; URL protocol registration:
;   The lgom:// scheme is registered at HKCU\Software\Classes\lgom so
;   browsers / the Windows shell know to invoke ldm.exe with the URL
;   as argv[1]. The primary instance parses positional lgom:// args in
;   main.go and dispatches them directly (no IPC forwarder is required
;   for the primary path). On Windows, secondary-instance URL
;   forwarding is intentionally not implemented (see
;   internal/urllauncher/urllauncher_windows.go); the primary still
;   gets the URL because the OS launches it on first invocation.

#define MyAppName "lgo_download_manager"
#define MyAppExeName "ldm.exe"
#define MyAppPublisher "langbiantianya"
#define MyAppURL "https://github.com/langbiantianya/lgo_download_manager"
#define MyAppId "{B4D9C2A1-3F7E-4B6A-9C5D-1E8F2A3B4C5D}"

; Build version — read from the LDM_VERSION env var (set by scripts/build.ps1
; from `git describe`), falling back to "0.0.0-dev" when unset. ISPP's GetEnv
; returns an empty string for unset variables, so we coalesce with the
; ternary below. ReadEnv(name, default) was removed in Inno Setup 6; the
; replacement is GetEnv(name).
#define MyAppVersionStr GetEnv("LDM_VERSION")
#if MyAppVersionStr == ""
  #define MyAppVersionStr "0.0.0-dev"
#endif

; VersionInfoVersion requires a strict 4-component Windows version
; (X.Y.Z.W) and refuses the "v0.1.0-3-gabc-dirty" form that AppVersion
; accepts. The build script sets LDM_WIN_VERSION to a clean 4-part number
; (commits-since-tag → Z.W, or 0.0.0.0 on a tagged release); when missing
; we fall back to 0.0.0.0 so a hand-invoked iscc still produces a valid
; installer.
#define MyAppWinVersionStr GetEnv("LDM_WIN_VERSION")
#if MyAppWinVersionStr == ""
  #define MyAppWinVersionStr "0.0.0.0"
#endif
[Setup]
AppId={{#MyAppId}}
AppName={#MyAppName}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppVersion={#MyAppVersionStr}
VersionInfoVersion={#MyAppWinVersionStr}
ArchitecturesAllowed=x86compatible
ArchitecturesInstallIn64BitMode=x86compatible

; Per-user install — no admin required.
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
AllowNoIcons=yes

; Install under %LOCALAPPDATA%\Programs (Microsoft's recommended
; location for per-user, non-admin installs). AppendVersionToSubDirectory
; is left off so that the install path is stable across upgrades —
; important because lgom:// registration hard-codes it into the registry.
DefaultDirName={{autolocalappdata}}\Programs\lgo_download_manager
DisableProgramGroupPage=yes

; GUI styling.
WizardStyle=modern
Compression=lzma2/ultra64
SolidCompression=yes
LZMAUseSeparateProcess=yes
MergeDuplicateFiles=yes

; ISCC runs with CWD = installer/, so the SetupIconFile path needs to
; climb back to the repo root. OutputBaseFilename/OutputDir are anchored
; on CWD too, so OutputDir=dist lands in installer/dist by default; we
; override with an absolute path to keep the artifact at the repo root
; alongside bin/ — matches the path scripts/build.ps1 prints.
OutputBaseFilename=ldm-setup-{#MyAppVersionStr}
OutputDir={#SourcePath}\..\dist
SetupIconFile={#SourcePath}\..\assets\ldm.ico
; Anchor source paths at the repo root so bin/ + LICENSE resolve the same
; way they do from the build script's CWD.
SourceDir={#SourcePath}\..\
UninstallDisplayIcon={{app}\{#MyAppExeName}},0
UninstallDisplayName={#MyAppName}

DisableFinishedPage=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{{cm:CreateDesktopIcon}}"; GroupDescription: "{{cm:AdditionalIcons}}"; Flags: unchecked
Name: "startmenu"; Description: "Create a Start menu shortcut"; GroupDescription: "Additional shortcuts:"; Flags: checkedonce

[Files]
; Source points at the build output produced by `make build-windows`.
Source: "bin\{#MyAppExeName}"; DestDir: "{{app}}"; Flags: ignoreversion
Source: "LICENSE"; DestDir: "{{app}}"; Flags: ignoreversion

[Dirs]
; The lock / instance directory used by urllauncher on Windows.
; Creating it at install time avoids first-run MkdirAll latency and
; makes the install fully offline.
Name: "{{userappdata}}\lgo_download_manager"

[Icons]
Name: "{{autoprograms}\{#MyAppName}}"; Filename: "{{app}\{#MyAppExeName}}"; Tasks: startmenu
Name: "{{autodesktop}\{#MyAppName}}"; Filename: "{{app}\{#MyAppExeName}}"; Tasks: desktopicon

[Registry]
; -------------------------------------------------------------------
; lgom:// URL scheme — user-scope registration.
;
; We use HKCU\Software\Classes\lgom rather than HKLM\Software\Classes
; so the install needs no admin rights. HKCU is consulted before HKLM
; for shell-open associations, so this works for the user that ran the
; installer even if HKLM has no entry.
;
; The three required pieces:
;   1. HKCU\Software\Classes\lgom — declares the protocol (default
;      value + the magic empty "URL Protocol" string).
;   2. ...\lgom\DefaultIcon        — icon used by browsers / shell.
;   3. ...\lgom\shell\open\command — actual launcher command line.
;
; The "%1" placeholder is replaced by the OS with the full URL the
; user clicked. ldm.exe's main() collects positional lgom:// args and
; dispatches them on the primary instance.
; -------------------------------------------------------------------
Root: HKCU; Subkey: "Software\Classes\lgom"; ValueType: string; ValueName: "";        ValueData: "URL:lgom Protocol"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\lgom"; ValueType: string; ValueName: "URL Protocol"; ValueData: "";               Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\lgom"; ValueType: string; ValueName: "FriendlyTypeName"; ValueData: "lgom Download Protocol"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\lgom\DefaultIcon"; ValueType: string; ValueName: ""; ValueData: """{{app}\{#MyAppExeName}}"",0"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\lgom\shell\open\command"; ValueType: string; ValueName: ""; ValueData: """{{app}\{#MyAppExeName}}"" ""%1"""; Flags: uninsdeletekey
; Advertise capability hint — modern browsers (Edge, Chrome) read this
; to suggest the handler when the user pastes an lgom:// URL.
Root: HKCU; Subkey: "Software\Classes\lgom"; ValueType: dword; ValueName: "EditFlags"; ValueData: "2"; Flags: uninsdeletekey

[Run]
; Offer to launch after install. Only fires if the user leaves the
; checkbox on the Finished page enabled.
Filename: "{{app}\{#MyAppExeName}}"; Description: "{{cm:LaunchProgram,{#MyAppName}}}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; Try to stop any running instance before uninstalling the binary.
; fyne.io/systray + graceful shutdown takes ~1s on quit; we give it 5.
Filename: "{{cmd}}"; Parameters: "/C taskkill /IM {#MyAppExeName} /F"; Flags: runhidden; RunOnceId: "StopLdmBeforeUninstall"

[UninstallDelete]
; The instance lock file lives in %APPDATA% and is recreated on next
; launch — clean it up so a stale lock doesn't survive the uninstall.
Type: files; Name: "{{userappdata}}\lgo_download_manager\instance.lock"