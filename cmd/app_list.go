/**
 * [INPUT]: 依赖 cmd/client（newClientFromProfile）、fmt、os、strings、github.com/olekukonko/tablewriter、github.com/spf13/cobra、cmd/output 辅助
 * [OUTPUT]: 对外提供 newAppListCmd 函数、parseFilter 解析 --filter 语法
 * [POS]: cmd/app 的 list 子命令，分页列出 org 下全部 App，输出列 KEY/NAME/DESCRIPTION/VERSION/CREATED AT（description 取自 Properties）；支持 --filter / table|json 输出；filter 支持 name(contains 模糊) / key(等值) / description
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

func newAppListCmd() *cobra.Command {
	var page int
	var size int
	var output string
	var filter string

	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List all apps",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppList(page, size, output, filter)
		},
	}

	cmd.Flags().IntVar(&page, "page", 1, "page number to fetch (starts from 1)")
	cmd.Flags().IntVar(&size, "size", 20, "number of apps per page")
	addOutputFlag(cmd, &output)
	cmd.Flags().StringVar(&filter, "filter", "", `filter expression, e.g. "name=待办,key=todo" (comma = OR; key 等值匹配, name/description 模糊匹配)`)
	return cmd
}

// parseFilter 把 "key=value,key2=value2" 过滤语法翻译为 CEL 表达式文本
// 逗号分隔的每组 key=value 之间是 OR（||）关系
// 不同字段使用不同的匹配语义：
//   - key:               等值匹配  key == 'value'
//   - name, description: 模糊匹配  name.contains('value')
//
// 返回空串表示无筛选；服务端将其包成 Expression{expression} 解析。
func parseFilter(expr string) (string, error) {
	if expr == "" {
		return "", nil
	}
	var terms []string
	for _, part := range strings.Split(expr, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 || kv[0] == "" || kv[1] == "" {
			return "", fmt.Errorf("invalid filter expression %q, expected key=value", part)
		}
		field, val := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
		switch field {
		case "key":
			terms = append(terms, fmt.Sprintf("%s == %s", field, celString(val)))
		case "name", "description":
			terms = append(terms, fmt.Sprintf("%s.contains(%s)", field, celString(val)))
		default:
			return "", fmt.Errorf("unsupported filter field %q", field)
		}
	}
	return strings.Join(terms, " || "), nil
}

// celString 将任意字符串转义为 CEL 单引号字符串字面量
// 仅需处理反斜杠与单引号；先转义反斜杠以免二次转义已注入的转义符
func celString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

func runAppList(page, size int, output, filterExpr string) error {
	output, err := resolveOutputFormat(output)
	if err != nil {
		return err
	}
	if page < 1 {
		return fmt.Errorf("page must be greater than or equal to 1")
	}
	if size < 1 {
		return fmt.Errorf("size must be greater than or equal to 1")
	}

	filter, err := parseFilter(filterExpr)
	if err != nil {
		return err
	}

	client, err := newClientFromProfile()
	if err != nil {
		return err
	}

	apps, total, err := client.ListApps(page, size, filter)
	if err != nil {
		return err
	}

	if output == outputJSON {
		return writeJSON(map[string]any{
			"data": apps,
			"pagination": map[string]int{
				"count": len(apps),
				"page":  page,
				"size":  size,
				"total": total,
			},
		})
	}

	if len(apps) == 0 {
		fmt.Println("No apps found.")
		return nil
	}

	rows := make([][]string, len(apps))
	for i, app := range apps {
		version, _ := app.Meta["version"].(string)
		createdAt, _ := app.Meta["createdAt"].(string)
		description, _ := app.Properties["description"].(string)
		rows[i] = []string{app.Key, app.Name, description, version, createdAt}
	}

	table := tablewriter.NewTable(os.Stdout)
	table.Header("KEY", "NAME", "DESCRIPTION", "VERSION", "CREATED AT")
	_ = table.Bulk(rows)
	_ = table.Render()

	fmt.Printf("\nShowing %d of %d apps\n", len(apps), total)
	return nil
}
