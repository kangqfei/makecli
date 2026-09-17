/**
 * [INPUT]: 依赖 encoding/json、errors、fmt、os、path、path/filepath、regexp、strings、
 *          gopkg.in/yaml.v3、github.com/spf13/cobra、cmd/app（loadAppManifestFromFile）、cmd/app_create（appDSLPath）
 * [OUTPUT]: 对外提供 newPreflightCmd 函数、errPreflightFailed 哨兵错误
 * [POS]: cmd 模块的顶层 preflight 命令，以 make-build-service build_spec.md 第 5 节检查
 *        清单为实现依据：
 *        构建模式 A/B 自动判定、包管理器按 lockfile 优先级判定（buildPreflightContext 一次
 *        收集事实），preflightChecks 表驱动逐项检查（ERROR/WARN/INFO 三级、条目与 spec 1:1、
 *        另有 makecli 自有 D1=apps/dsl），失败输出附 How to fix 指引（面向 AI agent 一步收敛）；
 *        存在 ERROR 返回 errPreflightFailed（main.go 转译退出码 1），作 CI / deploy 前置门禁
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// ---------------------------------- 哨兵错误 ----------------------------------

// errPreflightFailed 表示工程骨架检查未通过。沿 cobra RunE 链向上返回，
// 由 main.go 转译为退出码 1，使 CI / deploy 能据此门禁；它不是执行失败，
// 故 reportExecuteError（单一错误出口）放过它不打印 error: 行。
var errPreflightFailed = errors.New("preflight: project layout check failed")

// ---------------------------------- 命令定义 ----------------------------------

func newPreflightCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preflight [dir]",
		Short: "Check the directory satisfies the make build service contract",
		Long: `Preflight validates the project against the make build service build spec
before pushing, so builds fail here instead of remotely.

The build mode is auto-detected:

  mode A  apps components — apps/ui/package.json or apps/service/package.json
          exists; the platform provides the Dockerfiles for the components
  mode B  root Dockerfile — everything else; the repo brings its own Dockerfile

The package manager follows the lockfile (pnpm-lock.yaml > yarn.lock >
package-lock.json). Findings are reported as ERROR / WARN / INFO with a
"How to fix" hint each; any ERROR fails the run (exit code 1) so it can gate
CI or deploy. The directory defaults to the current working directory.`,
		Example: `  makecli preflight
  makecli preflight ./my-app`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			return runPreflight(root)
		},
	}
	return cmd
}

// finding 是一条未通过的检查，携带渲染 How to fix 所需的一切。
type finding struct {
	id, fix string
}

// runPreflight 构建上下文后按表求值：ERROR 通过打 ✓、失败打 ✗，WARN/INFO 仅在
// 命中时打 !/i（通过无话可说）；失败条目汇入 How to fix 块；存在 ERROR 返回
// errPreflightFailed（退出码 1），仅 WARN/INFO 不拦截。
func runPreflight(root string) error {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return fmt.Errorf("not a directory: %s", root)
	}
	ctx := buildPreflightContext(root)

	display := root
	if abs, err := filepath.Abs(root); err == nil {
		display = abs
	}
	mode := "B (root Dockerfile)"
	if ctx.modeA {
		mode = "A (apps components)"
	}
	pm := ctx.pm
	if len(ctx.lockfiles) == 0 {
		pm += " (no lockfile)"
	}
	fmt.Printf("%-17s %s\n", "Project:", display)
	fmt.Printf("%-17s %s\n", "Mode:", mode)
	fmt.Printf("%-17s %s\n\n", "Package manager:", pm)

	var errs, warns int
	var findings []finding
	for _, c := range preflightChecks {
		if !c.applies(ctx) {
			continue
		}
		res := c.run(ctx)
		switch {
		case res.ok:
			if c.level == levelError { // WARN/INFO 通过无话可说，不打印
				fmt.Printf("✓ %-3s %s\n", c.id, c.label)
			}
		case c.level == levelInfo:
			fmt.Printf("i %-3s [INFO]  %s\n", c.id, res.msg)
		case c.level == levelWarn:
			fmt.Printf("! %-3s [WARN]  %s\n", c.id, res.msg)
			warns++
			findings = append(findings, finding{id: c.id, fix: c.fix(ctx)})
		default:
			fmt.Printf("✗ %-3s [ERROR] %s\n", c.id, res.msg)
			errs++
			findings = append(findings, finding{id: c.id, fix: c.fix(ctx)})
		}
	}

	if len(findings) > 0 {
		fmt.Printf("\nHow to fix:\n")
		for _, f := range findings {
			fmt.Printf("  %-3s %s\n", f.id, f.fix)
		}
	}

	switch {
	case errs > 0:
		fmt.Printf("\nFAIL: %s, %s — the build service would reject or fail this build\n", plural(errs, "error"), plural(warns, "warning"))
		return errPreflightFailed
	case warns > 0:
		fmt.Printf("\nOK with %s — the build should succeed, see warnings above\n", plural(warns, "warning"))
	default:
		fmt.Printf("\nOK: ready for the make build service\n")
	}
	return nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// ================================ build spec 检查 ================================
// 以 make-build-service build_spec.md 第 5 节检查清单为实现依据。

// ---------------------------------- 文件投影 ----------------------------------

// workspacesField 兼容 package.json workspaces 的两种形态：
// ["ui"] 数组、{"packages": ["ui"]} 对象。
type workspacesField []string

func (w *workspacesField) UnmarshalJSON(data []byte) error {
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*w = arr
		return nil
	}
	var obj struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	*w = obj.Packages
	return nil
}

// packageJSON 是 package.json 面向检查的最小投影。
type packageJSON struct {
	Name       string            `json:"name"`
	Scripts    map[string]string `json:"scripts"`
	Workspaces workspacesField   `json:"workspaces"`
}

// pkgFile 是一次 package.json 读取的三态结果：不存在 / 存在但坏 / 存在且可用。
// 存在性与内容可用性分离——A1 只问存在，A2/A5/A6 才问内容，坏 JSON 归为内容检查的失败原因。
type pkgFile struct {
	path   string // 相对工程根，输出用
	exists bool
	err    error
	pkg    packageJSON
}

func loadPackageJSON(root, rel string) *pkgFile {
	p := &pkgFile{path: rel}
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		p.exists = !errors.Is(err, os.ErrNotExist)
		if p.exists {
			p.err = err
		}
		return p
	}
	p.exists = true
	if err := json.Unmarshal(data, &p.pkg); err != nil {
		p.err = fmt.Errorf("invalid JSON: %v", err)
	}
	return p
}

// pnpmWorkspaceFile 是 apps/pnpm-workspace.yaml 的读取结果，三态同 pkgFile。
type pnpmWorkspaceFile struct {
	exists   bool
	err      error
	packages []string
}

func loadPnpmWorkspace(root string) *pnpmWorkspaceFile {
	w := &pnpmWorkspaceFile{}
	data, err := os.ReadFile(filepath.Join(root, "apps", "pnpm-workspace.yaml"))
	if err != nil {
		w.exists = !errors.Is(err, os.ErrNotExist)
		if w.exists {
			w.err = err
		}
		return w
	}
	w.exists = true
	var doc struct {
		Packages []string `yaml:"packages"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		w.err = fmt.Errorf("invalid YAML: %v", err)
		return w
	}
	w.packages = doc.Packages
	return w
}

// ---------------------------------- 判定原语 ----------------------------------

// lockfilePriority 按 spec 第 1 节优先级排列：命中即定包管理器，其余被忽略。
var lockfilePriority = []struct{ file, pm string }{
	{"pnpm-lock.yaml", "pnpm"},
	{"yarn.lock", "yarn"},
	{"package-lock.json", "npm"},
}

// detectLockfiles 返回 dir 下按优先级排列的 lockfile 清单与判定出的包管理器。
// 无 lockfile 时包管理器回退 npm（spec 第 1 节：模式 A 走 npm install 兜底）。
func detectLockfiles(dir string) (files []string, pm string) {
	for _, lf := range lockfilePriority {
		if info, err := os.Stat(filepath.Join(dir, lf.file)); err == nil && !info.IsDir() {
			files = append(files, lf.file)
			if pm == "" {
				pm = lf.pm
			}
		}
	}
	if pm == "" {
		pm = "npm"
	}
	return files, pm
}

// workspaceCovers 判断 workspace 声明（pnpm packages / yarn+npm workspaces）是否覆盖
// 组件目录名。条目按 glob 语义匹配（path.Match），归一化 "./ui"、"ui/" 等写法；
// "!" 排除条目跳过——只判覆盖不判排除，排除误伤交构建期暴露。
func workspaceCovers(patterns []string, component string) bool {
	for _, p := range patterns {
		if strings.HasPrefix(p, "!") {
			continue
		}
		p = strings.TrimSuffix(strings.TrimPrefix(p, "./"), "/")
		if ok, err := path.Match(p, component); err == nil && ok {
			return true
		}
	}
	return false
}

// ---------------------------------- 上下文 ----------------------------------

// preflightContext 一次性收集全部检查所需事实，检查函数只读不做 IO。
type preflightContext struct {
	root           string
	repoName       string // 已 lower，G1 校验对象
	repoNameSource string // repoName 从哪来（输出用）

	modeA     bool
	lockfiles []string // 检测目录下的 lockfile，按 spec 优先级排列
	pm        string   // pnpm | yarn | npm（无 lockfile 回退 npm）
	lockDir   string   // lockfile 检测目录的相对名："apps" 或 "."（输出用）

	hasDSL           bool   // apps/dsl/ 目录存在
	appKey           string // apps/dsl/app.yaml 的 Make.App key；不可读时为空
	appYAMLReadable  bool   // app.yaml 解析成功且 key 非空（D1 用；deploy 靠它读 app key）
	appsPkg          *pkgFile
	uiPkg            *pkgFile
	servicePkg       *pkgFile
	uiDirExists      bool
	serviceDirExists bool
	pnpmWS           *pnpmWorkspaceFile

	rootPkg       *pkgFile
	hasDockerfile bool // 根目录 Dockerfile 存在（文件）
}

func buildPreflightContext(root string) *preflightContext {
	ctx := &preflightContext{
		root:       root,
		appsPkg:    loadPackageJSON(root, "apps/package.json"),
		uiPkg:      loadPackageJSON(root, "apps/ui/package.json"),
		servicePkg: loadPackageJSON(root, "apps/service/package.json"),
		rootPkg:    loadPackageJSON(root, "package.json"),
		pnpmWS:     loadPnpmWorkspace(root),
	}
	// spec 第 2 节：任一组件 package.json 存在即模式 A，无回退
	ctx.modeA = ctx.uiPkg.exists || ctx.servicePkg.exists
	ctx.hasDSL = dirExists(filepath.Join(root, "apps", "dsl"))
	ctx.uiDirExists = dirExists(filepath.Join(root, "apps", "ui"))
	ctx.serviceDirExists = dirExists(filepath.Join(root, "apps", "service"))
	if info, err := os.Stat(filepath.Join(root, "Dockerfile")); err == nil && !info.IsDir() {
		ctx.hasDockerfile = true
	}

	// spec 第 1 节：lockfile 检测目录模式 A 为 apps/，模式 B 为仓库根
	ctx.lockDir = "."
	lockPath := root
	if ctx.modeA {
		ctx.lockDir = "apps"
		lockPath = filepath.Join(root, "apps")
	}
	ctx.lockfiles, ctx.pm = detectLockfiles(lockPath)

	// app.yaml 只在此处解析一次：D1（可读性）与 repoName（key 取值）共用同一份事实，
	// 避免两处各自读文件、各自处理错误（曾经的 resolveRepoName 自己重新解析一遍）。
	if manifest, err := loadAppManifestFromFile(filepath.Join(root, appDSLPath)); err == nil && manifest.Key != "" {
		ctx.appKey = manifest.Key
		ctx.appYAMLReadable = true
	}
	ctx.repoName, ctx.repoNameSource = resolveRepoName(root, ctx.appKey, ctx.appYAMLReadable)
	return ctx
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// resolveRepoName 取镜像仓库名候选：appYAMLReadable 时 app.yaml 的 app key 优先（deploy
// 建仓即按 key 派生远端仓库名），否则回退目录 basename；统一 lower 后交 G1 校验。
// appKey/appYAMLReadable 由 buildPreflightContext 单次解析 app.yaml 得出，本函数不重新读文件。
func resolveRepoName(root, appKey string, appYAMLReadable bool) (name, source string) {
	if appYAMLReadable {
		return strings.ToLower(appKey), appDSLPath + " key"
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return strings.ToLower(filepath.Base(abs)), "directory name"
}

// ---------------------------------- 等级与结果 ----------------------------------

type checkLevel int

const (
	levelError checkLevel = iota
	levelWarn
	levelInfo
)

// checkResult 是单项检查的裁决：ok=true 通过；否则 msg 给出「实际 vs 期望」。
type checkResult struct {
	ok  bool
	msg string
}

func passed() checkResult { return checkResult{ok: true} }

func failf(format string, a ...any) checkResult {
	return checkResult{msg: fmt.Sprintf(format, a...)}
}

// ---------------------------------- 检查清单 ----------------------------------

// repoNameRE 是镜像仓库名合法性正则（spec G1，push 前置条件）。
var repoNameRE = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*$`)

// preflightCheck 与 build spec 第 5 节条目 1:1 对应：applies 是「条件」列，run 是
// 「判定」列，fix 是失败时的修复动作（为什么 + 改哪个文件 + 改成什么），供
// How to fix 块引导人或 AI agent 一步收敛。fix 为 nil 的条目（INFO）不进该块。
type preflightCheck struct {
	id      string
	level   checkLevel
	label   string // ERROR 级通过时打印的短标签
	applies func(*preflightContext) bool
	run     func(*preflightContext) checkResult
	fix     func(*preflightContext) string
}

func always(*preflightContext) bool { return true }

// componentBuildScript 校验 package.json 的 scripts.build 非空（A2 / B3 同构复用）。
func componentBuildScript(p *pkgFile) checkResult {
	if p.err != nil {
		return failf("%s: %v", p.path, p.err)
	}
	if strings.TrimSpace(p.pkg.Scripts["build"]) == "" {
		return failf("%s: scripts.build is missing or empty", p.path)
	}
	return passed()
}

// componentName 校验组件包名与目录名一致（A6 / A7 同构复用）。
func componentName(p *pkgFile, want string) checkResult {
	if p.err != nil {
		return failf("%s: %v", p.path, p.err)
	}
	if p.pkg.Name != want {
		return failf("%s: name is %q, must be %q", p.path, p.pkg.Name, want)
	}
	return passed()
}

// uncoveredComponents 汇总未被 workspace 声明覆盖的既存组件名（A4 / A5 共用）。
func uncoveredComponents(ctx *preflightContext, patterns []string) []string {
	var missing []string
	if ctx.uiPkg.exists && !workspaceCovers(patterns, "ui") {
		missing = append(missing, "ui")
	}
	if ctx.servicePkg.exists && !workspaceCovers(patterns, "service") {
		missing = append(missing, "service")
	}
	return missing
}

// preflightChecks 的顺序即输出顺序：模式相关条目在前（最可能出错），通用尾随，INFO 收尾。
var preflightChecks = []preflightCheck{
	{
		id: "D1", level: levelError, label: "apps/dsl/ (Make app DSL)",
		applies: func(ctx *preflightContext) bool { return ctx.modeA },
		run: func(ctx *preflightContext) checkResult {
			if !ctx.hasDSL {
				return failf("apps/dsl/ not found — Make app identity (app.yaml) lives there")
			}
			if !ctx.appYAMLReadable {
				return failf("apps/dsl/app.yaml is missing or has no readable Make.App key — `makecli app deploy` reads the app key from it")
			}
			return passed()
		},
		fix: func(_ *preflightContext) string {
			return "run `makecli app init` in the project root to scaffold apps/dsl/app.yaml; `makecli app deploy` reads the app key from it"
		},
	},
	{
		id: "A1", level: levelError, label: "apps/package.json (workspace root)",
		applies: func(ctx *preflightContext) bool { return ctx.modeA },
		run: func(ctx *preflightContext) checkResult {
			if !ctx.appsPkg.exists {
				return failf("apps/package.json not found")
			}
			return passed()
		},
		fix: func(_ *preflightContext) string {
			return "create apps/package.json as the workspace root — dependency install runs there once for all components"
		},
	},
	{
		id: "A2", level: levelError, label: "apps/ui/package.json scripts.build",
		applies: func(ctx *preflightContext) bool { return ctx.uiPkg.exists },
		run:     func(ctx *preflightContext) checkResult { return componentBuildScript(ctx.uiPkg) },
		fix: func(_ *preflightContext) string {
			return `add a non-empty "build" script to apps/ui/package.json; its build must emit apps/ui/dist/index.html`
		},
	},
	{
		id: "A2", level: levelError, label: "apps/service/package.json scripts.build",
		applies: func(ctx *preflightContext) bool { return ctx.servicePkg.exists },
		run:     func(ctx *preflightContext) checkResult { return componentBuildScript(ctx.servicePkg) },
		fix: func(_ *preflightContext) string {
			return `add a non-empty "build" script to apps/service/package.json; its build must emit apps/service/dist/server.js`
		},
	},
	{
		id: "A3", level: levelError, label: "lockfile in apps/ (service frozen install)",
		applies: func(ctx *preflightContext) bool { return ctx.modeA && ctx.servicePkg.exists },
		run: func(ctx *preflightContext) checkResult {
			if len(ctx.lockfiles) == 0 {
				return failf("no lockfile in apps/ (need one of pnpm-lock.yaml / yarn.lock / package-lock.json)")
			}
			return passed()
		},
		fix: func(_ *preflightContext) string {
			return "the service image reinstalls production deps with a frozen lockfile: run your package manager's install inside apps/ (e.g. `cd apps && pnpm install`) and commit the lockfile"
		},
	},
	{
		id: "A4", level: levelError, label: "apps/pnpm-workspace.yaml covers components",
		applies: func(ctx *preflightContext) bool { return ctx.modeA && ctx.pm == "pnpm" },
		run: func(ctx *preflightContext) checkResult {
			ws := ctx.pnpmWS
			if !ws.exists {
				return failf("apps/pnpm-workspace.yaml not found (required with pnpm)")
			}
			if ws.err != nil {
				return failf("apps/pnpm-workspace.yaml: %v", ws.err)
			}
			if missing := uncoveredComponents(ctx, ws.packages); len(missing) > 0 {
				return failf("apps/pnpm-workspace.yaml packages %v does not cover: %s", ws.packages, strings.Join(missing, ", "))
			}
			return passed()
		},
		fix: func(_ *preflightContext) string {
			return "declare every component in apps/pnpm-workspace.yaml (packages: [ui, service]) — dependency install does not reach undeclared components"
		},
	},
	{
		id: "A5", level: levelError, label: `apps/package.json "workspaces" covers components`,
		applies: func(ctx *preflightContext) bool {
			return ctx.modeA && ctx.pm != "pnpm" && ctx.appsPkg.exists
		},
		run: func(ctx *preflightContext) checkResult {
			if ctx.appsPkg.err != nil {
				return failf("%s: %v", ctx.appsPkg.path, ctx.appsPkg.err)
			}
			if missing := uncoveredComponents(ctx, ctx.appsPkg.pkg.Workspaces); len(missing) > 0 {
				return failf("apps/package.json workspaces %v does not cover: %s", ctx.appsPkg.pkg.Workspaces, strings.Join(missing, ", "))
			}
			return passed()
		},
		fix: func(_ *preflightContext) string {
			return `add "workspaces": ["ui", "service"] (the components that exist) to apps/package.json — yarn/npm install only reaches declared workspace members`
		},
	},
	{
		id: "A6", level: levelError, label: `apps/ui/package.json name == "ui"`,
		applies: func(ctx *preflightContext) bool { return ctx.uiPkg.exists },
		run:     func(ctx *preflightContext) checkResult { return componentName(ctx.uiPkg, "ui") },
		fix: func(_ *preflightContext) string {
			return `set "name": "ui" in apps/ui/package.json — the build system locates components by package name (--filter/--workspace), not by path`
		},
	},
	{
		id: "A7", level: levelError, label: `apps/service/package.json name == "service"`,
		applies: func(ctx *preflightContext) bool { return ctx.servicePkg.exists },
		run:     func(ctx *preflightContext) checkResult { return componentName(ctx.servicePkg, "service") },
		fix: func(_ *preflightContext) string {
			return `set "name": "service" in apps/service/package.json — the build system locates components by package name (--filter/--workspace), not by path`
		},
	},
	// A8 不限模式：spec 第 7 节要求「组件目录存在但无 package.json、回退模式 B」同报 A8+B1
	{
		id: "A8", level: levelWarn,
		applies: func(ctx *preflightContext) bool { return ctx.uiDirExists && !ctx.uiPkg.exists },
		run: func(_ *preflightContext) checkResult {
			return failf("apps/ui/ exists but has no package.json — the component is silently skipped, no ui image will be built")
		},
		fix: func(_ *preflightContext) string {
			return `add apps/ui/package.json (with "name": "ui" and a "build" script) or delete the apps/ui/ directory`
		},
	},
	{
		id: "A8", level: levelWarn,
		applies: func(ctx *preflightContext) bool { return ctx.serviceDirExists && !ctx.servicePkg.exists },
		run: func(_ *preflightContext) checkResult {
			return failf("apps/service/ exists but has no package.json — the component is silently skipped, no service image will be built")
		},
		fix: func(_ *preflightContext) string {
			return `add apps/service/package.json (with "name": "service" and a "build" script) or delete the apps/service/ directory`
		},
	},
	{
		id: "A9", level: levelWarn,
		applies: func(ctx *preflightContext) bool {
			return ctx.modeA && ctx.uiPkg.exists && !ctx.servicePkg.exists && len(ctx.lockfiles) == 0
		},
		run: func(_ *preflightContext) checkResult {
			return failf("no lockfile in apps/: build falls back to plain `npm install`, results are not reproducible")
		},
		fix: func(_ *preflightContext) string {
			return "run your package manager's install inside apps/ and commit the resulting lockfile"
		},
	},
	{
		id: "A11", level: levelInfo,
		applies: func(ctx *preflightContext) bool {
			return ctx.modeA && (ctx.rootPkg.exists || ctx.hasDockerfile)
		},
		run: func(ctx *preflightContext) checkResult {
			var ignored []string
			if ctx.rootPkg.exists {
				ignored = append(ignored, "package.json")
			}
			if ctx.hasDockerfile {
				ignored = append(ignored, "Dockerfile")
			}
			return failf("root %s ignored in mode A (apps components take over)", strings.Join(ignored, " and "))
		},
	},
	// A15 TEMP：build-job.sh(1b83199) 的 service 镜像模板仅支持 pnpm；差距关闭后整行删除
	{
		id: "A15", level: levelError, label: "service component uses pnpm (temporary gap)",
		applies: func(ctx *preflightContext) bool { return ctx.modeA && ctx.servicePkg.exists && ctx.pm != "pnpm" },
		run: func(ctx *preflightContext) checkResult {
			return failf("service images currently support pnpm only, detected %s (temporary build service gap)", ctx.pm)
		},
		fix: func(ctx *preflightContext) string {
			if len(ctx.lockfiles) == 0 {
				return "switch apps/ to pnpm: run `cd apps && pnpm install` (there is no existing lockfile to import) and commit pnpm-lock.yaml + pnpm-workspace.yaml"
			}
			return "switch apps/ to pnpm: delete other lockfiles, run `cd apps && pnpm import && pnpm install` (converts the existing lockfile) and commit pnpm-lock.yaml + pnpm-workspace.yaml"
		},
	},
	{
		id: "B1", level: levelError, label: "Dockerfile at repo root",
		applies: func(ctx *preflightContext) bool { return !ctx.modeA },
		run: func(ctx *preflightContext) checkResult {
			if !ctx.hasDockerfile {
				return failf("Dockerfile not found at repo root")
			}
			return passed()
		},
		fix: func(_ *preflightContext) string {
			return "add a Dockerfile at the repo root (build context is the repo root); or, if you meant the apps component mode, add apps/ui/package.json or apps/service/package.json"
		},
	},
	{
		id: "B2", level: levelError, label: "lockfile next to package.json",
		applies: func(ctx *preflightContext) bool { return !ctx.modeA && ctx.rootPkg.exists },
		run: func(ctx *preflightContext) checkResult {
			if len(ctx.lockfiles) == 0 {
				return failf("package.json exists but no lockfile (need one of pnpm-lock.yaml / yarn.lock / package-lock.json)")
			}
			return passed()
		},
		fix: func(_ *preflightContext) string {
			return "root-Dockerfile mode has no `npm install` fallback — without a lockfile the build runs `npm ci` and always fails; run your package manager's install and commit the lockfile"
		},
	},
	{
		id: "B3", level: levelError, label: "package.json scripts.build",
		applies: func(ctx *preflightContext) bool { return !ctx.modeA && ctx.rootPkg.exists },
		run:     func(ctx *preflightContext) checkResult { return componentBuildScript(ctx.rootPkg) },
		fix: func(_ *preflightContext) string {
			return `add a non-empty "build" script to package.json — with a root package.json the build service always runs install + build before the docker build`
		},
	},
	{
		id: "G1", level: levelError, label: "image repository name",
		applies: always,
		run: func(ctx *preflightContext) checkResult {
			if !repoNameRE.MatchString(ctx.repoName) {
				return failf("repo name %q (from %s) is not a valid image repository name (need %s)", ctx.repoName, ctx.repoNameSource, repoNameRE)
			}
			return passed()
		},
		fix: func(ctx *preflightContext) string {
			return fmt.Sprintf("rename so that %s lower-cases to letters/digits with single ./_/- separators — image push uses it as the repository name", ctx.repoNameSource)
		},
	},
	{
		id: "P1", level: levelWarn,
		applies: func(ctx *preflightContext) bool { return len(ctx.lockfiles) > 1 },
		run: func(ctx *preflightContext) checkResult {
			return failf("multiple lockfiles in %s: %s — %s wins (pnpm > yarn > npm), the rest are ignored",
				ctx.lockDir, strings.Join(ctx.lockfiles, ", "), ctx.lockfiles[0])
		},
		fix: func(ctx *preflightContext) string {
			return fmt.Sprintf("keep exactly one lockfile in %s: delete %s", ctx.lockDir, strings.Join(ctx.lockfiles[1:], ", "))
		},
	},
	{
		id: "G2", level: levelInfo, applies: always,
		run: func(_ *preflightContext) checkResult {
			return failf("build job time limit is 30 minutes by default")
		},
	},
}
