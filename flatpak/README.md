# Flatpak 安装包

一条命令完成「按架构编译二进制 + 生成 Flatpak」，默认生成 x86_64 和 aarch64：

```sh
./scripts/build_flatpak.sh
./scripts/build_flatpak.sh --arch=x86_64
./scripts/build_flatpak.sh --install
```

只生成宿主架构（CI 用法）：

```sh
./scripts/build_flatpak_native.sh --arch=x86_64
./scripts/build_flatpak_native.sh --arch=aarch64
```

原生脚本不交叉编译，不需要 qemu/交叉编译器；`--arch` 与宿主架构不符时会在预检
阶段报错。

## 环境要求

| 依赖 | 说明 |
| --- | --- |
| `flatpak` / `flatpak-builder` | 构建与安装 |
| Go + C 工具链 | 二进制在宿主机上按目标架构编译，Fyne 需要 cgo |
| 目标架构 SDK/runtime | `flatpak install --user --arch=<架构> flathub org.freedesktop.Sdk//26.08 org.freedesktop.Platform//26.08` |
| 跨架构：qemu-user-static | 让本机执行目标架构的构建步骤；以 binfmt 注册为生效判据 |
| 跨架构：目标 C 编译器 | 例如 `gcc-aarch64-linux-gnu`，可用 `--cc-aarch64` 指定 |

SDK 是构建期硬依赖，作为 sysroot 使用；只安装 Platform 无法出包。首次下载约 1 GB。
Fedora 等发行版自带的 flathub 可能过滤非本机架构 ref，需要添加未过滤远端。

不需要在宿主机安装目标架构的 X11/GL/wayland 开发包。构建时用目标架构 Flatpak SDK
生成 `dist/flatpak/<架构>/sysroot`，头文件、库、pkg-config 和 glibc 都与运行时
匹配。

## 命令与参数

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `--arch` | `x86_64,aarch64` | 逗号分隔，接受 `amd64`/`x64`、`arm64`/`aarch64` |
| `--version` | `git describe --tags --always --dirty` | 写入二进制与安装包文件名 |
| `--skip-build` | 关 | 复用已有的 `bin/lgo_download_manager-<架构>`，校验 ELF 架构 |
| `--install` | 关 | 出包后安装本机架构那份 |
| `--cc-x86_64` / `--cc-aarch64` | 自动查找 | 指定对应架构 C 编译器 |

## 产物

```text
dist/lgdm-<版本>-x86_64.flatpak
dist/lgdm-<版本>-aarch64.flatpak
bin/lgo_download_manager-<架构>
dist/flatpak/<架构>/build-dir/
dist/flatpak/<架构>/repo/
dist/flatpak/<架构>/state/
dist/flatpak/<架构>/sysroot/
```

Flatpak bundle 与架构绑定，不能合并。manifest 用 `only-arches` module 安装对应
二进制，`flatpak-builder --arch=X` 缺文件会直接报错，避免串架构。安装与卸载：

```sh
flatpak install --user dist/lgdm-<版本>-<架构>.flatpak
flatpak uninstall --user org.langbiantianya.LGDM
```

## 沙箱行为

- **开机自启**：宿主 `~/.config/autostart/lgo_download_manager.desktop` 使用
  `flatpak run org.langbiantianya.LGDM --autostart`，并带
  `X-Flatpak=org.langbiantianya.LGDM`。
- **配置与数据库**：`--filesystem=home` 使沙箱访问宿主家目录，
  `~/.config/lgo_download_manager` 与 `~/.local/share/lgo_download_manager` 和原生版
  共用；单实例锁保证只运行一个实例。
- **`lgom://`**：Flatpak 导出带 `MimeType=x-scheme-handler/lgom` 的桌面文件。
- **本地端口**：`--share=network` 让沙箱与宿主共用网络命名空间，扩展探测
  `127.0.0.1:17821` 即可发现实例。
- **托盘**：`--socket=session-bus`；GNOME 需要 AppIndicator 扩展才显示图标。
- **路径**：`--filesystem=home` 与 `host` 暴露宿主文件，但沙箱 `/tmp` 是私有
  tmpfs，因此 manifest 额外授权 `--filesystem=/tmp`，避免任务保存到 `/tmp` 后随
  沙箱退出丢失。

## 已验证与限制

本机 Fedora 44、Flatpak 1.18.4、flatpak-builder 1.4.12、qemu-user-static 与
gcc-aarch64-linux-gnu 环境下已验证：

- 默认脚本一次生成 x86_64 与 aarch64 两份包（约 11 MB / 9.7 MB），AppStream 与
  desktop-file 校验通过；缓存齐全后约 30 秒完成。
- 编译头文件、glibc 和图形库来自目标架构 SDK，aarch64 产物在沙箱中解析到
  `/usr/lib/aarch64-linux-gnu/*`，`uname -m` 为 `aarch64`。
- 两个架构可以并存于用户安装；`flatpak run` 默认本机架构，
  `flatpak run --arch=aarch64` 指定另一个。
- 两个架构均验证了沙箱中的自启开关；aarch64 用 qemu 运行。
- Flatpak 导出的 `Exec` 使用位置参数 `%u`，而不是 `--open-url`。
- 架构守卫会拒绝 `--skip-build` 传入的错误架构二进制。
- Flatpak 实例在宿主 `127.0.0.1` 上监听，Chromium 与 Firefox 扩展均完成端口交接，
  没有回退到协议通道。
- 原生脚本在 x86_64 上可一条命令出包；在 x86_64 上传 `--arch=aarch64` 会被预检拒绝。

限制：

- GNOME 托盘依赖 AppIndicator 扩展。
- 产物是未签名单文件 bundle，没有仓库签名或 OSTree remote 发布流程。
- x86_64 上构建 aarch64 需要 qemu；原生 aarch64 机器不需要 qemu 或交叉编译器。

## 排错

| 现象 | 原因 / 处理 |
| --- | --- |
| `本机无法执行 aarch64 的构建步骤` | 安装 `qemu-user-static`，确认 binfmt 为 enabled |
| `缺少 aarch64 的构建依赖 runtime/...Sdk...` | 按脚本提示安装目标架构 SDK 与 Platform；只装 Platform 不够 |
| `未发现用于 "flathub" 的远程引用` | 发行版 flathub 被过滤；添加 `https://dl.flathub.org/repo/flathub.flatpakrepo` 后用未过滤远端安装 |
| `找不到 aarch64 的 C 交叉编译器` | 安装 `gcc-aarch64-linux-gnu` 或指定 `--cc-aarch64` |
| ELF 架构与目标不符 | `--skip-build` 复用了错误架构二进制 |
| `create: 'lgo_download_manager' not found` | 先不带 `--skip-build` 构建 |
| `bits/wordsize.h` 缺失 | sysroot 不完整；重跑脚本重建 |
| 链接时报 `-latomic_asneeded` 或 undefined reference | SDK 布局变化导致 sysroot 视图失效；重建 sysroot |
| AppStream 校验失败 | 检查 `flatpak/org.langbiantianya.LGDM.metainfo.xml` |
