## 2026-11-04：Agent 插件绑定 secretFields 与 update
- 摘要：控制面客户端对齐插件绑定合同。详情读取 `secretFields.{configured,maskedValue}`；`agent update --plugins-json` 经 UpdateResource 整表替换绑定，已有项带 id，省略 writeOnly 密钥由服务端保留。
- 涉及文件：internal/api/agent.go、cmd/agent_update.go、cmd/agent_info.go、对应测试、README 与模块地图。
- 决策：创建输入继续拒绝 id；更新请求只发送 key/appKey/plugins，不改名称、提示词、模型或状态。不增加 PluginContent、发布或 session/run/event API，也不改 deploy/overview 词汇。
- 验证：`gofmt`、`go vet ./...`、`golangci-lint run ./...`、`go test ./... -count=1` 与 `node --test npm/*.test.js`。

## 2026-10-26：TC-013 初始化期间构建日志漏读回归
- 摘要：先保存构建起点，再扫描部署历史，防止期间新增构建失败被 tail=0 跳过。
- 涉及文件：logs_stages、logs_follow_start_test、帮助、README及模块地图；详见 ai-changes-app-build-logs.md。
- 决策：保持各流独立初始快照和既有部署历史过滤语义，后续按游标读取；不依赖任务阶段猜测历史。
- 验证：两例先红后绿，专项竞态20轮及全量竞态、vet/build、npm、lint通过；独立复审通过。

## 2026-10-26：TC-013 阶段跟随游标初始化
- 摘要：all + tail=0 + follow 在初始化阶段建立部署读取起点，阶段切换后不再过滤新事件。
- 涉及模块：logs_stages、HTTP 回归测试、命令帮助及文档；详细记录见 ai-changes-app-build-logs.md。
- 决策：复用现有分页与错误检查，只调整零历史组合跟随的初始化时机，兼容游标续读及其他模式。
- 验证：TDD、全量竞态、vet/build、npm、lint 与复审通过；真实任务 505 确认修复版完整展示原版遗漏的部署失败内容。

# AI 变更记录
