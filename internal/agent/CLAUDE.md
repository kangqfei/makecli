# internal/agent/
> L2 | 父级: /CLAUDE.md

keyless 本地 agent（隐藏命令 `makecli agent` 的执行层，agent-design/docs/execution.md §8.2）：标准 OpenAI 兼容客户端指向 gateway `/v1/chat/completions`，模型名用平台别名，配额与五维计量在模型面自动生效——设备端零厂商 key。默认即 code agent：gateway Provider（llm/gateway.go）+ 七工具注册表（root=cwd）+ 目录信任确认钩子 + 两层循环（code.go 编排、render.go 行式渲染）；`--chat-only` 退回 v1 纯聊天（client.go/repl.go）。两条 REPL 都支持 `!<cmd>` 本地命令直通（bang.go，不发起 LLM 请求）。内核五子包自 github.com/smallnest/pigo（MIT）移植。

## 子包
- `core/`: agent 内核叶子类型（Content/Message/AgentEvent 密封接口族、EventStream 泛型流、AgentTool 契约、hooks），纯 stdlib，其余子包的公共地基
- `tool/`: 内建工具（read/write/edit/grep/find/ls/bash）+ 注册表（JSON Schema 校验）+ 三段式/批量执行器；失败一律编码为 IsError 工具结果
- `llm/`: LLM 流式抽象纯类型（StreamFn/LlmContext/StreamConfig/事件集/Provider）+ GatewayProvider（OpenAI 兼容 SSE 指向 gateway 的唯一具体 Provider）
- `loop/`: 两层 agent 循环（流式响应 → 工具批执行 → 回喂；六钩子）+ 系统提示词组装（AGENTS.md 目录链）；compaction/subagent 已裁剪
- `trust/`: 按目录的信任决策持久化（~/.make/agent-trust.json），副作用工具授权依据

## 成员清单
- `client.go`: Client（ChatStream：SSE 流式补全，增量回调 + 全文返回）、Message、NewSessionID（X-Session-ID 计量维度，无安全语义）、APIError（OpenAI 风格错误体还原，解不出保底状态码+原文摘要）
- `client_test.go`: 覆盖 ChatStream 的单元测试（SSE 增量解析与 headers / OpenAI 错误体还原 / session id 随机性），httptest 隔离网络
- `repl.go`: RunOnce（单条 prompt 流式打印）与 RunREPL（读一行发一轮，历史进程内存续；/exit /quit 退出、/clear 清空历史、`!<cmd>` 本地直通[bang.go，dir 取进程 cwd]；单轮失败回滚本轮 user 消息不退出循环）——纯聊天路径（--chat-only）
- `repl_test.go`: 覆盖会话编排的单元测试（RunOnce 流式输出与 system 携带 / REPL 多轮历史携带 / /clear 清空），假 gateway 回显 messages
- `bang.go`: 本地命令直通（对齐 Claude Code 的 `!cmd`，两条 REPL 共用）——bangCommand 前缀解析（`!ls`/`! ls` 同义，裸 `!` 打 bangHint）、runBangCommand（复用 tool.BashTool 拿超时/退出码/取消语义，onUpdate 全量快照按水位差增量直写[stdout/stderr 合流可能并发触发故水位自带互斥]，非零退出只补一行 ✗ 首行状态不重复已流式打过的输出）、bangHistoryEntry（命令+输出折成英文框架句的转录喂回历史，超 16KiB 截头标注，空输出占位）；**刻意不过目录信任门控**——门控防的是模型自作主张调副作用工具，直通命令是用户在提示符下亲手敲的
- `bang_test.go`: 覆盖本地直通（前缀解析表 / code REPL 执行本机命令且该行零 LLM 请求 + 转录进历史被下一轮携带 / 未信任目录不弹确认 / 非零退出 ✗ 状态行且不重复输出 / 裸 `!` 只打提示不进历史 / --chat-only 路径同款 / 转录截断与空输出占位），复用两个假 gateway
- `code.go`: CodeOptions、RunCodeOnce（-p 无头：一轮循环到自然收尾，终结失败返 error 转退出码 1；未信任目录副作用工具直接拦截并提示 --approve）、RunCodeREPL（读一行发一轮共用历史；/exit /quit /clear 语义同纯聊天；确认与主循环共用同一 bufio.Reader）、handleInput（一行输入的分发：`!cmd` 走 bang.go 本地直通[dir=会话根，不发 LLM 请求]，其余作 prompt 驱动一轮；单轮失败打印 error 不中断）、appendUserText（历史追加 user 文本，直通转录与 prompt 共用）、newCodeSession（BuildSystemPrompt[root=cwd AGENTS.md 链] + 七工具注册[root=cwd] + trust.Manager[坏存储降级会话信任] + GatewayProvider 接线；EventBuffer 必须保持 0——确认钩子在 producer goroutine 读写终端）、trustBeforeToolCall（bash/write/edit 门控：受信放行；y=一次 / n=拒绝 / a=会话信任；拦截语作 IsError 工具结果回喂）、confirmToolCall
- `render.go`: runRenderer（行式渲染：assistant 文本按 partial 水位差增量直写、⚙ 工具行、结果截断预览[首 3 行/200 字符，IsError 红 ✗ 前缀]、turn 边界空行、TurnEnd 采集终结失败进 runErr 不打印）、supportsColor（out 是否终端，管道/测试 buffer 保持纯文本）与 errorMark（✗ 前缀，工具结果与本地直通共用）、toolCallSummary（bash→command/文件工具→path/搜索→pattern，确认提示复用）、previewLines/truncateOneLine
- `code_test.go`: 覆盖 code agent 编排（无头文本轮含 system/AGENTS.md/7 工具声明线缆、read 工具回喂轮、未信任 bash 拦截 + --approve 放行、REPL 确认 y/n/a 三态与提示次数、/clear 清史与历史接续、网关 401 转终结失败），脚本化 SSE 假 gateway + MAKE_CLI_CONFIG_DIR 隔离信任存储

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
