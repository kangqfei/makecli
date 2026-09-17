# internal/skillcontent/
> L2 | 父级: /CLAUDE.md

## 成员清单
- `reader.go`: 读取层——Read(fsys, "<skill>[/<path>]") 解析目标：path 缺省 SKILL.md、目标是目录则列一层子项（Result.Content / Entries 二选一）；未知 skill 报错附嵌入清单、路径未找到报错附 skill 顶层条目（错误自带导航，不设单独 list 语法）；逃逸由 fs.ValidPath 兜底；Names 列嵌入的 skill 名。纯函数作用于任意 fs.FS：生产用根包 embed.go 嵌入 skills/ submodule 后经 main.go 注入 cmd.SkillContentFS 的 FS，测试注入 fstest.MapFS
- `reader_test.go`: fstest.MapFS 覆盖主文件缺省/子文件/目录列举/未知 skill/未找到/逃逸拒绝（真实嵌入 FS 的冒烟在根包 embed_test.go）

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
