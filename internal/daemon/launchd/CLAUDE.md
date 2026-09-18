# internal/daemon/launchd/
> L2 | 父级: ../CLAUDE.md

daemon 的 macOS 托管面:把前台 `makecli daemon` 固化成用户级 LaunchAgent(~/Library/LaunchAgents/cn.qfei.makecli.daemon.plist),由 launchd 负责登录自启与退出拉起(RunAtLoad + KeepAlive)。只做 macOS,平台守卫在 cmd 层。

两条硬约束决定了本包的形状:
1. **launchd 不继承 shell 环境**——没有 .zshrc、没有 export 的变量。故 Config 里每一项都必须是「已解析的最终值」:gateway 地址等参数写死进 ProgramArguments,PATH 显式进 EnvironmentVariables(缺了就探测不到 claude / codex)。
2. **plist 留在磁盘上就等于登录自启**——launchd 每次登录扫描 LaunchAgents 目录。故 Stop 是 bootout + **disable**(往 override 库写持久记录),不是只 bootout,否则"停了却自己回来"。反过来 Install / Restart 必须先 enable 抵消它,Uninstall 也要 enable 收尾清记录——记录以 Label 为键独立于 plist 存在,留着会让下次安装装出一个"存在但被禁用"的服务,症状是启动无声无息什么也不发生。

成员清单
launchd.go: Label 常量、Config(BinaryPath/Args/Env/WorkingDir/LogPath)、Status(Installed/Loaded/Running/Disabled 四个正交事实)、ErrNotInstalled 哨兵;Render(text/template + xml.EscapeText 逐值转义,环境变量按键名排序保证同 Config 同字节)、PlistPath、Install(写 plist → enable → bootout 旧实例 → bootstrap → kickstart,覆盖式:改了参数重跑立刻生效;bootstrap 带 3 次退避重试兜 launchd 异步卸载竞态——bootout 立刻返回但服务真退出是异步的,紧跟的 bootstrap 会报 `5: Input/output error`)、Stop(bootout + disable,**保留 plist**,disable 失败必须上抛——用户会以为停干净了)、Uninstall(bootout + enable 清 override + 删 plist,返回此前是否托管)、Restart(复用磁盘 plist 不重渲染,故 start 敲定的参数原样保留;plist 不存在返回 ErrNotInstalled)、Query(plist 给"配置成什么样",launchctl list 给"现在活没活",print-disabled 给"登录还会不会自启";queryDisabled 兼容 `=> true` 与 `=> disabled` 两种 macOS 印法,读不出一律当未禁用不让 status 整体失败);launchctl 调用收口在包级 runLaunchctl 单一出口(单测打桩点),域固定 gui/$UID;parsePlist 按 <key> 与其后兄弟节点配对读回自家 plist(嵌套 dict 整体跳过,不是通用解析器)
launchd_test.go: 渲染转义与读回往返、渲染确定性、Install/Stop/Uninstall/Restart 的 launchctl 调用序(enable 打头、stop 必带 disable、uninstall 必带 enable 收尾)与文件效果(stop 留 plist / uninstall 删)、disable 失败上抛、bootstrap 重试自愈与失败不 kickstart、Query 三态 + Disabled 识别(两种印法 + 别的 Label 不误伤);打桩 runLaunchctl + t.Setenv("HOME") 隔离,非 macOS 机器也能跑全路径

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

测试同时隔离 HOME 与 USERPROFILE，保证 Windows 的 os.UserHomeDir 也只写临时目录。
