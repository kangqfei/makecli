/**
 * [INPUT]: 依赖 bytes、encoding/json、fmt、io、net/http、os、strings、time，github.com/mattn/go-isatty
 * [OUTPUT]: 对外提供 DebugFormat 枚举（DebugText / DebugJSON）；包内提供 debugSink 类型（request / response 渲染）、
 *           debugRequest 数据结构、newDebugSink 构造（stderr + TTY/NO_COLOR 判色）、humanBytes
 * [POS]: internal/api 的 --debug 渲染层。一份结构化数据、两种渲染：
 *        DebugText 是 curl -v 风格（→ 出站 / ← 入站摘要头按 HTTP 状态着色 + 两格缩进正文，curl 命令可直接复制）；
 *        DebugJSON 每请求一个 {request,response,timing} 对象，供 --output json 的 agent/管道消费。
 *        request() 与 integration.go 的 OCR 上传共用
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
)

// DebugFormat 选择 --debug 的渲染形态
type DebugFormat int

const (
	DebugText DebugFormat = iota // curl -v 风格，给人看
	DebugJSON                    // 每请求一个 JSON 对象，给 agent/管道看
)

// ---------------------------------- 颜色 ----------------------------------

const (
	ansiReset  = "\x1b[0m"
	ansiCyan   = "\x1b[36m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
	ansiDim    = "\x1b[2m"
)

// statusColor 按 HTTP 状态段选色：2xx 绿、4xx 黄、5xx 红，其余不着色
func statusColor(code int) string {
	switch {
	case code >= 500:
		return ansiRed
	case code >= 400:
		return ansiYellow
	case code >= 200 && code < 300:
		return ansiGreen
	}
	return ""
}

// ---------------------------------- 数据 ----------------------------------

// debugRequest 是一次出站请求的转储素材。Headers 有序（渲染顺序稳定）；
// Body 与 Form 二选一：JSON 请求填 Body，multipart 上传填 Form（file 值形如 "@name"）
type debugRequest struct {
	Method  string
	URL     string
	Headers [][2]string
	Body    []byte
	Form    [][2]string
}

// ---------------------------------- 渲染器 ----------------------------------

// debugSink 是 --debug 的渲染器；Client 持 nil 即关闭
type debugSink struct {
	w       io.Writer
	format  DebugFormat
	color   bool
	start   time.Time    // 最近一次 request 的起点，response 据此算耗时
	pending debugRequest // JSON 模式暂存的请求，与响应合成一个对象
}

// newDebugSink 输出到 stderr；文本模式仅当 stderr 是 TTY 且未设 NO_COLOR 时上色
func newDebugSink(format DebugFormat) *debugSink {
	_, noColor := os.LookupEnv("NO_COLOR")
	return &debugSink{
		w:      os.Stderr,
		format: format,
		color:  !noColor && isatty.IsTerminal(os.Stderr.Fd()),
	}
}

// paint 在启用颜色时包裹 ANSI 序列
func (d *debugSink) paint(color, s string) string {
	if !d.color || color == "" {
		return s
	}
	return color + s + ansiReset
}

// request 记录出站起点；文本模式立即渲染摘要头与 curl 命令，JSON 模式暂存等响应一起出
func (d *debugSink) request(r debugRequest) {
	d.start = time.Now()
	if d.format == DebugJSON {
		d.pending = r
		return
	}
	target := ""
	for _, h := range r.Headers {
		if h[0] == "X-Make-Target" {
			target = h[1]
		}
	}
	head := d.paint(ansiCyan, "→ "+r.Method+" "+r.URL) + "  " + d.paint(ansiDim, target)
	_, _ = fmt.Fprintf(d.w, "\n%s\n%s\n", strings.TrimRight(head, " "), indent(curlOf(r)))
}

// response 渲染入站：文本模式是摘要头 + 缩进美化的 body，JSON 模式是完整 {request,response,timing}
func (d *debugSink) response(resp *http.Response, raw []byte) {
	dur := time.Since(d.start)
	if d.format == DebugJSON {
		d.writeJSON(resp, raw, dur)
		return
	}
	head := d.paint(statusColor(resp.StatusCode), "← "+resp.Status) + "  " +
		d.paint(ansiDim, fmt.Sprintf("%s  %s", dur.Round(time.Millisecond), humanBytes(len(raw))))
	_, _ = fmt.Fprintf(d.w, "\n%s\n%s\n\n", head, indent(string(prettyJSON(raw))))
}

// debugEntry 是 JSON 模式的输出形态；struct 而非 map，让字段按阅读顺序（method → url → headers → body）而非字母序
type debugEntry struct {
	Request struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Body    any               `json:"body,omitempty"`
		Form    map[string]string `json:"form,omitempty"`
	} `json:"request"`
	Response struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    any               `json:"body"`
	} `json:"response"`
	Timing struct {
		TotalMS int64 `json:"total_ms"`
	} `json:"timing"`
}

// writeJSON 把暂存的请求与本次响应合成一个对象写出
func (d *debugSink) writeJSON(resp *http.Response, raw []byte, dur time.Duration) {
	r := d.pending
	var e debugEntry
	e.Request.Method, e.Request.URL, e.Request.Headers = r.Method, r.URL, headerMap(r.Headers)
	if r.Body != nil {
		e.Request.Body = jsonOrString(r.Body)
	}
	if r.Form != nil {
		e.Request.Form = headerMap(r.Form)
	}
	e.Response.Status, e.Response.Body = resp.StatusCode, jsonOrString(raw)
	e.Response.Headers = make(map[string]string, len(resp.Header))
	for k, v := range resp.Header {
		e.Response.Headers[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	e.Timing.TotalMS = dur.Milliseconds()
	out, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		out = []byte(fmt.Sprintf(`{"error":%q}`, err))
	}
	_, _ = fmt.Fprintf(d.w, "%s\n", out)
}

// ---------------------------------- 工具 ----------------------------------

// curlOf 把请求素材拼成可复制的 curl 命令（各段先收集再 join，免行尾悬空反斜杠）
func curlOf(r debugRequest) string {
	parts := []string{fmt.Sprintf("curl -X %s '%s'", r.Method, r.URL)}
	for _, h := range r.Headers {
		parts = append(parts, fmt.Sprintf("-H '%s: %s'", h[0], h[1]))
	}
	for _, f := range r.Form {
		parts = append(parts, fmt.Sprintf("-F '%s=%s'", f[0], f[1]))
	}
	if r.Body != nil {
		parts = append(parts, fmt.Sprintf("-d '%s'", r.Body))
	}
	return strings.Join(parts, " \\\n  ")
}

// headerMap 把有序键值对折成 map（JSON 输出用）
func headerMap(kv [][2]string) map[string]string {
	m := make(map[string]string, len(kv))
	for _, h := range kv {
		m[h[0]] = h[1]
	}
	return m
}

// jsonOrString 合法 JSON 原样内嵌，否则退化为字符串，绝不因格式问题吞掉信息
func jsonOrString(raw []byte) any {
	if json.Valid(raw) {
		return json.RawMessage(raw)
	}
	return string(raw)
}

// prettyJSON 合法 JSON 缩进美化，否则原样返回
func prettyJSON(raw []byte) []byte {
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") != nil {
		return raw
	}
	return pretty.Bytes()
}

// indent 给每行加两格缩进，让正文在摘要头下视觉分层
func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}

// humanBytes 把字节数渲染成人读单位（B / KB / MB），一位小数
func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
