# 使用指南

本文汇总 lgdm 的启动方式、命令行参数、运行时目录和常见使用入口。下载协议、
代理、BitTorrent 等功能的实现说明见各自的模块文档。

## 运行

带 GUI 运行：

```sh
./lgdm
```

无界面运行（仅保留调度器，不启动 Fyne 窗口）：

```sh
./lgdm -no-gui
```

强制开启轻量模式（关闭主窗口时释放 widget 树）：

```sh
./lgdm --light
```

已安装 Flatpak 包时：

```sh
flatpak run org.langbiantianya.LGDM                 # 默认运行本机架构
flatpak run --arch=aarch64 org.langbiantianya.LGDM  # 已安装 aarch64 版本时
```

## 命令行参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-config` | 见下文 | 运行时目录，SQLite 数据库与日志都保存到这里 |
| `-no-gui` | `false` | 启动时不打开 Fyne GUI |
| `-open-url` | `""` | 将一条 `lgom://...` URL 加入队列 |
| `-light` | `false` | 强制开启轻量模式，关闭主窗口时释放 widget 树 |
| `-debug` | `false` | 输出 Debug 日志；Windows 下优先写回父终端，无法挂接时写文件 |
| `-autostart` | `false` | 由系统登录启动项触发；初始只保留调度器和托盘，按需拉起 UI |
| `-rpc-listen` | `""` | UI 通道监听地址，格式为 `unix:<path>` 或 `tcp:<host:port>` |

也支持通过位置参数传入 `lgom://` URL，部分桌面环境会以这种方式转交协议 URL。

## 运行时目录

`-config` 的默认值：

- Windows：`%LOCALAPPDATA%\lgo_download_manager`
- 其它平台：`~/.config/lgo_download_manager`；`$HOME` 不可用时回退到当前目录

Windows 使用绝对路径，确保从任意工作目录启动的托盘实例和协议实例使用同一个
数据库。SQLite 数据库固定为 `<config>/lgdm.sqlite`，日志与 `handoff.json` 也位于
该目录。

> 数据库文件名已从 `ldm.sqlite` 改为 `lgdm.sqlite`，目录名仍是
> `lgo_download_manager`。旧文件不会被自动读取，需要时可手工改名。

环境变量 `LDM_RPC_LISTEN` 可提供与 `-rpc-listen` 相同的 UI 通道设置，命令行参数
优先。详细协议与调试方式见 [UI RPC](../internal/rpc/README.md)。

## 相关文档

- [URL 协议处理](../internal/urllauncher/README.md)
- [浏览器扩展接力](../internal/handoff/README.md)
- [HTTP/FTP/代理与设置](../internal/protocol/README.md)
- [BitTorrent 下载](../internal/bittorrent/README.md)
- [日志](../internal/logging/README.md)
- [开机自启](../internal/autostart/README.md)
- [设置项](../internal/settings/README.md)
- [Flatpak 安装](../flatpak/README.md)
