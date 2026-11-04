# 持续集成

三个工作流分别生成桌面端和浏览器扩展产物：

| 工作流 | job | runner | 脚本 / 命令 | 产物 |
| --- | --- | --- | --- | --- |
| `windows.yml` | `windows-x64` | `windows-latest` | `scripts/package_native.ps1 -Arch x64` | x64 MSI/EXE |
| `windows.yml` | `windows-arm64` | `windows-11-arm` | `scripts/package_native.ps1 -Arch arm64` | arm64 MSI/EXE |
| `linux.yml` | `flatpak-x86_64` | `ubuntu-24.04` | `scripts/build_flatpak_native.sh --arch=x86_64` | x86_64 Flatpak |
| `linux.yml` | `flatpak-aarch64` | `ubuntu-24.04-arm` | `scripts/build_flatpak_native.sh --arch=aarch64` | aarch64 Flatpak |
| `extension.yml` | `extension-chrome` | `ubuntu-latest` | `npm run package:chrome` | Chrome zip/crx |
| `extension.yml` | `extension-firefox` | `ubuntu-latest` | `npm run package:firefox` | Firefox zip/xpi |

桌面端按架构拆成独立 job，在原生架构 runner 上构建，不做交叉编译。产物作为
artifact 上传并保留 30 天；只有 `v*` tag 会运行 release job，把全部 artifact 汇总
发布到 GitHub Release。触发条件为 push 到 `master`、`v*` tag、PR 和手动
`workflow_dispatch`。

## 原生脚本与交叉脚本

| | `package.ps1` / `build_flatpak.sh` | `package_native.ps1` / `build_flatpak_native.sh` |
| --- | --- | --- |
| 目标架构 | 可一次生成多个，允许交叉 | 只生成宿主架构 |
| 工具链 | 交叉编译器 | 宿主 gcc / clang |
| Linux 额外依赖 | 跨架构时需要 qemu | 无 |
| 架构校验 | 有 | 有，预检即拒绝不匹配 |

Flatpak 原生构建仍使用目标 runtime 的 SDK 作为 sysroot，确保二进制链接它要运行的
libc、GL 与 X11。

## Windows 工具链

两个 Windows job 都先运行：

```powershell
scripts/package_native.ps1 -Arch <arch> -PrintToolchain
```

脚本选择宿主架构一致的 gcc + g++，把 `CC` / `CXX` 写入 `GITHUB_ENV`，后续打包步骤
直接使用。`windows-11-arm` 镜像 PATH 中的 mingw 可能是 x64，因此脚本会下载
LLVM-MinGW 的 aarch64 包，在 arm64 宿主上执行原生编译。

`windows-11-arm` 不带 winget，所以 WiX 使用 RID 无关的 .NET 工具包，LLVM-MinGW
使用官方 release zip 并校验 SHA256；Inno Setup 已预装。arm64 job 每次会下载约
181 MB 的 LLVM-MinGW zip，目前没有缓存。

## Linux SDK 安装

CI 用 `sudo flatpak remote-add` / `sudo flatpak install` 把 runtime 和 SDK 装进
系统安装 `/var/lib/flatpak`：

- 普通用户没有 `ConfigureRemote` 权限，不加 sudo 会报
  `Flatpak system operation ConfigureRemote not allowed for user`。
- 脚本中的 `flatpak info` 和 `flatpak-builder` 默认按系统安装解析，用户安装中的
  SDK 找不到。

flathub 使用 `https://dl.flathub.org/repo/flathub.flatpakrepo` 的未过滤 URL，
因为 Fedora 等发行版自带 flathub 可能拿不到 arm64 ref。

## 已知限制

- `ubuntu-24.04-arm` 和 `windows-11-arm` 这两个 arm64 runner 只在公开仓库可用。
  仓库转私有后需要改回 x64 runner + 交叉编译。
- Windows arm64 job 尚未在 CI 真实运行；首次运行就是验证。
- Linux x86_64 job 与本地原生脚本路径一致，已实测。
- 扩展包版本号取 tag 去掉 `v`。`CHROME_EXTENSION_KEY`、`AMO_API_KEY`、
  `AMO_API_SECRET` 的语义见 [扩展 README](../../lgom-extension/README.md)。
