/**
 * [INPUT]: 依赖 bytes、context、encoding/json、fmt、io、net/http、time；协议类型来自 protocol.go
 * [OUTPUT]: gateway 设备面类型化调用、不可变的执行租户作用域与 APIError（信封错误还原）
 * [POS]: internal/daemon 的传输层——Bearer token 鉴权，POST + X-Make-Target + 信封解包；正确性建立在拉取式 claim 上，连接断开只影响延迟
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// APIError 还原 gateway/context 的错误信封。
type APIError struct {
	HTTPStatus int
	Reason     string
	Msg        string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("gateway %d %s: %s", e.HTTPStatus, e.Reason, e.Msg)
}

// Client 是 gateway 设备面的 HTTP client。
type Client struct {
	baseURL  string
	token    string
	http     *http.Client
	tenantID string
}

// NewClient 构造 Client；baseURL 形如 https://gateway.example.com。
func NewClient(baseURL, token string) *Client {
	return &Client{baseURL: baseURL, token: token, http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// forExecution 只复制本次执行的租户路由，共享连接池但不修改节点级 client。
func (c *Client) forExecution(claim RunClaim) (*Client, error) {
	if err := validateExecution(claim); err != nil {
		return nil, err
	}
	tenantID := claim.Context.Namespace.TenantID
	if c.tenantID != "" && c.tenantID != tenantID {
		return nil, fmt.Errorf("执行租户与 client 作用域不一致")
	}
	scoped := *c
	scoped.tenantID = tenantID
	return &scoped, nil
}

// call 执行统一调用风格请求并解包信封。
func (c *Client) call(ctx context.Context, resource, target string, requestBody, responseData any) error {
	return c.callWithHeaders(ctx, resource, target, requestBody, responseData, nil)
}

func (c *Client) callWithHeaders(ctx context.Context, resource, target string, requestBody, responseData any, headers http.Header) error {
	bodyJSON, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+PathPrefix+"/"+resource, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		// 入册（换 node key 前）无 Bearer——gateway 对 runtime Create 免鉴权，setup-key 自证。
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	request.Header.Set(TargetHeader, target)
	if c.tenantID != "" {
		request.Header.Set("X-Tenant-ID", c.tenantID)
	}
	for name, values := range headers {
		request.Header[name] = append([]string(nil), values...)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("gateway unreachable: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	var envelope Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return &APIError{HTTPStatus: response.StatusCode, Reason: "invalid_envelope", Msg: "服务端未返回合法信封"}
	}
	if response.StatusCode != http.StatusOK || envelope.Code != 200 {
		var errorData ErrorData
		_ = json.Unmarshal(envelope.Data, &errorData)
		return &APIError{HTTPStatus: response.StatusCode, Reason: errorData.Reason, Msg: envelope.Msg}
	}
	if responseData != nil {
		if err := json.Unmarshal(envelope.Data, responseData); err != nil {
			return fmt.Errorf("unmarshal data: %w", err)
		}
	}
	return nil
}

// SetToken 设置 Bearer node key（入册换回后调用）。
func (c *Client) SetToken(token string) { c.token = token }

// EnrollRuntime 自助入册（runtime CreateResource，setup-key 在 body 里自证，免 Bearer）。
func (c *Client) EnrollRuntime(ctx context.Context, request CreateRuntimeRequest) (CreateRuntimeResponse, error) {
	var response CreateRuntimeResponse
	err := c.call(ctx, ResourceRuntime, TargetCreateResource, request, &response)
	return response, err
}

// Heartbeat 心跳（15s，runtime UpdateResource）；响应 actions 携带取消指令。
func (c *Client) Heartbeat(ctx context.Context, request UpdateRuntimeRequest) (UpdateRuntimeResponse, error) {
	var response UpdateRuntimeResponse
	err := c.call(ctx, ResourceRuntime, TargetUpdateResource, request, &response)
	return response, err
}

// ClaimRuns 领取待执行 run（run-claim 资源的 CreateResource：claim 即创建租约）。
func (c *Client) ClaimRuns(ctx context.Context, request CreateRunClaimRequest) ([]RunClaim, error) {
	var claims []RunClaim
	request.ExecutionProtocolVersion = "execution-v1"
	err := c.call(ctx, ResourceRunClaim, TargetCreateResource, request, &claims)
	return claims, err
}

// UpdateRun 状态迁移统一入口（语义由 status 目标值表达）。
func (c *Client) UpdateRun(ctx context.Context, request UpdateRunRequest) error {
	return c.call(ctx, ResourceRun, TargetUpdateResource, request, nil)
}

func (c *Client) RenewClaim(ctx context.Context, claim RunClaim) (RenewClaimResponse, error) {
	var result RenewClaimResponse
	scoped, err := c.forExecution(claim)
	if err != nil {
		return result, err
	}
	err = scoped.call(ctx, ResourceRunClaim, TargetUpdateResource, RenewClaimRequest{RunID: claim.RunID, LeaseToken: claim.LeaseToken}, &result)
	return result, err
}

// AppendEvents 租约 append（batchSeq 幂等，模糊重试安全）。
func (c *Client) AppendEvents(ctx context.Context, request CreateEventsRequest) (CreateEventsResponse, error) {
	var response CreateEventsResponse
	err := c.call(ctx, ResourceEvent, TargetCreateResource, request, &response)
	return response, err
}
