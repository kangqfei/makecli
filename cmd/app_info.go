/**
 * [INPUT]: 依赖 cmd/client（newClientFromProfile）、cmd/output 辅助、internal/api、errors、fmt、os、
 *          github.com/olekukonko/tablewriter、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newAppInfoCmd 函数
 * [POS]: cmd/app 的 info 子命令，展示单个 App 的元信息（GetApp）+ 双环境部署状态与 URL
 *        （GetDeploymentOverview）；从未部署（ErrNotFound/环境缺失）渲染 Not deployed 占位行
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/olekukonko/tablewriter"
	"github.com/qfeius/makecli/internal/api"
	"github.com/spf13/cobra"
)

func newAppInfoCmd() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:          "info <appKey>",
		Short:        "Show app details and deployment status",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppInfo(args[0], output)
		},
	}

	addOutputFlag(cmd, &output)
	return cmd
}

func runAppInfo(appKey, output string) error {
	output, err := resolveOutputFormat(output)
	if err != nil {
		return err
	}
	if err := validResourceKey(appKey); err != nil {
		return err
	}

	client, err := newClientFromProfile()
	if err != nil {
		return err
	}

	app, err := client.GetApp(appKey)
	if err != nil {
		if errors.Is(err, api.ErrNotFound) {
			return fmt.Errorf("app %q 不存在", appKey)
		}
		return err
	}

	// 从未部署是合法状态（ErrNotFound → overview 为 nil 渲染占位行），其余错误原样失败
	overview, err := client.GetDeploymentOverview(appKey)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return err
	}

	if output == outputJSON {
		return writeJSON(map[string]any{"app": app, "deployment": overview})
	}

	renderAppInfo(app, overview)
	return nil
}

// deploymentRow 把单环境状态折成一行表格；nil = 该环境从未部署，占位而非特判分支
func deploymentRow(env string, d *api.EnvDeployment) []string {
	if d == nil {
		return []string{env, "Not deployed", "-", "-"}
	}
	return []string{env, d.Status, d.CommitSha, d.URL}
}

func renderAppInfo(app *api.App, overview *api.DeploymentOverview) {
	version, _ := app.Meta["version"].(string)
	createdAt, _ := app.Meta["createdAt"].(string)
	pairAppKey, _ := app.Meta["pairAppKey"].(string)
	description, _ := app.Properties["description"].(string)

	fmt.Printf("%-13s %s\n", "Key:", app.Key)
	fmt.Printf("%-13s %s\n", "Name:", app.Name)
	fmt.Printf("%-13s %s\n", "Description:", description)
	fmt.Printf("%-13s %s\n", "Version:", version)
	fmt.Printf("%-13s %s\n", "Created At:", createdAt)
	fmt.Printf("%-13s %s\n", "Pair App Key:", pairAppKey)
	fmt.Println()

	var preview, production *api.EnvDeployment
	if overview != nil {
		preview, production = overview.Preview, overview.Production
	}

	table := tablewriter.NewTable(os.Stdout)
	table.Header("ENVIRONMENT", "STATUS", "COMMIT", "URL")
	_ = table.Bulk([][]string{
		deploymentRow(api.EnvBeta, preview),
		deploymentRow("production", production),
	})
	_ = table.Render()
}
