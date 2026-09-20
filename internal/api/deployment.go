/**
 * [INPUT]: 依赖 client.go 的 Client.do / notFoundCode / ErrNotFound、fmt
 * [OUTPUT]: 对外提供 EnvBeta / EnvProduction 用户面环境常量与 ServerEnvKey / DisplayEnv 词汇翻译（beta ⇄ 服务端 preview）、 EnvDeployment / DeploymentOverview 类型（含 Env(name) 环境选择器）、Client.GetDeploymentOverview(appKey) 方法
 * [POS]: internal/api 的部署服务（make-deployment）调用层，POST /deployment/v1/deployment/overview，
 *        与 client.go 的 Meta 操作共用 Client 与 do 原语；被 cmd/app_info 与 cmd/deploy（成功后带出环境 URL）消费
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package api

import "fmt"

// ---------------------------------- 环境词汇 ----------------------------------

// 用户面环境词汇是 beta / production（与 App 的 prod/beta 配对、Make Console 一致）；
// 服务端把 beta 环境仍以 "preview" 为 key（仓库 properties.env、部署总览字段、构建任务 environment）。
// ServerEnvKey / DisplayEnv 是这层翻译仅有的两处出入口，其余代码只说用户面词汇。
const (
	EnvBeta       = "beta"
	EnvProduction = "production"

	serverEnvPreview = "preview" // 服务端对 beta 环境的历史命名
)

// ServerEnvKey 把用户面环境名翻译成服务端 key（beta → preview，其余原样，服务端 key 传入也原样）
func ServerEnvKey(env string) string {
	if env == EnvBeta {
		return serverEnvPreview
	}
	return env
}

// DisplayEnv 把服务端环境 key 翻译成用户面词汇（preview → beta，其余原样）
func DisplayEnv(key string) string {
	if key == serverEnvPreview {
		return EnvBeta
	}
	return key
}

// EnvDeployment 描述单个环境（beta/production）的部署状态。
// URL 是该环境的访问地址，是 app info 命令的核心产出。
type EnvDeployment struct {
	Status         string `json:"status"`
	BuildTaskID    string `json:"buildTaskID"`
	CommitSha      string `json:"commitSha"`
	URL            string `json:"url"`
	DeploymentID   string `json:"deploymentID"`
	DesiredRelease string `json:"desiredRelease"`
	ActiveRelease  string `json:"activeRelease"`
}

// DeploymentOverview 是应用双环境部署总览。
// 环境字段用指针承载「无部署数据」：nil = 该环境从未部署，
// 让渲染层的占位分支自然落在 nil 判定上，不需要空结构体启发式。
type DeploymentOverview struct {
	TenantID   string         `json:"tenantID"`
	AppKey     string         `json:"appKey"`
	Preview    *EnvDeployment `json:"preview"`
	Production *EnvDeployment `json:"production"`
}

// Env 按环境名选取对应环境的部署状态，用户面 beta 与服务端 preview 均接受（经 ServerEnvKey 归一）；
// 未知名或该环境从未部署返回 nil。把「环境名 → 字段」的分支收口在类型内部，调用方免于双路 if。
func (o *DeploymentOverview) Env(name string) *EnvDeployment {
	switch ServerEnvKey(name) {
	case serverEnvPreview:
		return o.Preview
	case EnvProduction:
		return o.Production
	default:
		return nil
	}
}

// GetDeploymentOverview 查询指定 App 的双环境部署总览。
// 业务码 404 表示该 App 从未部署，返回 ErrNotFound（调用方视为合法状态而非失败）；
// 其余非 200 业务码与传输/解码错误原样上抛。
func (c *Client) GetDeploymentOverview(appKey string) (*DeploymentOverview, error) {
	body := map[string]any{"appKey": appKey}
	var result struct {
		Code    int                `json:"code"`
		Message string             `json:"msg"`
		Data    DeploymentOverview `json:"data"`
	}
	if err := c.do("MakeService.GetResource", "/deployment/v1/deployment/overview", body, &result); err != nil {
		return nil, err
	}
	if result.Code == notFoundCode {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, result.Message)
	}
	if result.Code != 200 {
		return nil, fmt.Errorf("API 错误 [%d]: %s", result.Code, result.Message)
	}
	return &result.Data, nil
}
