/**
 * [INPUT]: 依赖 cmd 包内的 mask、validateJWT、validateConfigKey、sampleConfig（包内白盒）
 * [OUTPUT]: 覆盖凭证遮掩、JWT 校验、config key 校验、context set/get（含旧键 environment 已非法 key）、sample 模板完整性与真实 loader 有效性的单元测试
 * [POS]: cmd 模块 configure.go 的配套测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
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

func TestConfigureSetContext(t *testing.T) {
	t.Run("valid context writes to settings", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if err := runConfigureSet("context", "test"); err != nil {
			t.Fatalf("runConfigureSet: %v", err)
		}
		s, err := config.LoadSettings()
		if err != nil {
			t.Fatal(err)
		}
		if s.Context != "test" {
			t.Errorf("settings context = %q, want test", s.Context)
		}
	})

	t.Run("invalid context rejected", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if err := runConfigureSet("context", "staging"); err == nil {
			t.Error("expected error for invalid context value")
		}
	})

	t.Run("legacy environment key is no longer a config key", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if err := runConfigureSet("environment", "test"); err == nil {
			t.Error("expected unknown-key error for 'environment'")
		}
	})

	t.Run("routes to settings regardless of --profile", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		setProfile(t, "test")
		if err := runConfigureSet("context", "production"); err != nil {
			t.Fatalf("runConfigureSet: %v", err)
		}
		s, _ := config.LoadSettings()
		if s.Context != "production" {
			t.Errorf("context not written to settings: %q", s.Context)
		}
	})
}

func TestConfigureSetGetChannel(t *testing.T) {
	t.Run("set beta writes to settings", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if err := runConfigureSet("channel", "beta"); err != nil {
			t.Fatalf("runConfigureSet: %v", err)
		}
		s, err := config.LoadSettings()
		if err != nil {
			t.Fatal(err)
		}
		if s.Channel != config.ChannelBeta {
			t.Errorf("settings channel = %q, want beta", s.Channel)
		}
	})

	t.Run("get prints configured channel", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if err := runConfigureSet("channel", "beta"); err != nil {
			t.Fatal(err)
		}
		out := captureStdout(t, func() {
			if err := runConfigureGet("channel"); err != nil {
				t.Errorf("runConfigureGet: %v", err)
			}
		})
		if strings.TrimSpace(out) != "beta" {
			t.Errorf("get channel = %q, want beta", strings.TrimSpace(out))
		}
	})

	t.Run("get defaults to stable when unset", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		out := captureStdout(t, func() {
			if err := runConfigureGet("channel"); err != nil {
				t.Errorf("runConfigureGet: %v", err)
			}
		})
		if strings.TrimSpace(out) != "stable" {
			t.Errorf("get channel = %q, want stable (缺省回退)", strings.TrimSpace(out))
		}
	})

	t.Run("unknown channel rejected", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		err := runConfigureSet("channel", "nightly")
		if err == nil || !strings.Contains(err.Error(), "stable, beta") {
			t.Fatalf("expected unknown-channel error listing valid names, got %v", err)
		}
	})
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
		for _, key := range []string{"context", "check-for-updates", "channel"} {
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

func TestConfigureGetContext(t *testing.T) {
	t.Run("default production when unset", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		out := captureStdout(t, func() { _ = runConfigureGet("context") })
		if strings.TrimSpace(out) != "production" {
			t.Errorf("get context = %q, want production", strings.TrimSpace(out))
		}
	})

	t.Run("reflects settings value", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if err := config.SetSetting("context", "test"); err != nil {
			t.Fatal(err)
		}
		out := captureStdout(t, func() { _ = runConfigureGet("context") })
		if strings.TrimSpace(out) != "test" {
			t.Errorf("get context = %q, want test", strings.TrimSpace(out))
		}
	})
}
