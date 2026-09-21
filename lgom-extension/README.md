# LGOM 浏览器扩展

拦截浏览器下载请求，通过 `lgom://` URL 协议将任务移交给 LGOM 桌面客户端
（`lgo_download_manager`）。

一套代码库，两个平台包：共享逻辑位于 `src/lib`，各浏览器独占的
manifest、后台脚本和协议触发器位于 `src/platform/<target>`，
Svelte 5 弹窗和选项页面位于 `src/popup` / `src/options`。

## 系统要求

| 主机                              | Manifest | 后台上下文                    |
| --------------------------------- | -------- | ----------------------------- |
| Chrome / Chromium / Edge 102+     | MV3      | ES 模块 Service Worker        |
| Firefox 140+                      | MV3      | ES 模块事件页面               |

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

| 脚本                   | 效果                                |
| ---------------------- | ----------------------------------- |
| `npm run build:chrome`  | Chrome 包输出至 `build/chrome/`     |
| `npm run build:firefox` | Firefox 包输出至 `build/firefox/`   |
| `npm run build:all`     | 同时构建两者                         |
| `npm run dev`           | 监听文件变更自动重新构建 Chrome 包   |
| `npm run dev:firefox`   | 同上，针对 Firefox                  |
| `npm run check`         | `svelte-check`（基于 `jsconfig.json`）|
| `npm run lint`          | `prettier --check` + `eslint`       |
| `npm run format`        | `prettier --write`                  |

每次构建会将 Vite 输出先暂存到临时目录，再组装成扩展根目录，
确保两个平台的文件互不污染。产物结构：

```
build/<platform>/
  manifest.json      # src/platform/<platform>/manifest.json
  background.js      # 打包后的后台入口
  chunks/            # 入口静态导入的共享代码块
  popup.html|css|js  # src/popup
  options.html|css|js
  icons/             # src/static/icons
  _locales/          # src/static/_locales
```

## 加载扩展

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

未打包的 Chrome 扩展 ID 由目录路径决定，移动 `build/chrome` 会改变 ID。
在扩展 ID 被加入白名单（如未来的原生消息主机 manifest）之前，
请在 Chrome manifest 中使用 `key` 固定 ID。

## 拦截原理

1. `webRequest.onBeforeSendHeaders`（仅观察，不阻塞）将每个 URL 的请求头
   缓存到内存 `Map`，并以 250 ms 防抖写入 `storage.session` 镜像。
   两个主机都会在空闲时清除后台上下文，因此仅靠内存 Map 不够；
   Chrome 需要 `extraHeaders` 才能获取 `Cookie`/`User-Agent`。
2. `downloads.onCreated` 事件触发。如拦截已禁用或 URL 不匹配当前过滤模式，
   则不处理。
3. 下载任务被取消并擦除。
4. 缓存的请求头经 `normalizeMetadata` 转为元数据，`buildLgomUrl` 生成交接 URL。
5. `triggerProtocol` 打开一个指向 `lgom://` URL 的后台标签页，1 秒后移除。
   Firefox 还会注入一个隐藏 iframe 作为备选方案，因为它可能拒绝向未知外部
   协议发起顶层导航。
6. 结果追加到最近拦截日志（最新优先，最多 50 条）——包括交接失败，
   这样弹窗不会在下载已被拦截但转发失败的情况下显示"尚无拦截记录"。

## 交接 URL 协议

```
lgom://download?url=<编码>[&name=<编码>[&ua=<编码>][&headers=<编码>&cookies=<编码>]]
```

`headers` 为 `Name: value` 逐行列表；逐跳和主机绑定头
（`host`、`connection`、`content-length`、`accept-encoding`、`transfer-encoding`、
`te`、`trailer`、`upgrade`）会被剥离。当完整编码后的 URL 超过 `maxUrlLength` 时，
降级为仅含核心参数（`url`、`name`、`ua`），以保证交接 URL 保持在
LGOM IPC 帧限制内（`MAX_FRAME_LEN`，64 KiB）。

### 浏览器确认提示

通过 `lgom://` 交接属于**外部协议启动**，浏览器会在确认对话框
（"打开 LGOM？"）后放行。该对话框无法在扩展代码中 suppress——
这是浏览器安全策略，而非扩展行为。一次性解除方法：

- **Chrome：** 在对话框中勾选"始终允许……打开此类链接"。
  管理员可通过 `AutoLaunchProtocolsFromOrigins` 策略预批准。
- **Firefox：** 在 `about:config` 中将
  `network.protocol-handler.warn-external.lgom` 设为 `false`。

完全移除提示需要一个浏览器不设卡的通道——`runtime.sendNativeMessage`
（需桌面应用安装原生消息主机）或回环 HTTP 桥接。两者均未实现。

## 配置

设置项位于选项页（`chrome://extensions` → 详情 → 扩展选项；
`about:addons` → 偏好设置）。

| 字段           | 默认值  | 说明                                        |
| -------------- | ------- | ------------------------------------------- |
| `enabled`      | `true`  | 主开关；关闭时不处理任何下载                |
| `mode`         | `all`   | `all` \| `whitelist` \| `blacklist`        |
| `patterns`     | `[]`    | 正则表达式；无效模式降级为子字符串匹配      |
| `maxUrlLength` | `2000`  | 保存时限制在 200–8000 范围内                 |

过滤语义：`all` 拦截所有；`whitelist` 仅拦截匹配的 URL；`blacklist`
拦截除匹配项外的所有。

存储键：`lgom_config`（`storage.local`）、`lgom_recent`
（`storage.local`）、`lgom_header_cache`（`storage.session`）。

## 权限

| 权限                    | 用途                                      |
| ----------------------- | ----------------------------------------- |
| `downloads`             | `onCreated` 及 `cancel` / `erase`         |
| `webRequest`            | 观察请求头以供交接                         |
| `storage`               | 配置、最近日志、请求头缓存镜像             |
| `tabs`                  | 打开并移除交接标签页                       |
| `scripting`（仅 Firefox）| 协议启动的隐藏 iframe 备选方案            |
| `<all_urls>`            | 拦截任意来源的下载                         |

权限按平台声明：Chrome 不含 `scripting`（因为没有 iframe 备选方案）；
两个 manifest 均未请求 `webRequestBlocking`（因为只观察请求）。

## 国际化

界面字符串位于 `src/static/_locales/<locale>/messages.json`，
通过 `i18nGetMessage()`（`src/lib/platform-api.js`）读取，该函数封装了
`browser.i18n.getMessage`，在 API 缺失时回退为键本身。
`en` 是 `default_locale`；`zh_CN` 已完整翻译。添加新语言时，
将 `_locales/en` 复制到新语言标签目录下并翻译各条目的 values
——manifest 中的 `__MSG_extensionName__` / `__MSG_extensionDescription__`
占位符也从同一文件解析。

## 目录结构

| 路径                          | 内容                                                    |
| ----------------------------- | ------------------------------------------------------- |
| `src/lib/constants.js`        | 协议、存储键、缓存调优、默认值                          |
| `src/lib/types.js`            | JSDoc typedef（无运行时导出）                           |
| `src/lib/platform-api.js`     | `browser`/`chrome` 处理、`i18nGetMessage`、平台名称     |
| `src/lib/url-builder.js`       | 请求头序列化、`buildLgomUrl`                            |
| `src/lib/metadata.js`         | `normalizeMetadata`、`shouldIntercept`                   |
| `src/lib/storage.js`          | 配置加载/保存                                          |
| `src/lib/recent.js`           | 最近拦截日志                                           |
| `src/platform/chrome/`        | MV3 manifest、模块 Service Worker、请求头缓存、协议触发器|
| `src/platform/firefox/`       | MV3 manifest、事件页面、请求头缓存、协议触发器 + iframe 备选|
| `src/popup/`                  | 工具栏弹窗（状态、主开关、最近列表）                     |
| `src/options/`                | 选项页（协议、过滤规则、关于）                          |
| `src/static/`                 | 图标和 `_locales` 直接复制到构建输出                    |
| `src/types.d.ts`              | 环境 `chrome`/`browser` 声明（不使用 `@types/chrome`） |
| `src/routes/layout.css`       | Tailwind v4 入口，被两个 UI 界面导入                   |

项目使用 JavaScript + JSDoc 类型注解，而非 TypeScript；`jsconfig.json`
启用了 `checkJs`，`svelte-check` 是类型检查关卡。

## 验证构建

```sh
npm run check && npm run lint
```

运行时检查：加载构建好的扩展，通过 DevTools 协议驱动，后台脚本的存储是最易观测的信号：

```js
// 在 Service Worker 上下文中
await chrome.downloads.download({ url: 'https://example.com/file.zip' });
await chrome.downloads.search({}); // 拦截取消擦除后为空 []
await chrome.storage.local.get('lgom_recent'); // [{ ok: true, reason: 'forwarded' }]
```
