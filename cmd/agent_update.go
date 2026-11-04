/**
 * [INPUT]: 依赖 cmd/client、validResourceKey、validAgentAppKey、cmd/jsonflag、cmd/output、internal/api、fmt、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newAgentUpdateCmd；包内 runAgentUpdate / agentUpdateRequest / agentUpdateOptions
 * [POS]: cmd/agent 的插件绑定替换子命令；--plugins-json 整表替换，已有绑定带 id，省略 writeOnly 密钥由服务端保留，其余 Agent 字段不发送
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"fmt"

	"github.com/qfeius/makecli/internal/api"
	"github.com/spf13/cobra"
)

type agentUpdateOptions struct {
	appKey, pluginsJSON, output string
}

func newAgentUpdateCmd() *cobra.Command {
	var o agentUpdateOptions
	cmd := &cobra.Command{
		Use:   "update <agentKey>",
		Short: "Update agent plugins on Make app",
		Long: "Replace plugin bindings for an agent on the actual environment appKey. " +
			"Include id to update an existing binding and omit writeOnly secrets to retain them. " +
			"Omit id to add a binding. Other agent fields are left unchanged.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Example: `  makecli agent update expense_agent --app expense_app --plugins-json '[{"id":"binding_01","pluginKey":"mcp_make","config":{"mode":"read"}}]'
  makecli agent update expense_agent --app expense_app --plugins-json @plugins.json --output json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentUpdate(args[0], o)
		},
	}
	cmd.Flags().StringVar(&o.appKey, "app", "", "actual environment app key (required)")
	_ = cmd.MarkFlagRequired("app")
	cmd.Flags().StringVar(&o.pluginsJSON, "plugins-json", "", "plugin bindings as a JSON array ("+jsonFlagForms+"); id updates an existing binding")
	_ = cmd.MarkFlagRequired("plugins-json")
	addOutputFlag(cmd, &o.output)
	return cmd
}

func runAgentUpdate(key string, o agentUpdateOptions) error {
	output, err := resolveOutputFormat(o.output)
	if err != nil {
		return err
	}
	req, err := agentUpdateRequest(key, o)
	if err != nil {
		return err
	}
	client, err := newClientFromProfile()
	if err != nil {
		return err
	}
	saved, err := client.UpdateAgent(req)
	if err != nil {
		return err
	}
	if output == outputJSON {
		return writeJSON(saved)
	}
	fmt.Printf("Agent '%s' updated successfully\n", saved.Key)
	fmt.Printf("%-10s %s\n%-10s %d\n", "App", saved.AppKey, "Plugins", len(saved.Plugins))
	return nil
}

// agentUpdateRequest 在触网前检查应用归属和插件数组形态。Schema、绑定 id 与密钥保留由服务端裁决。
func agentUpdateRequest(key string, o agentUpdateOptions) (api.UpdateAgentRequest, error) {
	req := api.UpdateAgentRequest{Key: key, AppKey: o.appKey}
	if err := validResourceKey(key); err != nil {
		return req, err
	}
	if err := validAgentAppKey(req.AppKey); err != nil {
		return req, err
	}
	var plugins []api.PluginBindingInput
	if err := decodeJSONFlag("plugins-json", o.pluginsJSON, &plugins); err != nil {
		return req, err
	}
	if plugins == nil {
		return req, fmt.Errorf("--plugins-json must be a JSON array")
	}
	req.Plugins = &plugins
	return req, nil
}
