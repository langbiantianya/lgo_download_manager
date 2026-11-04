# BitTorrent

BT 任务与 HTTP 分片任务共用任务列表、状态机和排序，底层由
`internal/bittorrent` 封装 [anacrolix/torrent](https://github.com/anacrolix/torrent)。
进程内共享一个 BT 客户端，包括监听端口、DHT 路由表和连接池；每个任务持有自己的
种子句柄和任务目录。客户端懒创建：没有 BT 任务时不监听端口，也不建立 DHT。

## 添加任务

把下列任意 URL 填入「新建任务」，也可经 `lgom://` 或浏览器扩展转发：

| URL 形态 | 处理方式 |
| --- | --- |
| `magnet:?xt=urn:btih:…&dn=…` | 从 DHT / tracker / peer 获取元数据 |
| `https://…/foo.torrent[?query]` | 通过 HTTP 代理设置获取种子 |
| `/path/to/foo.torrent`、`file:///path/foo.torrent` | 直接读取本地种子 |

`.torrent` 可立即显示精确总体积；磁力链接在获得元数据前显示「未知大小」，期间每
30 秒记录一次 `waiting for metadata` 日志，便于区分网络问题和等待状态。

## 保存路径

BT 保存路径是任务目录。默认目录名来自磁力链接 `dn` 参数（没有则用 infohash
前缀）或种子文件名。内容位于 `<任务目录>/<种子名>/…`，单文件种子为
`<任务目录>/<文件名>`。

- 未完成文件带 `.part` 后缀，完成后由客户端重命名。
- 「打开文件夹」打开任务目录；单文件种子可直接打开文件。
- 删除任务会连同任务目录和未完成数据一起删除，所有任务删除前都会确认。

## 断点续传与恢复

暂停会摘除种子并断开 peer。恢复时重新添加同一个种子，客户端识别已完成文件和
`.part`，只补缺失部分。首次取得磁力元数据后，info 字典缓存到
`lgdm.sqlite` 的 `bt_metainfo` 表，之后即使离线或 tracker 不可用也可继续。
进程异常退出后的 `ReclaimDownloadingTasks` 对 BT 同样生效。

## 做种与连接

默认分享率为 2，同时最多做种 2 个任务。完成任务不占下载并发槽位；超过做种上限的
任务保留种子但暂停上传，按 FIFO 接替。分享率设为 0 表示不进行做种，删除任务或
退出程序也会停止对应做种。

- tracker 公告、webseed 和种子 HTTP 抓取使用公共代理设置；BT peer 连接不受
  HTTP 代理影响。
- 首次启动探测可同时绑定 TCP/UDP 的端口并持久化；设 `0` 表示每次启动随机分配。
- 要提升入站连接速度，需要配置固定端口并在路由器映射；lgdm 不启用
  UPnP/NAT-PMP。
- DHT 和 PEX 默认开启，磁力链接没有 tracker 时主要依赖它们寻找 peer。

## 任务详情

BT 的详情窗口不复用 HTTP 任务的字节区间视图，而是展示：

- infohash、种子名、来源 URL、保存目录、添加/完成时间；
- 下载/做种/完成状态、上下行速度；
- 总大小、已下载、已上传、分享率；
- 已连接 peer、DHT/tracker 节点数、做种者和下载者；
- 分片大小/总数/完成数、tracker 列表、文件清单和进度。

底部按 BT piece 聚合成最多 1024 个桶绘制热力图。桶值随 `bt_info` 快照每秒更新。
peer 数等易变数据不写入 `store.Task`；BT 句柄由 scheduler 单独管理，暂停且不做种
后句柄被摘除，详情显示「会话已结束」。

与普通任务共用进度、速度、ETA、筛选、排序、搜索、并发上限、FIFO 和全局限速。
同一个任务最多打开一个详情窗口。

## 设置生效

限速修改立即生效。监听端口、DHT、PEX、连接上限等客户端构造参数在无 BT 任务时
立即重建客户端；否则延后到最后一个 BT 任务（包括做种中的任务）结束。

## 依赖

`github.com/anacrolix/torrent` 的存储层在 cgo 下会编入 cgo 版 SQLite，用于分片
完成状态，与 lgdm 自己的 `modernc.org/sqlite` 无关。项目 GUI 构建本来就要求 cgo，
所以正式二进制没有额外构建依赖。

## 测试

```sh
go test -v ./internal/bittorrent/
go test -v ./internal/scheduler/ -run Bittorrent
```

`TestDownloadFromLoopbackPeer` 是端到端用例：启动本机做种端，用 lgdm Manager
下载并验证进度、文件列表、内容、`.part` 重命名和真实上传。需要监听回环端口的用例
在受限沙箱中会跳过。
