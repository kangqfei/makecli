/**
 * [INPUT]: 依赖 client.go 的 Client.do / checkGetResult / notFoundCode / ErrNotFound / metaVersion，encoding/json、fmt
 * [OUTPUT]: 对外提供 PromoteRun / PromoteStep / PromoteStatus（含 Finished / Succeeded 终态判定）类型、
 *           PromoteStateSucceeded / Failed / Canceled / Terminated / TimedOut 状态常量、
 *           Client.PromoteApp(betaKey) / Client.GetPromoteStatus(betaKey, workflowID, runID) 方法
 * [POS]: internal/api 的应用环境发布（make-console app-environment）调用层：POST /console/v1/app-environment/product，
 *        X-Make-Target 区分动作——CreateResource 发起 beta → production 发布（服务端 Temporal 异步流程，
 *        固定 beta 最近一次成功部署的版本，回执 workflowId + runId），StatusResource 按 workflowId + runId 精确查本次进度。
 *        与 client.go 的 Meta 操作共用 Client 与 do 原语（网关前缀 /api/make 由 cmd 层补齐）；被 cmd/promote 消费
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package api

import (
	"encoding/json"
	"fmt"
)

// appEnvironmentProductPath 是「发布到 production」资源端点（创建 = 发起发布，状态 = 查进度）
const appEnvironmentProductPath = "/console/v1/app-environment/product"

// PromoteRun 是发起发布的回执。workflowId 按 beta 固定（preview-publish:{orgId}:{betaAppId}），
// runId 每次发布新生成——查进度必须两者同传，才能精确定位本次发布而非该 beta 的最近一次。
type PromoteRun struct {
	WorkflowID string `json:"workflowId"`
	RunID      string `json:"runId"`
}

// PromoteStep 是发布流程中的一个步骤（检查版本 → 发布配置 → 发布代码 → 等待部署）
type PromoteStep struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// PromoteStatus 是发布进度快照（服务端 environment-operations 的现有进度结果）。
// 四个 ID 字段用 json.Number：文档示例是字符串形态（"6098"），数字形态亦可能出现，
// Number 两者都能解码，CLI 只做展示不参与运算。
type PromoteStatus struct {
	WorkflowID         string        `json:"workflowId"`
	RunID              string        `json:"runId"`
	Type               string        `json:"type"`
	State              string        `json:"state"`
	Step               string        `json:"step"`
	Message            string        `json:"message,omitempty"`
	ProductAppID       json.Number   `json:"productAppId,omitempty"`
	PreviewAppID       json.Number   `json:"previewAppId,omitempty"`
	SourceBuildTaskID  json.Number   `json:"sourceBuildTaskId,omitempty"`
	ProductBuildTaskID json.Number   `json:"productBuildTaskId,omitempty"`
	Steps              []PromoteStep `json:"steps"`
}

// 发布流程终态常量。SUCCEEDED 来自接口文档；其余四个是 Temporal 工作流执行的终止状态，
// 服务端流程跑在 Temporal 上，失败/取消/终止/超时都不会再推进，轮询到即停。
const (
	PromoteStateSucceeded  = "SUCCEEDED"
	PromoteStateFailed     = "FAILED"
	PromoteStateCanceled   = "CANCELED"
	PromoteStateTerminated = "TERMINATED"
	PromoteStateTimedOut   = "TIMED_OUT"
)

// Finished 报告发布是否已达终态——`app promote --wait` 轮询的停止条件。
func (s *PromoteStatus) Finished() bool {
	switch s.State {
	case PromoteStateSucceeded, PromoteStateFailed, PromoteStateCanceled, PromoteStateTerminated, PromoteStateTimedOut:
		return true
	default:
		return false
	}
}

// Succeeded 报告发布是否成功收尾。
func (s *PromoteStatus) Succeeded() bool {
	return s.State == PromoteStateSucceeded
}

// appEnvironmentBody 构造 app-environment 资源请求体：key 是 beta app key，properties 按动作携带参数
func appEnvironmentBody(betaKey string, properties map[string]any) map[string]any {
	return map[string]any{
		"key":        betaKey,
		"type":       "Make.App",
		"meta":       map[string]any{"version": metaVersion},
		"properties": properties,
	}
}

// PromoteApp 调用 MakeService.CreateResource 发起 beta → production 发布。
// 服务端校验管理员权限与 beta/prod 绑定、固定 beta 最近一次成功部署的版本后异步执行；
// 同一 beta 已有发布在跑时复用当前任务（回执即该任务的 workflowId/runId），不会重复发布。
// 业务码 404 返回 ErrNotFound（beta app 不存在），其余非 200 原样为「API 错误」。
func (c *Client) PromoteApp(betaKey string) (*PromoteRun, error) {
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"msg"`
		Data    struct {
			Properties PromoteRun `json:"properties"`
		} `json:"data"`
	}
	if err := c.do("MakeService.CreateResource", appEnvironmentProductPath, appEnvironmentBody(betaKey, map[string]any{}), &result); err != nil {
		return nil, err
	}
	if result.Code == notFoundCode {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, result.Message)
	}
	if result.Code != 200 {
		return nil, fmt.Errorf("API 错误 [%d]: %s", result.Code, result.Message)
	}
	run := result.Data.Properties
	if run.WorkflowID == "" || run.RunID == "" {
		return nil, fmt.Errorf("服务端未返回发布任务标识（workflowId/runId）")
	}
	return &run, nil
}

// GetPromoteStatus 调用 MakeService.StatusResource 按 workflowId + runId 查询本次发布进度。
// 任务不存在（或响应无 state）返回 ErrNotFound；其余错误原样返回。
func (c *Client) GetPromoteStatus(betaKey, workflowID, runID string) (*PromoteStatus, error) {
	body := appEnvironmentBody(betaKey, map[string]any{"workflowId": workflowID, "runId": runID})
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"msg"`
		Data    struct {
			Properties PromoteStatus `json:"properties"`
		} `json:"data"`
	}
	if err := c.do("MakeService.StatusResource", appEnvironmentProductPath, body, &result); err != nil {
		return nil, err
	}
	if err := checkGetResult(result.Code, result.Message, result.Data.Properties.State != ""); err != nil {
		return nil, err
	}
	return &result.Data.Properties, nil
}
