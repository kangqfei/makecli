/**
 * [INPUT]: 依赖 bytes、testing、internal/skillcontent 的 Names/Read
 * [OUTPUT]: 真实嵌入 FS 的冒烟测试
 * [POS]: 根包 embed.go 的配套测试，钉住 embed 通路真的接上了 skills/ submodule（makeui 存在、带 frontmatter、references 非空）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package main

import (
	"bytes"
	"testing"

	"github.com/qfeius/makecli/internal/skillcontent"
)

func TestPlatformSkillsFS(t *testing.T) {
	fsys := platformSkillsFS()
	if len(skillcontent.Names(fsys)) == 0 {
		t.Fatal("embedded FS has no skills; run `make sync`")
	}
	res, err := skillcontent.Read(fsys, "makeui")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(res.Content, []byte("---\n")) && !bytes.HasPrefix(res.Content, []byte("---\r\n")) {
		t.Fatalf("makeui SKILL.md lacks frontmatter: %q", res.Content[:min(len(res.Content), 40)])
	}
	refs, err := skillcontent.Read(fsys, "makeui/references")
	if err != nil || len(refs.Entries) == 0 {
		t.Fatalf("makeui/references: entries=%v err=%v", refs.Entries, err)
	}
}
