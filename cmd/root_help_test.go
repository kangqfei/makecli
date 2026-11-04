/**
 * [INPUT]: 依赖 bytes、strings、testing、github.com/spf13/cobra
 * [OUTPUT]: 验证 skills 安装引导范围、未分组/分组与附加命令帮助，Agent 管理和目录分类及隐藏 run
 * [POS]: cmd/root.go usageTemplate 的回归测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const skillsSetupHint = "Skills setup (one-time, humans): makecli skills install --all --yes"

func renderHelp(t *testing.T, root *cobra.Command, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(append(args, "--help"))
	if err := root.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	return out.String()
}

func newHelpRoot() *cobra.Command {
	root := &cobra.Command{Use: "makecli"}
	installUsageTemplate(root)
	root.AddCommand(&cobra.Command{Use: "skills", Run: func(*cobra.Command, []string) {}})
	return root
}

func TestRootHelpShowsSkillsSetupHint(t *testing.T) {
	out := renderHelp(t, newHelpRoot())
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), skillsSetupHint) {
		t.Fatalf("root help should end with skills setup hint, got:\n%s", out)
	}
	if !strings.Contains(out, "AVAILABLE COMMANDS\n") {
		t.Fatalf("ungrouped root must retain available commands: %s", out)
	}
}

func TestAgentGroupedHelp(t *testing.T) {
	root := newHelpRoot()
	root.AddCommand(newAgentCmd())
	out := renderHelp(t, root, "agent")
	management := strings.Index(out, "AGENT MANAGEMENT COMMANDS\n")
	catalog := strings.Index(out, "CATALOG COMMANDS\n")
	flags := strings.Index(out, "FLAGS\n")
	if management < 0 || catalog <= management || flags <= catalog {
		t.Fatalf("unexpected group order: %s", out)
	}
	for _, name := range []string{"create", "delete", "disable", "enable", "info", "list", "update"} {
		if strings.Count(out, "  "+name+" ") != 1 || !strings.Contains(out[management:catalog], "  "+name+" ") {
			t.Errorf("management command %s missing, duplicated, or misplaced: %s", name, out)
		}
	}
	for _, name := range []string{"model", "plugin", "runtime"} {
		if strings.Count(out, "  "+name+" ") != 1 || !strings.Contains(out[catalog:flags], "  "+name+" ") {
			t.Errorf("catalog command %s missing, duplicated, or misplaced: %s", name, out)
		}
	}
	for _, forbidden := range []string{"AVAILABLE COMMANDS", "ADDITIONAL COMMANDS", "Run a local coding or chat agent"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("unexpected %q in agent help: %s", forbidden, out)
		}
	}
	if child := renderHelp(t, root, "agent", "plugin"); !strings.Contains(child, "AVAILABLE COMMANDS\n") || strings.Contains(child, "CATALOG COMMANDS") {
		t.Fatalf("ungrouped nested command must retain its own help: %s", child)
	}
}

func TestGroupedHelpAdditionalCommands(t *testing.T) {
	root := newHelpRoot()
	root.AddGroup(&cobra.Group{ID: "core", Title: "CORE COMMANDS"})
	root.AddCommand(&cobra.Command{Use: "visible", GroupID: "core", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(&cobra.Command{Use: "secret", GroupID: "core", Hidden: true, Run: func(*cobra.Command, []string) {}})
	out := renderHelp(t, root)
	group := strings.Index(out, "CORE COMMANDS\n")
	additional := strings.Index(out, "ADDITIONAL COMMANDS\n")
	if group < 0 || additional <= group || !strings.Contains(out[group:additional], "  visible ") || !strings.Contains(out[additional:], "  skills ") || strings.Contains(out, "secret") {
		t.Fatalf("grouped/ungrouped/hidden command rendering mismatch: %s", out)
	}
}

func TestSubcommandHelpOmitsSkillsSetupHint(t *testing.T) {
	out := renderHelp(t, newHelpRoot(), "skills")
	if strings.Contains(out, "Skills setup") {
		t.Fatalf("subcommand help should not carry skills setup hint, got:\n%s", out)
	}
}
