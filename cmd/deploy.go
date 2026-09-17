/**
 * [INPUT]: 依赖 cmd/client（newClientFromProfile/newRepoClientFromProfile）、cmd/app（loadAppManifestFromFile/validResourceKey）、cmd/app_create（appDSLPath）、cmd/git（openRepo/assertDeployable）、cmd/output（resolveOutputFormat/writeJSON/outputAuto/outputFlagUsage）、internal/api（ErrNotFound 哨兵、GetBuildTask/BuildTask 及 Finished/Succeeded 终态判定、GetDeploymentOverview/DeploymentOverview.Env 环境 URL）、errors、fmt、io、os、slices、strings、time、charm.land/huh/v2（production 确认表单）、github.com/mattn/go-isatty（TTY 检测）、github.com/go-git/go-git/v5（及 config/plumbing/transport/http 子包）、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newDeployCmd 函数；包内 assertAppRegistered（push 前 Meta 注册门控）、confirmProductionDeploy（production 部署确认）、runDeployStatus/waitAndRenderBuild/waitForBuild/deploymentURLFor/renderBuildResult/renderBuildStatus/formatBuildError/shortSha（--status/--wait 构建进度查询、等待与渲染）、buildStatusView（JSON 视图：BuildTask 平铺 + url omitempty）、errBuildFailed/errWaitTimeout 退出码哨兵（errors.go ExitCode 翻译为 2/124）、defaultWaitTimeout 常量；包级 gitPushFunc / confirmDeployFunc 可打桩变量（测试替换推送 / 终端交互，参照 update.go applyFunc、app_delete.go confirmDeleteFunc 模式）、buildPollInterval 可打桩轮询间隔；envBeta/envProduction 环境常量（别名 api.EnvBeta/EnvProduction）
 * [POS]: cmd 模块 app 命令组的 deploy 子命令——「纯 push 已提交状态」。--env 默认 envBeta（安全；用户面词汇 beta/production，服务端 key preview 的翻译收口在 api.ServerEnvKey/DisplayEnv），production 须显式 opt-in 且 push 前过 continue/abort 确认（--yes/-y 跳过，非交互终端拒绝并指引 --yes）。--status 短路部署，改为按本地 HEAD sha 反查构建服务（api.GetBuildTask，commitSha 即任务定位键）平铺渲染部署进度；--output table|json 双格式（json 仅限 --status 模式，deploy 推送输出会混入 stdout）。--wait 阻塞至构建终态（deploy --wait = push 后接上与 --status --wait 完全同一条等待路径）：轮询间隔 buildPollInterval=3s，ErrNotFound 视为「任务尚未创建」继续等（webhook 异步建任务窗口期），进度只在 status/phase 跃迁时打一行（json 模式走 stderr 保持 stdout 纯 JSON），--timeout 有界兜底（默认 5m，须与 --wait 搭配）；终态渲染完整详情后，未成功以 errBuildFailed（退出码 2）、超时以 errWaitTimeout（退出码 124）上抛，CI/agent 凭退出码判定。成功任务经 deploymentURLFor 带出对应环境访问 URL（与 app info 同源 GetDeploymentOverview，按 task.Environment 经 Env 选择器取址；URL 是结果装饰——仅 SUCCESS 查询、总览失败降级为空不影响主输出），table 尾行 URL:、json 平铺 url 字段。从 apps/dsl/app.yaml 读 app key，
 *        本地先行门控（openRepo 要求已 init、assertDeployable 要求有 commit 且工作树干净，脏/无仓库/无提交即报错，
 *        全在网络调用之前 fail-fast），再经 assertAppRegistered 用 Meta GetApp 把关 app 已注册（不存在即指引 app create -f，
 *        避免「有仓库、无 app」孤儿状态；在建仓库/推送之前短路），production 确认通过后再幂等准备 beta/production 仓库（MakeService.CreateResource）取 cloneUrl，
 *        用 go-git（纯 Go，不 shell-out）把当前 HEAD 推到固定分支（deployBranch，webhook 约定）；token 走 HTTP BasicAuth(make:<token>)。
 *        提交时机交还用户——deploy 不再自动 add/commit（建仓+ignore 由 `makecli app init` 负责）。
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/mattn/go-isatty"
	"github.com/qfeius/makecli/internal/api"
	"github.com/spf13/cobra"
)

// 部署目标环境——beta 是安全默认，production 是不可逆线上部署（词汇与 beta ⇄ 服务端 preview 的翻译收口在 internal/api）
const (
	envBeta       = api.EnvBeta
	envProduction = api.EnvProduction
)

// deployEnvs 是合法的部署环境集合，与服务端双仓库约定一一对应
var deployEnvs = []string{envBeta, envProduction}

// confirmDeployFunc 为包级可打桩变量，单测替换以隔离真实终端交互（参照 app_delete.go confirmDeleteFunc 模式）
var confirmDeployFunc = confirmProductionDeploy

// deployBranch 是构建流水线 webhook 监听的固定远端分支。
// 部署只推送到此分支——分支名是服务端约定，不是用户可调旋钮。
const deployBranch = "dev"

// anonymousRemote 是 go-git 临时 remote 的固定名（CreateRemoteAnonymous 约定值），
// 仅存在于内存、不写进 .git/config，用完即弃——cloneUrl 每次部署才解析，不该污染用户仓库配置。
const anonymousRemote = "anonymous"

// gitPushFunc 为包级可打桩变量，单测替换以隔离真实网络推送（本地仓库门控不打桩，跑真 go-git）
var gitPushFunc = pushCurrentHead

// errBuildFailed / errWaitTimeout 是 --wait 的语义化退出码哨兵（main 经 ExitCode 翻译为 2 / 124），
// 让 CI 与 agent 用退出码即可区分「构建失败」与「超时未完成」，无需解析文本。
var (
	errBuildFailed = errors.New("构建未成功")
	errWaitTimeout = errors.New("等待构建超时")
)

// buildPollInterval 是 --wait 的轮询间隔，包级变量供单测调小。
// 3s 对 CLI 已够实时；构建分钟级，更密只是徒增请求。
var buildPollInterval = 3 * time.Second

// defaultWaitTimeout 是 --wait 的缺省超时。有界等待是硬约束——无界阻塞对 CI 与
// agent 都是故障模式（agent 的工具调用有自己的超时上限，挂死比失败更贵）。
const defaultWaitTimeout = 5 * time.Minute

func newDeployCmd() *cobra.Command {
	var env string
	var force bool
	var yes bool
	var status bool
	var wait bool
	var timeout time.Duration
	var output string

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy an app to Make Platform",
		Example: `  makecli app deploy                         # 默认部署到 beta
  makecli app deploy --wait                  # 部署并阻塞至构建终态（退出码 0 成功 / 2 失败 / 124 超时）
  makecli app deploy --env production        # 部署到 production（需确认）
  makecli app deploy --env production --yes  # 跳过确认（CI / 非交互）
  makecli app deploy --status                # 查询当前 HEAD 提交的构建/部署进度
  makecli app deploy --status --wait         # 只等待构建终态，不推送
  makecli app deploy --status --output json  # 机器可读的进度快照`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// 无 --status 时 json 不合法：auto 直接落回 table（agent 跑 deploy 也该拿到推送日志，而不是报错）
			if !status && output == outputAuto {
				output = outputTable
			}
			format, err := resolveOutputFormat(output)
			if err != nil {
				return err
			}
			output = format
			if output == outputJSON && !status {
				return errors.New("--output json 需要与 --status 搭配（deploy 的推送输出会混入 stdout）")
			}
			if cmd.Flags().Changed("timeout") && !wait {
				return errors.New("--timeout 需要与 --wait 搭配")
			}
			if wait && timeout <= 0 {
				return errors.New("--timeout must be positive")
			}
			if status {
				return runDeployStatus(wait, timeout, output)
			}
			if err := runDeploy(env, force, yes); err != nil {
				return err
			}
			if !wait {
				return nil
			}
			// --wait：推送已完成，接上与 `--status --wait` 完全同一条等待路径（同一门控、同一渲染）
			return runDeployStatus(true, timeout, output)
		},
	}

	cmd.Flags().StringVar(&env, "env", envBeta, "target environment: beta | production")
	cmd.Flags().BoolVar(&force, "force", false, "force push")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the production deploy confirmation prompt")
	cmd.Flags().BoolVar(&status, "status", false, "show build status of the current HEAD commit instead of deploying")
	cmd.Flags().BoolVar(&wait, "wait", false, "block until the build reaches a terminal state (SUCCESS/FAILED/CANCELED)")
	cmd.Flags().DurationVar(&timeout, "timeout", defaultWaitTimeout, "max time to wait for the build (requires --wait)")
	cmd.Flags().StringVar(&output, "output", outputAuto, outputFlagUsage+"; json requires --status")
	return cmd
}

// runDeploy 编排「纯 push 部署」：env 校验 → 读 app key → 本地 git 门控（fail-fast）→ 注册门控 → production 确认 → 备仓库 → 推 HEAD。
// 本地门控刻意在网络调用之前：脏工作树 / 无仓库 / 无提交都不该先白跑一趟仓库准备，
// 提示用户先 commit 即可，零网络往返。skipConfirm（--yes）仅对 production 确认生效。
func runDeploy(env string, force, skipConfirm bool) error {
	if !slices.Contains(deployEnvs, env) {
		return fmt.Errorf("invalid --env %q: must be one of %s", env, strings.Join(deployEnvs, " | "))
	}

	appKey, err := appKeyFromDSL()
	if err != nil {
		return err
	}

	// 本地先行门控：仓库须已 init、有提交、工作树干净——否则报错让用户先 commit（网络之前）
	repo, err := openRepo()
	if err != nil {
		return err
	}
	if err := assertDeployable(repo); err != nil {
		return err
	}

	// app 身份真相在 Meta Server——push 之前确认已注册，否则只 init 过的本地工程也能
	// 推成功，留下「有仓库、无 app」的孤儿状态（app list 看不到）。网络门控，但刻意在
	// 建仓库/推送之前：既不为不存在的 app 建孤儿仓库，也不白推一趟。
	if err := assertAppRegistered(appKey); err != nil {
		return err
	}

	fmt.Printf("%-12s %s\n", "App:", appKey)
	fmt.Printf("%-12s %s\n", "Environment:", env)

	// production 是不可逆的线上部署——push 前要求显式 continue/abort 确认，--yes 跳过。
	// 确认刻意在建仓库之前：abort 时连幂等的仓库准备都不白跑。beta 是安全默认，不拦。
	if env == envProduction && !skipConfirm {
		if err := confirmDeployFunc(appKey); err != nil {
			return err
		}
	}

	client, token, err := newRepoClientFromProfile()
	if err != nil {
		return err
	}

	// CreateResource 幂等：组织/仓库不存在则创建，存在则复用，成功即可推送
	repoInfo, err := client.CreateRepository(appKey)
	if err != nil {
		return fmt.Errorf("准备代码仓库失败: %w", err)
	}

	// cloneURL 含内部组织 id 与仓库主机，是部署实现细节——只用于 push，不向用户展示
	cloneURL := repoInfo.CloneURLFor(env)
	if cloneURL == "" {
		return fmt.Errorf("服务端未返回 %s 环境的仓库地址", env)
	}

	if err := gitPushFunc(repo, cloneURL, token, force); err != nil {
		return err
	}

	fmt.Printf("Deployed '%s' to %s\n", appKey, env)
	return nil
}

// runDeployStatus 查询当前 HEAD 提交的构建/部署进度（wait=true 时阻塞至终态）。
// deploy 推的是 HEAD、构建服务以 push 的 commit sha 建任务，故本地 HEAD sha
// 即天然的任务定位键——无需让用户抄任务 ID，重跑即幂等地重新接上同一次构建。
// 工程定位与 deploy 同源（app.yaml + git 仓库门控），保证「在哪能 deploy，就在哪能查进度」。
func runDeployStatus(wait bool, timeout time.Duration, output string) error {
	appKey, err := appKeyFromDSL()
	if err != nil {
		return err
	}
	repo, err := openRepo()
	if err != nil {
		return err
	}
	head, err := repo.Head()
	if err != nil {
		return fmt.Errorf("仓库尚无提交：先 commit 并 makecli app deploy 后再查询进度")
	}
	sha := head.Hash().String()

	client, err := newClientFromProfile()
	if err != nil {
		return err
	}
	if wait {
		return waitAndRenderBuild(client, appKey, sha, timeout, output)
	}
	task, err := client.GetBuildTask(sha)
	if errors.Is(err, api.ErrNotFound) {
		return fmt.Errorf("commit %s 尚无构建任务：构建由 deploy 推送触发、服务端异步创建，请先 makecli app deploy 或稍后重试", shortSha(sha))
	}
	if err != nil {
		return fmt.Errorf("查询构建进度失败: %w", err)
	}
	return renderBuildResult(task, deploymentURLFor(client, appKey, task), output)
}

// deploymentURLFor 取构建成功后对应环境的访问地址（与 app info 同源：部署总览接口）。
// URL 是结果装饰而非部署结论——仅 SUCCESS 才查询（失败时线上仍是旧 release，展示会误导），
// 总览查询任何失败（含从未同步的 ErrNotFound）都降级为空串，绝不影响 deploy/status 主输出。
func deploymentURLFor(client *api.Client, appKey string, task *api.BuildTask) string {
	if !task.Succeeded() {
		return ""
	}
	overview, err := client.GetDeploymentOverview(appKey)
	if err != nil {
		return ""
	}
	if env := overview.Env(task.Environment); env != nil {
		return env.URL
	}
	return ""
}

// waitAndRenderBuild 阻塞轮询构建任务至终态，然后渲染完整详情（成功时带对应环境访问 URL）。
// json 模式下进度行走 stderr、stdout 只留最终状态对象，保持机器可解析；
// 未成功以 errBuildFailed 上抛（退出码 2）——渲染先于报错，失败详情不丢。
func waitAndRenderBuild(client *api.Client, appKey, sha string, timeout time.Duration, output string) error {
	progress := io.Writer(os.Stdout)
	if output == outputJSON {
		progress = os.Stderr
	}
	_, _ = fmt.Fprintf(progress, "Waiting for build of %s (timeout %s) ...\n", shortSha(sha), timeout)

	task, err := waitForBuild(client, sha, timeout, progress)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(progress)
	if err := renderBuildResult(task, deploymentURLFor(client, appKey, task), output); err != nil {
		return err
	}
	if !task.Succeeded() {
		return fmt.Errorf("%w（status: %s）", errBuildFailed, task.Status)
	}
	return nil
}

// waitForBuild 轮询 GetBuildTask 直到终态或超时。ErrNotFound 视为「任务尚未创建」继续等——
// push 后 webhook 异步建任务有窗口期，立即报错会制造假失败；由 timeout 统一兜底。
// 进度只在 status/phase 跃迁时打一行，不逐次刷屏（每个字节都会进 CI 日志与 agent 上下文）。
func waitForBuild(client *api.Client, sha string, timeout time.Duration, progress io.Writer) (*api.BuildTask, error) {
	deadline := time.Now().Add(timeout)
	lastLabel := ""
	for {
		task, err := client.GetBuildTask(sha)
		if err != nil && !errors.Is(err, api.ErrNotFound) {
			return nil, fmt.Errorf("查询构建进度失败: %w", err)
		}
		label := "task not created yet"
		if err == nil {
			label = task.Status
			if task.Phase != "" {
				label += " / " + task.Phase
			}
		}
		if label != lastLabel {
			_, _ = fmt.Fprintf(progress, "  %s\n", label)
			lastLabel = label
		}
		if err == nil && task.Finished() {
			return task, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w（%s）：最后状态 %s，构建可能仍在进行，可稍后 makecli app deploy --status 查询", errWaitTimeout, timeout, lastLabel)
		}
		time.Sleep(buildPollInterval)
	}
}

// buildStatusView 是构建状态的呈现视图：BuildTask 全字段平铺 + 成功后补充的环境访问 URL。
// 嵌入让 JSON 输出与线缆对象同形，url 仅在拿到时出现（omitempty）。
type buildStatusView struct {
	*api.BuildTask
	URL string `json:"url,omitempty"`
}

// renderBuildResult 按输出格式呈现构建任务：table 平铺渲染，json 输出平铺的状态视图对象。
func renderBuildResult(task *api.BuildTask, url, output string) error {
	if output == outputJSON {
		return writeJSON(buildStatusView{BuildTask: task, URL: url})
	}
	renderBuildStatus(task, url)
	return nil
}

// renderBuildStatus 平铺渲染构建任务详情（沿用 deploy 的 %-12s key-value 约定）。
// 可选字段（版本/镜像/错误/时间/URL）无值不渲染行，成功与失败共用同一张行表——
// Error 行只在失败任务、URL 行只在成功任务上自然出现，无需按 status 分支。
func renderBuildStatus(task *api.BuildTask, url string) {
	commit := shortSha(task.CommitSha)
	if task.CommitMessage != "" {
		commit += "  " + task.CommitMessage
	}
	rows := []struct{ label, value string }{
		{"App:", task.AppKey},
		{"Environment:", api.DisplayEnv(task.Environment)},
		{"Build:", fmt.Sprintf("#%d", task.ID)},
		{"Version:", task.DeploymentVersion},
		{"Commit:", commit},
		{"Status:", task.Status},
		{"Phase:", task.Phase},
		{"Error:", formatBuildError(task)},
		{"Image:", task.Image},
		{"Created:", task.CreateTime},
		{"Started:", task.StartTime},
		{"Finished:", task.FinishTime},
		{"URL:", url},
	}
	for _, r := range rows {
		if r.value != "" {
			fmt.Printf("%-12s %s\n", r.label, r.value)
		}
	}
}

// formatBuildError 把失败任务的错误码与错误信息拼成单行；两者皆空返回空串（该行不渲染）。
func formatBuildError(task *api.BuildTask) string {
	if task.ErrorCode != "" && task.ErrorMessage != "" {
		return fmt.Sprintf("[%s] %s", task.ErrorCode, task.ErrorMessage)
	}
	return task.ErrorCode + task.ErrorMessage
}

// shortSha 取 7 位短 sha 用于展示（git 惯例），过短原样返回。
func shortSha(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// appKeyFromDSL 从工程内 apps/dsl/app.yaml 读取 app key。
// app.yaml 是 app 身份的单一真相源（create 写出、apply/diff 读回），
// deploy 据此定位部署目标——目录可随意改名而部署仓库稳定，无需 --app 旋钮。
// 文件缺失给可操作错误：要么不在 app 工程根目录，要么尚未 makecli app create。
func appKeyFromDSL() (string, error) {
	if _, err := os.Stat(appDSLPath); err != nil {
		return "", fmt.Errorf("%s not found: run deploy from the app project root (or create it with `makecli app create`)", appDSLPath)
	}
	manifest, err := loadAppManifestFromFile(appDSLPath)
	if err != nil {
		return "", err
	}
	if err := validResourceKey(manifest.Key); err != nil {
		return "", fmt.Errorf("invalid app key in %s: %w", appDSLPath, err)
	}
	return manifest.Key, nil
}

// assertAppRegistered 确认 appKey 已在 Meta Server 注册为 App。
// deploy 推的是代码仓库，但 app 身份的真相在 Meta Server——跳过此关，
// 一个只 `app init` 过、从未 `app create` 的 key 也能 push 成功，留下「有仓库、无 app」
// 的孤儿状态。故 push 之前先 GetApp 把关：不存在给可操作错误指引按 app.yaml 注册远端
// （-f 形式取 app.yaml 里的精确 key，不像裸 key 会误建子目录），其余错误（网络/服务端）
// 原样上抛、绝不放行。
func assertAppRegistered(appKey string) error {
	client, err := newClientFromProfile()
	if err != nil {
		return err
	}
	if _, err := client.GetApp(appKey); err != nil {
		if errors.Is(err, api.ErrNotFound) {
			return fmt.Errorf("app '%s' 尚未在 Make 平台注册，请先创建: makecli app create -f %s", appKey, appDSLPath)
		}
		return fmt.Errorf("校验 app 是否存在失败: %w", err)
	}
	return nil
}

// confirmProductionDeploy 在 production 部署前要求 continue/abort 确认（与 app delete 同款 huh 护栏）。
// 非交互终端（CI / 管道）无法应答，直接拒绝并指引 --yes，杜绝挂起。
// confirmed 初值 false → 表单默认停在 Abort，用户须显式选 Continue 才放行；
// ErrUserAborted（Ctrl-C）与选 Abort 都视为取消。
func confirmProductionDeploy(appKey string) error {
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return fmt.Errorf("refusing to deploy %q to production without confirmation: re-run with --yes in a non-interactive shell", appKey)
	}

	confirmed := false
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Deploy %q to PRODUCTION?", appKey)).
				Description("This pushes to the live production environment.").
				Affirmative("Continue").
				Negative("Abort").
				Value(&confirmed),
		),
	).Run()

	if errors.Is(err, huh.ErrUserAborted) || (err == nil && !confirmed) {
		return fmt.Errorf("production deploy of %q cancelled", appKey)
	}
	return err
}

// pushCurrentHead 把仓库当前 HEAD 推送到部署分支。
// 调用前 assertDeployable 已确认 HEAD 存在且工作树干净，故此处 Head() 失败属防御性错误。
func pushCurrentHead(repo *git.Repository, cloneURL, token string, force bool) error {
	head, err := repo.Head()
	if err != nil {
		return fmt.Errorf("仓库无可推送的提交: %w", err)
	}
	fmt.Printf("Pushing %s -> %s ...\n", shortSha(head.Hash().String()), deployBranch)
	return pushHead(repo, head, cloneURL, token, force)
}

// pushHead 把 head 指向的提交推送到临时 remote 的固定部署分支。
// 用匿名 remote 承载 cloneUrl（不落 .git/config）；token 走 HTTP BasicAuth(make:<token>)；
// up-to-date（远端已是该提交）视为成功，不当错误。
func pushHead(repo *git.Repository, head *plumbing.Reference, cloneURL, token string, force bool) error {
	remote, err := repo.CreateRemoteAnonymous(&config.RemoteConfig{
		Name: anonymousRemote,
		URLs: []string{cloneURL},
	})
	if err != nil {
		return fmt.Errorf("准备推送目标失败: %w", err)
	}

	refspec := config.RefSpec(fmt.Sprintf("%s:refs/heads/%s", head.Name().String(), deployBranch))
	err = remote.Push(&git.PushOptions{
		RemoteName: anonymousRemote,
		RefSpecs:   []config.RefSpec{refspec},
		Auth:       &http.BasicAuth{Username: "make", Password: token},
		Force:      force,
		Progress:   os.Stdout,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("git push 失败: %w", err)
	}
	return nil
}
