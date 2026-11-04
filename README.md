## 说明
makecli 是 make 平台管理的命令行工具

## 安装
npm（macOS / Linux / Windows，x64 与 arm64；安装时不下载，二进制随平台子包一起装入）：
```bash
npm install -g @qfeius/makecli
```
Homebrew：
```bash
brew tap qfeius/makecli
brew install makecli
```
## 强制升级到最新版本
```
makecli update
```
默认会先更新 makecli 二进制，再按 `[settings] channel` 每次同步 Make platform skills；stable 的底层命令为：
```bash
npx -y skills add https://github.com/qfeius/make-platform-skills/tree/main --all -y --global
```
如需只更新二进制，可跳过 skills：
```
makecli update --skip-skills
```
或者使用下面命令
```
git -C $(brew --repo qfeius/makecli) pull && brew upgrade makecli
```
npm 安装的 makecli 执行 `makecli update` 时会自动委托给 `npm install -g @qfeius/makecli@<版本>`（pnpm 安装则委托 `pnpm add -g`），不会绕过包管理器原地替换二进制。

## 发布通道（stable / beta）

makecli 二进制与已安装的 Make platform skills 共用两个发布通道：

- **stable**（默认）：CLI 正式版本，Homebrew 与 `makecli update` 默认追踪；Skill 使用 `main` 分支
- **beta**：CLI 额外跟踪 `vX.Y.Z-beta.N` 预发布版本，不进 Homebrew，通过 `makecli update` 或 `npm install -g @qfeius/makecli@beta` 分发；Skill 使用 `beta` 分支

```bash
# 切入 beta 通道：之后裸 update 与后台更新提示自动追踪 beta
# （若出现更高的稳定版会自动收敛回稳定版）
makecli settings set channel beta
makecli update

# 查看当前通道
makecli settings get channel

# 切回稳定通道；稳定版低于手上的 beta 时降级需 --force，或等更高稳定版自然收敛
makecli settings set channel stable
makecli update v0.5.5 --force

# 不切通道，临时安装某个 beta 版本
makecli update v0.6.0-beta.1
```

只切换和更新 Skill，无需替换 CLI 二进制：

```bash
makecli settings set channel beta
makecli skills update

makecli settings set channel stable
makecli skills update
```

`settings set` 只保存通道；`skills install`、`skills update` 和 CLI 更新后的自动同步才覆盖已有同名 Skill，角色设置仍决定同步范围。`skills list` 按同一分支检查更新，顶部展示目标通道和分支；每行 `CHANNEL` 列展示该 Skill 实际安装时记录的通道。JSON 顶层 `channel`、`branch` 是当前目标，`data[].channel`、`data[].ref` 是安装来源。

安装记录中的 `ref=main` 显示 `stable`，`ref=beta` 显示 `beta`；旧记录没有 ref 或使用其他分支/tag 时显示 `unknown`，未安装条目显示 `-`。切换配置不会改变已安装来源；使用会记录 ref 的 `skills` 安装器重新执行 `makecli skills update` 后，列表才能据此显示通道。

切回 stable 也会覆盖安装，不按 Skill 版本号阻止切换；目标分支缺失的已装 Skill 保留，可显式 `skills uninstall`，第三方 Skill 不受影响。

显式安装某个 CLI 版本不修改通道配置，其后的 Skill 同步仍读取 `[settings] channel`。`skills read` 是二进制内嵌的构建快照，使用切换后的全局 Skill 时应让 Agent 读取已安装文件。

`makecli version list` 的 TYPE 列会标注 `Pre-release`，便于分辨预发布版本。

## 功能

### 配置凭证
```bash
# 配置默认 profile 的 access token
makecli configure

# 等同于
makecli configure token

# 配置指定 profile
makecli configure token --profile todo
```

交互示例：
```
Configuring profile [default]
MakeCLI Access Token [****YDUW]:
Credentials saved to ~/.make/credentials
```

凭证保存在 `~/.make/credentials`，格式：
```ini
[default]
access_token = AKIAUXFQEUPWGEXEYDUW

[todo]
access_token = AKIAUXFQEUPWGEXEYDUW
```

### 配置请求参数
```bash
# 交互式配置 X-Tenant-ID 和 X-Operator-ID
makecli configure config

# 配置指定 profile
makecli configure config --profile todo

# 单条设置
makecli configure set X-Tenant-ID tenant_abc
makecli configure set X-Operator-ID op_123

# 单条读取
makecli configure get X-Tenant-ID
```

配置保存在 `~/.make/config`，格式：
```ini
[default]
X-Tenant-ID = tenant_abc
X-Operator-ID = op_123
```

- `X-Tenant-ID` 和 `X-Operator-ID` 会自动作为 HTTP Header 附加到所有 API 请求中

### Access Token 优先级

token 按以下顺序取值，命中即止：

1. `--access-token` / `-t` 命令行参数
2. `MAKE_ACCESS_TOKEN` 环境变量
3. `~/.make/credentials` 中当前 profile 的 `access_token`

服务器地址同构：`--meta-server-url` > `MAKE_META_SERVER_URL` > `~/.make/config` 的 `meta-server-url`，都没配则用当前 context 的内置地址；代码仓库服务无 flag 级，`MAKE_REPO_SERVER_URL` > `repo-server-url` > 内置地址。

后端 context（连哪套 Make 后端：dev / test / production）支持在 profile 中配置默认值：`--context` > `MAKE_CLI_CONTEXT` > 当前 profile 的 `context` > `~/.make/config` 的 `[settings] context` > `production`。`makecli context list` 列出内置 context 并标当前，`makecli context use <name>` 修改全局默认，`makecli context show` 回显本次生效值。`app deploy` 只推 beta，`app promote` 把 beta 发布到 production；`app delete --appRole` 选择删除 beta、prod 或依次删除两者，并等待最终结果。旧版 `[settings] environment` 键不再识别：`makecli doctor` 只检查并标出可修项，`makecli doctor --fix` 原地迁移。

```bash
# CI / 脚本：用环境变量，不落盘、不进 shell history
export MAKE_ACCESS_TOKEN=xxx
makecli app list

# 临时指定一次
makecli -t xxx app list
```

覆盖只替换 token 本身，`X-Tenant-ID`、`X-Operator-ID` 与服务器地址仍取自 `--profile` 对应的配置。`makecli configure verify --output json` 的 `source` 字段（`flag` / `env` / `credentials`）可查看当前生效的 token 来源。

### 全局参数

所有 API 命令均支持以下全局参数：

```bash
# 指定 Meta Server 主机地址（覆盖 $MAKE_META_SERVER_URL 与 config 中的 meta-server-url，/api/make 自动补齐）
makecli app list --meta-server-url https://make.qfei.cn

# 指定 profile
makecli app list --profile todo

# 指定 access token（覆盖环境变量与 credentials 文件）
makecli app list --access-token xxx
```

### 查看版本
```bash
makecli version
```

### 本地运行 Agent

```bash
makecli agent run
makecli agent run -p "Review this project"
makecli agent run --chat-only -p "Hello"
makecli agent run --approve -p "Implement this change"
```

`agent` 无子命令时显示帮助；`run` 为隐藏子命令，默认帮助不显示，可显式调用或查看 `agent run --help`。本地运行参数 `--token`、`--gateway-server-url`、`--model`、`--prompt/-p`、`--system`、`--approve`、`--chat-only` 仅属于 `run`；token 默认读取 `MAKE_AGENT_TOKEN`，gateway 默认使用 `MAKE_AGENT_SERVER_URL` 或当前 context。默认使用编程工具，`--chat-only` 使用纯聊天；不传 `-p` 进入交互模式。

### 查看 Agent 插件目录

```bash
makecli agent plugin list
makecli agent plugin list --page 2 --size 20 --output json
```

不要求 `--app`，默认查询可用插件，分页默认 1/20（size 上限 100）。表格展示 KEY / NAME / DESCRIPTION / STATUS / CAPABILITIES；JSON 保留完整 `configSchema`，插件 key 用于 Agent 的 `plugins[].pluginKey`。

### 查看 Agent 模型目录

```bash
makecli agent model list
makecli agent model list --page 2 --size 20 --output json
```

不要求 `--app`，沿用全局凭证与 context。分页默认 1/20，size 上限 100；表格显示 KEY / NAME / PROVIDER / MODEL / MULTIMODAL，JSON 保留完整模型字段与分页；模型 key 用于 `agent create --model`。

### 查看 Agent 运行服务目录

```bash
makecli agent runtime list
makecli agent runtime list --page 2 --size 20 --output json
```

不要求 `--app`，沿用全局凭证与 context。`--page` 默认 1、`--size` 默认 20（1–100）；auto 在终端显示 KEY / NAME / SCOPE / ONLINE 表格，在管道中输出完整目录字段与分页 JSON。ONLINE 表示当前在线状态。

### 创建 Agent

```bash
makecli agent create expense_agent --app expense_app --model deepseek --name "报销助手"
makecli agent create expense_agent --app expense_app --model deepseek \
  --prompt-file prompt.txt --plugins-json @plugins.json --output json
```

只创建远端配置，不生成本地工程。`--app` 必填，原样传入实际环境 App 的 `appKey`；beta 和 production 使用各自的 App key。`runtimeKey` 固定为 `make-runtime-service`，不提供覆盖参数。
`--model` 必填；`--name` 默认等于 agentKey；`--status` 默认 `disabled`，可显式传 `enabled`。
`--prompt` 与 `--prompt-file` 互斥；`--plugins-json` 接受内联 JSON 数组、`@文件` 或 `-`（标准输入），插件项格式为 `{"pluginKey":"mcp_make","enabled":true,"config":{}}`。
凭证和后端沿用全局 `--profile` / `--access-token` / `--context` / `--meta-server-url`；输出默认 auto，终端显示创建结果，管道或 Agent 调用时输出保存后的 Agent JSON。

### 列出 Agent

```bash
makecli agent list --app expense_app
makecli agent list --app expense_app --output table
makecli agent list --app expense_app --page 2 --size 20 --output json
```

`--app` 必填，使用实际环境 App 的 key；`--page` 默认 1，`--size` 默认 20，范围 1–100。
输出沿用 `app list`：auto 在终端显示表格，在管道中输出 JSON；表格列为 KEY / NAME / DESCRIPTION / MODEL / STATUS / CREATED AT，并显示当前条数与总数。
JSON 包含 `data` 概要数组和 `pagination.count/page/size/total`，不展开 `prompt` 或 `plugins`；空列表返回 `data: []`。

### 查看 Agent 详情

```bash
makecli agent --app agent_poc info poc_agent
makecli agent info poc_agent --app agent_poc --output json
```

`--app` 必填，使用实际环境 App key；文本展示基本信息、提示词和插件配置。插件表增加 SECRETS 列，按 JSON Pointer 展示 `configured` / `unconfigured` 与 `maskedValue` 摘要，不回显明文。JSON 返回完整 Agent 配置，包含只读 `plugins[].secretFields`。

### 更新 Agent 插件

```bash
makecli agent update expense_agent --app expense_app \
  --plugins-json '[{"id":"binding_01","pluginKey":"mcp_make","config":{"mode":"read"}}]'
makecli agent update expense_agent --app expense_app --plugins-json @plugins.json --output json
```

`--app` 与 `--plugins-json` 必填。`plugins` 数组会整表替换：已有绑定必须带上 `agent info` 返回的 `id`，省略 `config` 里的 writeOnly 密钥即保留原值；新增绑定不要带 `id`，并提供必填密钥。空数组 `[]` 会移除全部绑定。名称、提示词、模型和状态不在这条命令里修改。创建命令的 `--plugins-json` 仍不接受 `id`。

### 启用或停用 Agent

```bash
makecli agent enable poc_agent --app agent_poc
makecli agent disable poc_agent --app agent_poc
```

`--app` 必填，仅更新 `status` 为 `enabled` / `disabled`；名称、提示词、模型和插件配置保持。支持 `--output auto|table|json`，JSON 返回保存后的完整 Agent。

### 删除 Agent

```bash
makecli agent delete invoice_agent --app invoice_app
makecli agent delete invoice_agent --app invoice_app --yes --output json
```

`--app` 必填，使用实际环境 App 的 key。软删除 Agent，保留历史记录。与 app delete 一致，默认要求输入 Agent key 确认，--yes/-y 跳过；非交互环境必须加 --yes。输出默认 auto，终端显示删除结果，管道输出 `key`、`appKey` 和 `deleted: true`。

### 列出 App
```bash
# 终端里默认表格输出；被管道 / 脚本 / AI Agent 调用时自动切为 JSON
makecli app list

# 显式指定输出格式（不受终端环境影响）
makecli app list --output json
makecli app list --output table

# 分页查看第 2 页，每页 10 条
makecli app list --page 2 --size 10

# 列出当前用户收藏的 App
makecli app +favo

# 与快捷命令等价的显式筛选
makecli app list --filter "workbenchGroup=favorite"

# 收藏列表沿用分页和输出格式
makecli app +favo --page 2 --size 10 --output json

# 列出当前用户创建的 App
makecli app +mine

# 与 +mine 等价的显式筛选
makecli app list --filter "workbenchGroup=created"

# 我的应用沿用分页和输出格式
makecli app +mine --page 2 --size 10 --output json
```

`app list` 支持的输出格式：

- `--output auto`（默认）stdout 是终端则表格，否则（管道、CI、AI Agent 调用）JSON
- `--output table` 强制表格输出
- `--output json` 输出 `data` 和 `pagination`，便于脚本或 AI Agent 消费
- 列表模式支持 `--page` 和 `--size`，其中 `--page` 默认从 `1` 开始

`app +favo` 是只读查询快捷命令，复用 `app list` 的过滤、分页和输出；收藏范围固定，不接受 `--filter` 覆盖。

`app +mine` 列出当前用户创建的应用，固定使用 `workbenchGroup=created`；分页和输出与 `app list` 一致，不接受 `--filter` 覆盖。

### 列出 Entity
```bash
# 列出指定 app 下的全部 entity（终端默认表格，管道/Agent 下默认 JSON）
makecli entity --app TODO list

# 以 JSON 输出 entity 列表
makecli entity --app TODO list --output json

# 分页查看第 2 页，每页 10 条
makecli entity --app TODO list --page 2 --size 10

# 查看单个 entity 详情
makecli entity --app TODO list Task

# 以 JSON 输出单个 entity 详情
makecli entity --app TODO list Task --output json
```

`entity list` 支持的输出格式：

- `--output auto`（默认）终端为表格/详情文本，管道/Agent 下为 JSON
- `--output table` 强制表格/详情文本输出
- `--output json` 列表模式输出 `data` 和 `pagination`，详情模式输出单个 `data` 对象
- 列表模式支持 `--page` 和 `--size`，其中 `--page` 默认从 `1` 开始

### 应用变量与密钥

默认从项目的 `apps/dsl/app.yaml` 读取应用身份；指定 `--app <appKey>` 时直接使用该 key，可在项目外执行。变量与密钥的 add/put/list/delete 均支持：

```bash
makecli app variable add BASE_URL https://api.example.com
makecli app variable put BASE_URL https://api-v2.example.com
makecli app variable put LOG_LEVEL debug --appRole prod --output json
makecli app variable list
makecli app variable list --app todo_minimal
makecli app variable list --appRole prod --page 1 --size 100 --output json
makecli app variable delete BASE_URL
makecli app variable delete BASE_URL --appRole prod --yes
makecli app secret add OPENAI_API_KEY
# Enter a secret value: ************
makecli app secret put OPENAI_API_KEY
makecli app secret list --output json
makecli app secret delete OPENAI_API_KEY
makecli app secret delete OPENAI_API_KEY --appRole prod --yes

makecli app secret add OPENAI_API_KEY --appRole prod
printf '%s' "$OPENAI_API_KEY" | makecli app secret add OPENAI_API_KEY --from-stdin
```

固定写入 runtime，`--appRole beta|prod` 默认 `beta`，角色原样传给 API；后端 context 仍使用 `--context production`。同名同类型配置更新，拒绝跨类型覆盖。保存后在下一次部署生效。密钥输入显示掩码，交互提示写入 stderr，支持 `--output auto|table|json`；debug 隐藏密钥写入的请求及响应正文，列表请求正常展示筛选与分页，响应仅移除密钥值。`--from-stdin` 允许空值、保留空格和内部换行，仅去除一个末尾 LF 或 CRLF，值最多 32 KiB。

`variable put <KEY> <VALUE>` 只更新已存在的普通变量，保留原有说明，支持空字符串；变量不存在、读取失败或类型为密钥时返回错误，不创建变量。更新在下一次部署生效。

`secret put <KEY>` 只更新已存在的密钥，保留说明；输入方式与 `secret add` 一致，支持掩码输入和 `--from-stdin`，不接受值参数。`secret list` 仅列出密钥元数据，支持 `--page`、`--size` 和 `--output`，表格值显示 `(encrypted)`、JSON 省略 value。`secret delete` 只删除密钥，输入 key 确认或使用 `--yes/-y`；非交互环境必须显式使用 `--yes`。更新与删除均在下次部署生效，不改写 app.yaml 的 requiredSecrets 声明。

`variable list` 固定查询 runtime，列出普通变量及密钥元数据，密钥值显示 `(encrypted)`；`--page` 默认 1，`--size` 默认 20（1–100），JSON 包含 `data` 与 `pagination`。`variable delete` 仅删除普通变量，默认输入 key 确认，`--yes/-y` 跳过；非交互环境必须显式使用 `--yes`。删除不会触发部署或改写 app.yaml；若变量仍在 `properties.vars` 中，后续 apply 会重新创建它。

### 构建与部署日志

```bash
makecli app deploy
makecli logs deploy

# 部署阶段：实例创建或更新、启动、健康检查
makecli logs deploy --stage deploy

# 构建和部署两个阶段，持续跟随
makecli logs deploy --stage all -f

# 查看生产环境同一提交的日志
makecli logs deploy --appRole prod --commit-sha <40位SHA> --stage all

# 最后 100 行并继续跟随；--timeout 仅用于 --follow，默认 3m
makecli logs deploy --tail 100 -f --timeout 2m

# 在项目外指定应用和完整提交 SHA
makecli logs deploy --app <appKey> --commit-sha <40位SHA> --output json

# 跳过已有日志，只读取之后新增的内容
makecli logs deploy --tail 0 -f

# 读取全部历史，或使用此前返回的游标续读
makecli logs deploy --tail -1
makecli logs deploy --commit-sha <40位SHA> --cursor <nextCursor>
```

`logs deploy` 默认从 `apps/dsl/app.yaml` 读取应用，从 Git HEAD 读取提交 SHA。`--appRole beta|prod` 默认 `beta`，选择应用角色对应的环境，按 SHA 查询并固定任务；使用配对应用的 key 时会定位所选角色的应用。`--stage build`（默认）查看安装依赖、编译、构建及推送镜像的日志；`--stage deploy` 查看实例创建或更新、启动、健康检查的部署事件；`--stage all` 按构建、部署顺序输出。日志读取完成不代表发布成功。

`--tail N` 限制每个阶段首次读取的历史行数，默认 200；`0` 跳过该阶段已有日志，`-1` 读取全部。构建日志由服务端截取，部署事件由 CLI 分页读取后截取。`all` 分别保留两个阶段的最后 N 行。`--stage all --tail 0 --follow` 先保存构建日志起始游标，再扫描部署历史并保存部署游标；扫描期间新增的构建日志按已保存游标续读。两个日志流各有独立的初始快照，不是同一时刻的原子快照。先输出新增构建日志，构建完成后从已保存的部署游标继续读取，不再次跳过部署内容；部署起点确定后新增的事件，包括最终失败事件，都会按游标读取。显式 `--tail` 与 `--cursor` 互斥；指定游标时不应用默认行数限制。构建日志服务端需支持 `tailLines`，旧服务可使用 `--tail=-1` 读取。

`--cursor` 使用同一阶段上次返回的 `nextCursor`：`build` 为字节偏移，`deploy` 为事件序号，`all` 为包含两个阶段位置的组合游标。组合游标绑定构建任务，不能用于另一任务。构建失败或取消且未进入部署时，部署阶段返回空日志并结束跟随。

`--follow` / `-f` 等待任务创建并持续输出正文，`--timeout` 支持 `3m`、`180s` 等时长，总超时默认 3 分钟，覆盖任务查询、请求、轮询和进程输出。stdout 或 stderr 管道阻塞时仍会退出；stderr 堵塞时，最后的超时或中断提示可能无法输出。超时仅停止本地读取，返回 124；Ctrl+C 返回 130；读取成功返回 0，参数或接口错误返回 1。

单次读取的 `--output auto` 在终端输出正文，在管道或 Agent 下输出 JSON（`appKey`、`appRole`、`commitSha`、`stage`、`content`、`nextCursor`、`completed`）；`appRole` 为本次选择的 `beta` 或 `prod`。`--output table` 可强制正文。跟随模式不支持 JSON，诊断信息写入 stderr。后端与凭证沿用 `--context` / `--profile`。

## 开发指南
### 编译
```
make
```

### 单元测试
```
make test
```

### 安装本地
```
make local
```

为 profile 配置默认后端：`makecli --profile wangLeiTest configure set context test`。读取使用 `configure get context`，清除使用 `configure set context ""`。仅未配置时向下回退，当前优先级的非法值直接报错。
