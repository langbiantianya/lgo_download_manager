# 下载协议与代理

`internal/protocol` 提供 HTTP、HTTPS、FTP、WebDAV 驱动，以及系统代理、PAC/WPAD
和手动代理支持。分片与续传策略见 [下载引擎](../engine/README.md)。

## HTTP / HTTPS / WebDAV / FTP

- HTTP/WebDAV 使用 HEAD 或 `PROPFIND` 探测，随后以 1 字节 Range 请求验证。
- FTP 使用 `SIZE` 与 `MDTM` 探测大小和版本，支持被动/主动模式。
- Range、摘要校验、半开连接检测和失败重规划由引擎统一执行。
- HTTP/WebDAV 请求显式使用 `Accept-Encoding: identity`。

## 代理模式

设置对话框提供三种 HTTP/HTTPS 代理模式：

- **使用系统代理**（默认）：优先读取 `HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY`
  和 `NO_PROXY`；当前协议没有环境代理时，再读取桌面会话设置。HTTP 与 HTTPS
  可分别选择代理端点，系统 bypass 与 `NO_PROXY` 同时生效。桌面代理快照缓存
  30 秒，设置变化后无需重启客户端即可重新探测。
- **不使用代理**：始终直连，忽略环境变量和系统设置。
- **手动设置代理**：填写 `http://`、`https://` 或 `socks5://` URL，并可用
  逗号分隔主机名、CIDR 或通配符作为绕过列表，例如 `*.lan`、`192.168.*`、
  `10.0.0.0/8`。

系统代理模式支持 Linux GNOME `gsettings`、KDE `kioslaverc`、
`/etc/environment`，macOS `scutil --proxy` 和 Windows WinINET。

系统代理可以执行 PAC 脚本中的 `FindProxyForURL`，支持
`PROXY`/`HTTPS`/`SOCKS5`/`DIRECT` 与常见 PAC helper。系统只报告自动发现时可尝试
标准 WPAD 地址 `http://wpad/wpad.dat`；也可以在设置中填写 PAC 地址或 `wpad`。
脚本获取失败时安全回退直连，并做短时间负缓存。

BitTorrent 的 tracker 公告、webseed 和 `.torrent` 文件抓取同样使用
`internal/protocol.HTTPProxyFunc`。BT peer 连接不是 HTTP，不受这里的代理影响；
BT 使用动态代理解析函数，修改设置不需要重建正在运行的 BT 客户端。

## 配置层

`protocol.SetProxyConfig()` 设置进程级全局代理。每个任务可通过
`AuthOptions{ProxyMode, ProxyURL, ProxyBypass, ProxyAutoConfigURL}` 覆盖。
`effectiveAuth()` 合并 per-call 与全局配置，`proxyFunc()` 按以下顺序选择：

1. `ProxyFromEnvironment`
2. 系统 PAC/WPAD
3. `cachedSystemProxy()` 平台 API
4. 手动代理 URL

PAC 脚本按 URL 缓存，并限制脚本大小与执行时间。

## 测试

```sh
go test -v ./internal/protocol/
```

HTTP/WebDAV 使用 `net/http/httptest`。FTP 的在线用例自动启动纯 Go 测试服务器
`scripts/ftp_test_server/ftpd.go`，不依赖外部服务。完整测试清单见
[scripts/TESTING.md](../../scripts/TESTING.md)，覆盖报告见
[TEST_REPORT.md](TEST_REPORT.md)。
