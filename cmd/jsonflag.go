/**
 * [INPUT]: 依赖 bytes、encoding/json、fmt、io、os、strings
 * [OUTPUT]: 对外提供 readJSONFlag / decodeJSONFlag 函数、jsonFlagForms 用法片段、flagStdin 包级可打桩 stdin
 * [POS]: cmd 模块 JSON 型 flag 的单一入口（对齐 lark-cli）：取值三形态 inline JSON | @file | -（stdin，每次调用只能一个 flag 用）；解码禁止未知字段，把 key 拼错挡在本地，取值合法性交服务端裁决。被 record list --sort-json、record aggregate --group-json/--aggregates-json/--sort-json 消费
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// jsonFlagForms 是 JSON 型 flag 帮助文案的公共后缀
const jsonFlagForms = "inline JSON, @file, or - for stdin"

// flagStdin 是 `-` 形态的读取源，包级变量供测试打桩
var flagStdin io.Reader = os.Stdin

// readJSONFlag 按三形态取出 flag 的原始字节：`-` 读 stdin，`@path` 读文件，其余视为内联 JSON
func readJSONFlag(value string) ([]byte, error) {
	switch {
	case value == "-":
		return io.ReadAll(flagStdin)
	case strings.HasPrefix(value, "@"):
		return os.ReadFile(strings.TrimPrefix(value, "@"))
	default:
		return []byte(value), nil
	}
}

// decodeJSONFlag 读取并严格解码 JSON 型 flag 到 dst：未知字段即报错，错误信息带 --name 定位
func decodeJSONFlag(name, value string, dst any) error {
	raw, err := readJSONFlag(value)
	if err != nil {
		return fmt.Errorf("--%s: %w", name, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("--%s: invalid JSON: %w", name, err)
	}
	return nil
}
