# lgom:// URL 协议

URL 格式：

```text
lgom://download?url=<encoded>
  [&name=<encoded>[&ua=<encoded>[&headers=<encoded>[&cookies=<encoded>]]]]
```

只有 `url` 必填。Windows 安装包注册：

```text
"<install>\lgdm.exe" "%1"
```

Linux 通过 `.desktop` 的 `MimeType=x-scheme-handler/lgom` 注册；Flatpak 会把同一
份桌面文件导出给宿主，并在导出的 `Exec` 中使用
`flatpak run … @@u %u @@`，让 `%u` 作为位置参数传给主进程。

## 冷启动与单实例转发

没有实例运行时，系统启动的新进程就是主实例，自己处理 URL。已有实例运行时，
新进程通过 Windows 命名管道或其它平台的 Unix socket 把 URL 转交给主实例，然后
退出。主实例刚退出导致转发失败时，新进程以非零状态结束并打印原因。

`--open-url` 和位置参数都支持 `lgom://` URL。多个 URL 会逐个转发，首个失败不会
丢弃后续 URL。

## 支持的下载来源

`url` 不限下载协议。支持 HTTPS、FTP、WebDAV，也接受 URL 编码后的磁力链接：

```text
lgom://download?url=magnet%3A%3Fxt%3Durn%3Abtih%3A…
```

以及 HTTP `.torrent` 链接和本地种子路径。BT URL 会进入「新建任务」对话框并切换
为保存目录语义。

默认通过 UI 确认后加入下载；`--no-gui` 下直接 Add + Start。`--autostart` 只影响
首次登录启动是否显示窗口。
