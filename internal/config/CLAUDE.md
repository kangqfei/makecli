# internal/config/
> L2 | 父级: /CLAUDE.md

## 成员清单
- `atomic.go`: 落盘原语 atomicWrite（同目录 temp + ReplaceFile 原子替换，render 出错清理不留残留），被 Save/SaveConfig 复用，消除 O_TRUNC 直写的崩溃损坏窗口；替换动作收口到 build-tag 分发的 ReplaceFile（POSIX 直通 rename，Windows 单步覆盖原语退避重试，两分支合同一致：失败时目标原样保有旧内容）
- `atomic_replace.go`: ReplaceFile 平台分支（!windows）——POSIX rename(2) 对既有目标本就原子覆盖，直接透传 os.Rename；导出供 internal/notifier 缓存落盘复用
- `atomic_replace_windows.go`: ReplaceFile 平台分支（windows）——失败安全替换：只用单步覆盖原语（renameFile=os.Rename，MoveFileEx+REPLACE_EXISTING；包级变量供失败注入测试）退避重试 3 次 ×10ms，任何失败路径都不触碰既有目标——dst 要么已是新内容、要么原样保有旧内容，无路径缺失窗口；目标被独占时如实报错让调用方重试，不冒丢数据险；零额外依赖（刻意不引入 golang.org/x/sys）
- `atomic_replace_windows_test.go`: Windows 分支的失败合同测试（windows-tagged）——renameFile 注入失败证明重复失败后 dst 原样保有旧内容且每次尝试时在位、重试计数正确、前失败后成功的重试出口
- `atomic_test.go`: 覆盖 atomicWrite 落盘内容/权限0600/无临时残留/覆盖既有文件/render 出错传播不落盘 + ReplaceFile 非 Windows 分支覆盖既有目标（内容替换/源消失/权限保留）
- `paths.go`: 配置目录解析中枢，提供 Dir 函数与 EnvConfigDir 常量，$MAKE_CLI_CONFIG_DIR 非空时覆盖默认 ~/.make
- `paths_test.go`: 覆盖 Dir 默认回退与 $MAKE_CLI_CONFIG_DIR 覆盖语义，串联 Save/Load 的 env 隔离测试
- `credentials.go`: 读写 credentials 文件（默认 ~/.make/credentials，INI 格式），提供 Load/Save/CredentialsPath，Credentials/Profile 类型（Profile 含 access_token[login 写] 与 node_key[daemon 入册写]——同段共存，Load→Save 两字段都保留，daemon 写 node_key 不抹 access_token；node_key 非空才输出行）；Save 经 atomicWrite 原子落盘，落盘前过 INI 注入防线（ValidateProfileName 文法+保留名、validateINIValue 拒 token/node_key 含换行/首尾空白）
- `credentials_test.go`: 覆盖 parseINI（白盒）+ Load/Save 全路径测试 + INI 注入拒绝（"evil]\n[other" profile 名、token 带换行/首尾空白，且拒绝时不落文件），用 t.Setenv("HOME",...) 隔离文件系统
- `config.go`: 读写 config 文件（默认 ~/.make/config，INI 格式），提供 LoadConfig/SaveConfig/SetSetting/MigrateSettings/ConfigPath，Config/ConfigProfile 类型（含 Context/MetaServerURL/RepoServerURL/AuthServerURL/XTenantID/OperatorID，INI key: context / meta-server-url / repo-server-url / auth-server-url / X-Tenant-ID / X-Operator-ID；auth-server-url 为 OAuth 身份服务器基址，供 login 派生 .well-known 元数据地址）；唯一写路径 saveConfigWithSettings（profile 段 + 显式 [settings] 段，经 ValidateProfileName 拒绝保留名 settings 作 profile）：SaveConfig 传磁盘现状以保留 [settings]，[settings] 的改写统一经 updateSettings 读-改-写原语——SetSetting 改单键、MigrateSettings 按 settings.go 的 legacySettingKeys 表把旧键搬到新键（新键已存在则只删旧键，返回搬家清单，被 cmd/doctor 消费）；落盘前过 INI 注入防线（ValidateProfileName + validateINIKey/validateINIValue：键限保守文法、值拒换行与首尾空白，防 "x\n[evil]" 伪造 section）；validateINIKey/validateINIValue 由此定义、credentials.go 复用；parseINISections 通用 INI 解析器供 settings.go 复用
- `config_test.go`: 覆盖 parseConfigINI（白盒）+ LoadConfig/SaveConfig 全路径测试 + INI 注入拒绝（profile 值带换行/首尾空白、SetSetting 非法键与换行值），复用 writeTempINI helper
- `channel.go`: 发布通道域常量（ChannelStable/ChannelBeta/DefaultChannel=stable + ChannelNames），与 context.go 同责的域取值单一真相源；stable 只跟踪正式版、beta 额外跟踪 prerelease，被 cmd 层（resolveChannel/setChannel）与 internal/notifier（channelOf/versionInChannel）消费
- `channel_test.go`: 覆盖通道常量/名称集与 LoadSettings 读取 [settings] channel（配置/未配置三态），$MAKE_CLI_CONFIG_DIR 隔离文件系统
- `context.go`: 后端 context 拓扑中枢，把 dev/test/production 三套**主机基址**（make/make-repo/myaccount/make-agent/openobserve，均为 scheme://host 不含路径；dev/test 用 qtech.cn 且带 {dev-,test-} 前缀，production 用 qfei.cn）收成一等 Context preset；词汇约定：context = 连哪套 Make 后端，environment 只属于 app 的部署环境（beta/production），两者不混用；AgentGatewayURL 供 daemon 直连（无网关前缀）；TraceServerURL 供 trace 子命令拼 OpenObserve 直达页（dev/test 共用 openobserve.qtech.cn，production 用 openobserve.qfei.cn）；Meta/Repo 的网关前缀 /api/make 不在此、由 cmd 层 withGateway 补齐，Auth 基址供 login 追加 .well-known；提供 LookupContext（空名回退 DefaultContext=production，未知名 ok=false）/ ContextNames（固定 lifecycle 顺序 dev → test → production，非字典序，help/错误提示/context list 同序）；作 cmd 层 URL 解析链的兜底层（flag > profile config > context preset）
- `context_test.go`: 覆盖 LookupContext（空回退/三 context 完整 preset/未知名）+ ContextNames 固定顺序且与 preset 表一一对应、DefaultContext
- `role.go`: 角色域常量（RoleUser + RoleNames={user}），与 channel.go / context.go 同责的域取值单一真相源；role 是这台机器上的人的 persona，与 context（连哪套后端）、profile（哪份凭证）正交，[settings] role 持久化；**未设置 = 原有行为**（skills 全量），不另立 developer 值；今天只有 internal/skillsync RoleSkills 消费（user 只装 makecli）
- `role_test.go`: 覆盖 RoleNames 只含 user + LoadSettings 读取 [settings] role（未配置为空 / 配置值原样）+ UnsetSetting 删键保留其余键与 profile、幂等
- `settings.go`: 读取 config 文件 [settings] 全局段，提供 Settings 类型（CheckForUpdates *bool / Context string / Channel string / Role string / Legacy map）与 LoadSettings、ValidateProfileName（保守文法 ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ 防 profile 名注入 INI section 语法 + 拒绝保留段名 settings 作 profile，防 [settings] profile 段与全局段碰撞；validProfileName 正则由此定义）；legacySettingKeys 表（旧键 → 新键，现含 environment → context）：读路径只把命中的旧键登记进 Settings.Legacy 不翻译，解析链不背历史包袱，搬家由 config.go MigrateSettings（doctor）一次性完成，新增改名只需加一行；settingsSection 常量由此定义，profile 解析层自动跳过该段；写入走 config.go 的 SetSetting
- `settings_test.go`: 覆盖 LoadSettings（check-for-updates 三态 + context 读取 + 旧键 environment 登记进 Legacy 且不翻译 / 无旧键 Legacy 为 nil）+ SetSetting 写入并保留 profile/其他 settings 键 + MigrateSettings（搬家保留其余键与 profile / 新键已存在时新键为准 / 无事可做返回空）+ LoadConfig 隔离 [settings] 段 + ValidateProfileName 文法全谱（合法名/64 字符边界/空名/括号换行空白/非法首字符/超长全拒）/Save 拒绝保留名

[PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md

- 2026-09-22：profile 新增可选 context；INI 读写与全局 settings 改写均保留该值。AGENTS.md 链接本文件，共用模块地图。
