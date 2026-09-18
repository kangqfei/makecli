/**
 * [INPUT]: 依赖 cmd 包内的 runRecordList（包内白盒），encoding/json、net/http、net/http/httptest、strings、testing
 * [OUTPUT]: 覆盖 record list 子命令核心逻辑的单元测试（列表/JSON输出/空列表/无凭证/API错误/未知profile/非法页码/非法格式/非法 sort-json/sort-json 直透/filter 直透）
 * [POS]: cmd 模块 record_list.go 的配套测试，用 httptest 隔离网络、t.Setenv 隔离凭证
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

func TestRunRecordList(t *testing.T) {
	t.Run("lists records in table format", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": []map[string]any{
					{"recordID": "rec_001", "name": "张三", "age": 18},
				},
				"pagination": map[string]any{"page": 1, "size": 20, "total": 1},
			})
		}))
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		out := captureStdout(t, func() {
			if err := runRecordList("TODO", "User", 1, 20, outputTable, "", "", ""); err != nil {
				t.Fatalf("runRecordList: %v", err)
			}
		})

		if !strings.Contains(out, "张三") {
			t.Fatalf("expected '张三' in output, got %q", out)
		}
		if !strings.Contains(out, "Showing 1 of 1") {
			t.Fatalf("expected 'Showing 1 of 1' in output, got %q", out)
		}
	})

	t.Run("lists records as json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": []map[string]any{
					{"recordID": "rec_001", "name": "张三", "age": 18},
				},
				"pagination": map[string]any{"page": 1, "size": 20, "total": 1},
			})
		}))
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		out := captureStdout(t, func() {
			if err := runRecordList("TODO", "User", 1, 20, outputJSON, "", "", ""); err != nil {
				t.Fatalf("runRecordList json: %v", err)
			}
		})

		if !strings.Contains(out, "\"data\"") {
			t.Fatalf("expected '\"data\"' in JSON output, got %q", out)
		}
		if !strings.Contains(out, "\"count\": 1") {
			t.Fatalf("expected '\"count\": 1' in JSON output, got %q", out)
		}
	})

	t.Run("empty list prints message", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data":       []any{},
				"pagination": map[string]any{"page": 1, "size": 20, "total": 0},
			})
		}))
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		out := captureStdout(t, func() {
			if err := runRecordList("TODO", "User", 1, 20, outputTable, "", "", ""); err != nil {
				t.Fatalf("runRecordList empty: %v", err)
			}
		})

		if !strings.Contains(out, "No records found") {
			t.Fatalf("expected 'No records found' in output, got %q", out)
		}
	})

	t.Run("fails without credentials", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		MetaServerURL = "http://unused"
		if err := runRecordList("TODO", "User", 1, 20, outputTable, "", "", ""); err == nil {
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

		if err := runRecordList("TODO", "User", 1, 20, outputTable, "", "", ""); err == nil {
			t.Fatal("expected error on API failure")
		}
	})

	t.Run("fails with unknown profile", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = "http://unused"
		setProfile(t, "nonexistent")
		if err := runRecordList("TODO", "User", 1, 20, outputTable, "", "", ""); err == nil {
			t.Fatal("expected error for unknown profile")
		}
	})

	t.Run("fails when page is less than 1", func(t *testing.T) {
		if err := runRecordList("TODO", "User", 0, 20, outputTable, "", "", ""); err == nil {
			t.Fatal("expected error for invalid page")
		}
	})

	t.Run("fails when size is less than 1", func(t *testing.T) {
		if err := runRecordList("TODO", "User", 1, 0, outputTable, "", "", ""); err == nil {
			t.Fatal("expected error for invalid size")
		}
	})

	t.Run("fails on unsupported output format", func(t *testing.T) {
		if err := runRecordList("TODO", "User", 1, 20, "xml", "", "", ""); err == nil {
			t.Fatal("expected error for unsupported output format")
		}
	})

	t.Run("fails on invalid sort-json", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = "http://unused"
		err := runRecordList("TODO", "User", 1, 20, outputTable, "", "createdAt:desc", "")
		if err == nil || !strings.Contains(err.Error(), "--sort-json") {
			t.Fatalf("expected error naming --sort-json, got %v", err)
		}
	})

	t.Run("sends sort-json as sort array", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			sort, ok := req["sort"].([]any)
			if !ok || len(sort) != 2 {
				t.Fatalf("expected sort array of 2, got %v", req["sort"])
			}
			first := sort[0].(map[string]any)
			if first["fieldKey"] != "createdAt" || first["order"] != "desc" {
				t.Errorf("unexpected first sort key: %v", first)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data":       []any{},
				"pagination": map[string]any{"page": 1, "size": 20, "total": 0},
			})
		}))
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		_ = captureStdout(t, func() {
			sortJSON := `[{"fieldKey":"createdAt","order":"desc"},{"fieldKey":"id","order":"asc"}]`
			if err := runRecordList("TODO", "User", 1, 20, outputTable, "", sortJSON, ""); err != nil {
				t.Fatalf("runRecordList with sort-json: %v", err)
			}
		})
	})

	t.Run("sends filter as Expression object", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			obj, ok := req["filter"].(map[string]any)
			if !ok {
				t.Fatalf("expected filter to be Expression object, got %T", req["filter"])
			}
			if obj["expression"] != "amount >= 100" {
				t.Errorf("expected raw CEL passthrough, got %v", obj["expression"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data":       []any{},
				"pagination": map[string]any{"page": 1, "size": 20, "total": 0},
			})
		}))
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		out := captureStdout(t, func() {
			if err := runRecordList("TODO", "User", 1, 20, outputTable, "", "", "amount >= 100"); err != nil {
				t.Fatalf("runRecordList with filter: %v", err)
			}
		})
		_ = out
	})
}
