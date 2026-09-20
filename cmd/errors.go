/**
 * [INPUT]: 依赖 errors、fmt、io，依赖 internal/api 的 ErrAuthFailed；读取全局 Profile 与 contextName() / accessTokenSource()
 * [OUTPUT]: 对外提供 reportExecuteError（CLI 错误呈现单一出口）、authFailedHint（鉴权引导文案）、ExitCode（错误→退出码翻译：0 成功 / 2 构建未成功 / 124 等待超时 / 其余 1）
 * [POS]: cmd 模块错误呈现的单一出口，被 root.go Execute 调用；收口原 diff/preflight 各自 SilenceErrors+自打印的特例；ExitCode 被 main 消费，语义化退出码让 CI/agent 免解析文本
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/qfeius/makecli/internal/api"
)

// reportExecuteError 是 CLI 错误呈现的单一出口（rootCmd 开 SilenceErrors 后由 Execute 调用）：
//   - nil：无事发生
//   - errDiffFound / errPreflightFailed / errDoctorFailed：纯退出码哨兵（CI 门禁信号），真实输出已由命令自身打印，此处静默
//   - api.ErrAuthFailed：升级为带 next-step 的鉴权引导（提示 makecli login + 回显 profile/context）
//   - 其余：复刻 cobra 默认的 `error: <msg>` 行
func reportExecuteError(w io.Writer, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, errDiffFound), errors.Is(err, errPreflightFailed), errors.Is(err, errDoctorFailed):
		return
	case errors.Is(err, api.ErrAuthFailed):
		_, _ = fmt.Fprintln(w, "error:", authFailedHint(err))
	default:
		_, _ = fmt.Fprintln(w, "error:", err)
	}
}

// ExitCode 把 Execute 返回的错误翻译成进程退出码（main 的唯一消费点）：
// 0 成功；2 构建未成功（--wait 等到 FAILED/CANCELED）；124 等待超时（沿用 GNU timeout 惯例）；
// 其余一律 1。CI 与 agent 靠退出码判定结果，不必解析输出文本。
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errBuildFailed):
		return 2
	case errors.Is(err, errWaitTimeout):
		return 124
	default:
		return 1
	}
}

// authFailedHint 把鉴权失败错误渲染成多行引导文案。首行复用 err 本身
// （= "鉴权失败 [990300403]: token验证失败"），由 reportExecuteError 贴上 "error:" 前缀；
// 其后回显当前 profile/context（帮助识别后端串号）并给出 makecli login 等 next-step。
func authFailedHint(err error) string {
	return fmt.Sprintf(`%s
当前 profile: %s | context: %s | token 来源: %s

凭证无效或已过期,请重新登陆:
    makecli login

若已登陆仍报此错,请确认 --context 与 token 颁发的后端一致,
可用 makecli configure verify 自检当前凭证。
token 来自 --access-token / $%s 时 login 不会覆盖它,请更换或撤掉该覆盖值。`, err, Profile, contextName(), accessTokenSource(), EnvAccessToken)
}
