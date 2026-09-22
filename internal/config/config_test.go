/**
 * [INPUT]: 依赖 config 包内的 parseConfigINI、LoadConfig、SaveConfig、SetSetting、ConfigPath（包内白盒）
 * [OUTPUT]: 覆盖 INI 解析与 config 读写全路径的单元测试（含 profile context 保留与 INI 注入拒绝）
 * [POS]: internal/config 模块 config.go 的配套测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------- Config ----------------------------------

func TestConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	want := filepath.Join(home, ".make", "config")
	if path != want {
		t.Errorf("ConfigPath = %q, want %q", path, want)
	}
}

func TestLoadConfigNonExistent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg) != 0 {
		t.Errorf("expected empty config, got %v", cfg)
	}
}

func TestSaveConfigAndLoad(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	original := Config{
		"default": {RepoServerURL: "https://repo.example/api/make", AuthServerURL: "https://auth.example", XTenantID: "tenant-1", OperatorID: "op-1"},
		"staging": {Context: "test", XTenantID: "tenant-2", OperatorID: ""},
	}

	if err := SaveConfig(original); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	path, _ := ConfigPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("config file not found: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("file permissions = %v, want 0600", info.Mode().Perm())
	}

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	for profile, want := range original {
		got := loaded[profile]
		if got.Context != want.Context {
			t.Errorf("profile %q: Context = %q, want %q", profile, got.Context, want.Context)
		}
		if got.RepoServerURL != want.RepoServerURL {
			t.Errorf("profile %q: RepoServerURL = %q, want %q", profile, got.RepoServerURL, want.RepoServerURL)
		}
		if got.XTenantID != want.XTenantID {
			t.Errorf("profile %q: XTenantID = %q, want %q", profile, got.XTenantID, want.XTenantID)
		}
		if got.OperatorID != want.OperatorID {
			t.Errorf("profile %q: OperatorID = %q, want %q", profile, got.OperatorID, want.OperatorID)
		}
		if got.AuthServerURL != want.AuthServerURL {
			t.Errorf("profile %q: AuthServerURL = %q, want %q", profile, got.AuthServerURL, want.AuthServerURL)
		}
	}
}

// TestSaveConfigRejectsInjection 锁定 INI 注入防线：含换行/首尾空白的值、非法 settings 键
// 都必须在落盘前被拒绝——否则 "x\n[evil]" 这类值会伪造出新的 section。
func TestSaveConfigRejectsInjection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("profile value with newline", func(t *testing.T) {
		err := SaveConfig(Config{"default": {MetaServerURL: "https://x\n[evil]\nmeta-server-url = https://attacker"}})
		if err == nil {
			t.Fatal("SaveConfig must reject a value containing a newline")
		}
	})

	t.Run("profile value with surrounding whitespace", func(t *testing.T) {
		if err := SaveConfig(Config{"default": {XTenantID: " padded "}}); err == nil {
			t.Fatal("SaveConfig must reject a value with leading/trailing whitespace")
		}
	})

	t.Run("setting value with newline", func(t *testing.T) {
		if err := SetSetting("environment", "test\n[evil]"); err == nil {
			t.Fatal("SetSetting must reject a value containing a newline")
		}
	})

	t.Run("invalid setting key", func(t *testing.T) {
		if err := SetSetting("bad key]", "x"); err == nil {
			t.Fatal("SetSetting must reject a key outside the INI key grammar")
		}
	})
}

func TestParseConfigINI(t *testing.T) {
	t.Run("both keys", func(t *testing.T) {
		f := writeTempINI(t, "[default]\nX-Tenant-ID = t1\nX-Operator-ID = o1\n")
		defer func() { _ = f.Close() }()

		cfg, err := parseConfigINI(f)
		if err != nil {
			t.Fatalf("parseConfigINI: %v", err)
		}
		if cfg["default"].XTenantID != "t1" || cfg["default"].OperatorID != "o1" {
			t.Errorf("unexpected config: %+v", cfg["default"])
		}
	})

	t.Run("partial keys", func(t *testing.T) {
		f := writeTempINI(t, "[default]\nX-Tenant-ID = t1\n")
		defer func() { _ = f.Close() }()

		cfg, err := parseConfigINI(f)
		if err != nil {
			t.Fatalf("parseConfigINI: %v", err)
		}
		if cfg["default"].XTenantID != "t1" {
			t.Errorf("XTenantID = %q, want %q", cfg["default"].XTenantID, "t1")
		}
		if cfg["default"].OperatorID != "" {
			t.Errorf("OperatorID = %q, want empty", cfg["default"].OperatorID)
		}
	})

	t.Run("auth-server-url key", func(t *testing.T) {
		f := writeTempINI(t, "[test]\nmeta-server-url = https://test-make.qtech.cn/api/make\nauth-server-url = https://test-myaccount.qtech.cn\n")
		defer func() { _ = f.Close() }()

		cfg, err := parseConfigINI(f)
		if err != nil {
			t.Fatalf("parseConfigINI: %v", err)
		}
		if cfg["test"].AuthServerURL != "https://test-myaccount.qtech.cn" {
			t.Errorf("AuthServerURL = %q, want %q", cfg["test"].AuthServerURL, "https://test-myaccount.qtech.cn")
		}
	})
}
