/**
 * [INPUT]: 依赖 fmt、os、path/filepath、strings；协议类型来自 protocol.go
 * [OUTPUT]: 对外提供 PrepareWorkDir（工作目录定位/创建 + description 身份职责与 instructions 执行要求渲染为 CLI 原生上下文文件），每次执行独立隔离
 * [POS]: internal/daemon 的执行环境层——租户/Execution/generation 的临时目录与本次身份文件
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PrepareWorkDir 为租户、Execution 和领取代次创建独立目录，平台输入不能指定宿主路径。
func PrepareWorkDir(baseDir string, claim RunClaim) (workDir string, err error) {
	if err := validateExecution(claim); err != nil {
		return "", err
	}
	identity, _ := json.Marshal([]any{claim.Context.Namespace.TenantID, claim.Execution.Execution.ID, claim.Execution.Lease.Generation})
	digest := sha256.Sum256(identity)
	workDir = filepath.Join(baseDir, "executions", hex.EncodeToString(digest[:]))
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return "", fmt.Errorf("create work dir: %w", err)
	}
	description := strings.TrimSpace(claim.Agent.Description)
	instructions := strings.TrimSpace(claim.Agent.Instructions)
	if description != "" || instructions != "" {
		var content strings.Builder
		fmt.Fprintf(&content, "# %s\n", claim.Agent.Name)
		if description != "" {
			fmt.Fprintf(&content, "\n## 身份与职责\n\n%s\n", description)
		}
		if instructions != "" {
			fmt.Fprintf(&content, "\n## 执行要求\n\n%s\n", instructions)
		}
		if len(claim.ExecutionIdentity) > 0 && json.Valid(claim.ExecutionIdentity) {
			fmt.Fprintf(&content, "\n## 本次执行身份\n\n%s\n", claim.ExecutionIdentity)
		}
		if claim.Initiator != nil {
			fmt.Fprintf(&content, "\n真实发起者：%s/%s。执行账户与发言人分别记录，不能把‘我’自动改写成执行账户。\n", claim.Initiator.Kind, claim.Initiator.ID)
		}
		for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
			if err := os.WriteFile(filepath.Join(workDir, name), []byte(content.String()), 0o600); err != nil {
				return "", fmt.Errorf("render %s: %w", name, err)
			}
		}
	}
	return workDir, nil
}
