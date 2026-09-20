/**
 * [INPUT]: 依赖 cmd/client（newClientFromProfile/resolveContext）、cmd/deploy（appKeyFromDSL/assertAppRegistered/confirmProductionAction/envURLFor/errWaitTimeout/buildPollInterval/shortSha）、cmd/output（resolveOutputFormat/writeJSON/addOutputFlag）、internal/api（PromoteApp/GetPromoteStatus/PromoteRun/PromoteStatus/GetDeploymentOverview/EnvBeta/EnvProduction/ErrNotFound）、internal/config（Dir）、encoding/json、errors、fmt、io、os、path/filepath、time、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newPromoteCmd 函数；包内 runPromote / runPromoteStatus（发起发布 / 查进度两条主路径）、promoteTarget + resolvePromoteTarget（app.yaml → 已注册 App → beta/product 双 key 定位）、waitAndRenderPromote / waitForPromote / renderPromoteResult / renderPromoteStatus（等待与渲染）、promoteReceipt / promoteStatusView（JSON 视图）、promoteRunRecord + savePromoteRun / loadPromoteRun / promoteRunPath（本地 run 记录）、errPromoteFailed 退出码哨兵（errors.go ExitCode 翻译为 2）、defaultPromoteTimeout 常量；包级 confirmPromoteFunc 可打桩变量
 * [POS]: cmd 模块 app 命令组的 promote 子命令——对标 vercel promote：把 beta 环境当前生效的版本发布到 production，
 *        输入是服务端的 beta 状态而非本地代码（不 push、不读本地 git），「先 beta 后 production」因此是结构约束而非校验分支。
 *        流程：app.yaml 取 key → assertAppRegistered → KeyForEnv 定位 beta/product 两个 app → 部署总览取 beta 当前 commit
 *        做来源摘要（beta 从未部署即 fail-fast；总览查询失败降级为 unknown 交服务端裁决）→ production 确认（--yes 跳过，
 *        非交互拒绝）→ api.PromoteApp 发起（服务端 Temporal 异步流程，回执 workflowId+runId）→ 回执落盘
 *        <config.Dir>/promote/<context>--<betaKey>.json（runId 每次不同、查进度必须同传，落盘后 --status 免抄 ID；
 *        按 context 分文件，dev/test/production 后端的同名 app 互不串号）。--status 读该记录查进度，--wait 轮询至终态
 *        （promote --wait = 发起后接上与 --status --wait 同一条等待路径），进度只在 state/step 跃迁时打一行，
 *        json 模式进度走 stderr、stdout 只留最终对象；未成功 errPromoteFailed（退出码 2）、超时 errWaitTimeout（124）。
 *        成功后经 envURLFor 带出 production 访问 URL。
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/qfeius/makecli/internal/api"
	"github.com/qfeius/makecli/internal/config"
	"github.com/spf13/cobra"
)

// confirmPromoteFunc 为包级可打桩变量（测试替换终端确认），参照 deploy.go confirmDeployFunc 模式
var confirmPromoteFunc = confirmProductionPromote

// errPromoteFailed 是 --wait 等到非成功终态的退出码哨兵（main 经 ExitCode 翻译为 2）
var errPromoteFailed = errors.New("发布未成功")

// defaultPromoteTimeout 是 --wait 的缺省超时：发布串联「配置同步 → 构建 → 部署」三段，比单次构建更长。
const defaultPromoteTimeout = 10 * time.Minute

func newPromoteCmd() *cobra.Command {
	var yes bool
	var status bool
	var wait bool
	var timeout time.Duration
	var output string

	cmd := &cobra.Command{
		Use:   "promote",
		Short: "Promote the beta environment to production",
		Long: `Promote publishes what is currently running in beta to production: the
console config plus the commit of beta's last successful deployment.

Nothing is pushed from the local repository. The source is always the beta
environment, so an app must be deployed to beta first. The publish runs
asynchronously on the server; --wait blocks until it reaches a terminal state.`,
		Example: `  makecli app promote                         # beta → production（需确认）
  makecli app promote --yes --wait            # CI / 非交互：跳过确认并阻塞至终态（退出码 0 成功 / 2 失败 / 124 超时）
  makecli app promote --status                # 查询最近一次发布的进度
  makecli app promote --status --wait         # 只等待发布终态，不发起新发布
  makecli app promote --status --output json  # 机器可读的进度快照`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			output, err := resolveOutputFormat(output)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("timeout") && !wait {
				return errors.New("--timeout 需要与 --wait 搭配")
			}
			if wait && timeout <= 0 {
				return errors.New("--timeout 必须大于 0")
			}
			if status {
				return runPromoteStatus(wait, timeout, output)
			}
			return runPromote(yes, wait, timeout, output)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the production promote confirmation prompt")
	cmd.Flags().BoolVar(&status, "status", false, "show progress of the last promote instead of starting one")
	cmd.Flags().BoolVar(&wait, "wait", false, "block until the promote reaches a terminal state")
	cmd.Flags().DurationVar(&timeout, "timeout", defaultPromoteTimeout, "max time to wait for the promote (requires --wait)")
	addOutputFlag(cmd, &output)
	return cmd
}

// promoteTarget 是一次 promote 的定位结果：app.yaml 里的 key 与配对中承载两个环境的 app key。
type promoteTarget struct {
	appKey     string
	betaKey    string
	productKey string
	context    string
}

// resolvePromoteTarget 从 app.yaml 取 key，经 Meta 注册门控后按配对信息定位 beta 与 product 两个 app。
// 无 beta 环境（未配对）给可操作错误：promote 的输入就是 beta，没有 beta 就无从发布。
func resolvePromoteTarget() (*promoteTarget, error) {
	appKey, err := appKeyFromDSL()
	if err != nil {
		return nil, err
	}
	app, err := assertAppRegistered(appKey)
	if err != nil {
		return nil, err
	}
	betaKey, err := app.KeyForEnv(api.EnvBeta)
	if err != nil {
		return nil, fmt.Errorf("app '%s' 没有 beta 环境，请先在 Make Console 创建 Beta 环境并 makecli app deploy", appKey)
	}
	productKey, err := app.KeyForEnv(api.EnvProduction)
	if err != nil {
		return nil, err
	}
	ctx, _, err := resolveContext()
	if err != nil {
		return nil, err
	}
	return &promoteTarget{appKey: appKey, betaKey: betaKey, productKey: productKey, context: ctx}, nil
}

// promoteReceipt 是发起发布后的回执视图（json 模式 stdout 输出）
type promoteReceipt struct {
	App     string `json:"app"`
	BetaApp string `json:"betaApp"`
	api.PromoteRun
}

// runPromote 编排 beta → production 发布：定位 → 来源摘要 → 确认 → 发起 → 落盘回执 →（--wait）等待。
// 摘要把 beta 当前 commit 打出来让用户看见发的是什么——本地 HEAD 可能与 beta 不一致，
// promote 的语义是「发布 beta 上的东西」，故不阻断，只呈现。
func runPromote(skipConfirm, wait bool, timeout time.Duration, output string) error {
	target, err := resolvePromoteTarget()
	if err != nil {
		return err
	}
	client, err := newClientFromProfile()
	if err != nil {
		return err
	}

	// json 模式 stdout 只留最终对象，摘要与进度走 stderr
	info := io.Writer(os.Stdout)
	if output == outputJSON {
		info = os.Stderr
	}

	source, current, err := promoteSummary(client, target.productKey)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(info, "%-12s %s\n", "App:", target.appKey)
	_, _ = fmt.Fprintf(info, "%-12s beta  %s  (%s)\n", "Source:", source, target.betaKey)
	_, _ = fmt.Fprintf(info, "%-12s production  (current: %s)\n", "Target:", current)

	if !skipConfirm {
		if err := confirmPromoteFunc(target.appKey); err != nil {
			return err
		}
	}

	run, err := client.PromoteApp(target.betaKey)
	if err != nil {
		return fmt.Errorf("发起发布失败: %w", err)
	}
	// 落盘失败不回滚已发起的发布——只是 --status 用不了，警告并把 runId 留在输出里
	if err := savePromoteRun(target, run); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "warning: 无法记录发布任务（--status 将不可用）: %v\n", err)
	}
	_, _ = fmt.Fprintf(info, "Promote started: run %s\n", run.RunID)

	if wait {
		return waitAndRenderPromote(client, target, run, timeout, output)
	}
	if output == outputJSON {
		return writeJSON(promoteReceipt{App: target.appKey, BetaApp: target.betaKey, PromoteRun: *run})
	}
	fmt.Println("Track progress: makecli app promote --status")
	return nil
}

// promoteSummary 取来源（beta 当前 commit）与目标（production 当前 commit）的短 sha 做确认摘要。
// beta 从未部署时 fail-fast——服务端同样会拒绝，但在确认表单之前拦住省一次交互；
// 总览查询本身失败则降级为 unknown 交服务端裁决，不因装饰性查询阻断发布。
func promoteSummary(client *api.Client, productKey string) (source, current string, err error) {
	overview, err := client.GetDeploymentOverview(productKey)
	if err != nil {
		return "unknown", "unknown", nil
	}
	beta := overview.Env(api.EnvBeta)
	if beta == nil {
		return "", "", errors.New("beta 环境尚未部署，请先 makecli app deploy")
	}
	return commitLabel(beta), commitLabel(overview.Env(api.EnvProduction)), nil
}

// commitLabel 把环境部署状态压成短 sha；nil（从未部署）或无 sha 显示 none。
func commitLabel(env *api.EnvDeployment) string {
	if env == nil || env.CommitSha == "" {
		return "none"
	}
	return shortSha(env.CommitSha)
}

// runPromoteStatus 查询最近一次发布的进度（wait=true 时阻塞至终态）。
// 定位与 promote 同源（app.yaml + Meta），run 标识来自本地记录——服务端要求 workflowId+runId 同传，
// 落盘即免让用户抄 ID，重跑幂等地接上同一次发布。
func runPromoteStatus(wait bool, timeout time.Duration, output string) error {
	target, err := resolvePromoteTarget()
	if err != nil {
		return err
	}
	rec, err := loadPromoteRun(target)
	if err != nil {
		return err
	}
	client, err := newClientFromProfile()
	if err != nil {
		return err
	}
	run := &api.PromoteRun{WorkflowID: rec.WorkflowID, RunID: rec.RunID}
	if wait {
		return waitAndRenderPromote(client, target, run, timeout, output)
	}
	st, err := client.GetPromoteStatus(target.betaKey, run.WorkflowID, run.RunID)
	if err != nil {
		if errors.Is(err, api.ErrNotFound) {
			return fmt.Errorf("发布任务 %s 不存在（可能已过期），可重新 makecli app promote", run.RunID)
		}
		return fmt.Errorf("查询发布进度失败: %w", err)
	}
	return renderPromoteResult(target, st, productionURLFor(client, target, st), output)
}

// productionURLFor 成功后取 production 访问地址；未成功不查（线上仍是旧 release，展示会误导）。
func productionURLFor(client *api.Client, target *promoteTarget, st *api.PromoteStatus) string {
	if !st.Succeeded() {
		return ""
	}
	return envURLFor(client, target.productKey, api.EnvProduction)
}

// waitAndRenderPromote 阻塞轮询发布至终态，然后渲染完整详情（成功时带 production URL）。
// 渲染先于报错，失败详情不丢；未成功以 errPromoteFailed 上抛（退出码 2）。
func waitAndRenderPromote(client *api.Client, target *promoteTarget, run *api.PromoteRun, timeout time.Duration, output string) error {
	progress := io.Writer(os.Stdout)
	if output == outputJSON {
		progress = os.Stderr
	}
	_, _ = fmt.Fprintf(progress, "Waiting for promote run %s (timeout %s) ...\n", run.RunID, timeout)

	st, err := waitForPromote(client, target.betaKey, run, timeout, progress)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(progress)
	if err := renderPromoteResult(target, st, productionURLFor(client, target, st), output); err != nil {
		return err
	}
	if !st.Succeeded() {
		return fmt.Errorf("%w（state: %s）", errPromoteFailed, st.State)
	}
	return nil
}

// waitForPromote 轮询 GetPromoteStatus 直到终态或超时。ErrNotFound 视为「任务尚未可查」继续等
// （发起后工作流登记有窗口期），由 timeout 统一兜底；进度只在 state/step 跃迁时打一行。
func waitForPromote(client *api.Client, betaKey string, run *api.PromoteRun, timeout time.Duration, progress io.Writer) (*api.PromoteStatus, error) {
	deadline := time.Now().Add(timeout)
	lastLabel := ""
	for {
		st, err := client.GetPromoteStatus(betaKey, run.WorkflowID, run.RunID)
		if err != nil && !errors.Is(err, api.ErrNotFound) {
			return nil, fmt.Errorf("查询发布进度失败: %w", err)
		}
		label := "run not visible yet"
		if err == nil {
			label = st.State
			if st.Step != "" {
				label += " / " + st.Step
			}
		}
		if label != lastLabel {
			_, _ = fmt.Fprintf(progress, "  %s\n", label)
			lastLabel = label
		}
		if err == nil && st.Finished() {
			return st, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w（%s）：最后状态 %s，发布可能仍在进行，可稍后 makecli app promote --status 查询", errWaitTimeout, timeout, lastLabel)
		}
		time.Sleep(buildPollInterval)
	}
}

// promoteStatusView 是发布进度的呈现视图：app 定位 + PromoteStatus 平铺 + 成功后的 production URL（omitempty）
type promoteStatusView struct {
	App     string `json:"app"`
	BetaApp string `json:"betaApp"`
	*api.PromoteStatus
	URL string `json:"url,omitempty"`
}

// renderPromoteResult 按输出格式呈现发布进度：table 平铺渲染，json 输出平铺的视图对象。
func renderPromoteResult(target *promoteTarget, st *api.PromoteStatus, url, output string) error {
	if output == outputJSON {
		return writeJSON(promoteStatusView{App: target.appKey, BetaApp: target.betaKey, PromoteStatus: st, URL: url})
	}
	renderPromoteStatus(target, st, url)
	return nil
}

// renderPromoteStatus 平铺渲染发布进度（沿用 deploy 的 %-12s key-value 约定）。
// 可选字段无值不渲染行；步骤列表逐行缩进，state 定宽对齐便于扫读。
func renderPromoteStatus(target *promoteTarget, st *api.PromoteStatus, url string) {
	rows := []struct{ label, value string }{
		{"App:", target.appKey},
		{"Beta app:", target.betaKey},
		{"Run:", st.RunID},
		{"Type:", st.Type},
		{"State:", st.State},
		{"Step:", st.Step},
		{"Message:", st.Message},
		{"Beta build:", buildRef(st.SourceBuildTaskID)},
		{"Prod build:", buildRef(st.ProductBuildTaskID)},
	}
	for _, r := range rows {
		if r.value != "" {
			fmt.Printf("%-12s %s\n", r.label, r.value)
		}
	}
	if len(st.Steps) > 0 {
		fmt.Println("Steps:")
		for _, s := range st.Steps {
			fmt.Printf("  %-10s %s (%s)\n", s.State, s.Name, s.Key)
		}
	}
	if url != "" {
		fmt.Printf("%-12s %s\n", "URL:", url)
	}
}

// buildRef 把构建任务 ID 渲染成 #ID；无值返回空串（该行不渲染）。
func buildRef(id json.Number) string {
	if id == "" {
		return ""
	}
	return "#" + id.String()
}

// ---------------------------------- 本地 run 记录 ----------------------------------

// promoteRunRecord 是落盘的发布回执：查进度必须 workflowId+runId 同传，记录让 --status 免抄 ID。
type promoteRunRecord struct {
	App        string `json:"app"`
	BetaApp    string `json:"betaApp"`
	WorkflowID string `json:"workflowId"`
	RunID      string `json:"runId"`
	StartedAt  string `json:"startedAt"`
}

// promoteRunPath 返回记录文件路径 <config.Dir>/promote/<context>--<betaKey>.json。
// 按 context 分文件：dev/test/production 后端上的同名 app 是不同的 app，记录不能串。
func promoteRunPath(target *promoteTarget) (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "promote", target.context+"--"+target.betaKey+".json"), nil
}

// savePromoteRun 覆盖写入最近一次发布的回执
func savePromoteRun(target *promoteTarget, run *api.PromoteRun) error {
	path, err := promoteRunPath(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	rec := promoteRunRecord{
		App: target.appKey, BetaApp: target.betaKey,
		WorkflowID: run.WorkflowID, RunID: run.RunID,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// loadPromoteRun 读取最近一次发布的回执；无记录给可操作错误指引先 promote。
func loadPromoteRun(target *promoteTarget) (*promoteRunRecord, error) {
	path, err := promoteRunPath(target)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("app '%s' 在 context %s 下没有发布记录，请先 makecli app promote", target.appKey, target.context)
		}
		return nil, err
	}
	var rec promoteRunRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("发布记录 %s 损坏: %w", path, err)
	}
	if rec.WorkflowID == "" || rec.RunID == "" {
		return nil, fmt.Errorf("发布记录 %s 缺少 workflowId/runId，请重新 makecli app promote", path)
	}
	return &rec, nil
}

// confirmProductionPromote 在发布到 production 前要求 continue/abort 确认（与 deploy 同一护栏）
func confirmProductionPromote(appKey string) error {
	return confirmProductionAction("promote", appKey, "This publishes the beta environment to production.")
}
