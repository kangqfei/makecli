/**
 * [INPUT]: 依赖 context、encoding/json、log/slog、time；协议与传输来自 protocol.go/client.go，执行契约来自 adapter 包，出站 mention 切分来自 mention.go
 * [OUTPUT]: 独立执行的 start → 授权窗口/续租/目录隔离 → CLI → 持久事实 → complete/fail/cancel
 * [POS]: internal/daemon 的执行编排——batch_seq 单调保证模糊重试不双写；中间文本映射为 status（最终答复才是 message，
 *        经 parseMentionBlocks 产出结构化 mention 块，message 事件在状态面物化出站投递并驱动互@）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/qfeius/makecli/internal/daemon/adapter"
)

// eventFlushSize / eventFlushInterval 是事件批量上报的攒批阈值。
const (
	eventFlushSize     = 16
	eventFlushInterval = 2 * time.Second
)

// executeRun 执行一个已 claim 的 run。ctx 取消即取消执行（取消指令经
// 心跳 actions 到达后由 daemon 调 cancel）；cancelled 标记决定收尾 reason。
func (d *Daemon) executeRun(ctx context.Context, backend adapter.Backend, claim RunClaim, cancelled *atomic.Bool) {
	logger := d.logger.With("run", claim.RunID, "session", claim.SessionID, "provider", backend.Provider())
	if err := validateExecution(claim); err != nil {
		logger.Warn("invalid execution claim", "err", err)
		return
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	if err := d.client.UpdateRun(ctx, UpdateRunRequest{RunID: claim.RunID, Status: RunStatusRunning, LeaseToken: claim.LeaseToken}); err != nil {
		logger.Error("start run", "err", err)
		return // start 失败不 FailRun：lease 可能已被回收，留给 sweeper 处置
	}
	leaseDone := make(chan struct{})
	go func() { defer close(leaseDone); d.keepExecutionLease(ctx, claim, stop, cancelled) }()
	defer func() { stop(); <-leaseDone }()

	fail := func(status, reason, detail string) {
		logger.Warn("run failed", "status", status, "reason", reason, "detail", detail)
		if err := d.client.UpdateRun(context.WithoutCancel(ctx), UpdateRunRequest{
			RunID: claim.RunID, Status: status, LeaseToken: claim.LeaseToken, FailureReason: reason,
		}); err != nil {
			logger.Error("fail run receipt", "err", err)
		}
	}

	if len(claim.Agent.MCPServers) > 0 {
		fail(RunStatusFailed, FailReasonCLICrash, "当前设备适配器不支持平台下发的业务工具凭据，请选择支持该能力的运行服务")
		return
	}
	pack, err := d.client.ReadContext(ctx, claim)
	if err != nil {
		fail(RunStatusFailed, FailReasonCLICrash, "读取 Context 窗口失败")
		return
	}
	prompt, err := BuildContextPrompt(pack)
	if err != nil {
		fail(RunStatusFailed, FailReasonCLICrash, err.Error())
		return
	}

	workDir, err := PrepareWorkDir(d.workBaseDir, claim)
	if err != nil {
		fail(RunStatusFailed, FailReasonCLICrash, "准备工作目录失败: "+err.Error())
		return
	}

	session, err := backend.Execute(ctx, prompt, adapter.ExecOptions{
		WorkDir:        workDir,
		MaxRunDuration: d.maxRunDuration,
	})
	if err != nil {
		fail(RunStatusFailed, FailReasonCLICrash, "启动 CLI 失败: "+err.Error())
		return
	}

	reporter := &eventReporter{daemon: d, claim: claim, logger: logger, stop: stop}
	for message := range session.Messages {
		reporter.add(ctx, message)
	}
	result := <-session.Result
	if ctx.Err() == nil && !cancelled.Load() {
		reporter.finish(ctx, result)
	} else {
		reporter.flush(ctx)
	}

	switch {
	case cancelled.Load():
		// 取消指令收尾：无论 CLI 以何种方式退出，终态都是 cancelled。
		fail(RunStatusCancelled, "", "")
	case reporter.err != nil:
		fail(RunStatusFailed, FailReasonCLICrash, "执行事实未能可靠保存")
	case result.IsError && ctx.Err() != nil:
		fail(RunStatusFailed, FailReasonTimeout, result.ErrorMessage)
	case result.IsError:
		fail(RunStatusFailed, FailReasonCLICrash, result.ErrorMessage)
	default:
		request := UpdateRunRequest{
			RunID: claim.RunID, Status: RunStatusCompleted, LeaseToken: claim.LeaseToken,
		}
		if result.Usage != nil {
			request.Usage = &Usage{
				InputTokens:         result.Usage.InputTokens,
				OutputTokens:        result.Usage.OutputTokens,
				CacheReadTokens:     result.Usage.CacheReadTokens,
				CacheCreationTokens: result.Usage.CacheCreationTokens,
			}
		}
		if err := d.client.UpdateRun(context.WithoutCancel(ctx), request); err != nil {
			logger.Error("complete run", "err", err)
			return
		}
		logger.Info("run completed")
	}
}

// eventReporter 攒批上报当前 Execution 的事件，batch_seq 从 1 单调递增——
// 服务端以此幂等吸收模糊重试，绝不双写。
type eventReporter struct {
	daemon   *Daemon
	claim    RunClaim
	logger   *slog.Logger
	buffer   []NewEvent
	batchSeq int64
	lastSent time.Time
	err      error
	stop     context.CancelFunc
}

// add 归一并缓冲一条执行事件，满批或超时即冲刷。
// 中间助手文本映射为 status——最终答复（Result.Text）才是 message 事件，
// 出站投递只由 message 物化，群里不会收到每一步的碎片文本。
func (r *eventReporter) add(ctx context.Context, message adapter.Message) {
	if r.err != nil {
		return
	}
	event := NewEvent{Actor: Actor{Kind: "agent"}, RunID: r.claim.RunID}
	switch message.Type {
	case adapter.MessageThinking:
		event.Type = "thinking"
		event.Payload = mustJSON(map[string]string{"text": message.Text})
	case adapter.MessageText:
		event.Type = "status"
		event.Payload = mustJSON(map[string]string{"text": message.Text})
	case adapter.MessageStatus:
		event.Type = "status"
		event.Payload = mustJSON(map[string]string{"text": message.Text})
	case adapter.MessageToolUse:
		event.Type = "tool_use"
		event.Payload = mustJSON(map[string]any{"callID": message.CallID, "tool": message.Tool, "input": json.RawMessage(nonEmptyJSON(message.Input))})
	case adapter.MessageToolResult:
		event.Type = "tool_result"
		event.Payload = mustJSON(map[string]any{"callID": message.CallID, "output": truncate(message.Output, 16*1024), "isError": message.IsError})
	case adapter.MessageError:
		event.Type = "error"
		event.Payload = mustJSON(map[string]string{"code": "brain_error", "message": message.Text})
	default:
		return
	}
	r.buffer = append(r.buffer, event)
	if len(r.buffer) >= eventFlushSize || time.Since(r.lastSent) >= eventFlushInterval {
		r.flush(ctx)
	}
}

// finish 追加最终 message 事件（成功且有产出时）并冲刷余量。
func (r *eventReporter) finish(ctx context.Context, result adapter.Result) {
	if r.err != nil {
		return
	}
	if !result.IsError && result.Text != "" {
		// @Name 切成结构化 mention 块——互@的平台内直通只认 mention 块，
		// 纯文本 @ 只是字面量（Design.md §7.5）。
		r.buffer = append(r.buffer, NewEvent{
			Type: "message", Actor: Actor{Kind: "agent"}, RunID: r.claim.RunID,
			Payload: mustJSON(map[string]any{"blocks": parseMentionBlocks(result.Text)}),
		})
	}
	if result.IsError && result.ErrorMessage != "" {
		r.buffer = append(r.buffer, NewEvent{
			Type: "error", Actor: Actor{Kind: "agent"}, RunID: r.claim.RunID,
			Payload: mustJSON(map[string]string{"code": "brain_error", "message": result.ErrorMessage}),
		})
	}
	r.flush(ctx)
}

func (r *eventReporter) flush(ctx context.Context) {
	if len(r.buffer) == 0 {
		return
	}
	r.batchSeq++
	// 收尾冲刷必须在取消后仍可达——用不承继取消的 ctx。
	_, err := r.daemon.client.AppendEvents(context.WithoutCancel(ctx), CreateEventsRequest{
		SessionID:  r.claim.SessionID,
		LeaseToken: r.claim.LeaseToken,
		BatchSeq:   r.batchSeq,
		Events:     r.buffer,
	})
	if err != nil {
		r.logger.Warn("append events failed", "batch_seq", r.batchSeq)
		r.err = err
		if r.stop != nil {
			r.stop()
		}
		return
	}
	r.buffer = r.buffer[:0]
	r.lastSent = time.Now()
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

func nonEmptyJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`null`)
	}
	return raw
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "\n…(截断)"
}
