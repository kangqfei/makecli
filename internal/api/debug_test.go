/**
 * [INPUT]: 依赖 api 包内的 debugSink / debugRequest / newDebugSink（包内白盒），bytes、encoding/json、net/http、net/http/httptest、strings、testing
 * [OUTPUT]: 覆盖 --debug 文本渲染（摘要头 + 可复制 curl + 缩进 body）、JSON 渲染（{request,response,timing} 形态）、非 JSON 响应体退化、humanBytes 的单元测试
 * [POS]: internal/api 模块 debug.go 的配套测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// roundTrip 走一次真实 httptest 请求，把 sink 的输出抓到 buf
func roundTrip(t *testing.T, format DebugFormat, respBody string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	c := New(srv.URL, "tok", WithDebug(true, format))
	c.debug.w = &buf
	c.debug.color = false
	_ = c.CreateApp("demo", "演示", nil)
	return buf.String()
}

func TestDebugText(t *testing.T) {
	out := roundTrip(t, DebugText, `{"code":200,"data":{"key":"demo"}}`)
	for _, want := range []string{
		"→ POST http://", "/meta/v1/app  MakeService.CreateResource\n",
		"\n  curl -X POST 'http://", "\n    -H 'Authorization: Bearer tok' \\\n",
		"\n    -d '{\"key\":\"demo\"",
		"← 200 OK  ", "  34 B\n",
		"\n  {\n    \"code\": 200,\n    \"data\": {\n      \"key\": \"demo\"\n    }\n  }\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestDebugTextNonJSONBody(t *testing.T) {
	out := roundTrip(t, DebugText, "<html>oops</html>")
	if !strings.Contains(out, "\n  <html>oops</html>\n") {
		t.Errorf("non-JSON body should be echoed verbatim, got:\n%s", out)
	}
}

func TestDebugJSON(t *testing.T) {
	out := roundTrip(t, DebugJSON, `{"code":200,"data":{"key":"demo"}}`)
	var entry struct {
		Request struct {
			Method  string            `json:"method"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Body    map[string]any    `json:"body"`
		} `json:"request"`
		Response struct {
			Status  int               `json:"status"`
			Headers map[string]string `json:"headers"`
			Body    map[string]any    `json:"body"`
		} `json:"response"`
		Timing struct {
			TotalMS *int64 `json:"total_ms"`
		} `json:"timing"`
	}
	if err := json.Unmarshal([]byte(out), &entry); err != nil {
		t.Fatalf("output is not a single JSON object: %v\n%s", err, out)
	}
	if entry.Request.Method != "POST" || !strings.HasSuffix(entry.Request.URL, "/meta/v1/app") {
		t.Errorf("request = %+v", entry.Request)
	}
	if entry.Request.Headers["X-Make-Target"] != "MakeService.CreateResource" || entry.Request.Headers["Authorization"] != "Bearer tok" {
		t.Errorf("request headers = %v", entry.Request.Headers)
	}
	if entry.Request.Body["key"] != "demo" {
		t.Errorf("request body = %v", entry.Request.Body)
	}
	if entry.Response.Status != 200 || entry.Response.Headers["content-type"] != "application/json" {
		t.Errorf("response status/headers = %d %v", entry.Response.Status, entry.Response.Headers)
	}
	if entry.Response.Body["code"] != float64(200) {
		t.Errorf("response body = %v", entry.Response.Body)
	}
	if entry.Timing.TotalMS == nil {
		t.Error("timing.total_ms missing")
	}
}

func TestDebugJSONNonJSONBody(t *testing.T) {
	out := roundTrip(t, DebugJSON, "<html>oops</html>")
	var entry struct {
		Response struct {
			Body string `json:"body"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(out), &entry); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if entry.Response.Body != "<html>oops</html>" {
		t.Errorf("non-JSON body should degrade to string, got %q", entry.Response.Body)
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int]string{0: "0 B", 512: "512 B", 1536: "1.5 KB", 3 << 20: "3.0 MB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
