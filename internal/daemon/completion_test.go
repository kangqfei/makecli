// [INPUT]: 真实 ClaudeCode adapter、隔离子进程及可控 HTTP 租约/持久化回执。
// [OUTPUT]: result 与取消竞争、空输出、持久化失败和租户路由的完整执行回归。
// [POS]: daemon 成功必须有持久化输出且执行仍有效；测试不使用模型或宿主凭据。
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qfeius/makecli/internal/daemon/adapter"
)

const testCLIMode = "MAKECLI_TEST_RESULT_BEFORE_EXIT"

func TestMain(m *testing.M) {
	if os.Getenv(testCLIMode) == "1" {
		fmt.Println(`{"type":"result","result":"already generated final answer","is_error":false}`)
		// 模拟已输出 result、尚未 EOF 的 CLI；由真实 adapter 的取消关闭进程。
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(m.Run())
}

type completionGateway struct {
	server        *httptest.Server
	mu            sync.Mutex
	statuses      []UpdateRunRequest
	batches       []CreateEventsRequest
	renewalDenied bool
	appendMode    string
	afterAppend   func()
}

func newCompletionGateway(t *testing.T, renewalDenied bool, appendMode string, afterAppend func()) *completionGateway {
	t.Helper()
	gateway := &completionGateway{renewalDenied: renewalDenied, appendMode: appendMode, afterAppend: afterAppend}
	gateway.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant-ID") != "tenant" {
			t.Errorf("missing tenant for lifecycle request %s", r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(Envelope{Code: 401})
			return
		}
		var data any = map[string]any{}
		switch r.URL.Path {
		case PathPrefix + "/run":
			var request UpdateRunRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			gateway.mu.Lock()
			gateway.statuses = append(gateway.statuses, request)
			gateway.mu.Unlock()
		case PathPrefix + "/context-window":
			data = ContextPack{Namespace: contextFixtureNamespace(r)}
		case PathPrefix + "/context-view":
			data = contextInputFixture(r, []ContextBlock{{Role: "user", Content: "Please produce a report."}})
		case PathPrefix + "/run-claim":
			if gateway.renewalDenied {
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(Envelope{Code: 503, Msg: "temporary renewal failure"})
				return
			}
			data = RenewClaimResponse{LeaseExpiresAt: time.Now().Add(time.Minute)}
		case PathPrefix + "/event":
			var request CreateEventsRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			gateway.mu.Lock()
			gateway.batches = append(gateway.batches, request)
			gateway.mu.Unlock()
			switch gateway.appendMode {
			case "denied":
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(Envelope{Code: 503})
				return
			case "missing":
				data = CreateEventsResponse{}
			case "duplicate":
				data = CreateEventsResponse{Duplicate: true}
			default:
				data = CreateEventsResponse{Appended: len(request.Events)}
			}
			if gateway.afterAppend != nil {
				gateway.afterAppend()
			}
		}
		raw, _ := json.Marshal(data)
		_ = json.NewEncoder(w).Encode(Envelope{Code: 200, Data: raw})
	}))
	t.Cleanup(gateway.server.Close)
	return gateway
}

func (g *completionGateway) outcome(t *testing.T) (UpdateRunRequest, int) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.statuses) == 0 {
		t.Fatal("no lifecycle requests observed")
	}
	return g.statuses[len(g.statuses)-1], len(g.batches)
}

func TestLeaseLossAfterCLIResultCannotComplete(t *testing.T) {
	t.Setenv(testCLIMode, "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	gateway := newCompletionGateway(t, true, "", nil)
	d := newTestDaemon(t, gateway.server.URL)
	claim := testClaim()
	claim.LeaseSeconds = 1
	var cancelled atomic.Bool
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	backend := &observedResultBackend{Backend: &adapter.ClaudeCode{ExecutablePath: executable}, result: make(chan adapter.Result, 1)}
	d.executeRun(ctx, backend, claim, &cancelled)
	select {
	case result := <-backend.result:
		if result.IsError || result.Text != "already generated final answer" {
			t.Fatalf("fixture did not return a buffered CLI success: %+v", result)
		}
	default:
		t.Fatal("real CLI adapter did not produce a result")
	}
	final, batches := gateway.outcome(t)
	if final.Status != RunStatusFailed || final.FailureReason != FailReasonCLICrash || batches != 0 {
		t.Fatalf("lease loss after successful result: final=%+v batches=%d", final, batches)
	}
}

type observedResultBackend struct {
	adapter.Backend
	result chan adapter.Result
}

func (b *observedResultBackend) Execute(ctx context.Context, prompt string, options adapter.ExecOptions) (*adapter.Session, error) {
	session, err := b.Backend.Execute(ctx, prompt, options)
	if err != nil {
		return nil, err
	}
	results := make(chan adapter.Result, 1)
	go func() {
		defer close(results)
		if result, ok := <-session.Result; ok {
			b.result <- result
			results <- result
		}
	}()
	return &adapter.Session{Messages: session.Messages, Result: results}, nil
}

func TestCompletionRequiresPersistedFinalOutput(t *testing.T) {
	tests := []struct {
		name, mode, text, want string
		cancelAfterAppend      bool
	}{
		{name: "saved", text: "answer", want: RunStatusCompleted},
		{name: "duplicate_receipt", mode: "duplicate", text: "answer", want: RunStatusCompleted},
		{name: "append_denied", mode: "denied", text: "answer", want: RunStatusFailed},
		{name: "missing_receipt", mode: "missing", text: "answer", want: RunStatusFailed},
		{name: "empty_result", want: RunStatusFailed},
		{name: "stopped_after_append", text: "answer", want: RunStatusFailed, cancelAfterAppend: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var afterAppend func()
			if test.cancelAfterAppend {
				afterAppend = cancel
			}
			gateway := newCompletionGateway(t, false, test.mode, afterAppend)
			d := newTestDaemon(t, gateway.server.URL)
			var cancelled atomic.Bool
			d.executeRun(ctx, &stubBackend{result: adapter.Result{Text: test.text}}, testClaim(), &cancelled)
			final, batches := gateway.outcome(t)
			if final.Status != test.want {
				t.Fatalf("final=%+v, want %s", final, test.want)
			}
			if test.want == RunStatusCompleted && batches != 1 {
				t.Fatalf("completed without one final batch: %d", batches)
			}
		})
	}
}

type successOnStopBackend struct{ stubBackend }

func (*successOnStopBackend) Execute(ctx context.Context, _ string, _ adapter.ExecOptions) (*adapter.Session, error) {
	messages := make(chan adapter.Message)
	results := make(chan adapter.Result, 1)
	go func() {
		<-ctx.Done()
		results <- adapter.Result{Text: "buffered final answer"}
		close(results)
		close(messages)
	}()
	return &adapter.Session{Messages: messages, Result: results}, nil
}

func TestDeadlineAfterCLIResultCannotComplete(t *testing.T) {
	gateway := newCompletionGateway(t, false, "", nil)
	d := newTestDaemon(t, gateway.server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var cancelled atomic.Bool
	d.executeRun(ctx, &successOnStopBackend{}, testClaim(), &cancelled)
	final, _ := gateway.outcome(t)
	if final.Status != RunStatusFailed || final.FailureReason != FailReasonTimeout {
		t.Fatalf("deadline must not complete: %+v", final)
	}
}
