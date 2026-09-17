/**
 * [INPUT]: 依赖 cmd/client（newClientFromProfile）、cmd/app（loadAppManifestFromFile）、errors、fmt、os、charm.land/huh/v2（交互确认表单）、github.com/mattn/go-isatty（TTY 检测）、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newAppDeleteCmd 函数；包内 resolveDeleteSteps（--env → 有序删除步骤）、betaPairKey（GetApp 读 meta.pairAppKey + appRole 校验）、deleteStep 类型、appRoleBeta/appRoleProduction/envAll 常量；包级 confirmDeleteFunc 可打桩变量（测试替换，参照 deploy.go gitPushFunc 模式）
 * [POS]: cmd/app 的 delete 子命令。每个 App 是 product/beta 一对（MetaAPIDesign.md：meta.appRole 标角色，meta.pairAppKey 指向配对 app，product 有 beta 配对时服务端 409 拒删），
 *        --env 必填（大小写不敏感）：production 删 <key> 本身；beta 先 GetApp(key) 校验 appRole=product 再取 pairAppKey 为目标——配对 key 以服务端为准，CLI 不硬编码派生规则；
 *        all 展开为先 beta 后 production 的有序步骤（服务端要求先清 beta；无配对则只剩 production），逐步删除逐步提示。
 *        用户面只见「app key + 环境」：确认表单敲的是用户给的 app key（标题注明环境），成功提示同样不露配对 key（gh repo delete 同款，huh 表单实现），--yes 跳过；支持 -f 文件模式
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

// App 角色（MetaAPIDesign.md）：product 是客户交付的 app，beta 是开发自测的配对 app；
// --env 取值用 production 对齐 deploy 的环境词汇，服务端 meta.appRole 对应值是 product
const (
	appRoleProduction = "production"
	appRoleBeta       = "beta"
	envAll            = "all"
	metaRoleProduct   = "product"
)

// confirmDeleteFunc 为包级可打桩变量，单测替换以隔离真实终端交互
var confirmDeleteFunc = confirmDeleteByTypingKey

func newAppDeleteCmd() *cobra.Command {
	var file string
	var env string
	var yes bool

	cmd := &cobra.Command{
		Use:   "delete [key] --env production|beta|all",
		Short: "Delete an app on Make",
		Long: `Delete one half of a Make app pair. Every app is created as a product/beta pair;
--env production deletes <key> itself, --env beta looks up the app and deletes its paired beta app,
--env all deletes beta first and then production (the server refuses to delete a product app
while its beta pair still exists).`,
		Example: `  makecli app delete myapp --env beta
  makecli app delete myapp --env production --yes
  makecli app delete myapp --env all
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
	cmd.Flags().StringVar(&env, "env", "", "which environment to delete: production | beta | all (required, case-insensitive)")
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

// deleteStep 是一次实际删除：target 是发给服务端的 key，env 是用户面的环境名
type deleteStep struct{ target, env string }

// betaPairKey 以服务端为准取 product app 的配对 beta key：GetApp 读 meta.pairAppKey，并校验 appRole 是 product——
// 否则把 beta key 传进来会顺着 pairAppKey 反删 product。无配对返回空串，由调用方按 env 语义决定是否报错
func betaPairKey(client *api.Client, key string) (string, error) {
	app, err := client.GetApp(key)
	if err != nil {
		return "", err
	}
	if role, _ := app.Meta["appRole"].(string); role != metaRoleProduct {
		return "", fmt.Errorf("%q is a %s app, not a product app: pass the product key instead", key, role)
	}
	pair, _ := app.Meta["pairAppKey"].(string)
	return pair, nil
}

// resolveDeleteSteps 把「product key + --env」展开成有序删除步骤：
// production 只删 <key>；beta 只删配对 app（无配对报错）；all 先 beta 后 production（服务端要求先清 beta，无配对则只剩 production）
func resolveDeleteSteps(client *api.Client, key, env string) ([]deleteStep, error) {
	switch env {
	case appRoleProduction:
		return []deleteStep{{key, appRoleProduction}}, nil
	case appRoleBeta:
		pair, err := betaPairKey(client, key)
		if err != nil {
			return nil, err
		}
		if pair == "" {
			return nil, fmt.Errorf("app %q has no beta environment", key)
		}
		return []deleteStep{{pair, appRoleBeta}}, nil
	case envAll:
		pair, err := betaPairKey(client, key)
		if err != nil {
			return nil, err
		}
		var steps []deleteStep
		if pair != "" {
			steps = append(steps, deleteStep{pair, appRoleBeta})
		}
		return append(steps, deleteStep{key, appRoleProduction}), nil
	}
	return nil, fmt.Errorf("invalid --env %q: must be %s, %s or %s", env, appRoleProduction, appRoleBeta, envAll)
}

func runAppDelete(key, env string, skipConfirm bool) error {
	env = strings.ToLower(env)
	client, err := newClientFromProfile()
	if err != nil {
		return err
	}
	steps, err := resolveDeleteSteps(client, key, env)
	if err != nil {
		return err
	}
	if !skipConfirm {
		if err := confirmDeleteFunc(key, env); err != nil {
			return err
		}
	}
	for _, st := range steps {
		if err := client.DeleteApp(st.target); err != nil {
			return fmt.Errorf("delete %s environment: %w", st.env, err)
		}
		fmt.Printf("App '%s' %s environment deleted successfully\n", key, st.env)
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
