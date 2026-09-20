/**
 * [INPUT]: 依赖 context、os/exec、slices、strings、time
 * [OUTPUT]: 对外提供 Sync / Options / Result / SkillsCommand，执行 Make platform skills 默认同步
 * [POS]: internal/skillsync 的编排层，被 cmd/update.go 在二进制更新后调用；同步命令按 Options.Role 经 role.go SyncCommand 派生（空 → --all，user → 按名）；Skip 判断后前置 EnsureNpx 环境门禁（Skip 不要求 npx）；隔离 npx 副作用，update 每次刷新 skills；dedupSortedNames 去重排序按名清单，被 remove.go / install.go 复用
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package skillsync

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

const (
	SkillsSource  = "qfeius/make-platform-skills"
	ActionSynced  = "synced"
	ActionSkipped = "skipped"
	ActionFailed  = "failed"
)

const syncTimeout = 3 * time.Minute

// Options 控制一次 skills 同步。
type Options struct {
	Version string
	Skip    bool
	// Role 决定同步范围（RoleSkills）：空（未设置）→ 全量，user → 只装 makecli。
	// update 后置同步按它分流，用户在 skills install --role 里的选择不会被冲回全量。
	Role string
}

// Result 描述同步结果，供 cmd 层渲染用户可见输出。
type Result struct {
	Action  string
	Reason  string
	Source  string
	Version string
	Command []string
	Output  string
}

// CommandString 返回可直接复制执行的同步命令。
func (r Result) CommandString() string {
	return strings.Join(r.Command, " ")
}

// SkillsCommand 返回官方 Make platform skills 安装/更新命令。
// 必须钉死 --global：上游 skills CLI 在项目目录内默认落 project scope（写 cwd 的
// skills-lock.json），而 makecli 的 lockfile / 清单 / 校验全部锚定全局 ~/.agents，
// 不钉 scope 会导致执行与清单看到的状态分裂。install.go / remove.go 同款约束。
func SkillsCommand() []string {
	return []string{"npx", "-y", "skills", "add", SkillsSource, "--all", "-y", "--global"}
}

var runSkillsCommand = defaultRunSkillsCommand

// Sync 同步 Make platform skills。除非显式 Skip，否则每次执行 npx 刷新。
func Sync(ctx context.Context, opts Options) (Result, error) {
	result := Result{
		Action:  ActionSynced,
		Source:  SkillsSource,
		Version: opts.Version,
		Command: SyncCommand(opts.Role),
	}

	if opts.Skip {
		result.Action = ActionSkipped
		result.Reason = "disabled by flag"
		return result, nil
	}

	if err := EnsureNpx(); err != nil {
		result.Action = ActionFailed
		return result, err
	}

	runCtx := ctx
	if runCtx == nil {
		runCtx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(runCtx, syncTimeout)
	defer cancel()

	output, err := runSkillsCommand(runCtx, result.Command)
	result.Output = strings.TrimSpace(output)
	if err != nil {
		result.Action = ActionFailed
		return result, fmt.Errorf("failed to sync Make platform skills: %w\nmanual fix: %s\n%s",
			err, result.CommandString(), trimOutput(result.Output))
	}

	return result, nil
}

func defaultRunSkillsCommand(ctx context.Context, command []string) (string, error) {
	if len(command) == 0 {
		return "", fmt.Errorf("empty command")
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// dedupSortedNames 返回去重排序后的副本，被 remove.go / install.go 的按名路径共用——
// 用户在命令行重复传同一个名字（如 `skills remove a a`）不该对上游 npx 发两次同名请求。
func dedupSortedNames(names []string) []string {
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	return slices.Compact(sorted)
}

func trimOutput(output string) string {
	const maxOutput = 4000
	if len(output) <= maxOutput {
		return output
	}
	return output[:maxOutput] + "\n... output truncated ..."
}
