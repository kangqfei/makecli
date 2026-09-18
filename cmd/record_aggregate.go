/**
 * [INPUT]: 依赖 cmd/client（newClientFromProfile）、cmd/jsonflag（decodeJSONFlag）、cmd/record_list（checkPagination/upperHeaders）、internal/api（AggregateOpts/GroupField/AggregateField/SortField）、fmt、os、strconv、github.com/olekukonko/tablewriter、github.com/spf13/cobra、cmd/output 辅助
 * [OUTPUT]: 对外提供 newRecordAggregateCmd 函数
 * [POS]: cmd/record 的 aggregate 子命令，对单个 Entity 做服务端聚合统计（/data/v1/aggregate，声明式 GROUP BY）。flag 与 API 字段机械映射：JSON 型 --group-json / --aggregates-json / --sort-json 直透（结构本地严格解码，取值服务端裁决），CEL 文本 --filter（聚合前 WHERE）/ --aggregate-filter（聚合后 HAVING）直透；表格列序取自请求声明（group 后 aggregates），维度列显示 label、指标列显示裸数值
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/olekukonko/tablewriter"
	"github.com/qfeius/makecli/internal/api"
	"github.com/spf13/cobra"
)

// recordAggregateOpts 承载 aggregate 子命令的全部 flag 取值
type recordAggregateOpts struct {
	page            int
	size            int
	output          string
	groupJSON       string
	aggregatesJSON  string
	sortJSON        string
	filter          string
	aggregateFilter string
}

func newRecordAggregateCmd() *cobra.Command {
	var o recordAggregateOpts

	cmd := &cobra.Command{
		Use:   "aggregate",
		Short: "Aggregate records in an entity (server-side GROUP BY)",
		Long: `Aggregate records of an entity server-side. --group-json declares the dimensions
and --aggregates-json the metrics; together they decide the output columns. Omit
--group-json for a single global row.

--filter narrows raw records before aggregation (WHERE); --aggregate-filter narrows
groups after it (HAVING) and may only reference metric aliases. Both are CEL
expressions evaluated server-side, same dialect as 'record list --filter'.

JSON flags accept inline JSON, @file, or - for stdin (stdin at most once per call).
Element shapes (values are validated server-side):
  group:      {"fieldKey": "...", "granularity": "day|week|month|quarter|year", "alias": "..."}
  aggregates: {"fieldKey": "...", "aggregate": "count|countDistinct|sum|avg|min|max", "alias": "..."}
  sort:       {"alias": "..."} or {"fieldKey": "..."}, plus "order": "asc|desc"`,
		Example: `  # orders by status and month: count + total amount, keep groups over 10000, largest first
  makecli record aggregate --app crm --entity order \
    --group-json '[{"fieldKey":"status"},{"fieldKey":"orderDate","granularity":"month","alias":"month"}]' \
    --aggregates-json '[{"aggregate":"count","alias":"orderCount"},{"fieldKey":"amount","aggregate":"sum","alias":"totalAmount"}]' \
    --filter "status != 'draft'" \
    --aggregate-filter "totalAmount > 10000" \
    --sort-json '[{"alias":"totalAmount","order":"desc"}]'

  # single global row, metrics loaded from a file
  makecli record aggregate --app crm --entity order --aggregates-json @aggregates.json`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			appKey, _ := cmd.Parent().Flags().GetString("app")
			entityKey, _ := cmd.Parent().Flags().GetString("entity")
			return runRecordAggregate(appKey, entityKey, o)
		},
	}

	cmd.Flags().IntVar(&o.page, "page", 1, "page number of group rows (starts from 1)")
	cmd.Flags().IntVar(&o.size, "size", 10, "group rows per page")
	addOutputFlag(cmd, &o.output)
	cmd.Flags().StringVar(&o.groupJSON, "group-json", "", "dimensions as a JSON array; omit for a global aggregate ("+jsonFlagForms+")")
	cmd.Flags().StringVar(&o.aggregatesJSON, "aggregates-json", "", "metrics as a JSON array, required ("+jsonFlagForms+")")
	_ = cmd.MarkFlagRequired("aggregates-json")
	cmd.Flags().StringVar(&o.filter, "filter", "", "CEL filter on raw records before aggregation (WHERE)")
	cmd.Flags().StringVar(&o.aggregateFilter, "aggregate-filter", "", "CEL filter on aggregated groups (HAVING), metric aliases only")
	cmd.Flags().StringVar(&o.sortJSON, "sort-json", "", "sort keys as a JSON array referencing declared aliases or group fieldKeys ("+jsonFlagForms+")")
	return cmd
}

func runRecordAggregate(appKey, entityKey string, o recordAggregateOpts) error {
	output, err := resolveOutputFormat(o.output)
	if err != nil {
		return err
	}
	if err := checkPagination(o.page, o.size); err != nil {
		return err
	}

	opts := api.AggregateOpts{Filter: o.filter, AggregateFilter: o.aggregateFilter, Page: o.page, Size: o.size}
	if o.groupJSON != "" {
		if err := decodeJSONFlag("group-json", o.groupJSON, &opts.Group); err != nil {
			return err
		}
	}
	if err := decodeJSONFlag("aggregates-json", o.aggregatesJSON, &opts.Aggregates); err != nil {
		return err
	}
	if o.sortJSON != "" {
		if err := decodeJSONFlag("sort-json", o.sortJSON, &opts.Sort); err != nil {
			return err
		}
	}

	client, err := newClientFromProfile()
	if err != nil {
		return err
	}
	rows, total, err := client.AggregateRecords(appKey, entityKey, opts)
	if err != nil {
		return err
	}

	if output == outputJSON {
		return writeJSON(map[string]any{
			"data": rows,
			"pagination": map[string]int{
				"count": len(rows),
				"page":  o.page,
				"size":  o.size,
				"total": total,
			},
		})
	}

	if len(rows) == 0 {
		fmt.Printf("No groups found in entity '%s'.\n", entityKey)
		return nil
	}

	// 列序来自请求声明：group 在前、aggregates 在后，不从响应首行猜
	headers := aggregateColumns(opts)
	cells := make([][]string, len(rows))
	for i, row := range rows {
		cells[i] = make([]string, len(headers))
		for j, h := range headers {
			cells[i][j] = formatAggregateCell(row[h])
		}
	}

	table := tablewriter.NewTable(os.Stdout)
	table.Header(upperHeaders(headers)...)
	_ = table.Bulk(cells)
	_ = table.Render()

	fmt.Printf("\nShowing %d of %d groups\n", len(rows), total)
	return nil
}

// aggregateColumns 按请求声明推导输出列名：分组维度取 alias 缺省 fieldKey，指标取 alias
func aggregateColumns(opts api.AggregateOpts) []string {
	columns := make([]string, 0, len(opts.Group)+len(opts.Aggregates))
	for _, g := range opts.Group {
		if g.Alias != "" {
			columns = append(columns, g.Alias)
			continue
		}
		columns = append(columns, g.FieldKey)
	}
	for _, a := range opts.Aggregates {
		columns = append(columns, a.Alias)
	}
	return columns
}

// formatAggregateCell 渲染一个单元格：维度列 {value,label} 取 label，指标列的 JSON number 按原值输出（不走 %v 的科学计数法）
func formatAggregateCell(v any) string {
	switch v := v.(type) {
	case map[string]any:
		return fmt.Sprintf("%v", v["label"])
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}
