// [INPUT]: 当前协议的 Claim、真实 HTTP 租约响应及显式本地 Context 端点。
// [OUTPUT]: 取消/失去租约会停止执行，业务凭据不被本机账号替代，镜像协议能读取真实授权窗口。
// [POS]: 设备消费端的确定性边界验证，不调用真实 coding CLI 或业务工具。
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecutionLeaseStopsOnCancellationOrDenial(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancelled", true: "denied"}[denied], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input RenewClaimRequest
				if json.NewDecoder(r.Body).Decode(&input) != nil || input.LeaseToken != "lease_1" || input.RunID != "run_1" || r.Header.Get(TargetHeader) != TargetUpdateResource {
					t.Error("wrong lease request")
				}
				if denied {
					w.WriteHeader(403)
					_, _ = w.Write([]byte(`{"code":403,"msg":"lease rejected","data":{"reason":"lease_invalid"}}`))
					return
				}
				data, _ := json.Marshal(RenewClaimResponse{LeaseExpiresAt: time.Now().Add(time.Minute), CancelRequested: true})
				_ = json.NewEncoder(w).Encode(Envelope{Code: 200, Data: data})
			}))
			defer server.Close()
			daemon := newTestDaemon(t, server.URL)
			claim := testClaim()
			claim.LeaseSeconds = 1
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var cancelled atomic.Bool
			done := make(chan struct{})
			go func() { defer close(done); daemon.keepExecutionLease(ctx, claim, cancel, &cancelled) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("lost lease did not stop execution")
			}
			if ctx.Err() == nil || cancelled.Load() == denied {
				t.Fatal("lease result or cancellation identity was lost")
			}
		})
	}
}

func TestUnsupportedPlatformToolsDoNotUseHostCredentials(t *testing.T) {
	gateway := newFakeGateway(t)
	defer gateway.server.Close()
	claim := testClaim()
	claim.Agent.MCPServers = []json.RawMessage{json.RawMessage(`{"server":"make"}`)}
	backend := &stubBackend{}
	daemon := newTestDaemon(t, gateway.server.URL)
	var cancelled atomic.Bool
	daemon.executeRun(context.Background(), backend, claim, &cancelled)
	if backend.gotOpts.WorkDir != "" {
		t.Fatal("unsupported platform credentials reached host CLI")
	}
}

func TestLocalContextExecutionProtocol(t *testing.T) {
	endpoint := os.Getenv("MAKECLI_CONTEXT_TEST_URL")
	raw := os.Getenv("MAKECLI_CONTEXT_TEST_CLAIM")
	if endpoint == "" || raw == "" {
		t.Skip("requires explicit local Context execution")
	}
	var claim RunClaim
	if err := json.Unmarshal([]byte(raw), &claim); err != nil {
		t.Fatal(err)
	}
	pack, err := NewClient(endpoint, "synthetic-node-key").ReadContext(context.Background(), claim)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := BuildContextPrompt(pack)
	if err != nil || !strings.Contains(prompt, "共享讨论") {
		t.Fatalf("authorized source missing: %v", err)
	}
}
