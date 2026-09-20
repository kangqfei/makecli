/**
 * [INPUT]: 依赖 doctor.go 的 runDoctor / errDoctorFailed / doctorFixHint；setProfile / setAccessTokenFlag / captureStdout 测试辅助；internal/config 隔离配置与凭证；regexp
 * [OUTPUT]: 覆盖 doctor 的单元测试（默认只读：旧键标 fixable 指引 --fix 且不改文件退出 1 / --fix 搬家到 context 且其后命令可用 / 健康配置 OK 退出 0 / 未知 context、channel 报 settings set 指引与缺 token 报问题退出 1 / --fix 修复后同轮 context 检查读到新键 / 哨兵静默 / --fix flag 注册）；squash + hasLine 让行断言不依赖名字列宽（列宽随最长检查名浮动）
 * [POS]: cmd 模块 doctor.go 的配套测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/config"
)

// squash 把连续空格压成一个：doctor 的名字列按最长检查名对齐，断言不该依赖列宽。
func squash(s string) string { return regexp.MustCompile(` {2,}`).ReplaceAllString(s, " ") }

// hasLine 拼出 squash 后一行的期望形态："<mark> <name> <msg>"。
func hasLine(mark, name, msg string) string { return mark + " " + name + " " + msg }

// healthyDoctorEnv 准备一份健康的隔离配置：token 来自 flag（不读凭证文件），无旧键。
func healthyDoctorEnv(t *testing.T) {
	t.Helper()
	t.Setenv(config.EnvConfigDir, t.TempDir())
	t.Setenv(EnvContext, "")
	t.Setenv(EnvAccessToken, "")
	setContextFlag(t, "")
	setProfile(t, "default")
	setAccessTokenFlag(t, "tok")
}

func TestDoctorReadOnlyByDefault(t *testing.T) {
	healthyDoctorEnv(t)
	if err := config.SetSetting("environment", "dev"); err != nil {
		t.Fatal(err)
	}

	var err error
	out := squash(captureStdout(t, func() { err = runDoctor(false) }))
	if !errors.Is(err, errDoctorFailed) {
		t.Fatalf("read-only doctor must report the fixable problem as failure, got %v\n%s", err, out)
	}
	for _, want := range []string{hasLine("✗", "settings", "outdated key(s): environment (fixable, run: "+doctorFixHint+")"), "FAIL: 1 problem left"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "fixed") {
		t.Errorf("read-only doctor must not fix anything:\n%s", out)
	}
	// 文件未被触碰：旧键仍在，解析链仍拒绝
	s, _ := config.LoadSettings()
	if s.Legacy["environment"] != "dev" || s.Context != "" {
		t.Errorf("read-only doctor modified settings: %+v", s)
	}
	if _, _, err := resolveContext(); err == nil {
		t.Error("legacy config should still be refused after read-only doctor")
	}
}

func TestDoctorFixMigratesLegacyEnvironment(t *testing.T) {
	healthyDoctorEnv(t)
	if err := config.SetSetting("environment", "dev"); err != nil {
		t.Fatal(err)
	}

	var err error
	out := squash(captureStdout(t, func() { err = runDoctor(true) }))
	if err != nil {
		t.Fatalf("runDoctor --fix: %v\n%s", err, out)
	}
	for _, want := range []string{hasLine("✗", "settings", "outdated key(s): environment"), hasLine("✓", "fixed", "renamed [settings] environment → context"), hasLine("✓", "context", "dev"), "OK: configuration is healthy"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "(fixable") {
		t.Errorf("--fix mode must not print the fixable hint:\n%s", out)
	}

	// 迁移后：文件只剩新键，解析链恢复且值保留
	s, _ := config.LoadSettings()
	if s.Context != "dev" || s.Legacy != nil {
		t.Errorf("post-migration settings = %+v", s)
	}
	name, _, err := resolveContext()
	if err != nil || name != "dev" {
		t.Errorf("resolveContext after doctor --fix = %q, %v", name, err)
	}
}

func TestDoctorHealthy(t *testing.T) {
	for _, fix := range []bool{false, true} {
		healthyDoctorEnv(t)
		var err error
		out := squash(captureStdout(t, func() { err = runDoctor(fix) }))
		if err != nil {
			t.Fatalf("runDoctor(fix=%v): %v\n%s", fix, err, out)
		}
		for _, want := range []string{"✓ settings", hasLine("✓", "context", "not set, defaults to production"), hasLine("✓", "channel", "not set, defaults to stable"), hasLine("✓", "token", `profile "default" (from flag)`), "OK: configuration is healthy"} {
			if !strings.Contains(out, want) {
				t.Errorf("fix=%v: output missing %q:\n%s", fix, want, out)
			}
		}
		if strings.Contains(out, "✗") {
			t.Errorf("fix=%v: healthy config should report no problems:\n%s", fix, out)
		}
	}
}

func TestDoctorReportsUnfixableProblems(t *testing.T) {
	t.Run("unknown context and channel", func(t *testing.T) {
		healthyDoctorEnv(t)
		if err := config.SetSetting("context", "staging"); err != nil {
			t.Fatal(err)
		}
		if err := config.SetSetting("channel", "nightly"); err != nil {
			t.Fatal(err)
		}
		var err error
		out := squash(captureStdout(t, func() { err = runDoctor(true) }))
		if !errors.Is(err, errDoctorFailed) {
			t.Fatalf("expected errDoctorFailed, got %v", err)
		}
		for _, want := range []string{hasLine("✗", "context", `unknown context "staging"`), "makecli settings set context <value>", hasLine("✗", "channel", `unknown channel "nightly"`), "makecli settings set channel <value>", "FAIL: 2 problems left"} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "(fixable") {
			t.Errorf("unfixable problems must not be marked fixable:\n%s", out)
		}
	})

	t.Run("missing token points to login", func(t *testing.T) {
		healthyDoctorEnv(t)
		setAccessTokenFlag(t, "")
		setProfile(t, "work")
		var err error
		out := squash(captureStdout(t, func() { err = runDoctor(false) }))
		if !errors.Is(err, errDoctorFailed) {
			t.Fatalf("expected errDoctorFailed, got %v", err)
		}
		if !strings.Contains(out, hasLine("✗", "token", `profile "work" has no access token — run: makecli login --profile work`)) {
			t.Errorf("output missing login guidance:\n%s", out)
		}
	})

	t.Run("credentials token counts", func(t *testing.T) {
		healthyDoctorEnv(t)
		setAccessTokenFlag(t, "")
		if err := config.Save(config.Credentials{"default": {AccessToken: "file-tok"}}); err != nil {
			t.Fatal(err)
		}
		var err error
		out := squash(captureStdout(t, func() { err = runDoctor(false) }))
		if err != nil {
			t.Fatalf("runDoctor: %v\n%s", err, out)
		}
		if !strings.Contains(out, hasLine("✓", "token", `profile "default" (from credentials)`)) {
			t.Errorf("output missing credentials source:\n%s", out)
		}
	})
}

func TestDoctorFixLegacyValueStillValidated(t *testing.T) {
	// 旧键搬家后值原样保留；若旧值本身非法，同一轮 context 检查须读到新键并报问题
	healthyDoctorEnv(t)
	if err := config.SetSetting("environment", "staging"); err != nil {
		t.Fatal(err)
	}
	var err error
	out := squash(captureStdout(t, func() { err = runDoctor(true) }))
	if !errors.Is(err, errDoctorFailed) {
		t.Fatalf("expected errDoctorFailed, got %v", err)
	}
	for _, want := range []string{hasLine("✓", "fixed", "renamed [settings] environment → context"), hasLine("✗", "context", `unknown context "staging"`)} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorSentinelIsSilent(t *testing.T) {
	var buf bytes.Buffer
	reportExecuteError(&buf, errDoctorFailed)
	if buf.Len() != 0 {
		t.Errorf("errDoctorFailed should be silent, got %q", buf.String())
	}
	if ExitCode(errDoctorFailed) != 1 {
		t.Errorf("ExitCode(errDoctorFailed) = %d, want 1", ExitCode(errDoctorFailed))
	}
}

func TestDoctorFixFlagRegistered(t *testing.T) {
	cmd := newDoctorCmd()
	f := cmd.Flags().Lookup("fix")
	if f == nil {
		t.Fatal("doctor should register --fix")
	}
	if f.DefValue != "false" {
		t.Errorf("--fix must default to false (read-only doctor), got %q", f.DefValue)
	}
}
