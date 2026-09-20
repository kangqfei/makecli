/**
 * [INPUT]: 依赖 bytes、errors、fmt、strings、testing，依赖 internal/api 的 ErrAuthFailed；用 setProfile/setContextFlag 隔离全局态
 * [OUTPUT]: 覆盖 reportExecuteError 各分支（nil/退出码哨兵静默/真实错误打印/鉴权升级）+ authFailedHint 文案 + ExitCode 退出码映射（0/2/124/1）
 * [POS]: cmd 模块错误呈现单一出口的测试，守护退出码哨兵静默、真实错误打印、鉴权引导升级三态与退出码契约
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/api"
)

// TestReportExecuteError 锁定错误呈现单一出口的契约：
// 退出码哨兵（errDiffFound/errPreflightFailed）静默、真实错误打印 error: 前缀、鉴权失败升级为引导。
func TestReportExecuteError(t *testing.T) {
	authErr := fmt.Errorf("%w [990300403]: token验证失败", api.ErrAuthFailed)

	tests := []struct {
		name      string
		err       error
		wantPrint bool
		wantParts []string // 若打印，输出须包含的片段
	}{
		{"nil silent", nil, false, nil},
		{"diff sentinel silent", errDiffFound, false, nil},
		{"preflight sentinel silent", errPreflightFailed, false, nil},
		{"real error prints", errors.New("connection refused"), true, []string{"error:", "connection refused"}},
		{"auth failed upgrades", authErr, true, []string{
			"error: 鉴权失败 [990300403]: token验证失败",
			"makecli login",
			"makecli configure verify",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			reportExecuteError(&buf, tt.err)
			if (buf.Len() > 0) != tt.wantPrint {
				t.Fatalf("printed=%v (%q), want printed=%v", buf.Len() > 0, buf.String(), tt.wantPrint)
			}
			for _, part := range tt.wantParts {
				if !strings.Contains(buf.String(), part) {
					t.Errorf("output missing %q\ngot:\n%s", part, buf.String())
				}
			}
		})
	}
}

// TestAuthFailedHintShowsContext 守护鉴权引导回显当前 profile / context（用于识别后端串号）。
func TestAuthFailedHintShowsContext(t *testing.T) {
	setProfile(t, "work")
	setContextFlag(t, "production")

	hint := authFailedHint(fmt.Errorf("%w [990300403]: token验证失败", api.ErrAuthFailed))

	for _, want := range []string{
		"鉴权失败 [990300403]: token验证失败",
		"当前 profile: work | context: production | token 来源: credentials",
		"    makecli login",
	} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint missing %q\ngot:\n%s", want, hint)
		}
	}
}

// TestExitCode 锁定退出码契约：0 成功、2 构建未成功、124 等待超时（GNU timeout 惯例）、其余 1。
// CI 与 agent 靠退出码区分「构建失败」与「超时未完成」，不必解析文本。
func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil is success", nil, 0},
		{"build failed maps to 2", fmt.Errorf("%w（status: FAILED）", errBuildFailed), 2},
		{"wait timeout maps to 124", fmt.Errorf("%w（5m0s）", errWaitTimeout), 124},
		{"generic error maps to 1", errors.New("boom"), 1},
		{"diff sentinel keeps 1", errDiffFound, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExitCode(c.err); got != c.want {
				t.Errorf("ExitCode(%v) = %d, want %d", c.err, got, c.want)
			}
		})
	}
}
