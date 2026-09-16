/**
 * [INPUT]: 依赖 config 包内的 Environment/LookupEnvironment/EnvironmentNames/DefaultEnvironment（包内白盒），slices、testing
 * [OUTPUT]: 覆盖环境 preset 查表与命名的单元测试
 * [POS]: internal/config 模块 environment.go 的配套测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package config

import (
	"slices"
	"testing"
)

func TestLookupEnvironment(t *testing.T) {
	t.Run("empty falls back to default production", func(t *testing.T) {
		env, ok := LookupEnvironment("")
		if !ok {
			t.Fatal("empty name should map to DefaultEnvironment")
		}
		if env.MetaServerURL != "https://make.qfei.cn" {
			t.Errorf("default MetaServerURL = %q", env.MetaServerURL)
		}
	})

	t.Run("known environments full preset", func(t *testing.T) {
		cases := map[string]Environment{
			"dev": {
				MetaServerURL:   "https://dev-make.qtech.cn",
				RepoServerURL:   "https://dev-make-repo.qtech.cn",
				AuthServerURL:   "https://dev-myaccount.qtech.cn",
				AgentGatewayURL: "https://dev-make-agent.qtech.cn",
				TraceServerURL:  "https://openobserve.qtech.cn",
			},
			"test": {
				MetaServerURL:   "https://test-make.qtech.cn",
				RepoServerURL:   "https://test-make-repo.qtech.cn",
				AuthServerURL:   "https://test-myaccount.qtech.cn",
				AgentGatewayURL: "https://test-make-agent.qtech.cn",
				TraceServerURL:  "https://openobserve.qtech.cn",
			},
			"production": {
				MetaServerURL:   "https://make.qfei.cn",
				RepoServerURL:   "https://make-repo.qfei.cn",
				AuthServerURL:   "https://myaccount.qfei.cn",
				AgentGatewayURL: "https://make-agent.qfei.cn",
				TraceServerURL:  "https://openobserve.qfei.cn",
			},
		}
		for name, want := range cases {
			got, ok := LookupEnvironment(name)
			if !ok {
				t.Errorf("%s: not found", name)
				continue
			}
			if got != want {
				t.Errorf("%s preset = %+v, want %+v", name, got, want)
			}
		}
	})

	t.Run("unknown environment returns false", func(t *testing.T) {
		if _, ok := LookupEnvironment("staging"); ok {
			t.Error("unknown environment should return ok=false")
		}
	})
}

func TestEnvironmentNames(t *testing.T) {
	names := EnvironmentNames()
	for _, want := range []string{"dev", "test", "production"} {
		if !slices.Contains(names, want) {
			t.Errorf("EnvironmentNames missing %q: %v", want, names)
		}
	}
	if !slices.IsSorted(names) {
		t.Errorf("EnvironmentNames not sorted: %v", names)
	}
	if DefaultEnvironment != "production" {
		t.Errorf("DefaultEnvironment = %q, want production", DefaultEnvironment)
	}
}
