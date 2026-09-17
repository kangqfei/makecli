/**
 * [INPUT]: 依赖 cmd 包内的 runAppInfo（包内白盒）、encoding/json、net/http、net/http/httptest、strings
 * [OUTPUT]: 覆盖 app info 子命令核心逻辑的单元测试
 * [POS]: cmd 模块 app_info.go 的配套测试，用 httptest 隔离网络、t.Setenv 隔离凭证
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// appInfoMetaBody 是 GetApp 的标准成功响应
func appInfoMetaBody() map[string]any {
	return map[string]any{
		"code": 200, "msg": "success",
		"data": map[string]any{
			"key": "apptest_001", "name": "测试应用", "type": "Make.App",
			"meta":       map[string]any{"version": "1.0.0", "createdAt": "2026-01-01T10:00:00Z", "pairAppKey": "apptest_001_beta_"},
			"properties": map[string]any{"description": "demo app"},
		},
	}
}

// appInfoEnvBody 构造单环境部署响应段
func appInfoEnvBody(url string) map[string]any {
	return map[string]any{
		"status": "Ready", "buildTaskID": "bt-123", "commitSha": "9b05c7d",
		"url": url, "deploymentID": "dep-1",
		"desiredRelease": "rel-1", "activeRelease": "rel-1",
	}
}

// newAppInfoServer 起一个按路径分流的 mock：meta/v1/app 回 GetApp，
// deployment/v1/deployment/overview 交给 deployHandler 定制。
// 路径带 /api/make 网关前缀——cmd 层客户端经 withGateway 构建，出站路径含前缀
func newAppInfoServer(t *testing.T, deployHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/make/meta/v1/app":
			_ = json.NewEncoder(w).Encode(appInfoMetaBody())
		case "/api/make/deployment/v1/deployment/overview":
			deployHandler(w, r)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
}

func TestRunAppInfo(t *testing.T) {
	t.Run("renders meta header and both environments", func(t *testing.T) {
		srv := newAppInfoServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": map[string]any{
					"tenantID": "90", "appKey": "apptest_001",
					"preview":    appInfoEnvBody("https://apptest-001-preview-90.dev-make.qtech.cn"),
					"production": appInfoEnvBody("https://apptest-001-prod-90.dev-make.qtech.cn"),
				},
			})
		})
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		output := captureStdout(t, func() {
			if err := runAppInfo("apptest_001", outputTable); err != nil {
				t.Fatalf("runAppInfo: %v", err)
			}
		})

		for _, want := range []string{
			"Key:", "apptest_001", "测试应用", "demo app", "1.0.0", "2026-01-01T10:00:00Z",
			"Pair App Key:", "apptest_001_beta_",
			"ENVIRONMENT", "beta", "production", "Ready", "9b05c7d",
			"https://apptest-001-preview-90.dev-make.qtech.cn",
			"https://apptest-001-prod-90.dev-make.qtech.cn",
		} {
			if !strings.Contains(output, want) {
				t.Errorf("expected output to contain %q, got:\n%s", want, output)
			}
		}
	})

	t.Run("missing environment renders placeholder row", func(t *testing.T) {
		srv := newAppInfoServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": map[string]any{
					"tenantID": "90", "appKey": "apptest_001",
					"preview": appInfoEnvBody("https://apptest-001-preview-90.dev-make.qtech.cn"),
				},
			})
		})
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		output := captureStdout(t, func() {
			if err := runAppInfo("apptest_001", outputTable); err != nil {
				t.Fatalf("runAppInfo: %v", err)
			}
		})

		if !strings.Contains(output, "Not deployed") {
			t.Errorf("expected placeholder for missing production, got:\n%s", output)
		}
		if !strings.Contains(output, "https://apptest-001-preview-90.dev-make.qtech.cn") {
			t.Errorf("expected preview url, got:\n%s", output)
		}
	})

	t.Run("never deployed (404) renders placeholders and succeeds", func(t *testing.T) {
		srv := newAppInfoServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 404, "msg": "not deployed"})
		})
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		output := captureStdout(t, func() {
			if err := runAppInfo("apptest_001", outputTable); err != nil {
				t.Fatalf("expected success on never-deployed app, got %v", err)
			}
		})

		if got := strings.Count(output, "Not deployed"); got != 2 {
			t.Errorf("expected 2 placeholder rows, got %d in:\n%s", got, output)
		}
	})

	t.Run("app not found fails", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 404, "msg": "app not found"})
		}))
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		err := runAppInfo("apptest_001", outputTable)
		if err == nil {
			t.Fatal("expected error for missing app")
		}
		if !strings.Contains(err.Error(), "不存在") {
			t.Errorf("expected not-found message, got %v", err)
		}
	})

	t.Run("deployment service error fails command", func(t *testing.T) {
		srv := newAppInfoServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "msg": "boom"})
		})
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		if err := runAppInfo("apptest_001", outputTable); err == nil {
			t.Fatal("expected error on deployment service failure")
		}
	})

	t.Run("prints json with full detail fields", func(t *testing.T) {
		srv := newAppInfoServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": map[string]any{
					"tenantID": "90", "appKey": "apptest_001",
					"preview":    appInfoEnvBody("https://apptest-001-preview-90.dev-make.qtech.cn"),
					"production": appInfoEnvBody("https://apptest-001-prod-90.dev-make.qtech.cn"),
				},
			})
		})
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		output := captureStdout(t, func() {
			if err := runAppInfo("apptest_001", outputJSON); err != nil {
				t.Fatalf("runAppInfo json: %v", err)
			}
		})

		for _, want := range []string{`"app"`, `"deployment"`, `"deploymentID"`, `"desiredRelease"`, `"activeRelease"`, `"buildTaskID"`} {
			if !strings.Contains(output, want) {
				t.Errorf("expected JSON output to contain %s, got:\n%s", want, output)
			}
		}
		if strings.Contains(output, "ENVIRONMENT") {
			t.Errorf("expected JSON-only output, got:\n%s", output)
		}
	})

	t.Run("never deployed prints json null deployment", func(t *testing.T) {
		srv := newAppInfoServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 404, "msg": "not deployed"})
		})
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		output := captureStdout(t, func() {
			if err := runAppInfo("apptest_001", outputJSON); err != nil {
				t.Fatalf("runAppInfo json: %v", err)
			}
		})

		if !strings.Contains(output, `"deployment": null`) {
			t.Errorf("expected null deployment in JSON, got:\n%s", output)
		}
	})

	t.Run("fails on unsupported output format", func(t *testing.T) {
		if err := runAppInfo("apptest_001", "xml"); err == nil {
			t.Fatal("expected error for unsupported output format")
		}
	})

	t.Run("fails on invalid app key before network", func(t *testing.T) {
		if err := runAppInfo("_bad", outputTable); err == nil {
			t.Fatal("expected error for invalid app key")
		}
	})

	t.Run("fails without credentials", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		MetaServerURL = "http://unused"
		if err := runAppInfo("apptest_001", outputTable); err == nil {
			t.Fatal("expected error for missing credentials")
		}
	})

	t.Run("fails on unknown profile", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		setProfile(t, "nonexistent")
		if err := runAppInfo("apptest_001", outputTable); err == nil {
			t.Fatal("expected error for unknown profile")
		}
	})

	t.Run("info subcommand is mounted under app", func(t *testing.T) {
		for _, c := range newAppCmd().Commands() {
			if c.Name() == "info" {
				return
			}
		}
		t.Fatal("expected app command to mount info subcommand")
	})
}
