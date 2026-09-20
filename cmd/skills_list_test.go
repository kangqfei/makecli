/**
 * [INPUT]: 依赖 context、encoding/json、strings、testing、internal/skillsync
 * [OUTPUT]: 覆盖 runSkillsList（table/json/空态/警告/非法格式）与 skills 默认行为 = list
 * [POS]: cmd/skills list 子命令测试，stubListSkills 打桩隔离 lockfile 与网络
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/config"
	"github.com/qfeius/makecli/internal/skillsync"
	"github.com/spf13/cobra"
)

// TestRunSkillsListRoleUserFiltersAvailable 锁定 available 按角色名单过滤：
// user 只关心 makecli，远端的 developer skills 不该被当成 more available。
func TestRunSkillsListRoleUserFiltersAvailable(t *testing.T) {
	stubListSkills(t, sampleInventory())
	if err := config.SetSetting("role", config.RoleUser); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runSkillsList(context.Background(), outputTable, false); err != nil {
			t.Errorf("runSkillsList: %v", err)
		}
	})
	if !strings.Contains(out, "2 installed, 1 outdated\n") || strings.Contains(out, "more available") {
		t.Errorf("user role must not count developer skills as available:\n%s", out)
	}

	// 名单内未装的 skill 仍计入
	inv := sampleInventory()
	inv.Skills = append(inv.Skills, skillsync.SkillInfo{Name: "makecli", Status: skillsync.StatusNotInstalled})
	stubListSkills(t, inv)
	if err := config.SetSetting("role", config.RoleUser); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() { _ = runSkillsList(context.Background(), outputTable, false) })
	if !strings.Contains(out, "(1 more available, --all to show)") {
		t.Errorf("makecli is in the user list and must count as available:\n%s", out)
	}
}

func TestRunSkillsListEmptyHintCarriesRole(t *testing.T) {
	stubListSkills(t, skillsync.Inventory{})
	if err := config.SetSetting("role", config.RoleUser); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { _ = runSkillsList(context.Background(), outputTable, false) })
	if !strings.Contains(out, "makecli skills install --role user --yes") {
		t.Errorf("empty state must guide with the configured role:\n%s", out)
	}
}

// stubListSkills 打桩 listSkillsFunc 返回固定 Inventory。
func stubListSkills(t *testing.T, inv skillsync.Inventory) {
	t.Helper()
	// list 读 [settings] role 决定 available 分母，隔离配置目录（缺省 developer = 全量）
	t.Setenv(config.EnvConfigDir, t.TempDir())
	orig := listSkillsFunc
	listSkillsFunc = func(ctx context.Context) skillsync.Inventory { return inv }
	t.Cleanup(func() { listSkillsFunc = orig })
}

func sampleInventory() skillsync.Inventory {
	return skillsync.Inventory{Skills: []skillsync.SkillInfo{
		{Name: "make-app-auth", Status: skillsync.StatusNotInstalled, RemoteHash: "d"},
		{Name: "makedsl", Status: skillsync.StatusOutdated, Description: "DSL 设计与生成", UpdatedAt: "2026-07-02T00:00:00.000Z", LocalHash: "a", RemoteHash: "b"},
		{Name: "makeui", Status: skillsync.StatusUpToDate, Description: "页面布局", UpdatedAt: "2026-07-01T00:00:00.000Z", LocalHash: "c", RemoteHash: "c"},
	}}
}

func TestRunSkillsListTable(t *testing.T) {
	stubListSkills(t, sampleInventory())

	out := captureStdout(t, func() {
		if err := runSkillsList(context.Background(), outputTable, false); err != nil {
			t.Errorf("runSkillsList: %v", err)
		}
	})

	for _, want := range []string{"NAME", "STATUS", "makedsl", "outdated", "makeui", "up-to-date"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// 默认视图只列已安装：远端可装条目不进表格，汇总行提示 --all
	if strings.Contains(out, "make-app-auth") || strings.Contains(out, "not installed") {
		t.Errorf("default view must hide not-installed skills:\n%s", out)
	}
	if !strings.Contains(out, "2 installed, 1 outdated (1 more available, --all to show)") {
		t.Errorf("missing summary line:\n%s", out)
	}
	if !strings.Contains(out, "makecli skills update") {
		t.Errorf("missing update hint:\n%s", out)
	}
	// UPDATED AT 裁到日期
	if !strings.Contains(out, "2026-07-02") || strings.Contains(out, "00:00:00") {
		t.Errorf("updated at must be date-only:\n%s", out)
	}
}

func TestRunSkillsListAllShowsCatalog(t *testing.T) {
	stubListSkills(t, sampleInventory())

	out := captureStdout(t, func() {
		if err := runSkillsList(context.Background(), outputTable, true); err != nil {
			t.Errorf("runSkillsList: %v", err)
		}
	})

	for _, want := range []string{"make-app-auth", "not installed", "makedsl", "makeui"} {
		if !strings.Contains(out, want) {
			t.Errorf("--all view missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "2 installed, 1 outdated, 1 available") {
		t.Errorf("missing catalog summary line:\n%s", out)
	}
}

func TestRunSkillsListJSON(t *testing.T) {
	stubListSkills(t, sampleInventory())

	out := captureStdout(t, func() {
		if err := runSkillsList(context.Background(), outputJSON, false); err != nil {
			t.Errorf("runSkillsList: %v", err)
		}
	})

	var payload struct {
		Data []skillsync.SkillInfo `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	// JSON 与表格同一过滤语义：默认只含已安装
	if len(payload.Data) != 2 {
		t.Fatalf("expected 2 installed skills, got %d", len(payload.Data))
	}
	if payload.Data[0].Description != "DSL 设计与生成" {
		t.Fatalf("JSON must keep full description: %+v", payload.Data[0])
	}
}

func TestRunSkillsListJSONAll(t *testing.T) {
	stubListSkills(t, sampleInventory())

	out := captureStdout(t, func() {
		if err := runSkillsList(context.Background(), outputJSON, true); err != nil {
			t.Errorf("runSkillsList: %v", err)
		}
	})

	var payload struct {
		Data []skillsync.SkillInfo `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(payload.Data) != 3 {
		t.Fatalf("expected full catalog of 3 skills, got %d", len(payload.Data))
	}
}

func TestRunSkillsListEmpty(t *testing.T) {
	stubListSkills(t, skillsync.Inventory{})

	out := captureStdout(t, func() {
		if err := runSkillsList(context.Background(), outputTable, false); err != nil {
			t.Errorf("runSkillsList: %v", err)
		}
	})

	if !strings.Contains(out, "No Make platform skills installed") {
		t.Errorf("missing empty state:\n%s", out)
	}
	if !strings.Contains(out, "makecli skills install --all --yes") {
		t.Errorf("empty state must guide installation (role unset → --all):\n%s", out)
	}
}

func TestRunSkillsListWarnings(t *testing.T) {
	inv := sampleInventory()
	inv.LockWarning = "schema version is 2"
	inv.RemoteErr = context.DeadlineExceeded
	stubListSkills(t, inv)

	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			if err := runSkillsList(context.Background(), outputTable, false); err != nil {
				t.Errorf("warnings must not fail the command: %v", err)
			}
		})
	})

	if !strings.Contains(stderr, "schema version is 2") {
		t.Errorf("stderr missing lock warning:\n%s", stderr)
	}
	if !strings.Contains(stderr, "remote check failed") {
		t.Errorf("stderr missing remote warning:\n%s", stderr)
	}
	if !strings.Contains(stdout, "makedsl") {
		t.Errorf("table must still render on warnings:\n%s", stdout)
	}
}

func TestRunSkillsListInvalidOutput(t *testing.T) {
	if err := runSkillsList(context.Background(), "xml", false); err == nil {
		t.Fatal("expected error for invalid output format")
	}
}

func TestSkillsDefaultIsList(t *testing.T) {
	stubListSkills(t, sampleInventory())

	cmd := newSkillsCmd()
	cmd.SetArgs([]string{})
	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Errorf("execute: %v", err)
		}
	})

	if !strings.Contains(out, "makedsl") {
		t.Errorf("bare 'makecli skills' must render list:\n%s", out)
	}
}

func TestSkillsUnknownSubcommandErrors(t *testing.T) {
	stubListSkills(t, sampleInventory())

	// 必须挂在 parent 下执行：cobra 的 legacyArgs 只对 root 命令做未知子命令
	// 检查，真实二进制里 skills 有 parent，未知子命令会静默回落到默认 list。
	root := &cobra.Command{Use: "makecli", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newSkillsCmd())
	root.SetArgs([]string{"skills", "remove", "makeui"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("typo subcommand must error, not fall back to list, got: %v", err)
	}
}

func TestTruncateLine(t *testing.T) {
	if got := truncateLine("短描述", 60); got != "短描述" {
		t.Fatalf("short string must pass through, got %q", got)
	}
	long := strings.Repeat("很", 70)
	got := truncateLine(long, 60)
	if len([]rune(got)) != 61 || !strings.HasSuffix(got, "…") {
		t.Fatalf("expected 60 runes + ellipsis, got %q", got)
	}
}
