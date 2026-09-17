/**
 * [INPUT]: 依赖 bytes、strings、testing、testing/fstest
 * [OUTPUT]: 覆盖 Read（主文件缺省/子文件/目录列举/未知 skill/未找到/逃逸拒绝）、Names
 * [POS]: skillcontent 模块测试；fstest.MapFS 隔离内容（真实嵌入 FS 的冒烟在根包 embed_test.go）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package skillcontent

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"makeui/SKILL.md":                 {Data: []byte("---\nname: makeui\n---\n# makeui\n")},
		"makeui/references/principles.md": {Data: []byte("# Principles")},
		"makeui/references/layout.md":     {Data: []byte("# Layout")},
		"makedsl/SKILL.md":                {Data: []byte("# makedsl\n")},
	}
}

func TestNames(t *testing.T) {
	got := Names(testFS())
	if strings.Join(got, ",") != "makedsl,makeui" {
		t.Fatalf("Names = %v, want [makedsl makeui]", got)
	}
}

func TestReadMainFileByDefault(t *testing.T) {
	for _, target := range []string{"makeui", "makeui/", "makeui/SKILL.md"} {
		res, err := Read(testFS(), target)
		if err != nil {
			t.Fatalf("Read(%q): %v", target, err)
		}
		if !res.IsMain() || res.Skill != "makeui" {
			t.Fatalf("Read(%q) = %+v, want main file of makeui", target, res)
		}
		if !bytes.HasPrefix(res.Content, []byte("---\nname: makeui")) {
			t.Fatalf("Read(%q) content = %q", target, res.Content)
		}
	}
}

func TestReadReference(t *testing.T) {
	res, err := Read(testFS(), "makeui/references/principles.md")
	if err != nil {
		t.Fatal(err)
	}
	if res.IsMain() || res.Path != "references/principles.md" || string(res.Content) != "# Principles" {
		t.Fatalf("got %+v", res)
	}
}

func TestReadDirectoryLists(t *testing.T) {
	res, err := Read(testFS(), "makeui/references")
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != nil || strings.Join(res.Entries, ",") != "layout.md,principles.md" {
		t.Fatalf("got %+v", res)
	}
}

func TestReadUnknownSkillListsEmbedded(t *testing.T) {
	for _, target := range []string{"nope", "", ".", "..", "../makeui", "/makeui", "makeui/../etc"} {
		_, err := Read(testFS(), target)
		if err == nil || !strings.Contains(err.Error(), "embedded skills: makedsl, makeui") {
			t.Fatalf("Read(%q) err = %v, want unknown skill listing", target, err)
		}
	}
}

func TestReadNotFoundListsTopLevel(t *testing.T) {
	_, err := Read(testFS(), "makeui/references/nope.md")
	if err == nil || !strings.Contains(err.Error(), "top-level entries: SKILL.md, references/") {
		t.Fatalf("err = %v", err)
	}
}

// 冒烟：embed 通路真的接上了 submodule——makeui 在，主文件带 frontmatter，references 非空。
