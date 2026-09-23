/**
 * [INPUT]: 依赖 context、encoding/json、time
 * [OUTPUT]: 对外提供 Backend 接口、ExecOptions、Session、Message/MessageType、Result、TokenUsage——外接 brain 的统一执行契约
 * [POS]: internal/daemon/adapter 的接口层——单方法 Backend.Execute（agent-design/docs/execution.md §8.1 的 adapter 契约），
 *        CLI 差异止步于各实现文件
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package adapter

import (
	"context"
	"encoding/json"
	"time"
)

// Backend 是外接 brain CLI 的统一执行接口：一次 Execute = 一个 run。
type Backend interface {
	// Execute 启动一次执行并返回流式会话。调用方消费 Session.Messages
	// 直到关闭，然后从 Session.Result 取最终结果（恰好一个值）。
	Execute(ctx context.Context, prompt string, opts ExecOptions) (*Session, error)
	// Provider 返回能力名（claude-code / codex），与 claim capability 一致。
	Provider() string
	// Detect 探测本机 CLI 可用性，返回版本（不可用返回 error）。
	Detect(ctx context.Context) (string, error)
}

// ExecOptions 配置单次执行。
type ExecOptions struct {
	WorkDir         string        // 工作目录（execenv 准备）
	ResumeSessionID string        // 非空则续接既有 CLI 会话
	MaxRunDuration  time.Duration // 时长兜底（设备侧 wall-clock，默认 1h）
}

// Session 是一次运行中的执行流。
type Session struct {
	// Messages 流式产出事件，执行结束时关闭（先于 Result）。
	Messages <-chan Message
	// Result 恰好收到一个最终结果后关闭。
	Result <-chan Result
}

// MessageType 是统一事件类型——与平台事件词汇一一对应。
type MessageType string

const (
	MessageThinking   MessageType = "thinking"
	MessageText       MessageType = "text" // 中间助手文本（daemon 映射为 status，最终答案在 Result）
	MessageToolUse    MessageType = "tool_use"
	MessageToolResult MessageType = "tool_result"
	MessageStatus     MessageType = "status"
	MessageError      MessageType = "error"
)

// Message 是执行过程中的一条统一事件。
type Message struct {
	Type    MessageType
	Text    string          // thinking / text / status / error
	Tool    string          // tool_use / tool_result
	CallID  string          // tool_use / tool_result 配对键
	Input   json.RawMessage // tool_use 入参
	Output  string          // tool_result 输出
	IsError bool            // tool_result 失败标记
}

// Result 是最终结果。
type Result struct {
	Text         string      // 最终答复文本（成功时非空）
	IsError      bool        // 执行失败
	ErrorMessage string      // 失败原因（IsError 时）
	CLISessionID string      // CLI 会话 ID（连续性回写）
	Usage        *TokenUsage // CLI 上报的 token 计数
}

// TokenUsage 是 CLI 上报的 token 计数。
type TokenUsage struct {
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}
