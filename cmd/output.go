/**
 * [INPUT]: 依赖 bytes、encoding/json、fmt、os、github.com/mattn/go-isatty、github.com/spf13/cobra；依赖 internal/notifier 的 Pending
 * [OUTPUT]: 对外提供 --output 旗标注册（addOutputFlag）、格式解析（resolveOutputFormat：auto 按 stdout TTY 落到 table/json）和 JSON 编码辅助函数；包内 stdoutIsTerminal 可打桩探针、resolvedOutput / resolveInvokedOutput（root PersistentPreRun 落定本次调用的输出格式，供 --debug 选转储形态）、attachNotice 把 _notice 追加到顶层 JSON 对象末尾
 * [POS]: cmd 模块的输出层辅助，--output 三态 auto|table|json 的单一真相源（默认 auto：人在终端看表格，agent/管道/CI 收 JSON），所有 --output json 的唯一 stdout 出口；有待提示更新时在顶层对象末尾追加 _notice.update（对齐 lark-cli：agent 在非 TTY 下也能收到升级提示，stderr 文本提示只给 TTY 上的人看）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/qfeius/makecli/internal/notifier"
	"github.com/spf13/cobra"
)

const (
	outputAuto  = "auto"
	outputTable = "table"
	outputJSON  = "json"
)

const outputFlagUsage = "output format (auto|table|json); auto = table on a terminal, json when piped or run by an agent"

// stdoutIsTerminal 是 auto 的唯一判定信号：stdout 连着终端即人在看，否则是管道/agent/CI 在消费。
// 包级变量供测试打桩。
var stdoutIsTerminal = func() bool { return isatty.IsTerminal(os.Stdout.Fd()) }

// resolvedOutput 是本次调用最终生效的输出格式（table/json），由 root PersistentPreRun 经 resolveInvokedOutput 落定；
// 没注册 --output 的命令按 auto 规则落定。--debug 的转储形态跟随它（client.go debugOption）
var resolvedOutput string

// resolveInvokedOutput 在命令执行前把被调命令的 --output（缺省视为 auto）解析到 resolvedOutput。
// 非法值这里不报错——留给命令自己的 resolveOutputFormat 给出带上下文的错误。
func resolveInvokedOutput(cmd *cobra.Command) {
	output := outputAuto
	if f := cmd.Flags().Lookup("output"); f != nil {
		output = f.Value.String()
	}
	format, err := resolveOutputFormat(output)
	if err != nil {
		format, _ = resolveOutputFormat(outputAuto)
	}
	resolvedOutput = format
}

// addOutputFlag 注册标准 --output 旗标，默认 auto。
func addOutputFlag(cmd *cobra.Command, output *string) {
	cmd.Flags().StringVar(output, "output", outputAuto, outputFlagUsage)
}

// resolveOutputFormat 把 --output 解析为最终的 table 或 json：auto 按 stdout 是否为 TTY 落定，非法值报错。
func resolveOutputFormat(output string) (string, error) {
	switch output {
	case outputAuto:
		if stdoutIsTerminal() {
			return outputTable, nil
		}
		return outputJSON, nil
	case outputTable, outputJSON:
		return output, nil
	default:
		return "", fmt.Errorf("unsupported output format %q, valid options: %s, %s, %s", output, outputAuto, outputTable, outputJSON)
	}
}

// writeJSON 把 v 以 2 空格缩进写到 stdout；有待提示更新时把 _notice 挂到顶层对象上。
func writeJSON(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if u := notifier.Pending(); u != nil {
		notice, err := json.Marshal(map[string]any{"update": u})
		if err != nil {
			return err
		}
		body = attachNotice(body, notice)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, body, "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	_, err = os.Stdout.Write(out.Bytes())
	return err
}

// attachNotice 把 "_notice": notice 追加为顶层 JSON 对象（紧凑编码）的最后一个成员。
// 不解析不重排，调用方的字段顺序原封不动；顶层不是对象（如数组、null）无处可挂，原样返回。
func attachNotice(obj, notice []byte) []byte {
	if len(obj) < 2 || obj[0] != '{' {
		return obj
	}
	var b bytes.Buffer
	b.Write(obj[:len(obj)-1]) // 去掉收尾 }
	if len(obj) > 2 {         // 空对象 {} 不补逗号
		b.WriteByte(',')
	}
	b.WriteString(`"_notice":`)
	b.Write(notice)
	b.WriteByte('}')
	return b.Bytes()
}
