/**
 * [INPUT]: 依赖 cmd/client（newClientFromProfile）、cmd/jsonflag（decodeJSONFlag）、internal/api（ListRecordOpts/SortField）、fmt、os、sort、strings、github.com/olekukonko/tablewriter、github.com/spf13/cobra、cmd/output 辅助
 * [OUTPUT]: 对外提供 newRecordListCmd 函数，包内 checkPagination / upperHeaders / extractKeys 供 record aggregate 复用
 * [POS]: cmd/record 的 list 子命令，按 appKey + entityKey 分页查询 Record，支持 fields（fieldKey 列表）/sort-json（JSON 数组直透，元素 {fieldKey,order}）/filter（raw CEL 直透，服务端裁决）/table|json 输出
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/qfeius/makecli/internal/api"
	"github.com/spf13/cobra"
)

func newRecordListCmd() *cobra.Command {
	var page int
	var size int
	var output string
	var fields string
	var sortJSON string
	var filter string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List records in an entity",
		Long: `List records in an entity. Use --filter to narrow results with a CEL
expression evaluated server-side (a subset of https://cel.dev).`,
		Example: `  # numeric + set membership
  makecli record list --app crm --entity order --filter "amount >= 100 && status in ['todo','doing']"

  # text contains + null check
  makecli record list --app crm --entity order --filter "title.contains('升级') && owner != null"

  # records I own (Make system variable)
  makecli record list --app crm --entity order --filter "owner == _currentUser"

  # newest first
  makecli record list --app crm --entity order --sort-json '[{"fieldKey":"createdAt","order":"desc"}]'`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			appKey, _ := cmd.Parent().Flags().GetString("app")
			entityKey, _ := cmd.Parent().Flags().GetString("entity")
			return runRecordList(appKey, entityKey, page, size, output, fields, sortJSON, filter)
		},
	}

	cmd.Flags().IntVar(&page, "page", 1, "page number (starts from 1)")
	cmd.Flags().IntVar(&size, "size", 20, "records per page")
	addOutputFlag(cmd, &output)
	cmd.Flags().StringVar(&fields, "fields", "", "comma-separated field keys to display")
	cmd.Flags().StringVar(&sortJSON, "sort-json", "", `sort keys as a JSON array of {"fieldKey","order"} (`+jsonFlagForms+`)`)
	cmd.Flags().StringVar(&filter, "filter", "", "CEL filter expression evaluated server-side (see EXAMPLES)")
	return cmd
}

func runRecordList(appKey, entityKey string, page, size int, output, fields, sortJSON, filter string) error {
	output, err := resolveOutputFormat(output)
	if err != nil {
		return err
	}
	if err := checkPagination(page, size); err != nil {
		return err
	}

	client, err := newClientFromProfile()
	if err != nil {
		return err
	}

	opts := api.ListRecordOpts{Page: page, Size: size, Filter: filter}
	if fields != "" {
		opts.Fields = strings.Split(fields, ",")
	}
	if sortJSON != "" {
		if err := decodeJSONFlag("sort-json", sortJSON, &opts.Sort); err != nil {
			return err
		}
	}

	records, total, err := client.ListRecords(appKey, entityKey, opts)
	if err != nil {
		return err
	}

	if output == outputJSON {
		return writeJSON(map[string]any{
			"data": records,
			"pagination": map[string]int{
				"count": len(records),
				"page":  page,
				"size":  size,
				"total": total,
			},
		})
	}

	if len(records) == 0 {
		fmt.Printf("No records found in entity '%s'.\n", entityKey)
		return nil
	}

	// 自动从首条记录提取列名（或使用 --fields 指定的列）
	var headers []string
	if len(opts.Fields) > 0 {
		headers = opts.Fields
	} else {
		headers = extractKeys(records[0])
	}

	rows := make([][]string, len(records))
	for i, rec := range records {
		row := make([]string, len(headers))
		for j, h := range headers {
			row[j] = fmt.Sprintf("%v", rec[h])
		}
		rows[i] = row
	}

	table := tablewriter.NewTable(os.Stdout)
	table.Header(upperHeaders(headers)...)
	_ = table.Bulk(rows)
	_ = table.Render()

	fmt.Printf("\nShowing %d of %d records\n", len(records), total)
	return nil
}

// checkPagination 校验分页参数下界（page/size 均从 1 起）
func checkPagination(page, size int) error {
	if page < 1 {
		return fmt.Errorf("page must be greater than or equal to 1")
	}
	if size < 1 {
		return fmt.Errorf("size must be greater than or equal to 1")
	}
	return nil
}

// upperHeaders 把列名大写后转成 tablewriter 表头参数
func upperHeaders(keys []string) []any {
	headers := make([]any, len(keys))
	for i, k := range keys {
		headers[i] = strings.ToUpper(k)
	}
	return headers
}

// extractKeys 从 map 中提取排序后的 key 列表
func extractKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
