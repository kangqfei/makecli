/**
 * [INPUT]: 依赖 cmd/client（newClientFromProfile）、internal/api（Client/UniqueConstraint）、fmt、os、strings、github.com/olekukonko/tablewriter、github.com/spf13/cobra、cmd/output 辅助、cmd/app_list（parseFilter）
 * [OUTPUT]: 对外提供 newEntityListCmd 函数
 * [POS]: cmd/entity 的 list 子命令，按 appKey 分页列出 entity（KEY/NAME/VERSION），位置参数为 entity key 时显示单个 entity 详情（Fields 表 + Unique constraints 表）；支持 --filter / table|json
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/qfeius/makecli/internal/api"
	"github.com/spf13/cobra"
)

func newEntityListCmd() *cobra.Command {
	var page int
	var size int
	var output string
	var filter string

	cmd := &cobra.Command{
		Use:          "list [entity-key]",
		Short:        "List entities in an app, or show a specific entity",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			appKey, _ := cmd.Parent().Flags().GetString("app")
			entityKey := ""
			if len(args) == 1 {
				entityKey = args[0]
			}
			return runEntityList(appKey, entityKey, page, size, output, filter)
		},
	}

	cmd.Flags().IntVar(&page, "page", 1, "page number to fetch (starts from 1)")
	cmd.Flags().IntVar(&size, "size", 20, "number of entities per page")
	addOutputFlag(cmd, &output)
	cmd.Flags().StringVar(&filter, "filter", "", `filter expression, e.g. "name=任务" or "key=task" (comma = OR)`)
	return cmd
}

func runEntityList(appKey, entityKey string, page, size int, output, filterExpr string) error {
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

	if entityKey != "" {
		return showEntity(client, appKey, entityKey, output)
	}
	return listEntities(client, appKey, page, size, output, filter)
}

func listEntities(client *api.Client, appKey string, page, size int, output string, filter string) error {
	entities, total, err := client.ListEntities(appKey, page, size, filter)
	if err != nil {
		return err
	}

	if output == outputJSON {
		return writeJSON(map[string]any{
			"data": entities,
			"pagination": map[string]int{
				"count": len(entities),
				"page":  page,
				"size":  size,
				"total": total,
			},
		})
	}

	if len(entities) == 0 {
		fmt.Printf("No entities found in app '%s'.\n", appKey)
		return nil
	}

	rows := make([][]string, len(entities))
	for i, e := range entities {
		version, _ := e.Meta["version"].(string)
		rows[i] = []string{e.Key, e.Name, version}
	}

	table := tablewriter.NewTable(os.Stdout)
	table.Header("KEY", "NAME", "VERSION")
	_ = table.Bulk(rows)
	_ = table.Render()

	fmt.Printf("\nShowing %d of %d entities\n", len(entities), total)
	return nil
}

func showEntity(client *api.Client, appKey, key, output string) error {
	entity, err := client.GetEntity(appKey, key)
	if err != nil {
		return err
	}

	if output == outputJSON {
		return writeJSON(map[string]any{
			"data": entity,
		})
	}

	version, _ := entity.Meta["version"].(string)
	fmt.Printf("Key:     %s\n", entity.Key)
	fmt.Printf("Name:    %s\n", entity.Name)
	fmt.Printf("App:     %s\n", entity.AppKey)
	fmt.Printf("Version: %s\n", version)

	if len(entity.Properties.Fields) == 0 {
		fmt.Println("\nNo fields.")
	} else {
		fmt.Println("\nFields:")
		rows := make([][]string, len(entity.Properties.Fields))
		for i, f := range entity.Properties.Fields {
			rows[i] = []string{f.Key, f.Name, f.Type}
		}
		table := tablewriter.NewTable(os.Stdout)
		table.Header("KEY", "NAME", "TYPE")
		_ = table.Bulk(rows)
		_ = table.Render()
	}

	renderUniqueConstraints(entity.Properties.UniqueConstraints)
	return nil
}

// renderUniqueConstraints 展示 Entity 的唯一性约束表（NAME / FIELDS），无约束则静默
func renderUniqueConstraints(constraints []api.UniqueConstraint) {
	if len(constraints) == 0 {
		return
	}
	fmt.Println("\nUnique constraints:")
	rows := make([][]string, len(constraints))
	for i, c := range constraints {
		rows[i] = []string{c.Name, strings.Join(c.Fields, ", ")}
	}
	table := tablewriter.NewTable(os.Stdout)
	table.Header("NAME", "FIELDS")
	_ = table.Bulk(rows)
	_ = table.Render()
}
