; Inno Setup script for lgo_download_manager (lgdm) — EXE 安装包。
;
; 由 scripts/package.ps1 调用,一般不手工执行:
;     ISCC.exe installer\installer.iss
;
; 设计要点:
;   - 用户态安装(PrivilegesRequired=lowest),目录是
;     %LOCALAPPDATA%\Programs\lgo_download_manager,不需要 UAC。
;   - 写 HKCU\Software\Classes\lgom 注册 lgom:// 协议,命令行是
;     "<install>\lgdm.exe" "%1"。浏览器/资源管理器把 URL 作为 argv[1]
;     交给新进程;若主实例已在运行,新进程会通过命名管道把 URL 转发
;     过去再退出(见 internal/urllauncher/urllauncher_windows.go)。
;   - 安装/升级/卸载前都要结束正在运行的 lgdm(托盘常驻,exe 被占用会
;     让文件替换失败):安装时 [Code] 的 PrepareToInstall 直接 taskkill,
;     配合 CloseApplications 的 Restart Manager 兜底;卸载时 UninstallRun
;     里 taskkill。
;
; 版本号来自环境变量,由 scripts/package.ps1 设置:
;     LDM_VERSION      AppVersion,形如 v0.1.0-3-gabc1234
;     LDM_WIN_VERSION  VersionInfoVersion 需要的 X.Y.Z.W 数字版本
;
; 架构由 scripts/package.ps1 用 /DMyArch=x64|arm64 传入(缺省 x64):
;     x64   → ArchitecturesAllowed=x64compatible,载荷 bin\lgdm-x64.exe
;     arm64 → ArchitecturesAllowed=arm64,          载荷 bin\lgdm-arm64.exe
; Inno 6 的架构标识只有 arm64 / x64compatible / x86compatible / arm32compatible,
; 没有 arm64compatible(用了会编译报错)。

#ifndef MyArch
  #define MyArch "x64"
#endif

#if MyArch == "arm64"
  #define MyArchAllowed "arm64"
  #define MyArchInstallMode "arm64"
  #define MyAppSourceExe "lgdm-arm64.exe"
#else
  #define MyArchAllowed "x64compatible"
  #define MyArchInstallMode "x64compatible"
  #define MyAppSourceExe "lgdm-x64.exe"
#endif

#define MyAppName "lgo_download_manager"
; 装到目标机器上的文件名固定是 lgdm.exe(协议注册表里的命令行、托盘/UI 自复制
; 都按这个名字找),架构只体现在 bin\ 里的待打包文件名上。
#define MyAppExeName "lgdm.exe"
#define MyAppPublisher "langbiantianya"
#define MyAppURL "https://github.com/langbiantianya/lgo_download_manager"
; AppId 是卸载/升级的身份标识;Inno 里 "{{" 转义成字面量 "{"。
#define MyAppId "{{B4D9C2A1-3F7E-4B6A-9C5D-1E8F2A3B4C5D}"

#define MyAppVersionStr GetEnv("LDM_VERSION")
#if MyAppVersionStr == ""
  #define MyAppVersionStr "0.0.0-dev"
#endif

; VersionInfoVersion 只接受严格的 4 段数字(X.Y.Z.W),不接受
; "v0.1.0-3-gabc1234" 这类 git describe 结果,因此单独取一个变量。
#define MyAppWinVersionStr GetEnv("LDM_WIN_VERSION")
#if MyAppWinVersionStr == ""
  #define MyAppWinVersionStr "0.0.0.0"
#endif

[Setup]
AppId={#MyAppId}
AppName={#MyAppName}
AppVersion={#MyAppVersionStr}
AppVerName={#MyAppName} {#MyAppVersionStr}
VersionInfoVersion={#MyAppWinVersionStr}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
UninstallDisplayName={#MyAppName}
UninstallDisplayIcon={app}\{#MyAppExeName},0

; 用户态安装:只写 HKCU 和自己的 profile。
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
DefaultDirName={localappdata}\Programs\lgo_download_manager
; 目录在升级之间保持稳定 —— lgom:// 注册表里的命令行会硬编码它。
DisableDirPage=auto
DisableProgramGroupPage=yes
AllowNoIcons=yes

ArchitecturesAllowed={#MyArchAllowed}
ArchitecturesInstallIn64BitMode={#MyArchInstallMode}

; 安装/升级前让 Restart Manager 关掉正在运行的 lgdm,否则 exe 被占用
; 会变成「重启后替换」。
CloseApplications=yes
RestartApplications=no

; GUI 与压缩。
WizardStyle=modern
Compression=lzma2/ultra64
SolidCompression=yes
LZMAUseSeparateProcess=yes
MergeDuplicateFiles=yes

; 路径一律锚在脚本所在目录,便于从任意 CWD 调用 ISCC。
OutputDir={#SourcePath}\..\dist
OutputBaseFilename=lgdm-setup-{#MyAppVersionStr}-{#MyArch}
SetupIconFile={#SourcePath}\..\assets\lgdm.ico

[Languages]
; Inno Setup 官方只带 Default.isl(英文);中文界面需要外部 .isl,
; 这里不引入,保证官方安装器开箱即可编译。
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "startmenu"; Description: "创建开始菜单快捷方式"; GroupDescription: "快捷方式:"; Flags: checkedonce
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "快捷方式:"; Flags: unchecked

[Files]
Source: "{#SourcePath}\..\bin\{#MyAppSourceExe}"; DestDir: "{app}"; DestName: "{#MyAppExeName}"; Flags: ignoreversion
Source: "{#SourcePath}\..\assets\lgdm.ico"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourcePath}\..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; WorkingDir: "{app}"; IconFilename: "{app}\lgdm.ico"; Tasks: startmenu
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; WorkingDir: "{app}"; IconFilename: "{app}\lgdm.ico"; Tasks: desktopicon

[Registry]
; -------------------------------------------------------------------
; lgom:// 协议 —— 用户级注册(HKCU\Software\Classes,无需提权)。
; 三件套:声明协议、指定图标、给出命令行。
; -------------------------------------------------------------------
Root: HKCU; Subkey: "Software\Classes\lgom"; ValueType: string; ValueName: ""; ValueData: "URL:lgom Protocol"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\lgom"; ValueType: string; ValueName: "URL Protocol"; ValueData: ""; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\lgom"; ValueType: string; ValueName: "FriendlyTypeName"; ValueData: "lgom Download Protocol"; Flags: uninsdeletekey
; 现代浏览器(Edge/Chrome)读这个提示位,粘贴 lgom:// URL 时用来建议处理程序。
Root: HKCU; Subkey: "Software\Classes\lgom"; ValueType: dword; ValueName: "EditFlags"; ValueData: "2"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\lgom\DefaultIcon"; ValueType: string; ValueName: ""; ValueData: """{app}\lgdm.ico"",0"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\lgom\shell\open\command"; ValueType: string; ValueName: ""; ValueData: """{app}\{#MyAppExeName}"" ""%1"""; Flags: uninsdeletekey

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "运行 {#MyAppName}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; 卸载前结束正在运行的实例(exe 被占用会导致文件删不掉)。
Filename: "{cmd}"; Parameters: "/C taskkill /IM {#MyAppExeName} /F"; Flags: runhidden; RunOnceId: "StopLdmBeforeUninstall"

[UninstallDelete]
; 用户数据目录(lgdm.sqlite 任务库 + wal/shm + UI IPC 的 ui-*.sock)。
; 它不是安装包创建的,而是程序首次运行时建的,所以得显式删。
; UninstallDelete 只在卸载时执行 —— 覆盖安装/升级不会走到这里,
; 用户的任务列表不会因为升级而丢。
Type: filesandordirs; Name: "{localappdata}\lgo_download_manager"

[Code]
// 安装/升级前同样要结束正在运行的实例:lgdm 常驻托盘,静默安装时
// CloseApplications 没有用户可以询问,直接自己 taskkill 最确定。
// 找不到进程时 taskkill 返回 128,忽略即可。
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
begin
  Exec(ExpandConstant('{cmd}'), '/C taskkill /IM {#MyAppExeName} /F',
    '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Result := '';
end;
