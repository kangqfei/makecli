# daemon Context Execution 接入

## 2026-09-18 评审问题修复

- 在现有 feature/context-execution 分支合入团队最新 main，继续当前 Execution 接入，不增加旧内部协议兼容路径。
- 续租失败或执行取消后，即使 CLI 已返回成功 Result 也不得确认 completed；成功回执保留执行 context，最终 message 必须取得事件持久化确认。失败/取消回执仍允许在本地取消后清理状态，服务端继续校验租约。
- Context 镜像保留 toolUse/toolResult、callID、工具参数、输出与 isError。CLI 以有角色和顺序的历史事实呈现，保留 outcome_unknown，不根据历史内容重放工具。混合或缺身份的工具块明确失败。
- 从有效 Claim 派生不可变的租户 client，start/renew/append/complete/fail/window 均携本执行租户；节点 client 和连接池复用不修改租户状态，拒绝将已限定 client 改为另一租户。
- 补充真实 ClaudeCode adapter 配合合成 CLI 子进程的竞态回归、事件回执拒绝/缺失/重复、空结果、截止时间，以及 shared 生命周期和双租户并发回归。
- 修改只涉及 daemon、对应测试与分层文档；未添加私有模块依赖，未修改服务端数据库或配置，也未发布 CLI 二进制/npm。

验证：Windows daemon/adapter/launchd 测试通过；Linux 全量 Go 与 npm 测试、vet、daemon race、golangci-lint 和构建通过。隔离 PostgreSQL 上使用当前 Context/Gateway 与修复后的 daemon 完成 shared 节点的 start、窗口读取、3 次续租、事件追加和完成回执，回读 completed 与 1 条最终消息。CLI/模型输出使用受控 fixture，不代表真实模型或线上部署验收。
