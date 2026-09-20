/**
 * [INPUT]: 依赖 role.go 的 RoleSkills / SyncCommand、internal/config 角色常量；slices、testing
 * [OUTPUT]: 覆盖角色名单表与同步命令派生的单元测试
 * [POS]: internal/skillsync 模块 role.go 的配套测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package skillsync

import (
	"slices"
	"testing"

	"github.com/qfeius/makecli/internal/config"
)

func TestRoleSkills(t *testing.T) {
	cases := []struct {
		role      string
		wantNames []string
		wantAll   bool
	}{
		{config.RoleUser, []string{"makecli"}, false},
		{"", nil, true},        // 未设置 → 全量（原有行为）
		{"unknown", nil, true}, // 名单表不校验，交 cmd 层键表
	}
	for _, tc := range cases {
		names, all := RoleSkills(tc.role)
		if !slices.Equal(names, tc.wantNames) || all != tc.wantAll {
			t.Errorf("RoleSkills(%q) = (%v, %v), want (%v, %v)", tc.role, names, all, tc.wantNames, tc.wantAll)
		}
	}
	// 每个合法角色都必须在表里有显式一行（漏行会静默退成全量）
	for _, role := range config.RoleNames() {
		if _, ok := roleSkills[role]; !ok {
			t.Errorf("roleSkills missing an explicit entry for %q", role)
		}
	}
}

func TestSyncCommandByRole(t *testing.T) {
	if got := SyncCommand(""); !slices.Equal(got, SkillsCommand()) {
		t.Errorf("unset role sync = %v, want SkillsCommand %v", got, SkillsCommand())
	}
	want := []string{"npx", "-y", "skills", "add", SkillsSource, "-s", "makecli", "-a", "*", "-y", "--global"}
	if got := SyncCommand(config.RoleUser); !slices.Equal(got, want) {
		t.Errorf("user sync = %v, want %v", got, want)
	}
}
