/**
 * [INPUT]: 依赖 context、errors、fmt、os、strings、charm.land/huh/v2、github.com/mattn/go-isatty、github.com/spf13/cobra、internal/skillsync（PlanInstall/Install/RoleSkills）、internal/config（RoleNames/UnsetSetting）、cmd/settings（lookupSettingKey/setSetting）
 * [OUTPUT]: 对外提供 newSkillsInstallCmd 函数；包级 planInstallFunc / installSkillsFunc / confirmInstallFunc 可打桩变量；包内 installSelection → installPlanArgs（按名 / --all / --role 归一，附确认后的 role 落盘动作）
 * [POS]: cmd/skills 的 install 子命令：按名选装（不碰 role）/ --all（全量，原有行为，并清除 [settings] role 回到全量同步）/ --role user（名单由 skillsync.RoleSkills 给出：只装 makecli，确认后、安装前写 [settings] role，让 update 后置同步沿用同一范围）三者互斥，
 *        缺省 huh confirm 确认（--yes 跳过，非 TTY 拒绝并指引），两阶段调用 skillsync.PlanInstall → Install
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"charm.land/huh/v2"
	"github.com/mattn/go-isatty"
	"github.com/qfeius/makecli/internal/config"
	"github.com/qfeius/makecli/internal/skillsync"
	"github.com/spf13/cobra"
)

// planInstallFunc / installSkillsFunc / confirmInstallFunc 为包级可打桩变量，
// 单测替换以隔离网络、npx 执行与终端交互（参照 skills_uninstall.go uninstallSkillsFunc 模式）。
var planInstallFunc = skillsync.PlanInstall
var installSkillsFunc = skillsync.Install
var confirmInstallFunc = confirmInstall

func newSkillsInstallCmd() *cobra.Command {
	var all, yes bool
	var role string

	cmd := &cobra.Command{
		Use:   "install [name]...",
		Short: "Install Make platform skills",
		Long: `Install Make platform skills by name, or all of them with --all.

--role user installs only the makecli skill (manage resources, no app
development) and remembers the choice in [settings] role, so "makecli update"
keeps syncing just that. --all installs everything and clears the role again.
Explicit names leave the role untouched.`,
		Example: `  makecli skills install makedsl makeui    # 按名选装
  makecli skills install --all             # 全量安装（装缺的 + 升级已有）
  makecli skills install --all --yes       # 跳过确认（CI / 非交互）
  makecli skills install --role user       # 只装 makecli，并记住只同步它`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillsInstall(cmd.Context(), cmd, args, installSelection{all: all, role: role}, yes)
		},
	}

	cmd.Flags().StringVar(&role, "role", "", "install the skill set for a role ("+strings.Join(config.RoleNames(), "|")+") and remember it in [settings] role")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "install all Make platform skills (clears [settings] role)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// installSelection 是 install 的选择方式：按名 / --all / --role 三选一。
type installSelection struct {
	all  bool
	role string
}

// installPlanArgs 是 resolve 的结果：交给 PlanInstall 的 (names, all)，以及确认后对 [settings] role 的动作。
type installPlanArgs struct {
	names     []string
	all       bool
	roleWrite func() error // nil = 不碰 role；--role 写入；--all 清除（回到全量即回到原状）
}

// resolve 把选择归一：--role → 名单由 skillsync.RoleSkills 给出并记住 role；
// --all → 全量并清除 role；按名 → 原样，role 不动。
func (s installSelection) resolve(names []string) (installPlanArgs, error) {
	switch {
	case (s.all || s.role != "") && len(names) > 0:
		return installPlanArgs{}, errors.New("cannot use --all / --role with skill names")
	case s.all && s.role != "":
		return installPlanArgs{}, errors.New("cannot use --all with --role")
	case s.role != "":
		if err := lookupAndValidateRole(s.role); err != nil {
			return installPlanArgs{}, err
		}
		roleNames, all := skillsync.RoleSkills(s.role)
		return installPlanArgs{names: roleNames, all: all, roleWrite: func() error { return setSetting("role", s.role) }}, nil
	case s.all:
		return installPlanArgs{all: true, roleWrite: func() error { return config.UnsetSetting("role") }}, nil
	case len(names) == 0:
		return installPlanArgs{}, errors.New("specify skill names or --all (run 'makecli skills list' to see what's available)")
	}
	return installPlanArgs{names: names}, nil
}

// lookupAndValidateRole 复用 settings 键表的取值校验，让 --role 与 settings set role 报同一句错。
func lookupAndValidateRole(role string) error {
	k, _ := lookupSettingKey("role")
	return k.validate(role)
}

func runSkillsInstall(ctx context.Context, cmd *cobra.Command, names []string, sel installSelection, yes bool) error {
	args, err := sel.resolve(names)
	if err != nil {
		return err
	}

	plan, err := planInstallFunc(ctx, args.names, args.all)
	if err != nil {
		return err
	}
	if plan.Warning != "" {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", plan.Warning)
	}

	if !yes {
		if err := confirmInstallFunc(plan); err != nil {
			return err
		}
	}

	// 确认之后、安装之前落盘 role：安装失败也留下选择，下次 update 按 role 自愈
	if args.roleWrite != nil {
		if err := args.roleWrite(); err != nil {
			return err
		}
	}

	if err := installSkillsFunc(ctx, plan); err != nil {
		return err
	}

	if len(plan.Names) > 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), skillsDoneLine(plan.Names, "installed"))
	} else {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Installed all Make platform skills")
	}
	return nil
}

// confirmInstall 在执行前确认安装计划（deploy production 同款 huh confirm 护栏）。
// 非交互终端（管道 / CI）无法确认，直接拒绝并指引 --yes，杜绝挂起。
func confirmInstall(plan skillsync.InstallPlan) error {
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return errors.New("refusing to install without confirmation: re-run with --yes in a non-interactive shell")
	}

	list := "all skills"
	if len(plan.Names) > 0 {
		list = strings.Join(plan.Names, ", ")
	}

	confirmed := false
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Install Make platform skills?").
				Description(fmt.Sprintf("Source: %s\nSkills: %s\nTarget: all detected code agents",
					skillsync.SkillsSource, list)).
				Affirmative("Install").
				Negative("Abort").
				Value(&confirmed),
		),
	).Run()

	if errors.Is(err, huh.ErrUserAborted) || (err == nil && !confirmed) {
		return errors.New("install cancelled")
	}
	return err
}
