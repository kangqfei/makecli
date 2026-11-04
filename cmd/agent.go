/**
 * [INPUT]: 依赖 fmt、strings、unicode/utf8、github.com/spf13/cobra、agent_{create,update,list,delete,info,status,run,runtime,model,plugin}.go
 * [OUTPUT]: 对外提供 agentCmd；包内 newAgentCmd / addAgentCommandGroup 和 validAgentAppKey
 * [POS]: cmd/agent 命令组，配置管理与目录查询分组展示，显式本地 run 保持隐藏；无子命令显示帮助
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

var agentCmd = newAgentCmd()

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "agent", Short: "Manage agents", Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	addAgentCommandGroup(cmd, &cobra.Group{ID: "management", Title: "AGENT MANAGEMENT COMMANDS"},
		newAgentCreateCmd(), newAgentUpdateCmd(), newAgentListCmd(), newAgentDeleteCmd(), newAgentInfoCmd(),
		newAgentStatusCmd("enable", "enabled"), newAgentStatusCmd("disable", "disabled"))
	addAgentCommandGroup(cmd, &cobra.Group{ID: "catalog", Title: "CATALOG COMMANDS"},
		newAgentRuntimeCmd(), newAgentModelCmd(), newAgentPluginCmd())
	cmd.AddCommand(newAgentRunCmd())
	return cmd
}

// addAgentCommandGroup 用同一份 group.ID 注册分组与成员，避免归属和展示定义漂移。
func addAgentCommandGroup(parent *cobra.Command, group *cobra.Group, commands ...*cobra.Command) {
	parent.AddGroup(group)
	for _, command := range commands {
		command.GroupID = group.ID
	}
	parent.AddCommand(commands...)
}

// validAgentAppKey 统一远端配置命令的应用归属约束，不套用资源 key 的 20 字符限制。
func validAgentAppKey(key string) error {
	if strings.TrimSpace(key) == "" || utf8.RuneCountInString(key) > 128 {
		return fmt.Errorf("--app must contain 1–128 characters and must not be blank")
	}
	return nil
}

func init() {
	rootCmd.AddCommand(agentCmd)
}
