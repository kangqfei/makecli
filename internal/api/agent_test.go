/**
 * [INPUT]: 依赖 agent.go 和 Client，encoding/json、errors、net/http、net/http/httptest、strings、testing
 * [OUTPUT]: 覆盖 appKey 应用归属、Agent 创建、列表、详情、删除、状态更新与插件绑定替换请求、公开配置、secretFields 与概要响应、可选字段及错误回执
 * [POS]: internal/api 的 Agent 创建、列表、详情、删除与插件更新协议回归测试，httptest 隔离网络
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateAgent(t *testing.T) {
	disabled := false
	req := CreateAgentRequest{
		Key: "expense_agent", Name: "报销助手", AppKey: "expense_app",
		RuntimeKey: "make-runtime-service", ModelKey: "deepseek", Status: "disabled",
		Prompt: "按实际结果回答", Description: "报销",
		Plugins: []CreateAgentPlugin{{PluginKey: "mcp_make", Enabled: &disabled, Config: map[string]any{}}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/agent/v1/agent" {
			t.Errorf("unexpected endpoint: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("X-Make-Target") != "MakeService.CreateResource" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected routing/auth headers")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["appKey"] != "expense_app" || body["productKey"] != nil || body["runtimeKey"] != "make-runtime-service" || body["type"] != nil || body["properties"] != nil {
			t.Errorf("unexpected request body: %v", body)
		}
		plugins, ok := body["plugins"].([]any)
		if !ok || len(plugins) != 1 {
			t.Errorf("unexpected plugins: %v", body["plugins"])
		} else if plugins[0].(map[string]any)["enabled"] != false {
			t.Error("explicit false must be preserved")
		}
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"key":"expense_agent","name":"报销助手","appKey":"expense_app","runtimeKey":"make-runtime-service","modelKey":"deepseek","status":"disabled","prompt":"按实际结果回答","plugins":[{"id":"binding_01","pluginKey":"mcp_make","enabled":false,"config":{},"channelEndpoint":{"serverURL":"https://example.com/callback"}}],"meta":{"createdAt":"2026-10-12T00:00:00Z","updatedAt":"2026-10-12T00:00:00Z"}}}`))
	}))
	defer srv.Close()
	saved, err := New(srv.URL, "test-token").CreateAgent(req)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Key != req.Key || saved.AppKey != req.AppKey || len(saved.Plugins) != 1 || saved.Plugins[0].ID != "binding_01" || saved.Plugins[0].ChannelEndpoint == nil || saved.Meta.CreatedAt == "" {
		t.Errorf("unexpected response: %+v", saved)
	}
}

func TestSetAgentStatus(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		auth             bool
	}{
		{"success", `{"code":200,"data":{"key":"poc_agent","appKey":"agent_poc","status":"enabled","prompt":"unchanged","plugins":[]}}`, "", false},
		{"forbidden", `{"code":403,"msg":"forbidden","data":{"reason":"FORBIDDEN"}}`, "forbidden", false},
		{"auth", `{"code":401,"msg":"unauthorized","data":{}}`, "鉴权失败", true},
		{"gateway auth", `{"code":990300403,"msg":"expired"}`, "鉴权失败", true},
		{"wrong status", `{"code":200,"data":{"key":"poc_agent","appKey":"agent_poc","status":"disabled"}}`, "不一致", false},
		{"wrong scope", `{"code":200,"data":{"key":"poc_agent","appKey":"other_app","status":"enabled"}}`, "不一致", false},
		{"empty", `{"code":200,"data":{}}`, "不一致", false},
		{"malformed", `{`, "响应", false},
		{"wrong data", `{"code":200,"data":[]}`, "Agent 响应", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/agent/v1/agent" || r.Header.Get("X-Make-Target") != "MakeService.UpdateResource" {
					t.Errorf("unexpected routing: %s %s", r.Method, r.URL)
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body) != 3 || body["key"] != "poc_agent" || body["appKey"] != "agent_poc" || body["status"] != "enabled" {
					t.Errorf("unexpected status update: %+v", body)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			saved, err := New(srv.URL, "test-token").SetAgentStatus("poc_agent", "agent_poc", "enabled")
			if tc.want == "" {
				if err != nil || saved == nil || saved.Status != "enabled" || saved.Prompt != "unchanged" {
					t.Errorf("saved = %+v, error = %v", saved, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) || saved != nil {
				t.Fatalf("saved = %+v, error = %v, want %q", saved, err, tc.want)
			}
			if tc.auth && !errors.Is(err, ErrAuthFailed) {
				t.Errorf("auth must use ErrAuthFailed: %v", err)
			}
		})
	}
}

func TestGetAgent(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		sentinel         error
	}{
		{"success", `{"code":200,"data":{"key":"poc_agent","appKey":"agent_poc","prompt":"hello","plugins":[{"id":"binding_01","pluginKey":"mcp_make","enabled":false,"config":{}}]}}`, "", nil},
		{"not found", `{"code":404,"msg":"missing","data":{"reason":"NOT_FOUND"}}`, "", ErrNotFound},
		{"empty", `{"code":200,"data":{}}`, "", ErrNotFound},
		{"auth", `{"code":401,"msg":"unauthorized","data":{}}`, "", ErrAuthFailed},
		{"forbidden", `{"code":403,"msg":"forbidden","data":{"reason":"FORBIDDEN"}}`, "forbidden", nil},
		{"wrong data", `{"code":200,"data":[]}`, "Agent 响应", nil},
		{"malformed", `{`, "响应", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/agent/v1/agent" || r.Header.Get("X-Make-Target") != "MakeService.GetResource" {
					t.Errorf("unexpected routing: %s %s", r.Method, r.URL)
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body) != 2 || body["key"] != "poc_agent" || body["appKey"] != "agent_poc" {
					t.Errorf("unexpected body: %+v", body)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			agent, err := New(srv.URL, "test-token").GetAgent("poc_agent", "agent_poc")
			if tc.sentinel != nil {
				if !errors.Is(err, tc.sentinel) {
					t.Errorf("error = %v, want %v", err, tc.sentinel)
				}
				return
			}
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) || errors.Is(err, ErrNotFound) {
					t.Errorf("error = %v, want %q", err, tc.want)
				}
				return
			}
			if err != nil || agent == nil || agent.Prompt != "hello" || len(agent.Plugins) != 1 || agent.Plugins[0].ID != "binding_01" {
				t.Errorf("agent = %+v, err = %v", agent, err)
			}
		})
	}
}

func TestGetAgentSecretFields(t *testing.T) {
	body := `{"code":200,"data":{"key":"poc_agent","appKey":"agent_poc","prompt":"hello","plugins":[{"id":"binding_01","pluginKey":"channel_feishu","enabled":true,"config":{"appId":"cli"},"secretFields":{"/appSecret":{"configured":true,"maskedValue":"abcd********wxyz"},"/verificationToken":{"configured":false,"maskedValue":null}}}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	agent, err := New(srv.URL, "test-token").GetAgent("poc_agent", "agent_poc")
	if err != nil {
		t.Fatal(err)
	}
	fields := agent.Plugins[0].SecretFields
	secret := fields["/appSecret"]
	if !secret.Configured || secret.MaskedValue == nil || *secret.MaskedValue != "abcd********wxyz" {
		t.Errorf("configured secret = %+v", secret)
	}
	missing := fields["/verificationToken"]
	if missing.Configured || missing.MaskedValue != nil {
		t.Errorf("unconfigured secret = %+v", missing)
	}
}

func TestUpdateAgentPlugins(t *testing.T) {
	masked := "abcd********wxyz"
	response := func() string {
		raw, err := json.Marshal(map[string]any{
			"code": 200,
			"data": Agent{
				AgentSummary: AgentSummary{Key: "poc_agent", AppKey: "agent_poc", Status: "disabled"},
				Plugins: []AgentPlugin{{
					ID: "binding_01", PluginKey: "channel_feishu", Enabled: true,
					Config: map[string]any{"appId": "cli"},
					SecretFields: map[string]AgentPluginSecretField{
						"/appSecret": {Configured: true, MaskedValue: &masked},
					},
				}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}()
	for _, tc := range []struct {
		name    string
		plugins *[]PluginBindingInput
		wantID  bool
	}{
		{"with binding id", &[]PluginBindingInput{{ID: "binding_01", PluginKey: "channel_feishu", Config: map[string]any{"appId": "cli"}}}, true},
		{"without binding id", &[]PluginBindingInput{{PluginKey: "mcp_make", Config: map[string]any{}}}, false},
		{"empty table", &[]PluginBindingInput{}, false},
		{"omit plugins", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/agent/v1/agent" || r.Header.Get("X-Make-Target") != "MakeService.UpdateResource" {
					t.Errorf("unexpected routing: %s %s", r.Method, r.URL)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["key"] != "poc_agent" || body["appKey"] != "agent_poc" || body["status"] != nil || body["secretFields"] != nil {
					t.Errorf("unexpected agent fields: %v", body)
				}
				plugins, exists := body["plugins"]
				switch {
				case tc.plugins == nil:
					if exists {
						t.Errorf("omitted plugins were sent: %v", plugins)
					}
				case !exists:
					t.Error("plugins array was omitted")
				default:
					items, ok := plugins.([]any)
					if !ok || len(items) != len(*tc.plugins) {
						t.Fatalf("plugins = %v", plugins)
					}
					if len(items) == 1 {
						item := items[0].(map[string]any)
						_, hasID := item["id"]
						if hasID != tc.wantID {
							t.Errorf("id present = %v, want %v (%v)", hasID, tc.wantID, item)
						}
						if tc.wantID && item["id"] != "binding_01" {
							t.Errorf("id = %v", item["id"])
						}
						if _, hasSecret := item["secretFields"]; hasSecret {
							t.Errorf("write payload must not echo secretFields: %v", item)
						}
						config := item["config"].(map[string]any)
						if _, leaked := config["appSecret"]; leaked {
							t.Errorf("omitted writeOnly secret was sent: %v", config)
						}
					}
				}
				_, _ = w.Write([]byte(response))
			}))
			defer srv.Close()
			saved, err := New(srv.URL, "test-token").UpdateAgent(UpdateAgentRequest{
				Key: "poc_agent", AppKey: "agent_poc", Plugins: tc.plugins,
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "empty table" {
				return
			}
			secret := saved.Plugins[0].SecretFields["/appSecret"]
			if !secret.Configured || secret.MaskedValue == nil || *secret.MaskedValue != masked {
				t.Errorf("saved secret = %+v", secret)
			}
		})
	}
}

func TestUpdateAgentErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		auth             bool
	}{
		{"forbidden", `{"code":403,"msg":"forbidden","data":{"reason":"FORBIDDEN"}}`, "forbidden", false},
		{"auth", `{"code":401,"msg":"unauthorized","data":{}}`, "鉴权失败", true},
		{"gateway auth", `{"code":990300403,"msg":"expired"}`, "鉴权失败", true},
		{"wrong scope", `{"code":200,"data":{"key":"poc_agent","appKey":"other_app"}}`, "不一致", false},
		{"empty", `{"code":200,"data":{}}`, "不一致", false},
		{"malformed", `{`, "响应", false},
		{"wrong data", `{"code":200,"data":[]}`, "Agent 响应", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			saved, err := New(srv.URL, "test-token").UpdateAgent(UpdateAgentRequest{Key: "poc_agent", AppKey: "agent_poc"})
			if err == nil || !strings.Contains(err.Error(), tc.want) || saved != nil {
				t.Fatalf("saved = %+v, error = %v, want %q", saved, err, tc.want)
			}
			if tc.auth && !errors.Is(err, ErrAuthFailed) {
				t.Errorf("auth must use ErrAuthFailed: %v", err)
			}
		})
	}
}

func TestDeleteAgent(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"success", `{"code":200,"msg":"success","data":{}}`, "", 200},
		{"not found", `{"code":404,"msg":"agent not found","data":{"reason":"NOT_FOUND"}}`, "agent not found", 404},
		{"forbidden", `{"code":403,"msg":"forbidden","data":{"reason":"FORBIDDEN"}}`, "forbidden", 403},
		{"auth", `{"code":401,"msg":"unauthorized","data":{}}`, "鉴权失败", 200},
		{"gateway auth", `{"code":990300403,"msg":"expired"}`, "鉴权失败", 200},
		{"malformed", `{`, "响应", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/agent/v1/agent" || r.Header.Get("X-Make-Target") != "MakeService.DeleteResource" {
					t.Errorf("unexpected routing: %s %s", r.Method, r.URL)
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body) != 2 || body["key"] != "invoice_agent" || body["appKey"] != "invoice_app" {
					t.Errorf("unexpected body: %+v", body)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			err := New(srv.URL, "test-token").DeleteAgent("invoice_agent", "invoice_app")
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if strings.Contains(tc.name, "auth") && !errors.Is(err, ErrAuthFailed) {
				t.Errorf("authentication error must use ErrAuthFailed: %v", err)
			}
		})
	}
}

func TestCreateAgentOptionalFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		for _, key := range []string{"description", "prompt", "runtimeKey", "status", "plugins"} {
			if _, exists := body[key]; exists {
				t.Errorf("optional %s should be omitted", key)
			}
		}
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":{"key":"demo"}}`))
	}))
	defer srv.Close()
	_, err := New(srv.URL, "test-token").CreateAgent(CreateAgentRequest{Key: "demo", Name: "demo", AppKey: "expense_app", ModelKey: "deepseek"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateAgentErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"conflict", `{"code":409,"msg":"Agent already exists","data":{"reason":"KEY_CONFLICT"}}`, "Agent already exists", 409},
		{"auth", `{"code":401,"msg":"unauthorized","data":{"reason":"UNAUTHORIZED"}}`, "鉴权失败", 401},
		{"forbidden", `{"code":403,"msg":"forbidden","data":{"reason":"FORBIDDEN"}}`, "forbidden", 403},
		{"bad json", `{`, "响应", 200},
		{"null data", `{"code":200,"data":null}`, "缺少 key", 200},
		{"wrong data", `{"code":200,"data":[]}`, "响应", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := New(srv.URL, "test-token").CreateAgent(CreateAgentRequest{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
			if tc.name == "auth" && !errors.Is(err, ErrAuthFailed) {
				t.Errorf("401 must use ErrAuthFailed: %v", err)
			}
		})
	}
}

func TestListAgents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/agent/v1/agent" || r.Header.Get("X-Make-Target") != "MakeService.ListResources" {
			t.Errorf("unexpected list routing: %s %s", r.Method, r.URL)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["appKey"] != nil || body["filter"].(map[string]any)["expression"] != "appKey == 'expense_app'" {
			t.Errorf("unexpected filter: %v", body)
		}
		pagination := body["pagination"].(map[string]any)
		if pagination["page"] != float64(2) || pagination["size"] != float64(20) {
			t.Errorf("unexpected pagination: %v", pagination)
		}
		_, _ = w.Write([]byte(`{"code":200,"msg":"success","data":[{"key":"expense_agent","name":"报销助手","appKey":"expense_app","description":"报销查询","runtimeKey":"make-runtime-service","modelKey":"deepseek","status":"disabled","meta":{"createdAt":"2026-10-13T00:00:00Z","updatedAt":"2026-10-13T00:00:00Z"}}],"pagination":{"page":2,"size":20,"total":42}}`))
	}))
	defer srv.Close()
	agents, total, err := New(srv.URL, "test-token").ListAgents(2, 20, "appKey == 'expense_app'")
	if err != nil {
		t.Fatal(err)
	}
	if total != 42 || len(agents) != 1 || agents[0].AppKey != "expense_app" || agents[0].Meta.UpdatedAt == "" || agents[0].Description != "报销查询" {
		t.Errorf("unexpected summaries: %+v, total %d", agents, total)
	}
}

func TestListAgentsResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"empty", `{"code":200,"data":[],"pagination":{"total":0}}`, "", 200},
		{"null", `{"code":200,"data":null,"pagination":{"total":0}}`, "", 200},
		{"business error", `{"code":400,"msg":"application scope required","data":{"reason":"INVALID_FILTER"}}`, "application scope required", 200},
		{"http error", `{"code":403,"msg":"forbidden","data":{"reason":"FORBIDDEN"}}`, "forbidden", 403},
		{"auth", `{"code":401,"msg":"unauthorized","data":{"reason":"UNAUTHORIZED"}}`, "鉴权失败", 200},
		{"malformed", `{`, "响应", 200},
		{"wrong data", `{"code":200,"data":{}}`, "Agent 列表响应", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			agents, total, err := New(srv.URL, "test-token").ListAgents(1, 20, "appKey == 'demo'")
			if tc.want == "" {
				if err != nil || agents == nil || len(agents) != 0 || total != 0 {
					t.Fatalf("empty list = %+v, %d, %v", agents, total, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
			if tc.name == "auth" && !errors.Is(err, ErrAuthFailed) {
				t.Errorf("401 must use ErrAuthFailed: %v", err)
			}
		})
	}
}
