// [INPUT]: 当前 Execution 的有效租约、执行取消句柄与独立原子取消标记。
// [OUTPUT]: 执行全过程续租，平台取消或续租失败立即停止该次 CLI。
// [POS]: 节点心跳不代替任务租约，失去权限后不能继续本地副作用。
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
package daemon

import (
	"context"
	"sync/atomic"
	"time"
)

func (d *Daemon) keepExecutionLease(ctx context.Context, claim RunClaim, cancel context.CancelFunc, cancelled *atomic.Bool) {
	interval := time.Duration(claim.LeaseSeconds) * time.Second / 3
	if interval <= 0 || interval > 5*time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		bounded, done := context.WithTimeout(ctx, min(interval, 3*time.Second))
		receipt, err := d.client.RenewClaim(bounded, claim)
		done()
		if err != nil || !receipt.LeaseExpiresAt.After(time.Now()) {
			d.logger.Warn("execution lease lost", "execution", claim.Execution.Execution.ID)
			cancel()
			return
		}
		if receipt.CancelRequested {
			cancelled.Store(true)
			cancel()
			return
		}
	}
}
