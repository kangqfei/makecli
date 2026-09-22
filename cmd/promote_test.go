/**
 * [INPUT]: 依赖 cmd 包内的 runPromote / runPromoteStatus / newPromoteCmd / confirmPromoteFunc / errPromoteFailed / errWaitTimeout（包内白盒）、enterAppDir / saveDefaultToken / stubMetaServer / stubPollInterval / captureStdout（既有测试 helper），encoding/json、errors、net/http、net/http/httptest、strings、testing、time
 * [OUTPUT]: 覆盖 promote 子命令的单元测试（发起：CreateResource 以 beta key 发起、回执落盘、确认门控 abort 短路不触达发布接口、--yes 跳过确认、beta 从未部署 fail-fast、总览失败降级不阻断、无 beta 配对报错；--status：无记录报错、轮询至 SUCCEEDED 带 production URL、跃迁去重、FAILED → errPromoteFailed、超时 → errWaitTimeout、not-found 窗口期容忍、json 模式 stdout 纯 JSON）
 * [POS]: cmd 模块 promote.go 的配套测试，用 httptest 按路径 + X-Make-Target 路由的 Meta mock（promoteMeta）隔离网络，stubConfirmPromote 打桩终端确认
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// promoteMeta 是按路径 + X-Make-Target 路由的 Meta mock：GetApp 答已注册的 prod app（配对 myapp_beta_），
// 部署总览按 betaDeployed 答 preview 有/无，发布接口 CreateResource 记录 key 并回固定回执，StatusResource 按序答快照。
type promoteMeta struct {
	betaDeployed bool
	overviewFail bool
	createCalls  int
	createKey    string
	statusCalls  int
	statusSeq    []map[string]any
	statusBodies []map[string]any
}

func (m *promoteMeta) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case strings.Contains(r.URL.Path, "/console/v1/app-environment/product") && r.Header.Get("X-Make-Target") == "MakeService.CreateResource":
			m.createCalls++
			m.createKey, _ = body["key"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "成功", "data": map[string]any{
				"key": body["key"], "type": "Make.App",
				"properties": map[string]any{"promoteId": "run-1"},
			}})
		case strings.Contains(r.URL.Path, "/console/v1/app-environment/product"):
			i := min(m.statusCalls, len(m.statusSeq)-1)
			m.statusCalls++
			m.statusBodies = append(m.statusBodies, body)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "成功", "data": map[string]any{
				"key": body["key"], "properties": m.statusSeq[i],
			}})
		case strings.Contains(r.URL.Path, "/deployment/v1/deployment/overview"):
			if m.overviewFail {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "msg": "overview unavailable"})
				return
			}
			data := map[string]any{"appKey": "myapp",
				"production": map[string]any{"status": "Ready", "commitSha": "9f8e7d6aaaaaaa", "url": productionURLFixture}}
			if m.betaDeployed {
				data["preview"] = map[string]any{"status": "Ready", "commitSha": "abc1234bbbbbbb", "url": previewURLFixture}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "success", "data": data})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "ok", "data": map[string]any{
				"key": "myapp", "name": "myapp", "type": "Make.App",
				"meta": map[string]any{"appRole": "prod", "pairAppKey": "myapp_beta_"},
			}})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// promoteSnap 构造一帧发布进度快照
func promoteSnap(state, step string) map[string]any {
	return map[string]any{
		"promoteId": "run-1", "type": "PUBLISH_PRODUCT",
		"state": state, "step": step, "sourceBuildTaskId": "1257",
		"steps": []map[string]any{{"key": "PREPARING_PRODUCT_PUBLISH", "name": "检查发布版本", "state": "SUCCEEDED"}},
	}
}

func stubConfirmPromote(t *testing.T, err error) *int {
	t.Helper()
	calls := new(int)
	orig := confirmPromoteFunc
	confirmPromoteFunc = func(string) error { *calls++; return err }
	t.Cleanup(func() { confirmPromoteFunc = orig })
	return calls
}

// setupPromote 进入已注册 app 工程、隔离 HOME、调小轮询间隔
func setupPromote(t *testing.T, m *promoteMeta) {
	t.Helper()
	enterAppDir(t, "myapp")
	t.Setenv("HOME", t.TempDir())
	saveDefaultToken(t)
	stubPollInterval(t)
	stubMetaServer(t, m.serve(t).URL)
}

func TestRunPromote(t *testing.T) {
	t.Run("promotes beta app and records the run", func(t *testing.T) {
		m := &promoteMeta{betaDeployed: true}
		setupPromote(t, m)
		confirms := stubConfirmPromote(t, nil)

		out := captureStdout(t, func() {
			if err := runPromote(false, false, defaultPromoteTimeout, outputTable); err != nil {
				t.Fatal(err)
			}
		})
		if *confirms != 1 || m.createCalls != 1 || m.createKey != "myapp_beta_" {
			t.Fatalf("confirm=%d create=%d key=%q", *confirms, m.createCalls, m.createKey)
		}
		for _, want := range []string{"Source:      beta  abc1234  (myapp_beta_)", "Target:      production  (current: 9f8e7d6)", "Promote ID:  run-1", "promote --status --id run-1"} {
			if !strings.Contains(out, want) {
				t.Errorf("stdout missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("abort short-circuits before the publish call", func(t *testing.T) {
		m := &promoteMeta{betaDeployed: true}
		setupPromote(t, m)
		stubConfirmPromote(t, errors.New("production promote of \"myapp\" cancelled"))
		_ = captureStdout(t, func() {
			err := runPromote(false, false, defaultPromoteTimeout, outputTable)
			if err == nil || !strings.Contains(err.Error(), "cancelled") {
				t.Fatalf("expected cancel error, got %v", err)
			}
		})
		if m.createCalls != 0 {
			t.Fatal("publish must not be called after abort")
		}
	})

	t.Run("--yes skips confirmation", func(t *testing.T) {
		m := &promoteMeta{betaDeployed: true}
		setupPromote(t, m)
		confirms := stubConfirmPromote(t, errors.New("must not be asked"))
		_ = captureStdout(t, func() {
			if err := runPromote(true, false, defaultPromoteTimeout, outputTable); err != nil {
				t.Fatal(err)
			}
		})
		if *confirms != 0 || m.createCalls != 1 {
			t.Fatalf("confirm=%d create=%d", *confirms, m.createCalls)
		}
	})

	t.Run("beta never deployed fails fast before confirmation", func(t *testing.T) {
		m := &promoteMeta{betaDeployed: false}
		setupPromote(t, m)
		confirms := stubConfirmPromote(t, nil)
		_ = captureStdout(t, func() {
			err := runPromote(false, false, defaultPromoteTimeout, outputTable)
			if err == nil || !strings.Contains(err.Error(), "beta 环境尚未部署") {
				t.Fatalf("expected beta-not-deployed error, got %v", err)
			}
		})
		if *confirms != 0 || m.createCalls != 0 {
			t.Fatalf("must short-circuit: confirm=%d create=%d", *confirms, m.createCalls)
		}
	})

	t.Run("overview failure degrades to unknown and still promotes", func(t *testing.T) {
		m := &promoteMeta{overviewFail: true}
		setupPromote(t, m)
		stubConfirmPromote(t, nil)
		out := captureStdout(t, func() {
			if err := runPromote(false, false, defaultPromoteTimeout, outputTable); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(out, "beta  unknown") || m.createCalls != 1 {
			t.Fatalf("expected degraded summary and publish call:\n%s", out)
		}
	})

	t.Run("app without beta pair is rejected", func(t *testing.T) {
		enterAppDir(t, "solo")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "ok", "data": map[string]any{
				"key": "solo", "name": "solo", "type": "Make.App", "meta": map[string]any{"appRole": "prod"}}})
		}))
		t.Cleanup(srv.Close)
		stubMetaServer(t, srv.URL)
		err := runPromote(true, false, defaultPromoteTimeout, outputTable)
		if err == nil || !strings.Contains(err.Error(), "没有 beta 环境") {
			t.Fatalf("expected no-beta error, got %v", err)
		}
	})

	t.Run("--wait json keeps stdout pure JSON", func(t *testing.T) {
		m := &promoteMeta{betaDeployed: true, statusSeq: []map[string]any{promoteSnap("RUNNING", "SYNCING_PRODUCT_CONFIG"), promoteSnap("SUCCEEDED", "COMPLETED")}}
		setupPromote(t, m)
		stubConfirmPromote(t, nil)
		out := captureStdout(t, func() {
			if err := runPromote(false, true, time.Second, outputJSON); err != nil {
				t.Fatal(err)
			}
		})
		var view map[string]any
		if err := json.Unmarshal([]byte(out), &view); err != nil {
			t.Fatalf("stdout is not pure JSON: %v\n%s", err, out)
		}
		if view["app"] != "myapp" || view["betaApp"] != "myapp_beta_" || view["state"] != "SUCCEEDED" || view["url"] != productionURLFixture {
			t.Fatalf("unexpected view: %v", view)
		}
	})
}

func TestRunPromoteStatus(t *testing.T) {
	t.Run("--status and --id must come together", func(t *testing.T) {
		// 旗标校验在 RunE 入口、先于一切定位与网络：两种缺半边都拒绝
		for _, args := range [][]string{{"--status"}, {"--id", "run-1"}} {
			cmd := newPromoteCmd()
			cmd.SetArgs(args)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--id") {
				t.Errorf("args %v: expected --status/--id pairing error, got %v", args, err)
			}
		}
	})

	t.Run("queries with the given promoteId", func(t *testing.T) {
		m := &promoteMeta{betaDeployed: true, statusSeq: []map[string]any{promoteSnap("RUNNING", "PUSHING_PRODUCT_CODE")}}
		setupPromote(t, m)
		out := captureStdout(t, func() {
			if err := runPromoteStatus("run-1", false, defaultPromoteTimeout, outputTable); err != nil {
				t.Fatal(err)
			}
		})
		props, _ := m.statusBodies[0]["properties"].(map[string]any)
		if m.statusBodies[0]["key"] != "myapp_beta_" || props["promoteId"] != "run-1" {
			t.Fatalf("status request must carry beta key + given promoteId: %v", m.statusBodies[0])
		}
		for _, want := range []string{"State:       RUNNING", "Step:        PUSHING_PRODUCT_CODE", "Beta build:  #1257", "SUCCEEDED  检查发布版本 (PREPARING_PRODUCT_PUBLISH)"} {
			if !strings.Contains(out, want) {
				t.Errorf("stdout missing %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "URL:") {
			t.Error("running promote must not render production URL")
		}
	})

	t.Run("--wait polls to SUCCEEDED, dedupes transitions, renders URL", func(t *testing.T) {
		m := &promoteMeta{betaDeployed: true, statusSeq: []map[string]any{
			promoteSnap("RUNNING", "SYNCING_PRODUCT_CONFIG"), promoteSnap("RUNNING", "SYNCING_PRODUCT_CONFIG"),
			promoteSnap("RUNNING", "WAITING_PRODUCT_DEPLOYMENT"), promoteSnap("SUCCEEDED", "COMPLETED"),
		}}
		setupPromote(t, m)
		out := captureStdout(t, func() {
			if err := runPromoteStatus("run-1", true, time.Second, outputTable); err != nil {
				t.Fatal(err)
			}
		})
		if strings.Count(out, "RUNNING / SYNCING_PRODUCT_CONFIG") != 1 {
			t.Errorf("transition lines must be deduped:\n%s", out)
		}
		if !strings.Contains(out, "State:       SUCCEEDED") || !strings.Contains(out, "URL:         "+productionURLFixture) {
			t.Errorf("final detail with URL expected:\n%s", out)
		}
	})

	t.Run("--wait FAILED renders detail and returns errPromoteFailed", func(t *testing.T) {
		snap := promoteSnap("FAILED", "PUSHING_PRODUCT_CODE")
		snap["message"] = "build failed"
		m := &promoteMeta{betaDeployed: true, statusSeq: []map[string]any{snap}}
		setupPromote(t, m)
		out := captureStdout(t, func() {
			err := runPromoteStatus("run-1", true, time.Second, outputTable)
			if !errors.Is(err, errPromoteFailed) {
				t.Fatalf("expected errPromoteFailed, got %v", err)
			}
		})
		if !strings.Contains(out, "Message:     build failed") || strings.Contains(out, "URL:") {
			t.Errorf("failed detail expected without URL:\n%s", out)
		}
		if ExitCode(errPromoteFailed) != 2 {
			t.Error("errPromoteFailed must map to exit code 2")
		}
	})

	t.Run("--wait tolerates not-found window then times out", func(t *testing.T) {
		m := &promoteMeta{betaDeployed: true, statusSeq: []map[string]any{{}}}
		setupPromote(t, m)
		_ = captureStdout(t, func() {
			err := runPromoteStatus("run-1", true, 20*time.Millisecond, outputTable)
			if !errors.Is(err, errWaitTimeout) {
				t.Fatalf("expected errWaitTimeout, got %v", err)
			}
		})
		if m.statusCalls < 2 {
			t.Fatalf("expected repeated polling through not-found window, got %d calls", m.statusCalls)
		}
	})
}
