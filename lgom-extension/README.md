# LGOM 浏览器扩展

拦截浏览器下载请求，把任务移交给 LGOM 桌面客户端（`lgo_download_manager`）。
交接按「**本地端口** → **`lgom://` URL**」的顺序尝试：首选是桌面客户端的
本机回环端口，既不弹"打开 LGOM？"确认框、也不开标签页；只有客户端没在运行
（端口无人应答）时才退到 `lgom://`——那是最后手段，也是唯一能冷启动客户端的一条。

两条通道的取舍：

| 通道                          | 需要什么                       | 能冷启动客户端 | 浏览器确认框 / 标签页 |
| ----------------------------- | ------------------------------ | -------------- | --------------------- |
| 本地端口（`127.0.0.1:17821`） | 客户端正在运行（托盘常驻即可） | 否             | 无 / 无               |
| `lgom://` URL                 | 系统里注册过 `lgom://` 处理器  | 是             | 有 / 复用单个标签页   |

端口通道是首选：不依赖任何额外安装，元数据以 JSON 正文投递（不再受 URL 长度
限制，Cookie / User-Agent 也不会出现在可被导航、会进历史记录的 URL 里）。
它唯一的短板是要求客户端已经在跑，所以 `lgom://` 作为兜底保留。

一套代码库，两个平台包：共享逻辑位于 `src/lib`，各浏览器独占的
manifest、后台脚本和协议触发器位于 `src/platform/<target>`，
Svelte 5 弹窗 / 选项页 / 转发页位于 `src/popup` / `src/options` / `src/handoff`。

## 系统要求

| 主机                          | Manifest | 后台上下文             | 交接通道顺序                |
| ----------------------------- | -------- | ---------------------- | --------------------------- |
| Chrome / Chromium / Edge 102+ | MV3      | ES 模块 Service Worker | 本地端口 → `lgom://` 标签页 |
| Firefox 140+                  | MV3      | ES 模块事件页面        | 本地端口 → `lgom://` 标签页 |

客户端在运行时（托盘常驻）交接完全静默：不弹确认框、不开标签页。客户端没在
运行时两条路径都退到 `lgom://` 标签页，由浏览器的确认对话框放行，同时把客户端
冷启动起来——之后的交接又回到端口通道。

Chrome 版本下限由 `chrome.storage.session`（102+）的请求头缓存镜像功能决定，
通过该 manifest 中的 `minimum_chrome_version` 强制执行。

Firefox 未实现 MV3 Service Worker，因此其后台必须声明为事件页面
（`background.scripts`），而非 `background.service_worker`；后者会导致 Firefox
拒绝安装并报错"background.service_worker 目前已禁用，请添加 background.scripts"。
Firefox 的 `strict_min_version` 为 140.0，因为该版本引入了新扩展必需的
`data_collection_permissions`。

两个主机都将后台 bundle 加载为 **ES 模块**。Vite 输出包含
`import ... from "./chunks/..."` 语句的代码，经典后台脚本无法执行——会静默终止扩展。
请确保两个 manifest 中的 `"type": "module"` 与打包器输出保持同步。

## 构建

```sh
npm install
npm run build:all        # -> build/chrome/ 和 build/firefox/
```

| 脚本                      | 效果                                    |
| ------------------------- | --------------------------------------- |
| `npm run build:chrome`    | Chrome 包输出至 `build/chrome/`         |
| `npm run build:firefox`   | Firefox 包输出至 `build/firefox/`       |
| `npm run build:all`       | 同时构建两者                            |
| `npm run package:chrome`  | Chrome 成品输出至 `dist/`（zip + crx）  |
| `npm run package:firefox` | Firefox 成品输出至 `dist/`（zip + xpi） |
| `npm run package:all`     | 两个平台都出成品                        |
| `npm run dev`             | 监听文件变更自动重新构建 Chrome 包      |
| `npm run dev:firefox`     | 同上，针对 Firefox                      |
| `npm run check`           | `svelte-check`（基于 `jsconfig.json`）  |
| `npm run lint`            | `prettier --check` + `eslint`           |
| `npm run format`          | `prettier --write`                      |

每次构建会将 Vite 输出先暂存到临时目录，再组装成扩展根目录，
确保两个平台的文件互不污染。产物结构：

```
build/<platform>/
  manifest.json        # src/platform/<platform>/manifest.json
  background.js        # 打包后的后台入口
  chunks/              # 入口静态导入的共享代码块
  popup.html|css|js    # src/popup
  options.html|css|js  # src/options
  handoff.html|css|js  # src/handoff（仅 Chrome 回退通道会打开这个页面）
  icons/               # src/static/icons
  _locales/            # src/static/_locales
```

## 打包（可直接安装的扩展包）

```sh
npm install
npm run build:all
npm run package:all      # -> dist/
```

`scripts/package.mjs` 只搬运 `build/<platform>/`，不重新编译；先跑对应的
`build:<platform>`（或 `build:all`）再打包。产物：

| 文件                                | 用途                                                                                                                                                      |
| ----------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `lgom-extension-chrome-<版本>.zip`  | Chrome 应用商店上传；解压后用「加载已解压的扩展」加载                                                                                                     |
| `lgom-extension-chrome-<版本>.crx`  | 签名过的单文件包：Chromium 系开发者模式下拖进 `chrome://extensions` 直接安装                                                                              |
| `lgom-extension-firefox-<版本>.zip` | AMO 上传                                                                                                                                                  |
| `lgom-extension-firefox-<版本>.xpi` | 安装：未签名时只有 Developer Edition / Nightly（`xpinstall.signatures.required=false`）或 `about:debugging` 临时加载能装；AMO 签名后 release 版可直接安装 |

选项：

| 选项               | 说明                                                                                                                            |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------- |
| `--version=X.Y.Z`  | 覆盖产物版本，并改写构建目录里的 `manifest.json`（1~4 段数字）。CI 用 tag 版本                                                  |
| `--key=<file.pem>` | CRX 签名私钥。**扩展 ID 由这把私钥决定**：不给就在 `dist/` 下新生成一把，ID 每次打包都变，升级会被浏览器当成另一个扩展          |
| `--sign`           | 把 Firefox 包提交 AMO 签名（`--channel=unlisted`），需要 `AMO_API_KEY` / `AMO_API_SECRET`；AMO 按版本号去重，同一版本只能签一次 |

## 加载扩展

```sh
npm run build:all
npm run package:all

# Chrome / Chromium：开发者模式 -> 把 dist/lgom-extension-chrome-<版本>.crx 拖进 chrome://extensions
#                    （品牌版 Chrome 从本地文件装 crx 受策略限制，此时用 zip 解压后「加载已解压的扩展」）
# Firefox：直接打开 dist/lgom-extension-firefox-<版本>.xpi
#          未签名时仅 Developer Edition / Nightly 能装，见「打包」
```

调试用未打包目录：

```sh
npm run build:chrome

# Chromium 系列浏览器（见下方注意事项）
chrome  --load-extension="$PWD/build/chrome"
# 或：chrome://extensions -> 启用开发者模式 -> 加载已解压的扩展 -> build/chrome
```

```sh
npm run build:firefox
# about:debugging#/runtime/this-firefox -> 临时加载附加组件 -> build/firefox/manifest.json
```

品牌版 Google Chrome ≥ 137 会忽略 `--load-extension`（此命令行参数仅 Chromium
和 Chrome for Testing 构建版本支持）。请通过 `chrome://extensions` 加载，
或使用 Chromium 系列构建（如 `/usr/bin/microsoft-edge --load-extension=...`）
进行脚本化测试。

Microsoft Edge 不受这条限制：Edge 155 仍接受 `--load-extension`（本机实测
headless 下加载构建产物、拦截下载并完成端口交接）。所以脚本化验证可以直接
`microsoft-edge --headless=new --load-extension=build/chrome <url>`。

未打包的 Chrome 扩展 ID 由目录路径决定，移动 `build/chrome` 会改变 ID；打包后的
ID 由 CRX 签名私钥决定（`--key`，见「打包」）。想让未打包扩展的 ID 稳定，可在
Chrome manifest 中用 `key` 固定（CI 用的是 `CHROME_EXTENSION_KEY` 对应的那把私钥）。

## 拦截原理

1. `webRequest.onBeforeSendHeaders`（仅观察，不阻塞）将每个 URL 的请求头
   缓存到内存 `Map`，并以 250 ms 防抖写入 `storage.session` 镜像。
   两个主机都会在空闲时清除后台上下文，因此仅靠内存 Map 不够；
   Chrome 需要 `extraHeaders` 才能获取 `Cookie`/`User-Agent`。
2. `downloads.onCreated` 事件触发。如拦截已禁用或 URL 不匹配当前过滤模式，
   则不处理。
3. 下载任务被取消并擦除。
4. 缓存的请求头经 `normalizeMetadata` 转为元数据。同时生成两种表示：
   `buildPortPayload` 的 JSON 正文（端口通道用）与 `buildLgomUrl` 的
   `lgom://` URL（回退通道用）。
5. `triggerProtocol` 按优先级尝试两条通道，返回实际成功的那条：

   1. **本地端口**：`sendToLocalPort` 先确认客户端在监听（`GET /ping`），
      再把 JSON 正文 `POST /download` 过去。不弹确认框、不开标签页，也不需要
      任何额外安装。客户端没在跑时这一步极快失败，直接往下走。
   2. **`lgom://` 外部协议**：最后手段。Chrome 复用同一个转发标签页
      `tabs.update` 到该 URL，Firefox 新建标签页并在交接后移除。确认对话框的
      一次性豁免见「[确认提示与回退通道](#确认提示与回退通道)」。

   每次交接都从头重试整条链，所以把客户端跑起来后无需重载浏览器即可回到端口通道。

6. 结果追加到最近拦截日志（最新优先，最多 50 条）——包括交接失败，
   这样弹窗不会在下载已被拦截但转发失败的情况下显示"尚无拦截记录"。
   `reason` 记录走的是哪条通道，弹窗里以徽标形式显示：

| `reason`         | 通道                           | 弹窗徽标 |
| ---------------- | ------------------------------ | -------- |
| `forwarded-port` | 本地端口                       | 端口     |
| `forwarded-tab`  | `lgom://` 回退（客户端没在跑） | 协议     |

更早版本写入的 `forwarded` / `forwarded-native`（当时静默通道是原生消息主机）
读出来仍按「主机」显示，方便对照旧日志。

## 本地端口通道

桌面客户端在 `127.0.0.1` 上监听候选端口中的第一个空闲端口，扩展探测同一份
列表来发现它（两边的常量必须同步：Go 侧
`internal/handoff/handoff.go` 的 `PortCandidates`，扩展侧
`src/lib/constants.js` 的 `LOCAL_PORT_CANDIDATES`）：

```
17821, 17822, 17823, 17824, 17825
```

接口（`internal/handoff`）：

| 请求                | 响应                                                                    |
| ------------------- | ----------------------------------------------------------------------- |
| `GET /ping`         | `200 {"app":"lgo_download_manager","api":1,"port":17821,"version":"…"}` |
| `POST /download`    | `200 {"ok":true}` / 4xx-5xx `{"ok":false,"error":"…"}`                  |
| `OPTIONS /download` | 预检应答（只对扩展来源放行）                                            |

`POST /download` 的正文与 `lgom://download?…` 的参数一一对应：

```json
{
	"url": "https://…",
	"name": "a.zip",
	"ua": "Mozilla/5.0 …",
	"headers": "Referer: …",
	"cookies": "sid=1"
}
```

发现与缓存：扩展先用 `storage.session`（键 `lgom_local_port`）里缓存的上次端口
发一次 `GET /ping` 验证，失败就在候选表上并行探测，取响应且 `app` 匹配的最小
端口。所有候选都无响应 → 视为「客户端没在运行」，静默降级到下一个通道。

客户端装在 Flatpak / 沙箱里不影响这条通道：manifest 的 `--share=network` 让沙箱与
宿主共用网络命名空间，扩展按宿主上的 `127.0.0.1:<候选端口>` 探测就能发现它
（本机实测：Flatpak 实例收到 `handoff: request … origin=chrome-extension://…`）。

请求头：每次请求都带 `X-LGOM-Client`（值不重要，存在即可）与
`Content-Type: application/json`。客户端对 `POST /download` 的来源策略是：

- 不带 `Origin`（curl、本机脚本）→ 放行；
- `Origin` 是扩展来源（`chrome-extension://`、`moz-extension://`、
  `extension://`（Edge）、`safari-web-extension://`）→ 放行，并回显
  `Access-Control-Allow-Origin`；
- 其它来源（含 `null`，即沙箱 iframe / `data:` 页面）→ `403`。

自定义头把请求变成非简单请求，普通网页必须先过预检，而预检只对扩展来源放行，
所以网页既发不出请求、也读不到响应。客户端只监听 `127.0.0.1`（不监听
`0.0.0.0`/`::`），同机以外的连接进不来；收到的请求只会走「弹新建任务对话框」
（`--no-gui` / `--autostart` 形态走与 `lgom://` 完全一致的直接入队路径）。

排障：

- 弹窗里「桌面端连接」显示端口号 → 端口通道可用；显示「未运行」→ 客户端没跑
  （或端口被防火墙 / 安全软件挡住）。
- 扩展控制台（`chrome://extensions` → 服务工作进程 / `about:debugging` →
  检查）里出现 `local port hand-off failed (…)` → 客户端在，但拒了这条请求：
  多为来源不是扩展、或业务进程弹不出对话框。
- 客户端侧 `--debug` 日志会打出 `handoff: listening on 127.0.0.1:<端口>`、
  每条请求的 `origin`（`handoff: request … origin=…`）与拒绝原因。
- 客户端配置目录下的 `handoff.json` 记录当前监听端口、pid 与版本，供排障与
  其它本机工具读取（扩展不读它，只探测固定候选端口）。

## 交接 URL 协议

`lgom://` 回退通道携带这个 URL，直接把它作为导航目标交给浏览器/操作系统。
（端口通道不用这个格式，它发的是[上面的 JSON 正文](#本地端口通道)；正因为
如此它才不受 `maxUrlLength` 限制。）

```
lgom://download?url=<编码>[&name=<编码>[&ua=<编码>][&headers=<编码>&cookies=<编码>]]
```

`headers` 为 `Name: value` 逐行列表；逐跳和主机绑定头
（`host`、`connection`、`content-length`、`accept-encoding`、`transfer-encoding`、
`te`、`trailer`、`upgrade`）会被剥离。当完整编码后的 URL 超过 `maxUrlLength` 时，
降级为仅含核心参数（`url`、`name`、`ua`），以保证交接 URL 保持在
LGOM IPC 帧限制内（`MAX_FRAME_LEN`，64 KiB）。

## 确认提示与回退通道

通过 `lgom://` 交接属于**外部协议启动**，浏览器会在确认对话框（"打开 LGOM？"）
后放行——这个对话框无法在扩展代码里 suppress，属于浏览器安全策略。所以客户端
没运行时的目标不是"消灭对话框"，而是"只弹一次、并且不再每次新建标签页"：

- **Chrome：** 复用同一个转发标签页，每次 `tabs.update` 到新的 `lgom://` URL。
  之所以用 `tabs.update` 而不是 `tabs.create`：Chrome 只对带**可信发起方
  origin** 的导航提供"始终允许……打开此类链接"复选框
  （`MayRememberAllowDecisionsForThisOrigin` →
  `network::IsOriginPotentiallyTrustworthy`，而 `chrome-extension://` 已注册为
  secure scheme），`UpdateFunction` 会带上 `extension()->origin()`，`CreateFunction`
  在没有 `setSelfAsOpener` 时不会——用 `tabs.create` 就等于每次都要重新手点。
  勾一次后记录落在 profile 的 `protocol_handler.allowed_origin_protocol_pairs`，
  同一 origin + `lgom` 组合之后都是静默转发；管理员也可以用
  `AutoLaunchProtocolsFromOrigins` 策略按 origin 预批准。
  另外，转发标签页必须等扩展页 commit 之后再导航：Chrome 会丢弃"没有任何已提交
  导航、唯一待决导航又变成外部协议"的标签页，否则每次交接都会退化成新建标签页。
- **Firefox：** 扩展仍走 `lgom://` 标签页，但提示属于浏览器窗口而非标签页，
  标签页完成交接后即移除（1 秒）。想免提示，在 `about:config` 里把
  `network.protocol-handler.warn-external.lgom` 设为 `false`。

最近拦截日志里的 `reason`（弹窗里显示为徽标）能看出走的是哪条通道：
`forwarded-port` = 本地端口，`forwarded-tab` = `lgom://` 回退（客户端没在运行）。

这段回退通道只有在客户端没运行时才会用到；客户端在跑时端口通道就接住了下载，
不会开标签页、也不会弹确认框。

## 配置

设置项位于选项页（`chrome://extensions` → 详情 → 扩展选项；
`about:addons` → 偏好设置）。

| 字段           | 默认值 | 说明                                   |
| -------------- | ------ | -------------------------------------- |
| `enabled`      | `true` | 主开关；关闭时不处理任何下载           |
| `mode`         | `all`  | `all` \| `whitelist` \| `blacklist`    |
| `patterns`     | `[]`   | 正则表达式；无效模式降级为子字符串匹配 |
| `maxUrlLength` | `2000` | 保存时限制在 200–8000 范围内           |

过滤语义：`all` 拦截所有；`whitelist` 仅拦截匹配的 URL；`blacklist`
拦截除匹配项外的所有。

存储键：`lgom_config`（`storage.local`）、`lgom_recent`
（`storage.local`）、`lgom_header_cache`（`storage.session`）。

## 权限

| 权限                 | 用途                                     |
| -------------------- | ---------------------------------------- |
| `downloads`          | `onCreated` 及 `cancel` / `erase`        |
| `webRequest`         | 观察请求头以供交接                       |
| `storage`            | 配置、最近日志、请求头缓存镜像、端口缓存 |
| `tabs`               | 打开并复用交接标签页                     |
| `<all_urls>`         | 拦截任意来源的下载                       |
| `http://127.0.0.1/*` | 访问客户端本地端口通道（见下）           |

两个平台声明的权限一致（`downloads` / `storage` / `tabs` / `webRequest`），
都不需要 `scripting`，也都没有请求 `webRequestBlocking`（因为只观察请求）。

`http://127.0.0.1/*` 显式列出（`<all_urls>` 其实已覆盖回环地址），用来说明扩展
为什么要访问本机端口。Go 侧对预检与响应都回显扩展来源的 CORS 头，所以即使浏览器
没把这个主机权限发给扩展，端口通道依然能用。

## 国际化

界面字符串位于 `src/static/_locales/<locale>/messages.json`，
通过 `i18nGetMessage()`（`src/lib/platform-api.js`）读取，该函数封装了
`browser.i18n.getMessage`，在 API 缺失时回退为键本身。
`en` 是 `default_locale`；`zh_CN` 已完整翻译。添加新语言时，
将 `_locales/en` 复制到新语言标签目录下并翻译各条目的 values
——manifest 中的 `__MSG_extensionName__` / `__MSG_extensionDescription__`
占位符也从同一文件解析。

## 目录结构

| 路径                                 | 内容                                                               |
| ------------------------------------ | ------------------------------------------------------------------ |
| `src/lib/constants.js`               | 协议、存储键、缓存调优、默认值                                     |
| `src/lib/types.js`                   | JSDoc typedef（无运行时导出）                                      |
| `src/lib/platform-api.js`            | `browser`/`chrome` 处理、`i18nGetMessage`、平台名称                |
| `src/lib/url-builder.js`             | 请求头序列化、`buildLgomUrl`、`buildPortPayload`                   |
| `src/lib/local-port.js`              | 本地端口通道：`/ping` 发现 + 端口缓存 + `POST /download`           |
| `src/lib/channels.js`                | 通道 → 最近日志 `reason` / 弹窗徽标的映射                          |
| `src/lib/metadata.js`                | `normalizeMetadata`、`shouldIntercept`                             |
| `src/lib/storage.js`                 | 配置加载/保存                                                      |
| `src/lib/recent.js`                  | 最近拦截日志                                                       |
| `src/platform/chrome/`               | MV3 manifest、模块 Service Worker、请求头缓存、协议触发器          |
| `src/platform/chrome/handoff-tab.js` | `lgom://` 回退：复用单个转发标签页                                 |
| `src/platform/firefox/`              | MV3 事件页面、请求头缓存、协议触发器                               |
| `src/handoff/`                       | 转发标签页承载的扩展页（Chrome 回退通道可见的那个页面）            |
| `src/popup/`                         | 工具栏弹窗（状态、主开关、最近列表）                               |
| `src/options/`                       | 选项页（协议、过滤规则、关于）                                     |
| `src/static/`                        | 图标和 `_locales` 直接复制到构建输出                               |
| `src/types.d.ts`                     | 环境 `chrome`/`browser` 声明（不使用 `@types/chrome`）             |
| `src/routes/layout.css`              | Tailwind v4 入口，被两个 UI 界面导入                               |
| `scripts/build.mjs`                  | 构建入口：把 Vite 产物组装成 `build/<platform>/`                   |
| `scripts/package.mjs`                | 打包入口：把 `build/<platform>/` 打成 `dist/` 里的 zip / crx / xpi |

项目使用 JavaScript + JSDoc 类型注解，而非 TypeScript；`jsconfig.json`
启用了 `checkJs`，`svelte-check` 是类型检查关卡。

## 验证构建

```sh
npm run check && npm run lint
```

打包产物也能装进真实浏览器验证：`web-ext` 走的是浏览器原生的临时加载通道，
Firefox 侧会打印 `Installed ... as a temporary add-on`。

```sh
npm run build:all
npx web-ext run -s build/firefox -f firefox                     # Firefox
npx web-ext run -s build/chrome -t chromium \                   # Chromium 系二进制
  --chromium-binary=/usr/bin/chromium
```

品牌版 Chrome ≥ 137 不接受 `--load-extension`，脚本化加载要走 CDP
`Extensions.loadUnpacked`（需要 `--remote-debugging-pipe`）；手动验证则在
`chrome://extensions` 里「加载已解压的扩展」指向解压后的 zip。

运行时检查：加载构建好的扩展，通过 DevTools 协议驱动，后台脚本的存储是最易观测的信号：

```js
// 在 Service Worker 上下文中
await chrome.downloads.download({ url: 'https://example.com/file.zip' });
await chrome.downloads.search({}); // 拦截取消擦除后为空 []
// reason 就是走的通道：forwarded-port / forwarded-tab
await chrome.storage.local.get('lgom_recent'); // [{ ok: true, reason: 'forwarded-port' }]
```

端口通道还能不依赖浏览器地直连验证：客户端在跑时（`--debug` 看日志，
或读 `<config>/handoff.json` 里的端口）直接打它的接口即可：

```sh
curl -s http://127.0.0.1:17821/ping                       # {"app":"lgo_download_manager",…}
curl -s -X POST -H 'Content-Type: application/json' \
  -H 'X-LGOM-Client: extension' \
  -d '{"url":"https://example.com/a.zip","name":"a.zip"}' \
  http://127.0.0.1:17821/download                          # {"ok":true}
# 带非扩展 Origin（含 null）会被拒：{"ok":false,"error":"origin not allowed: …"} + 403
```

回退通道在真实浏览器里的观测点：先停掉客户端，`lgom_recent` 的 `reason` 就变成
`forwarded-tab`，且多次下载后标签页仍是同一个（`chrome.tabs.query({})` 里只有
一个 `handoff.html`）。勾选一次"始终允许"后，后续交接不再弹确认框——记录落在
profile 的 `protocol_handler.allowed_origin_protocol_pairs`。

## 持续集成

`.github/workflows/extension.yml` 在 push / PR / `v*` tag 上跑
`npm ci` + `build:<platform>` + `package:<platform>`，产物作为 artifact 上传
（保留 30 天）；`v*` tag 上把 zip / crx / xpi 一并附到 GitHub Release。
版本号用 tag 去掉 `v`（`v1.2.3` → `1.2.3`），非 tag 推送用 manifest 里的版本。

| secret                           | 用途                                                                                               |
| -------------------------------- | -------------------------------------------------------------------------------------------------- |
| `CHROME_EXTENSION_KEY`           | CRX 签名私钥（PEM）。不配则每次构建新生成一把，扩展 ID 每次都变                                    |
| `AMO_API_KEY` / `AMO_API_SECRET` | AMO 的 JWT issuer / secret。配了之后 `v*` tag 的 Firefox 包自动提交 AMO 签名（`channel=unlisted`） |
