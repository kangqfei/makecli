/**
 * [INPUT]: 依赖 github.com/spf13/cobra、internal/update、internal/build、internal/config（通道常量）、internal/skillsync；client.go 的 resolveChannel
 * [OUTPUT]: 对外提供 newUpdateCmd 函数；包内 runUpdateCheck / changelogFileURL / channelSuffix / hintBetaAboveStable
 * [POS]: cmd 模块的 update 子命令，从 GitHub Releases 自更新二进制；
 *        无 arg 走 latest 流程（按 [settings] channel 选通道：stable 走 /releases/latest、beta 走列表取 semver 最高），有 arg 走指定版本流程；降级需 --force；DEV 版本跳过比较；
 *        --check 仅检查最新版本并打印报告，不安装、不同步 skills；stable 通道下持有高于最新稳定版的 beta 时补降级去向提示；二进制流程结束后默认每次同步 Make platform skills
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/qfeius/makecli/internal/build"
	"github.com/qfeius/makecli/internal/config"
	"github.com/qfeius/makecli/internal/skillsync"
	"github.com/qfeius/makecli/internal/update"
	"github.com/spf13/cobra"
)

// applyFunc 包装 update.Apply，便于测试打桩避免真实替换二进制。
var applyFunc = update.Apply

// syncSkillsFunc 包装 skillsync.Sync，便于测试打桩避免真实执行 npx。
var syncSkillsFunc = skillsync.Sync

func newUpdateCmd() *cobra.Command {
	var force bool
	var check bool
	var skipSkills bool
	cmd := &cobra.Command{
		Use:   "update [version]",
		Short: "Update makecli to the latest or a specific version",
		Example: `  makecli update
  makecli update --check
  makecli update v0.2.0
  makecli update --force v0.0.1
  makecli update --skip-skills
  makecli settings set channel beta    # make bare 'update' track pre-releases`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			if check {
				if target != "" {
					return fmt.Errorf("--check does not take a version argument")
				}
				return runUpdateCheck(cmd, build.Version)
			}
			return runUpdate(cmd, target, force, skipSkills)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "allow downgrade to an older version")
	cmd.Flags().BoolVar(&check, "check", false, "report whether an update is available without installing")
	cmd.Flags().BoolVar(&skipSkills, "skip-skills", false, "skip Make platform skills sync")
	return cmd
}

// runUpdateCheck 仅检查最新版本并打印报告，不下载、不替换二进制、不同步 skills。
func runUpdateCheck(cmd *cobra.Command, currentVersion string) error {
	channel, err := resolveChannel()
	if err != nil {
		return err
	}
	release, newer, err := update.CheckLatest(currentVersion, channel == config.ChannelBeta)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if !newer {
		_, _ = fmt.Fprintf(out, "Already up to date (%s)%s\n", release.TagName, channelSuffix(channel))
		hintBetaAboveStable(out, currentVersion, release.TagName, channel)
		return nil
	}

	_, _ = fmt.Fprintf(out, "Update available: %s → %s%s\n",
		formatCurrentVersion(currentVersion), release.TagName, channelSuffix(channel))
	_, _ = fmt.Fprintf(out, "  Release:   %s\n", release.HTMLURL)
	_, _ = fmt.Fprintf(out, "  Changelog: %s\n", changelogFileURL())
	_, _ = fmt.Fprintf(out, "\nRun `makecli update` to install.\n")
	return nil
}

// changelogFileURL 返回仓库 CHANGELOG.md 的固定地址（main 分支始终最新）。
func changelogFileURL() string {
	return "https://github.com/qfeius/makecli/blob/main/CHANGELOG.md"
}

func runUpdate(cmd *cobra.Command, target string, force, skipSkills bool) error {
	currentVersion := build.Version
	if target == "" {
		return runUpdateLatest(cmd, currentVersion, skipSkills)
	}
	return runUpdateSpecific(cmd, currentVersion, target, force, skipSkills)
}

func runUpdateLatest(cmd *cobra.Command, currentVersion string, skipSkills bool) error {
	channel, err := resolveChannel()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Checking for updates...%s\n", channelSuffix(channel))

	release, newer, err := update.CheckLatest(currentVersion, channel == config.ChannelBeta)
	if err != nil {
		return err
	}

	if !newer {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Already up to date (%s)%s\n", release.TagName, channelSuffix(channel))
		hintBetaAboveStable(cmd.OutOrStdout(), currentVersion, release.TagName, channel)
		return runSkillSync(cmd, release.TagName, skipSkills)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updating makecli: %s → %s\n",
		formatCurrentVersion(currentVersion), release.TagName)

	if err := applyFunc(release); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated makecli: %s → %s\n",
		formatCurrentVersion(currentVersion), release.TagName)
	return runSkillSync(cmd, release.TagName, skipSkills)
}

func runUpdateSpecific(cmd *cobra.Command, currentVersion, target string, force, skipSkills bool) error {
	tag, err := update.NormalizeTag(target)
	if err != nil {
		return err
	}

	release, err := update.GetRelease(tag)
	if err != nil {
		return err
	}

	cmp := update.CompareVersions(tag, currentVersion)
	switch {
	case cmp == 0:
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Already at %s, skipping.\n", tag)
		return runSkillSync(cmd, tag, skipSkills)
	case cmp < 0 && !force:
		return fmt.Errorf("%s is older than current %s. Use --force to downgrade",
			tag, formatCurrentVersion(currentVersion))
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updating makecli: %s → %s\n",
		formatCurrentVersion(currentVersion), tag)

	if err := applyFunc(release); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated makecli: %s → %s\n",
		formatCurrentVersion(currentVersion), tag)
	return runSkillSync(cmd, tag, skipSkills)
}

func runSkillSync(cmd *cobra.Command, version string, skipSkills bool) error {
	// 同步范围随 [settings] role：user 只刷 makecli，developer 全量——用户在 skills install --role 里的选择不被冲掉
	role, err := resolveRole()
	if err != nil {
		return err
	}
	renderSkillSyncStart(cmd.OutOrStdout(), skillsync.SkillsSource, skillsync.SyncCommand(role))

	result, err := syncSkillsFunc(cmd.Context(), skillsync.Options{
		Version: formatCurrentVersion(version),
		Skip:    skipSkills,
		Role:    role,
	})
	if err != nil {
		return err
	}

	renderSkillSyncResult(cmd.OutOrStdout(), result)
	return nil
}

func renderSkillSyncStart(w io.Writer, source string, command []string) {
	_, _ = fmt.Fprintf(w, "Syncing Make platform skills: %s\n", source)
	_, _ = fmt.Fprintf(w, "Skills command: %s\n", strings.Join(command, " "))
}

func renderSkillSyncResult(w io.Writer, result skillsync.Result) {
	switch result.Action {
	case skillsync.ActionSkipped:
		_, _ = fmt.Fprintf(w, "Skills sync skipped for makecli %s (%s)\n", result.Version, result.Reason)
	default:
		_, _ = fmt.Fprintf(w, "Skills updated for makecli %s\n", result.Version)
	}
}

// channelSuffix 仅在非默认通道时回显，稳定用户输出零变化
func channelSuffix(channel string) string {
	if channel == config.ChannelBeta {
		return " [beta channel]"
	}
	return ""
}

// hintBetaAboveStable 消解「Already up to date 却拿着 -beta.N」的字面矛盾：
// stable 通道下当前预发布版本高于最新稳定版时，补两行去向说明。
func hintBetaAboveStable(w io.Writer, current, latestStable, channel string) {
	if channel != config.ChannelStable || !update.IsPrerelease(current) {
		return
	}
	if update.CompareVersions(latestStable, current) >= 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "Note: current %s is a pre-release above the stable channel.\n", formatCurrentVersion(current))
	_, _ = fmt.Fprintf(w, "Run `makecli update %s --force` to return to stable, or wait for a newer stable release.\n", latestStable)
}

// formatCurrentVersion 格式化当前版本号用于显示
func formatCurrentVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	if v == "DEV" {
		return v
	}
	return "v" + v
}
