/**
 * [INPUT]: 依赖 encoding/json、fmt，依赖同包 Client.do / Client.post 方法、writeStatusErr（非 200 翻译，含 409 唯一性冲突，收口于 client.go）
 * [OUTPUT]: 对外提供 DeleteRecordResult / SortField / ListRecordOpts / GroupField / AggregateField / AggregateOpts 类型、CreateRecord / GetRecord / UpdateRecord / UpdateRecordsBatch / DeleteRecords / ListRecords / AggregateRecords 方法（写方法违反唯一性约束时返回 UniqueConstraintError）
 * [POS]: internal/api 的 Data Service 层，封装 Record CRUD 与聚合统计（/data/v1/aggregate，声明式 GROUP BY），与 client.go 的 Meta Service 层平级；ListRecords / AggregateRecords 共用 listPage 解码分页响应
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package api

import (
	"encoding/json"
	"fmt"
)

// ---------------------------------- 数据类型 ----------------------------------

// DeleteRecordResult 描述批量删除中单条记录的处理结果
type DeleteRecordResult struct {
	RecordID string `json:"recordID"`
	Code     int    `json:"code"`
	Message  string `json:"msg"`
}

// SortField 描述排序键与方向：Record 列表以 FieldKey 引用字段，聚合查询还可以 Alias 引用 group / aggregates 声明的别名。
// 两个引用键互斥且由服务端裁决，CLI 只透传
type SortField struct {
	FieldKey string `json:"fieldKey,omitempty"`
	Alias    string `json:"alias,omitempty"`
	Order    string `json:"order"` // "asc" | "desc"
}

// ListRecordOpts 封装 ListRecords 的可选参数
// Filter 为可选的 CEL 表达式文本（原样透传，由服务端校验），空串时不发送
type ListRecordOpts struct {
	Fields []string
	Sort   []SortField
	Filter string
	Page   int
	Size   int
}

// GroupField 是聚合查询的分组维度（DataAPIDesign「聚合统计 Record 数据」group 元素）：
// FieldKey 须为 Schema 中 capabilities.aggregable=true 的字段（与列表分组的 groupable 独立，Lookup 按关联 ID 分组），
// Granularity 仅 Date 字段可用（day/week/month/quarter/year），Alias 缺省时输出列名为 FieldKey；取值合法性由服务端裁决
type GroupField struct {
	FieldKey    string `json:"fieldKey"`
	Granularity string `json:"granularity,omitempty"`
	Alias       string `json:"alias,omitempty"`
}

// AggregateField 是聚合查询的指标（aggregates 元素）：count 不带 FieldKey；Alias 是该指标在响应、aggregateFilter 与 sort 中的引用名
type AggregateField struct {
	FieldKey  string `json:"fieldKey,omitempty"`
	Aggregate string `json:"aggregate"`
	Alias     string `json:"alias"`
}

// AggregateOpts 封装 AggregateRecords 的参数
// Filter（聚合前，WHERE）与 AggregateFilter（聚合后，HAVING）均为 CEL 文本，空串时不发送；Group 为空即单行全局聚合
type AggregateOpts struct {
	Group           []GroupField
	Aggregates      []AggregateField
	Filter          string
	AggregateFilter string
	Sort            []SortField
	Page            int
	Size            int
}

// ---------------------------------- Record 操作 ----------------------------------

// CreateRecord 调用 MakeService.CreateResource 创建一条记录
// 返回新创建记录的 recordID
func (c *Client) CreateRecord(appKey, entityKey string, data map[string]any) (string, error) {
	reqBody := map[string]any{
		"appKey":    appKey,
		"entityKey": entityKey,
		"data":      data,
	}
	var result struct {
		Code    int             `json:"code"`
		Message string          `json:"msg"`
		Data    json.RawMessage `json:"data"` // 成败两种形态，判完 code 再按需解析
	}
	if err := c.do("MakeService.CreateResource", "/data/v1/record", reqBody, &result); err != nil {
		return "", err
	}
	if result.Code != 200 {
		return "", writeStatusErr(result.Code, result.Message, result.Data)
	}
	var created struct {
		RecordID string `json:"recordID"`
	}
	if err := json.Unmarshal(result.Data, &created); err != nil {
		return "", fmt.Errorf("无效的响应格式: %w", err)
	}
	return created.RecordID, nil
}

// GetRecord 调用 MakeService.GetResource 获取单条记录
// 返回记录的动态字段 map
func (c *Client) GetRecord(appKey, entityKey, recordID string) (map[string]any, error) {
	reqBody := map[string]any{
		"appKey":    appKey,
		"entityKey": entityKey,
		"recordID":  recordID,
	}
	var result struct {
		Code    int            `json:"code"`
		Message string         `json:"msg"`
		Data    map[string]any `json:"data"`
	}
	if err := c.do("MakeService.GetResource", "/data/v1/record", reqBody, &result); err != nil {
		return nil, err
	}
	if result.Code != 200 {
		return nil, fmt.Errorf("API 错误 [%d]: %s", result.Code, result.Message)
	}
	return result.Data, nil
}

// UpdateRecord 调用 MakeService.UpdateResource 更新单条记录
func (c *Client) UpdateRecord(appKey, entityKey, recordID string, data map[string]any) error {
	body := map[string]any{
		"appKey":    appKey,
		"entityKey": entityKey,
		"recordID":  recordID,
		"data":      data,
	}
	return c.post("MakeService.UpdateResource", "/data/v1/record", body)
}

// UpdateRecordsBatch 调用 MakeService.UpdateResource 批量更新多条记录
//
// 路由设计：CLI 的 `record update` 命令根据 recordID 数量透明路由——
// 单条走 UpdateRecord（/data/v1/record），多条走本方法（/data/v1/field）。
// 用户无需感知两个不同的 API 端点。
// data 的 key 应该是 fieldKey（英文标识符）。
func (c *Client) UpdateRecordsBatch(appKey, entityKey string, recordIDs []string, data map[string]any) error {
	body := map[string]any{
		"appKey":       appKey,
		"entityKey":    entityKey,
		"recordIDList": recordIDs,
		"data":         data,
	}
	return c.post("MakeService.UpdateResource", "/data/v1/field", body)
}

// DeleteRecords 调用 MakeService.DeleteResource 批量删除记录
// 返回每条记录的删除结果
func (c *Client) DeleteRecords(appKey, entityKey string, recordIDs []string) ([]DeleteRecordResult, error) {
	reqBody := map[string]any{
		"appKey":       appKey,
		"entityKey":    entityKey,
		"recordIDList": recordIDs,
	}
	var result struct {
		Code    int                  `json:"code"`
		Message string               `json:"msg"`
		Data    []DeleteRecordResult `json:"data"`
	}
	if err := c.do("MakeService.DeleteResource", "/data/v1/record", reqBody, &result); err != nil {
		return nil, err
	}
	if result.Code != 200 {
		return nil, fmt.Errorf("API 错误 [%d]: %s", result.Code, result.Message)
	}
	return result.Data, nil
}

// ListRecords 调用 MakeService.ListResources 分页查询记录列表
// 返回记录列表和服务端 total 数量
// fields/sort 字段名使用 fieldKey（英文标识符）
func (c *Client) ListRecords(appKey, entityKey string, opts ListRecordOpts) ([]map[string]any, int, error) {
	reqBody := map[string]any{
		"appKey":     appKey,
		"entityKey":  entityKey,
		"pagination": map[string]any{"page": opts.Page, "size": opts.Size},
	}
	if len(opts.Fields) > 0 {
		reqBody["fields"] = opts.Fields
	}
	if len(opts.Sort) > 0 {
		reqBody["sort"] = opts.Sort
	}
	if opts.Filter != "" {
		reqBody["filter"] = expression(opts.Filter)
	}
	return c.listPage("/data/v1/record", reqBody)
}

// AggregateRecords 调用 MakeService.ListResources 对单个 Entity 做服务端聚合统计（POST /data/v1/aggregate）
// 返回分组行列表（维度列为 {value,label}，指标列为裸数值）和分组总行数
func (c *Client) AggregateRecords(appKey, entityKey string, opts AggregateOpts) ([]map[string]any, int, error) {
	reqBody := map[string]any{
		"appKey":     appKey,
		"entityKey":  entityKey,
		"aggregates": opts.Aggregates,
		"pagination": map[string]any{"page": opts.Page, "size": opts.Size},
	}
	if len(opts.Group) > 0 {
		reqBody["group"] = opts.Group
	}
	if len(opts.Sort) > 0 {
		reqBody["sort"] = opts.Sort
	}
	if opts.Filter != "" {
		reqBody["filter"] = expression(opts.Filter)
	}
	if opts.AggregateFilter != "" {
		reqBody["aggregateFilter"] = expression(opts.AggregateFilter)
	}
	return c.listPage("/data/v1/aggregate", reqBody)
}

// expression 把 CEL 文本包成服务端的 Expression 对象（原样透传，合法性由服务端裁决）
func expression(cel string) map[string]any {
	return map[string]any{"expression": cel}
}

// listPage 发送 MakeService.ListResources 请求并解码分页响应，返回行列表与服务端 total
func (c *Client) listPage(path string, reqBody map[string]any) ([]map[string]any, int, error) {
	var result struct {
		Code       int              `json:"code"`
		Message    string           `json:"msg"`
		Data       []map[string]any `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	if err := c.do("MakeService.ListResources", path, reqBody, &result); err != nil {
		return nil, 0, err
	}
	if result.Code != 200 {
		return nil, 0, fmt.Errorf("API 错误 [%d]: %s", result.Code, result.Message)
	}
	return result.Data, result.Pagination.Total, nil
}
