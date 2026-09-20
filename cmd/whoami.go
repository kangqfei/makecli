/**
 * [INPUT]: 依赖 internal/api（ErrAuthFailed/UserInfo/GetUserInfo）、internal/config（Load/ValidateProfileName）、cmd/client（newClientFromProfile/envName）、cmd/login（runLogin/defaultLoginTimeout）、cmd/output（resolveOutputFormat/writeJSON）、errors、fmt、os、github.com/olekukonko/tablewriter、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newWhoamiCmd 函数
 * [POS]: cmd 模块的 whoami 顶级命令，展示当前 token 对应的用户身份（表格列序 User ID/Name/Tenant ID/Tenant/Profile/Environment）；未登录/凭证失效时自动触发 login 流程（wrangler whoami 式交互）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/olekukonko/tablewriter"
	"github.com/qfeius/makecli/internal/api"
	"github.com/qfeius/makecli/internal/config"
	"github.com/spf13/cobra"
)

// loginFunc 为包级可打桩变量，单测替换以免真 OAuth 流程（参照 login.go openBrowserFunc 模式）。
var loginFunc = runLogin

func newWhoamiCmd() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:          "whoami",
		Short:        "Show the user identity associated with the current access token",
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runWhoami(output)
		},
	}
	addOutputFlag(cmd, &output)
	return cmd
}

// runWhoami 查询并展示当前用户身份。登录状态收敛为「每次调用至多触发一次登录」：
// 无 token 先登录再查；有 token 直接查，鉴权失败（过期/失效）登录后重试一次，
// 重试仍失败则原样上抛（errors.go 会升级为引导文案）。token 取值链见 resolveAccessToken。
func runWhoami(output string) error {
	output, err := resolveOutputFormat(output)
	if err != nil {
		return err
	}
	if err := config.ValidateProfileName(Profile); err != nil {
		return err
	}

	token, source, err := resolveAccessToken()
	if err != nil {
		return err
	}
	// 登录只修 credentials 文件：token 来自 flag / env 时，login 写下的新 token 会被覆盖值继续遮蔽，
	// 「登录成功却还是失败」是死循环——覆盖态下鉴权失败直接上抛，由 errors.go 回显 token 来源。
	renewable := source == tokenSourceCredentials
	loggedIn := token != ""
	if !loggedIn {
		fmt.Fprintln(os.Stderr, "You are not logged in. Starting the login flow...")
		if err := loginFunc(defaultLoginTimeout, false); err != nil {
			return err
		}
	}

	info, err := fetchUserInfo()
	if loggedIn && renewable && errors.Is(err, api.ErrAuthFailed) {
		fmt.Fprintln(os.Stderr, "Your access token is invalid or expired. Starting the login flow...")
		if err := loginFunc(defaultLoginTimeout, false); err != nil {
			return err
		}
		info, err = fetchUserInfo()
	}
	if err != nil {
		return err
	}

	if output == outputJSON {
		return writeJSON(info)
	}
	renderWhoami(info)
	return nil
}

// fetchUserInfo 每次重建客户端再查询，登录后落盘的新 token 自然生效，无需缓存失效分支。
func fetchUserInfo() (*api.UserInfo, error) {
	client, err := newClientFromProfile()
	if err != nil {
		return nil, err
	}
	return client.GetUserInfo()
}

// renderWhoami 以 tablewriter FIELD/VALUE 表格输出用户身份（对齐 app list 的表格约定）。
// 问候语沿用 wrangler whoami 风格；用户/租户关系失效是异常态，仅 valid=false 时以 stderr 警示。
func renderWhoami(info *api.UserInfo) {
	fmt.Printf("👋 You are logged in as %s.\n\n", info.Name)

	table := tablewriter.NewTable(os.Stdout)
	table.Header("FIELD", "VALUE")
	_ = table.Bulk([][]string{
		{"User ID", info.ID},
		{"Name", info.Name},
		{"Tenant ID", info.Tenant.ID},
		{"Tenant", info.Tenant.TenantName},
		{"Profile", Profile},
		{"Context", contextName()},
	})
	_ = table.Render()

	if !info.Valid {
		fmt.Fprintln(os.Stderr, "\n⚠ 当前用户与租户的关系已失效 (valid=false)，请联系管理员确认租户状态。")
	}
}
