/**
 * [INPUT]: 依赖 cmd 包内的 resolveAccessToken / metaServerURL / repoServerURL / resolveEnvironment / 全局 AccessToken / MetaServerURL / Environment（白盒），internal/config（Save/SetSetting）、testing
 * [OUTPUT]: 覆盖 token 取值链（--access-token > $MAKE_ACCESS_TOKEN > credentials）、主机地址取值链（flag > $MAKE_*_SERVER_URL > profile config > 环境内置地址）、环境解析优先级（flag > settings > 默认）与 withGateway 网关前缀拼接的单元测试
 * [POS]: cmd 模块 client.go resolveAccessToken / resolveEnvironment / withGateway 的配套测试，t.Setenv 隔离配置
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qfeius/makecli/internal/config"
)

// setEnvFlag 临时覆盖全局 Environment（--env），结束自动还原。
func setEnvFlag(t *testing.T, name string) {
	t.Helper()
	old := Environment
	Environment = name
	t.Cleanup(func() { Environment = old })
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
	env := config.Environment{MetaServerURL: "https://preset-meta", RepoServerURL: "https://preset-repo"}
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

func TestResolveEnvironment(t *testing.T) {
	t.Run("default production when nothing set", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		setEnvFlag(t, "")
		env, err := resolveEnvironment()
		if err != nil {
			t.Fatalf("resolveEnvironment: %v", err)
		}
		if env.MetaServerURL != "https://make.qfei.cn" {
			t.Errorf("default MetaServerURL = %q", env.MetaServerURL)
		}
	})

	t.Run("settings environment over default", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		setEnvFlag(t, "")
		if err := config.SetSetting("environment", "test"); err != nil {
			t.Fatal(err)
		}
		env, err := resolveEnvironment()
		if err != nil {
			t.Fatalf("resolveEnvironment: %v", err)
		}
		if env.RepoServerURL != "https://test-make-repo.qtech.cn" {
			t.Errorf("RepoServerURL = %q, want test", env.RepoServerURL)
		}
	})

	t.Run("--env flag over settings", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if err := config.SetSetting("environment", "test"); err != nil {
			t.Fatal(err)
		}
		setEnvFlag(t, "production")
		env, err := resolveEnvironment()
		if err != nil {
			t.Fatalf("resolveEnvironment: %v", err)
		}
		if env.MetaServerURL != "https://make.qfei.cn" {
			t.Errorf("MetaServerURL = %q, want production", env.MetaServerURL)
		}
	})

	t.Run("unknown environment errors", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		setEnvFlag(t, "staging")
		if _, err := resolveEnvironment(); err == nil {
			t.Error("expected error for unknown environment")
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
