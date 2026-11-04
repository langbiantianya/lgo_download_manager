# 开机自启

设置中的 **Auto start** 开关启用后，业务主进程在操作系统登录时静默启动：不显示
主窗口，只保留下载调度器和系统托盘。用户可从托盘打开 UI，扩展请求也会按需拉起
界面。

| 平台 | 注册位置 | 命令 / 参数 |
| --- | --- | --- |
| Windows | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` | `"<exe>" --autostart` |
| Linux | `$XDG_CONFIG_HOME/autostart/lgo_download_manager.desktop` | `Exec=<exe> --autostart`，`X-GNOME-Autostart-enabled=true` |
| Linux（Flatpak） | 宿主 `~/.config/autostart/lgo_download_manager.desktop` | `Exec=flatpak run org.langbiantianya.LGDM --autostart`，`X-Flatpak=org.langbiantianya.LGDM` |
| macOS | `~/Library/LaunchAgents/org.langbiantianya.LGDM.plist` | `ProgramArguments`、`RunAtLoad=true`、`ProcessType=Background` |

Flatpak 沙箱中的 `/app/bin/lgo_download_manager` 在宿主上不存在，因此注册项必须用
宿主可执行的 `flatpak run`，并写入宿主 autostart 目录。入口目录由 Flatpak
manifest 的 `--filesystem=xdg-config/autostart` 映射进沙箱。

`--autostart` 只控制首次启动是否显示窗口，不改变后续 URL/扩展请求的交互规则。
只有显式 `--no-gui` 才会跳过新建任务对话框并直接入队。

启动时 `settings.ReconcileAutoStart` 以持久化设置为准，修正第三方工具修改或
安装卸载留下的状态漂移。注册失败只记录日志，不让整个 UI 保存操作失败。

Linux 的桌面入口使用 `NoDisplay=true`，仅用于自启；macOS 使用
`ProcessType=Background`；Windows 写 HKCU，不需要管理员权限。
