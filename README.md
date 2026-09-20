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
默认会先更新 makecli 二进制，再每次同步 Make platform skills：
```
npx -y skills add qfeius/make-platform-skills --all -y
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

makecli 有两个发布通道：

- **stable**（默认）：正式版本，Homebrew 与 `makecli update` 默认追踪
- **beta**：`vX.Y.Z-beta.N` 预发布版本，先行体验新功能；不进 Homebrew，通过 `makecli update` 自更新或 `npm install -g @qfeius/makecli@beta` 分发

```bash
# 切入 beta 通道：之后裸 update 与后台更新提示自动追踪 beta
# （若出现更高的稳定版会自动收敛回稳定版）
makecli configure set channel beta
makecli update

# 查看当前通道
makecli configure get channel

# 切回稳定通道；稳定版低于手上的 beta 时降级需 --force，或等更高稳定版自然收敛
makecli configure set channel stable
makecli update v0.5.5 --force

# 不切通道，临时安装某个 beta 版本
makecli update v0.6.0-beta.1
```

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

后端 context（连哪套 Make 后端：dev / test / production）与 profile 正交：`--context` > `MAKE_CLI_CONTEXT` > `~/.make/config` 的 `[settings] context` > `production`。`makecli context list` 列出内置 context 并标当前，`makecli context use <name>` 持久切换，`makecli context show` 回显本次生效值。注意 context 不是 app 的部署环境——`app deploy --env beta|production` 里的 `--env` 指后者。旧版 `[settings] environment` 键不再识别：`makecli doctor` 只检查并标出可修项，`makecli doctor --fix` 原地迁移。

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

### 列出 App
```bash
# 终端里默认表格输出；被管道 / 脚本 / AI Agent 调用时自动切为 JSON
makecli app list

# 显式指定输出格式（不受终端环境影响）
makecli app list --output json
makecli app list --output table

# 分页查看第 2 页，每页 10 条
makecli app list --page 2 --size 10
```

`app list` 支持的输出格式：

- `--output auto`（默认）stdout 是终端则表格，否则（管道、CI、AI Agent 调用）JSON
- `--output table` 强制表格输出
- `--output json` 输出 `data` 和 `pagination`，便于脚本或 AI Agent 消费
- 列表模式支持 `--page` 和 `--size`，其中 `--page` 默认从 `1` 开始

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
