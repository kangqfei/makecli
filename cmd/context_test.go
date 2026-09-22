/**
 * [INPUT]: 依赖 context.go 的 runContextList / runContextUse / runContextShow 与 newContextCmd；setContextFlag / captureStdout / stubStdoutTerminal 测试辅助；internal/config 隔离配置
 * [OUTPUT]: 覆盖 context 命令组的单元测试（list 表格标当前 / JSON 全字段 / use 持久化与拒绝未知名 / show 遵循解析链 / 旧键配置指引 doctor / 子命令与别名注册）
 * [POS]: cmd 模块 context.go 的配套测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/config"
)

func TestContextList(t *testing.T) {
	t.Run("table marks the current context", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		t.Setenv(EnvContext, "")
		setContextFlag(t, "")
		stubStdoutTerminal(t, true)
		if err := config.SetSetting("context", "test"); err != nil {
			t.Fatal(err)
		}

		out := captureStdout(t, func() {
			if err := runContextList(outputAuto); err != nil {
				t.Errorf("runContextList: %v", err)
			}
		})
		for _, want := range []string{"CURRENT", "NAME", "META SERVER", "AUTH SERVER", "dev", "production", "https://test-make.qtech.cn"} {
			if !strings.Contains(out, want) {
				t.Errorf("table missing %q:\n%s", want, out)
			}
		}
		if strings.Count(out, "*") != 1 {
			t.Errorf("exactly one row should be marked current:\n%s", out)
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "*") && !strings.Contains(line, "test") {
				t.Errorf("marker on wrong row: %q", line)
			}
		}
	})

	t.Run("json carries every preset field", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		t.Setenv(EnvContext, "")
		setContextFlag(t, "dev")

		out := captureStdout(t, func() {
			if err := runContextList(outputJSON); err != nil {
				t.Errorf("runContextList: %v", err)
			}
		})
		var views []contextJSONView
		if err := json.Unmarshal([]byte(out), &views); err != nil {
			t.Fatalf("json invalid: %v\n%s", err, out)
		}
		if len(views) != len(config.ContextNames()) {
			t.Fatalf("got %d contexts, want %d", len(views), len(config.ContextNames()))
		}
		for _, v := range views {
			if v.Current != (v.Name == "dev") {
				t.Errorf("%s: current = %v", v.Name, v.Current)
			}
			c, _ := config.LookupContext(v.Name)
			if v.MetaServerURL != c.MetaServerURL || v.RepoServerURL != c.RepoServerURL || v.AuthServerURL != c.AuthServerURL ||
				v.AgentGatewayURL != c.AgentGatewayURL || v.TraceServerURL != c.TraceServerURL {
				t.Errorf("%s: json view %+v does not match preset %+v", v.Name, v, c)
			}
		}
	})

	t.Run("legacy config points to doctor", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		t.Setenv(EnvContext, "")
		setContextFlag(t, "")
		if err := config.SetSetting("environment", "dev"); err != nil {
			t.Fatal(err)
		}
		err := runContextList(outputJSON)
		if err == nil || !strings.Contains(err.Error(), "makecli doctor --fix") {
			t.Fatalf("expected doctor guidance, got %v", err)
		}
	})

	t.Run("invalid output rejected", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		if err := runContextList("yaml"); err == nil {
			t.Error("expected error for invalid output format")
		}
	})
}

func TestContextUse(t *testing.T) {
	t.Run("persists to settings and confirms", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		out := captureStdout(t, func() {
			if err := runContextUse("dev"); err != nil {
				t.Errorf("runContextUse: %v", err)
			}
		})
		if !strings.Contains(out, `Global default context set to "dev".`) {
			t.Errorf("unexpected confirmation: %q", out)
		}
		s, err := config.LoadSettings()
		if err != nil {
			t.Fatal(err)
		}
		if s.Context != "dev" {
			t.Errorf("settings context = %q, want dev", s.Context)
		}
	})

	t.Run("rejects unknown context", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		err := runContextUse("staging")
		if err == nil || !strings.Contains(err.Error(), "dev, test, production") {
			t.Fatalf("expected unknown-context error listing valid names, got %v", err)
		}
	})
}

func TestContextShow(t *testing.T) {
	t.Run("follows the resolution chain", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		if err := config.SetSetting("context", "test"); err != nil {
			t.Fatal(err)
		}
		t.Setenv(EnvContext, "dev")
		setContextFlag(t, "")
		out := captureStdout(t, func() {
			if err := runContextShow(); err != nil {
				t.Errorf("runContextShow: %v", err)
			}
		})
		if strings.TrimSpace(out) != "dev" {
			t.Errorf("show = %q, want dev (env var over settings)", strings.TrimSpace(out))
		}
	})

	t.Run("default when nothing configured", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		t.Setenv(EnvContext, "")
		setContextFlag(t, "")
		out := captureStdout(t, func() { _ = runContextShow() })
		if strings.TrimSpace(out) != config.DefaultContext {
			t.Errorf("show = %q, want %s", strings.TrimSpace(out), config.DefaultContext)
		}
	})
}

func TestContextCmdRegistration(t *testing.T) {
	cmd := newContextCmd()
	names := map[string]bool{}
	for _, sub := range cmd.Commands() {
		names[sub.Name()] = true
		if sub.Name() == "list" && !sub.HasAlias("ls") {
			t.Error("list should have alias ls")
		}
	}
	for _, want := range []string{"list", "use", "show"} {
		if !names[want] {
			t.Errorf("context is missing subcommand %q", want)
		}
	}
}
