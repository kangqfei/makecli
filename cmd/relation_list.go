/**
 * [INPUT]: 依赖 cmd/client（newClientFromProfile）、internal/api（Client）、fmt、os、github.com/olekukonko/tablewriter、github.com/spf13/cobra、cmd/output 辅助、cmd/app_list（parseFilter）
 * [OUTPUT]: 对外提供 newRelationListCmd 函数
 * [POS]: cmd/relation 的 list 子命令，按 appKey 分页列出 relation（KEY/NAME/FROM/TO/VERSION），位置参数为 relation key 时显示详情；支持 --filter / table|json 输出
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"fmt"
	"os"

	"github.com/olekukonko/tablewriter"
	"github.com/qfeius/makecli/internal/api"
	"github.com/spf13/cobra"
)

func newRelationListCmd() *cobra.Command {
	var page int
	var size int
	var output string
	var filter string

	cmd := &cobra.Command{
		Use:          "list [relation-key]",
		Short:        "List relations in an app, or show a specific relation",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			appKey, _ := cmd.Parent().Flags().GetString("app")
			relationKey := ""
			if len(args) == 1 {
				relationKey = args[0]
			}
			return runRelationList(appKey, relationKey, page, size, output, filter)
		},
	}

	cmd.Flags().IntVar(&page, "page", 1, "page number to fetch (starts from 1)")
	cmd.Flags().IntVar(&size, "size", 20, "number of relations per page")
	addOutputFlag(cmd, &output)
	cmd.Flags().StringVar(&filter, "filter", "", `filter expression, e.g. "name=项目" or "key=project_has_tasks" (comma = OR)`)
	return cmd
}

func runRelationList(appKey, relationKey string, page, size int, output, filterExpr string) error {
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

	if relationKey != "" {
		return showRelation(client, appKey, relationKey, output)
	}
	return listRelations(client, appKey, page, size, output, filter)
}

func listRelations(client *api.Client, appKey string, page, size int, output string, filter string) error {
	relations, total, err := client.ListRelations(appKey, page, size, filter)
	if err != nil {
		return err
	}

	if output == outputJSON {
		return writeJSON(map[string]any{
			"data": relations,
			"pagination": map[string]int{
				"count": len(relations),
				"page":  page,
				"size":  size,
				"total": total,
			},
		})
	}

	if len(relations) == 0 {
		fmt.Printf("No relations found in app '%s'.\n", appKey)
		return nil
	}

	rows := make([][]string, len(relations))
	for i, r := range relations {
		version, _ := r.Meta["version"].(string)
		from := fmt.Sprintf("%s(%s)", r.Properties.From.EntityKey, r.Properties.From.Cardinality)
		to := fmt.Sprintf("%s(%s)", r.Properties.To.EntityKey, r.Properties.To.Cardinality)
		rows[i] = []string{r.Key, r.Name, from, to, version}
	}

	table := tablewriter.NewTable(os.Stdout)
	table.Header("KEY", "NAME", "FROM", "TO", "VERSION")
	_ = table.Bulk(rows)
	_ = table.Render()

	fmt.Printf("\nShowing %d of %d relations\n", len(relations), total)
	return nil
}

func showRelation(client *api.Client, appKey, key, output string) error {
	relation, err := client.GetRelation(appKey, key)
	if err != nil {
		return err
	}

	if output == outputJSON {
		return writeJSON(map[string]any{
			"data": relation,
		})
	}

	version, _ := relation.Meta["version"].(string)
	fmt.Printf("Key:          %s\n", relation.Key)
	fmt.Printf("Name:         %s\n", relation.Name)
	fmt.Printf("App:          %s\n", relation.AppKey)
	fmt.Printf("Version:      %s\n", version)
	fmt.Printf("\nFrom:\n")
	fmt.Printf("  EntityKey:   %s\n", relation.Properties.From.EntityKey)
	fmt.Printf("  Cardinality: %s\n", relation.Properties.From.Cardinality)
	fmt.Printf("\nTo:\n")
	fmt.Printf("  EntityKey:   %s\n", relation.Properties.To.EntityKey)
	fmt.Printf("  Cardinality: %s\n", relation.Properties.To.Cardinality)
	return nil
}
