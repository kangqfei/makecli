# makecli - qfeius 的命令行工具
Go 1.25.8 + github.com/spf13/cobra + github.com/go-git/go-git/v5（app init/create/deploy 纯 Go 操作 git，不 shell-out，二进制自包含；deploy 是「纯 push 已提交状态」，提交时机交还用户）+ charm.land/huh/v2（app delete 删除确认交互表单；go directive 因其要求顶到 1.25.8，需 ≥1.25.8 工具链构建）

<directory>

- `agents/` - 脚手架模板文件（CLAUDE.md.tmpl / AGENTS.md.tmpl 由 app create 写出；gitignore.tmpl 是 .gitignore 期望清单单一真相源，由 cmd/git ensureGitignore 增量补齐），.tmpl 后缀避开 GEB L2 撞名，通过 embed.FS 编译进二进制
- `cmd/` - Cobra 子命令层（root（全局 --profile / --access-token / --meta-server-url / --context；代码仓库主机无 flag 级、只走 $MAKE_REPO_SERVER_URL > profile > 内置地址；覆盖类 flag 三级可配置来源同构 flag > $MAKE_<KEY> 环境变量 > profile 文件，主机地址再以 context 内置地址兜底，收口 client.go resolveAccessToken / metaServerURL / repoServerURL；词汇约定：context = 连哪套 Make 后端 dev/test/production，environment 只指 app 的 beta/production 部署环境）、version、configure[token/config/set/get/verify/resolve]（只管 profile 段）、settings[set/get/list]（只管 [settings] 全局段：context / channel / role / check-for-updates，settingKeys 表驱动，命令面与 INI 段 1:1；configure 收到全局键报错指路不转发）、context[list/use/show]（对标 docker context：内置 preset 无 create/rm，use 写 [settings] context）、doctor（本地配置体检，默认只读对齐 brew/flutter doctor：可修项标 fixable 指引 --fix，`doctor --fix` 才把旧键 [settings] environment → context 原地搬家，修不了的给 next-step，仍有问题退出码 1；解析链遇旧键只报错指引 doctor --fix，不背兼容包袱）、app[create/init/list/info/delete（--env production|beta|all 必填，始终传 prod key + ?appRole= 让服务端定位目标、不反查 pairAppKey，all 先 beta 后 production；prod 有 beta 配对时服务端 409 拒删）/deploy（只推 beta，无 --env；production 只能经 promote 从 beta 发布）/promote（对标 vercel promote：把 beta 当前生效版本发布到 production，输入是服务端 beta 状态而非本地代码，不 push；回执 workflowId+runId 落盘 ~/.make/promote/<context>--<betaKey>.json 供 --status 免抄 ID；--wait 轮询至终态退出码 0/2/124；production 确认护栏 confirmProductionPromote）]、entity、relation、record[create/get/update/delete/list/aggregate（/data/v1/aggregate 声明式 GROUP BY：--group-json 维度、--aggregates-json 指标、--filter WHERE、--aggregate-filter HAVING、--sort-json）]、apply、diff、update、skills[list/install（--role user 只装 makecli 并写 [settings] role，--all 全量并清除 role；未设置 role 一切照旧；update 后置同步与 list 的 available 分母都按 role 分流，角色名单表在 internal/skillsync role.go）/update/remove]、schema、integration[ocr]、preflight、login、daemon[隐藏，含 start/stop/restart/status/uninstall launchd 托管子命令]、agent[隐藏]、trace[隐藏，按 --id 与 --days/--hours/--minutes 窗口拼 OpenObserve trace 直达 URL 并开浏览器]）；git.go 收口共享 go-git 原语；jsonflag.go 收口 JSON 型 flag（对齐 lark-cli：命名 = API 字段名 + `-json`，取值三形态 inline | @file | -（stdin），DisallowUnknownFields 严格解码、取值交服务端裁决）
- `internal/api/` - Make Meta/Data/Integration Service HTTP 客户端（Client + functional options，X-Make-Target 路由 + 自定义 headers 注入 + 每请求注入 W3C Traceparent/X-Log-Id + WithDryRun 时注入 X-Dry-Run + `--debug` 请求/响应转储（debug.go：curl -v 风格文本或 `--output json` 时的 {request,response,timing} JSON，形态由 cmd/root PersistentPreRun 落定的 resolvedOutput 决定）（CreateResource 全族写命令 --dry-run 共用：远端跑真实流程但 ROLLBACK 不落库）；Meta 操作走 /meta/v1/，Record 操作走 /data/v1/（聚合统计 /data/v1/aggregate，与列表共用 listPage 解码），Integration 操作走 /integration/v1/，构建任务查询走 /build/v1/build（`app deploy --status` 按本地 HEAD commitSha 反查部署进度，`--wait` 轮询至终态、退出码 0/2/124 语义化），部署总览查询走 /deployment/v1/deployment/overview（`app info` 展示双环境部署状态与 URL），应用环境发布走 /console/v1/app-environment/product（CreateResource 发起 beta → production、StatusResource 按 workflowId+runId 查进度，`app promote` 消费），代码仓库操作走独立 host 的 /code/v1/repository（请求带 ?version=<CLI 版本>，服务端据此做强制升级门禁））
- `internal/trace/` - W3C Trace Context 出站头生成（零依赖手写 traceparent v00；进程级 trace-id 单一真相源——每次 CLI 调用一个、全程稳定，X-Log-Id=trace-id 段，parent-id 每请求新生成），被 internal/api 请求咽喉点消费
- `internal/oauth/` - 浏览器 OAuth 登陆原语（PKCE + 单跳 discovery + RFC 7591 动态注册 + 授权URL/换token + 动态端口回调 server），从 contract-cli 移植，被 cmd/login 编排
- `internal/build/` - 构建元数据（Version/Date，由 ldflags 注入）
- `internal/config/` - 凭证与配置管理（读写 credentials 和 config，INI 格式；默认 ~/.make，可用 $MAKE_CLI_CONFIG_DIR 覆盖）；内建 dev/test/production context preset（后端主机基址 + Agent gateway + OpenObserve 基址，scheme://host 不含路径），全局 [settings] context 选当前 context（旧键 environment 只登记进 Settings.Legacy，MigrateSettings 供 doctor 搬家），URL 解析链 flag > $MAKE_*_SERVER_URL > profile config > context preset（链在 cmd/client.go）；发布通道常量 stable/beta（channel.go），全局 [settings] channel 选通道；Meta/Repo 网关前缀 /api/make 由 cmd 层 withGateway 自动补齐（配置只写主机名）
- `internal/update/` - 自更新引擎（GitHub Releases 查询、下载、原子替换二进制）；CheckLatest 双通道：stable 走 /releases/latest（GitHub 服务端过滤 prerelease），beta 走 /releases 列表取 semver 最高（候选含稳定版，反超自动收敛回 stable）
- `internal/skillsync/` - Make platform skills 同步/清单/删除/安装（Sync 默认每次 npx 安装/升级 qfeius/make-platform-skills --all，--skip-skills 跳过；List 合并 lockfile + SKILL.md + GitHub Contents API 做 outdated 比对；Remove 两阶段逐个删除（PlanRemove lockfile 校验/--all 展开，绝不透传上游 --all）；PlanInstall 两阶段按名/全量安装（npx 环境门禁与远端校验）；Install 执行 npx 安装），被 cmd/update 与 cmd/skills 消费
- `internal/daemon/` - Agent 平台设备接入（隐藏命令 `makecli daemon`）：注册/心跳/claim 轮询驱动本机 coding CLI（claude-code / codex adapter），claim 的 description 身份职责与 instructions 执行要求渲染进 CLI 原生上下文文件，最终答复经 @Name 解析产出结构化 mention 块（互@触发，agent-design/Design.md §7.5），协议 wire 类型镜像 agent-design/Contract.md（公开仓库无法 import 私有 agent-contract）；子包 `launchd/` 是 macOS 托管层——把前台形态固化成用户级 LaunchAgent（登录自启 + 退出拉起），供 `daemon start/stop/restart/status` 驱动
- `internal/agent/` - keyless 本地 code agent（隐藏命令 `makecli agent`，agent-design/Design.md §8.2）：默认即 code agent——gateway Provider（llm/gateway.go，OpenAI 兼容 SSE 指向 /v1/chat/completions，平台 token 只开模型门、设备端零厂商 key）+ 七工具注册表（root=cwd）+ 目录信任确认钩子（副作用工具 bash/write/edit 逐次 y/n/a，--approve 免确认）+ 两层循环行式渲染（一次性 -p / 交互 REPL，历史进程内存续）+ REPL 的 `!<cmd>` 本地命令直通（bang.go，对齐 Claude Code：不发起 LLM 请求，直接跑本机 shell，转录进历史；用户亲手敲的命令不过目录信任门控）；--chat-only 退回纯聊天 ChatStream（同样带直通）；内核五子包 core（叶子类型/事件流）、tool（read/write/edit/grep/find/ls/bash + Schema 校验执行器）、llm（StreamFn 流式抽象 + GatewayProvider）、loop（两层循环 + 提示词组装）、trust（目录信任持久化）移植自 github.com/smallnest/pigo（MIT，剥离 compaction/subagent/todo/webfetch）
- `npm/` - npm 分发层（`@qfeius/makecli`）：bin/makecli.js 是主包唯一 JS——用 require.resolve 定位 `@qfeius/makecli-<platform>-<arch>` 子包内的 Go 二进制并 spawnSync 透传；build.js 读 GoReleaser 的 dist/artifacts.json 生成 6 个平台子包（携带二进制、声明 os/cpu）+ 1 个主包（optionalDependencies 精确钉住同版本子包），stdout 按发布顺序输出目录供 release.yml 逐个 `npm publish`；安装时零下载、零 postinstall，镜像与代理全由 npm 自身处理
- `skills/` - git submodule → qfeius/make-platform-skills（超项目钉住 commit、跟踪 main，`make sync` 拉齐，发版前 /ship Step 0 自动 bump 并提交，build/test/vet/lint 均以 sync 为前置；CI/release checkout 开 submodules）；内容单一真相源，本仓库不改其文件；由根包 embed.go 嵌入，geb lint 显式 --exclude
- `internal/skillcontent/` - 内嵌 skill 内容的读取层（`makecli skills read <skill>[/<path>]`，对齐 lark-cli skills read）：reader.go Read 作用于任意 fs.FS，解析目标 path 缺省 SKILL.md、目录则列一层、错误自带导航（未知 skill 附嵌入清单 / 未找到附顶层条目）；生产 FS 来自根包 embed.go 经 main.go 注入 cmd.SkillContentFS
- `internal/notifier/` - 自动更新提示（读本地缓存零延迟判定写进程级 pending，过期或跨通道后台 goroutine 刷新后重写；两个消费者读同一份 pending：stderr 文本提示仅 TTY 且仅命令成功后置于末尾（对齐 gh）、`--output json` 顶层对象末尾追加 `_notice.update{current,latest,url,command,message}` 不问 TTY，对齐 lark-cli 让 agent 收到升级提示；三态开关 env MAKE_CLI_UPDATE_NOTIFIER > config [settings] > 默认开；按 [settings] channel 检查与提示，缓存带 channel 字段跨通道失效，beta.N 白名单拒 git-describe 伪版本）

- `docs/` - 按需求维护中文 AI 变更记录；运行契约和模块职责继续以现有分层文档为准

</directory>

<root>

- `main.go` - 程序入口，注入 cmd.SkillContentFS 后调用 cmd.Execute()
- `embed.go` - go:embed 白名单嵌入 skills/ submodule 的 skills/*/SKILL.md + references/（scripts/ agents/ 不嵌入）；go:embed 不能引用包目录之外的路径，submodule 在仓库根故嵌入点落在根 main 包；embed_test.go 验证真实 FS 及 LF/CRLF 的合法 frontmatter

</root>

<config>

- `go.mod` - 模块声明，module github.com/qfeius/makecli
- `Makefile` - 本地构建脚本（build/test/vet/clean），通过 ldflags 注入版本和日期；test 同时跑 Go 测试与 npm/ 的 node:test，与 CI 门禁一致
- `CHANGELOG.md` - 版本变更记录（Keep a Changelog 格式）；`update --check` 链接指向此文件；发版时由 /ship Step 5 从 git log 重生成并提交回 main
- `.goreleaser.yml` - 发布流水线：多平台构建 + 自动推送 Homebrew Tap
- `.github/workflows/release.yml` - 打 v* tag 时触发 GoReleaser 发布，随后同 job 内 `node npm/build.js` 生成包并逐个 `npm publish`，认证走 npm Trusted Publishing（OIDC，`id-token: write`，无长期 token；7 个包在 npmjs.com 各配 qfeius/makecli + release.yml 的 publisher）；beta tag → dist-tag beta，正式 → latest
- `.github/workflows/ci.yml` - push main / PR 时运行 golangci-lint + vet + test + `node --test "npm/*.test.js"`（PR 另跑 Claude 安全扫描）

</config>

## 发布流程
```
git tag v1.0.0 && git push --tags
→ GitHub Actions 触发 GoReleaser
→ 构建 linux/darwin/windows × amd64/arm64 二进制
→ 推送 formula 到 qfeius/homebrew-makecli
→ npm publish @qfeius/makecli + 6 个平台子包（Trusted Publishing OIDC，自动附 provenance）
```

## 常用命令
```bash
make sync           # 拉齐 skills submodule（build/test/vet/lint 自动前置）
make build          # 构建到 bin/makecli（自动注入版本和日期）
make test           # 运行全部测试
make vet            # 静态检查
make local          # 构建并安装到 ~/.local/bin
```

## 开发纪律（AI Agent 必读）
验证必须**门控**提交——血泪教训，曾三次推送编译失败的代码到 main。

1. **Edit 失败必核实**：返回 "String to replace not found" = 该编辑**未生效**，必须重读文件重做，绝不假设成功后继续。一次静默失败的 import/调用点替换就会让整包 build red。
2. **改 Go 代码后先验证再提交**：单独跑 `make vet && make test`（+ `golangci-lint run ./...`），**确认 exit 0** 才进入 `git commit` / `git push`。**禁止**在同一批工具调用里 test + commit + push——red 会来不及拦住提交。
3. **gofmt 不查编译**：`gofmt -l` 干净 ≠ 能编译。CI 门禁含 golangci-lint，本地须 0 issues 才 push（新测试常踩 gocritic：`stringXbytes` 改 `bytes.Equal`、手写线性查找改 `slices.Contains`）。
4. **沙箱假性失败**：`go vet/test/build` 在命令沙箱下因 module cache（`~/code/go/pkg/mod`）不可写而报 `operation not permitted`，非代码错误；Go 工具链命令直接禁用沙箱跑。
5. **跨 worktree 落地用 patch/逐段 Edit**，勿整文件 cp 覆盖——worktree 可能基于陈旧 commit，会静默回退他人近期工作。

## 安装方式
```bash
npm install -g @qfeius/makecli
# 或
brew tap qfeius/makecli
brew trust qfeius/makecli
brew install makecli
```
