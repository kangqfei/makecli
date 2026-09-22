/**
 * [INPUT]: 依赖 cmd/clone 的 runAppSync、cmd/deploy 的 appKeyFromManifest、cmd/app_create 的 appDSLPath/fileExists、go-git、cobra、fmt
 * [OUTPUT]: 提供 newPullCmd 与 runPull，无位置参数，从当前工程清单读取 appKey 后更新当前仓库
 * [POS]: app 命令组的隐藏 pull 入口；clone 创建指定 app 的目录，pull 更新已有工程，共用 runAppSync
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/spf13/cobra"
)

func newPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "pull",
		Short:        "Pull the app's beta repository into the current project",
		Hidden:       true,
		Example:      `  makecli app pull               # 更新当前 app 工程`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPull()
		},
	}
}

func runPull() error {
	if !fileExists(appDSLPath) {
		return fmt.Errorf("%s not found: run `makecli app pull` from the app project root", appDSLPath)
	}
	appKey, err := appKeyFromManifest(appDSLPath)
	if err != nil {
		return err
	}
	if _, err := git.PlainOpen("."); err != nil {
		return fmt.Errorf("current project is not an accessible git repository; use `makecli app clone <appKey>` to clone an app: %w", err)
	}
	return runAppSync(appKey, ".", "Pulled")
}
