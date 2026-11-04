# lgdm

本地下载管理器，提供 Fyne GUI、SQLite 状态持久化、并发分片下载、BitTorrent 和浏览器
接力。业务进程持有下载任务与调度器，Fyne UI 作为子进程通过 gRPC 或 HTTP 连接。

## 主要能力

- HTTP、HTTPS、FTP、WebDAV 并发下载、断点续传与完整性校验。
- 磁力链接和 `.torrent` 文件下载、做种、DHT/PEX 与分享率控制。
- 系统、手动和 PAC/WPAD 代理。
- 浏览器扩展本地端口接力，以及 `lgom://` URL 协议。
- Windows MSI/EXE 与 Linux Flatpak 安装包。
- 中英文等多语言界面、托盘常驻、开机自启和并发任务 FIFO 调度。

## 快速开始

构建需要 Go 和可用的 C 工具链，版本与平台依赖见
[开发与构建](docs/development.md)。

```sh
go build -o bin/lgdm .
./bin/lgdm
```

无界面运行：

```sh
./bin/lgdm -no-gui
```

## 文档目录

| 文档 | 内容 |
| --- | --- |
| [使用指南](docs/usage.md) | 启动方式、命令行参数、运行时目录 |
| [整体架构](docs/architecture.md) | 系统架构图、进程模型、分层、模块职责与数据流 |
| [开发与构建](docs/development.md) | 本地构建、版本元数据与开发入口 |
| [测试指南](scripts/TESTING.md) | 全量与分模块测试、FTP 测试服务器 |
| [Windows 安装包](installer/README.md) | MSI/EXE 构建、C/C++ 工具链、安装与排错 |
| [Flatpak 安装包](flatpak/README.md) | 多架构打包、沙箱行为与排错 |
| [持续集成](.github/workflows/README.md) | Windows/Flatpak/扩展的 CI job 与发布流程 |

### 模块文档

| 模块 | 文档 |
| --- | --- |
| 下载引擎 | [internal/engine](internal/engine/README.md) |
| 下载协议与代理 | [internal/protocol](internal/protocol/README.md) |
| BitTorrent | [internal/bittorrent](internal/bittorrent/README.md) |
| 任务调度 | [internal/scheduler](internal/scheduler/README.md) |
| 任务存储与状态 | [internal/store](internal/store/README.md) |
| 业务/UI RPC | [internal/rpc](internal/rpc/README.md) |
| UI 子进程 | [internal/ui](internal/ui/README.md) |
| 浏览器端口接力 | [internal/handoff](internal/handoff/README.md) |
| URL 协议 | [internal/urllauncher](internal/urllauncher/README.md) |
| 设置 | [internal/settings](internal/settings/README.md) |
| 开机自启 | [internal/autostart](internal/autostart/README.md) |
| 日志 | [internal/logging](internal/logging/README.md) |
| 浏览器扩展 | [lgom-extension](lgom-extension/README.md) |

## 许可证

本项目采用 [Mozilla Public License 2.0](LICENSE)（MPL 2.0）。
