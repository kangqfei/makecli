/**
 * [INPUT]: 依赖 daemon.go 的地址解析与入册凭证互斥逻辑；setContextFlag/setProfile 测试辅助；internal/config 隔离 credentials
 * [OUTPUT]: 对外提供 gateway 地址取值链与 node key/setup-key 互斥状态机的单元测试
 * [POS]: cmd 模块的 daemon 测试面——锁定"缺省零配置连对环境"与"重新入册必须先 uninstall"纪律
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/config"
)

func TestResolveAgentServerURLPresetByContext(t *testing.T) {
	t.Setenv("MAKE_CLI_CONFIG_DIR", t.TempDir()) // 隔离 [settings] context
	tests := []struct {
		context string
		want    string
	}{
		{"dev", "https://dev-make-agent.qtech.cn"},
		{"test", "https://test-make-agent.qtech.cn"},
		{"production", "https://make-agent.qfei.cn"},
	}
	for _, tt := range tests {
		setContextFlag(t, tt.context)
		url, err := resolveAgentGatewayServerURL()
		if err != nil {
			t.Fatalf("resolve(%s): %v", tt.context, err)
		}
		if url != tt.want {
			t.Fatalf("resolve(%s) = %q, want %q", tt.context, url, tt.want)
		}
	}
}

func TestResolveAgentServerURLEnvVarOverridesPreset(t *testing.T) {
	t.Setenv("MAKE_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("MAKE_AGENT_SERVER_URL", "http://10.26.2.221:8081")
	setContextFlag(t, "production")
	url, err := resolveAgentGatewayServerURL()
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://10.26.2.221:8081" {
		t.Fatalf("env var 应覆盖 preset: %q", url)
	}
}

func TestResolveAgentServerURLFlagWins(t *testing.T) {
	t.Setenv("MAKE_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("MAKE_AGENT_SERVER_URL", "http://from-env:1")
	original := daemonGatewayServerURL
	daemonGatewayServerURL = "http://from-flag:2"
	t.Cleanup(func() { daemonGatewayServerURL = original })
	url, err := resolveAgentGatewayServerURL()
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://from-flag:2" {
		t.Fatalf("flag 应最高优先: %q", url)
	}
}

func TestNewEnrolledDaemonRejectsSetupKeyWhenNodeKeyExists(t *testing.T) {
	t.Setenv("MAKE_CLI_CONFIG_DIR", t.TempDir())
	setProfile(t, "work")
	if err := config.Save(config.Credentials{
		"work": {AccessToken: "access", NodeKey: "make_node_existing"},
	}); err != nil {
		t.Fatal(err)
	}

	_, err := newEnrolledDaemon(context.Background(), daemonRunConfig{
		ServerURL: "https://gateway.example.com",
		SetupKey:  "make_setup_fresh",
	}, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "daemon uninstall") {
		t.Fatalf("已有 node key 时应拒绝 setup-key 并指向 uninstall，实际: %v", err)
	}
}
