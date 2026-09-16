/**
 * [INPUT]: 依赖 cmd/client（newClientFromProfile）、fmt、sort、github.com/spf13/cobra、cmd/output 辅助
 * [OUTPUT]: 对外提供 newRecordGetCmd 函数
 * [POS]: cmd/record 的 get 子命令，获取单条 Record 并以 table 或 json 格式展示
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

func newRecordGetCmd() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:          "get <record-id>",
		Short:        "Get a record by ID",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			appKey, _ := cmd.Parent().Flags().GetString("app")
			entityKey, _ := cmd.Parent().Flags().GetString("entity")
			return runRecordGet(appKey, entityKey, args[0], output)
		},
	}

	addOutputFlag(cmd, &output)
	return cmd
}

func runRecordGet(appKey, entityKey, recordID, output string) error {
	output, err := resolveOutputFormat(output)
	if err != nil {
		return err
	}

	client, err := newClientFromProfile()
	if err != nil {
		return err
	}

	data, err := client.GetRecord(appKey, entityKey, recordID)
	if err != nil {
		return err
	}

	if output == outputJSON {
		return writeJSON(map[string]any{"data": data})
	}

	// table 模式: 按 key 排序，逐行输出 key-value
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%-20s %v\n", k, data[k])
	}
	return nil
}
