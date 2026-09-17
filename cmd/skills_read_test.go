/**
 * [INPUT]: 依赖 bytes、strings、testing、testing/fstest、github.com/spf13/cobra
 * [OUTPUT]: 覆盖 skills read 的主文件输出 + stderr 指引、子文件原文无指引、目录列举、slash/双参等价、错误透传、参数个数校验
 * [POS]: cmd/skills read 子命令测试，stubSkillContent 注入 fstest.MapFS 隔离真实 embed
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/spf13/cobra"
)

func stubSkillContent(t *testing.T) {
	t.Helper()
	orig := SkillContentFS
	SkillContentFS = fstest.MapFS{
		"makeui/SKILL.md":                 {Data: []byte("---\nname: makeui\n---\n# makeui\n")},
		"makeui/references/principles.md": {Data: []byte("# Principles\n")},
	}
	t.Cleanup(func() { SkillContentFS = orig })
}

// execSkillsRead 以真实 cobra 路径执行 `skills read <args>`，返回 stdout / stderr / err。
func execSkillsRead(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := &cobra.Command{Use: "makecli"}
	root.AddCommand(newSkillsCmd())
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"skills", "read"}, args...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func TestSkillsReadMainFileWithTip(t *testing.T) {
	stubSkillContent(t)
	stdout, stderr, err := execSkillsRead(t, "makeui")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "---\nname: makeui\n---\n# makeui\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "makecli skills read makeui <relative-path>") {
		t.Fatalf("stderr lacks tip: %q", stderr)
	}
}

func TestSkillsReadReferenceRawNoTip(t *testing.T) {
	stubSkillContent(t)
	for _, args := range [][]string{
		{"makeui", "references/principles.md"},
		{"makeui/references/principles.md"},
		{"makeui/references", "principles.md"},
	} {
		stdout, stderr, err := execSkillsRead(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if stdout != "# Principles\n" || stderr != "" {
			t.Fatalf("%v: stdout=%q stderr=%q", args, stdout, stderr)
		}
	}
}

func TestSkillsReadDirectoryLists(t *testing.T) {
	stubSkillContent(t)
	stdout, _, err := execSkillsRead(t, "makeui", "references")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "principles.md\n" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestSkillsReadErrors(t *testing.T) {
	stubSkillContent(t)
	if _, _, err := execSkillsRead(t, "nope"); err == nil || !strings.Contains(err.Error(), "embedded skills: makeui") {
		t.Fatalf("unknown skill err = %v", err)
	}
	if _, _, err := execSkillsRead(t, "makeui", "missing.md"); err == nil || !strings.Contains(err.Error(), "top-level entries") {
		t.Fatalf("not found err = %v", err)
	}
	if _, _, err := execSkillsRead(t); err == nil {
		t.Fatal("expected arg-count error for no args")
	}
	if _, _, err := execSkillsRead(t, "a", "b", "c"); err == nil {
		t.Fatal("expected arg-count error for 3 args")
	}
}
