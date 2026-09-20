/**
 * [INPUT]: 依赖 internal/config（ConfigPath/LoadSettings/MigrateSettings/ContextNames/ChannelNames/DefaultContext/DefaultChannel）、cmd/client（resolveAccessToken/resolveContext/tokenSource 常量）、errors、fmt、slices、strings、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newDoctorCmd 函数、errDoctorFailed 哨兵错误、doctorFixHint 常量；包内 doctorChecks 检查表、runDoctor(fix) 白盒入口
 * [POS]: cmd 模块的 doctor 命令——本地配置体检，对齐 brew/flutter/npm doctor 的只读默认：检查表逐项求值，带 fix 的问题默认只标 fixable 并指引 --fix，
 *        --fix 时当场修复并回显（[settings] 旧键 environment → context 由 config.MigrateSettings 搬家），修不了的问题给 next-step 指引；
 *        这是旧配置升级到新格式的唯一通道——解析链（resolveContext）遇到旧键只报错指引 doctor --fix，不背兼容包袱；存在未修复问题返回 errDoctorFailed（退出码 1）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/qfeius/makecli/internal/config"
	"github.com/spf13/cobra"
)

// errDoctorFailed 表示体检后仍有未修复的问题。沿 RunE 链上抛，由 ExitCode 译为 1；
// 问题详情已由 doctor 自身打印，reportExecuteError 放过它不再打 error: 行。
var errDoctorFailed = errors.New("doctor: configuration needs attention")

func newDoctorCmd() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the health of your make environment",
		Long: `Doctor checks the health of your make environment.
With --fix it also applies the safe fixes in place.`,
		Example: `  makecli doctor
  makecli doctor --fix`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(fix)
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "apply the fixes doctor knows are safe (rewrites ~/.make/config)")
	return cmd
}

// doctorResult 是一次检查的结论：ok 时 msg 是状态摘要；否则 msg 描述问题，
// fix 非 nil 表示能自动修复（返回修复描述），nil 时 msg 自带 next-step 指引。
type doctorResult struct {
	ok  bool
	msg string
	fix func() (string, error)
}

// doctorCheck 是检查表的一行。检查按表顺序执行且每项重新读盘，
// 因此前一项的 fix 对后一项立即可见（旧键搬家后 context 检查看到的已是新键）。
type doctorCheck struct {
	name string
	run  func() doctorResult
}

var doctorChecks = []doctorCheck{
	{"settings", checkLegacySettings},
	{"context", checkContext},
	{"channel", checkChannel},
	{"token", checkToken},
}

// checkLegacySettings 发现 [settings] 仍用旧键即交 MigrateSettings 搬家。
func checkLegacySettings() doctorResult {
	s, err := config.LoadSettings()
	if err != nil {
		return doctorResult{msg: err.Error()}
	}
	if len(s.Legacy) == 0 {
		return doctorResult{ok: true, msg: "keys are up to date"}
	}
	return doctorResult{
		msg: "outdated key(s): " + strings.Join(sortedKeys(s.Legacy), ", "),
		fix: func() (string, error) {
			moved, err := config.MigrateSettings()
			if err != nil {
				return "", err
			}
			parts := make([]string, 0, len(moved))
			for _, old := range sortedKeys(moved) {
				parts = append(parts, fmt.Sprintf("[settings] %s → %s", old, moved[old]))
			}
			return "renamed " + strings.Join(parts, ", "), nil
		},
	}
}

// checkContext 校验 [settings] context 的值；未配置即默认值，不算问题。
func checkContext() doctorResult {
	s, err := config.LoadSettings()
	if err != nil {
		return doctorResult{msg: err.Error()}
	}
	if s.Context == "" {
		return doctorResult{ok: true, msg: fmt.Sprintf("not set, defaults to %s", config.DefaultContext)}
	}
	if !slices.Contains(config.ContextNames(), s.Context) {
		return doctorResult{msg: fmt.Sprintf("unknown context %q, valid: %s — run: makecli context use <name>", s.Context, strings.Join(config.ContextNames(), ", "))}
	}
	return doctorResult{ok: true, msg: s.Context}
}

// checkChannel 校验 [settings] channel 的值；未配置即默认值，不算问题。
func checkChannel() doctorResult {
	s, err := config.LoadSettings()
	if err != nil {
		return doctorResult{msg: err.Error()}
	}
	if s.Channel == "" {
		return doctorResult{ok: true, msg: fmt.Sprintf("not set, defaults to %s", config.DefaultChannel)}
	}
	if !slices.Contains(config.ChannelNames(), s.Channel) {
		return doctorResult{msg: fmt.Sprintf("unknown channel %q, valid: %s — run: makecli configure set channel <name>", s.Channel, strings.Join(config.ChannelNames(), ", "))}
	}
	return doctorResult{ok: true, msg: s.Channel}
}

// checkToken 确认当前 profile 拿得到 access token（来源 flag/env/credentials 任一）。
func checkToken() doctorResult {
	if err := config.ValidateProfileName(Profile); err != nil {
		return doctorResult{msg: err.Error()}
	}
	token, source, err := resolveAccessToken()
	if err != nil {
		return doctorResult{msg: err.Error()}
	}
	if token == "" {
		return doctorResult{msg: fmt.Sprintf("profile %q has no access token — run: makecli login --profile %s", Profile, Profile)}
	}
	return doctorResult{ok: true, msg: fmt.Sprintf("profile %q (from %s)", Profile, source)}
}

// doctorFixHint 是可修复问题在只读模式下的指引；解析链的旧键报错与之同文，用户看到的是同一句话。
const doctorFixHint = "makecli doctor --fix"

// runDoctor 按表逐项检查：通过打 ✓，问题打 ✗ 并计数。可修复的问题在 fix=true 时当场
// 修复后打 ✓ fixed 不计数，否则只标 fixable 并给出 --fix 指引；末尾汇总，仍有问题返回 errDoctorFailed。
func runDoctor(fix bool) error {
	path, err := config.ConfigPath()
	if err != nil {
		return err
	}
	fmt.Printf("%-9s %s\n\n", "Config:", path)

	problems := 0
	for _, c := range doctorChecks {
		res := c.run()
		switch {
		case res.ok:
			fmt.Printf("✓ %-9s %s\n", c.name, res.msg)
		case res.fix == nil:
			problems++
			fmt.Printf("✗ %-9s %s\n", c.name, res.msg)
		case !fix:
			problems++
			fmt.Printf("✗ %-9s %s (fixable, run: %s)\n", c.name, res.msg, doctorFixHint)
		default:
			fmt.Printf("✗ %-9s %s\n", c.name, res.msg)
			done, err := res.fix()
			if err != nil {
				problems++
				fmt.Printf("  %-9s fix failed: %v\n", "", err)
				continue
			}
			fmt.Printf("✓ %-9s %s\n", "fixed", done)
		}
	}

	if problems > 0 {
		fmt.Printf("\nFAIL: %s left, see above\n", plural(problems, "problem"))
		return errDoctorFailed
	}
	fmt.Printf("\nOK: configuration is healthy\n")
	return nil
}
