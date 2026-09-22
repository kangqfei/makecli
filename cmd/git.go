/**
 * [INPUT]: 依赖 agents（embed gitignore.tmpl）、github.com/go-git/go-git/v5（及 config/object 子包）、bytes、errors、fmt、os、path/filepath、strings、time
 * [OUTPUT]: 对外提供（包内）openRepo / initGitRepo / ensureGitignore / gitSignature / stageAndCommit / assertDeployable / assertClean
 * [POS]: cmd 模块的共享 go-git 原语层——把「与具体命令无关」的 git 操作收口一处，被 app_init（init+gitignore）、
 *        app_create（init+gitignore+initial commit）、deploy（openRepo+assertDeployable）、clone（initGitRepo+assertClean）四处复用，消除 deploy.go 独占 git 逻辑的耦合。
 *        命令专属的 git 约定（部署分支 / 匿名 remote / push / fetch+ff）仍内聚于 deploy.go / clone.go，不下沉到此。
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/qfeius/makecli/agents"
)

// ---------------------------------- 仓库打开 / 初始化 ----------------------------------

// openRepo 打开当前目录所属的 git 仓库；不存在则给可操作错误（不再就地 init）。
// DetectDotGit 让子目录里执行也能向上找到仓库根（对齐 git 命令行的探测行为）。
// 「只开不 init」是 deploy 反转的一部分：deploy 是纯 push，建仓交给 `makecli app init`。
func openRepo() (*git.Repository, error) {
	repo, err := git.PlainOpenWithOptions(".", &git.PlainOpenOptions{DetectDotGit: true})
	if err == nil {
		return repo, nil
	}
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return nil, fmt.Errorf("no git repository found; run `makecli app init` first")
	}
	return nil, fmt.Errorf("打开 git 仓库失败: %w", err)
}

// initGitRepo 在 dir 就地建立 git 仓库；该目录自身已是仓库根则跳过。
// 用 PlainOpen（不 DetectDotGit）——只问「这个目录自身是不是仓库根」，不探测父仓库：
// app 目录应是独立仓库根，即便嵌套在别的 git 仓库里也建自己的 .git。
func initGitRepo(dir string) (created bool, err error) {
	if _, err := git.PlainOpen(dir); err == nil {
		return false, nil
	} else if !errors.Is(err, git.ErrRepositoryNotExists) {
		return false, fmt.Errorf("检查 git 仓库失败: %w", err)
	}
	if _, err := git.PlainInit(dir, false); err != nil {
		return false, fmt.Errorf("git init 失败: %w", err)
	}
	return true, nil
}

// ---------------------------------- .gitignore 增量补齐 ----------------------------------

// ensureGitignore 把 dir/.gitignore 补齐到包含全部期望 ignore 条目。
//   - 文件不存在 → 原样写出 embed 模板全文（含注释分组）。
//   - 已存在 → 仅追加缺失条目（带 `# added by makecli` 标记），保留用户已有自定义行不动。
//
// 期望条目的单一真相源是 agents/gitignore.tmpl，不另起硬编码清单。
func ensureGitignore(dir string) (changed bool, err error) {
	entries, tmpl, err := gitignoreCanonical()
	if err != nil {
		return false, err
	}
	path := filepath.Join(dir, ".gitignore")

	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, tmpl, 0644); err != nil {
			return false, fmt.Errorf("写 .gitignore 失败: %w", err)
		}
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("读 .gitignore 失败: %w", err)
	}

	present := map[string]bool{}
	for _, line := range strings.Split(string(existing), "\n") {
		present[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, e := range entries {
		if !present[e] {
			missing = append(missing, e)
		}
	}
	if len(missing) == 0 {
		return false, nil
	}

	var buf bytes.Buffer
	buf.Write(existing)
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		buf.WriteByte('\n')
	}
	buf.WriteString("\n# added by makecli\n")
	for _, e := range missing {
		buf.WriteString(e)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return false, fmt.Errorf("写 .gitignore 失败: %w", err)
	}
	return true, nil
}

// gitignoreCanonical 返回 embed 模板里的期望 ignore 条目（trim 后非空、非注释行）及模板原文。
func gitignoreCanonical() (entries []string, template []byte, err error) {
	data, err := agents.Templates.ReadFile("gitignore.tmpl")
	if err != nil {
		return nil, nil, fmt.Errorf("read embedded gitignore: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		entries = append(entries, t)
	}
	return entries, data, nil
}

// ---------------------------------- 提交 / 部署门控 ----------------------------------

// stageAndCommit 暂存全部改动并在有变更时提交，返回是否真的产生了提交。
// AddWithOptions{All} 等价 git add -A（含新增/删除，且尊重 .gitignore）；
// 工作树干净时返回 (false, nil)，不造空提交。
func stageAndCommit(repo *git.Repository, msg string) (committed bool, err error) {
	w, err := repo.Worktree()
	if err != nil {
		return false, fmt.Errorf("读取工作树失败: %w", err)
	}
	if err := w.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return false, fmt.Errorf("暂存改动失败: %w", err)
	}
	status, err := w.Status()
	if err != nil {
		return false, fmt.Errorf("读取工作树状态失败: %w", err)
	}
	if status.IsClean() {
		return false, nil
	}
	if _, err := w.Commit(msg, &git.CommitOptions{Author: gitSignature(repo)}); err != nil {
		return false, fmt.Errorf("提交失败: %w", err)
	}
	return true, nil
}

// assertDeployable 校验仓库可被纯 push 部署：必须有可推送的提交，且工作树干净。
// 这是 deploy 反转的核心——把提交时机交还用户，deploy 只推已提交状态。
func assertDeployable(repo *git.Repository) error {
	if _, err := repo.Head(); err != nil {
		return fmt.Errorf("nothing committed yet; commit before deploy:\n  git add -A && git commit -m \"your message\"")
	}
	return assertClean(repo, "commit before deploy:\n%s  git add -A && git commit -m \"your message\"")
}

// assertClean 校验工作树干净（无未暂存/未提交/未跟踪改动），HEAD 可以不存在（空仓库也算干净）。
// hint 是脏树时给用户的指引，含一个 %s 占位接收 status 文本——deploy 要求先 commit，clone 要求先 commit 或清理，
// 门控本身同一条，只有指引不同。
func assertClean(repo *git.Repository, hint string) error {
	w, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("读取工作树失败: %w", err)
	}
	status, err := w.Status()
	if err != nil {
		return fmt.Errorf("读取工作树状态失败: %w", err)
	}
	if !status.IsClean() {
		return fmt.Errorf("working tree has uncommitted changes; "+hint, status.String())
	}
	return nil
}

// gitSignature 解析提交署名：优先用户 git 配置(user.name/email)，缺失则回退 makecli 身份。
// 用 LocalScope——go-git 据此返回 system+global+local 合并视图，等价 `git commit` 看到的身份链
// （含用户全局 ~/.gitconfig）；deploy/create 不该因用户没配 git 身份就失败。
func gitSignature(repo *git.Repository) *object.Signature {
	name, email := "makecli", "makecli@make.local"
	if cfg, err := repo.ConfigScoped(config.LocalScope); err == nil {
		if cfg.User.Name != "" {
			name = cfg.User.Name
		}
		if cfg.User.Email != "" {
			email = cfg.User.Email
		}
	}
	return &object.Signature{Name: name, Email: email, When: time.Now()}
}
