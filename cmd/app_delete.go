/**
 * [INPUT]: 依赖 cmd/client（newClientFromProfile）、cmd/app（loadAppManifestFromFile）、internal/api（EnvBeta/EnvProduction、RoleForEnv、WithAppRole、DeleteApp）、errors、fmt、os、strings、charm.land/huh/v2（交互确认表单）、github.com/mattn/go-isatty（TTY 检测）、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newAppDeleteCmd 函数；包内 resolveDeleteEnvs（--env → 有序环境序列）、envAll 常量；包级 confirmDeleteFunc 可打桩变量（测试替换，参照 deploy.go gitPushFunc 模式）
 * [POS]: cmd/app 的 delete 子命令。每个 App 是 prod/beta 一对（MetaAPIDesign.md：meta.appRole 标角色，prod 有 beta 配对时服务端 409 拒删）。
 *        --env 必填（大小写不敏感）：production / beta 各删一半，all 先 beta 后 production；每一步用 WithAppRole(RoleForEnv(env)) 的 client 发「prod key + ?appRole=」交服务端定位目标，
 *        CLI 不反查 pairAppKey、不触 GetApp——配对关系是服务端知识，删除路径零读请求。
 *        用户面只见「app key + 环境」：确认表单敲的是用户给的 app key（标题注明环境），成功提示逐环境一行，--yes 跳过；支持 -f 文件模式
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"charm.land/huh/v2"
	"github.com/mattn/go-isatty"
	"github.com/qfeius/makecli/internal/api"
	"github.com/spf13/cobra"
)

// envAll 是 --env 的第三个取值：一次删掉整对（先 beta 后 production，服务端要求先清 beta）
const envAll = "all"

// confirmDeleteFunc 为包级可打桩变量，单测替换以隔离真实终端交互
var confirmDeleteFunc = confirmDeleteByTypingKey

func newAppDeleteCmd() *cobra.Command {
	var file string
	var env string
	var yes bool

	cmd := &cobra.Command{
		Use:   "delete [key] --env production|beta|ALL",
		Short: "Delete an app on Make",
		Long: `Delete one half of a Make app pair. Every app is created as a prod/beta pair;
--env production deletes the prod app, --env beta deletes its paired beta app,
--env ALL deletes beta first and then production (the server refuses to delete a prod app
while its beta pair still exists). <key> is always the prod app key.`,
		Example: `  makecli app delete myapp --env beta
  makecli app delete myapp --env production --yes
  makecli app delete myapp --env ALL
  makecli app delete -f app.yaml --env beta`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if file != "" {
				return runAppDeleteFromFile(file, env, yes)
			}
			if len(args) == 0 {
				return fmt.Errorf("requires app key or -f flag")
			}
			return runAppDelete(args[0], env, yes)
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "path to YAML file containing Make.App resource")
	cmd.Flags().StringVar(&env, "env", "", "which environment to delete: production | beta | ALL (required)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the deletion confirmation prompt")
	_ = cmd.MarkFlagRequired("env")
	return cmd
}

func runAppDeleteFromFile(path, env string, skipConfirm bool) error {
	manifest, err := loadAppManifestFromFile(path)
	if err != nil {
		return err
	}
	return runAppDelete(manifest.Key, env, skipConfirm)
}

// resolveDeleteEnvs 把 --env 展开成有序的用户面环境序列：
// production / beta 各一步；all 先 beta 后 production（服务端要求先清 beta）。
// 纯函数不触网：每一步的删除目标都由服务端按 ?appRole= 定位，CLI 无需知道配对 key
func resolveDeleteEnvs(env string) ([]string, error) {
	switch env {
	case api.EnvProduction:
		return []string{api.EnvProduction}, nil
	case api.EnvBeta:
		return []string{api.EnvBeta}, nil
	case envAll:
		return []string{api.EnvBeta, api.EnvProduction}, nil
	}
	return nil, fmt.Errorf("invalid --env %q: must be %s, %s or %s", env, api.EnvProduction, api.EnvBeta, envAll)
}

func runAppDelete(key, env string, skipConfirm bool) error {
	env = strings.ToLower(env)
	envs, err := resolveDeleteEnvs(env)
	if err != nil {
		return err
	}
	if !skipConfirm {
		if err := confirmDeleteFunc(key, env); err != nil {
			return err
		}
	}
	// 每个环境一个 client：appRole 是 client 级横切 query，删哪一半由它选定
	for _, e := range envs {
		client, err := newClientFromProfile(api.WithAppRole(api.RoleForEnv(e)))
		if err != nil {
			return err
		}
		if err := client.DeleteApp(key); err != nil {
			return fmt.Errorf("delete %s environment: %w", e, err)
		}
		fmt.Printf("App '%s' %s environment deleted successfully\n", key, e)
	}
	return nil
}

// confirmDeleteByTypingKey 要求用户原样输入 app key 才放行删除（gh repo delete 同款强护栏）。
// 用户面只见「app + 环境」，配对 key 是服务端实现细节不外露。
// 非交互终端（管道 / CI）无法输入确认，直接拒绝并指引 --yes，杜绝挂起。
// huh 表单的 Validate 在输入 ≠ key 时阻断提交，唯一出路是输对 key 或 Ctrl-C 取消。
func confirmDeleteByTypingKey(key, env string) error {
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return fmt.Errorf("refusing to delete %s environment of %q without confirmation: re-run with --yes in a non-interactive shell", env, key)
	}

	var typed string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(fmt.Sprintf("Delete %s environment of app %q — this cannot be undone.", env, key)).
				Description(fmt.Sprintf("Type %q to confirm:", key)).
				Value(&typed).
				Validate(func(s string) error {
					if s != key {
						return fmt.Errorf("does not match %q", key)
					}
					return nil
				}),
		),
	).Run()

	if errors.Is(err, huh.ErrUserAborted) {
		return fmt.Errorf("deletion of %s environment of %q cancelled", env, key)
	}
	return err
}
