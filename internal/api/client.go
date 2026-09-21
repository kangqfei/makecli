/**
 * [INPUT]: 依赖 bytes、encoding/json、errors、fmt、io、net/http、strings、time，依赖 debug.go 的 debugSink，依赖 internal/trace 的 TraceID/Traceparent
 * [OUTPUT]: 对外提供 Client 类型、ErrNotFound / ErrAuthFailed 哨兵错误、UniqueConstraintError 类型化错误（409 唯一性冲突，errors.As 判定）、Option / WithDebug(on, DebugFormat) / WithHeaders / WithDryRun 功能选项、New 构造函数、App（Role / PairAppKey / HostedEnv / KeyForEnv：prod/beta 配对知识收口，环境 → 承载 app key）、RoleProd / RoleBeta 常量 / Field / Entity / EntityProperties / UniqueConstraint / RelationEnd / RelationProperties / Relation / Schema 类型、CreateApp(key, name, properties) / ListApps(page, size, filter) / DeleteApp(key) / GetApp(key) / CreateEntity(key, name, appKey, props) / ListEntities(appKey, page, size, filter) / GetEntity(appKey, key) / UpdateEntity(key, name, appKey, props) / DeleteEntity / CreateRelation(key, name, appKey, props) / UpdateRelation / ListRelations(appKey, ...) / GetRelation(appKey, key) / DeleteRelation / GetSchema(appKey) 方法。资源以 Key 为唯一标识符（英数下划线），Name 为用户可见展示名（支持中文）。Get* 方法在资源确实不存在时返回 ErrNotFound（可用 errors.Is 判定），其余错误（传输/非 not-found 业务码/解码）原样返回
 * [POS]: internal/api 的核心，封装 Make Meta Service 的 HTTP 调用
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qfeius/makecli/internal/trace"
)

// ---------------------------------- 哨兵错误 ----------------------------------

// ErrNotFound 表示资源确实不存在（区别于传输错误 / 非 not-found 业务错误 / 解码失败）。
// Get* 方法用 %w 包裹返回它，调用方用 errors.Is(err, ErrNotFound) 判定。
// 它把"不存在"这个语义从模糊的 (err == nil && Key == "") 启发式里解放出来，
// 让 apply 的 create-or-update 决策不再把瞬时故障误判为"不存在"。
var ErrNotFound = errors.New("资源不存在")

// notFoundCode 是 Meta Server 表示资源不存在的业务错误码
const notFoundCode = 404

// ErrAuthFailed 表示后端拒绝了本次请求的凭证（token 无效 / 过期 / 与环境不匹配）。
// 它把横切的"鉴权失败"语义从散落各处的 (code, msg) 里解放出来，
// 让 cmd 层用 errors.Is(err, api.ErrAuthFailed) 统一翻译成 `makecli login` 引导。
// 与 ErrNotFound 同为包级哨兵，是 api 层"只报事实、不管呈现"的分层边界。
var ErrAuthFailed = errors.New("鉴权失败")

// authFailedCode 是后端表示鉴权失败的业务错误码。
// 后端无公开错误码表，此处仅收录已知的 token 验证失败码；如有其它鉴权码段，在此扩展。
const authFailedCode = 990300403

// authFailedErr 是 ErrAuthFailed 的单一构造点：以 %w 包裹哨兵并保留原始 code/msg 供上层展示。
func authFailedErr(code int, message string) error {
	return fmt.Errorf("%w [%d]: %s", ErrAuthFailed, code, message)
}

// conflictCode 是写入违反唯一性约束时后端返回的业务码（见 DataAPIDesign 唯一性约束冲突）。
const conflictCode = 409

// UniqueConstraintError 表示写入（创建/更新/批量更新 Record）违反了 Entity 的唯一性约束。
// 与 ErrNotFound / ErrAuthFailed 同为 api 层「只报事实、不管呈现」的分层边界：
// api 携带冲突的约束名 Constraint 与参与字段 Fields，cmd 层直接展示其自解释的 Error() 串。
// 用 errors.As(err, &api.UniqueConstraintError{}) 判定。
type UniqueConstraintError struct {
	Constraint string   // 冲突的唯一约束名
	Fields     []string // 参与该约束的字段 key
	Message    string   // 后端原始 msg（约束名/字段缺失时的兜底）
}

func (e *UniqueConstraintError) Error() string {
	if e.Constraint != "" && len(e.Fields) > 0 {
		return fmt.Sprintf("唯一性约束冲突 [%s]：字段 (%s) 已存在相同值", e.Constraint, strings.Join(e.Fields, ", "))
	}
	if e.Message != "" {
		return "唯一性约束冲突：" + e.Message
	}
	return "唯一性约束冲突"
}

// conflictData 承载 409 唯一性冲突响应的 data 形态（constraint + fields）。
type conflictData struct {
	Constraint string   `json:"constraint"`
	Fields     []string `json:"fields"`
}

// writeStatusErr 把写操作的非 200 业务码翻译成错误：409 唯一性冲突翻译为 UniqueConstraintError
// （携带约束名与字段），其余沿用通用「API 错误」。收口原本散落各写方法的非 200 翻译重复。
//
// data 以原始字节传入、只在 409 分支解析：成功响应的 data 形态由各端点自定
// （对象 / 布尔 / 数组皆有），写路径不该对它提任何要求。解析失败退回 msg 兜底。
func writeStatusErr(code int, msg string, data json.RawMessage) error {
	if code == conflictCode {
		var conflict conflictData
		_ = json.Unmarshal(data, &conflict)
		return &UniqueConstraintError{Constraint: conflict.Constraint, Fields: conflict.Fields, Message: msg}
	}
	return fmt.Errorf("API 错误 [%d]: %s", code, msg)
}

// ---------------------------------- 客户端 ----------------------------------

// Client 封装 Make Meta Service 的 HTTP 调用
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	debug      *debugSink // nil 即关闭
	dryRun     bool
	headers    map[string]string
}

// Option 是 Client 的功能选项
type Option func(*Client)

// WithDebug 启用 debug 模式，把每个请求与响应转储到 stderr；format 选文本（curl -v 风格）或 JSON（渲染见 debug.go）
func WithDebug(on bool, format DebugFormat) Option {
	return func(c *Client) {
		c.debug = nil
		if on {
			c.debug = newDebugSink(format)
		}
	}
}

// WithHeaders 设置额外请求头（如 X-Tenant-ID、X-Operator-ID）
func WithHeaders(h map[string]string) Option {
	return func(c *Client) { c.headers = h }
}

// WithDryRun 开启 dry-run 模式：每个写请求带上横切信号 X-Dry-Run: true，
// 服务端跑真实业务流程但以 ROLLBACK 替换 COMMIT（不落库）。响应结构与真实请求一字不差，
// 调用方仍按 code 判定成功/失败——CLI 自知发的是 dry-run，无需从响应里区分。
func WithDryRun(on bool) Option {
	return func(c *Client) { c.dryRun = on }
}

// New 创建新的 API 客户端，30s 超时
func New(baseURL, token string, opts ...Option) *Client {
	c := &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ---------------------------------- 数据类型 ----------------------------------

// App 代表 Meta Server 返回的单个 App 资源
// Key 是唯一标识符（英数下划线，2-20，创建后不可改）
// Name 是用户可见的展示名（允许中文，必填）
type App struct {
	Key        string         `json:"key"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Meta       map[string]any `json:"meta"`
	Properties map[string]any `json:"properties"`
}

// App 角色（MetaAPIDesign.md）：每个 App 是 prod/beta 一对，meta.appRole 标角色，meta.pairAppKey 指向配对 app
const (
	RoleProd = "prod"
	RoleBeta = "beta"
)

// Role 返回 meta.appRole；缺省（历史 app）视为 prod
func (a *App) Role() string {
	if role, _ := a.Meta["appRole"].(string); role == RoleBeta {
		return RoleBeta
	}
	return RoleProd
}

// PairAppKey 返回 meta.pairAppKey（无配对为空串）
func (a *App) PairAppKey() string {
	pair, _ := a.Meta["pairAppKey"].(string)
	return pair
}

// HostedEnv 返回本 app 自身承载的环境：beta 角色承载 EnvBeta，prod 角色承载 EnvProduction
func (a *App) HostedEnv() string {
	if a.Role() == RoleBeta {
		return EnvBeta
	}
	return EnvProduction
}

// KeyForEnv 返回承载指定环境（EnvBeta / EnvProduction）的 app key：
// 本 app 自身承载该环境则是自己，否则是配对 app。代码仓库、部署都挂在承载环境的那个 app 上，
// 用户只需给一个 key，CLI 据服务端的角色/配对信息定位目标；无配对时报错。
func (a *App) KeyForEnv(env string) (string, error) {
	if env == a.HostedEnv() {
		return a.Key, nil
	}
	if pair := a.PairAppKey(); pair != "" {
		return pair, nil
	}
	return "", fmt.Errorf("app %q has no %s environment", a.Key, env)
}

// ---------------------------------- App 操作 ----------------------------------

// CreateApp 调用 MakeService.CreateResource 在 Meta Server 创建 App
// key 是英文标识符（不可改），name 是用户可见展示名（必填，支持中文）
func (c *Client) CreateApp(key, name string, properties map[string]any) error {
	body := map[string]any{
		"key":        key,
		"name":       name,
		"type":       "Make.App",
		"meta":       map[string]any{"version": metaVersion},
		"properties": properties,
	}
	return c.post("MakeService.CreateResource", "/meta/v1/app", body)
}

// ListApps 调用 MakeService.ListResources 获取 org 下全部 App
// filter 为可选的 CEL 表达式文本，空串时不发送 filter 字段
// 返回 App 列表和服务端 total 数量
func (c *Client) ListApps(page, size int, filter string) ([]App, int, error) {
	reqBody := map[string]any{
		"sort":       []map[string]any{{"fieldKey": "id", "order": "asc"}},
		"pagination": map[string]any{"page": page, "size": size},
	}
	if filter != "" {
		reqBody["filter"] = map[string]any{"expression": filter}
	}

	var result struct {
		Code       int    `json:"code"`
		Message    string `json:"msg"`
		Data       []App  `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	if err := c.do("MakeService.ListResources", "/meta/v1/app", reqBody, &result); err != nil {
		return nil, 0, err
	}
	if result.Code != 200 {
		return nil, 0, fmt.Errorf("API 错误 [%d]: %s", result.Code, result.Message)
	}
	return result.Data, result.Pagination.Total, nil
}

// DeleteApp 调用 MakeService.DeleteResource 删除指定 App（按 key 定位）
func (c *Client) DeleteApp(key string) error {
	body := map[string]any{
		"key":  key,
		"type": "Make.App",
	}
	return c.post("MakeService.DeleteResource", "/meta/v1/app", body)
}

// ---------------------------------- Entity 操作 ----------------------------------

// Field 代表 Entity 下的单个字段定义
// Key 是 Entity 范围内唯一的标识符（英数下划线，2-20）
// Name 是用户可见的展示名（允许中文，必填）
type Field struct {
	Key         string         `json:"key"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Meta        map[string]any `json:"meta"`
	Properties  map[string]any `json:"properties"`
	Validations map[string]any `json:"validations,omitempty"`
	// Capabilities 是服务端按字段类型派生的能力位（groupable 列表分组 /
	// aggregable 聚合维度 / sortable / supportsUniqueConstraint 等）。用 map
	// 而非结构体：键集合由服务端演进（aggregable 即后加、旧版本可缺失，缺失
	// ≠ false），CLI 不解释语义只原样透传。仅读路径填充；写路径不设置，
	// omitempty 保证不向线缆发空对象（与 Validations 同款只读透传语义）。
	Capabilities map[string]any `json:"capabilities,omitempty"`
}

// Entity 代表 Meta Server 返回的单个 Entity 资源
// Key 是 App 范围内唯一的标识符；AppKey 引用所属 App 的 key
type Entity struct {
	Key        string           `json:"key"`
	Name       string           `json:"name"`
	Type       string           `json:"type"`
	AppKey     string           `json:"appKey"`
	Meta       map[string]any   `json:"meta"`
	Properties EntityProperties `json:"properties"`
}

// EntityProperties 封装 Entity 的 fields 列表与可选的唯一性约束
// UniqueConstraints 为空时经 omitempty 省略，序列化与「只发 fields」逐字节一致
type EntityProperties struct {
	Fields            []Field            `json:"fields"`
	UniqueConstraints []UniqueConstraint `json:"uniqueConstraints,omitempty"`
}

// UniqueConstraint 描述 Entity 级唯一性约束（底层 = TiDB 唯一索引，跨记录约束）。
// Name 是约束名（Entity 内唯一，作为写入冲突报错与 migration 的稳定锚）。
// Fields 是参与约束的字段 key 列表（单字段唯一即 n=1，复合唯一为 2≤n≤3）。
// 元组中任一字段为空则该行不参与唯一判定（对齐 SQL NULL ≠ NULL）。
type UniqueConstraint struct {
	Name   string   `json:"name"`
	Fields []string `json:"fields"`
}

// ---------------------------------- Relation 操作 ----------------------------------

// RelationEnd 描述关系的一端（实体 key + 基数）
type RelationEnd struct {
	EntityKey   string `json:"entityKey"`
	Cardinality string `json:"cardinality"` // "one" | "many"
}

// RelationProperties 封装 Relation 的 from/to 两端
type RelationProperties struct {
	From RelationEnd `json:"from"`
	To   RelationEnd `json:"to"`
}

// Relation 代表 Meta Server 返回的单个 Relation 资源
// Key 是 App 范围内唯一的标识符；AppKey 引用所属 App 的 key
type Relation struct {
	Key        string             `json:"key"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	AppKey     string             `json:"appKey"`
	Meta       map[string]any     `json:"meta"`
	Properties RelationProperties `json:"properties"`
}

// metaVersion 是所有写操作（Create/Update）默认附带的 DSL 元数据版本号。
const metaVersion = "1.0.0"

// writeResource 是 Entity/Relation 的 Create/Update 共享写入原语：四者唯一的差异
// 仅在 action（Create/Update verb）、resType、path、properties，其余 body 结构完全一致。
// 抽此一处收口，消除四个逐字重复的函数体（含 meta 版本号这类「配置当代码写 N 遍」的味道）。
func (c *Client) writeResource(action, resType, path, key, name, appKey string, properties any) error {
	body := map[string]any{
		"key":        key,
		"name":       name,
		"type":       resType,
		"appKey":     appKey,
		"meta":       map[string]any{"version": metaVersion},
		"properties": properties,
	}
	return c.post(action, path, body)
}

// CreateEntity 调用 MakeService.CreateResource 在指定 App 下创建 Entity
// key 是 Entity 标识符（英数下划线），name 是展示名（必填）；appKey 引用所属 App
// props 经 json tag fields/uniqueConstraints 直接序列化，与 CreateRelation 收 RelationProperties 对称
func (c *Client) CreateEntity(key, name, appKey string, props EntityProperties) error {
	return c.writeResource("MakeService.CreateResource", "Make.Entity", "/meta/v1/entity",
		key, name, appKey, props)
}

// ListEntities 调用 MakeService.ListResources 获取指定 App 下全部 Entity
// filter 为可选的 CEL 表达式文本，空串时不发送 filter 字段
// 返回 Entity 列表和服务端 total 数量
func (c *Client) ListEntities(appKey string, page, size int, filter string) ([]Entity, int, error) {
	reqBody := map[string]any{
		"appKey":     appKey,
		"sort":       []map[string]any{{"fieldKey": "id", "order": "asc"}},
		"pagination": map[string]any{"page": page, "size": size},
	}
	if filter != "" {
		reqBody["filter"] = map[string]any{"expression": filter}
	}
	var result struct {
		Code       int      `json:"code"`
		Message    string   `json:"msg"`
		Data       []Entity `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	if err := c.do("MakeService.ListResources", "/meta/v1/entity", reqBody, &result); err != nil {
		return nil, 0, err
	}
	if result.Code != 200 {
		return nil, 0, fmt.Errorf("API 错误 [%d]: %s", result.Code, result.Message)
	}
	return result.Data, result.Pagination.Total, nil
}

// GetEntity 调用 MakeService.GetResource 获取指定 Entity 的详细信息（按 key 定位）
// 资源不存在时返回 ErrNotFound；传输/非 not-found 业务错误原样返回
func (c *Client) GetEntity(appKey, key string) (*Entity, error) {
	reqBody := map[string]any{"appKey": appKey, "key": key}
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"msg"`
		Data    Entity `json:"data"`
	}
	if err := c.do("MakeService.GetResource", "/meta/v1/entity", reqBody, &result); err != nil {
		return nil, err
	}
	if err := checkGetResult(result.Code, result.Message, result.Data.Key != ""); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// GetApp 调用 MakeService.GetResource 获取指定 App（按 key 定位）
// 资源不存在时返回 ErrNotFound；传输/非 not-found 业务错误原样返回
func (c *Client) GetApp(key string) (*App, error) {
	reqBody := map[string]any{"key": key}
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"msg"`
		Data    App    `json:"data"`
	}
	if err := c.do("MakeService.GetResource", "/meta/v1/app", reqBody, &result); err != nil {
		return nil, err
	}
	if err := checkGetResult(result.Code, result.Message, result.Data.Key != ""); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// UpdateEntity 调用 MakeService.UpdateResource 更新指定 Entity（按 key 定位）
func (c *Client) UpdateEntity(key, name, appKey string, props EntityProperties) error {
	return c.writeResource("MakeService.UpdateResource", "Make.Entity", "/meta/v1/entity",
		key, name, appKey, props)
}

// DeleteEntity 调用 MakeService.DeleteResource 删除指定 Entity（按 key 定位）
func (c *Client) DeleteEntity(key, appKey string) error {
	body := map[string]any{
		"key":    key,
		"type":   "Make.Entity",
		"appKey": appKey,
	}
	return c.post("MakeService.DeleteResource", "/meta/v1/entity", body)
}

// CreateRelation 调用 MakeService.CreateResource 在指定 App 下创建 Relation
// key 是 Relation 标识符，name 是展示名（必填）；appKey 引用所属 App
// props 经 json tag from/to 直接序列化，与旧版逐字段展开等价
func (c *Client) CreateRelation(key, name, appKey string, props RelationProperties) error {
	return c.writeResource("MakeService.CreateResource", "Make.Relation", "/meta/v1/relation",
		key, name, appKey, props)
}

// UpdateRelation 调用 MakeService.UpdateResource 更新指定 Relation（按 key 定位）
func (c *Client) UpdateRelation(key, name, appKey string, props RelationProperties) error {
	return c.writeResource("MakeService.UpdateResource", "Make.Relation", "/meta/v1/relation",
		key, name, appKey, props)
}

// ListRelations 调用 MakeService.ListResources 获取指定 App 下全部 Relation
// filter 为可选的 CEL 表达式文本，空串时不发送 filter 字段
// 返回 Relation 列表和服务端 total 数量
func (c *Client) ListRelations(appKey string, page, size int, filter string) ([]Relation, int, error) {
	reqBody := map[string]any{
		"appKey":     appKey,
		"sort":       []map[string]any{{"fieldKey": "id", "order": "asc"}},
		"pagination": map[string]any{"page": page, "size": size},
	}
	if filter != "" {
		reqBody["filter"] = map[string]any{"expression": filter}
	}
	var result struct {
		Code       int        `json:"code"`
		Message    string     `json:"msg"`
		Data       []Relation `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	if err := c.do("MakeService.ListResources", "/meta/v1/relation", reqBody, &result); err != nil {
		return nil, 0, err
	}
	if result.Code != 200 {
		return nil, 0, fmt.Errorf("API 错误 [%d]: %s", result.Code, result.Message)
	}
	return result.Data, result.Pagination.Total, nil
}

// GetRelation 调用 MakeService.GetResource 获取指定 Relation 的详细信息（按 key 定位）
// 资源不存在时返回 ErrNotFound；传输/非 not-found 业务错误原样返回
func (c *Client) GetRelation(appKey, key string) (*Relation, error) {
	reqBody := map[string]any{"appKey": appKey, "key": key}
	var result struct {
		Code    int      `json:"code"`
		Message string   `json:"msg"`
		Data    Relation `json:"data"`
	}
	if err := c.do("MakeService.GetResource", "/meta/v1/relation", reqBody, &result); err != nil {
		return nil, err
	}
	if err := checkGetResult(result.Code, result.Message, result.Data.Key != ""); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// checkGetResult 把 GetResource 的业务码/数据收敛成统一的"存在/不存在/出错"三态：
//   - code == 404（not-found 业务码）        → ErrNotFound
//   - code == 200 且 !exists（软空响应）      → ErrNotFound
//   - code != 200 且非 404                    → 原样业务错误（不映射为 not-found）
//   - code == 200 且 exists                   → nil（存在）
//
// 软空响应分支保留了服务端"200 + 空 data"表示不存在的现实约定，
// 让"不存在"语义被 ErrNotFound 这一个哨兵收口。exists 由调用方按资源主键
// 判定（Meta 资源 data.key 非空、构建任务 data.id 非零），字符串/数字主键统一收敛。
func checkGetResult(code int, message string, exists bool) error {
	if code == notFoundCode {
		return fmt.Errorf("%w: %s", ErrNotFound, message)
	}
	if code != 200 {
		return fmt.Errorf("API 错误 [%d]: %s", code, message)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

// DeleteRelation 调用 MakeService.DeleteResource 删除指定 Relation（按 key 定位）
func (c *Client) DeleteRelation(key, appKey string) error {
	body := map[string]any{
		"key":    key,
		"type":   "Make.Relation",
		"appKey": appKey,
	}
	return c.post("MakeService.DeleteResource", "/meta/v1/relation", body)
}

// ---------------------------------- Schema 操作 ----------------------------------

// Schema 代表 App 的聚合视图（App + Entities + Relations）
type Schema struct {
	App       App        `json:"app"`
	Entities  []Entity   `json:"entities"`
	Relations []Relation `json:"relations"`
}

// GetSchema 调用 MakeService.GetResource 获取指定 App 的聚合 Schema（按 appKey 定位）
func (c *Client) GetSchema(appKey string) (*Schema, error) {
	reqBody := map[string]any{"appKey": appKey}
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"msg"`
		Data    Schema `json:"data"`
	}
	if err := c.do("MakeService.GetResource", "/meta/v1/schema", reqBody, &result); err != nil {
		return nil, err
	}
	if result.Code != 200 {
		return nil, fmt.Errorf("API 错误 [%d]: %s", result.Code, result.Message)
	}
	return &result.Data, nil
}

// ---------------------------------- 核心请求 ----------------------------------

// do 执行 POST 请求并将响应体解码到 result
func (c *Client) do(target, path string, body, result any) error {
	return c.request(http.MethodPost, target, path, body, result)
}

// request 执行任意 HTTP 方法的请求并将响应体解码到 result。
// body 为 nil 时不携带请求体与 Content-Type（GET 类只读调用），非 nil 时 JSON 序列化。
func (c *Client) request(method, target, path string, body, result any) error {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("序列化请求失败: %w", err)
		}
	}

	// trace 头：trace-id 全程稳定（X-Log-Id 与 traceparent 第二段一致），parent-id 每请求新生成
	traceparent, logID := trace.Traceparent(), trace.TraceID()

	// 请求头有序收集：一份数据同时喂 http.Request 与 --debug 转储，杜绝两处漂移
	headers := [][2]string{}
	if body != nil {
		headers = append(headers, [2]string{"Content-Type", "application/json"})
	}
	headers = append(headers,
		[2]string{"Authorization", "Bearer " + c.token},
		[2]string{"X-Make-Target", target},
		[2]string{"Traceparent", traceparent},
		[2]string{"X-Log-Id", logID},
	)
	if c.dryRun {
		headers = append(headers, [2]string{"X-Dry-Run", "true"})
	}
	for k, v := range c.headers {
		headers = append(headers, [2]string{k, v})
	}

	var payload io.Reader
	if body != nil {
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.baseURL+path, payload)
	if err != nil {
		return err
	}
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	if c.debug != nil {
		c.debug.request(debugRequest{Method: method, URL: c.baseURL + path, Headers: headers, Body: data})
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}
	if c.debug != nil {
		c.debug.response(resp, raw)
	}
	// 鉴权失败是横切错误：轻量探针先读 code，命中鉴权码即抛 ErrAuthFailed 哨兵，
	// 让 cmd 层统一翻译成 `makecli login` 引导，无需各方法各自识别。
	var probe struct {
		Code    int    `json:"code"`
		Message string `json:"msg"`
	}
	if json.Unmarshal(raw, &probe) == nil && probe.Code == authFailedCode {
		return authFailedErr(probe.Code, probe.Message)
	}
	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("无效的响应格式: %w", err)
	}
	return nil
}

// post 是 do 的便捷包装，用于只需检查 code == 200 的写操作。
// data 原样收着不解析（各端点成功形态不一，DeleteApp 就回 true），
// 非 200 经 writeStatusErr 翻译——409 → UniqueConstraintError（届时才解析 data），其余 → 通用错误。
func (c *Client) post(target, path string, body any) error {
	var result struct {
		Code    int             `json:"code"`
		Message string          `json:"msg"`
		Data    json.RawMessage `json:"data"`
	}
	if err := c.do(target, path, body, &result); err != nil {
		return err
	}
	if result.Code != 200 {
		return writeStatusErr(result.Code, result.Message, result.Data)
	}
	return nil
}
