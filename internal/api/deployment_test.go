/**
 * [INPUT]: 依赖 client.go 的 New / GetDeploymentOverview、encoding/json、errors、net/http、net/http/httptest
 * [OUTPUT]: 覆盖 GetDeploymentOverview 的单元测试
 * [POS]: internal/api 模块 deployment.go 的配套测试，用 httptest 隔离网络
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fullEnvDeployment 构造一份全字段的环境部署响应体（url 参数化便于双环境区分）
func fullEnvDeployment(url string) map[string]any {
	return map[string]any{
		"status":         "Ready",
		"buildTaskID":    "bt-123",
		"commitSha":      "9b05c7d",
		"url":            url,
		"deploymentID":   "app-90-apptest-001-1jkq6p",
		"desiredRelease": "release-90-apptest-001-vl97si",
		"activeRelease":  "release-90-apptest-001-vl97si",
	}
}

func TestGetDeploymentOverview(t *testing.T) {
	t.Run("parses both environments", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/deployment/v1/deployment/overview" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if r.Header.Get("X-Make-Target") != "MakeService.GetResource" {
				t.Errorf("unexpected X-Make-Target: %s", r.Header.Get("X-Make-Target"))
			}
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if req["appKey"] != "apptest_001" {
				t.Errorf("unexpected appKey: %v", req["appKey"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": map[string]any{
					"tenantID": "90", "appKey": "apptest_001",
					"preview":    fullEnvDeployment("https://apptest-001-preview-90.dev-make.qtech.cn"),
					"production": fullEnvDeployment("https://apptest-001-prod-90.dev-make.qtech.cn"),
				},
			})
		}))
		defer srv.Close()

		got, err := New(srv.URL, "token").GetDeploymentOverview("apptest_001")
		if err != nil {
			t.Fatalf("GetDeploymentOverview: %v", err)
		}
		if got.TenantID != "90" || got.AppKey != "apptest_001" {
			t.Errorf("unexpected identity: %+v", got)
		}
		if got.Preview == nil || got.Production == nil {
			t.Fatalf("expected both environments, got %+v", got)
		}
		if got.Preview.URL != "https://apptest-001-preview-90.dev-make.qtech.cn" {
			t.Errorf("unexpected preview url: %s", got.Preview.URL)
		}
		if got.Preview.Status != "Ready" || got.Preview.CommitSha != "9b05c7d" {
			t.Errorf("unexpected preview fields: %+v", got.Preview)
		}
		if got.Production.URL != "https://apptest-001-prod-90.dev-make.qtech.cn" {
			t.Errorf("unexpected production url: %s", got.Production.URL)
		}
		if got.Preview.BuildTaskID != "bt-123" || got.Preview.DeploymentID == "" ||
			got.Preview.DesiredRelease == "" || got.Preview.ActiveRelease == "" {
			t.Errorf("expected full detail fields, got %+v", got.Preview)
		}
	})

	t.Run("missing environment decodes as nil", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": map[string]any{
					"tenantID": "90", "appKey": "apptest_001",
					"preview": fullEnvDeployment("https://apptest-001-preview-90.dev-make.qtech.cn"),
				},
			})
		}))
		defer srv.Close()

		got, err := New(srv.URL, "token").GetDeploymentOverview("apptest_001")
		if err != nil {
			t.Fatalf("GetDeploymentOverview: %v", err)
		}
		if got.Preview == nil {
			t.Fatal("expected preview to be present")
		}
		if got.Production != nil {
			t.Fatalf("expected production nil, got %+v", got.Production)
		}
	})

	t.Run("404 maps to ErrNotFound", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 404, "msg": "not deployed"})
		}))
		defer srv.Close()

		_, err := New(srv.URL, "token").GetDeploymentOverview("apptest_001")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("non-200 business code fails without ErrNotFound", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "msg": "boom"})
		}))
		defer srv.Close()

		_, err := New(srv.URL, "token").GetDeploymentOverview("apptest_001")
		if err == nil {
			t.Fatal("expected error on API failure")
		}
		if errors.Is(err, ErrNotFound) {
			t.Fatalf("500 must not map to ErrNotFound, got %v", err)
		}
	})

	t.Run("auth failure code maps to ErrAuthFailed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 990300403, "msg": "token invalid"})
		}))
		defer srv.Close()

		_, err := New(srv.URL, "token").GetDeploymentOverview("apptest_001")
		if !errors.Is(err, ErrAuthFailed) {
			t.Fatalf("expected ErrAuthFailed, got %v", err)
		}
	})

	t.Run("transport error surfaces", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
		srv.Close() // 立即关闭制造连接失败

		if _, err := New(srv.URL, "token").GetDeploymentOverview("apptest_001"); err == nil {
			t.Fatal("expected transport error")
		}
	})

	t.Run("malformed response fails", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not json"))
		}))
		defer srv.Close()

		if _, err := New(srv.URL, "token").GetDeploymentOverview("apptest_001"); err == nil {
			t.Fatal("expected decode error")
		}
	})
}

// TestDeploymentOverviewEnv 覆盖环境选择器：按名取 preview/production（用户面 beta 亦映射到 preview），未知名与缺失环境返回 nil。
func TestDeploymentOverviewEnv(t *testing.T) {
	preview := &EnvDeployment{URL: "https://p.example"}
	production := &EnvDeployment{URL: "https://prod.example"}
	o := &DeploymentOverview{Preview: preview, Production: production}

	if got := o.Env("preview"); got != preview {
		t.Errorf("Env(preview) = %v, want preview entry", got)
	}
	if got := o.Env(EnvBeta); got != preview {
		t.Errorf("Env(beta) = %v, want preview entry (user-facing beta maps to server preview)", got)
	}
	if got := o.Env("production"); got != production {
		t.Errorf("Env(production) = %v, want production entry", got)
	}
	if got := o.Env("staging"); got != nil {
		t.Errorf("Env(staging) = %v, want nil", got)
	}
	if got := (&DeploymentOverview{}).Env("preview"); got != nil {
		t.Errorf("Env on never-deployed overview = %v, want nil", got)
	}
}

func TestEnvVocabulary(t *testing.T) {
	for in, want := range map[string]string{"beta": "preview", "production": "production", "preview": "preview"} {
		if got := ServerEnvKey(in); got != want {
			t.Errorf("ServerEnvKey(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"preview": "beta", "production": "production", "beta": "beta"} {
		if got := DisplayEnv(in); got != want {
			t.Errorf("DisplayEnv(%q) = %q, want %q", in, got, want)
		}
	}
}
