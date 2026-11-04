/**
 * [INPUT]: 依赖 agent_info.go、agentCmd、agentCreateTestRoot、captureStdout、internal/api，encoding/json、errors、net/http、net/http/httptest、strings、testing
 * [OUTPUT]: 覆盖 Agent 详情查询、参数顺序、文本与完整 JSON、空插件及失败无输出
 * [POS]: cmd/agent info 的命令集成测试，httptest 隔离真实后端
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/api"
)

const agentInfoTestBody = `{"code":200,"data":{"key":"poc_agent","name":"POC Agent","appKey":"agent_poc","description":"demo agent","modelKey":"deepseek","runtimeKey":"make-runtime-service","status":"disabled","prompt":"first line\nsecond line","meta":{"createdAt":"2026-10-13T00:00:00Z","updatedAt":"2026-10-14T00:00:00Z"},"plugins":[{"id":"binding_01","pluginKey":"mcp_make","enabled":false,"config":{"mode":"read"},"channelEndpoint":{"serverURL":"https://example.com/callback"},"secretFields":{"/token":{"configured":true,"maskedValue":"abcd********wxyz"},"/optional":{"configured":false,"maskedValue":null}}}]}}`

func TestAgentInfoRemote(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		args         []string
	}{
		{"parent flag", outputTable, []string{"agent", "--app", "agent_poc", "info", "poc_agent"}},
		{"child flag", outputJSON, []string{"agent", "info", "poc_agent", "--app", "agent_poc"}},
		{"auto in pipe", outputAuto, []string{"agent", "info", "--app", "agent_poc", "poc_agent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/make/agent/v1/agent" || r.Header.Get("X-Make-Target") != "MakeService.GetResource" {
					t.Errorf("unexpected routing: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer override-token" {
					t.Error("info must use the global access token")
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body) != 2 || body["key"] != "poc_agent" || body["appKey"] != "agent_poc" {
					t.Errorf("unexpected body: %+v", body)
				}
				_, _ = w.Write([]byte(agentInfoTestBody))
			}))
			defer srv.Close()
			root := agentCreateTestRoot(t, srv.URL)
			parent, _, _ := root.Find([]string{"agent"})
			parent.AddCommand(newAgentInfoCmd())
			root.SetArgs(append(tc.args, "--output", tc.output, "--access-token", "override-token"))
			out := captureStdout(t, func() {
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
			})
			if calls != 1 {
				t.Fatalf("calls = %d, want 1", calls)
			}
			if tc.output == outputTable {
				for _, want := range []string{"Key:          poc_agent", "Name:         POC Agent", "demo agent", "App:          agent_poc", "deepseek", "make-runtime-service", "disabled", "2026-10-13", "2026-10-14", "first line\nsecond line", "mcp_make", "binding_01", "false", `{"mode":"read"}`, "https://example.com/callback", "/optional unconfigured", "/token configured abcd********wxyz"} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %q in output: %s", want, out)
					}
				}
				return
			}
			var result api.Agent
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			secret := result.Plugins[0].SecretFields["/token"]
			missing := result.Plugins[0].SecretFields["/optional"]
			if result.Key != "poc_agent" || result.Prompt != "first line\nsecond line" || len(result.Plugins) != 1 || result.Plugins[0].Config["mode"] != "read" || result.Plugins[0].Enabled || result.Plugins[0].ChannelEndpoint == nil || !secret.Configured || secret.MaskedValue == nil || *secret.MaskedValue != "abcd********wxyz" || missing.Configured || missing.MaskedValue != nil {
				t.Errorf("incomplete JSON: %s", out)
			}
		})
	}
	child, _, err := agentCmd.Find([]string{"info"})
	if err != nil || child == agentCmd || child.Short != "Show agent details on Make app" {
		t.Fatalf("info registration/help mismatch: %v", err)
	}
}

func TestAgentInfoInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{}, {"poc_agent"}, {"poc_agent", "--app", ""}, {"poc_agent", "--app", " "},
		{"poc_agent", "--app", strings.Repeat("a", 129)}, {"_invalid", "--app", "demo"},
		{"poc_agent", "extra", "--app", "demo"}, {"poc_agent", "--app", "demo", "--output", "yaml"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
			defer srv.Close()
			root := agentCreateTestRoot(t, srv.URL)
			parent, _, _ := root.Find([]string{"agent"})
			parent.AddCommand(newAgentInfoCmd())
			root.SetArgs(append([]string{"agent", "info"}, args...))
			if err := root.Execute(); err == nil {
				t.Error("expected invalid input error")
			}
			if calls != 0 {
				t.Errorf("invalid input made %d calls", calls)
			}
		})
	}
}

func TestAgentInfoNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":404,"msg":"agent not found","data":{"reason":"NOT_FOUND"}}`))
	}))
	defer srv.Close()
	agentCreateTestRoot(t, srv.URL)
	out := captureStdout(t, func() {
		if err := runAgentInfo("poc_agent", "agent_poc", outputJSON); !errors.Is(err, api.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound", err)
		}
	})
	if out != "" {
		t.Errorf("failure printed a success result: %s", out)
	}
}

func TestRenderAgentInfoEmptyPlugins(t *testing.T) {
	out := captureStdout(t, func() {
		if err := renderAgentInfo(&api.Agent{}); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "Plugins:      -\n") {
		t.Errorf("missing empty plugin placeholder: %s", out)
	}
}
