/**
 * [INPUT]: 依赖 api 包内的 Client.PromoteApp / Client.GetPromoteStatus / PromoteStatus（包内白盒），encoding/json、errors、net/http、net/http/httptest、testing
 * [OUTPUT]: 覆盖发布接口的单元测试（PromoteApp 请求形态 / 回执解析 / 缺 promoteId 报错 / 404 → ErrNotFound / 业务错误；GetPromoteStatus 请求携带 promoteId / 字段解析（ID 字符串与数字两形态）/ 空 state → ErrNotFound）+ Finished/Succeeded 终态判定表测
 * [POS]: internal/api 模块 promote.go 的配套测试，用 httptest 隔离网络
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

// TestPromoteStatusStates 覆盖终态判定：五个终态 Finished 为真，进行态为假，Succeeded 只认 SUCCEEDED。
func TestPromoteStatusStates(t *testing.T) {
	cases := []struct {
		state     string
		finished  bool
		succeeded bool
	}{
		{PromoteStateSucceeded, true, true},
		{PromoteStateFailed, true, false},
		{PromoteStateCanceled, true, false},
		{PromoteStateTerminated, true, false},
		{PromoteStateTimedOut, true, false},
		{"RUNNING", false, false},
		{"", false, false},
	}
	for _, c := range cases {
		st := &PromoteStatus{State: c.state}
		if got := st.Finished(); got != c.finished {
			t.Errorf("Finished(%q) = %v, want %v", c.state, got, c.finished)
		}
		if got := st.Succeeded(); got != c.succeeded {
			t.Errorf("Succeeded(%q) = %v, want %v", c.state, got, c.succeeded)
		}
	}
}

// promoteServer 起一个记录请求形态并回固定响应体的 mock
func promoteServer(t *testing.T, response string) (*httptest.Server, *string, *string, *map[string]any) {
	t.Helper()
	var gotTarget, gotPath string
	gotBody := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTarget = r.Header.Get("X-Make-Target")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotTarget, &gotPath, &gotBody
}

func TestPromoteApp(t *testing.T) {
	t.Run("sends CreateResource with beta key and parses run", func(t *testing.T) {
		srv, target, path, body := promoteServer(t, `{"code":200,"msg":"成功","data":{"key":"myapp_beta_","type":"Make.App",
			"properties":{"promoteId":"7f96a378"}}}`)
		run, err := New(srv.URL, "tok").PromoteApp("myapp_beta_")
		if err != nil {
			t.Fatal(err)
		}
		if *target != "MakeService.CreateResource" || *path != "/console/v1/app-environment/product" {
			t.Fatalf("unexpected request: target=%q path=%q", *target, *path)
		}
		if (*body)["key"] != "myapp_beta_" || (*body)["type"] != "Make.App" {
			t.Fatalf("unexpected body: %v", *body)
		}
		if run.PromoteID != "7f96a378" {
			t.Fatalf("unexpected run: %+v", run)
		}
	})

	t.Run("missing promoteId is a contract error", func(t *testing.T) {
		srv, _, _, _ := promoteServer(t, `{"code":200,"msg":"ok","data":{"properties":{}}}`)
		if _, err := New(srv.URL, "tok").PromoteApp("myapp_beta_"); err == nil {
			t.Fatal("expected error for empty run receipt")
		}
	})

	t.Run("404 maps to ErrNotFound", func(t *testing.T) {
		srv, _, _, _ := promoteServer(t, `{"code":404,"msg":"beta app not found"}`)
		_, err := New(srv.URL, "tok").PromoteApp("nope_beta_")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("business error surfaces code and message", func(t *testing.T) {
		srv, _, _, _ := promoteServer(t, `{"code":409,"msg":"beta app is migrating"}`)
		_, err := New(srv.URL, "tok").PromoteApp("myapp_beta_")
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("expected plain API error, got %v", err)
		}
	})
}

func TestGetPromoteStatus(t *testing.T) {
	t.Run("sends StatusResource with promoteId, parses progress", func(t *testing.T) {
		srv, target, path, body := promoteServer(t, `{"code":200,"msg":"成功","data":{"key":"myapp_beta_","properties":{
			"promoteId":"7f96a378","type":"PUBLISH_PRODUCT",
			"state":"SUCCEEDED","step":"COMPLETED","productAppId":"6098","previewAppId":6142,
			"sourceBuildTaskId":"1257","productBuildTaskId":1284,
			"steps":[{"key":"PREPARING_PRODUCT_PUBLISH","name":"检查发布版本","state":"SUCCEEDED"}]}}}`)
		st, err := New(srv.URL, "tok").GetPromoteStatus("myapp_beta_", "7f96a378")
		if err != nil {
			t.Fatal(err)
		}
		if *target != "MakeService.StatusResource" || *path != "/console/v1/app-environment/product" {
			t.Fatalf("unexpected request: target=%q path=%q", *target, *path)
		}
		props, _ := (*body)["properties"].(map[string]any)
		if props["promoteId"] != "7f96a378" {
			t.Fatalf("status request must carry promoteId: %v", *body)
		}
		if st.State != PromoteStateSucceeded || st.Step != "COMPLETED" || st.Type != "PUBLISH_PRODUCT" {
			t.Fatalf("unexpected status: %+v", st)
		}
		// ID 字段字符串与数字两形态都能落到 json.Number
		if st.ProductAppID.String() != "6098" || st.PreviewAppID.String() != "6142" ||
			st.SourceBuildTaskID.String() != "1257" || st.ProductBuildTaskID.String() != "1284" {
			t.Fatalf("id fields not decoded: %+v", st)
		}
		if len(st.Steps) != 1 || st.Steps[0].Name != "检查发布版本" {
			t.Fatalf("steps not decoded: %+v", st.Steps)
		}
	})

	t.Run("empty state is not found", func(t *testing.T) {
		srv, _, _, _ := promoteServer(t, `{"code":200,"msg":"ok","data":{"properties":{}}}`)
		_, err := New(srv.URL, "tok").GetPromoteStatus("myapp_beta_", "7f96a378")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})
}
