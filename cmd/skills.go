/**
 * [INPUT]: 依赖 fmt、strings、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newSkillsCmd 函数；包内 skillsDoneLine 被 install / uninstall 复用
 * [POS]: cmd 模块的 skills 命令组，挂载 list / install / update / uninstall / read 子命令；默认 RunE = list（参考 version.go 的 gh 模式）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newSkillsCmd() *cobra.Command {
	var output string
	var all bool
	cmd := &cobra.Command{
		Use:          "skills",
		Short:        "Manage Make platform skills",
		SilenceUsage: true,
		// NoArgs 让未知子命令（如已改名的 remove）报错，而不是静默回落到默认 list；
		// cobra 的 legacyArgs 只对 root 命令做此检查，非 root 需显式声明。
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillsList(cmd.Context(), output, all)
		},
	}
	addOutputFlag(cmd, &output)
	cmd.Flags().BoolVar(&all, "all", false, "include not-installed skills from the remote catalog")
	cmd.AddCommand(newSkillsListCmd())
	cmd.AddCommand(newSkillsInstallCmd())
	cmd.AddCommand(newSkillsUpdateCmd())
	cmd.AddCommand(newSkillsUninstallCmd())
	cmd.AddCommand(newSkillsReadCmd())
	return cmd
}

// skillsDoneLine 渲染 install / uninstall 的成功完成句：
// 「makeui skill installed.」/「makedsl, makeui skills uninstalled.」
func skillsDoneLine(names []string, verb string) string {
	noun := "skill"
	if len(names) > 1 {
		noun = "skills"
	}
	return fmt.Sprintf("%s %s %s.", strings.Join(names, ", "), noun, verb)
}
