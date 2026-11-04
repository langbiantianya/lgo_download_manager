# 开发与构建

## 环境

- Go 版本以根目录 `go.mod` 为准。
- GUI 依赖 Fyne，构建时需要可用的 C 工具链和平台图形开发库。
- BitTorrent 依赖的 uTP 在 cgo 下包含 C++ 源码，因此 Windows 构建还需要 C++
  编译器，并应把 C/C++ 运行时静态链接进产物。

Windows 打包脚本会自动寻找并成对配置 `CC` / `CXX`，完整的安装包构建说明见
[Windows 打包](../installer/README.md)。Linux 安装包构建见
[Flatpak](../flatpak/README.md)。

## 本机构建

```sh
go build -o bin/lgdm.exe .   # Windows 手工调试
go build -o bin/lgdm .       # Linux / macOS 手工调试
```

正式安装包由各平台脚本生成，统一写入版本信息并校验目标架构：

```powershell
pwsh -File scripts\package.ps1
```

```sh
./scripts/build_flatpak.sh
```

## 版本元数据

`internal/version` 提供 `Version`、`Commit`、`Date` 三个变量，默认是开发期占位符。
Windows 与 Linux 打包脚本会通过 `go build -ldflags -X` 注入 `git describe`、
短提交号和 UTC 构建时间。手工构建也可以传入同样的链接参数。

启动时日志会打印：

```text
lgdm <version> (commit <commit>, built <date>)
```

## 测试

```sh
go test ./...
```

分模块测试、端到端测试和 FTP 测试服务器说明见
[scripts/TESTING.md](../scripts/TESTING.md)。

浏览器扩展使用独立的 npm 工具链，构建与测试见
[lgom-extension/README.md](../lgom-extension/README.md)。

## 代码入口

业务进程、UI 子进程和浏览器接力的整体关系见
[架构说明](architecture.md)。各模块职责见根 README 的文档索引。
