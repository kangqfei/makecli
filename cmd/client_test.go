/**
 * [INPUT]: 依赖 cmd 包内的 resolveAccessToken / metaServerURL / repoServerURL / resolveContext / contextName / 全局 AccessToken / MetaServerURL / Context（白盒），internal/config（Save/SetSetting）、strings、testing
 * [OUTPUT]: 覆盖 token 取值链（--access-token > $MAKE_ACCESS_TOKEN > credentials）、主机地址取值链（flag > $MAKE_*_SERVER_URL > profile config > context 内置地址）、context 解析优先级（flag > $MAKE_CLI_CONTEXT > settings > 默认、旧键 environment 拒绝并指引 doctor、flag 绕过旧键守卫、contextName 失败回显 unknown）与 withGateway 网关前缀拼接的单元测试
 * [POS]: cmd 模块 client.go resolveAccessToken / resolveContext / withGateway 的配套测试，t.Setenv 隔离配置
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/config"
)

// setContextFlag 临时覆盖全局 Context（--context），结束自动还原。
func setContextFlag(t *testing.T, name string) {
	t.Helper()
	old := Context
	Context = name
	t.Cleanup(func() { Context = old })
}

// setAccessTokenFlag 临时覆盖全局 AccessToken（--access-token），结束自动还原。
func setAccessTokenFlag(t *testing.T, token string) {
	t.Helper()
	old := AccessToken
	AccessToken = token
	t.Cleanup(func() { AccessToken = old })
}

// TestResolveAccessToken 锁定 token 取值链契约：flag > env > credentials，
// 以及每一级的 source 标识——configure verify 与鉴权引导都靠它回答「token 从哪来」。
func TestResolveAccessToken(t *testing.T) {
	cases := []struct {
		name       string
		flag, env  string
		file       string
		wantToken  string
		wantSource string
	}{
		{"flag over env and file", "from-flag", "from-env", "from-file", "from-flag", tokenSourceFlag},
		{"env over file", "", "from-env", "from-file", "from-env", tokenSourceEnv},
		{"file when nothing overrides", "", "", "from-file", "from-file", tokenSourceCredentials},
		{"empty everywhere", "", "", "", "", tokenSourceCredentials},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv(EnvAccessToken, tc.env)
			setAccessTokenFlag(t, tc.flag)
			if tc.file != "" {
				if err := config.Save(config.Credentials{"default": config.Profile{AccessToken: tc.file}}); err != nil {
					t.Fatal(err)
				}
			}
			token, source, err := resolveAccessToken()
			if err != nil {
				t.Fatalf("resolveAccessToken: %v", err)
			}
			if token != tc.wantToken || source != tc.wantSource {
				t.Errorf("got (%q, %s), want (%q, %s)", token, source, tc.wantToken, tc.wantSource)
			}
		})
	}
}

// setMetaServerURLFlag 临时覆盖全局 MetaServerURL，结束自动还原。
func setMetaServerURLFlag(t *testing.T, meta string) {
	t.Helper()
	old := MetaServerURL
	MetaServerURL = meta
	t.Cleanup(func() { MetaServerURL = old })
}

// TestServerURLChain 锁定主机地址取值链契约：flag > env > profile config > 环境内置地址；
// repo 链同构但没有 flag 级（代码仓库主机是部署实现细节），flag 只影响 meta。
func TestServerURLChain(t *testing.T) {
	cp := config.ConfigProfile{MetaServerURL: "https://cfg-meta", RepoServerURL: "https://cfg-repo"}
	env := config.Context{MetaServerURL: "https://preset-meta", RepoServerURL: "https://preset-repo"}
	cases := []struct {
		name               string
		flag, envVar       string
		cp                 config.ConfigProfile
		wantMeta, wantRepo string
	}{
		{"flag over everything (meta only; repo has no flag)", "https://flag", "https://env", cp, "https://flag", "https://env"},
		{"env over config", "", "https://env", cp, "https://env", "https://env"},
		{"config over preset", "", "", cp, "https://cfg-meta", "https://cfg-repo"},
		{"preset when nothing set", "", "", config.ConfigProfile{}, "https://preset-meta", "https://preset-repo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setMetaServerURLFlag(t, tc.flag)
			t.Setenv(EnvMetaServerURL, tc.envVar)
			t.Setenv(EnvRepoServerURL, tc.envVar)
			if got := metaServerURL(tc.cp, env); got != tc.wantMeta {
				t.Errorf("metaServerURL = %q, want %q", got, tc.wantMeta)
			}
			if got := repoServerURL(tc.cp, env); got != tc.wantRepo {
				t.Errorf("repoServerURL = %q, want %q", got, tc.wantRepo)
			}
		})
	}
}

func TestResolveContext(t *testing.T) {
	t.Run("default production when nothing set", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv(EnvContext, "")
		setContextFlag(t, "")
		name, c, err := resolveContext()
		if err != nil {
			t.Fatalf("resolveContext: %v", err)
		}
		if name != "production" || c.MetaServerURL != "https://make.qfei.cn" {
			t.Errorf("default = %q / %q", name, c.MetaServerURL)
		}
	})

	t.Run("settings context over default", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv(EnvContext, "")
		setContextFlag(t, "")
		if err := config.SetSetting("context", "test"); err != nil {
			t.Fatal(err)
		}
		name, c, err := resolveContext()
		if err != nil {
			t.Fatalf("resolveContext: %v", err)
		}
		if name != "test" || c.RepoServerURL != "https://test-make-repo.qtech.cn" {
			t.Errorf("got %q / %q, want test", name, c.RepoServerURL)
		}
	})

	t.Run("$MAKE_CLI_CONTEXT over settings", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		setContextFlag(t, "")
		if err := config.SetSetting("context", "test"); err != nil {
			t.Fatal(err)
		}
		t.Setenv(EnvContext, "dev")
		name, _, err := resolveContext()
		if err != nil {
			t.Fatalf("resolveContext: %v", err)
		}
		if name != "dev" {
			t.Errorf("name = %q, want dev (env var over settings)", name)
		}
	})

	t.Run("--context flag over env var and settings", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if err := config.SetSetting("context", "test"); err != nil {
			t.Fatal(err)
		}
		t.Setenv(EnvContext, "dev")
		setContextFlag(t, "production")
		name, c, err := resolveContext()
		if err != nil {
			t.Fatalf("resolveContext: %v", err)
		}
		if name != "production" || c.MetaServerURL != "https://make.qfei.cn" {
			t.Errorf("got %q / %q, want production", name, c.MetaServerURL)
		}
	})

	t.Run("unknown context errors", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		setContextFlag(t, "staging")
		if _, _, err := resolveContext(); err == nil {
			t.Error("expected error for unknown context")
		}
	})

	t.Run("legacy environment key refuses and points to doctor", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv(EnvContext, "")
		setContextFlag(t, "")
		if err := config.SetSetting("environment", "dev"); err != nil {
			t.Fatal(err)
		}
		_, _, err := resolveContext()
		if err == nil || !strings.Contains(err.Error(), "makecli doctor --fix") || !strings.Contains(err.Error(), "environment") {
			t.Fatalf("expected outdated-config error naming the legacy key and doctor, got %v", err)
		}
	})

	t.Run("flag bypasses the legacy guard", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if err := config.SetSetting("environment", "dev"); err != nil {
			t.Fatal(err)
		}
		setContextFlag(t, "test")
		if _, _, err := resolveContext(); err != nil {
			t.Fatalf("explicit --context should not consult settings: %v", err)
		}
	})
}

func TestContextName(t *testing.T) {
	t.Run("returns resolved name", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		setContextFlag(t, "dev")
		if got := contextName(); got != "dev" {
			t.Errorf("contextName = %q, want dev", got)
		}
	})
	t.Run("unknown on resolution failure", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		setContextFlag(t, "staging")
		if got := contextName(); got != "unknown" {
			t.Errorf("contextName = %q, want unknown", got)
		}
	})
}

// TestWithGateway 锁定网关前缀拼接的幂等契约：主机基址补 /api/make，
// 已含前缀/尾随斜杠/空串各自的归一化行为。
func TestWithGateway(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://test-make.qtech.cn", "https://test-make.qtech.cn/api/make"},          // 纯主机名补前缀
		{"https://test-make.qtech.cn/", "https://test-make.qtech.cn/api/make"},         // 尾随斜杠先裁后补
		{"https://test-make.qtech.cn/api/make", "https://test-make.qtech.cn/api/make"}, // 幂等：已含前缀原样返回
		{"https://x/api/make/", "https://x/api/make"},                                  // 幂等 + 裁尾斜杠
		{"", ""}, // 空串原样返回
	}
	for _, c := range cases {
		if got := withGateway(c.in); got != c.want {
			t.Errorf("withGateway(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveChannel(t *testing.T) {
	writeSettings := func(t *testing.T, content string) {
		t.Helper()
		dir := t.TempDir()
		t.Setenv(config.EnvConfigDir, dir)
		if content != "" {
			if err := os.WriteFile(filepath.Join(dir, "config"), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("unset falls back to stable", func(t *testing.T) {
		writeSettings(t, "")
		ch, err := resolveChannel()
		if err != nil {
			t.Fatal(err)
		}
		if ch != config.ChannelStable {
			t.Fatalf("channel = %q, want stable", ch)
		}
	})

	t.Run("beta from settings", func(t *testing.T) {
		writeSettings(t, "[settings]\nchannel = beta\n")
		ch, err := resolveChannel()
		if err != nil {
			t.Fatal(err)
		}
		if ch != config.ChannelBeta {
			t.Fatalf("channel = %q, want beta", ch)
		}
	})

	t.Run("unknown value rejected", func(t *testing.T) {
		writeSettings(t, "[settings]\nchannel = nightly\n")
		if _, err := resolveChannel(); err == nil {
			t.Fatal("expected error for unknown channel")
		}
	})
}
