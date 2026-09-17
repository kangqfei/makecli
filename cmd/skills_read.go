/**
 * [INPUT]: 依赖 errors、fmt、io、io/fs、strings、github.com/spf13/cobra、internal/skillcontent
 * [OUTPUT]: 对外提供 newSkillsReadCmd 函数、SkillContentFS 导出变量（由 main.go 注入根包 embed 的 FS；测试注入 fstest.MapFS）
 * [POS]: cmd/skills 的 read 子命令，打印二进制内嵌的 skill 内容（SKILL.md / references 文件 / 目录列举），与 CLI 版本同步、离线可用；对齐 lark-cli skills read
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/qfeius/makecli/internal/skillcontent"
	"github.com/spf13/cobra"
)

// SkillContentFS 是内嵌 skill 内容的来源。go:embed 只能引用包目录之下的路径，而 skills/ submodule 在仓库根，
// 故由根 main 包嵌入后注入这里；测试注入 fstest.MapFS。nil 表示未注入（只会发生在绕过 main 的场景）。
var SkillContentFS fs.FS

func newSkillsReadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "read <skill>[/<path>] [path]",
		Short: "Print an embedded skill's SKILL.md, or a file under it",
		Long: "Print agent-readable skill content (SKILL.md and references/) embedded in this binary " +
			"at build time, so it always matches the CLI version and works offline. " +
			"A directory target lists its entries. Machine resources (scripts/) are not embedded.",
		Example: `  makecli skills read makeui                             # the skill's SKILL.md
  makecli skills read makeui references/principles.md    # a file under the skill
  makecli skills read makeui/references/principles.md    # same, slash form
  makecli skills read makeui references                  # list a directory`,
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillsRead(cmd.OutOrStdout(), cmd.ErrOrStderr(), strings.Join(args, "/"))
		},
	}
}

// runSkillsRead 把文件原文逐字节写 stdout（与源文件一致，便于 agent 直接消费）；
// 目录列举每行一项；主文件读毕在 stderr 附引用文件的读取指引（前置空行与正文分隔）。
func runSkillsRead(stdout, stderr io.Writer, target string) error {
	if SkillContentFS == nil {
		return errors.New("skill content is not embedded in this build")
	}
	res, err := skillcontent.Read(SkillContentFS, target)
	if err != nil {
		return err
	}
	if res.Entries != nil {
		_, err := fmt.Fprintln(stdout, strings.Join(res.Entries, "\n"))
		return err
	}
	if _, err := stdout.Write(res.Content); err != nil {
		return err
	}
	if res.IsMain() {
		_, _ = fmt.Fprintf(stderr, "\n> Tip: files this skill references (e.g. references/...) are embedded too: "+
			"`makecli skills read %s <relative-path>`; another skill's files: `makecli skills read <skill> <path>`.\n", res.Skill)
	}
	return nil
}
