/**
 * [INPUT]: 依赖 encoding/json、time；线上形状真相源是 agent-design/docs/contracts/internal.md（黄金测试锁在 agent-contract 仓库）
 * [OUTPUT]: daemon 的 runtime/Execution/Context wire 类型，保留文本工具调用、结果及错误事实
 * [POS]: internal/daemon 的协议词汇表。makecli 是公开 GitHub 仓库，无法 import 私有 agent-contract 模块，
 *        故在此镜像线上 JSON 形状；字段变更必须与 agent-contract 同步（先 agent-design/docs/contracts/internal.md，后两边类型）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package daemon

import (
	"encoding/json"
	"time"
)

// TargetHeader 是统一调用风格的 action 头。
const TargetHeader = "X-Make-Target"

// 封闭六动词中 daemon 消费的三个（Make 平台规范：新能力 = 新资源域 × 六动词）。
const (
	TargetCreateResource = "MakeService.CreateResource"
	TargetGetResource    = "MakeService.GetResource"
	TargetUpdateResource = "MakeService.UpdateResource"
	TargetListResources  = "MakeService.ListResources"
)

// PathPrefix 是 agent 服务段的路径前缀；设备面经 gateway 透传，内外同路径。
const PathPrefix = "/api/make/agent/v1"

// daemon 消费的资源域。
const (
	ResourceEvent    = "event"
	ResourceRunClaim = "run-claim"
	ResourceRun      = "run"
	ResourceRuntime  = "runtime"
)

// Envelope 是统一响应信封 {code, msg, data}。
type Envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data,omitempty"`
}

// ErrorData 是错误响应 data 的最小可解析形状（领域详情为兄弟字段）。
type ErrorData struct {
	Reason string `json:"reason"`
}

// Actor 是事件产生方身份。
type Actor struct {
	Kind string `json:"kind"` // end_user | agent | platform
	ID   string `json:"id,omitempty"`
}

// MentionTarget 是 mention 块的目标身份（镜像 agent-contract 词汇）。
// 出站方向 daemon 只知道名字：ID 先填名字，平台按 id-or-name 归一化。
type MentionTarget struct {
	Kind string `json:"kind"` // agent | team | end_user
	ID   string `json:"id"`
}

// Block 是渠道无关内容块（入站消费 text/mention 的 text 呈现，出站产出
// text 与 mention——互@触发依赖结构化 mention 块，纯文本 @ 不触发）。
type Block struct {
	Kind string `json:"kind"` // text | image | file | mention
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"`
	Name string `json:"name,omitempty"`

	// mention 块的目标
	Target *MentionTarget `json:"target,omitempty"`
}

// NewEvent 是待写入事件（append 用）。
type NewEvent struct {
	Type    string          `json:"type"`
	Actor   Actor           `json:"actor"`
	Payload json.RawMessage `json:"payload,omitempty"`
	RunID   string          `json:"runID,omitempty"`
}

// RuntimeCapability 是 runtime 探测到的一个 CLI 脑。
type RuntimeCapability struct {
	Provider string `json:"provider"`
	Version  string `json:"version,omitempty"`
}

// CreateRuntimeRequest / Response —— 自助入册：出示 setup-key 换回长期 node key。
type CreateRuntimeRequest struct {
	SetupKey     string              `json:"setupKey"`
	Name         string              `json:"name"`
	Capabilities []RuntimeCapability `json:"capabilities"`
}

type CreateRuntimeResponse struct {
	RuntimeID string `json:"runtimeID"`
	NodeKey   string `json:"nodeKey"` // 明文仅入册响应一次
}

// UpdateRuntimeRequest / Response —— 15s 心跳（node key 鉴权）；actions 是平台→runtime 指令通道。
type UpdateRuntimeRequest struct {
	Capabilities []RuntimeCapability `json:"capabilities,omitempty"`
}

type RuntimeAction struct {
	Kind        string `json:"kind"` // v1 仅 cancel_run
	RunID       string `json:"runID,omitempty"`
	ExecutionID string `json:"executionId,omitempty"`
}

type UpdateRuntimeResponse struct {
	Ack     bool            `json:"ack"`
	Actions []RuntimeAction `json:"actions,omitempty"`
}

// CreateRunClaimRequest —— runtime 身份来自 node key（gateway 注入 X-Runtime-ID），请求体不带 runtimeID。
type CreateRunClaimRequest struct {
	ExecutionProtocolVersion string   `json:"executionProtocolVersion"`
	Capabilities             []string `json:"capabilities"`
	Max                      int      `json:"max"`
}

// AgentBundle 是 claim 下发的 agent 渲染包：Description 定义身份职责，
// Instructions 定义具体执行要求，execenv 据此渲染工作目录。
type AgentBundle struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Instructions string            `json:"instructions"`
	RunParams    json.RawMessage   `json:"runParams,omitempty"`
	MCPServers   []json.RawMessage `json:"mcpServers,omitempty"`
}

// SeqRange 是触发事件区间 [FromSeq, ToSeq]。
type SeqRange struct {
	FromSeq int64 `json:"fromSeq"`
	ToSeq   int64 `json:"toSeq"`
}

// ChainState 是链的当前状态。
type ChainState struct {
	ID         string `json:"id"`
	Depth      int    `json:"depth"`
	BudgetLeft int    `json:"budgetLeft"`
}

// RunClaim 是 claim 的单条结果。
type RunClaim struct {
	Execution         *ExecutionClaim   `json:"execution"`
	Context           *ContextExecution `json:"context"`
	ExecutionIdentity json.RawMessage   `json:"executionIdentity,omitempty"`
	Initiator         *Actor            `json:"initiator,omitempty"`
	RunID             string            `json:"runID"`
	SessionID         string            `json:"sessionID"`
	LeaseToken        string            `json:"leaseToken"`
	LeaseSeconds      int               `json:"leaseSeconds"`
	Agent             AgentBundle       `json:"agent"`
	Trigger           SeqRange          `json:"trigger"`
	Chain             ChainState        `json:"chain"`
}

type ExecutionClaim struct {
	Execution struct {
		ID    string `json:"id"`
		RunID string `json:"runId"`
	} `json:"execution"`
	Lease struct {
		ExecutionID string `json:"executionId"`
		WorkerID    string `json:"workerId"`
		Generation  int64  `json:"generation"`
		Token       string `json:"token"`
	} `json:"lease"`
}

type ContextNamespace struct {
	TenantID   string `json:"tenantId"`
	UserID     string `json:"userId"`
	ProductKey string `json:"productKey"`
	AgentKey   string `json:"agentKey"`
}

type ContextExecution struct {
	Namespace ContextNamespace `json:"namespace"`
	SessionID string           `json:"sessionId"`
	TurnID    string           `json:"turnId"`
}

type ContextBlock struct {
	Role       string             `json:"role"`
	Content    string             `json:"content"`
	Parts      []Block            `json:"parts,omitempty"`
	ToolUse    *ContextToolUse    `json:"toolUse,omitempty"`
	ToolResult *ContextToolResult `json:"toolResult,omitempty"`
}

type ContextToolUse struct {
	CallID string          `json:"callID"`
	Tool   string          `json:"tool"`
	Input  json.RawMessage `json:"input"`
}

type ContextToolResult struct {
	CallID  string `json:"callID"`
	Output  string `json:"output"`
	IsError bool   `json:"isError,omitempty"`
}

type ContextPack struct {
	Blocks        []ContextBlock   `json:"blocks"`
	Namespace     ContextNamespace `json:"namespace"`
	NextPageToken string           `json:"nextPageToken,omitempty"`
}

type ContextSourceRef struct {
	Kind         string           `json:"kind"`
	Namespace    ContextNamespace `json:"namespace"`
	SessionID    string           `json:"sessionId"`
	TurnID       string           `json:"turnId"`
	InputPartID  string           `json:"inputPartId"`
	DigestSHA256 string           `json:"digestSha256"`
}

type ContextReadRequest struct {
	Namespace ContextNamespace  `json:"namespace"`
	SessionID string            `json:"sessionId"`
	TurnID    string            `json:"turnId"`
	View      string            `json:"view"`
	Source    *ContextSourceRef `json:"source,omitempty"`
	PageToken string            `json:"pageToken,omitempty"`
}

type ContextReadResult struct {
	Turn *struct {
		InputParts []struct {
			ID           string `json:"id"`
			DigestSHA256 string `json:"digestSha256"`
		} `json:"inputParts"`
	} `json:"turn,omitempty"`
	Context *ContextPack `json:"context,omitempty"`
}

type RenewClaimRequest struct {
	RunID      string `json:"runID"`
	LeaseToken string `json:"leaseToken"`
}
type RenewClaimResponse struct {
	LeaseExpiresAt  time.Time `json:"leaseExpiresAt"`
	CancelRequested bool      `json:"cancelRequested"`
}

// Usage 是 CLI 上报的 token 计数（记录展示用，不参与计费）。
type Usage struct {
	InputTokens         int64 `json:"inputTokens"`
	OutputTokens        int64 `json:"outputTokens"`
	CacheReadTokens     int64 `json:"cacheReadTokens,omitempty"`
	CacheCreationTokens int64 `json:"cacheCreationTokens,omitempty"`
}

// run 状态机目标值与类型化失败原因（agent-design/docs/contracts/internal.md §3.3）。
const (
	RunStatusRunning   = "running"
	RunStatusCompleted = "completed"
	RunStatusFailed    = "failed"
	RunStatusCancelled = "cancelled"

	FailReasonCLICrash = "cli_crash"
	FailReasonTimeout  = "timeout"
)

// UpdateRunRequest 是 run 状态迁移的统一请求——语义由 status 目标值表达：
// running / completed / failed 必带 lease；cancelled 带 lease 即设备收尾。
type UpdateRunRequest struct {
	RunID         string `json:"runID"`
	Status        string `json:"status"`
	LeaseToken    string `json:"leaseToken,omitempty"`
	Usage         *Usage `json:"usage,omitempty"`
	FailureReason string `json:"failureReason,omitempty"`
}

// CreateEventsRequest —— 租约 append：batchSeq per-run 从 1 单调递增，
// ≤ 已记录值的批次被服务端幂等吸收（模糊重试安全）。
type CreateEventsRequest struct {
	SessionID  string     `json:"sessionID"`
	LeaseToken string     `json:"leaseToken"`
	BatchSeq   int64      `json:"batchSeq"`
	Events     []NewEvent `json:"events"`
}

type CreateEventsResponse struct {
	Appended  int  `json:"appended"`
	Duplicate bool `json:"duplicate,omitempty"`
}
