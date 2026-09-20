/**
 * [INPUT]: 依赖 github.com/spf13/cobra、github.com/spf13/pflag、os、strings、internal/config（ContextNames/DefaultContext）、internal/notifier
 * [OUTPUT]: 对外提供 Execute 函数、rootCmd 根命令、全局变量 Profile / AccessToken / MetaServerURL / Context / DebugMode；包内 commandName 解析器（Execute 前解析顶级命令名喂给 notifier.Start）、installUsageTemplate（usageTemplate + 模板函数装配，根 help 尾附 skills 安装引导）
 * [POS]: cmd 模块的入口，挂载 version / configure / context / doctor / login / whoami / app / entity / relation / record / apply / diff / update / skills / schema / integration / preflight 子命令；定义全局 --profile / --access-token / --meta-server-url / --context / --debug PersistentFlag；后端 URL 兜底交给 config.Context preset；错误呈现经 reportExecuteError 单一出口（SilenceErrors，见 errors.go）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"os"
	"strings"

	"github.com/qfeius/makecli/internal/config"
	"github.com/qfeius/makecli/internal/notifier"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// DebugMode 全局调试模式标志，从命令行读取
var DebugMode bool

// MetaServerURL Meta Server 基础 URL，从命令行读取。空串 = 回退 $MAKE_META_SERVER_URL > profile config > 环境内置地址（client.go metaServerURL）
var MetaServerURL string

// Profile 全局凭证 profile 名称，从命令行读取（--profile）。
// 默认值与 PersistentFlag 注册一致，确保未经过 cobra 解析时（如单元测试）也可用。
var Profile = "default"

// AccessToken 全局 access token 覆盖（--access-token / -t）。空串 = 回退 $MAKE_ACCESS_TOKEN，再回退 credentials 文件。
// 取值链收口在 client.go resolveAccessToken；flag 值会留在 shell history / ps，CI 应优先用环境变量。
var AccessToken string

// Context 全局后端 context 名（--context）。空串 = 回退 $MAKE_CLI_CONTEXT > [settings] context > config.DefaultContext。
// 后端 URL 五件套由当前 context 的 config.Context preset 兜底（见 client.go resolveContext）。
// 词汇约定：context 选 Make 后端（dev/test/production）；environment 只指 app 的部署环境（beta/production，app deploy/delete 的 --env）。
var Context string

var rootCmd = &cobra.Command{
	Use:   "makecli",
	Short: "makecli — agentic development platform cli",
	// 任何子命令执行前先落定本次调用的输出格式，--debug 转储形态据此选文本/JSON
	PersistentPreRun: func(cmd *cobra.Command, _ []string) { resolveInvokedOutput(cmd) },
}

// usageTemplate 对齐 GitHub CLI 风格：段落标题全大写；根命令尾部附 skills 一次性安装引导（对齐 lark-cli，仅根级）
// 不含命令描述——描述由 cobra 默认 HelpTemplate 的 (or .Long .Short) 负责，
// 此处再印一遍会与 --help 重复（曾因所有命令只设 Short 而未暴露）
const usageTemplate = `USAGE
  {{.UseLine}}{{if .HasAvailableSubCommands}} [command]{{end}}
{{if .HasAvailableSubCommands}}
AVAILABLE COMMANDS
{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}  {{rpad .Name .NamePadding }} {{.Short}}
{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}
FLAGS
{{.LocalFlags.FlagUsages | trimRightSpace}}
{{end}}{{if parentFlags .}}
INHERITED FLAGS
{{parentFlags . | trimRightSpace}}
{{end}}{{if globalFlags .}}
GLOBAL FLAGS
{{globalFlags . | trimRightSpace}}
{{end}}{{if .HasExample}}
EXAMPLES
{{.Example}}
{{end}}{{if .HasAvailableSubCommands}}
Use "{{.CommandPath}} [command] --help" for more information about a command.
{{end}}{{if not .HasParent}}
Skills setup (one-time, humans): {{.CommandPath}} skills install --all --yes
{{end}}`

// installUsageTemplate 给 root 装上 usageTemplate 及其依赖的模板函数：
// 拆分 InheritedFlags 为 global（root 级）和 parent（中间命令级）。
func installUsageTemplate(root *cobra.Command) {
	cobra.AddTemplateFunc("globalFlags", func(cmd *cobra.Command) string {
		fs := pflag.NewFlagSet("global", pflag.ContinueOnError)
		cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
			if root.PersistentFlags().Lookup(f.Name) != nil {
				fs.AddFlag(f)
			}
		})
		return fs.FlagUsages()
	})
	cobra.AddTemplateFunc("parentFlags", func(cmd *cobra.Command) string {
		fs := pflag.NewFlagSet("parent", pflag.ContinueOnError)
		cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
			if root.PersistentFlags().Lookup(f.Name) == nil {
				fs.AddFlag(f)
			}
		})
		return fs.FlagUsages()
	})
	root.SetUsageTemplate(usageTemplate)
}

// Execute 是程序入口，由 main.go 调用
func Execute(version, buildDate string) error {
	rootCmd.Version = formatVersion(version, buildDate)
	rootCmd.SetVersionTemplate(`{{.Version}}`)
	installUsageTemplate(rootCmd)
	// 子命令自己的 PersistentPreRunE（configure/entity/record/relation）默认会遮蔽 root 的钩子，
	// 开启遍历让 root 的输出格式落定对全部命令生效
	cobra.EnableTraverseRunHooks = true
	// 错误呈现收口到 Execute 出口的 reportExecuteError 单一出口：
	// 抑制 cobra 自动打印，让鉴权失败能升级为引导、退出码哨兵能保持静默。
	rootCmd.SilenceErrors = true
	rootCmd.PersistentFlags().BoolVar(&DebugMode, "debug", false, "enable debug mode: dump each HTTP request/response to stderr (curl-style text, or JSON when --output is json)")
	_ = rootCmd.PersistentFlags().MarkHidden("debug")
	rootCmd.PersistentFlags().StringVar(&MetaServerURL, "meta-server-url", "", "Meta Server base URL (overrides $"+EnvMetaServerURL+" and the profile config)")
	rootCmd.PersistentFlags().StringVar(&Profile, "profile", "default", "credentials profile to use")
	rootCmd.PersistentFlags().StringVarP(&AccessToken, "access-token", "t", "", "access token (overrides $"+EnvAccessToken+" and the profile credentials)")
	rootCmd.PersistentFlags().StringVar(&Context, "context", "", "backend context "+strings.Join(config.ContextNames(), "|")+" (overrides $"+EnvContext+" and [settings] context, default "+config.DefaultContext+")")
	rootCmd.AddCommand(newVersionCmd(version, buildDate))
	rootCmd.AddCommand(newConfigureCmd())
	rootCmd.AddCommand(newSettingsCmd())
	rootCmd.AddCommand(newContextCmd())
	rootCmd.AddCommand(newDoctorCmd())
	rootCmd.AddCommand(newLoginCmd())
	rootCmd.AddCommand(newWhoamiCmd())
	rootCmd.AddCommand(newApplyCmd())
	rootCmd.AddCommand(newAppCmd())
	rootCmd.AddCommand(newEntityCmd())
	rootCmd.AddCommand(newRelationCmd())
	rootCmd.AddCommand(newRecordCmd())
	rootCmd.AddCommand(newUpdateCmd())
	rootCmd.AddCommand(newSkillsCmd())
	rootCmd.AddCommand(newDiffCmd())
	rootCmd.AddCommand(newPreflightCmd())
	rootCmd.AddCommand(newSchemaCmd())
	rootCmd.AddCommand(newIntegrationCmd())
	n := notifier.Start(commandName(rootCmd, os.Args[1:]))
	err := rootCmd.Execute()
	reportExecuteError(os.Stderr, err)
	// 对齐 gh：升级提示只在命令成功后、所有输出之后出现；失败时不提示，也不为后台刷新多等。
	if err == nil {
		n.Finish()
	}
	return err
}

// commandName 解析本次实际调用的顶级子命令名（version/update/app...）。
// 无子命令或解析失败时返回 ""（由判定链视为跳过）。
func commandName(root *cobra.Command, args []string) string {
	cmd, _, err := root.Find(args)
	if err != nil || cmd == nil || cmd == root {
		return ""
	}
	for cmd.Parent() != nil && cmd.Parent() != root {
		cmd = cmd.Parent()
	}
	return cmd.Name()
}
