/**
 * [INPUT]: 依赖 internal/api 包内的 Client（包内白盒），encoding/json、errors、net/http、net/http/httptest、testing
 * [OUTPUT]: 覆盖 Client.CreateApp / ListApps / DeleteApp（含成功 data 为标量 true 的写路径、?appRole= 断言）/ RoleForEnv（与 HostedEnv 互逆）/ WithHeaders / WithDebug / WithDryRun（X-Dry-Run 注入/缺席）/ GetApp / GetEntity / GetRelation（含 ErrNotFound 语义）/ ErrAuthFailed 鉴权语义 / Traceparent+X-Log-Id 出站头 的单元测试
 * [POS]: internal/api client.go 的配套测试，用 httptest 隔离网络
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestCreateApp(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Make-Target") != "MakeService.CreateResource" {
				t.Errorf("unexpected X-Make-Target: %s", r.Header.Get("X-Make-Target"))
			}
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Errorf("unexpected Authorization: %s", r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "create app success"})
		}))
		defer srv.Close()

		if err := New(srv.URL, "test-token").CreateApp("myapp", "我的应用", nil); err != nil {
			t.Fatalf("CreateApp: %v", err)
		}
	})

	t.Run("API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 400, "message": "invalid name"})
		}))
		defer srv.Close()

		if err := New(srv.URL, "test-token").CreateApp("myapp", "我的应用", nil); err == nil {
			t.Fatal("expected error on API failure")
		}
	})

	t.Run("invalid response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not json"))
		}))
		defer srv.Close()

		if err := New(srv.URL, "test-token").CreateApp("myapp", "我的应用", nil); err == nil {
			t.Fatal("expected error for invalid JSON response")
		}
	})
}

func TestDeleteApp(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Make-Target") != "MakeService.DeleteResource" {
				t.Errorf("unexpected X-Make-Target: %s", r.Header.Get("X-Make-Target"))
			}
			if got := r.URL.Query().Get("appRole"); got != RoleBeta {
				t.Errorf("appRole query = %q, want %q", got, RoleBeta)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["key"] != "myapp" || body["type"] != "Make.App" {
				t.Errorf("unexpected body: %v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "delete app success"})
		}))
		defer srv.Close()

		if err := New(srv.URL, "test-token").DeleteApp("myapp", RoleBeta); err != nil {
			t.Fatalf("DeleteApp: %v", err)
		}
	})

	// 写操作成功响应的 data 形态由各端点自定：DeleteApp 回 true。
	// 写路径不得对成功 data 的形态提任何要求（409 冲突形态只在冲突分支解析）。
	t.Run("success with scalar data", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "success", "data": true})
		}))
		defer srv.Close()

		if err := New(srv.URL, "test-token").DeleteApp("myapp", RoleProd); err != nil {
			t.Fatalf("DeleteApp: %v", err)
		}
	})

	t.Run("API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "msg": "internal error"})
		}))
		defer srv.Close()

		if err := New(srv.URL, "test-token").DeleteApp("myapp", RoleProd); err == nil {
			t.Fatal("expected error on API failure")
		}
	})
}

func TestListApps(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Make-Target") != "MakeService.ListResources" {
				t.Errorf("unexpected X-Make-Target: %s", r.Header.Get("X-Make-Target"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"message": "success",
				"data": []map[string]any{
					{"key": "ProjectA", "name": "项目A", "type": "Make.App", "meta": map[string]any{"version": "1.0.0"}, "properties": map[string]any{"description": "demo"}},
					{"key": "ProjectB", "name": "项目B", "type": "Make.App", "meta": map[string]any{"version": "2.0.0"}, "properties": map[string]any{"description": "demo"}},
				},
				"pagination": map[string]any{"page": 1, "size": 10, "total": 2},
			})
		}))
		defer srv.Close()

		apps, total, err := New(srv.URL, "test-token").ListApps(1, 10, "")
		if err != nil {
			t.Fatalf("ListApps: %v", err)
		}
		if total != 2 {
			t.Errorf("expected total=2, got %d", total)
		}
		if len(apps) != 2 {
			t.Errorf("expected 2 apps, got %d", len(apps))
		}
		if apps[0].Name != "项目A" {
			t.Errorf("unexpected first app name: %s", apps[0].Name)
		}
	})

	t.Run("API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "message": "internal error"})
		}))
		defer srv.Close()

		if _, _, err := New(srv.URL, "test-token").ListApps(1, 10, ""); err == nil {
			t.Fatal("expected error on API failure")
		}
	})

	t.Run("empty list", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "message": "success",
				"data":       []any{},
				"pagination": map[string]any{"page": 1, "size": 10, "total": 0},
			})
		}))
		defer srv.Close()

		apps, total, err := New(srv.URL, "test-token").ListApps(1, 10, "")
		if err != nil {
			t.Fatalf("ListApps: %v", err)
		}
		if total != 0 || len(apps) != 0 {
			t.Errorf("expected empty result, got apps=%d total=%d", len(apps), total)
		}
	})
}

func TestWithHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Tenant-ID"); got != "tenant-abc" {
			t.Errorf("X-Tenant-ID = %q, want %q", got, "tenant-abc")
		}
		if got := r.Header.Get("X-Operator-ID"); got != "op-123" {
			t.Errorf("X-Operator-ID = %q, want %q", got, "op-123")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "ok"})
	}))
	defer srv.Close()

	headers := map[string]string{
		"X-Tenant-ID":   "tenant-abc",
		"X-Operator-ID": "op-123",
	}
	client := New(srv.URL, "test-token", WithHeaders(headers))
	if err := client.CreateApp("test", "测试", nil); err != nil {
		t.Fatalf("CreateApp with headers: %v", err)
	}
}

// TestWithDryRun 验证 dry-run 横切信号的注入：开启时写请求带 X-Dry-Run: true，
// 关闭（默认）时该头缺席——服务端据此决定 ROLLBACK 还是 COMMIT。
func TestWithDryRun(t *testing.T) {
	t.Run("injects X-Dry-Run: true when enabled", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Dry-Run"); got != "true" {
				t.Errorf("X-Dry-Run = %q, want %q", got, "true")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "ok"})
		}))
		defer srv.Close()

		if err := New(srv.URL, "test-token", WithDryRun(true)).CreateApp("test", "测试", nil); err != nil {
			t.Fatalf("CreateApp dry-run: %v", err)
		}
	})

	t.Run("omits X-Dry-Run by default", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Dry-Run"); got != "" {
				t.Errorf("X-Dry-Run should be absent, got %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "ok"})
		}))
		defer srv.Close()

		if err := New(srv.URL, "test-token").CreateApp("test", "测试", nil); err != nil {
			t.Fatalf("CreateApp: %v", err)
		}
		// WithDryRun(false) 与不传等价：仍不应注入该头
		if err := New(srv.URL, "test-token", WithDryRun(false)).CreateApp("test", "测试", nil); err != nil {
			t.Fatalf("CreateApp WithDryRun(false): %v", err)
		}
	})
}

// TestTraceHeaders 验证每个出站请求都带 W3C traceparent 与 X-Log-Id，
// 且 X-Log-Id 等于 traceparent 的 trace-id 段（二者必须指向同一 trace）。
func TestTraceHeaders(t *testing.T) {
	re := regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-01$`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tp := r.Header.Get("Traceparent")
		m := re.FindStringSubmatch(tp)
		if m == nil {
			t.Errorf("Traceparent 不符合 W3C v00 格式: %q", tp)
		} else if logID := r.Header.Get("X-Log-Id"); logID != m[1] {
			t.Errorf("X-Log-Id %q != traceparent trace-id 段 %q", logID, m[1])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "ok"})
	}))
	defer srv.Close()

	if err := New(srv.URL, "test-token").CreateApp("test", "测试", nil); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
}

func TestWithDebugOption(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "ok"})
	}))
	defer srv.Close()

	client := New(srv.URL, "test-token", WithDebug(true, DebugText))
	if err := client.CreateApp("test", "测试", nil); err != nil {
		t.Fatalf("CreateApp with debug: %v", err)
	}
}

// ---------------------------------- Get* + ErrNotFound 语义 ----------------------------------

// newGetServer 启动一个原样回放给定 JSON body / HTTP status 的测试服务器，
// 用于精确控制 GetResource 的响应，验证 not-found 与真实错误的区分。
func newGetServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestAuthFailedSemantics(t *testing.T) {
	t.Run("auth-failed code returns ErrAuthFailed", func(t *testing.T) {
		srv := newGetServer(t, http.StatusOK, `{"code":990300403,"msg":"token验证失败"}`)
		defer srv.Close()

		_, _, err := New(srv.URL, "bad-token").ListApps(1, 10, "")
		if !errors.Is(err, ErrAuthFailed) {
			t.Fatalf("expected ErrAuthFailed, got %v", err)
		}
		// 原始 code/msg 须保留供上层展示（cmd 层引导文案首行复用它）
		if !strings.Contains(err.Error(), "990300403") || !strings.Contains(err.Error(), "token验证失败") {
			t.Errorf("error should preserve code/msg, got %q", err.Error())
		}
	})

	t.Run("non-auth 500 is NOT ErrAuthFailed", func(t *testing.T) {
		srv := newGetServer(t, http.StatusOK, `{"code":500,"msg":"boom"}`)
		defer srv.Close()

		_, _, err := New(srv.URL, "test-token").ListApps(1, 10, "")
		if err == nil {
			t.Fatal("expected error on 500")
		}
		if errors.Is(err, ErrAuthFailed) {
			t.Fatalf("500 must not map to ErrAuthFailed, got %v", err)
		}
	})
}

func TestGetAppNotFoundSemantics(t *testing.T) {
	t.Run("not-found business code returns ErrNotFound", func(t *testing.T) {
		srv := newGetServer(t, http.StatusOK, `{"code":404,"msg":"app not found","data":{}}`)
		defer srv.Close()

		_, err := New(srv.URL, "test-token").GetApp("ghost")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("200 with empty data returns ErrNotFound", func(t *testing.T) {
		srv := newGetServer(t, http.StatusOK, `{"code":200,"msg":"ok","data":{}}`)
		defer srv.Close()

		_, err := New(srv.URL, "test-token").GetApp("ghost")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound for empty data, got %v", err)
		}
	})

	t.Run("exists returns no error", func(t *testing.T) {
		srv := newGetServer(t, http.StatusOK, `{"code":200,"msg":"ok","data":{"key":"myapp","name":"我的应用","type":"Make.App"}}`)
		defer srv.Close()

		app, err := New(srv.URL, "test-token").GetApp("myapp")
		if err != nil {
			t.Fatalf("GetApp: %v", err)
		}
		if app.Key != "myapp" {
			t.Fatalf("expected key myapp, got %q", app.Key)
		}
	})

	t.Run("500 business code is NOT ErrNotFound", func(t *testing.T) {
		srv := newGetServer(t, http.StatusOK, `{"code":500,"msg":"internal error","data":{}}`)
		defer srv.Close()

		_, err := New(srv.URL, "test-token").GetApp("myapp")
		if err == nil {
			t.Fatal("expected error on 500 business code")
		}
		if errors.Is(err, ErrNotFound) {
			t.Fatalf("500 must not map to ErrNotFound, got %v", err)
		}
	})

	t.Run("transport error is NOT ErrNotFound", func(t *testing.T) {
		// 指向一个已关闭的 server，触发真实传输错误
		srv := newGetServer(t, http.StatusOK, `{"code":200}`)
		url := srv.URL
		srv.Close()

		_, err := New(url, "test-token").GetApp("myapp")
		if err == nil {
			t.Fatal("expected transport error")
		}
		if errors.Is(err, ErrNotFound) {
			t.Fatalf("transport error must not map to ErrNotFound, got %v", err)
		}
	})

	t.Run("decode error is NOT ErrNotFound", func(t *testing.T) {
		srv := newGetServer(t, http.StatusOK, `not json`)
		defer srv.Close()

		_, err := New(srv.URL, "test-token").GetApp("myapp")
		if err == nil {
			t.Fatal("expected decode error")
		}
		if errors.Is(err, ErrNotFound) {
			t.Fatalf("decode error must not map to ErrNotFound, got %v", err)
		}
	})
}

func TestGetEntityNotFoundSemantics(t *testing.T) {
	t.Run("not-found returns ErrNotFound", func(t *testing.T) {
		srv := newGetServer(t, http.StatusOK, `{"code":404,"msg":"entity not found","data":{}}`)
		defer srv.Close()

		_, err := New(srv.URL, "test-token").GetEntity("myapp", "ghost")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("500 is NOT ErrNotFound", func(t *testing.T) {
		srv := newGetServer(t, http.StatusOK, `{"code":500,"msg":"boom","data":{}}`)
		defer srv.Close()

		_, err := New(srv.URL, "test-token").GetEntity("myapp", "task")
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("expected non-ErrNotFound error, got %v", err)
		}
	})

	t.Run("exists returns no error", func(t *testing.T) {
		srv := newGetServer(t, http.StatusOK, `{"code":200,"msg":"ok","data":{"key":"task","name":"任务","appKey":"myapp"}}`)
		defer srv.Close()

		ent, err := New(srv.URL, "test-token").GetEntity("myapp", "task")
		if err != nil {
			t.Fatalf("GetEntity: %v", err)
		}
		if ent.Key != "task" {
			t.Fatalf("expected key task, got %q", ent.Key)
		}
	})
}

func TestGetRelationNotFoundSemantics(t *testing.T) {
	t.Run("not-found returns ErrNotFound", func(t *testing.T) {
		srv := newGetServer(t, http.StatusOK, `{"code":404,"msg":"relation not found","data":{}}`)
		defer srv.Close()

		_, err := New(srv.URL, "test-token").GetRelation("myapp", "ghost")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("transport error is NOT ErrNotFound", func(t *testing.T) {
		srv := newGetServer(t, http.StatusOK, `{"code":200}`)
		url := srv.URL
		srv.Close()

		_, err := New(url, "test-token").GetRelation("myapp", "rel")
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("expected non-ErrNotFound transport error, got %v", err)
		}
	})

	t.Run("exists returns no error", func(t *testing.T) {
		srv := newGetServer(t, http.StatusOK, `{"code":200,"msg":"ok","data":{"key":"project_has_tasks","name":"关联","appKey":"myapp"}}`)
		defer srv.Close()

		rel, err := New(srv.URL, "test-token").GetRelation("myapp", "project_has_tasks")
		if err != nil {
			t.Fatalf("GetRelation: %v", err)
		}
		if rel.Key != "project_has_tasks" {
			t.Fatalf("expected key project_has_tasks, got %q", rel.Key)
		}
	})
}

// RoleForEnv 与 HostedEnv 互为逆映射：任一角色的 app，承载环境再映射回来仍是该角色
func TestRoleForEnv(t *testing.T) {
	for _, role := range []string{RoleProd, RoleBeta} {
		app := &App{Meta: map[string]any{"appRole": role}}
		if got := RoleForEnv(app.HostedEnv()); got != role {
			t.Errorf("RoleForEnv(HostedEnv(%s)) = %q, want %q", role, got, role)
		}
	}
	if got := RoleForEnv("unknown"); got != RoleProd {
		t.Errorf("RoleForEnv(unknown) = %q, want %q", got, RoleProd)
	}
}

func TestAppKeyForEnv(t *testing.T) {
	prod := &App{Key: "shop", Meta: map[string]any{"appRole": "prod", "pairAppKey": "shop_beta_"}}
	beta := &App{Key: "shop_beta_", Meta: map[string]any{"appRole": "beta", "pairAppKey": "shop"}}
	legacy := &App{Key: "old", Meta: map[string]any{}} // 无 appRole 的历史 app 视为 prod
	tests := []struct {
		name    string
		app     *App
		env     string
		want    string
		wantErr bool
	}{
		{"prod/beta → pair", prod, EnvBeta, "shop_beta_", false},
		{"prod/production → self", prod, EnvProduction, "shop", false},
		{"beta/beta → self", beta, EnvBeta, "shop_beta_", false},
		{"beta/production → pair", beta, EnvProduction, "shop", false},
		{"legacy/production → self", legacy, EnvProduction, "old", false},
		{"legacy/beta → 无配对报错", legacy, EnvBeta, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.app.KeyForEnv(tt.env)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("KeyForEnv = %q, want %q", got, tt.want)
			}
		})
	}
}
