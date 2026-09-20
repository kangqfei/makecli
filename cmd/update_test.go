/**
 * [INPUT]: 依赖 cmd 包内的 runUpdate / runUpdateCheck / applyFunc（白盒），internal/update 的 Release 类型 + SetAPIBaseURLForTest，internal/build 的 Version
 * [OUTPUT]: 覆盖 update 子命令决策逻辑的单元测试（含 --check 仅检查模式）
 * [POS]: cmd 模块 update.go 的配套测试，applyFunc 钩子打桩避免真实替换二进制
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/build"
	"github.com/qfeius/makecli/internal/config"
	"github.com/qfeius/makecli/internal/skillsync"
	"github.com/qfeius/makecli/internal/update"
	"github.com/spf13/cobra"
)

// setApplyFunc 在测试期间打桩 applyFunc 并在结束时恢复
func setApplyFunc(t *testing.T, f func(*update.Release) error) *bool {
	t.Helper()
	called := false
	old := applyFunc
	applyFunc = func(r *update.Release) error {
		called = true
		return f(r)
	}
	t.Cleanup(func() { applyFunc = old })
	return &called
}

// setSyncSkillsSuccess 在测试期间打桩 skills 同步，避免真实执行 npx。
func setSyncSkillsSuccess(t *testing.T) *[]skillsync.Options {
	t.Helper()
	// 后置同步读 [settings] role / channel，隔离配置目录不让本机 ~/.make/config 漏进来
	if os.Getenv(config.EnvConfigDir) == "" {
		t.Setenv(config.EnvConfigDir, t.TempDir())
	}
	var calls []skillsync.Options
	old := syncSkillsFunc
	syncSkillsFunc = func(_ context.Context, opts skillsync.Options) (skillsync.Result, error) {
		calls = append(calls, opts)
		action := skillsync.ActionSynced
		reason := ""
		if opts.Skip {
			action = skillsync.ActionSkipped
			reason = "disabled by flag"
		}
		return skillsync.Result{
			Action:  action,
			Reason:  reason,
			Source:  skillsync.SkillsSource,
			Version: opts.Version,
			Command: skillsync.SyncCommand(opts.Role),
		}, nil
	}
	t.Cleanup(func() { syncSkillsFunc = old })
	return &calls
}

// setBuildVersion 在测试期间覆盖 build.Version
func setBuildVersion(t *testing.T, v string) {
	t.Helper()
	old := build.Version
	build.Version = v
	t.Cleanup(func() { build.Version = old })
}

// mockReleaseServer 启动 httptest 服务器并替换 apiBaseURL
//
//	status == 0 时不显式 WriteHeader（默认 200）
//	body != nil 时返回 JSON 编码的 body
func mockReleaseServer(t *testing.T, status int, body any) func() {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status != 0 {
			w.WriteHeader(status)
		}
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
	old := update.SetAPIBaseURLForTest(srv.URL)
	return func() {
		update.SetAPIBaseURLForTest(old)
		srv.Close()
	}
}

// noopApply 是 applyFunc 的成功桩
func noopApply(_ *update.Release) error { return nil }

// dummyCmd 提供一个有 OutOrStdout 的 cobra.Command 实例供 runUpdate 使用
func dummyCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	return cmd
}

func bufferedCmd() (*cobra.Command, *bytes.Buffer) {
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	return cmd, &out
}

// ----------------------------------------------------------------------
// 无 arg：latest 流程
// ----------------------------------------------------------------------

func TestRunUpdate_NoArg_AlreadyLatest(t *testing.T) {
	setBuildVersion(t, "1.0.0")
	cleanup := mockReleaseServer(t, 0, update.Release{TagName: "v1.0.0"})
	defer cleanup()

	called := setApplyFunc(t, noopApply)
	syncCalls := setSyncSkillsSuccess(t)

	if err := runUpdate(dummyCmd(), "", false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *called {
		t.Error("applyFunc should not be called when already up to date")
	}
	if len(*syncCalls) != 1 || (*syncCalls)[0].Version != "v1.0.0" {
		t.Fatalf("sync calls = %+v, want one call for v1.0.0", *syncCalls)
	}
}

func TestRunUpdate_NoArg_Upgrade(t *testing.T) {
	setBuildVersion(t, "1.0.0")
	cleanup := mockReleaseServer(t, 0, update.Release{TagName: "v2.0.0"})
	defer cleanup()

	called := setApplyFunc(t, noopApply)
	syncCalls := setSyncSkillsSuccess(t)

	if err := runUpdate(dummyCmd(), "", false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !*called {
		t.Error("applyFunc should be called when newer release is available")
	}
	if len(*syncCalls) != 1 || (*syncCalls)[0].Version != "v2.0.0" {
		t.Fatalf("sync calls = %+v, want one call for v2.0.0", *syncCalls)
	}
}

// TestRunUpdate_SkillSyncFollowsRole 锁定 update 后置同步按 [settings] role 分流：
// user 只刷 makecli（按名命令），不会把 skills install --role user 的选择冲回全量。
func TestRunUpdate_SkillSyncFollowsRole(t *testing.T) {
	t.Setenv(config.EnvConfigDir, t.TempDir())
	if err := config.SetSetting("role", config.RoleUser); err != nil {
		t.Fatal(err)
	}
	setBuildVersion(t, "0.3.4")
	cleanup := mockReleaseServer(t, 0, update.Release{TagName: "v0.4.0"})
	defer cleanup()
	setApplyFunc(t, noopApply)
	syncCalls := setSyncSkillsSuccess(t)
	cmd, out := bufferedCmd()

	if err := runUpdate(cmd, "", false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*syncCalls) != 1 || (*syncCalls)[0].Role != config.RoleUser {
		t.Fatalf("sync calls = %+v, want one call with Role=user", *syncCalls)
	}
	if !strings.Contains(out.String(), "Skills command: npx -y skills add qfeius/make-platform-skills -s makecli -a * -y --global") {
		t.Fatalf("user role must sync by name:\n%s", out.String())
	}
}

func TestRunUpdate_NoArg_UpgradeKeepsBinaryOutputAndAddsSkillsOutput(t *testing.T) {
	t.Setenv(config.EnvConfigDir, t.TempDir()) // role 未配置 → 全量（原有行为）
	setBuildVersion(t, "0.3.4-3-g5197914")
	cleanup := mockReleaseServer(t, 0, update.Release{TagName: "v0.4.0"})
	defer cleanup()

	setApplyFunc(t, noopApply)
	setSyncSkillsSuccess(t)
	cmd, out := bufferedCmd()

	if err := runUpdate(cmd, "", false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"Checking for updates...",
		"Updating makecli: v0.3.4-3-g5197914 → v0.4.0",
		"Updated makecli: v0.3.4-3-g5197914 → v0.4.0",
		"Syncing Make platform skills: qfeius/make-platform-skills",
		"Skills command: npx -y skills add qfeius/make-platform-skills --all -y",
		"Skills updated for makecli v0.4.0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Skills state:") {
		t.Fatalf("output should not expose internal state path:\n%s", got)
	}
}

// ----------------------------------------------------------------------
// 指定版本
// ----------------------------------------------------------------------

func TestRunUpdate_SpecificVersion_Upgrade(t *testing.T) {
	setBuildVersion(t, "1.0.0")
	cleanup := mockReleaseServer(t, 0, update.Release{TagName: "v2.0.0"})
	defer cleanup()

	var appliedTag string
	called := setApplyFunc(t, func(r *update.Release) error {
		appliedTag = r.TagName
		return nil
	})
	syncCalls := setSyncSkillsSuccess(t)

	if err := runUpdate(dummyCmd(), "v2.0.0", false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !*called {
		t.Fatal("applyFunc should be called for upgrade")
	}
	if appliedTag != "v2.0.0" {
		t.Errorf("applied tag = %q, want v2.0.0", appliedTag)
	}
	if len(*syncCalls) != 1 || (*syncCalls)[0].Version != "v2.0.0" {
		t.Fatalf("sync calls = %+v, want one call for v2.0.0", *syncCalls)
	}
}

func TestRunUpdate_SpecificVersion_NormalizeWithoutV(t *testing.T) {
	setBuildVersion(t, "1.0.0")
	cleanup := mockReleaseServer(t, 0, update.Release{TagName: "v2.0.0"})
	defer cleanup()

	called := setApplyFunc(t, noopApply)
	setSyncSkillsSuccess(t)

	// 输入不带 v 前缀
	if err := runUpdate(dummyCmd(), "2.0.0", false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !*called {
		t.Error("applyFunc should be called when target normalizes to a newer version")
	}
}

func TestRunUpdate_SpecificVersion_SameVersion(t *testing.T) {
	setBuildVersion(t, "1.0.0")
	cleanup := mockReleaseServer(t, 0, update.Release{TagName: "v1.0.0"})
	defer cleanup()

	called := setApplyFunc(t, noopApply)
	syncCalls := setSyncSkillsSuccess(t)

	if err := runUpdate(dummyCmd(), "v1.0.0", false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *called {
		t.Error("applyFunc should NOT be called when target == current")
	}
	if len(*syncCalls) != 1 || (*syncCalls)[0].Version != "v1.0.0" {
		t.Fatalf("sync calls = %+v, want one call for v1.0.0", *syncCalls)
	}
}

func TestRunUpdate_SpecificVersion_DowngradeRefused(t *testing.T) {
	setBuildVersion(t, "2.0.0")
	cleanup := mockReleaseServer(t, 0, update.Release{TagName: "v1.0.0"})
	defer cleanup()

	called := setApplyFunc(t, noopApply)
	setSyncSkillsSuccess(t)

	err := runUpdate(dummyCmd(), "v1.0.0", false, false)
	if err == nil {
		t.Fatal("expected downgrade refusal error")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should hint at --force, got: %v", err)
	}
	if *called {
		t.Error("applyFunc should NOT be called on refused downgrade")
	}
}

func TestRunUpdate_SpecificVersion_DowngradeWithForce(t *testing.T) {
	setBuildVersion(t, "2.0.0")
	cleanup := mockReleaseServer(t, 0, update.Release{TagName: "v1.0.0"})
	defer cleanup()

	called := setApplyFunc(t, noopApply)
	setSyncSkillsSuccess(t)

	if err := runUpdate(dummyCmd(), "v1.0.0", true, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !*called {
		t.Error("applyFunc should be called with --force on downgrade")
	}
}

func TestRunUpdate_InvalidSemver(t *testing.T) {
	setBuildVersion(t, "1.0.0")
	// mock server should not be hit
	cleanup := mockReleaseServer(t, 0, update.Release{TagName: "v1.0.0"})
	defer cleanup()

	called := setApplyFunc(t, noopApply)
	setSyncSkillsSuccess(t)

	err := runUpdate(dummyCmd(), "abc", false, false)
	if err == nil {
		t.Fatal("expected error for invalid semver")
	}
	if *called {
		t.Error("applyFunc should NOT be called for invalid input")
	}
}

func TestRunUpdate_TagNotFound(t *testing.T) {
	setBuildVersion(t, "1.0.0")
	cleanup := mockReleaseServer(t, http.StatusNotFound, nil)
	defer cleanup()

	called := setApplyFunc(t, noopApply)
	setSyncSkillsSuccess(t)

	err := runUpdate(dummyCmd(), "v9.9.9", false, false)
	if err == nil {
		t.Fatal("expected error for missing tag")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should say 'not found', got: %v", err)
	}
	if *called {
		t.Error("applyFunc should NOT be called on tag not found")
	}
}

func TestRunUpdate_DEVSkipsComparison(t *testing.T) {
	setBuildVersion(t, "DEV")
	// 选一个明显"旧"的版本作为 target —— DEV 应该允许而不需要 --force
	cleanup := mockReleaseServer(t, 0, update.Release{TagName: "v0.0.1"})
	defer cleanup()

	called := setApplyFunc(t, noopApply)
	setSyncSkillsSuccess(t)

	if err := runUpdate(dummyCmd(), "v0.0.1", false, false); err != nil {
		t.Fatalf("DEV should allow apply without --force, got: %v", err)
	}
	if !*called {
		t.Error("applyFunc should be called when current is DEV")
	}
}

func TestRunUpdate_SkipSkillsPassesSkipOption(t *testing.T) {
	setBuildVersion(t, "1.0.0")
	cleanup := mockReleaseServer(t, 0, update.Release{TagName: "v1.0.0"})
	defer cleanup()

	setApplyFunc(t, noopApply)
	syncCalls := setSyncSkillsSuccess(t)

	if err := runUpdate(dummyCmd(), "", false, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*syncCalls) != 1 || !(*syncCalls)[0].Skip {
		t.Fatalf("sync calls = %+v, want Skip=true", *syncCalls)
	}
}

// ----------------------------------------------------------------------
// --check：仅检查不安装
// ----------------------------------------------------------------------

func TestRunUpdateCheck_UpdateAvailable(t *testing.T) {
	setBuildVersion(t, "0.3.4")
	cleanup := mockReleaseServer(t, 0, update.Release{
		TagName: "v0.3.6",
		HTMLURL: "https://github.com/qfeius/makecli/releases/tag/v0.3.6",
	})
	defer cleanup()

	// --check 永不安装：apply 被调用即失败
	called := setApplyFunc(t, noopApply)
	syncCalls := setSyncSkillsSuccess(t)

	cmd := dummyCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := runUpdateCheck(cmd, build.Version); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Update available: v0.3.4 → v0.3.6",
		"https://github.com/qfeius/makecli/releases/tag/v0.3.6",
		"https://github.com/qfeius/makecli/blob/main/CHANGELOG.md",
		"Run `makecli update` to install.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if *called {
		t.Error("--check must not install (applyFunc called)")
	}
	if len(*syncCalls) != 0 {
		t.Fatalf("--check must not sync skills, got calls: %+v", *syncCalls)
	}
}

func TestRunUpdateCheck_AlreadyLatest(t *testing.T) {
	setBuildVersion(t, "0.3.6")
	cleanup := mockReleaseServer(t, 0, update.Release{TagName: "v0.3.6"})
	defer cleanup()

	cmd := dummyCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := runUpdateCheck(cmd, build.Version); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Already up to date (v0.3.6)") {
		t.Errorf("output = %q, want 'Already up to date (v0.3.6)'", out)
	}
	if strings.Contains(out, "Update available") {
		t.Errorf("should not say update available when latest:\n%s", out)
	}
}

func TestUpdateCmd_CheckRejectsVersionArg(t *testing.T) {
	cmd := newUpdateCmd()
	cmd.SetArgs([]string{"--check", "v1.0.0"})
	cmd.SilenceErrors = true
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error: --check does not take a version argument")
	}
}

func TestRunUpdateCheckBetaChannel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvConfigDir, dir)
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte("[settings]\nchannel = beta\n"), 0600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/qfeius/makecli/releases" {
			t.Errorf("path = %s, want /repos/qfeius/makecli/releases", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"tag_name":"v9.9.9-beta.1","prerelease":true,"html_url":"https://example.com/r"}]`))
	}))
	defer srv.Close()
	old := update.SetAPIBaseURLForTest(srv.URL)
	defer update.SetAPIBaseURLForTest(old)

	var buf bytes.Buffer
	cmd := newUpdateCmd()
	cmd.SetOut(&buf)

	if err := runUpdateCheck(cmd, "0.5.5"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "v9.9.9-beta.1") {
		t.Fatalf("output missing beta tag: %s", out)
	}
	if !strings.Contains(out, "[beta channel]") {
		t.Fatalf("output missing channel echo: %s", out)
	}
}

func TestRunUpdateCheckStableHintsWhenCurrentBetaHigher(t *testing.T) {
	t.Setenv(config.EnvConfigDir, t.TempDir()) // channel 未配置 → stable
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.5.5","html_url":"https://example.com/r"}`))
	}))
	defer srv.Close()
	old := update.SetAPIBaseURLForTest(srv.URL)
	defer update.SetAPIBaseURLForTest(old)

	var buf bytes.Buffer
	cmd := newUpdateCmd()
	cmd.SetOut(&buf)

	if err := runUpdateCheck(cmd, "0.6.0-beta.1"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "pre-release above the stable channel") {
		t.Fatalf("output missing beta-above-stable hint: %s", out)
	}
	if !strings.Contains(out, "--force") {
		t.Fatalf("hint must mention --force downgrade path: %s", out)
	}
}
