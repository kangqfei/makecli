/**
 * [INPUT]: 依赖 agent_update.go、agentCmd、agentCreateTestRoot、captureStdout、internal/api，encoding/json、net/http、net/http/httptest、strings、testing
 * [OUTPUT]: 覆盖 agent update 的插件整表替换、绑定 id 有无、省略密钥、secretFields 回执、非法输入零请求
 * [POS]: cmd/agent update 的端到端命令测试，httptest 隔离远端副作用
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/api"
)

func TestAgentUpdateRemote(t *testing.T) {
	for _, tc := range []struct {
		name, plugins, output string
		wantID                bool
	}{
		{"keep binding", `[{"id":"binding_01","pluginKey":"channel_feishu","config":{"appId":"cli"}}]`, outputTable, true},
		{"add binding", `[{"pluginKey":"mcp_make","enabled":false,"config":{}}]`, outputJSON, false},
		{"clear bindings", `[]`, outputJSON, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/make/agent/v1/agent" || r.Header.Get("X-Make-Target") != "MakeService.UpdateResource" {
					t.Errorf("unexpected routing: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer override-token" {
					t.Error("must use the global access token")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body) != 3 || body["key"] != "expense_agent" || body["appKey"] != "expense_app" || body["status"] != nil {
					t.Errorf("update must send only key, appKey and plugins: %v", body)
				}
				plugins, ok := body["plugins"].([]any)
				if !ok {
					t.Fatalf("plugins = %v", body["plugins"])
				}
				if tc.plugins == "[]" && len(plugins) != 0 || tc.plugins != "[]" && len(plugins) != 1 {
					t.Fatalf("plugins = %v", plugins)
				}
				if len(plugins) == 1 {
					item := plugins[0].(map[string]any)
					_, hasID := item["id"]
					if hasID != tc.wantID {
						t.Errorf("id present = %v, want %v", hasID, tc.wantID)
					}
					if !tc.wantID && item["enabled"] != false {
						t.Errorf("explicit false must be preserved: %v", item)
					}
					if _, leaked := item["secretFields"]; leaked {
						t.Errorf("secretFields must not be written: %v", item)
					}
					config, _ := item["config"].(map[string]any)
					if _, leaked := config["appSecret"]; leaked {
						t.Errorf("omitted writeOnly secret was sent: %v", config)
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{
					"key": "expense_agent", "appKey": "expense_app", "status": "disabled",
					"plugins": []map[string]any{{
						"id": "binding_01", "pluginKey": "channel_feishu", "enabled": true,
						"config":       map[string]any{"appId": "cli"},
						"secretFields": map[string]any{"/appSecret": map[string]any{"configured": true, "maskedValue": "abcd********wxyz"}},
					}},
				}})
			}))
			defer srv.Close()
			root := agentCreateTestRoot(t, srv.URL)
			parent, _, _ := root.Find([]string{"agent"})
			parent.AddCommand(newAgentUpdateCmd())
			root.SetArgs([]string{"agent", "update", "expense_agent", "--app", "expense_app", "--plugins-json", tc.plugins, "--output", tc.output, "--access-token", "override-token"})
			out := captureStdout(t, func() {
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
			})
			if calls != 1 {
				t.Fatalf("calls = %d, want 1", calls)
			}
			if tc.output == outputTable {
				if !strings.Contains(out, "updated successfully") || !strings.Contains(out, "expense_app") {
					t.Errorf("unexpected text output: %s", out)
				}
				return
			}
			var saved api.Agent
			if err := json.Unmarshal([]byte(out), &saved); err != nil {
				t.Fatal(err)
			}
			secret := saved.Plugins[0].SecretFields["/appSecret"]
			if saved.Key != "expense_agent" || !secret.Configured || secret.MaskedValue == nil || *secret.MaskedValue != "abcd********wxyz" {
				t.Errorf("incomplete JSON: %s", out)
			}
		})
	}
	child, _, err := agentCmd.Find([]string{"update"})
	if err != nil || child == agentCmd || child.Name() != "update" {
		t.Fatalf("real agent parent must register update: %v", err)
	}
}

func TestAgentUpdateInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing key", []string{"--app", "expense_app", "--plugins-json", "[]"}},
		{"missing plugins", []string{"expense_agent", "--app", "expense_app"}},
		{"missing app", []string{"expense_agent", "--plugins-json", "[]"}},
		{"bad plugins", []string{"expense_agent", "--app", "expense_app", "--plugins-json", "{"}},
		{"null plugins", []string{"expense_agent", "--app", "expense_app", "--plugins-json", "null"}},
		{"secret echo", []string{"expense_agent", "--app", "expense_app", "--plugins-json", `[{"pluginKey":"mcp_make","config":{},"secretFields":{"/token":{"configured":true}}}]`}},
		{"invalid key", []string{"_bad", "--app", "expense_app", "--plugins-json", "[]"}},
		{"blank app", []string{"expense_agent", "--app", " ", "--plugins-json", "[]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
			defer srv.Close()
			root := agentCreateTestRoot(t, srv.URL)
			parent, _, _ := root.Find([]string{"agent"})
			parent.AddCommand(newAgentUpdateCmd())
			root.SetArgs(append([]string{"agent", "update"}, tc.args...))
			if err := root.Execute(); err == nil {
				t.Error("expected invalid input error")
			}
			if calls != 0 {
				t.Errorf("invalid input made %d remote calls", calls)
			}
		})
	}
}

func TestAgentUpdateAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":403,"msg":"forbidden","data":{"reason":"FORBIDDEN"}}`))
	}))
	defer srv.Close()
	root := agentCreateTestRoot(t, srv.URL)
	parent, _, _ := root.Find([]string{"agent"})
	parent.AddCommand(newAgentUpdateCmd())
	root.SetArgs([]string{"agent", "update", "expense_agent", "--app", "expense_app", "--plugins-json", "[]"})
	out := captureStdout(t, func() {
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "forbidden") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	if out != "" {
		t.Errorf("failure must not print success: %s", out)
	}
}
