/**
 * [INPUT]: 依赖 settings.go 的 setSetting / runSettingsGet / runSettingsList / settingKeys / newSettingsCmd；captureStdout / stubStdoutTerminal 测试辅助；internal/config 隔离配置
 * [OUTPUT]: 覆盖 settings 命令组的单元测试（set 各键写入与取值校验 / 未知键列合法键 / profile 键误投指路 configure 且不写 profile / 不受 --profile 影响、get 生效值与缺省回退、list 表格与 JSON 含来源、键表与 Settings 字段一一对应、子命令注册）
 * [POS]: cmd 模块 settings.go 的配套测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/config"
)

func TestSettingsSet(t *testing.T) {
	t.Run("context", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		if err := setSetting("context", "test"); err != nil {
			t.Fatal(err)
		}
		s, _ := config.LoadSettings()
		if s.Context != "test" {
			t.Errorf("context = %q, want test", s.Context)
		}
		if err := setSetting("context", "staging"); err == nil || !strings.Contains(err.Error(), "dev, test, production") {
			t.Errorf("expected unknown-context error listing valid names, got %v", err)
		}
	})

	t.Run("channel", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		if err := setSetting("channel", "beta"); err != nil {
			t.Fatal(err)
		}
		s, _ := config.LoadSettings()
		if s.Channel != config.ChannelBeta {
			t.Errorf("channel = %q, want beta", s.Channel)
		}
		if err := setSetting("channel", "nightly"); err == nil || !strings.Contains(err.Error(), "stable, beta") {
			t.Errorf("expected unknown-channel error listing valid names, got %v", err)
		}
	})

	t.Run("check-for-updates", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		if err := setSetting("check-for-updates", "false"); err != nil {
			t.Fatal(err)
		}
		s, _ := config.LoadSettings()
		if s.CheckForUpdates == nil || *s.CheckForUpdates {
			t.Errorf("check-for-updates = %v, want false", s.CheckForUpdates)
		}
		if err := setSetting("check-for-updates", "maybe"); err == nil {
			t.Error("expected error for non-boolean value")
		}
	})

	t.Run("unknown key lists valid keys", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		err := setSetting("colour", "blue")
		if err == nil || !strings.Contains(err.Error(), strings.Join(settingKeyNames(), ", ")) {
			t.Errorf("expected unknown-setting error listing keys, got %v", err)
		}
	})

	t.Run("profile key redirects to configure", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		for _, key := range validConfigKeys {
			if isSettingKey(key) {
				continue
			}
			err := setSetting(key, "x")
			if err == nil || !strings.Contains(err.Error(), "makecli configure set "+key) {
				t.Errorf("settings set %s: expected redirect to configure, got %v", key, err)
			}
			err = runSettingsGet(key)
			if err == nil || !strings.Contains(err.Error(), "makecli configure get "+key) {
				t.Errorf("settings get %s: expected redirect to configure, got %v", key, err)
			}
		}
		cfg, _ := config.LoadConfig()
		if len(cfg) != 0 {
			t.Errorf("settings must not write profiles: %v", cfg)
		}
	})

	t.Run("invalid value does not touch the file", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		_ = setSetting("context", "staging")
		s, _ := config.LoadSettings()
		if s.Context != "" {
			t.Errorf("invalid value must not be written, got %q", s.Context)
		}
	})

	t.Run("routes to settings regardless of --profile", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		setProfile(t, "work")
		if err := setSetting("context", "production"); err != nil {
			t.Fatal(err)
		}
		s, _ := config.LoadSettings()
		cfg, _ := config.LoadConfig()
		if s.Context != "production" || len(cfg) != 0 {
			t.Errorf("settings = %+v, profiles = %v", s, cfg)
		}
	})
}

func TestSettingsGet(t *testing.T) {
	t.Run("defaults when unset", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		for _, k := range settingKeys {
			out := captureStdout(t, func() {
				if err := runSettingsGet(k.name); err != nil {
					t.Errorf("get %s: %v", k.name, err)
				}
			})
			// role 无缺省：未设置就打空行
			if strings.TrimSpace(out) != k.def {
				t.Errorf("get %s = %q, want default %q", k.name, strings.TrimSpace(out), k.def)
			}
		}
	})

	t.Run("reflects configured value", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		if err := setSetting("channel", "beta"); err != nil {
			t.Fatal(err)
		}
		out := captureStdout(t, func() { _ = runSettingsGet("channel") })
		if strings.TrimSpace(out) != "beta" {
			t.Errorf("get channel = %q, want beta", strings.TrimSpace(out))
		}
	})

	t.Run("unknown key", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		if err := runSettingsGet("colour"); err == nil {
			t.Error("expected error for unknown key")
		}
	})
}

func TestSettingsList(t *testing.T) {
	t.Run("table shows every key with source", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		stubStdoutTerminal(t, true)
		if err := setSetting("context", "dev"); err != nil {
			t.Fatal(err)
		}
		out := captureStdout(t, func() {
			if err := runSettingsList(outputAuto); err != nil {
				t.Errorf("runSettingsList: %v", err)
			}
		})
		for _, want := range []string{"KEY", "VALUE", "SOURCE", "context", "dev", "channel", "stable", "role", "check-for-updates", "true"} {
			if !strings.Contains(out, want) {
				t.Errorf("table missing %q:\n%s", want, out)
			}
		}
		// role 无缺省且未设置：VALUE 列显示占位 "-"
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "role") && !strings.Contains(line, "-") {
				t.Errorf("unset role should render '-' placeholder: %q", line)
			}
		}
		if strings.Count(out, "config") != 1 || strings.Count(out, "default") != len(settingKeys)-1 {
			t.Errorf("source column wrong (want 1 config + %d default):\n%s", len(settingKeys)-1, out)
		}
	})

	t.Run("json carries key/value/source", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		if err := setSetting("check-for-updates", "false"); err != nil {
			t.Fatal(err)
		}
		out := captureStdout(t, func() {
			if err := runSettingsList(outputJSON); err != nil {
				t.Errorf("runSettingsList: %v", err)
			}
		})
		var views []settingJSONView
		if err := json.Unmarshal([]byte(out), &views); err != nil {
			t.Fatalf("json invalid: %v\n%s", err, out)
		}
		if len(views) != len(settingKeys) {
			t.Fatalf("got %d settings, want %d", len(views), len(settingKeys))
		}
		for _, v := range views {
			switch v.Key {
			case "check-for-updates":
				if v.Value != "false" || v.Source != "config" {
					t.Errorf("check-for-updates view = %+v", v)
				}
			default:
				if v.Source != "default" {
					t.Errorf("%s should come from default: %+v", v.Key, v)
				}
			}
		}
	})

	t.Run("invalid output rejected", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		if err := runSettingsList("yaml"); err == nil {
			t.Error("expected error for invalid output format")
		}
	})
}

// TestSettingKeysRoundTrip 守护键表与 loader 一致：每个键 set 后 value() 必须原样读回（漏接字段即红灯）。
func TestSettingKeysRoundTrip(t *testing.T) {
	samples := map[string]string{"context": "test", "channel": "beta", "role": "user", "check-for-updates": "false"}
	for _, k := range settingKeys {
		sample, ok := samples[k.name]
		if !ok {
			t.Fatalf("no round-trip sample for setting %q — add one", k.name)
		}
		t.Setenv(config.EnvConfigDir, t.TempDir())
		if err := setSetting(k.name, sample); err != nil {
			t.Fatalf("set %s: %v", k.name, err)
		}
		s, _ := config.LoadSettings()
		if got := k.value(s); got != sample {
			t.Errorf("%s: value() = %q after set %q", k.name, got, sample)
		}
	}
}

func TestSettingsCmdRegistration(t *testing.T) {
	cmd := newSettingsCmd()
	names := map[string]bool{}
	for _, sub := range cmd.Commands() {
		names[sub.Name()] = true
	}
	for _, want := range []string{"set", "get", "list"} {
		if !names[want] {
			t.Errorf("settings is missing subcommand %q", want)
		}
	}
}
