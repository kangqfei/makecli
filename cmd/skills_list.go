/**
 * [INPUT]: 依赖 context、fmt、os、slices、github.com/olekukonko/tablewriter、github.com/spf13/cobra、internal/skillsync、cmd/output 辅助
 * [OUTPUT]: 对外提供 newSkillsListCmd 函数；包内 runSkillsList 被 skills 命令组默认行为复用
 * [POS]: cmd/skills 的 list 子命令，合并本地 lockfile 与 GitHub 远端状态，输出列 NAME/STATUS/DESCRIPTION/UPDATED AT；默认只列已安装（包管理器 list 惯例），--all 才含远端 not installed 条目（表格与 JSON 同一过滤语义），默认视图汇总行提示 N more available（available 按 [settings] role 的名单过滤：user 不把 developer skills 当可装；空态引导带当前 role）；支持 table|json；远端失败降级 unknown + stderr 警告，退出码恒 0
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/olekukonko/tablewriter"
	"github.com/qfeius/makecli/internal/skillsync"
	"github.com/spf13/cobra"
)

// listSkillsFunc 包装 skillsync.List，便于测试打桩避免读真实 lockfile / 触网。
var listSkillsFunc = skillsync.List

func newSkillsListCmd() *cobra.Command {
	var output string
	var all bool
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List installed Make platform skills",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillsList(cmd.Context(), output, all)
		},
	}
	addOutputFlag(cmd, &output)
	cmd.Flags().BoolVar(&all, "all", false, "include not-installed skills from the remote catalog")
	return cmd
}

func runSkillsList(ctx context.Context, output string, all bool) error {
	output, err := resolveOutputFormat(output)
	if err != nil {
		return err
	}

	inv := listSkillsFunc(ctx)

	if inv.LockWarning != "" {
		_, _ = fmt.Fprintf(os.Stderr, "warning: %s\n", inv.LockWarning)
	}
	if inv.RemoteErr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "warning: remote check failed: %v\n", inv.RemoteErr)
	}

	// 默认只列已安装（list 的包管理器惯例）；--all 才展示远端完整目录。
	skills := inv.Skills
	if !all {
		skills = slices.DeleteFunc(slices.Clone(inv.Skills), func(s skillsync.SkillInfo) bool {
			return s.Status == skillsync.StatusNotInstalled
		})
	}

	if output == outputJSON {
		return writeJSON(map[string]any{"data": skills})
	}

	// 角色决定"可装"的分母：user 只关心名单内的 skill，其余 skills 不该被当成 more available；未设置即全量（原有行为）
	role, err := resolveRole()
	if err != nil {
		return err
	}
	roleNames, _ := skillsync.RoleSkills(role)

	if len(skills) == 0 {
		fmt.Println("No Make platform skills installed.")
		fmt.Printf("Run 'makecli skills install %s --yes' to install.\n", installFlagForRole(role))
		return nil
	}

	rows := make([][]string, len(skills))
	for i, s := range skills {
		rows[i] = []string{s.Name, s.Status, truncateLine(s.Description, 60), shortDate(s.UpdatedAt)}
	}

	table := tablewriter.NewTable(os.Stdout)
	table.Header("NAME", "STATUS", "DESCRIPTION", "UPDATED AT")
	_ = table.Bulk(rows)
	_ = table.Render()

	// 汇总永远基于全量清单（默认视图靠它提示被隐藏的条目），available 按角色名单过滤。
	installed, outdated, available := summarizeSkills(inv.Skills, roleNames)
	if all {
		fmt.Printf("\n%d installed, %d outdated, %d available\n", installed, outdated, available)
	} else {
		fmt.Printf("\n%d installed, %d outdated%s\n", installed, outdated, availableHint(available))
	}
	if outdated+available > 0 {
		fmt.Println("Run 'makecli skills update' to install/upgrade.")
	}
	return nil
}

// installFlagForRole 把当前 role 翻译成 install 的选择 flag：未设置 → --all（原有引导），user → --role user。
func installFlagForRole(role string) string {
	if role == "" {
		return "--all"
	}
	return "--role " + role
}

// availableHint 在默认视图汇总行尾附上被隐藏的可装数量；0 可装时整段消失。
func availableHint(available int) string {
	if available == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d more available, --all to show)", available)
}

// summarizeSkills 统计已安装 / 落后 / 远端可装数量。
// roleNames 非 nil 时，只有名单内的未装 skill 计入 available（nil = 全量角色，远端全部计入）。
func summarizeSkills(skills []skillsync.SkillInfo, roleNames []string) (installed, outdated, available int) {
	for _, s := range skills {
		switch s.Status {
		case skillsync.StatusNotInstalled:
			if roleNames == nil || slices.Contains(roleNames, s.Name) {
				available++
			}
		case skillsync.StatusOutdated:
			installed++
			outdated++
		default: // up-to-date / removed upstream / unknown 都属已安装
			installed++
		}
	}
	return installed, outdated, available
}

// truncateLine 把描述截到 max 个 rune 加省略号——表格列宽护栏，JSON 输出保留全文。
func truncateLine(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// shortDate 把 ISO 时间戳裁到日期部分——表格展示用，JSON 输出保留全值。
func shortDate(iso string) string {
	if len(iso) > 10 {
		return iso[:10]
	}
	return iso
}
