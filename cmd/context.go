/**
 * [INPUT]: 依赖 internal/config（ContextNames/LookupContext/DefaultContext）、cmd/client（resolveContext）、cmd/configure（setContext）、cmd/output（addOutputFlag/resolveOutputFormat/writeJSON）、fmt、os、github.com/olekukonko/tablewriter、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newContextCmd 函数（含 list/use/show 子命令）；包内 runContextList / runContextUse / runContextShow 白盒入口
 * [POS]: cmd 模块的 context 命令组——后端 context（dev/test/production）的用户面，对标 docker context：list 列内置 preset 并标当前、use 持久化到 [settings] context、show 回显解析结果；
 *        context 是内置 preset 故无 create/rm，自定义地址仍走 profile 的 URL 覆盖键；解析链与写路径分别复用 client.go resolveContext 与 configure.go setContext，不另起炉灶
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"fmt"
	"os"

	"github.com/olekukonko/tablewriter"
	"github.com/qfeius/makecli/internal/config"
	"github.com/spf13/cobra"
)

func newContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage the backend context (which Make backend to talk to)",
		Long: `A context selects which Make backend every command talks to: dev, test or production.
It is resolved as --context > $` + EnvContext + ` > [settings] context > ` + config.DefaultContext + `.

Not to be confused with an app's deployment environment (beta / production),
which is the --env flag of "app deploy" and "app delete".`,
		Example: `  makecli context list
  makecli context use test
  makecli context show

  # one-off override without switching
  makecli app list --context dev`,
	}
	cmd.AddCommand(newContextListCmd())
	cmd.AddCommand(newContextUseCmd())
	cmd.AddCommand(newContextShowCmd())
	return cmd
}

// ---------------------------------- list ----------------------------------

func newContextListCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:          "list",
		Aliases:      []string{"ls"},
		Short:        "List the built-in contexts and mark the current one",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runContextList(output)
		},
	}
	addOutputFlag(cmd, &output)
	return cmd
}

type contextJSONView struct {
	Name            string `json:"name"`
	Current         bool   `json:"current"`
	MetaServerURL   string `json:"meta_server_url"`
	RepoServerURL   string `json:"repo_server_url"`
	AuthServerURL   string `json:"auth_server_url"`
	AgentGatewayURL string `json:"agent_gateway_url"`
	TraceServerURL  string `json:"trace_server_url"`
}

func runContextList(output string) error {
	output, err := resolveOutputFormat(output)
	if err != nil {
		return err
	}
	current, _, err := resolveContext()
	if err != nil {
		return err
	}

	names := config.ContextNames()
	views := make([]contextJSONView, len(names))
	for i, name := range names {
		c, _ := config.LookupContext(name)
		views[i] = contextJSONView{
			Name:            name,
			Current:         name == current,
			MetaServerURL:   c.MetaServerURL,
			RepoServerURL:   c.RepoServerURL,
			AuthServerURL:   c.AuthServerURL,
			AgentGatewayURL: c.AgentGatewayURL,
			TraceServerURL:  c.TraceServerURL,
		}
	}
	if output == outputJSON {
		return writeJSON(views)
	}

	rows := make([][]string, len(views))
	for i, v := range views {
		marker := ""
		if v.Current {
			marker = "*"
		}
		rows[i] = []string{marker, v.Name, v.MetaServerURL, v.AuthServerURL}
	}
	table := tablewriter.NewTable(os.Stdout)
	table.Header("CURRENT", "NAME", "META SERVER", "AUTH SERVER")
	_ = table.Bulk(rows)
	_ = table.Render()
	return nil
}

// ---------------------------------- use ----------------------------------

func newContextUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "use <name>",
		Short:        "Set the current context (writes [settings] context, shared by every profile)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runContextUse(args[0])
		},
	}
}

func runContextUse(name string) error {
	if err := setContext(name); err != nil {
		return err
	}
	fmt.Printf("Switched to context %q.\n", name)
	return nil
}

// ---------------------------------- show ----------------------------------

func newContextShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "show",
		Short:        "Print the context in effect for this invocation",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runContextShow()
		},
	}
}

func runContextShow() error {
	name, _, err := resolveContext()
	if err != nil {
		return err
	}
	fmt.Println(name)
	return nil
}
