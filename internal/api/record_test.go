/**
 * [INPUT]: 依赖 internal/api 包内的 Client（包内白盒），encoding/json、errors、net/http、net/http/httptest、strings
 * [OUTPUT]: 覆盖 CreateRecord / GetRecord / UpdateRecord / UpdateRecordsBatch / DeleteRecords / ListRecords / AggregateRecords 的单元测试（含 409 唯一性冲突 → UniqueConstraintError）
 * [POS]: internal/api record.go 的配套测试，用 httptest 隔离网络
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------- CreateRecord ----------------------------------

func TestRecordCreate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/data/v1/record" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if r.Header.Get("X-Make-Target") != "MakeService.CreateResource" {
				t.Errorf("unexpected X-Make-Target: %s", r.Header.Get("X-Make-Target"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["appKey"] != "myapp" || body["entityKey"] != "user" {
				t.Errorf("unexpected body: %v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "ok",
				"data": map[string]any{"recordID": "rec-001"},
			})
		}))
		defer srv.Close()

		id, err := New(srv.URL, "test-token").CreateRecord("myapp", "user", map[string]any{"name": "Jim"})
		if err != nil {
			t.Fatalf("CreateRecord: %v", err)
		}
		if id != "rec-001" {
			t.Errorf("expected recordID=rec-001, got %s", id)
		}
	})

	t.Run("API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 400, "msg": "invalid entity"})
		}))
		defer srv.Close()

		if _, err := New(srv.URL, "test-token").CreateRecord("myapp", "bad", nil); err == nil {
			t.Fatal("expected error on API failure")
		}
	})

	t.Run("409 surfaces UniqueConstraintError with constraint and fields", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 409, "msg": "Unique constraint violated",
				"data": map[string]any{"constraint": "uniq_email", "fields": []string{"email"}},
			})
		}))
		defer srv.Close()

		_, err := New(srv.URL, "test-token").CreateRecord("myapp", "user", map[string]any{"email": "a@b.com"})
		var uce *UniqueConstraintError
		if !errors.As(err, &uce) {
			t.Fatalf("expected *UniqueConstraintError, got %T: %v", err, err)
		}
		if uce.Constraint != "uniq_email" || len(uce.Fields) != 1 || uce.Fields[0] != "email" {
			t.Errorf("UniqueConstraintError = %+v, want constraint=uniq_email fields=[email]", uce)
		}
		if !strings.Contains(uce.Error(), "uniq_email") || !strings.Contains(uce.Error(), "email") {
			t.Errorf("Error() = %q, want it to name the constraint and field", uce.Error())
		}
	})
}

// ---------------------------------- GetRecord ----------------------------------

func TestRecordGet(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/data/v1/record" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if r.Header.Get("X-Make-Target") != "MakeService.GetResource" {
				t.Errorf("unexpected X-Make-Target: %s", r.Header.Get("X-Make-Target"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "ok",
				"data": map[string]any{"name": "Jim", "age": float64(30)},
			})
		}))
		defer srv.Close()

		data, err := New(srv.URL, "test-token").GetRecord("myapp", "user", "rec-001")
		if err != nil {
			t.Fatalf("GetRecord: %v", err)
		}
		if data["name"] != "Jim" {
			t.Errorf("unexpected name: %v", data["name"])
		}
	})

	t.Run("API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 404, "msg": "not found"})
		}))
		defer srv.Close()

		if _, err := New(srv.URL, "test-token").GetRecord("myapp", "user", "bad"); err == nil {
			t.Fatal("expected error on API failure")
		}
	})
}

// ---------------------------------- UpdateRecord ----------------------------------

func TestRecordUpdate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/data/v1/record" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if r.Header.Get("X-Make-Target") != "MakeService.UpdateResource" {
				t.Errorf("unexpected X-Make-Target: %s", r.Header.Get("X-Make-Target"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["recordID"] != "rec-001" {
				t.Errorf("unexpected recordID: %v", body["recordID"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "ok"})
		}))
		defer srv.Close()

		err := New(srv.URL, "test-token").UpdateRecord("myapp", "user", "rec-001", map[string]any{"name": "Yu"})
		if err != nil {
			t.Fatalf("UpdateRecord: %v", err)
		}
	})

	t.Run("API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "msg": "internal error"})
		}))
		defer srv.Close()

		if err := New(srv.URL, "test-token").UpdateRecord("myapp", "user", "rec-001", nil); err == nil {
			t.Fatal("expected error on API failure")
		}
	})

	t.Run("409 surfaces UniqueConstraintError through post path", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 409, "msg": "Unique constraint violated",
				"data": map[string]any{"constraint": "uniq_pm", "fields": []string{"project_id", "member_id"}},
			})
		}))
		defer srv.Close()

		err := New(srv.URL, "test-token").UpdateRecord("myapp", "pm", "rec-001", map[string]any{"member_id": "u1"})
		var uce *UniqueConstraintError
		if !errors.As(err, &uce) {
			t.Fatalf("expected *UniqueConstraintError, got %T: %v", err, err)
		}
		if uce.Constraint != "uniq_pm" || len(uce.Fields) != 2 {
			t.Errorf("UniqueConstraintError = %+v, want constraint=uniq_pm with 2 fields", uce)
		}
	})
}

// ---------------------------------- UpdateRecordsBatch ----------------------------------

func TestRecordUpdateBatch(t *testing.T) {
	t.Run("success with /data/v1/field path", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 核心断言：批量更新走 /data/v1/field 而非 /data/v1/record
			if r.URL.Path != "/data/v1/field" {
				t.Errorf("expected path /data/v1/field, got %s", r.URL.Path)
			}
			if r.Header.Get("X-Make-Target") != "MakeService.UpdateResource" {
				t.Errorf("unexpected X-Make-Target: %s", r.Header.Get("X-Make-Target"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			ids, ok := body["recordIDList"].([]any)
			if !ok || len(ids) != 2 {
				t.Errorf("expected recordIDList with 2 items, got %v", body["recordIDList"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "ok"})
		}))
		defer srv.Close()

		err := New(srv.URL, "test-token").UpdateRecordsBatch(
			"myapp", "user",
			[]string{"rec-001", "rec-002"},
			map[string]any{"status": "active"},
		)
		if err != nil {
			t.Fatalf("UpdateRecordsBatch: %v", err)
		}
	})

	t.Run("API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 400, "msg": "bad request"})
		}))
		defer srv.Close()

		err := New(srv.URL, "test-token").UpdateRecordsBatch("myapp", "user", []string{"x"}, nil)
		if err == nil {
			t.Fatal("expected error on API failure")
		}
	})
}

// ---------------------------------- DeleteRecords ----------------------------------

func TestRecordDelete(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/data/v1/record" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if r.Header.Get("X-Make-Target") != "MakeService.DeleteResource" {
				t.Errorf("unexpected X-Make-Target: %s", r.Header.Get("X-Make-Target"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			ids, ok := body["recordIDList"].([]any)
			if !ok || len(ids) != 2 {
				t.Errorf("expected recordIDList with 2 items, got %v", body["recordIDList"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "ok",
				"data": []map[string]any{
					{"recordID": "rec-001", "code": 200, "msg": "deleted"},
					{"recordID": "rec-002", "code": 200, "msg": "deleted"},
				},
			})
		}))
		defer srv.Close()

		results, err := New(srv.URL, "test-token").DeleteRecords("myapp", "user", []string{"rec-001", "rec-002"})
		if err != nil {
			t.Fatalf("DeleteRecords: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("expected 2 results, got %d", len(results))
		}
		if results[0].RecordID != "rec-001" || results[0].Code != 200 {
			t.Errorf("unexpected first result: %+v", results[0])
		}
	})

	t.Run("API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "msg": "internal error"})
		}))
		defer srv.Close()

		if _, err := New(srv.URL, "test-token").DeleteRecords("myapp", "user", []string{"x"}); err == nil {
			t.Fatal("expected error on API failure")
		}
	})
}

// ---------------------------------- ListRecords ----------------------------------

func TestRecordList(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/data/v1/record" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if r.Header.Get("X-Make-Target") != "MakeService.ListResources" {
				t.Errorf("unexpected X-Make-Target: %s", r.Header.Get("X-Make-Target"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["appKey"] != "myapp" || body["entityKey"] != "user" {
				t.Errorf("unexpected body: %v", body)
			}
			// 验证可选字段被发送
			if body["fields"] == nil {
				t.Error("expected fields in request body")
			}
			if body["sort"] == nil {
				t.Error("expected sort in request body")
			}
			// 验证 filter 包成 Expression 对象（CEL 原样透传）
			obj, ok := body["filter"].(map[string]any)
			if !ok {
				t.Errorf("expected filter to be Expression object, got %T", body["filter"])
			} else if obj["expression"] != "amount >= 100" {
				t.Errorf("expected expression 'amount >= 100', got %v", obj["expression"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "ok",
				"data": []map[string]any{
					{"name": "Jim", "age": 30},
					{"name": "Yu", "age": 25},
				},
				"pagination": map[string]any{"total": 50},
			})
		}))
		defer srv.Close()

		records, total, err := New(srv.URL, "test-token").ListRecords("myapp", "user", ListRecordOpts{
			Fields: []string{"name", "age"},
			Sort:   []SortField{{FieldKey: "age", Order: "desc"}},
			Filter: "amount >= 100",
			Page:   1,
			Size:   10,
		})
		if err != nil {
			t.Fatalf("ListRecords: %v", err)
		}
		if total != 50 {
			t.Errorf("expected total=50, got %d", total)
		}
		if len(records) != 2 {
			t.Errorf("expected 2 records, got %d", len(records))
		}
		if records[0]["name"] != "Jim" {
			t.Errorf("unexpected first record name: %v", records[0]["name"])
		}
	})

	t.Run("without optional fields", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			// 验证可选字段不被发送
			if body["fields"] != nil {
				t.Error("expected no fields in request body")
			}
			if body["sort"] != nil {
				t.Error("expected no sort in request body")
			}
			if body["filter"] != nil {
				t.Error("expected no filter in request body")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "ok",
				"data":       []map[string]any{},
				"pagination": map[string]any{"total": 0},
			})
		}))
		defer srv.Close()

		records, total, err := New(srv.URL, "test-token").ListRecords("myapp", "user", ListRecordOpts{
			Page: 1, Size: 10,
		})
		if err != nil {
			t.Fatalf("ListRecords: %v", err)
		}
		if total != 0 || len(records) != 0 {
			t.Errorf("expected empty result, got records=%d total=%d", len(records), total)
		}
	})

	t.Run("API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "msg": "internal error"})
		}))
		defer srv.Close()

		if _, _, err := New(srv.URL, "test-token").ListRecords("myapp", "user", ListRecordOpts{Page: 1, Size: 10}); err == nil {
			t.Fatal("expected error on API failure")
		}
	})
}

func TestRecordAggregate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/data/v1/aggregate" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if r.Header.Get("X-Make-Target") != "MakeService.ListResources" {
				t.Errorf("unexpected X-Make-Target: %s", r.Header.Get("X-Make-Target"))
			}
			var body struct {
				AppKey          string           `json:"appKey"`
				EntityKey       string           `json:"entityKey"`
				Group           []map[string]any `json:"group"`
				Aggregates      []map[string]any `json:"aggregates"`
				Filter          map[string]any   `json:"filter"`
				AggregateFilter map[string]any   `json:"aggregateFilter"`
				Sort            []map[string]any `json:"sort"`
				Pagination      map[string]any   `json:"pagination"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.AppKey != "crm" || body.EntityKey != "order" {
				t.Errorf("unexpected keys: %s/%s", body.AppKey, body.EntityKey)
			}
			// group：granularity/alias 按 omitempty 只在设置时出现
			if len(body.Group) != 2 || body.Group[0]["fieldKey"] != "status" || body.Group[1]["granularity"] != "month" || body.Group[1]["alias"] != "month" {
				t.Errorf("unexpected group: %v", body.Group)
			}
			if _, has := body.Group[0]["granularity"]; has {
				t.Errorf("expected granularity omitted for plain group, got %v", body.Group[0])
			}
			// aggregates：count 不带 fieldKey
			if len(body.Aggregates) != 2 || body.Aggregates[0]["aggregate"] != "count" || body.Aggregates[1]["fieldKey"] != "amount" {
				t.Errorf("unexpected aggregates: %v", body.Aggregates)
			}
			if _, has := body.Aggregates[0]["fieldKey"]; has {
				t.Errorf("expected fieldKey omitted for count, got %v", body.Aggregates[0])
			}
			if body.Filter["expression"] != "status != 'draft'" || body.AggregateFilter["expression"] != "totalAmount > 10000" {
				t.Errorf("unexpected filters: %v / %v", body.Filter, body.AggregateFilter)
			}
			// sort 以 alias 引用指标时不发 fieldKey
			if len(body.Sort) != 1 || body.Sort[0]["alias"] != "totalAmount" || body.Sort[0]["order"] != "desc" {
				t.Errorf("unexpected sort: %v", body.Sort)
			}
			if _, has := body.Sort[0]["fieldKey"]; has {
				t.Errorf("expected fieldKey omitted for alias sort, got %v", body.Sort[0])
			}
			if body.Pagination["page"] != float64(2) || body.Pagination["size"] != float64(5) {
				t.Errorf("unexpected pagination: %v", body.Pagination)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "Aggregate records success",
				"data": []map[string]any{
					{"status": map[string]any{"value": "completed", "label": "已完成"}, "orderCount": 128, "totalAmount": 125300.5},
				},
				"pagination": map[string]any{"page": 2, "size": 5, "total": 7},
			})
		}))
		defer srv.Close()

		rows, total, err := New(srv.URL, "test-token").AggregateRecords("crm", "order", AggregateOpts{
			Group: []GroupField{
				{FieldKey: "status"},
				{FieldKey: "orderDate", Granularity: "month", Alias: "month"},
			},
			Aggregates: []AggregateField{
				{Aggregate: "count", Alias: "orderCount"},
				{FieldKey: "amount", Aggregate: "sum", Alias: "totalAmount"},
			},
			Filter:          "status != 'draft'",
			AggregateFilter: "totalAmount > 10000",
			Sort:            []SortField{{Alias: "totalAmount", Order: "desc"}},
			Page:            2,
			Size:            5,
		})
		if err != nil {
			t.Fatalf("AggregateRecords: %v", err)
		}
		if total != 7 {
			t.Errorf("expected total=7, got %d", total)
		}
		if len(rows) != 1 || rows[0]["orderCount"] != float64(128) {
			t.Errorf("unexpected rows: %v", rows)
		}
	})

	t.Run("without optional fields", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			for _, key := range []string{"group", "sort", "filter", "aggregateFilter"} {
				if body[key] != nil {
					t.Errorf("expected no %s in request body, got %v", key, body[key])
				}
			}
			if body["aggregates"] == nil {
				t.Error("expected aggregates in request body")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "ok",
				"data":       []map[string]any{{"total": 42}},
				"pagination": map[string]any{"total": 1},
			})
		}))
		defer srv.Close()

		rows, total, err := New(srv.URL, "test-token").AggregateRecords("crm", "order", AggregateOpts{
			Aggregates: []AggregateField{{Aggregate: "count", Alias: "total"}},
			Page:       1, Size: 10,
		})
		if err != nil {
			t.Fatalf("AggregateRecords: %v", err)
		}
		if total != 1 || len(rows) != 1 {
			t.Errorf("expected single global row, got rows=%d total=%d", len(rows), total)
		}
	})

	t.Run("API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 400, "msg": "alias required"})
		}))
		defer srv.Close()

		_, _, err := New(srv.URL, "test-token").AggregateRecords("crm", "order", AggregateOpts{Page: 1, Size: 10})
		if err == nil {
			t.Fatal("expected error on API failure")
		}
	})
}
