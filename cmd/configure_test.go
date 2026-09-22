/**
 * [INPUT]: 依赖 cmd 包内的 mask、validateJWT、validateConfigKey、runConfigureSet/Get、settingKeyNames、sampleConfig（包内白盒）
 * [OUTPUT]: 覆盖凭证遮掩、JWT 校验、config key 校验、profile context 读写/校验/清除与全局隔离、仅限全局的键误投 configure 时拒绝并指路 settings 且不落盘、sample 模板完整性与真实 loader 有效性的单元测试
 * [POS]: cmd 模块 configure.go 的配套测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/config"
)

func TestMask(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"ab", "**"},
		{"abcd", "****"},   // 恰好 4 位 → 全遮掩
		{"abcde", "*bcde"}, // 5 位 → 1 星 + 末4位
		{"hello", "*ello"},
		{"12345678", "****5678"}, // 8 位 → 4 星 + 末4位
	}

	for _, tt := range tests {
		got := mask(tt.input)
		if got != tt.want {
			t.Errorf("mask(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidateJWT(t *testing.T) {
	// ---------------------------------- 合法 JWT ----------------------------------
	// header.payload.signature 每段均为合法 base64url
	validSeg := "eyJhbGciOiJIUzI1NiJ9" // {"alg":"HS256"}
	validJWT := validSeg + "." + validSeg + "." + validSeg

	if err := validateJWT(validJWT); err != nil {
		t.Errorf("valid JWT returned error: %v", err)
	}

	// ---------------------------------- 非法格式 ----------------------------------
	cases := []struct {
		name  string
		token string
	}{
		{"two segments", "only.two"},
		{"four segments", "a.b.c.d"},
		{"invalid base64url in first segment", "invalid!@#." + validSeg + "." + validSeg},
		{"empty string", ""},
	}

	for _, tt := range cases {
		if err := validateJWT(tt.token); err == nil {
			t.Errorf("validateJWT(%q) [%s]: expected error, got nil", tt.token, tt.name)
		}
	}
}

func TestValidConfigKeys(t *testing.T) {
	if err := validateConfigKey("meta-server-url"); err != nil {
		t.Errorf("meta-server-url should be valid: %v", err)
	}
	if err := validateConfigKey("repo-server-url"); err != nil {
		t.Errorf("repo-server-url should be valid: %v", err)
	}
	if err := validateConfigKey("auth-server-url"); err != nil {
		t.Errorf("auth-server-url should be valid: %v", err)
	}
	if err := validateConfigKey("X-Tenant-ID"); err != nil {
		t.Errorf("X-Tenant-ID should be valid: %v", err)
	}
	if err := validateConfigKey("X-Operator-ID"); err != nil {
		t.Errorf("X-Operator-ID should be valid: %v", err)
	}
	if err := validateConfigKey("bad-key"); err == nil {
		t.Error("bad-key should be invalid")
	}
}

// TestConfigureRejectsSettingKeys 守护 configure set/get 对全局键的拒绝并指路 settings（不静默转发）。
func TestConfigureRejectsSettingKeys(t *testing.T) {
	t.Setenv(config.EnvConfigDir, t.TempDir())
	for _, key := range settingKeyNames() {
		if slices.Contains(validConfigKeys, key) {
			continue
		}
		err := runConfigureSet(key, "x")
		if err == nil || !strings.Contains(err.Error(), "makecli settings set "+key) {
			t.Errorf("configure set %s: expected redirect to settings, got %v", key, err)
		}
		err = runConfigureGet(key)
		if err == nil || !strings.Contains(err.Error(), "makecli settings get "+key) {
			t.Errorf("configure get %s: expected redirect to settings, got %v", key, err)
		}
	}
	// 拒绝发生在写盘之前：settings 段不得被触碰
	s, _ := config.LoadSettings()
	if s.Context != "" || s.Channel != "" || s.CheckForUpdates != nil {
		t.Errorf("configure must not write settings: %+v", s)
	}
}

func TestReservedProfileName(t *testing.T) {
	t.Run("configure set rejects --profile settings", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		setProfile(t, "settings")
		if err := runConfigureSet("auth-server-url", "https://x"); err == nil {
			t.Error("expected error writing to reserved profile 'settings'")
		}
	})

	t.Run("resolveProfile rejects settings", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		setProfile(t, "settings")
		if _, _, _, err := resolveProfile(); err == nil {
			t.Error("resolveProfile should reject reserved profile 'settings'")
		}
	})
}

func TestSampleConfig(t *testing.T) {
	// ---- 完整性：每个可写 profile key 都必须在 sample 里露面（漏键即红灯）----
	t.Run("documents every configurable key", func(t *testing.T) {
		for _, key := range validConfigKeys {
			if !strings.Contains(sampleConfig, key) {
				t.Errorf("sampleConfig missing profile key %q", key)
			}
		}
		for _, key := range settingKeyNames() {
			if slices.Contains(validConfigKeys, key) {
				continue
			}
			if !strings.Contains(sampleConfig, key) {
				t.Errorf("sampleConfig missing settings key %q", key)
			}
		}
	})

	// ---- 有效性：sample 必须被真实 loader 解析，且活跃的 context 是合法 context 名 ----
	t.Run("parses through real loader with valid active values", func(t *testing.T) {
		t.Setenv(config.EnvConfigDir, t.TempDir())
		path, err := config.ConfigPath()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(sampleConfig), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := config.LoadSettings()
		if err != nil {
			t.Fatalf("LoadSettings on sample: %v", err)
		}
		if !slices.Contains(config.ContextNames(), s.Context) {
			t.Errorf("sample active context %q not in %v", s.Context, config.ContextNames())
		}
		if s.Legacy != nil {
			t.Errorf("sample must not use legacy settings keys: %v", s.Legacy)
		}
		if !slices.Contains(config.ChannelNames(), s.Channel) {
			t.Errorf("sample active channel %q not in %v", s.Channel, config.ChannelNames())
		}
		// profile 覆盖键平铺为激活占位值 → 每个 ConfigProfile 字段都应解析出非空值，
		// 证明每行 `key = value` 都被真实 loader 接住（漏键/写坏即空，红灯）
		cfg, err := config.LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig on sample: %v", err)
		}
		def := cfg["default"]
		for name, got := range map[string]string{
			"context":         def.Context,
			"meta-server-url": def.MetaServerURL,
			"repo-server-url": def.RepoServerURL,
			"auth-server-url": def.AuthServerURL,
			"X-Tenant-ID":     def.XTenantID,
			"X-Operator-ID":   def.OperatorID,
		} {
			if got == "" {
				t.Errorf("sample default profile key %q parsed empty (commented out or malformed?)", name)
			}
		}
	})
}

func TestConfigureProfileContext(t *testing.T) {
	t.Setenv(config.EnvConfigDir, t.TempDir())
	setProfile(t, "named")
	if err := config.SetSetting("context", "production"); err != nil {
		t.Fatal(err)
	}
	if err := runConfigureSet("context", "test"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := runConfigureGet("context"); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) != "test" {
		t.Fatalf("get = %q", out)
	}
	if err := runConfigureSet("context", "typo"); err == nil {
		t.Fatal("invalid context accepted")
	}
	cfg, err := config.LoadConfig()
	if err != nil || cfg["named"].Context != "test" {
		t.Fatalf("invalid write changed profile: %v, %v", cfg, err)
	}
	if err := config.SetSetting("channel", "beta"); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadConfig()
	if err != nil || cfg["named"].Context != "test" {
		t.Fatalf("settings write lost context: %v, %v", cfg, err)
	}
	s, err := config.LoadSettings()
	if err != nil || s.Context != "production" {
		t.Fatalf("global context changed: %+v, %v", s, err)
	}
	if err := runConfigureSet("context", ""); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadConfig()
	if err != nil || cfg["named"].Context != "" {
		t.Fatalf("clear failed: %v, %v", cfg, err)
	}
}
