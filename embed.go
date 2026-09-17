/**
 * [INPUT]: 依赖标准库 embed 包、io/fs；引用仓库根 git submodule skills/（上游 qfeius/make-platform-skills，超项目钉住 commit）
 * [OUTPUT]: 包内提供 platformSkillsFS（fs.FS，以 skill 名为根：makeui/SKILL.md、makeui/references/x.md）
 * [POS]: 根包的 embed 文件——go:embed 只能引用包目录之下的路径，submodule 在仓库根，故嵌入点也落在根 main 包，
 *        再由 main.go 注入 cmd.SkillContentFS；把上游 skills 的 agent 可读内容（SKILL.md + references/）编译进二进制，scripts/ agents/ 是机器资源不嵌入
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package main

import (
	"embed"
	"io/fs"
)

// 白名单式嵌入：新的内容类型须显式加入 pattern 才会随二进制发布。
// submodule 未初始化时这里编译失败（pattern 无匹配），这是有意的——强制先 `make sync`。
//
//go:embed skills/skills/*/SKILL.md skills/skills/*/references
var platformSkills embed.FS

// platformSkillsFS 以 skill 名为根，去掉 submodule 的路径前缀
func platformSkillsFS() fs.FS {
	sub, err := fs.Sub(platformSkills, "skills/skills")
	if err != nil {
		panic("embed: " + err.Error())
	}
	return sub
}
