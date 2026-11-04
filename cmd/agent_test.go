/**
 * [INPUT]: 依赖 agent.go；stdout_test.go 的隔离 helper
 * [OUTPUT]: 对外提供 agent 命令回归——公开展示于根命令 help、默认帮助无 token 要求、运行参数下沉到 run
 * [POS]: cmd 模块的测试面（agent 命令）
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"strings"
	"testing"
)

func TestAgentCommandVisibleInHelp(t *testing.T) {
	if agentCmd.Hidden {
		t.Error("agent 命令必须公开")
	}
	var output strings.Builder
	rootCmd.SetOut(&output)
	rootCmd.SetErr(&output)
	t.Cleanup(func() { rootCmd.SetOut(nil); rootCmd.SetErr(nil) })
	rootCmd.SetArgs([]string{"--help"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	if !strings.Contains(output.String(), "agent") || !strings.Contains(output.String(), "Manage agents") {
		t.Errorf("help 应显示 agent 命令:\n%s", output.String())
	}
}

func TestAgentGroupShowsHelp(t *testing.T) {
	t.Setenv("MAKE_AGENT_TOKEN", "")
	cmd := newAgentCmd()
	var output strings.Builder
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"create", "update", "delete", "info", "list", "enable", "disable", "runtime", "model", "plugin"} {
		if !strings.Contains(output.String(), name) {
			t.Errorf("help missing %s: %s", name, output.String())
		}
	}
	run, _, err := cmd.Find([]string{"run"})
	if err != nil || run == cmd || !run.Hidden || strings.Contains(output.String(), run.Short) {
		t.Fatalf("run must remain callable but hidden from group help: %v", err)
	}
	for _, name := range []string{"approve", "chat-only", "token", "gateway-server-url", "model", "prompt", "system"} {
		if cmd.Flags().Lookup(name) != nil || strings.Contains(output.String(), "--"+name) {
			t.Errorf("run flag --%s must not belong to the agent group", name)
		}
	}
}
