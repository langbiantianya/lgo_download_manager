# 浏览器扩展接力

`internal/handoff` 是浏览器扩展的首选交接通道：业务进程监听本机回环端口，扩展用
JSON 提交下载请求。该通道不弹浏览器确认框、不开标签页，也不要求额外本地组件；
它的限制是客户端必须在运行。客户端未运行时，扩展回退到 `lgom://` URL。

| 优先级 | 通道 | 需要什么 | 可冷启动 lgdm | 确认框 / 标签页 |
| --- | --- | --- | --- | --- |
| 1 | 本地端口 | lgdm 正在运行 | 否 | 无 / 无 |
| 2 | `lgom://` URL | 已注册协议处理器 | 是 | 有 / 复用标签页 |

扩展每次从头重试整条链。客户端启动后无需重载浏览器即可回到端口通道。扩展侧的
实现、配置和排障见 [lgom-extension/README.md](../../lgom-extension/README.md)。

## 本地端口

业务进程在 `127.0.0.1` 上依次尝试候选端口：

```text
17821, 17822, 17823, 17824, 17825
```

Go 侧常量为 `PortCandidates`，扩展侧对应
`lgom-extension/src/lib/constants.js` 中的 `LOCAL_PORT_CANDIDATES`，两边必须同步。

接口：

| 请求 | 响应 |
| --- | --- |
| `GET /ping` | `200 {"app":"lgo_download_manager","api":1,"port":17821,"version":"…"}` |
| `POST /download` | `200 {"ok":true}` 或 4xx/5xx `{"ok":false,"error":"…"}` |
| `OPTIONS /download` | CORS 预检，仅对扩展来源放行 |

`POST /download` 使用 JSON：

```json
{
  "url": "https://example.com/a.zip",
  "name": "a.zip",
  "ua": "Mozilla/5.0 …",
  "headers": "Referer: …",
  "cookies": "sid=1"
}
```

字段与 `lgom://download` 查询参数对应。JSON 正文不受 URL 长度限制，Cookie 和
User-Agent 不会进入可导航、会进历史记录的 URL。`url` 也可以接受磁力链接或
`.torrent` 链接。

请求必须带 `X-LGOM-Client` 和 `Content-Type: application/json`。来源策略：

- 不带 `Origin` 的本机脚本和 CLI：放行；
- 扩展来源 `chrome-extension://`、`moz-extension://`、Edge 的 `extension://`、
  `safari-web-extension://`：放行并回显 CORS；
- 其它来源，包括 `null`：返回 403。

自定义请求头让普通网页必须先通过预检，而预检只对扩展来源放行。服务只监听
`127.0.0.1`，不监听 `0.0.0.0` 或 `::`。

## 请求落地

- 默认形态会拉起或复用 UI，并打开预填好的「新建下载任务」对话框，由用户确认。
- `--no-gui` 形态与 `lgom://` 的直接入队路径一致，执行 Add + Start。
- `--autostart` 只控制首次登录时是否立即显示窗口；之后收到扩展请求仍会按需
  拉起 UI 并弹出确认对话框。

实际监听端口、pid 和版本写入 `<config>/handoff.json`，供排障和其它本机工具读取；
扩展不读取该文件，只探测固定候选端口。`--debug` 日志会输出监听端口和请求来源。

## 测试

```sh
go test -v ./internal/handoff/
```

测试覆盖来源策略、候选端口顺延、全部占用时报错、仅绑回环、请求体校验、错误回传
和发现文件内容。测试需要绑定回环端口，受限环境会自动跳过。
