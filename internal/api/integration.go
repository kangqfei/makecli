/**
 * [INPUT]: 依赖 bytes、encoding/json、fmt、io、mime/multipart、net/http、net/url、path/filepath、strconv，依赖 debug.go 的 debugSink / debugRequest，依赖 internal/trace 的 TraceID/Traceparent
 * [OUTPUT]: 对外提供 OCROptions 类型、Client.OCR(filename, reader, opts) 方法，返回 OCR 服务 data 字段（map[string]any，已递归剥除 position 坐标字段）
 * [POS]: internal/api 的 integration 子层，封装 Make Integration 服务（/integration/v1/ocr）的 HTTP 调用，与 client.go (Meta/Data) 平级
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"

	"github.com/qfeius/makecli/internal/trace"
)

// ---------------------------------- OCR ----------------------------------

const ocrEndpoint = "/integration/v1/ocr"

// OCROptions 控制 OCR 请求的可选参数（multipart 字段 + URL query）
//
// multipart 字段:
//
//	BusinessID  - 业务单据 ID（0 表示不传）
//	VerifyVAT   - 是否开启发票联网验真，nil 时使用服务端默认（true），非 nil 时显式传值
//
// Query 参数（与 spec 中 0/1 布尔语义对齐，false 时不发送，true 时发送 1）:
//
//	CoordRestoreOriginal     - 坐标基准是否对齐原图（默认 false → 切图基准）
//	SpecificPages            - 指定识别的页码（如 "1,3,2" / "2-4"），空字符串不传
//	CropCompleteImage        - 是否输出票据切片 base64
//	CropValueImage           - 是否输出关键字段切片 base64
//	MergeDigitalElecInvoice  - 是否合并多页全电票
//	ReturnPPI                - 是否返回 PDF 解码 PPI
type OCROptions struct {
	BusinessID              int64
	VerifyVAT               *bool
	CoordRestoreOriginal    bool
	SpecificPages           string
	CropCompleteImage       bool
	CropValueImage          bool
	MergeDigitalElecInvoice bool
	ReturnPPI               bool
}

// queryString 把 OCROptions 中的 query 参数序列化为 URL query
func (o OCROptions) queryString() string {
	q := url.Values{}
	if o.CoordRestoreOriginal {
		q.Set("coord_restore", "1")
	}
	if o.SpecificPages != "" {
		q.Set("specific_pages", o.SpecificPages)
	}
	if o.CropCompleteImage {
		q.Set("crop_complete_image", "1")
	}
	if o.CropValueImage {
		q.Set("crop_value_image", "1")
	}
	if o.MergeDigitalElecInvoice {
		q.Set("merge_digital_elec_invoice", "1")
	}
	if o.ReturnPPI {
		q.Set("return_ppi", "1")
	}
	return q.Encode()
}

// OCR 上传本地文件给 OCR 服务并返回响应 data 字段
//
// filename: 仅用于 multipart 的 filename 部分，调用方负责传入正确的扩展名
// content:  文件内容流，调用方负责打开/关闭
// opts:     可选参数（multipart business_id / verify_vat + 6 个 URL query 控制）
func (c *Client) OCR(filename string, content io.Reader, opts OCROptions) (map[string]any, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filepath.Base(filename))
	if err != nil {
		return nil, fmt.Errorf("创建 multipart file 字段失败: %w", err)
	}
	if _, err := io.Copy(fw, content); err != nil {
		return nil, fmt.Errorf("写入文件内容失败: %w", err)
	}
	if opts.BusinessID > 0 {
		if err := mw.WriteField("business_id", strconv.FormatInt(opts.BusinessID, 10)); err != nil {
			return nil, fmt.Errorf("写入 multipart business_id 字段失败: %w", err)
		}
	}
	if opts.VerifyVAT != nil {
		if err := mw.WriteField("verify_vat", strconv.FormatBool(*opts.VerifyVAT)); err != nil {
			return nil, fmt.Errorf("写入 multipart verify_vat 字段失败: %w", err)
		}
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("关闭 multipart writer 失败: %w", err)
	}

	endpoint := c.baseURL + ocrEndpoint
	if q := opts.queryString(); q != "" {
		endpoint += "?" + q
	}
	contentType := mw.FormDataContentType()

	// trace 头：与 do() 同源，trace-id 全程稳定，parent-id 每请求新生成
	traceparent, logID := trace.Traceparent(), trace.TraceID()

	// 请求头有序收集：一份数据同时喂 http.Request 与 --debug 转储
	headers := [][2]string{
		{"Content-Type", contentType},
		{"Authorization", "Bearer " + c.token},
		{"Traceparent", traceparent},
		{"X-Log-Id", logID},
	}
	for k, v := range c.headers {
		headers = append(headers, [2]string{k, v})
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, &buf)
	if err != nil {
		return nil, err
	}
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	if c.debug != nil {
		form := [][2]string{{"file", "@" + filename}}
		if opts.BusinessID > 0 {
			form = append(form, [2]string{"business_id", strconv.FormatInt(opts.BusinessID, 10)})
		}
		if opts.VerifyVAT != nil {
			form = append(form, [2]string{"verify_vat", strconv.FormatBool(*opts.VerifyVAT)})
		}
		c.debug.request(debugRequest{Method: http.MethodPost, URL: endpoint, Headers: headers, Form: form})
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if c.debug != nil {
		c.debug.response(resp, raw)
	}

	var result struct {
		Code      int            `json:"code"`
		Message   string         `json:"msg"`
		RequestID string         `json:"request_id"`
		Data      map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("无效的响应格式: %w", err)
	}
	if result.Code == authFailedCode {
		return nil, authFailedErr(result.Code, result.Message)
	}
	if result.Code != 200 {
		return nil, fmt.Errorf("API 错误 [%d]: %s", result.Code, result.Message)
	}
	stripPosition(result.Data)
	return result.Data, nil
}

// stripPosition 递归剥除所有名为 "position" 的字段（坐标信息对 CLI 用户无价值，过滤后输出更干净）
func stripPosition(v any) {
	switch x := v.(type) {
	case map[string]any:
		delete(x, "position")
		for _, vv := range x {
			stripPosition(vv)
		}
	case []any:
		for _, vv := range x {
			stripPosition(vv)
		}
	}
}
