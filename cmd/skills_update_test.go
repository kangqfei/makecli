/**
 * [INPUT]: 依赖 bytes、context、strings、testing、internal/skillsync
 * [OUTPUT]: 覆盖 skills update 子命令走 runSkillSync 且不跳过
 * [POS]: cmd/skills update 子命令测试，syncSkillsFunc 打桩避免真实执行 npx
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/config"
	"github.com/qfeius/makecli/internal/skillsync"
)

func TestSkillsUpdateRunsSync(t *testing.T) {
	t.Setenv(config.EnvConfigDir, t.TempDir()) // 同步读 [settings] role，隔离本机配置
	var gotOpts skillsync.Options
	orig := syncSkillsFunc
	syncSkillsFunc = func(ctx context.Context, opts skillsync.Options) (skillsync.Result, error) {
		gotOpts = opts
		return skillsync.Result{
			Action:  skillsync.ActionSynced,
			Source:  skillsync.SkillsSource,
			Version: opts.Version,
			Command: skillsync.SkillsCommand(),
		}, nil
	}
	t.Cleanup(func() { syncSkillsFunc = orig })

	cmd := newSkillsUpdateCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if gotOpts.Skip {
		t.Fatal("skills update must never skip sync")
	}
	if !strings.Contains(buf.String(), "Syncing Make platform skills") {
		t.Fatalf("missing sync start output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "Skills updated") {
		t.Fatalf("missing sync result output:\n%s", buf.String())
	}
}
