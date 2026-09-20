/**
 * [INPUT]: 依赖 config 包内的 RoleNames/RoleUser 与 LoadSettings / SetSetting / UnsetSetting（白盒），slices、testing
 * [OUTPUT]: 覆盖角色常量、名单与 [settings] role 读写删的单元测试
 * [POS]: internal/config 模块 role.go 的配套测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package config

import (
	"slices"
	"testing"
)

func TestRoleNames(t *testing.T) {
	if want := []string{"user"}; !slices.Equal(RoleNames(), want) {
		t.Errorf("RoleNames = %v, want %v (unset role = full install, no developer value)", RoleNames(), want)
	}
}

func TestLoadSettings_Role(t *testing.T) {
	t.Run("unset is empty", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		writeConfigFile(t, "[settings]\ncontext = dev\n")
		s, err := LoadSettings()
		if err != nil {
			t.Fatal(err)
		}
		if s.Role != "" {
			t.Errorf("Role = %q, want empty", s.Role)
		}
	})

	t.Run("reads configured role", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		writeConfigFile(t, "[settings]\nrole = user\n")
		s, err := LoadSettings()
		if err != nil {
			t.Fatal(err)
		}
		if s.Role != RoleUser {
			t.Errorf("Role = %q, want user", s.Role)
		}
	})
}

func TestUnsetSetting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeConfigFile(t, "[settings]\nrole = user\ncontext = dev\n\n[default]\nX-Tenant-ID = t1\n")

	if err := UnsetSetting("role"); err != nil {
		t.Fatalf("UnsetSetting: %v", err)
	}
	s, _ := LoadSettings()
	if s.Role != "" || s.Context != "dev" {
		t.Errorf("settings after unset = %+v, want role cleared and context kept", s)
	}
	cfg, _ := LoadConfig()
	if cfg["default"].XTenantID != "t1" {
		t.Errorf("profile lost across UnsetSetting: %+v", cfg["default"])
	}
	// 幂等：再删一次不报错
	if err := UnsetSetting("role"); err != nil {
		t.Fatalf("UnsetSetting twice: %v", err)
	}
}
