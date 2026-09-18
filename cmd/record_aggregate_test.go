/**
 * [INPUT]: 依赖 cmd 包内的 runRecordAggregate / aggregateColumns / formatAggregateCell（包内白盒），encoding/json、net/http、net/http/httptest、strings、testing
 * [OUTPUT]: 覆盖 record aggregate 子命令的单元测试（请求体直透/表格列序与单元格渲染/JSON 输出/空结果/非法 JSON flag/非法分页/无凭证/API 错误）
 * [POS]: cmd 模块 record_aggregate.go 的配套测试，用 httptest 隔离网络、t.Setenv 隔离凭证
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

const (
	testGroupJSON      = `[{"fieldKey":"status"},{"fieldKey":"orderDate","granularity":"month","alias":"month"}]`
	testAggregatesJSON = `[{"aggregate":"count","alias":"orderCount"},{"fieldKey":"amount","aggregate":"sum","alias":"totalAmount"}]`
)

func aggregateServer(t *testing.T, check func(req map[string]any), rows []map[string]any, total int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if check != nil {
			check(req)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200, "msg": "Aggregate records success",
			"data":       rows,
			"pagination": map[string]any{"page": 1, "size": 10, "total": total},
		})
	}))
}

func TestRunRecordAggregate(t *testing.T) {
	baseOpts := recordAggregateOpts{page: 1, size: 10, output: outputTable, groupJSON: testGroupJSON, aggregatesJSON: testAggregatesJSON}

	t.Run("passes request through and renders table in declared column order", func(t *testing.T) {
		srv := aggregateServer(t, func(req map[string]any) {
			if req["appKey"] != "crm" || req["entityKey"] != "order" {
				t.Errorf("unexpected keys in request: %v", req)
			}
			if req["filter"].(map[string]any)["expression"] != "status != 'draft'" {
				t.Errorf("unexpected filter: %v", req["filter"])
			}
			if req["aggregateFilter"].(map[string]any)["expression"] != "totalAmount > 10000" {
				t.Errorf("unexpected aggregateFilter: %v", req["aggregateFilter"])
			}
			sort := req["sort"].([]any)[0].(map[string]any)
			if sort["alias"] != "totalAmount" || sort["order"] != "desc" || sort["fieldKey"] != nil {
				t.Errorf("unexpected sort: %v", sort)
			}
			if len(req["group"].([]any)) != 2 || len(req["aggregates"].([]any)) != 2 {
				t.Errorf("unexpected group/aggregates: %v / %v", req["group"], req["aggregates"])
			}
		}, []map[string]any{
			{"status": map[string]any{"value": "completed", "label": "已完成"}, "month": map[string]any{"value": "2026-03", "label": "2026-03"}, "orderCount": 128, "totalAmount": 1250000.5},
			{"status": map[string]any{"value": nil, "label": "未填写"}, "month": map[string]any{"value": "2026-03", "label": "2026-03"}, "orderCount": 6, "totalAmount": 1000000},
		}, 2)
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		o := baseOpts
		o.filter = "status != 'draft'"
		o.aggregateFilter = "totalAmount > 10000"
		o.sortJSON = `[{"alias":"totalAmount","order":"desc"}]`
		out := captureStdout(t, func() {
			if err := runRecordAggregate("crm", "order", o); err != nil {
				t.Fatalf("runRecordAggregate: %v", err)
			}
		})

		for _, want := range []string{"已完成", "未填写", "1250000.5", "1000000", "128"} {
			if !strings.Contains(out, want) {
				t.Errorf("expected %q in output, got %q", want, out)
			}
		}
		if strings.Contains(out, "1e+06") || strings.Contains(out, "map[") {
			t.Errorf("expected plain numbers and labels, got %q", out)
		}
		// 列名只在表头出现一次，首次出现的位置即列序
		if !columnsInOrder(out, "STATUS", "MONTH", "ORDERCOUNT", "TOTALAMOUNT") {
			t.Errorf("expected declared column order, got %q", out)
		}
		if !strings.Contains(out, "Showing 2 of 2 groups") {
			t.Errorf("expected footer, got %q", out)
		}
	})

	t.Run("omits optional request fields", func(t *testing.T) {
		srv := aggregateServer(t, func(req map[string]any) {
			for _, key := range []string{"group", "sort", "filter", "aggregateFilter"} {
				if req[key] != nil {
					t.Errorf("expected no %s in request, got %v", key, req[key])
				}
			}
		}, []map[string]any{{"orderCount": 42, "totalAmount": 7}}, 1)
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		o := baseOpts
		o.groupJSON = ""
		out := captureStdout(t, func() {
			if err := runRecordAggregate("crm", "order", o); err != nil {
				t.Fatalf("runRecordAggregate: %v", err)
			}
		})
		if !strings.Contains(out, "Showing 1 of 1 groups") {
			t.Errorf("expected single global row, got %q", out)
		}
	})

	t.Run("outputs json with pagination", func(t *testing.T) {
		srv := aggregateServer(t, nil, []map[string]any{{"orderCount": 42}}, 1)
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		o := baseOpts
		o.output = outputJSON
		out := captureStdout(t, func() {
			if err := runRecordAggregate("crm", "order", o); err != nil {
				t.Fatalf("runRecordAggregate json: %v", err)
			}
		})
		if !strings.Contains(out, `"data"`) || !strings.Contains(out, `"count": 1`) || !strings.Contains(out, `"total": 1`) {
			t.Errorf("unexpected json output: %q", out)
		}
	})

	t.Run("empty result prints message", func(t *testing.T) {
		srv := aggregateServer(t, nil, []map[string]any{}, 0)
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		out := captureStdout(t, func() {
			if err := runRecordAggregate("crm", "order", baseOpts); err != nil {
				t.Fatalf("runRecordAggregate empty: %v", err)
			}
		})
		if !strings.Contains(out, "No groups found") {
			t.Errorf("expected empty message, got %q", out)
		}
	})

	t.Run("fails on invalid json flags before any request", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		MetaServerURL = "http://unused"
		for name, mutate := range map[string]func(*recordAggregateOpts){
			"group-json":      func(o *recordAggregateOpts) { o.groupJSON = `[{"fieldKey":"status","granularit":"day"}]` },
			"aggregates-json": func(o *recordAggregateOpts) { o.aggregatesJSON = "count" },
			"sort-json":       func(o *recordAggregateOpts) { o.sortJSON = "totalAmount:desc" },
		} {
			o := baseOpts
			mutate(&o)
			err := runRecordAggregate("crm", "order", o)
			if err == nil || !strings.Contains(err.Error(), "--"+name) {
				t.Errorf("expected error naming --%s, got %v", name, err)
			}
		}
	})

	t.Run("fails on invalid pagination or output", func(t *testing.T) {
		for _, o := range []recordAggregateOpts{
			{page: 0, size: 10, output: outputTable},
			{page: 1, size: 0, output: outputTable},
			{page: 1, size: 10, output: "xml"},
		} {
			if err := runRecordAggregate("crm", "order", o); err == nil {
				t.Errorf("expected error for opts %+v", o)
			}
		}
	})

	t.Run("fails without credentials", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		MetaServerURL = "http://unused"
		if err := runRecordAggregate("crm", "order", baseOpts); err == nil {
			t.Fatal("expected error for missing credentials")
		}
	})

	t.Run("fails on API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 400, "msg": "alias required"})
		}))
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		err := runRecordAggregate("crm", "order", baseOpts)
		if err == nil || !strings.Contains(err.Error(), "alias required") {
			t.Fatalf("expected server error surfaced, got %v", err)
		}
	})
}

// columnsInOrder 判定各列名在表头中依次出现（缺席或乱序均为 false）
func columnsInOrder(header string, columns ...string) bool {
	last := -1
	for _, c := range columns {
		i := strings.Index(header, c)
		if i <= last {
			return false
		}
		last = i
	}
	return true
}

func TestFormatAggregateCell(t *testing.T) {
	cases := map[string]struct {
		in   any
		want string
	}{
		"dimension label": {map[string]any{"value": "3001", "label": "字节跳动"}, "字节跳动"},
		"integer number":  {float64(128), "128"},
		"decimal number":  {125300.5, "125300.5"},
		"large number":    {float64(1000000), "1000000"},
		"date string":     {"2026-03-05", "2026-03-05"},
	}
	for name, c := range cases {
		if got := formatAggregateCell(c.in); got != c.want {
			t.Errorf("%s: expected %q, got %q", name, c.want, got)
		}
	}
}
