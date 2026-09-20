/**
 * [INPUT]: 依赖 config 包内 LoadSettings / LoadConfig / MigrateSettings / ConfigPath / settingsSection（白盒）
 * [OUTPUT]: 覆盖 [settings] 全局段读取、旧键登记与搬家、profile 解析隔离的单元测试
 * [POS]: internal/config 模块 settings.go 的配套测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfigFile 在 ConfigPath 处写入 config 文件内容
func writeConfigFile(t *testing.T, content string) {
	t.Helper()
	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestLoadSettings_NoFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.CheckForUpdates != nil {
		t.Errorf("expected nil CheckForUpdates, got %v", *s.CheckForUpdates)
	}
}

func TestLoadSettings_Disabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeConfigFile(t, "[settings]\ncheck-for-updates = false\n\n[default]\nX-Tenant-ID = t1\n")

	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.CheckForUpdates == nil {
		t.Fatal("expected CheckForUpdates set, got nil")
	}
	if *s.CheckForUpdates {
		t.Errorf("CheckForUpdates = %v, want false", *s.CheckForUpdates)
	}
}

func TestLoadSettings_Enabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeConfigFile(t, "[settings]\ncheck-for-updates = true\n")
	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.CheckForUpdates == nil || *s.CheckForUpdates != true {
		t.Errorf("expected true, got %v", s.CheckForUpdates)
	}
}

func TestLoadConfig_IgnoresSettingsSection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeConfigFile(t, "[settings]\ncheck-for-updates = false\n\n[default]\nX-Tenant-ID = t1\n")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if _, ok := cfg[settingsSection]; ok {
		t.Error("settings section should not appear as a profile")
	}
	if cfg["default"].XTenantID != "t1" {
		t.Errorf("default profile lost: %+v", cfg["default"])
	}
}

func TestSaveConfig_PreservesSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeConfigFile(t, "[settings]\ncheck-for-updates = false\n\n[default]\nX-Tenant-ID = t1\n")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.CheckForUpdates == nil || *s.CheckForUpdates != false {
		t.Errorf("settings lost across SaveConfig round-trip: %v", s.CheckForUpdates)
	}
}

func TestLoadSettings_Context(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeConfigFile(t, "[settings]\ncontext = test\n")
	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.Context != "test" {
		t.Errorf("Context = %q, want test", s.Context)
	}
}

func TestSetSetting_WritesAndPreserves(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// 预置：一个 profile + 一个已有 settings 键
	writeConfigFile(t, "[settings]\ncheck-for-updates = false\n\n[default]\nmeta-server-url = https://x/api/make\n")

	if err := SetSetting("context", "production"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.Context != "production" {
		t.Errorf("Context = %q, want production", s.Context)
	}
	// 既有 settings 键保留
	if s.CheckForUpdates == nil || *s.CheckForUpdates != false {
		t.Errorf("check-for-updates lost across SetSetting: %v", s.CheckForUpdates)
	}
	// profile 段保留
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg["default"].MetaServerURL != "https://x/api/make" {
		t.Errorf("profile lost across SetSetting: %+v", cfg["default"])
	}
}

func TestSetSetting_NoExistingFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := SetSetting("context", "test"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.Context != "test" {
		t.Errorf("Context = %q, want test", s.Context)
	}
}

func TestLoadSettings_LegacyKeys(t *testing.T) {
	t.Run("legacy environment key is registered not translated", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		writeConfigFile(t, "[settings]\nenvironment = dev\n")
		s, err := LoadSettings()
		if err != nil {
			t.Fatalf("LoadSettings: %v", err)
		}
		if s.Context != "" {
			t.Errorf("legacy key must not be translated into Context, got %q", s.Context)
		}
		if s.Legacy["environment"] != "dev" {
			t.Errorf("Legacy = %v, want environment=dev", s.Legacy)
		}
	})

	t.Run("no legacy keys leaves Legacy nil", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		writeConfigFile(t, "[settings]\ncontext = dev\n")
		s, err := LoadSettings()
		if err != nil {
			t.Fatalf("LoadSettings: %v", err)
		}
		if s.Legacy != nil {
			t.Errorf("Legacy should be nil, got %v", s.Legacy)
		}
	})
}

func TestMigrateSettings(t *testing.T) {
	t.Run("moves environment to context and preserves the rest", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		writeConfigFile(t, "[settings]\nenvironment = dev\ncheck-for-updates = false\n\n[default]\nX-Tenant-ID = t1\n")

		moved, err := MigrateSettings()
		if err != nil {
			t.Fatalf("MigrateSettings: %v", err)
		}
		if moved["environment"] != "context" {
			t.Errorf("moved = %v, want environment→context", moved)
		}
		s, err := LoadSettings()
		if err != nil {
			t.Fatalf("LoadSettings: %v", err)
		}
		if s.Context != "dev" {
			t.Errorf("Context = %q, want dev", s.Context)
		}
		if s.Legacy != nil {
			t.Errorf("legacy key should be gone, got %v", s.Legacy)
		}
		if s.CheckForUpdates == nil || *s.CheckForUpdates {
			t.Errorf("check-for-updates lost across migration: %v", s.CheckForUpdates)
		}
		cfg, _ := LoadConfig()
		if cfg["default"].XTenantID != "t1" {
			t.Errorf("profile lost across migration: %+v", cfg["default"])
		}
	})

	t.Run("new key wins when both present", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		writeConfigFile(t, "[settings]\nenvironment = dev\ncontext = test\n")

		moved, err := MigrateSettings()
		if err != nil {
			t.Fatalf("MigrateSettings: %v", err)
		}
		if moved["environment"] != "context" {
			t.Errorf("moved = %v, want environment→context", moved)
		}
		s, _ := LoadSettings()
		if s.Context != "test" {
			t.Errorf("Context = %q, want test (new key must win)", s.Context)
		}
		if s.Legacy != nil {
			t.Errorf("legacy key should be gone, got %v", s.Legacy)
		}
	})

	t.Run("nothing to migrate returns empty", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		writeConfigFile(t, "[settings]\ncontext = dev\n")
		moved, err := MigrateSettings()
		if err != nil {
			t.Fatalf("MigrateSettings: %v", err)
		}
		if len(moved) != 0 {
			t.Errorf("moved = %v, want empty", moved)
		}
	})
}

func TestValidateProfileName(t *testing.T) {
	if err := ValidateProfileName("settings"); err == nil {
		t.Error("'settings' must be rejected as a profile name (reserved section)")
	}
	valid := []string{
		"default", "test", "production", "my-profile",
		"a", "A1", "user.name_x-1",
		"a" + strings.Repeat("b", 63), // 64 字符上限恰好通过
	}
	for _, name := range valid {
		if err := ValidateProfileName(name); err != nil {
			t.Errorf("%q should be a valid profile name: %v", name, err)
		}
	}
	// 文法收紧：空名、INI 语法字符（括号/换行）、空白、非法首字符、超长——全部拒绝，
	// 否则 profile 名会被原样写进 [section] 头，"evil]\n[other" 可注入新段。
	invalid := []string{
		"",
		"evil]\n[other",
		"[bracket",
		"closing]",
		"has space",
		"tab\tname",
		"newline\nname",
		"-leading-dash",
		".leading-dot",
		"_leading-underscore",
		"a" + strings.Repeat("b", 64), // 65 字符超长
	}
	for _, name := range invalid {
		if err := ValidateProfileName(name); err == nil {
			t.Errorf("%q must be rejected as a profile name", name)
		}
	}
}

func TestSaveRejectsReservedProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(Config{"settings": {MetaServerURL: "https://x"}}); err == nil {
		t.Error("SaveConfig should reject a profile named 'settings'")
	}
	if err := Save(Credentials{"settings": {AccessToken: "tok"}}); err == nil {
		t.Error("Save (credentials) should reject a profile named 'settings'")
	}
}
