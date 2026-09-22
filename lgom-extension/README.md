# LGOM 浏览器扩展

拦截浏览器下载请求，把任务移交给 LGOM 桌面客户端（`lgo_download_manager`）。
Chrome 的默认通道是**原生消息主机**（`runtime.sendNativeMessage`）——不弹
"打开 LGOM？"确认框，也不开标签页；主机没装时自动回退到 `lgom://` URL 协议
（复用单个转发标签页）。Firefox 走 `lgom://` 标签页。

一套代码库，两个平台包：共享逻辑位于 `src/lib`，各浏览器独占的
manifest、后台脚本和协议触发器位于 `src/platform/<target>`，
Svelte 5 弹窗 / 选项页 / 转发页位于 `src/popup` / `src/options` / `src/handoff`，
原生消息主机（POSIX sh，不进扩展包）位于 `native-host`。

## 系统要求

| 主机                          | Manifest | 后台上下文             | 默认交接通道                            |
| ----------------------------- | -------- | ---------------------- | --------------------------------------- |
| Chrome / Chromium / Edge 102+ | MV3      | ES 模块 Service Worker | 原生消息主机，缺省回退 `lgom://` 标签页 |
| Firefox 140+                  | MV3      | ES 模块事件页面        | `lgom://` 标签页                        |

Chrome 要做到完全不弹确认框、不开标签页，需要一次性安装原生消息主机
（`native-host/install.sh`，见「[原生消息主机](#原生消息主机)」）；不装也能用，
只是每次交接走 `lgom://` 回退通道。

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

`native-host/` 不参与构建：原生消息主机的 manifest 必须由浏览器之外的本地程序
写入，装不进扩展包。

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

未打包的 Chrome 扩展 ID 由目录路径决定，移动 `build/chrome` 会改变 ID；打包后的
ID 由 CRX 签名私钥决定（`--key`，见「打包」）。原生消息主机按 ID 授权
（manifest 里的 `allowed_origins`），所以装主机时要把当前 ID 交给
`install.sh --extension-id`；想让未打包扩展的 ID 稳定，可在 Chrome manifest 中
用 `key` 固定（CI 用的是 `CHROME_EXTENSION_KEY` 对应的那把私钥）。

## 拦截原理

1. `webRequest.onBeforeSendHeaders`（仅观察，不阻塞）将每个 URL 的请求头
   缓存到内存 `Map`，并以 250 ms 防抖写入 `storage.session` 镜像。
   两个主机都会在空闲时清除后台上下文，因此仅靠内存 Map 不够；
   Chrome 需要 `extraHeaders` 才能获取 `Cookie`/`User-Agent`。
2. `downloads.onCreated` 事件触发。如拦截已禁用或 URL 不匹配当前过滤模式，
   则不处理。
3. 下载任务被取消并擦除。
4. 缓存的请求头经 `normalizeMetadata` 转为元数据，`buildLgomUrl` 生成交接 URL。
5. `triggerProtocol` 把 URL 交给桌面端，按优先级尝试两条通道：
   **原生消息主机**（默认，`runtime.sendNativeMessage` → `native-host/lgom-bridge.sh`，
   不弹确认框、不开标签页），失败后回退到 **`lgom://` 外部协议**——复用同一个
   转发标签页 `tabs.update` 到该 URL。两条通道的取舍、确认对话框的一次性豁免
   见「[原生消息主机](#原生消息主机)」与
   「[确认提示与回退通道](#确认提示与回退通道)」。主机每次交接都重新尝试一次，
   所以装好主机后无需重载浏览器即可生效。

6. 结果追加到最近拦截日志（最新优先，最多 50 条）——包括交接失败，
   这样弹窗不会在下载已被拦截但转发失败的情况下显示"尚无拦截记录"。
   `reason` 记录走的是哪条通道：`forwarded`（原生主机）或
   `forwarded-tab`（`lgom://` 回退，说明主机没装）。

## 交接 URL 协议

两条通道都携带同一个 `lgom://` URL：原生消息主机把它作为 JSON 负载里的 `url`
字段转发，回退通道直接把它作为导航目标交给浏览器/操作系统。

```
lgom://download?url=<编码>[&name=<编码>[&ua=<编码>][&headers=<编码>&cookies=<编码>]]
```

`headers` 为 `Name: value` 逐行列表；逐跳和主机绑定头
（`host`、`connection`、`content-length`、`accept-encoding`、`transfer-encoding`、
`te`、`trailer`、`upgrade`）会被剥离。当完整编码后的 URL 超过 `maxUrlLength` 时，
降级为仅含核心参数（`url`、`name`、`ua`），以保证交接 URL 保持在
LGOM IPC 帧限制内（`MAX_FRAME_LEN`，64 KiB）。

## 原生消息主机

### 安装

扩展无法自己安装原生消息主机——manifest 必须由浏览器之外的本地程序写入。
`native-host/install.sh` 就是那个程序，不需要 root：

```sh
# 扩展 ID 从 chrome://extensions 读（未打包扩展的 ID 由目录路径决定，
# 打包后的 ID 由签名私钥决定，见「加载扩展」「打包」）
lgom-extension/native-host/install.sh --extension-id <扩展 ID>

lgom-extension/native-host/install.sh --help       # 全部选项
lgom-extension/native-host/install.sh --uninstall  # 移除 manifest 与已安装的 bridge
```

它做两件事：

1. 把 `lgom-bridge.sh` 复制到 `$XDG_DATA_HOME`（默认 `~/.local/share`）下的
   `lgom-native-host/`，并在需要时写 `bridge.conf` 固定客户端路径；
2. 在每个**已存在**的浏览器 profile 目录下写
   `<profile>/NativeMessagingHosts/org.langbiantianya.lgom.json`
   （即 Chromium 的 `DIR_USER_NATIVE_MESSAGING`；macOS 用
   `~/Library/Application Support/...`）：Linux 是
   `~/.config/{google-chrome,chromium,microsoft-edge,…}`，不在列表里的浏览器
   用 `--browsers` 指定，自定义 profile 目录用 `--user-data-dir`。

常用选项：`--extension-id id1,id2`（同时授权多个 ID，例如开发版 + 商店版）、
`--lgdm-bin <绝对路径>`、`--browsers a,b`、`--user-data-dir <dir>`。
写进 manifest 的 `allowed_origins` 只认这些 ID：没列进去的扩展调用
`sendNativeMessage` 会被拒绝（扩展侧会记一条 `native hand-off failed (forbidden)`）。

### 主机协议

`lgom-bridge.sh` 是 POSIX sh，没有 Python/Node 依赖。它只做三件事：

1. 从 stdin 读**一帧**原生消息：4 字节本机字节序长度 + UTF-8 JSON。
   扩展固定发送 `{"url":"lgom://download?…"}`（URL 已百分号编码，因此负载里不含
   引号或反斜杠，用 `sed` 取串是安全的）。
2. 用该 URL 启动客户端：`lgo_download_manager <lgom://…>`，与 `.desktop` 的
   `Exec=lgo_download_manager %u` 完全一致——已经在跑的实例会经它自己的单实例
   socket 收到 URL，**bridge 不参与那套协议，Go 侧也不需要任何改动**。
   客户端按 `bridge.conf` 的 `LGDM_BIN` → `PATH`（`lgo_download_manager`）→
   Flatpak（`org.langbiantianya.LGDM`）→ `xdg-open` 依次解析。
   启动时用 `setsid` 脱离宿主进程组：Chrome 收到回帧后会立刻杀掉 host。
3. 回一帧结果后退出：成功 `{"ok":true}`；失败 `{"ok":false,"error":"<code>"}`，
   code 为 `no-url` / `not-lgom` / `no-client`。单帧上限 1 MiB（Chrome 的限制）。

扩展每次交接都先试主机：主机是后装的，装完刷新扩展即可生效，不需要重载浏览器；
失败时按 error 分类，只有"没装"（`not found`）静默降级，其它情况会往扩展
控制台写一条 warning（说明主机装了但用不了）。

### 平台限制

Windows 上原生消息主机必须是**可执行文件**（Chrome 用 `CreateProcess` 直接拉起，
`.bat`/脚本不行，manifest 也没有传参位），因此 `install.sh` 在 Windows 上直接
报错退出：那里的回退是下面「确认提示与回退通道」的单标签页方案，或者让 lgdm.exe
自己实现原生消息主机模式（那就要改 Go 侧了）。

### 排错

- **最近日志里全是 `forwarded-tab`** → 主机没装，或 manifest 的
  `allowed_origins` 里没有当前扩展 ID。`install.sh --extension-id <当前 ID>`
  重装一次即可（改完不需要重载浏览器，但扩展 ID 变了必须重装）。
- **Chrome 侧的原始错误**：带 `--enable-logging=stderr --log-level=1` 启动后
  `grep -E "native_messag|launch_context"`，常见串是
  `Specified native messaging host not found.`（manifest 不在或名字不对）、
  `Access to the specified native messaging host is forbidden.`（ID 没授权）、
  `Native host has exited.`（bridge 读帧失败提前退出，用下面那条命令单独试）。
- **单独验证 bridge**：见「[验证构建](#验证构建)」里的拼帧命令——它不依赖浏览器，
  能直接看出 bridge 是取到了 URL 还是回了 `no-url`/`no-client`。

## 确认提示与回退通道

通过 `lgom://` 交接属于**外部协议启动**，浏览器会在确认对话框（"打开 LGOM？"）
后放行——这个对话框无法在扩展代码里 suppress，属于浏览器安全策略。所以主机
缺失时的目标不是"消灭对话框"，而是"只弹一次、并且不再每次新建标签页"：

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

最近拦截日志里的 `reason` 能看出走的是哪条通道：`forwarded` = 原生主机，
`forwarded-tab` = `lgom://` 回退（也就是主机没装）。

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

| 权限              | 用途                              |
| ----------------- | --------------------------------- |
| `downloads`       | `onCreated` 及 `cancel` / `erase` |
| `webRequest`      | 观察请求头以供交接                |
| `storage`         | 配置、最近日志、请求头缓存镜像    |
| `tabs`            | 打开并复用交接标签页              |
| `nativeMessaging` | 与原生消息主机（bridge）通信      |
| `<all_urls>`      | 拦截任意来源的下载                |

Chrome 声明 `downloads` / `nativeMessaging` / `storage` / `tabs` / `webRequest`；
Firefox 走 `lgom://` 标签页，因此没有 `nativeMessaging`。两者都不需要
`scripting`；均未请求 `webRequestBlocking`（因为只观察请求）。

## 国际化

界面字符串位于 `src/static/_locales/<locale>/messages.json`，
通过 `i18nGetMessage()`（`src/lib/platform-api.js`）读取，该函数封装了
`browser.i18n.getMessage`，在 API 缺失时回退为键本身。
`en` 是 `default_locale`；`zh_CN` 已完整翻译。添加新语言时，
将 `_locales/en` 复制到新语言标签目录下并翻译各条目的 values
——manifest 中的 `__MSG_extensionName__` / `__MSG_extensionDescription__`
占位符也从同一文件解析。

## 目录结构

| 路径                                 | 内容                                                                 |
| ------------------------------------ | -------------------------------------------------------------------- |
| `src/lib/constants.js`               | 协议、存储键、缓存调优、默认值                                       |
| `src/lib/types.js`                   | JSDoc typedef（无运行时导出）                                        |
| `src/lib/platform-api.js`            | `browser`/`chrome` 处理、`i18nGetMessage`、平台名称                  |
| `src/lib/url-builder.js`             | 请求头序列化、`buildLgomUrl`                                         |
| `src/lib/metadata.js`                | `normalizeMetadata`、`shouldIntercept`                               |
| `src/lib/storage.js`                 | 配置加载/保存                                                        |
| `src/lib/recent.js`                  | 最近拦截日志                                                         |
| `src/platform/chrome/`               | MV3 manifest、模块 Service Worker、请求头缓存、协议触发器            |
| `src/platform/chrome/native-host.js` | 原生消息主机客户端（成败分类）                                       |
| `src/platform/chrome/handoff-tab.js` | `lgom://` 回退：复用单个转发标签页                                   |
| `src/platform/firefox/`              | MV3 事件页面、请求头缓存、协议触发器                                 |
| `src/handoff/`                       | 转发标签页承载的扩展页（Chrome 回退通道可见的那个页面）              |
| `src/popup/`                         | 工具栏弹窗（状态、主开关、最近列表）                                 |
| `src/options/`                       | 选项页（协议、过滤规则、关于）                                       |
| `src/static/`                        | 图标和 `_locales` 直接复制到构建输出                                 |
| `src/types.d.ts`                     | 环境 `chrome`/`browser` 声明（不使用 `@types/chrome`）               |
| `src/routes/layout.css`              | Tailwind v4 入口，被两个 UI 界面导入                                 |
| `native-host/lgom-bridge.sh`         | 原生消息主机本体：读一帧、启动客户端、回一帧（POSIX sh，不进扩展包） |
| `native-host/install.sh`             | 注册/卸载主机：写各浏览器 `NativeMessagingHosts` manifest            |
| `scripts/build.mjs`                  | 构建入口：把 Vite 产物组装成 `build/<platform>/`                     |
| `scripts/package.mjs`                | 打包入口：把 `build/<platform>/` 打成 `dist/` 里的 zip / crx / xpi   |

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
// reason 就是走的通道：forwarded = 原生主机，forwarded-tab = lgom:// 回退
await chrome.storage.local.get('lgom_recent'); // [{ ok: true, reason: 'forwarded' }]
```

原生消息主机可以脱离浏览器单独验证：bridge 只依赖 stdin 上的
`4 字节长度 + JSON`，喂它一帧就能看它是否按预期启动客户端并回帧
（下面的 python3 只用来拼帧，bridge 自身不依赖 Python）：

```sh
python3 -c 'import json,struct,sys;m=json.dumps({"url":"lgom://download?url=https%3A%2F%2Fexample.com%2Fa.zip"}).encode();sys.stdout.buffer.write(struct.pack("<I",len(m))+m)' \
  | LGDM_BIN=/bin/echo ./native-host/lgom-bridge.sh | od -c
# 0000000  \v  \0  \0  \0   {   "   o   k   "   :   t   r   u   e   }
# bridge 已把 URL 交给 LGDM_BIN(它的 stdout 被丢弃,想看参数就指向一个记日志的脚本)
```

回退通道（无主机）在真实浏览器里的观测点：`lgom_recent` 的 `reason` 为
`forwarded-tab`，且多次下载后标签页仍是同一个（`chrome.tabs.query({})` 里只有
一个 `handoff.html`）。勾选一次"始终允许"后，后续交接不再弹确认框——记录落在
profile 的 `protocol_handler.allowed_origin_protocol_pairs`。

## 持续集成

`.github/workflows/extension.yml` 在 push / PR / `v*` tag 上跑
`npm ci` + `build:<platform>` + `package:<platform>`，产物作为 artifact 上传
（保留 30 天）；`v*` tag 上把 zip / crx / xpi 一并附到 GitHub Release
（外加 `lgom-native-host-<版本>.tar.gz`——原生消息主机装不到扩展包里，
必须由用户本地执行，所以单独出包）。
版本号用 tag 去掉 `v`（`v1.2.3` → `1.2.3`），非 tag 推送用 manifest 里的版本。

| secret                           | 用途                                                                                               |
| -------------------------------- | -------------------------------------------------------------------------------------------------- |
| `CHROME_EXTENSION_KEY`           | CRX 签名私钥（PEM）。不配则每次构建新生成一把，扩展 ID 每次都变                                    |
| `AMO_API_KEY` / `AMO_API_SECRET` | AMO 的 JWT issuer / secret。配了之后 `v*` tag 的 Firefox 包自动提交 AMO 签名（`channel=unlisted`） |
