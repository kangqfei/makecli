/**
 * [INPUT]: 依赖 config 包内的 Context/LookupContext/ContextNames/DefaultContext（包内白盒），slices、testing
 * [OUTPUT]: 覆盖 context preset 查表与命名的单元测试
 * [POS]: internal/config 模块 context.go 的配套测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package config

import (
	"slices"
	"testing"
)

func TestLookupContext(t *testing.T) {
	t.Run("empty falls back to default production", func(t *testing.T) {
		c, ok := LookupContext("")
		if !ok {
			t.Fatal("empty name should map to DefaultContext")
		}
		if c.MetaServerURL != "https://make.qfei.cn" {
			t.Errorf("default MetaServerURL = %q", c.MetaServerURL)
		}
	})

	t.Run("known contexts full preset", func(t *testing.T) {
		cases := map[string]Context{
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
			got, ok := LookupContext(name)
			if !ok {
				t.Errorf("%s: not found", name)
				continue
			}
			if got != want {
				t.Errorf("%s preset = %+v, want %+v", name, got, want)
			}
		}
	})

	t.Run("unknown context returns false", func(t *testing.T) {
		if _, ok := LookupContext("staging"); ok {
			t.Error("unknown context should return ok=false")
		}
	})
}

func TestContextNames(t *testing.T) {
	// lifecycle 顺序 dev → test → production，且与 preset 表一一对应（不多不少）
	names := ContextNames()
	if want := []string{"dev", "test", "production"}; !slices.Equal(names, want) {
		t.Errorf("ContextNames = %v, want %v", names, want)
	}
	if len(names) != len(contexts) {
		t.Errorf("ContextNames has %d entries, preset table has %d", len(names), len(contexts))
	}
	for _, name := range names {
		if _, ok := contexts[name]; !ok {
			t.Errorf("ContextNames lists %q but preset table lacks it", name)
		}
	}
	if DefaultContext != "production" {
		t.Errorf("DefaultContext = %q, want production", DefaultContext)
	}
}
