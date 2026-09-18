/**
 * [INPUT]: 依赖 github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newRecordCmd 函数
 * [POS]: cmd 模块的 record 命令组，挂载 create / get / update / delete / list / aggregate 子命令，--app（appKey）和 --entity（entityKey）参数为子命令继承
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import "github.com/spf13/cobra"

func newRecordCmd() *cobra.Command {
	var appKey string
	var entityKey string

	cmd := &cobra.Command{
		Use:   "record",
		Short: "Manage records in an entity",
		Example: `  # create from a flat field map, then read it back
  makecli record create --app crm --entity order --json order.json
  makecli record get rec_001 --app crm --entity order

  # list with a server-side CEL filter, newest first
  makecli record list --app crm --entity order --filter "status in ['todo','doing']" \
    --sort-json '[{"fieldKey":"createdAt","order":"desc"}]'

  # aggregate: order count and total amount per status (server-side GROUP BY)
  makecli record aggregate --app crm --entity order --group-json '[{"fieldKey":"status"}]' \
    --aggregates-json '[{"aggregate":"count","alias":"orders"},{"fieldKey":"amount","aggregate":"sum","alias":"total"}]'

  # update several records with the same values, then delete them
  makecli record update rec_001 rec_002 --app crm --entity order --json patch.json
  makecli record delete rec_001 rec_002 --app crm --entity order`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if appKey == "" || entityKey == "" {
				return cmd.Usage()
			}
			return nil
		},
	}

	cmd.PersistentFlags().StringVar(&appKey, "app", "", "app key (required)")
	_ = cmd.MarkPersistentFlagRequired("app")
	cmd.PersistentFlags().StringVar(&entityKey, "entity", "", "entity key (required)")
	_ = cmd.MarkPersistentFlagRequired("entity")

	cmd.AddCommand(newRecordCreateCmd())
	cmd.AddCommand(newRecordGetCmd())
	cmd.AddCommand(newRecordUpdateCmd())
	cmd.AddCommand(newRecordDeleteCmd())
	cmd.AddCommand(newRecordListCmd())
	cmd.AddCommand(newRecordAggregateCmd())
	return cmd
}
