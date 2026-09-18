# internal/daemon/
> L2 | 父级: ../../CLAUDE.md

外接 brain 的 runtime 接入(agent-design/Design.md §8.1):setup-key 自助入册换回 node key、node key 心跳续活、拉取式 claim 领工作、驱动本机 coding CLI 执行并回写事件流。正确性完全建立在拉取式 claim 上,连接断开只影响延迟。功能未稳定,入口命令(makecli daemon)隐藏。

成员清单
context.go: 按 Claim 固定 Execution/generation/租约读取 Context 窗口；文本 CLI 拒绝不支持的媒体，不回落旧会话事件
lease.go: 执行期间独立续租，取消或失去租约立即终止对应 CLI
execution_test.go: 真实 HTTP 的租约失效、取消、平台工具凭据拒绝及当前 Context 协议验收
protocol.go: daemon 协议 wire 类型(封闭六动词常量/信封/runtime 入册[setup-key→node key]/claim 的 AgentBundle 含 description 身份职责 + instructions 执行要求/生命周期/事件,Block 含 mention 的 Target 目标)——makecli 是公开仓库无法 import 私有 agent-contract,在此镜像线上形状,真相源 agent-design/Contract.md
client.go: gateway runtime 面 /api/make/agent/v1/{resource} 的类型化 HTTP client(入册免 Bearer[setup-key 在 body 自证],其余 Bearer node key + X-Make-Target + 信封解包;SetToken 入册后换 node key),APIError 还原类型化原因
daemon.go: 主循环——New 要求 setup-key/node key 严格互斥：前者首次入册且请求不带 Bearer，后者供已入册 runtime 正常续连；新 node key 经 NodeKey() 交 cmd 落盘，跨 uninstall 不保留身份→ 心跳 goroutine(15s runtime Update,消费 Run/Execution 取消指令)→ 按 provider 分别 claim 轮询(3s,RunClaim 不带 provider,单能力请求领到即知道用哪个 CLI)→ v1 串行执行
run.go: 单 run 执行编排——start → 授权窗口与独立续租 → 执行 → 事件攒批上报(batch_seq 单调,模糊重试不双写;中间文本映射 status,最终答复才是 message,且经 parseMentionBlocks 产出结构化 mention 块驱动互@)→ complete/fail(取消收尾优先)
mention.go: 出站 mention 解析——parseMentionBlocks 把 CLI 最终答复的 @Name 切成 text+mention 块序列(名字寻址,平台归一化为 agent_id;未命中渠道侧退化 @文本,故宁可多切不做本地名册校验)
execenv.go: 按租户/Execution/generation 隔离目录，渲染本次身份与指令，禁止远端指定宿主目录或复用其他执行的 CLI 会话
daemon_test.go / execenv_test.go / mention_test.go: 编排回归(httptest 假 gateway + 桩 backend)、执行环境回归与出站 mention 解析回归
adapter/: CLI 适配层,见其 CLAUDE.md
launchd/: macOS 托管层(把前台形态固化成 LaunchAgent,登录自启 + 退出拉起),见其 CLAUDE.md

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

当前原生设备适配器只消费已授权文本窗口；媒体或平台下发的业务 MCP 凭据未获得适配时明确拒绝，不能默默省略输入或借用宿主的其他业务账号。服务端托管 Runtime 的完整能力独立声明。
