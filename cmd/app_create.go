/**
 * [INPUT]: 依赖 cmd/client（newClientFromProfile）、cmd/app（loadAppManifestFromFile、validResourceKey、defaultName）、cmd/apply（ResourceManifest）、cmd/git（initGitRepo/ensureGitignore/stageAndCommit）、agents（embed 模板）、internal/api（WithDryRun）、bytes、fmt、os、path/filepath、github.com/go-git/go-git/v5、gopkg.in/yaml.v3、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newAppCreateCmd 函数；包内 runAppCreate / writeScaffold / scaffoldOutputs / fileExists / scaffoldGit / renderAppDSL / newAppManifest / deriveAppKey（writeScaffold/scaffoldOutputs/deriveAppKey/newAppManifest 被 app_init 复用）
 * [POS]: cmd/app 的 create 子命令——= app init 本地脚手架内核 + 远端 App 注册 + initial commit + 代码仓库。
 *        位置参数 <appKey> 同时是「目录名 + key」（filepath.Base(filepath.Abs(arg)) 推导，`.`/`..` 隐藏便利），
 *        validResourceKey 把关；写 CLAUDE.md/AGENTS.md（embed 模板，scaffoldFile 映射 embed→out 名）+ apps/dsl/app.yaml（ResourceManifest 序列化，与 apply/diff 同结构往返）；
 *        执行序「远端先行」：加载凭证→CreateApp→writeScaffold(幂等 skip-if-exists)→scaffoldGit(init+.gitignore+initial commit)，新目录远端失败时本地零残留、重跑干净；代码仓库不在此阶段准备（服务端要求先建 Beta 环境，由 deploy 幂等准备）；
 *        writeScaffold 幂等故 create 可与 init 组合（先 init 本地、再 create 补远端，不再硬拒已存在文件）；scaffoldGit 复用 init 内核再加 initial commit→create 产物即干净可 deploy；git 失败降级 stderr 警告（不阻断已成功的远端创建）；
 *        -f 文件模式仅建远端不脚手架；
 *        --dry-run 经 api.WithDryRun 注入 X-Dry-Run 头让远端校验但不落库，校验通过后立即收尾（跳过脚手架/git/仓库准备这些真实副作用），脚手架与文件两模式同款短路
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/qfeius/makecli/agents"
	"github.com/qfeius/makecli/internal/api"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// scaffoldFile 把 embed 模板名映射到写出文件名（同名去 .tmpl 后缀）。
type scaffoldFile struct{ embed, out string }

// scaffoldTemplates 是脚手架从 embed 写出的引导文档。
// .gitignore 不在此列——它由 scaffoldGit 经 ensureGitignore 幂等管理（与 app init 同一真相源）。
var scaffoldTemplates = []scaffoldFile{
	{"CLAUDE.md.tmpl", "CLAUDE.md"},
	{"AGENTS.md.tmpl", "AGENTS.md"},
}

// appDSLPath 是 App DSL 种子在工程内的相对路径（对齐 preflight 骨架）
var appDSLPath = filepath.Join("apps", "dsl", "app.yaml")

func newAppCreateCmd() *cobra.Command {
	var description string
	var displayName string
	var file string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "create <appKey>",
		Short: "Create a new Make app (scaffolds <appKey>/ and creates it on Make)",
		Example: `  makecli app create shop
  makecli app create shop --name "我的商城"
  makecli app create shop --name "My Shop" --description "demo shop"
  makecli app create shop --dry-run
  makecli app create -f apps/dsl/app.yaml`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if file != "" {
				return runAppCreateFromFile(file, dryRun)
			}
			if len(args) == 0 {
				return fmt.Errorf("requires <appKey> (or '.') or -f flag")
			}
			return runAppCreate(args[0], displayName, description, dryRun)
		},
	}

	cmd.Flags().StringVar(&description, "description", "", "app description")
	cmd.Flags().StringVar(&displayName, "name", "", "app display name (defaults to appKey)")
	cmd.Flags().StringVarP(&file, "file", "f", "", "path to YAML file containing Make.App resource (remote only, no scaffold)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate creation on Make without persisting (no scaffold, no git, no repo)")
	return cmd
}

// ---------------------------------- 脚手架模式：本地 + 远端 + 仓库 ----------------------------------

// runAppCreate = app init 核心 + 远端注册 + initial commit：解析 appKey → 加载凭证 → 远端创建 → 写脚手架 → git init+gitignore+commit → 准备仓库。
// 顺序刻意「远端先行」：token 失效 / 冲突 / 网络故障都在写任何本地文件之前报错，
// 这样换 profile 或修复 token 后重跑是干净的，不会被上一次的半成品工程拦住。
// 本地脚手架走幂等 skip-if-exists（与 app init 同一内核），故可与 init 组合：先 init 本地、再 create 补远端。
func runAppCreate(folder, displayName, description string, dryRun bool) error {
	appKey, err := deriveAppKey(folder)
	if err != nil {
		return err
	}
	manifest := newAppManifest(appKey, defaultName(displayName, appKey), description)

	client, err := newClientFromProfile(api.WithDryRun(dryRun))
	if err != nil {
		return err
	}

	// 远端先行：坏 token / 冲突 / 网络故障都在写任何本地文件之前报错，
	// 新目录失败时零残留、重跑干净；写本地走幂等 skip-if-exists，与 app init 同一脚手架内核。
	if apiErr := client.CreateApp(manifest.Key, manifest.Name, manifest.Properties); apiErr != nil {
		return apiErr
	}

	// dry-run 在远端校验通过后立即收尾：脚手架 / git init 都是真实副作用，
	// 而 dry-run 只回答「这次创建会不会成功」，故一律跳过，不在本地留任何痕迹。
	if dryRun {
		fmt.Printf("Dry run: app '%s' would be created successfully (no changes made)\n", appKey)
		return nil
	}

	if _, err := writeScaffold(folder, manifest); err != nil {
		return err
	}

	scaffoldGit(folder, appKey)

	fmt.Printf("App '%s' created successfully\n", appKey)
	return nil
}

// scaffoldGit 把脚手架目录变成可立即部署的 git 仓库：init（幂等）+ .gitignore + 一次 initial commit。
// 与 app init 共享内核（initGitRepo / ensureGitignore），但额外做 commit——使 create 产物即干净、可直接 deploy。
// 失败仅降级为 stderr 警告：远端 App 与本地脚手架均已就绪，
// git 没起来属可单独补救（重跑 `makecli app init` + 手动 commit），不该让已成功的 create 报全败。
// 全程不写 stdout——保持成功输出仅 `App 'X' created successfully` 一行。
func scaffoldGit(folder, appKey string) {
	if _, err := initGitRepo(folder); err != nil {
		warnGit(err)
		return
	}
	if _, err := ensureGitignore(folder); err != nil {
		warnGit(err)
		return
	}
	repo, err := git.PlainOpen(folder)
	if err != nil {
		warnGit(err)
		return
	}
	if _, err := stageAndCommit(repo, fmt.Sprintf("Initial scaffold for %s", appKey)); err != nil {
		warnGit(err)
	}
}

func warnGit(err error) {
	fmt.Fprintf(os.Stderr, "warning: git not initialized: %v (run 'makecli app init')\n", err)
}

// deriveAppKey 从目录参数推导 appKey：取绝对路径的 basename，统一覆盖 `shop` / `.` / `..`。
func deriveAppKey(folder string) (string, error) {
	abs, err := filepath.Abs(folder)
	if err != nil {
		return "", fmt.Errorf("resolve '%s': %w", folder, err)
	}
	appKey := filepath.Base(abs)
	if err := validResourceKey(appKey); err != nil {
		return "", fmt.Errorf("directory name %q can't be an app key: %w", appKey, err)
	}
	return appKey, nil
}

// newAppManifest 构造 Make.App 清单（脚手架写文件与远端 CreateApp 共用，单一真相源）。
// 空 description 不进 properties——保证 app.yaml 与远端无即时 diff 漂移。
func newAppManifest(appKey, name, description string) ResourceManifest {
	props := map[string]any{}
	if description != "" {
		props["description"] = description
	}
	return ResourceManifest{
		Key:        appKey,
		Name:       name,
		Type:       "Make.App",
		Meta:       map[string]any{"version": "1.0.0"},
		Properties: props,
	}
}

// scaffoldOutputs 是脚手架写出文件的相对路径（顺序即 init 状态输出顺序，单一真相源）。
func scaffoldOutputs() []string {
	outs := make([]string, 0, len(scaffoldTemplates)+1)
	for _, f := range scaffoldTemplates {
		outs = append(outs, f.out)
	}
	return append(outs, appDSLPath)
}

// writeScaffold 幂等写出本地工程骨架：CLAUDE.md / AGENTS.md（embed 模板）+ apps/dsl/app.yaml（DSL 种子）。
// skip-if-exists——已存在的文件原样保留（不覆盖用户编辑），返回本次真正新建的文件清单（供 init 输出）。
// .gitignore 不在此——由 ensureGitignore 管理。idempotent 是 init 与 create 共享同一脚手架内核的前提。
func writeScaffold(folder string, manifest ResourceManifest) ([]string, error) {
	if err := os.MkdirAll(folder, 0755); err != nil {
		return nil, fmt.Errorf("create '%s': %w", folder, err)
	}
	var created []string
	for _, f := range scaffoldTemplates {
		target := filepath.Join(folder, f.out)
		if fileExists(target) {
			continue
		}
		data, err := agents.Templates.ReadFile(f.embed)
		if err != nil {
			return created, fmt.Errorf("read embedded %s: %w", f.embed, err)
		}
		if err := os.WriteFile(target, data, 0644); err != nil {
			return created, err
		}
		created = append(created, f.out)
	}

	dslFull := filepath.Join(folder, appDSLPath)
	if !fileExists(dslFull) {
		dsl, err := renderAppDSL(manifest)
		if err != nil {
			return created, err
		}
		if err := os.MkdirAll(filepath.Dir(dslFull), 0755); err != nil {
			return created, fmt.Errorf("create '%s': %w", filepath.Dir(dslFull), err)
		}
		if err := os.WriteFile(dslFull, dsl, 0644); err != nil {
			return created, err
		}
		created = append(created, appDSLPath)
	}
	return created, nil
}

// fileExists 是 skip-if-exists 的只读判定（os.Stat 成功即视为存在）。
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// renderAppDSL 把清单序列化为人类可编辑的 app.yaml（2 空格缩进对齐 DSL 例子 + 顶部用法注释）。
// 复用 ResourceManifest，使 create 写出的就是 apply/diff 能原样读回的 manifest。
func renderAppDSL(manifest ResourceManifest) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# Make App DSL · edit then `makecli apply -f apps/dsl/app.yaml`\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(manifest); err != nil {
		return nil, err
	}
	_ = enc.Close()
	return buf.Bytes(), nil
}

// ---------------------------------- 文件模式：仅远端 ----------------------------------

func runAppCreateFromFile(path string, dryRun bool) error {
	manifest, err := loadAppManifestFromFile(path)
	if err != nil {
		return err
	}

	if err := validResourceKey(manifest.Key); err != nil {
		return err
	}

	client, err := newClientFromProfile(api.WithDryRun(dryRun))
	if err != nil {
		return err
	}

	props := manifest.Properties
	if props == nil {
		props = map[string]any{}
	}

	// 展示名缺省时回退用 key
	displayName := defaultName(manifest.Name, manifest.Key)

	if apiErr := client.CreateApp(manifest.Key, displayName, props); apiErr != nil {
		return apiErr
	}

	if dryRun {
		fmt.Printf("Dry run: app '%s' would be created successfully (no changes made)\n", manifest.Key)
		return nil
	}

	fmt.Printf("App '%s' created successfully\n", manifest.Key)
	return nil
}
