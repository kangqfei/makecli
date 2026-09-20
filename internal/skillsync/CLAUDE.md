# internal/skillsync/
> L2 | 父级: /CLAUDE.md

## 成员清单
- `env.go`: 环境门禁层——EnsureNpx 确认 npx 可用，缺失时输出 How-to-fix 安装指引（brew / nodejs.org）；lookPathFunc 为测试接缝；Sync / Remove / PlanInstall 在 shell out 前统一调用
- `env_test.go`: 覆盖 EnsureNpx 存在/缺失路径；stubNpxPresent / stubNpxMissing 供 sync/remove/install 测试复用
- `sync.go`: 同步层——同步命令按 Options.Role 经 role.go SyncCommand 派生（空/未设置 → --all 原有行为，user → -s makecli 按名）；Skip 判断后前置 EnsureNpx 环境门禁（Skip 不要求 npx）；默认每次执行 `npx -y skills add qfeius/make-platform-skills --all -y --global`（--global 钉死全局 scope：上游在项目目录内默认落 project scope，会与全局 lockfile/清单状态分裂，install/remove 同款约束）；Options 控制 Version/Skip，Result 给 cmd/update.go 渲染用户可见输出；runSkillsCommand 包级 seam 隔离 npx 副作用并带 3 分钟超时（syncTimeout 与 trimOutput 被 remove.go 复用）；dedupSortedNames 去重排序按名清单，被 remove.go / install.go 按名路径共用（防重复名字对上游发两次同名请求）
- `sync_test.go`: 覆盖每次同步都执行命令、--skip-skills 跳过、命令失败时输出手动修复命令；白盒替换 runSkillsCommand，避免真实执行 npx
- `role.go`: 角色名单表——roleSkills（user → {makecli}；不在表里的角色含未设置的空串 = 全量）是"哪个角色装哪些 skills"的单一真相源；这是 CLI 的 UX 决策故放本仓库而非 skills 仓库；RoleSkills(role) 返回 (names, all)，空/未知角色一律全量（取值合法性由 cmd 层 settings 键表把关，此处不校验）；SyncCommand(role) = InstallCommand(RoleSkills(role))，Sync（update 后置同步）与 cmd/skills install --role 共用，保证 update 不会把 user 的选择冲回全量
- `role_test.go`: 覆盖 RoleSkills 三态（user 按名 / 空与未知回退全量）+ 每个合法角色在表里有显式行（漏行会静默退成全量）+ SyncCommand 按角色派生（未设置 = SkillsCommand，user = 按名 -s makecli）
- `inventory.go`: 清单层——本地半边（lockfile 读取过滤 Make platform skills + SKILL.md 描述解析：readLock/readDescription/extractFrontmatter）+ 远端半边（GitHub Contents API 拉 skill 目录 tree SHA：fetchRemoteSkills）+ List 合并两者产出排序后的 Inventory{Skills []SkillInfo, LockWarning, RemoteErr}；Status* 常量描述本地×远端比对结果；lockPathFunc/skillsDirFunc/inventoryAPIBaseURL 为测试接缝
- `inventory_test.go`: 覆盖 readLock（缺失/过滤/损坏/版本不匹配）、extractFrontmatter/readDescription、fetchRemoteSkills（正常/HTTP 错误）、List（状态合并/远端下架/远端不可达降级 unknown/已装条目补 description）；stubLockFile/stubSkillsDir/stubRemoteAPI 隔离文件系统与网络
- `remove.go`: 删除层——两阶段：PlanRemove（EnsureNpx 门禁 → readLock 校验按名 / 展开 --all，--all 从 lockfile 展开为按名清单绝不透传上游 --all，空清单报错，lockfile 警告进 RemovePlan.Warning）；按名路径经 dedupSortedNames 去重排序；plan.Names 落定后统一拒绝 flag 形状名字（`-` 前缀，防投毒 lockfile 混入 "--all" 被透传上游）；Remove 逐个执行（RemoveCommand 单 skill 命令带 --global 钉死全局 scope、每次独立 syncTimeout、失败不中断、汇总 failed to uninstall N of M）返回 []RemoveResult；被 cmd/skills_uninstall.go 消费；复用 readLock / runSkillsCommand / syncTimeout / trimOutput / dedupSortedNames
- `remove_test.go`: 覆盖 RemoveCommand 单数构造、PlanRemove（按名校验/第三方拒绝/未安装拒绝/空 lockfile/--all 展开排序/--all 空报错/损坏 lockfile 警告/缺 npx 门禁/按名去重排序/flag 形状名拒绝）、Remove（逐个调用/部分失败继续并汇总）；stubNpxPresent + stubLockFile + stubRunSkillsCommand 隔离
- `install.go`: 安装层——两阶段：PlanInstall（EnsureNpx 门禁 → fetchRemoteSkills 校验按名/展开 --all → InstallCommand 构造命令）产出 InstallPlan 供 cmd 层确认展示；Install 执行计划；按名拼错报错列可用名字，远端不可达降级 Warning 放行（按名清单仍经 dedupSortedNames）；--all 复用 SkillsCommand（与 update 同一命令），按名走上游 -s 多值 + -a '*' + --global（钉死全局 scope）；plan.Names 落定后统一拒绝 flag 形状名字，最终 Command 由去重排序后的 plan.Names 构造（非原始 names）
- `install_test.go`: 覆盖 InstallCommand 构造（按名/--all）、PlanInstall（校验通过/拼错列候选/远端不可达降级/--all 展开/--all 降级/缺 npx 不触网/按名去重排序且 Command 随 plan.Names 构造）、Install（执行计划命令/失败含 manual fix）；stubNpxPresent + stubRemoteAPI + stubRunSkillsCommand 隔离

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
