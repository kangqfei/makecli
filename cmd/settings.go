/**
 * [INPUT]: 依赖 internal/config（LoadSettings/SetSetting/ContextNames/ChannelNames/DefaultContext/DefaultChannel）、cmd/output（addOutputFlag/resolveOutputFormat/writeJSON）、fmt、os、slices、strconv、strings、github.com/olekukonko/tablewriter、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newSettingsCmd 函数（含 set/get/list 子命令）；包内 settingKeys 表、settingKeyNames、lookupSettingKey、requireSettingKey（profile 键误投时指路 configure，与 configure.go rejectSettingKey 对称）、setSetting（唯一校验+写入路径，被 context use 复用）、isSettingKey（供 configure 拒绝并指路）
 * [POS]: cmd 模块的 settings 命令组——config 文件 [settings] 全局段的用户面，与 INI 段 1:1（profile 段归 configure）：set 校验后写 config.SetSetting、get 打印生效值（未设置回退缺省）、list 列全部键含来源；
 *        每个键一行 settingKey{validate, value, def}，新增全局键只需加一行，set/get/list/doctor 自动跟上；context use / skills install --role 是 setSetting 之上的语义糖，不另起写路径
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/qfeius/makecli/internal/config"
	"github.com/spf13/cobra"
)

// ---------------------------------- 键表 ----------------------------------

// settingKey 描述一个 [settings] 全局键：取值校验、从 Settings 读原始值（"" = 未设置）、缺省值（"" = 无缺省，未设置就是未设置）。
type settingKey struct {
	name     string
	validate func(string) error
	value    func(config.Settings) string
	def      string
}

// oneOf 生成"取值 ∈ 名单"的校验器，错误信息列出合法值。
func oneOf(what string, names func() []string) func(string) error {
	return func(v string) error {
		if slices.Contains(names(), v) {
			return nil
		}
		return fmt.Errorf("unknown %s %q, valid: %s", what, v, strings.Join(names(), ", "))
	}
}

// settingKeys 是全部全局键的单一真相源（有序：list 与 doctor 按此顺序输出）。
var settingKeys = []settingKey{
	{
		name:     "context",
		validate: oneOf("context", config.ContextNames),
		value:    func(s config.Settings) string { return s.Context },
		def:      config.DefaultContext,
	},
	{
		name:     "channel",
		validate: oneOf("channel", config.ChannelNames),
		value:    func(s config.Settings) string { return s.Channel },
		def:      config.DefaultChannel,
	},
	{
		// 未设置即原有行为（skills 全量），故无缺省值：get 打空、list 显示 "-"
		name:     "role",
		validate: oneOf("role", config.RoleNames),
		value:    func(s config.Settings) string { return s.Role },
	},
	{
		name: "check-for-updates",
		validate: func(v string) error {
			if _, err := strconv.ParseBool(v); err != nil {
				return fmt.Errorf("check-for-updates must be true or false, got %q", v)
			}
			return nil
		},
		value: func(s config.Settings) string {
			if s.CheckForUpdates == nil {
				return ""
			}
			return strconv.FormatBool(*s.CheckForUpdates)
		},
		def: "true",
	},
}

// settingKeyNames 返回全部全局键名（表序），供 help 与错误提示。
func settingKeyNames() []string {
	names := make([]string, len(settingKeys))
	for i, k := range settingKeys {
		names[i] = k.name
	}
	return names
}

// lookupSettingKey 按名查键；未知名返回 ok=false。
func lookupSettingKey(name string) (settingKey, bool) {
	for _, k := range settingKeys {
		if k.name == name {
			return k, true
		}
	}
	return settingKey{}, false
}

// isSettingKey 回答"这个名字是不是全局键"，供 configure set/get 拒绝并指路到 settings。
func isSettingKey(name string) bool {
	_, ok := lookupSettingKey(name)
	return ok
}

// requireSettingKey 按名查键；profile 键误投到 settings 时指路 configure（与 configure.go rejectSettingKey 对称），
// 其余未知名列出合法全局键。
func requireSettingKey(name, verb string) (settingKey, error) {
	if k, ok := lookupSettingKey(name); ok {
		return k, nil
	}
	if slices.Contains(validConfigKeys, name) {
		return settingKey{}, fmt.Errorf("%q is a profile key, not a global setting; use: makecli configure %s %s", name, verb, name)
	}
	return settingKey{}, fmt.Errorf("unknown setting %q, valid: %s", name, strings.Join(settingKeyNames(), ", "))
}

// setSetting 是全局键的唯一校验 + 写入路径：键经 requireSettingKey，取值经该键的 validate，
// 通过后写 config.SetSetting（不受 --profile 影响）。context use 等语义糖都经此落盘。
func setSetting(name, value string) error {
	k, err := requireSettingKey(name, "set")
	if err != nil {
		return err
	}
	if err := k.validate(value); err != nil {
		return err
	}
	return config.SetSetting(name, value)
}

// effectiveSetting 返回一个键的生效值与来源：文件里有值取文件（source=config），否则取缺省（source=default）。
func effectiveSetting(k settingKey, s config.Settings) (value, source string) {
	if v := k.value(s); v != "" {
		return v, "config"
	}
	return k.def, "default"
}

// ---------------------------------- 命令组 ----------------------------------

func newSettingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Manage global settings (the [settings] section of ~/.make/config)",
		Long: `Global settings apply to every profile. Keys: ` + strings.Join(settingKeyNames(), ", ") + `.

Per-profile values (tenant, operator, host overrides) live in "makecli configure".`,
		Example: `  makecli settings list
  makecli settings set channel beta
  makecli settings get context`,
	}
	cmd.AddCommand(newSettingsSetCmd())
	cmd.AddCommand(newSettingsGetCmd())
	cmd.AddCommand(newSettingsListCmd())
	return cmd
}

func newSettingsSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "set <key> <value>",
		Short:        "Set a global setting",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return setSetting(args[0], args[1])
		},
	}
}

func newSettingsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "get <key>",
		Short:        "Print the effective value of a global setting",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSettingsGet(args[0])
		},
	}
}

func runSettingsGet(name string) error {
	k, err := requireSettingKey(name, "get")
	if err != nil {
		return err
	}
	s, err := config.LoadSettings()
	if err != nil {
		return err
	}
	value, _ := effectiveSetting(k, s)
	fmt.Println(value)
	return nil
}

func newSettingsListCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List every global setting with its effective value and source",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSettingsList(output)
		},
	}
	addOutputFlag(cmd, &output)
	return cmd
}

type settingJSONView struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

func runSettingsList(output string) error {
	output, err := resolveOutputFormat(output)
	if err != nil {
		return err
	}
	s, err := config.LoadSettings()
	if err != nil {
		return err
	}
	views := make([]settingJSONView, len(settingKeys))
	for i, k := range settingKeys {
		value, source := effectiveSetting(k, s)
		views[i] = settingJSONView{Key: k.name, Value: value, Source: source}
	}
	if output == outputJSON {
		return writeJSON(views)
	}
	rows := make([][]string, len(views))
	for i, v := range views {
		rows[i] = []string{v.Key, firstNonEmpty(v.Value, "-"), v.Source}
	}
	table := tablewriter.NewTable(os.Stdout)
	table.Header("KEY", "VALUE", "SOURCE")
	_ = table.Bulk(rows)
	_ = table.Render()
	return nil
}
