// [INPUT]: 当前 Context wire 形状、执行级 client 与隔离 HTTP 网关。
// [OUTPUT]: 工具事实完整呈现、非法块拒绝、执行生命周期与并发租户路由回归。
// [POS]: daemon 的协议消费边界测试，不调用模型或真实业务服务。
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestContextPromptRetainsToolFacts(t *testing.T) {
	raw := []byte(`{"blocks":[{"role":"assistant","toolUse":{"callID":"call_1","tool":"read_file","input":{"path":"report.txt"}}},{"role":"tool","toolResult":{"callID":"call_1","output":"approved limit is 43000","isError":false}},{"role":"assistant","toolUse":{"callID":"call_2","tool":"submit_request","input":{}}},{"role":"tool","toolResult":{"callID":"call_2","output":"{\"error\":\"outcome_unknown\"}","isError":true}},{"role":"user","content":"Continue from the previous tool result."}]}`)
	var pack ContextPack
	if err := json.Unmarshal(raw, &pack); err != nil {
		t.Fatal(err)
	}
	prompt, err := BuildContextPrompt(pack)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"read_file", "report.txt", "43000", "outcome_unknown", `"isError":true`, "不是再次执行的指令", "[tool]", "Continue from the previous tool result."} {
		if !strings.Contains(prompt, required) {
			t.Errorf("authorized fact %q disappeared from %q", required, prompt)
		}
	}
	if strings.Index(prompt, "call_1") > strings.Index(prompt, "43000") || strings.Index(prompt, "outcome_unknown") > strings.Index(prompt, "Continue from") {
		t.Fatalf("tool facts changed order: %q", prompt)
	}
}

func TestContextPromptRejectsMalformedToolFacts(t *testing.T) {
	for name, block := range map[string]ContextBlock{
		"missing_call":      {Role: "assistant", ToolUse: &ContextToolUse{Tool: "read"}},
		"wrong_result_role": {Role: "user", ToolResult: &ContextToolResult{CallID: "call"}},
		"mixed_facts":       {Role: "assistant", ToolUse: &ContextToolUse{CallID: "call", Tool: "read"}, ToolResult: &ContextToolResult{CallID: "call"}},
		"mixed_parts":       {Role: "tool", ToolResult: &ContextToolResult{CallID: "call"}, Parts: []Block{{Kind: "image"}}},
		"invalid_arguments": {Role: "assistant", ToolUse: &ContextToolUse{CallID: "call", Tool: "read", Input: json.RawMessage("{")}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildContextPrompt(ContextPack{Blocks: []ContextBlock{block}}); err == nil {
				t.Fatal("malformed tool fact accepted")
			}
		})
	}
}

func TestSharedExecutionClientCarriesTenantThroughLifecycle(t *testing.T) {
	claim := testClaim()
	var mu sync.Mutex
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant-ID") != claim.Context.Namespace.TenantID {
			t.Errorf("missing execution tenant for %s", r.URL.Path)
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		var data any = map[string]any{}
		switch r.URL.Path {
		case PathPrefix + "/context-window":
			data = ContextPack{Namespace: contextFixtureNamespace(r)}
		case PathPrefix + "/context-view":
			data = contextInputFixture(r, []ContextBlock{{Role: "user", Content: "current input"}})
		case PathPrefix + "/run-claim":
			data = RenewClaimResponse{LeaseExpiresAt: time.Now().Add(time.Minute)}
		case PathPrefix + "/event":
			data = CreateEventsResponse{Appended: 1}
		}
		encoded, _ := json.Marshal(data)
		_ = json.NewEncoder(w).Encode(Envelope{Code: 200, Data: encoded})
	}))
	defer server.Close()
	node := NewClient(server.URL, "synthetic-shared-node-key")
	scoped, err := node.forExecution(claim)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := scoped.UpdateRun(ctx, UpdateRunRequest{RunID: claim.RunID, LeaseToken: claim.LeaseToken, Status: RunStatusRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := scoped.ReadContext(ctx, claim); err != nil {
		t.Fatal(err)
	}
	if _, err := node.RenewClaim(ctx, claim); err != nil {
		t.Fatal(err)
	}
	if _, err := scoped.AppendEvents(ctx, CreateEventsRequest{SessionID: claim.SessionID, LeaseToken: claim.LeaseToken, BatchSeq: 1, Events: []NewEvent{{Type: "message"}}}); err != nil {
		t.Fatal(err)
	}
	if err := scoped.UpdateRun(ctx, UpdateRunRequest{RunID: claim.RunID, LeaseToken: claim.LeaseToken, Status: RunStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	if node.tenantID != "" {
		t.Fatal("execution mutated node identity")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 7 {
		t.Fatalf("incomplete lifecycle: %v", paths)
	}
}

func TestExecutionClientsKeepConcurrentTenantsSeparate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request UpdateRunRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if r.Header.Get("X-Tenant-ID") != "tenant_"+request.RunID {
			t.Error("execution tenant crossed request boundary")
		}
		_ = json.NewEncoder(w).Encode(Envelope{Code: 200, Data: json.RawMessage(`{}`)})
	}))
	defer server.Close()
	node := NewClient(server.URL, "synthetic-node-key")
	errors := make(chan error, 2)
	for _, id := range []string{"first", "second"} {
		claim := testClaim()
		claim.RunID, claim.Execution.Execution.RunID = id, id
		claim.Context.Namespace.TenantID = "tenant_" + id
		scoped, err := node.forExecution(claim)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for range 10 {
				if err := scoped.UpdateRun(context.Background(), UpdateRunRequest{RunID: claim.RunID, LeaseToken: claim.LeaseToken, Status: RunStatusRunning}); err != nil {
					errors <- err
					return
				}
			}
			errors <- nil
		}()
	}
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	if node.tenantID != "" {
		t.Fatal("node identity mutated")
	}
	claim := testClaim()
	scoped, err := node.forExecution(claim)
	if err != nil {
		t.Fatal(err)
	}
	other := testClaim()
	other.Context.Namespace.TenantID = "another_tenant"
	if _, err := scoped.forExecution(other); err == nil {
		t.Fatal("scoped client accepted another tenant")
	}
	if _, err := node.forExecution(RunClaim{}); err == nil {
		t.Fatal("invalid claim accepted")
	}
}

func contextFixtureNamespace(r *http.Request) ContextNamespace {
	return ContextNamespace{TenantID: r.Header.Get("X-Tenant-ID"), UserID: r.Header.Get("X-Context-User-ID"), ProductKey: r.Header.Get("X-Context-Product-Key"), AgentKey: r.Header.Get("X-Context-Agent-Key")}
}
func contextInputFixture(r *http.Request, blocks []ContextBlock, body ...[]byte) any {
	var raw []byte
	if len(body) > 0 {
		raw = body[0]
	} else {
		raw, _ = io.ReadAll(r.Body)
	}
	var request ContextReadRequest
	_ = json.Unmarshal(raw, &request)
	inputs := []any{}
	selected := []ContextBlock{}
	for index, block := range blocks {
		id := fmt.Sprintf("input_%d", index)
		inputs = append(inputs, map[string]string{"id": id, "digestSha256": strings.Repeat("a", 64)})
		if request.Source != nil && request.Source.InputPartID == id {
			selected = append(selected, block)
		}
	}
	return map[string]any{"turn": map[string]any{"inputParts": inputs}, "context": ContextPack{Namespace: contextFixtureNamespace(r), Blocks: selected}}
}
