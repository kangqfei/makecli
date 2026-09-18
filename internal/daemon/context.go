// [INPUT]: 平台 Claim 的独立执行、当前租约与四维调用范围。
// [OUTPUT]: 服务端组装的授权窗口和文本 CLI 输入，不回落旧 Session 事件或旧 CLI 会话。
// [POS]: 公开 CLI 的协议镜像消费端，不导入私有服务模块，也不自行扩大来源权限。
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
package daemon

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func validateExecution(claim RunClaim) error {
	if claim.Execution == nil || claim.Context == nil || claim.RunID == "" || claim.LeaseToken == "" {
		return fmt.Errorf("claim 缺少独立执行与 Context")
	}
	execution, lease := claim.Execution.Execution, claim.Execution.Lease
	scope := claim.Context.Namespace
	if execution.ID == "" || execution.RunID != claim.RunID || lease.ExecutionID != execution.ID || lease.Generation < 1 || lease.Token != claim.LeaseToken || lease.WorkerID == "" || claim.Context.SessionID != claim.SessionID || claim.Context.TurnID == "" || scope.TenantID == "" || scope.UserID == "" || scope.ProductKey == "" || scope.AgentKey == "" {
		return fmt.Errorf("claim 的执行范围不一致")
	}
	return nil
}

func (c *Client) ReadContext(ctx context.Context, claim RunClaim) (ContextPack, error) {
	var result ContextPack
	if err := validateExecution(claim); err != nil {
		return result, err
	}
	identity := claim.Context.Namespace
	headers := http.Header{}
	for name, value := range map[string]string{"X-Tenant-ID": identity.TenantID, "X-Runtime-ID": claim.Execution.Lease.WorkerID, "X-Context-Client-ID": claim.Execution.Lease.WorkerID, "X-Context-User-ID": identity.UserID, "X-Context-Product-Key": identity.ProductKey, "X-Context-Agent-Key": identity.AgentKey, "X-Context-Run-ID": claim.RunID, "X-Context-Execution-ID": claim.Execution.Execution.ID, "X-Context-Generation": strconv.FormatInt(claim.Execution.Lease.Generation, 10), "X-Context-Lease-Token": claim.LeaseToken} {
		headers.Set(name, value)
	}
	request := struct {
		Namespace     ContextNamespace `json:"namespace"`
		SessionID     string           `json:"sessionId"`
		Operation     string           `json:"operation"`
		CurrentTurnID string           `json:"currentTurnId"`
	}{identity, claim.Context.SessionID, "fill", claim.Context.TurnID}
	err := c.callWithHeaders(ctx, "context-window", TargetCreateResource, request, &result, headers)
	return result, err
}

func BuildContextPrompt(pack ContextPack) (string, error) {
	parts := make([]string, 0, len(pack.Blocks))
	for _, block := range pack.Blocks {
		text := strings.TrimSpace(block.Content)
		if len(block.Parts) > 0 {
			pieces := []string{}
			for _, part := range block.Parts {
				if part.Kind != "text" && part.Kind != "mention" {
					return "", fmt.Errorf("当前设备 CLI 尚不支持该多模态输入，请选择支持媒体的运行服务")
				}
				pieces = append(pieces, part.Text)
			}
			text = strings.Join(pieces, "\n")
		}
		if text != "" {
			parts = append(parts, "["+block.Role+"]\n"+text)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("当前执行没有可读取的输入")
	}
	return strings.Join(parts, "\n\n"), nil
}
