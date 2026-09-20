/**
 * [INPUT]: 依赖 internal/config（ConfigPath/LoadSettings/MigrateSettings）、cmd/settings（settingKeys 表）、cmd/client（resolveAccessToken/tokenSource 常量）、errors、fmt、strings、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newDoctorCmd 函数、errDoctorFailed 哨兵错误、doctorFixHint 常量；包内 doctorChecks 检查表组装、checkSetting 按键生成检查、runDoctor(fix) 白盒入口
 * [POS]: cmd 模块的 doctor 命令——本地配置体检，对齐 brew/flutter/npm doctor 的只读默认：检查表逐项求值（旧键搬家 → settingKeys 表逐键取值校验 → 凭证），带 fix 的问题默认只标 fixable 并指引 --fix，
 *        --fix 时当场修复并回显（[settings] 旧键 environment → context 由 config.MigrateSettings 搬家），修不了的问题给 next-step 指引；
 *        这是旧配置升级到新格式的唯一通道——解析链（resolveContext）遇到旧键只报错指引 doctor --fix，不背兼容包袱；存在未修复问题返回 errDoctorFailed（退出码 1）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"errors"
	"fmt"
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

// doctorChecks 按序组装：旧键搬家先行，随后逐个全局键（settingKeys 表序，与 settings list 同序），最后凭证。
func doctorChecks() []doctorCheck {
	checks := []doctorCheck{{"settings", checkLegacySettings}}
	for _, k := range settingKeys {
		checks = append(checks, doctorCheck{k.name, checkSetting(k)})
	}
	return append(checks, doctorCheck{"token", checkToken})
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

// checkSetting 生成一个全局键的检查：未配置即默认值不算问题；配置了就过该键的 validate，
// 不合法附 settings set 指引（值无法猜测，不提供 fix）。
func checkSetting(k settingKey) func() doctorResult {
	return func() doctorResult {
		s, err := config.LoadSettings()
		if err != nil {
			return doctorResult{msg: err.Error()}
		}
		v := k.value(s)
		if v == "" {
			return doctorResult{ok: true, msg: fmt.Sprintf("not set, defaults to %s", k.def)}
		}
		if err := k.validate(v); err != nil {
			return doctorResult{msg: fmt.Sprintf("%v — run: makecli settings set %s <value>", err, k.name)}
		}
		return doctorResult{ok: true, msg: v}
	}
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
	fmt.Printf("Config: %s\n\n", path)

	// 名字列按最长检查名对齐（settingKeys 里的键名长度不一，如 check-for-updates）
	checks := doctorChecks()
	width := 0
	for _, c := range checks {
		width = max(width, len(c.name))
	}
	line := func(mark, name, msg string) { fmt.Printf("%s %-*s %s\n", mark, width, name, msg) }

	problems := 0
	for _, c := range checks {
		res := c.run()
		switch {
		case res.ok:
			line("✓", c.name, res.msg)
		case res.fix == nil:
			problems++
			line("✗", c.name, res.msg)
		case !fix:
			problems++
			line("✗", c.name, res.msg+" (fixable, run: "+doctorFixHint+")")
		default:
			line("✗", c.name, res.msg)
			done, err := res.fix()
			if err != nil {
				problems++
				line(" ", "", "fix failed: "+err.Error())
				continue
			}
			line("✓", "fixed", done)
		}
	}

	if problems > 0 {
		fmt.Printf("\nFAIL: %s left, see above\n", plural(problems, "problem"))
		return errDoctorFailed
	}
	fmt.Printf("\nOK: configuration is healthy\n")
	return nil
}
