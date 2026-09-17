/**
 * [INPUT]: 依赖 cmd 包的 Execute/ExitCode/SkillContentFS、internal/build 的 Version/Date、embed.go 的 platformSkillsFS
 * [OUTPUT]: 可执行二进制 makecli
 * [POS]: 程序入口，将构建元数据与内嵌 skill 内容注入命令层
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package main

import (
	"os"

	"github.com/qfeius/makecli/cmd"
	"github.com/qfeius/makecli/internal/build"
)

func main() {
	cmd.SkillContentFS = platformSkillsFS()
	// 退出码语义收口在 cmd.ExitCode：0 成功 / 2 构建未成功 / 124 等待超时 / 其余 1
	if err := cmd.Execute(build.Version, build.Date); err != nil {
		os.Exit(cmd.ExitCode(err))
	}
}
