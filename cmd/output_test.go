/**
 * [INPUT]: 依赖 cmd 包内 resolveOutputFormat / stdoutIsTerminal / addOutputFlag / writeJSON / attachNotice（白盒）、stdout_test 的 captureStdout；internal/notifier 的 SetPendingForTest / Update；github.com/spf13/cobra
 * [OUTPUT]: 覆盖 resolveOutputFormat 的 auto 按 TTY 落定 table/json、显式值透传、非法值拒绝，addOutputFlag 默认 auto；writeJSON 无提示时输出逐字节不变、有提示时 _notice.update 追加为末尾成员且原字段顺序不动、attachNotice 对对象/空对象/数组/null 的处理
 * [POS]: cmd 模块 output.go 的配套测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qfeius/makecli/internal/notifier"
	"github.com/spf13/cobra"
)

func stubStdoutTerminal(t *testing.T, isTTY bool) {
	t.Helper()
	old := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return isTTY }
	t.Cleanup(func() { stdoutIsTerminal = old })
}

func TestResolveOutputFormat(t *testing.T) {
	cases := []struct {
		name  string
		input string
		tty   bool
		want  string
	}{
		{"auto on terminal renders table", outputAuto, true, outputTable},
		{"auto when piped renders json", outputAuto, false, outputJSON},
		{"explicit table ignores tty", outputTable, false, outputTable},
		{"explicit json ignores tty", outputJSON, true, outputJSON},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubStdoutTerminal(t, tc.tty)
			got, err := resolveOutputFormat(tc.input)
			if err != nil {
				t.Fatalf("resolveOutputFormat(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("resolveOutputFormat(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}

	t.Run("rejects unknown format", func(t *testing.T) {
		_, err := resolveOutputFormat("yaml")
		if err == nil || !strings.Contains(err.Error(), "unsupported output format") {
			t.Fatalf("expected unsupported format error, got %v", err)
		}
	})
}

func TestAddOutputFlagDefaultsToAuto(t *testing.T) {
	var output string
	cmd := &cobra.Command{Use: "x"}
	addOutputFlag(cmd, &output)
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if output != outputAuto {
		t.Fatalf("default --output = %q, want %q", output, outputAuto)
	}
}

func setPendingUpdate(t *testing.T, u *notifier.Update) {
	t.Helper()
	old := notifier.SetPendingForTest(u)
	t.Cleanup(func() { notifier.SetPendingForTest(old) })
}

func TestWriteJSONWithoutNotice(t *testing.T) {
	setPendingUpdate(t, nil)
	out := captureStdout(t, func() {
		if err := writeJSON(map[string]any{"b": 1, "a": "x"}); err != nil {
			t.Errorf("writeJSON: %v", err)
		}
	})
	want := "{\n  \"a\": \"x\",\n  \"b\": 1\n}\n"
	if out != want {
		t.Fatalf("writeJSON output = %q, want %q", out, want)
	}
}

func TestWriteJSONAppendsNotice(t *testing.T) {
	setPendingUpdate(t, &notifier.Update{
		Current: "1.0.0", Latest: "2.0.0", URL: "https://example.com/r",
		Command: "makecli update", Message: "makecli 2.0.0 available, current 1.0.0, run: makecli update",
	})
	type view struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	out := captureStdout(t, func() {
		if err := writeJSON(view{Name: "x", Count: 2}); err != nil {
			t.Errorf("writeJSON: %v", err)
		}
	})

	var got struct {
		Name   string `json:"name"`
		Count  int    `json:"count"`
		Notice struct {
			Update notifier.Update `json:"update"`
		} `json:"_notice"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if got.Name != "x" || got.Count != 2 {
		t.Errorf("original fields altered: %+v", got)
	}
	if got.Notice.Update.Latest != "2.0.0" || got.Notice.Update.Command != "makecli update" {
		t.Errorf("_notice.update = %+v", got.Notice.Update)
	}
	// 结构体字段顺序原封不动，_notice 追加在末尾
	if strings.Index(out, `"name"`) > strings.Index(out, `"count"`) || strings.Index(out, `"count"`) > strings.Index(out, `"_notice"`) {
		t.Errorf("field order changed:\n%s", out)
	}
}

func TestAttachNotice(t *testing.T) {
	notice := []byte(`{"update":{"latest":"2.0.0"}}`)
	cases := []struct {
		name, in, want string
	}{
		{"object", `{"a":1}`, `{"a":1,"_notice":{"update":{"latest":"2.0.0"}}}`},
		{"empty object", `{}`, `{"_notice":{"update":{"latest":"2.0.0"}}}`},
		{"array untouched", `[1,2]`, `[1,2]`},
		{"null untouched", `null`, `null`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := attachNotice([]byte(c.in), notice)
			if string(got) != c.want {
				t.Errorf("attachNotice(%s) = %s, want %s", c.in, got, c.want)
			}
			if !json.Valid(got) {
				t.Errorf("attachNotice(%s) produced invalid JSON: %s", c.in, got)
			}
		})
	}
}

func TestResolveInvokedOutput(t *testing.T) {
	orig := stdoutIsTerminal
	t.Cleanup(func() { stdoutIsTerminal = orig })

	t.Run("显式 --output json", func(t *testing.T) {
		stdoutIsTerminal = func() bool { return true }
		cmd := &cobra.Command{}
		var out string
		addOutputFlag(cmd, &out)
		_ = cmd.Flags().Set("output", outputJSON)
		resolveInvokedOutput(cmd)
		if resolvedOutput != outputJSON {
			t.Errorf("resolvedOutput = %q, want json", resolvedOutput)
		}
	})
	t.Run("无 --output 旗标按 auto：管道落 json", func(t *testing.T) {
		stdoutIsTerminal = func() bool { return false }
		resolveInvokedOutput(&cobra.Command{})
		if resolvedOutput != outputJSON {
			t.Errorf("resolvedOutput = %q, want json", resolvedOutput)
		}
	})
	t.Run("非法值退回 auto 而不炸", func(t *testing.T) {
		stdoutIsTerminal = func() bool { return true }
		cmd := &cobra.Command{}
		var out string
		addOutputFlag(cmd, &out)
		_ = cmd.Flags().Set("output", "yaml")
		resolveInvokedOutput(cmd)
		if resolvedOutput != outputTable {
			t.Errorf("resolvedOutput = %q, want table", resolvedOutput)
		}
	})
}
