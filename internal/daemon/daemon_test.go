/**
 * [INPUT]: 依赖 daemon.go/run.go/client.go/execenv.go 与 adapter 契约；net/http/httptest 模拟 gateway
 * [OUTPUT]: 授权窗口消费、事件批次确认与完成/取消/失败的执行编排回归
 * [POS]: internal/daemon 的测试面——对 gateway 打桩测编排，不依赖真实 CLI 与网络
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qfeius/makecli/internal/daemon/adapter"
)

// fakeGateway 按 X-Make-Target 记录调用并回预设响应。
type fakeGateway struct {
	mu     sync.Mutex
	calls  []string
	bodies map[string][]byte
	server *httptest.Server
	blocks []ContextBlock
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	fake := &fakeGateway{bodies: map[string][]byte{}}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get(TargetHeader)
		body := make([]byte, 0)
		if r.Body != nil {
			buffer := make([]byte, 64*1024)
			n, _ := r.Body.Read(buffer)
			body = buffer[:n]
		}
		key := r.URL.Path + "|" + target
		fake.mu.Lock()
		fake.calls = append(fake.calls, key)
		fake.bodies[key] = body
		fake.mu.Unlock()

		var data any = map[string]any{}
		if r.URL.Path == PathPrefix+"/context-window" {
			if r.Header.Get("X-Context-Execution-ID") == "" || r.Header.Get("X-Context-Lease-Token") == "" {
				t.Error("window request missing execution authorization")
			}
			data = ContextPack{Blocks: fake.blocks}
		}
		if r.URL.Path == PathPrefix+"/"+ResourceEvent && target == TargetCreateResource {
			var request CreateEventsRequest
			if err := json.Unmarshal(body, &request); err != nil {
				t.Error(err)
			}
			data = CreateEventsResponse{Appended: len(request.Events)}
		}
		dataJSON, _ := json.Marshal(data)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Envelope{Code: 200, Msg: "ok", Data: dataJSON})
	}))
	return fake
}

func (f *fakeGateway) targets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// stubBackend 返回脚本化的执行流。
type stubBackend struct {
	messages []adapter.Message
	result   adapter.Result
	gotOpts  adapter.ExecOptions
	gotText  string
}

func (b *stubBackend) Provider() string                       { return "claude-code" }
func (b *stubBackend) Detect(context.Context) (string, error) { return "stub-1.0", nil }
func (b *stubBackend) Execute(_ context.Context, prompt string, opts adapter.ExecOptions) (*adapter.Session, error) {
	b.gotOpts = opts
	b.gotText = prompt
	messages := make(chan adapter.Message, len(b.messages))
	results := make(chan adapter.Result, 1)
	for _, message := range b.messages {
		messages <- message
	}
	close(messages)
	results <- b.result
	close(results)
	return &adapter.Session{Messages: messages, Result: results}, nil
}

func testClaim() RunClaim {
	claim := RunClaim{
		RunID: "run_1", SessionID: "session_1", LeaseToken: "lease_1",
		Execution: &ExecutionClaim{},
		Context:   &ContextExecution{Namespace: ContextNamespace{TenantID: "tenant", UserID: "agent_account", ProductKey: "product", AgentKey: "agent"}, SessionID: "session_1", TurnID: "turn_1"},
		Agent:     AgentBundle{Name: "测试", Instructions: "你是测试 agent"},
		Trigger:   SeqRange{FromSeq: 0, ToSeq: 1},
	}
	claim.Execution.Execution.ID = "execution_1"
	claim.Execution.Execution.RunID = claim.RunID
	claim.Execution.Lease.ExecutionID = "execution_1"
	claim.Execution.Lease.WorkerID = "worker"
	claim.Execution.Lease.Generation = 1
	claim.Execution.Lease.Token = claim.LeaseToken
	return claim
}

func newTestDaemon(t *testing.T, gatewayURL string) *Daemon {
	t.Helper()
	return &Daemon{
		client:         NewClient(gatewayURL, "token"),
		backends:       map[string]adapter.Backend{},
		workBaseDir:    t.TempDir(),
		maxRunDuration: time.Minute,
		logger:         slog.New(slog.NewTextHandler(testWriter{t}, nil)),
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Log(string(p)); return len(p), nil }

func TestNewSetupKeyEnrollmentHasNoBearer(t *testing.T) {
	var gotAuthorization string
	var gotRequest CreateRuntimeRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		if r.URL.Path != PathPrefix+"/"+ResourceRuntime || r.Header.Get(TargetHeader) != TargetCreateResource {
			t.Errorf("unexpected enrollment request: path=%s target=%s", r.URL.Path, r.Header.Get(TargetHeader))
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Errorf("decode enrollment request: %v", err)
		}
		data, _ := json.Marshal(CreateRuntimeResponse{RuntimeID: "runtime_new", NodeKey: "make_node_new"})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Envelope{Code: 200, Msg: "ok", Data: data})
	}))
	defer server.Close()

	agentDaemon, err := New(context.Background(), Options{
		ServerURL: server.URL,
		SetupKey:  "make_setup_fresh",
		Backends:  []adapter.Backend{&stubBackend{}},
		Logger:    slog.New(slog.NewTextHandler(testWriter{t}, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuthorization != "" {
		t.Fatalf("重新入册携带了旧 Authorization: %q", gotAuthorization)
	}
	if gotRequest.SetupKey != "make_setup_fresh" {
		t.Fatalf("setup key = %q, want fresh setup key", gotRequest.SetupKey)
	}
	if agentDaemon.NodeKey() != "make_node_new" {
		t.Fatalf("node key = %q, want enrollment response key", agentDaemon.NodeKey())
	}
}

func TestNewRejectsNodeKeyAndSetupKeyTogether(t *testing.T) {
	_, err := New(context.Background(), Options{
		ServerURL: "https://gateway.example.com",
		NodeKey:   "make_node_existing",
		SetupKey:  "make_setup_fresh",
	})
	if err == nil || !strings.Contains(err.Error(), "不能同时使用") {
		t.Fatalf("同时提供两种凭证应在任何探测或网络请求前拒绝，实际: %v", err)
	}
}

func TestExecuteRunHappyPath(t *testing.T) {
	gateway := newFakeGateway(t)
	defer gateway.server.Close()
	gateway.blocks = []ContextBlock{{Role: "user", Content: "第一条"}, {Role: "user", Content: "第二条"}}
	backend := &stubBackend{
		messages: []adapter.Message{
			{Type: adapter.MessageThinking, Text: "想"},
			{Type: adapter.MessageToolUse, Tool: "Bash", CallID: "c1"},
			{Type: adapter.MessageToolResult, CallID: "c1", Output: "ok"},
		},
		result: adapter.Result{Text: "最终答复", CLISessionID: "cli_new"},
	}
	daemonUnderTest := newTestDaemon(t, gateway.server.URL)

	var cancelled atomic.Bool
	daemonUnderTest.executeRun(context.Background(), backend, testClaim(), &cancelled)

	if backend.gotText != "[user]\n第一条\n\n[user]\n第二条" {
		t.Fatalf("prompt = %q, want 触发区间合并", backend.gotText)
	}
	if backend.gotOpts.WorkDir == "" {
		t.Fatal("应准备工作目录")
	}
	runUpdateKey := PathPrefix + "/" + ResourceRun + "|" + TargetUpdateResource
	eventListKey := PathPrefix + "/context-window|" + TargetCreateResource
	eventCreateKey := PathPrefix + "/" + ResourceEvent + "|" + TargetCreateResource
	targets := gateway.targets()
	// start(update) → list → append(执行事件+最终 message) → complete(update)
	if targets[0] != runUpdateKey || targets[1] != eventListKey || targets[len(targets)-1] != runUpdateKey {
		t.Fatalf("targets = %v", targets)
	}
	var complete UpdateRunRequest
	_ = json.Unmarshal(gateway.bodies[runUpdateKey], &complete)
	if complete.Status != RunStatusCompleted || backend.gotOpts.ResumeSessionID != "" {
		t.Fatalf("complete = %+v, execution must not persist cross-run continuity", complete)
	}
	var appended CreateEventsRequest
	_ = json.Unmarshal(gateway.bodies[eventCreateKey], &appended)
	hasMessage := false
	for _, event := range appended.Events {
		if event.Type == "message" {
			hasMessage = true
		}
	}
	if !hasMessage {
		t.Fatalf("最终答复应作为 message 事件上报: %+v", appended.Events)
	}
}

func TestExecuteRunFailureReportsCLICrash(t *testing.T) {
	gateway := newFakeGateway(t)
	defer gateway.server.Close()
	gateway.blocks = []ContextBlock{{Role: "user", Content: "hi"}}
	backend := &stubBackend{result: adapter.Result{IsError: true, ErrorMessage: "boom"}}
	daemonUnderTest := newTestDaemon(t, gateway.server.URL)

	var cancelled atomic.Bool
	daemonUnderTest.executeRun(context.Background(), backend, testClaim(), &cancelled)

	runUpdateKey := PathPrefix + "/" + ResourceRun + "|" + TargetUpdateResource
	var fail UpdateRunRequest
	_ = json.Unmarshal(gateway.bodies[runUpdateKey], &fail)
	if fail.Status != RunStatusFailed || fail.FailureReason != FailReasonCLICrash {
		t.Fatalf("fail = %+v, want status=failed reason=cli_crash", fail)
	}
}

func TestExecuteRunCancelledFinalizesAsCancelled(t *testing.T) {
	gateway := newFakeGateway(t)
	defer gateway.server.Close()
	gateway.blocks = []ContextBlock{{Role: "user", Content: "hi"}}
	backend := &stubBackend{result: adapter.Result{IsError: true, ErrorMessage: "被杀"}}
	daemonUnderTest := newTestDaemon(t, gateway.server.URL)

	var cancelled atomic.Bool
	cancelled.Store(true) // 心跳 actions 已置位
	daemonUnderTest.executeRun(context.Background(), backend, testClaim(), &cancelled)

	runUpdateKey := PathPrefix + "/" + ResourceRun + "|" + TargetUpdateResource
	var fail UpdateRunRequest
	_ = json.Unmarshal(gateway.bodies[runUpdateKey], &fail)
	if fail.Status != RunStatusCancelled {
		t.Fatalf("fail = %+v, want status=cancelled（取消收尾优先于错误原因）", fail)
	}
}
