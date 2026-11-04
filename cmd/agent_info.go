/**
 * [INPUT]: 依赖 cmd/client、validResourceKey、validAgentAppKey、cmd/output、internal/api、encoding/json、fmt、maps、os、slices、strings、github.com/olekukonko/tablewriter、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newAgentInfoCmd；包内 runAgentInfo/renderAgentInfo/formatPluginSecrets
 * [POS]: cmd/agent 的详情子命令，按 appKey + key 只读查询，文本展示基本信息、提示词、插件与 secretFields，JSON 返回完整配置
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/qfeius/makecli/internal/api"
	"github.com/spf13/cobra"
)

func newAgentInfoCmd() *cobra.Command {
	var appKey, output string
	cmd := &cobra.Command{
		Use:          "info <agentKey>",
		Short:        "Show agent details on Make app",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Example:      "  makecli agent --app agent_poc info poc_agent\n  makecli agent info poc_agent --app agent_poc --output json",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentInfo(args[0], appKey, output)
		},
	}
	cmd.Flags().StringVar(&appKey, "app", "", "actual environment app key (required)")
	_ = cmd.MarkFlagRequired("app")
	addOutputFlag(cmd, &output)
	return cmd
}

func runAgentInfo(key, appKey, output string) error {
	output, err := resolveOutputFormat(output)
	if err != nil {
		return err
	}
	if err := validResourceKey(key); err != nil {
		return err
	}
	if err := validAgentAppKey(appKey); err != nil {
		return err
	}
	client, err := newClientFromProfile()
	if err != nil {
		return err
	}
	agent, err := client.GetAgent(key, appKey)
	if err != nil {
		return err
	}
	if output == outputJSON {
		return writeJSON(agent)
	}
	return renderAgentInfo(agent)
}

func renderAgentInfo(agent *api.Agent) error {
	for _, field := range [][2]string{
		{"Key:", agent.Key}, {"Name:", agent.Name}, {"Description:", agent.Description},
		{"App:", agent.AppKey}, {"Model:", agent.ModelKey}, {"Runtime:", agent.RuntimeKey},
		{"Status:", agent.Status}, {"Created At:", agent.Meta.CreatedAt}, {"Updated At:", agent.Meta.UpdatedAt},
		{"Prompt:", agent.Prompt},
	} {
		fmt.Printf("%-13s %s\n", field[0], field[1])
	}
	if len(agent.Plugins) == 0 {
		fmt.Printf("%-13s %s\n", "Plugins:", "-")
		return nil
	}
	rows := make([][]string, len(agent.Plugins))
	for i, plugin := range agent.Plugins {
		config, err := json.Marshal(plugin.Config)
		if err != nil {
			return err
		}
		endpoint := "-"
		if plugin.ChannelEndpoint != nil {
			endpoint = plugin.ChannelEndpoint.ServerURL
		}
		rows[i] = []string{plugin.ID, plugin.PluginKey, fmt.Sprint(plugin.Enabled), string(config), endpoint, formatPluginSecrets(plugin.SecretFields)}
	}
	fmt.Println()
	table := tablewriter.NewTable(os.Stdout)
	table.Header("ID", "PLUGIN", "ENABLED", "CONFIG", "ENDPOINT", "SECRETS")
	if err := table.Bulk(rows); err != nil {
		return err
	}
	return table.Render()
}

// formatPluginSecrets 按 JSON Pointer 排序展示只读密钥状态，不输出明文。
func formatPluginSecrets(fields map[string]api.AgentPluginSecretField) string {
	if len(fields) == 0 {
		return "-"
	}
	keys := slices.Sorted(maps.Keys(fields))
	parts := make([]string, len(keys))
	for i, key := range keys {
		field := fields[key]
		state := "unconfigured"
		if field.Configured {
			state = "configured"
		}
		parts[i] = key + " " + state
		if field.MaskedValue != nil && *field.MaskedValue != "" {
			parts[i] += " " + *field.MaskedValue
		}
	}
	return strings.Join(parts, ", ")
}
