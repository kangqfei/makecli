/**
 * [INPUT]: 依赖 cmd 包内的 runSchema（包内白盒），internal/config、encoding/json、net/http、net/http/httptest、strings
 * [OUTPUT]: 覆盖 schema 子命令核心逻辑的单元测试
 * [POS]: cmd 模块 schema.go 的配套测试，用 httptest 隔离网络、t.Setenv 隔离凭证
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

func TestRunSchema(t *testing.T) {
	t.Run("returns schema successfully", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Make-Target") != "MakeService.GetResource" {
				t.Errorf("unexpected X-Make-Target: %s", r.Header.Get("X-Make-Target"))
			}
			if !strings.HasSuffix(r.URL.Path, "/meta/v1/schema") {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			var req map[string]string
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["appKey"] != "expense_management" {
				t.Errorf("unexpected appKey: %s", req["appKey"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "get app schema success",
				"data": map[string]any{
					"app": map[string]any{
						"key": "expense_management", "name": "报销管理", "type": "Make.App",
						"meta":       map[string]any{"version": "1.0.0"},
						"properties": map[string]any{"description": "demo"},
					},
					"entities": []map[string]any{
						{"key": "expense_report", "name": "报销单", "type": "Make.Entity", "appKey": "expense_management",
							"meta": map[string]any{"version": "1.0.0"},
							"properties": map[string]any{"fields": []map[string]any{{"key": "applicant", "name": "申请人", "type": "Make.Field.Text",
								"capabilities": map[string]any{"groupable": false, "aggregable": false, "sortable": true, "supportsUniqueConstraint": true}}}}},
					},
					"relations": []map[string]any{
						{"key": "report_has_invoices", "name": "报销单发票关联", "type": "Make.Relation", "appKey": "expense_management",
							"meta": map[string]any{"version": "1.0.0"},
							"properties": map[string]any{
								"from": map[string]any{"entityKey": "expense_report", "cardinality": "one"},
								"to":   map[string]any{"entityKey": "expense_invoice", "cardinality": "many"},
							}},
					},
				},
			})
		}))
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		output := captureStdout(t, func() {
			if err := runSchema("expense_management"); err != nil {
				t.Fatalf("runSchema: %v", err)
			}
		})

		if !strings.Contains(output, "\"app\"") {
			t.Fatalf("expected app in output, got %q", output)
		}
		if !strings.Contains(output, "\"entities\"") {
			t.Fatalf("expected entities in output, got %q", output)
		}
		if !strings.Contains(output, "\"relations\"") {
			t.Fatalf("expected relations in output, got %q", output)
		}
		if !strings.Contains(output, "expense_report") {
			t.Fatalf("expected entity name in output, got %q", output)
		}
		// capabilities 是服务端派生的能力位，schema 输出须原样透传（Field 结构缺此字段时会被静默丢弃）
		if !strings.Contains(output, "\"capabilities\"") {
			t.Fatalf("expected field capabilities in output, got %q", output)
		}
		// 键集合由服务端演进（aggregable 后加），CLI 用 map 透传、不做白名单
		for _, key := range []string{"groupable", "aggregable", "sortable", "supportsUniqueConstraint"} {
			if !strings.Contains(output, key) {
				t.Fatalf("expected capability key %q in output, got %q", key, output)
			}
		}
	})

	t.Run("fails without credentials", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		MetaServerURL = "http://unused"
		if err := runSchema("myapp"); err == nil {
			t.Fatal("expected error for missing credentials")
		}
	})

	t.Run("fails on API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "msg": "server error"})
		}))
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		if err := runSchema("myapp"); err == nil {
			t.Fatal("expected error on API failure")
		}
	})

	t.Run("fails on unknown profile", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = "http://unused"
		setProfile(t, "nonexistent")
		if err := runSchema("myapp"); err == nil {
			t.Fatal("expected error for unknown profile")
		}
	})
}
