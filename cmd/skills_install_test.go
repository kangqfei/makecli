/**
 * [INPUT]: 依赖 bytes、context、errors、strings、testing、internal/skillsync
 * [OUTPUT]: 覆盖 skills install 的互斥校验/无参报错/确认流/-y 跳过/警告出 stderr/错误透传/非 TTY 门控/子命令注册
 * [POS]: cmd/skills install 子命令测试；planInstallFunc / installSkillsFunc / confirmInstallFunc 打桩隔离网络、npx 与终端交互
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/config"
	"github.com/qfeius/makecli/internal/skillsync"
)

// settingsRole 读回 [settings] role（"" = 未写）
func settingsRole(t *testing.T) string {
	t.Helper()
	s, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	return s.Role
}

func TestSkillsInstallRoleUser(t *testing.T) {
	plan := skillsync.InstallPlan{Names: []string{"makecli"}}
	rec := stubSkillsInstall(t, plan, nil, nil, nil)

	out, _, err := runInstallCmd("--role", "user", "-y")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if rec.planAll || !slices.Equal(rec.planNames, []string{"makecli"}) {
		t.Fatalf("user role must plan exactly makecli: names=%v all=%v", rec.planNames, rec.planAll)
	}
	if got := settingsRole(t); got != config.RoleUser {
		t.Fatalf("[settings] role = %q, want user", got)
	}
	if !strings.Contains(out, "makecli skill installed.") {
		t.Fatalf("missing success output:\n%s", out)
	}
}

func TestSkillsInstallAllClearsRole(t *testing.T) {
	rec := stubSkillsInstall(t, skillsync.InstallPlan{All: true}, nil, nil, nil)
	if err := config.SetSetting("role", config.RoleUser); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runInstallCmd("--all", "-y"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !rec.planAll || len(rec.planNames) != 0 {
		t.Fatalf("expected full install, got names=%v all=%v", rec.planNames, rec.planAll)
	}
	if got := settingsRole(t); got != "" {
		t.Fatalf("--all must clear [settings] role (back to full sync), got %q", got)
	}
}

func TestSkillsInstallAllWithRoleRejected(t *testing.T) {
	rec := stubSkillsInstall(t, skillsync.InstallPlan{}, nil, nil, nil)
	_, _, err := runInstallCmd("--all", "--role", "user")
	if err == nil || !strings.Contains(err.Error(), "cannot use --all with --role") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
	if rec.planCalls != 0 {
		t.Fatalf("plan must not run, got %d calls", rec.planCalls)
	}
}

func TestSkillsInstallByNameLeavesRoleAlone(t *testing.T) {
	stubSkillsInstall(t, skillsync.InstallPlan{Names: []string{"makedsl"}}, nil, nil, nil)
	if _, _, err := runInstallCmd("makedsl", "-y"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := settingsRole(t); got != "" {
		t.Fatalf("by-name install must not write role, got %q", got)
	}
}

func TestSkillsInstallRoleValidation(t *testing.T) {
	t.Run("unknown role rejected before plan", func(t *testing.T) {
		rec := stubSkillsInstall(t, skillsync.InstallPlan{}, nil, nil, nil)
		_, _, err := runInstallCmd("--role", "developer", "-y")
		if err == nil || !strings.Contains(err.Error(), `unknown role "developer", valid: user`) {
			t.Fatalf("expected unknown-role error listing valid roles, got %v", err)
		}
		if rec.planCalls != 0 || settingsRole(t) != "" {
			t.Fatalf("invalid role must neither plan nor persist: plan=%d role=%q", rec.planCalls, settingsRole(t))
		}
	})

	t.Run("role with names rejected", func(t *testing.T) {
		rec := stubSkillsInstall(t, skillsync.InstallPlan{}, nil, nil, nil)
		_, _, err := runInstallCmd("--role", "user", "makedsl")
		if err == nil || !strings.Contains(err.Error(), "cannot use --all / --role with skill names") {
			t.Fatalf("expected mutual-exclusion error, got %v", err)
		}
		if rec.planCalls != 0 {
			t.Fatalf("plan must not run, got %d calls", rec.planCalls)
		}
	})
}

func TestSkillsInstallRolePersistsAfterConfirmBeforeInstall(t *testing.T) {
	t.Run("declined confirm leaves role unset", func(t *testing.T) {
		stubSkillsInstall(t, skillsync.InstallPlan{Names: []string{"makecli"}}, nil, errors.New("install cancelled"), nil)
		if _, _, err := runInstallCmd("--role", "user"); err == nil {
			t.Fatal("expected cancellation error")
		}
		if got := settingsRole(t); got != "" {
			t.Fatalf("cancelled install must not persist role, got %q", got)
		}
	})

	t.Run("failed install keeps role for the next update to heal", func(t *testing.T) {
		stubSkillsInstall(t, skillsync.InstallPlan{Names: []string{"makecli"}}, nil, nil, errors.New("npx failed"))
		if _, _, err := runInstallCmd("--role", "user", "-y"); err == nil {
			t.Fatal("expected install error")
		}
		if got := settingsRole(t); got != config.RoleUser {
			t.Fatalf("role must be persisted before install runs, got %q", got)
		}
	})
}

// installStubs 汇集三个打桩点的调用记录。
type installStubs struct {
	planNames    []string
	planAll      bool
	planCalls    int
	confirmCalls int
	installCalls int
	installPlan  skillsync.InstallPlan
}

// stubSkillsInstall 打桩 planInstallFunc / confirmInstallFunc / installSkillsFunc。
func stubSkillsInstall(t *testing.T, plan skillsync.InstallPlan, planErr, confirmErr, installErr error) *installStubs {
	t.Helper()
	// --role 会写 [settings] role，每个测试隔离配置目录
	t.Setenv(config.EnvConfigDir, t.TempDir())
	rec := &installStubs{}

	origPlan := planInstallFunc
	planInstallFunc = func(_ context.Context, names []string, all bool) (skillsync.InstallPlan, error) {
		rec.planCalls++
		rec.planNames = names
		rec.planAll = all
		return plan, planErr
	}
	origConfirm := confirmInstallFunc
	confirmInstallFunc = func(skillsync.InstallPlan) error {
		rec.confirmCalls++
		return confirmErr
	}
	origInstall := installSkillsFunc
	installSkillsFunc = func(_ context.Context, p skillsync.InstallPlan) error {
		rec.installCalls++
		rec.installPlan = p
		return installErr
	}
	t.Cleanup(func() {
		planInstallFunc = origPlan
		confirmInstallFunc = origConfirm
		installSkillsFunc = origInstall
	})
	return rec
}

// runInstallCmd 构造并执行 install 子命令，返回 stdout/stderr 与错误。
func runInstallCmd(args ...string) (string, string, error) {
	cmd := newSkillsInstallCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestSkillsInstallByName(t *testing.T) {
	plan := skillsync.InstallPlan{Names: []string{"makedsl", "makeui"}}
	rec := stubSkillsInstall(t, plan, nil, nil, nil)

	out, _, err := runInstallCmd("makedsl", "makeui", "-y")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if rec.planAll || !slices.Equal(rec.planNames, []string{"makedsl", "makeui"}) {
		t.Fatalf("unexpected plan args: names=%v all=%v", rec.planNames, rec.planAll)
	}
	if rec.installCalls != 1 {
		t.Fatalf("expected 1 install call, got %d", rec.installCalls)
	}
	if !slices.Equal(rec.installPlan.Names, plan.Names) {
		t.Fatalf("plan must pass through unmodified: %+v", rec.installPlan)
	}
	if !strings.Contains(out, "makedsl, makeui skills installed.") {
		t.Fatalf("missing success output:\n%s", out)
	}
}

func TestSkillsInstallAll(t *testing.T) {
	plan := skillsync.InstallPlan{All: true, Names: []string{"makedsl", "makeui"}}
	rec := stubSkillsInstall(t, plan, nil, nil, nil)

	out, _, err := runInstallCmd("--all", "-y")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !rec.planAll || len(rec.planNames) != 0 {
		t.Fatalf("unexpected plan args: names=%v all=%v", rec.planNames, rec.planAll)
	}
	if !strings.Contains(out, "makedsl, makeui skills installed.") {
		t.Fatalf("missing success output:\n%s", out)
	}
}

func TestSkillsInstallAllUnknownList(t *testing.T) {
	// --all 且远端不可达：Names 为空，成功输出退化为固定句。
	rec := stubSkillsInstall(t, skillsync.InstallPlan{All: true}, nil, nil, nil)

	out, _, err := runInstallCmd("--all", "-y")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if rec.installCalls != 1 {
		t.Fatalf("expected 1 install call, got %d", rec.installCalls)
	}
	if !strings.Contains(out, "Installed all Make platform skills") {
		t.Fatalf("missing success output:\n%s", out)
	}
}

func TestSkillsInstallAllWithNamesRejected(t *testing.T) {
	rec := stubSkillsInstall(t, skillsync.InstallPlan{}, nil, nil, nil)

	_, _, err := runInstallCmd("--all", "makedsl")
	if err == nil || !strings.Contains(err.Error(), "cannot use --all / --role with skill names") {
		t.Fatalf("expected mutual-exclusion error, got: %v", err)
	}
	if rec.planCalls != 0 {
		t.Fatalf("plan must not run, got %d calls", rec.planCalls)
	}
}

func TestSkillsInstallRequiresNamesOrAll(t *testing.T) {
	rec := stubSkillsInstall(t, skillsync.InstallPlan{}, nil, nil, nil)

	_, _, err := runInstallCmd()
	if err == nil || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("expected usage error, got: %v", err)
	}
	if rec.planCalls != 0 {
		t.Fatalf("plan must not run, got %d calls", rec.planCalls)
	}
}

func TestSkillsInstallConfirmThenInstall(t *testing.T) {
	rec := stubSkillsInstall(t, skillsync.InstallPlan{Names: []string{"makedsl"}}, nil, nil, nil)

	if _, _, err := runInstallCmd("makedsl"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if rec.confirmCalls != 1 || rec.installCalls != 1 {
		t.Fatalf("expected confirm=1 install=1, got confirm=%d install=%d", rec.confirmCalls, rec.installCalls)
	}
}

func TestSkillsInstallConfirmDeclineShortCircuits(t *testing.T) {
	rec := stubSkillsInstall(t, skillsync.InstallPlan{Names: []string{"makedsl"}}, nil, errors.New("install cancelled"), nil)

	_, _, err := runInstallCmd("makedsl")
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected cancellation error, got: %v", err)
	}
	if rec.installCalls != 0 {
		t.Fatalf("install must not run after decline, got %d calls", rec.installCalls)
	}
}

func TestSkillsInstallYesSkipsConfirm(t *testing.T) {
	rec := stubSkillsInstall(t, skillsync.InstallPlan{Names: []string{"makedsl"}}, nil, errors.New("must not be called"), nil)

	if _, _, err := runInstallCmd("makedsl", "-y"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if rec.confirmCalls != 0 {
		t.Fatalf("confirm must be skipped with -y, got %d calls", rec.confirmCalls)
	}
}

func TestSkillsInstallWarningGoesToStderr(t *testing.T) {
	plan := skillsync.InstallPlan{Names: []string{"makedsl"}, Warning: "cannot verify skill names"}
	stubSkillsInstall(t, plan, nil, nil, nil)

	out, errOut, err := runInstallCmd("makedsl", "-y")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(errOut, "warning: cannot verify skill names") {
		t.Fatalf("warning missing from stderr:\n%s", errOut)
	}
	if strings.Contains(out, "warning:") {
		t.Fatalf("warning must not pollute stdout:\n%s", out)
	}
}

func TestSkillsInstallPlanErrorPropagates(t *testing.T) {
	rec := stubSkillsInstall(t, skillsync.InstallPlan{}, errors.New("unknown Make platform skills: nope"), nil, nil)

	_, _, err := runInstallCmd("nope", "-y")
	if err == nil || !strings.Contains(err.Error(), "unknown Make platform skills") {
		t.Fatalf("expected plan error, got: %v", err)
	}
	if rec.installCalls != 0 {
		t.Fatalf("install must not run on plan error, got %d calls", rec.installCalls)
	}
}

func TestSkillsInstallErrorPropagates(t *testing.T) {
	stubSkillsInstall(t, skillsync.InstallPlan{Names: []string{"makedsl"}}, nil, nil, errors.New("failed to install skills"))

	_, _, err := runInstallCmd("makedsl", "-y")
	if err == nil || !strings.Contains(err.Error(), "failed to install skills") {
		t.Fatalf("expected install error, got: %v", err)
	}
}

func TestConfirmInstallNonInteractiveRejects(t *testing.T) {
	// go test 环境 stdin 非 TTY，真门控直接生效。
	err := confirmInstall(skillsync.InstallPlan{Names: []string{"makedsl"}})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected non-interactive rejection hinting --yes, got: %v", err)
	}
}

func TestSkillsGroupHasInstall(t *testing.T) {
	for _, sub := range newSkillsCmd().Commands() {
		if sub.Name() == "install" {
			return
		}
	}
	t.Fatal("skills group missing install subcommand")
}
