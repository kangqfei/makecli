/**
 * [INPUT]: 依赖 cmd/deploy（betaRepoURL/appKeyFromManifest/deployBranch/anonymousRemote/envBeta/shortSha）、cmd/app（validResourceKey）、cmd/app_create（fileExists/appDSLPath）、cmd/git（initGitRepo/assertClean）、errors、fmt、os、path/filepath、github.com/go-git/go-git/v5（及 config/plumbing/plumbing/object/plumbing/transport/plumbing/transport/http 子包）、github.com/spf13/cobra
 * [OUTPUT]: 对外提供 newCloneCmd 函数（Hidden：deploy 的反向配套，暂不对普通用户展示）；包内 runClone（目标解析）、runAppSync（clone/pull 共享编排）、assertCloneTarget（只读门控：仓库须干净树 / 非仓库须空或不存在）、prepareCloneRepo（远端定位通过后才 MkdirAll + init）、resolveCloneTarget（必填 appKey 即目录名，目录已属别的 app 则拒绝）、
 *           cloneDeployBranch（fetch + ff-only，返回 cloneResult{from,to,updated}）、fetchDeployBranch（匿名 remote fetch 到 cloneTrackingRef）、fastForward（四态：空仓库 checkout / 已最新 / 快进 / 分叉报错）、
 *           cloneTrackingRef 常量、包级 gitCloneFunc 可打桩变量（测试替换拉取，参照 deploy.go gitPushFunc）
 * [POS]: cmd 模块 app 命令组的 clone 子命令——deploy 的反向：deploy 把本地 HEAD push 到 beta 仓库的 deployBranch，clone 把 beta 仓库的 deployBranch fetch 回本地并快进当前分支。
 *        身份、门控、仓库定位与 deploy 同源（显式 appKey → betaRepoURL：注册门控 + KeyForEnv(beta) + 幂等 CreateRepository → cloneUrl；HTTP BasicAuth make:<token>；匿名 remote 不落 .git/config）。
 *        必填位置参数 <appKey>：把该 app 拉到 ./<appKey>（对齐 app create「key 即目录名」），目录不存在则创建、不是仓库则 initGitRepo——但都在远端定位成功之后，app 不存在/凭证失效时本地零残留。
 *        本地策略是 ff-only（对齐 `git pull --ff-only`）：工作树必须干净（assertClean，HEAD 可无）、本地分叉即报错交用户处理，绝不自动 merge/覆盖——clone 与 deploy 一样只搬运已提交状态。
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/spf13/cobra"
)

// cloneTrackingRef 是 fetch 落地的远端跟踪引用：只存一个 hash，不进 .git/config，
// 顺带让用户能 `git diff make/main` 对照远端部署状态。
var cloneTrackingRef = plumbing.NewRemoteReferenceName("make", deployBranch)

// gitCloneFunc 为包级可打桩变量，单测替换以隔离真实网络拉取（本地仓库门控不打桩，跑真 go-git）
var gitCloneFunc = cloneDeployBranch

// cloneResult 描述一次拉取把当前分支从哪搬到了哪：from 为零值表示空仓库首次检出；updated=false 即已是最新。
type cloneResult struct {
	from, to plumbing.Hash
	updated  bool
}

func newCloneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "clone <appKey>",
		Short:        "Clone the app's beta repository to local",
		Hidden:       true, // deploy 的反向配套，暂不对普通用户展示；稳定后摘除
		Example:      `  makecli app clone shop           # 把 app shop 克隆到 ./shop`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClone(args[0])
		},
	}
	return cmd
}

// runClone 编排「fetch + ff-only」：定位 app 与目录 → 本地只读门控（fail-fast）→ 定位 beta 仓库 → 建目录/仓库 → 拉取快进。
// 门控刻意在网络之前且零副作用：脏工作树不该先白跑一趟仓库准备；建目录与 git init 放在远端定位之后——
// app 不存在 / 凭证失效时本地不留任何残留目录，修好后重跑是干净的。
func runClone(appKeyArg string) error {
	appKey, dir, err := resolveCloneTarget(appKeyArg)
	if err != nil {
		return err
	}
	return runAppSync(appKey, dir, "Cloned")
}

// runAppSync 共用目录门控、远端定位和拉取逻辑；action 只决定成功提示。
func runAppSync(appKey, dir, action string) error {
	if err := assertCloneTarget(dir); err != nil {
		return err
	}

	cloneURL, token, err := betaRepoURL(appKey)
	if err != nil {
		return err
	}

	fmt.Printf("%-12s %s\n", "App:", appKey)
	fmt.Printf("%-12s %s\n", "Environment:", envBeta)

	repo, err := prepareCloneRepo(dir)
	if err != nil {
		return err
	}
	res, err := gitCloneFunc(repo, cloneURL, token)
	if err != nil {
		return err
	}
	if !res.updated {
		fmt.Printf("Already up to date (%s)\n", shortSha(res.to.String()))
		return nil
	}
	fmt.Printf("%s '%s' from %s (%s)\n", action, appKey, envBeta, res.rangeLabel())
	return nil
}

// rangeLabel 渲染 from -> to 的短 sha 区间；首次检出无 from，只给 to。
func (r cloneResult) rangeLabel() string {
	if r.from.IsZero() {
		return shortSha(r.to.String())
	}
	return shortSha(r.from.String()) + " -> " + shortSha(r.to.String())
}

// assertCloneTarget 只读检查目标目录可被拉取（不建目录、不 init）：
//   - 已是 git 仓库 → 工作树须干净（与 deploy 同一门控，HEAD 可无）；
//   - 不是仓库 → 须不存在或为空目录（init 之后天然是干净树；非空目录 init 后必是脏树，直接在此拒绝）。
func assertCloneTarget(dir string) error {
	repo, err := git.PlainOpen(dir)
	if err == nil {
		return assertClean(repo, "commit or discard them before syncing:\n%s")
	}
	if !errors.Is(err, git.ErrRepositoryNotExists) {
		return fmt.Errorf("检查 git 仓库失败: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取目录失败: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("directory '%s' is not empty and not a git repository; clone into an empty directory or an existing app repository", dir)
	}
	return nil
}

// prepareCloneRepo 在 dir 建目录 + git init（均幂等）并打开仓库；assertCloneTarget 已保证这里得到的是干净树。
func prepareCloneRepo(dir string) (*git.Repository, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建目录失败: %w", err)
	}
	if _, err := initGitRepo(dir); err != nil {
		return nil, err
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return nil, fmt.Errorf("打开 git 仓库失败: %w", err)
	}
	return repo, nil
}

// resolveCloneTarget 校验必填 appKey，以 ./<appKey> 为目标目录。
// 目录里已有 app.yaml 且 key 不同即拒绝，避免把 app A 拉进 app B 的工程。
func resolveCloneTarget(appKeyArg string) (appKey, dir string, err error) {
	if err := validResourceKey(appKeyArg); err != nil {
		return "", "", err
	}
	manifest := filepath.Join(appKeyArg, appDSLPath)
	if fileExists(manifest) {
		existing, err := appKeyFromManifest(manifest)
		if err != nil {
			return "", "", err
		}
		if existing != appKeyArg {
			return "", "", fmt.Errorf("directory '%s' belongs to app '%s' (%s); refusing to clone '%s' into it", appKeyArg, existing, manifest, appKeyArg)
		}
	}
	return appKeyArg, appKeyArg, nil
}

// cloneDeployBranch 把远端 deployBranch 拉回本地并快进当前分支（ff-only）。
func cloneDeployBranch(repo *git.Repository, cloneURL, token string) (cloneResult, error) {
	fmt.Printf("Fetching %s ...\n", deployBranch)
	target, err := fetchDeployBranch(repo, cloneURL, token)
	if err != nil {
		return cloneResult{}, err
	}
	return fastForward(repo, target)
}

// fetchDeployBranch 经匿名 remote 把远端 deployBranch 取到 cloneTrackingRef，返回其指向的提交。
// up-to-date 不当错误；远端空仓库或尚无 deployBranch 翻译成「先 deploy」的可操作指引。
func fetchDeployBranch(repo *git.Repository, cloneURL, token string) (plumbing.Hash, error) {
	remote, err := repo.CreateRemoteAnonymous(&config.RemoteConfig{
		Name: anonymousRemote,
		URLs: []string{cloneURL},
	})
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("准备拉取来源失败: %w", err)
	}
	refspec := config.RefSpec(fmt.Sprintf("+refs/heads/%s:%s", deployBranch, cloneTrackingRef))
	err = remote.Fetch(&git.FetchOptions{
		RemoteName: anonymousRemote,
		RefSpecs:   []config.RefSpec{refspec},
		Auth:       &http.BasicAuth{Username: "make", Password: token},
		Progress:   os.Stdout,
	})
	switch {
	case errors.Is(err, transport.ErrEmptyRemoteRepository), errors.Is(err, git.NoMatchingRefSpecError{}):
		return plumbing.ZeroHash, fmt.Errorf("remote has no '%s' branch yet: nothing deployed; run `makecli app deploy` first", deployBranch)
	case err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate):
		return plumbing.ZeroHash, fmt.Errorf("git fetch 失败: %w", err)
	}
	ref, err := repo.Reference(cloneTrackingRef, true)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("读取远端跟踪引用失败: %w", err)
	}
	return ref.Hash(), nil
}

// fastForward 把当前分支快进到 target（工作树已由 assertClean 保证干净）：
//   - 无 HEAD（空仓库）→ 在 target 上建 deployBranch 分支并检出，等价 clone；
//   - target 已在 HEAD 历史里（相等或本地领先）→ 已最新，不动；
//   - HEAD 在 target 历史里 → 硬重置到 target，干净树上等价 ff；
//   - 两边分叉 → 报错交用户处理，不自动 merge。
func fastForward(repo *git.Repository, target plumbing.Hash) (cloneResult, error) {
	w, err := repo.Worktree()
	if err != nil {
		return cloneResult{}, fmt.Errorf("读取工作树失败: %w", err)
	}
	head, err := repo.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		err := w.Checkout(&git.CheckoutOptions{
			Hash:   target,
			Branch: plumbing.NewBranchReferenceName(deployBranch),
			Create: true,
		})
		if err != nil {
			return cloneResult{}, fmt.Errorf("检出失败: %w", err)
		}
		return cloneResult{to: target, updated: true}, nil
	}
	if err != nil {
		return cloneResult{}, fmt.Errorf("读取 HEAD 失败: %w", err)
	}

	headCommit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return cloneResult{}, fmt.Errorf("读取 HEAD 提交失败: %w", err)
	}
	targetCommit, err := repo.CommitObject(target)
	if err != nil {
		return cloneResult{}, fmt.Errorf("读取远端提交失败: %w", err)
	}
	if reached, err := ancestorOf(targetCommit, headCommit); err != nil || reached {
		return cloneResult{from: head.Hash(), to: head.Hash()}, err
	}
	reached, err := ancestorOf(headCommit, targetCommit)
	if err != nil {
		return cloneResult{}, err
	}
	if !reached {
		return cloneResult{}, fmt.Errorf("local %s (%s) and remote %s (%s) have diverged; resolve manually (e.g. git rebase %s) before clone",
			head.Name().Short(), shortSha(head.Hash().String()), deployBranch, shortSha(target.String()), cloneTrackingRef.Short())
	}
	if err := w.Reset(&git.ResetOptions{Commit: target, Mode: git.HardReset}); err != nil {
		return cloneResult{}, fmt.Errorf("快进失败: %w", err)
	}
	return cloneResult{from: head.Hash(), to: target, updated: true}, nil
}

// ancestorOf 判断 a 是否在 b 的历史里（含 a == b），错误统一包装成可读文本。
func ancestorOf(a, b *object.Commit) (bool, error) {
	ok, err := a.IsAncestor(b)
	if err != nil {
		return false, fmt.Errorf("比对提交历史失败: %w", err)
	}
	return ok, nil
}
