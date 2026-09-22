/**
 * [INPUT]: 依赖 cmd 包内的 newCloneCmd / runClone / cloneDeployBranch / gitCloneFunc / cloneTrackingRef / pushCurrentHead / initGitRepo / stageAndCommit（包内白盒）、deploy_test 的 enterAppDir / gitCommitAll / newBareRemote / newMockRepoServer / newAppExistsMeta / noNetRepoServer / stubMetaServer、app_create_test 的 chdir / writeTestFile / saveDefaultToken / newMockMeta、stdout_test 的 captureStdout，errors、fmt、os、path/filepath、strings、testing、github.com/go-git/go-git/v5（及 plumbing 子包）
 * [OUTPUT]: 覆盖 clone 子命令的单元测试（Hidden 不进 help；runClone 编排：必填 appKey 拉 beta 配对仓库到 ./<appKey> 并自动建仓、无参或多参拒绝、非法 appKey 拒绝且不建目录、目录已属别的 app 拒绝不触网、脏树 fail-fast 不触网、非空非仓库目录在触网前拒绝且不 init、app 未注册指引 app create 且不留目录残留、无凭证不拉取；cloneDeployBranch 真 go-git：空仓库首次检出等价 clone 且落 main 分支、重复拉取 up-to-date、远端新提交快进、本地领先视为最新不动、分叉报错不动工作树、远端空仓库指引 deploy）
 * [POS]: cmd 模块 clone.go 的配套测试，与 deploy_test 共用夹具：httptest 隔离网络、gitCloneFunc 打桩隔离拉取、newBareRemote 本地裸仓库经 pushCurrentHead 灌入远端状态后跑真实 fetch/ff
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// cloneCall 打桩 gitCloneFunc：记录 runClone 传入的拉取参数，按 res/err 返回。
type cloneCall struct {
	cloneURL string
	token    string
	called   bool
	res      cloneResult
	err      error
}

func (p *cloneCall) install(t *testing.T) {
	t.Helper()
	old := gitCloneFunc
	gitCloneFunc = func(_ *git.Repository, cloneURL, token string) (cloneResult, error) {
		p.called = true
		p.cloneURL, p.token = cloneURL, token
		return p.res, p.err
	}
	t.Cleanup(func() { gitCloneFunc = old })
}

func TestCloneCommandHiddenFromHelp(t *testing.T) {
	app := newAppCmd()
	clone, _, err := app.Find([]string{"clone"})
	if err != nil || clone == app || clone.Name() != "clone" {
		t.Fatalf("app clone not registered: command=%v, err=%v", clone, err)
	}
	if !clone.Hidden {
		t.Fatal("app clone 应为 Hidden")
	}
}

func TestCloneCommandRequiresAppKey(t *testing.T) {
	for _, args := range [][]string{nil, {"shop", "extra"}} {
		cmd := newCloneCmd()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("args %v must be rejected before cloning", args)
		}
	}
	cmd := newCloneCmd()
	if err := cmd.ValidateArgs([]string{"shop"}); err != nil {
		t.Fatalf("one appKey must be accepted: %v", err)
	}
}

// prepareCloneTarget 创建已有 app 仓库，调用后停留在其父目录。
func prepareCloneTarget(t *testing.T, key string) {
	t.Helper()
	parent := t.TempDir()
	dir := filepath.Join(parent, key)
	dslDir := filepath.Join(dir, "apps", "dsl")
	if err := os.MkdirAll(dslDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf("key: %s\nname: %s\ntype: Make.App\nmeta:\n  version: 1.0.0\nproperties: {}\n", key, key)
	writeTestFile(t, filepath.Join(dslDir, "app.yaml"), []byte(manifest))
	chdir(t, dir)
	gitCommitAll(t)
	chdir(t, parent)
}

// ---------------------------------- runClone 编排（真仓库门控 + 拉取桩） ----------------------------------

func TestRunClone(t *testing.T) {
	t.Run("clones beta repo of the explicit appKey", func(t *testing.T) {
		prepareCloneTarget(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		stubMetaServer(t, newAppExistsMeta(t).URL)
		t.Setenv(EnvRepoServerURL, newMockRepoServer(t).URL)
		p := &cloneCall{res: cloneResult{from: plumbing.NewHash("a"), to: plumbing.NewHash("b"), updated: true}}
		p.install(t)

		out := captureStdout(t, func() {
			if err := runClone("myapp"); err != nil {
				t.Errorf("runClone: %v", err)
			}
		})

		if p.cloneURL != "https://repo.example/org/myapp_beta_.git" {
			t.Errorf("clone url = %q, want the beta pair app repo", p.cloneURL)
		}
		if p.token == "" {
			t.Error("token should not be empty")
		}
		if !strings.Contains(out, "Cloned 'myapp' from beta") {
			t.Errorf("output missing success line: %q", out)
		}
	})

	t.Run("reports up to date without a cloned line", func(t *testing.T) {
		prepareCloneTarget(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		stubMetaServer(t, newAppExistsMeta(t).URL)
		t.Setenv(EnvRepoServerURL, newMockRepoServer(t).URL)
		p := &cloneCall{res: cloneResult{to: plumbing.NewHash("b")}}
		p.install(t)

		out := captureStdout(t, func() {
			if err := runClone("myapp"); err != nil {
				t.Errorf("runClone: %v", err)
			}
		})

		if !strings.Contains(out, "Already up to date") || strings.Contains(out, "Cloned") {
			t.Errorf("output = %q, want up-to-date line only", out)
		}
	})

	t.Run("appKey argument clones into ./<appKey> and inits git", func(t *testing.T) {
		chdir(t, t.TempDir())
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		stubMetaServer(t, newAppExistsMeta(t).URL)
		t.Setenv(EnvRepoServerURL, newMockRepoServer(t).URL)
		p := &cloneCall{res: cloneResult{to: plumbing.NewHash("b"), updated: true}}
		p.install(t)

		out := captureStdout(t, func() {
			if err := runClone("shop"); err != nil { // ./shop 尚不存在
				t.Errorf("runClone: %v", err)
			}
		})

		if !p.called {
			t.Error("expected clone to be called")
		}
		if _, err := git.PlainOpen("shop"); err != nil {
			t.Errorf("shop should be a git repository after clone: %v", err)
		}
		if !strings.Contains(out, "Cloned 'shop' from beta") {
			t.Errorf("output missing success line: %q", out)
		}
	})

	t.Run("fails fast when working tree is dirty", func(t *testing.T) {
		prepareCloneTarget(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		writeTestFile(t, "myapp/uncommitted.txt", []byte("dirty"))
		t.Setenv(EnvRepoServerURL, noNetRepoServer(t).URL)
		p := &cloneCall{}
		p.install(t)

		err := runClone("myapp")
		if err == nil {
			t.Fatal("expected error when working tree is dirty")
		}
		if !strings.Contains(err.Error(), "uncommitted") {
			t.Errorf("error should mention uncommitted changes, got: %v", err)
		}
		if p.called {
			t.Error("clone must not run with a dirty tree")
		}
	})

	t.Run("unregistered app leaves no directory behind", func(t *testing.T) {
		chdir(t, t.TempDir())
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		meta := newMockMeta(t, 200, "ok") // data 为空 → GetApp 返回 ErrNotFound
		t.Cleanup(meta.Close)
		stubMetaServer(t, meta.URL)
		t.Setenv(EnvRepoServerURL, noNetRepoServer(t).URL)
		p := &cloneCall{}
		p.install(t)

		if err := runClone("shop"); err == nil {
			t.Fatal("expected error when app is not registered")
		}
		if _, err := os.Stat("shop"); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("shop must not be created when remote resolution fails (stat err = %v)", err)
		}
		if p.called {
			t.Error("clone must not run for an unregistered app")
		}
	})

	t.Run("rejects non-empty directory that is not a repository before any network", func(t *testing.T) {
		chdir(t, t.TempDir())
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		if err := os.MkdirAll("shop", 0755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join("shop", "note.txt"), []byte("stuff"))
		t.Setenv(EnvRepoServerURL, noNetRepoServer(t).URL)
		p := &cloneCall{}
		p.install(t)

		err := runClone("shop")
		if err == nil || !strings.Contains(err.Error(), "not empty") {
			t.Fatalf("want not-empty error, got: %v", err)
		}
		if _, err := git.PlainOpen("shop"); err == nil {
			t.Error("shop must not be turned into a repository on rejection")
		}
		if p.called {
			t.Error("clone must not run into a non-empty non-repo directory")
		}
	})

	t.Run("rejects empty appKey even inside an app project", func(t *testing.T) {
		enterAppDir(t, "myapp")
		if err := runClone(""); err == nil {
			t.Fatal("empty appKey must not fall back to app.yaml")
		}
	})

	t.Run("rejects invalid appKey argument before anything", func(t *testing.T) {
		chdir(t, t.TempDir())
		if err := runClone("_bad"); err == nil {
			t.Fatal("expected error for invalid appKey")
		}
		if _, err := os.Stat("_bad"); !errors.Is(err, os.ErrNotExist) {
			t.Error("invalid key must not create a directory")
		}
	})

	t.Run("refuses to clone into a directory owned by another app", func(t *testing.T) {
		chdir(t, t.TempDir())
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		// ./shop 里的 app.yaml 声明自己是 other
		dslDir := filepath.Join("shop", "apps", "dsl")
		if err := os.MkdirAll(dslDir, 0755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(dslDir, "app.yaml"), []byte("key: other\nname: other\ntype: Make.App\nmeta:\n  version: 1.0.0\nproperties: {}\n"))
		t.Setenv(EnvRepoServerURL, noNetRepoServer(t).URL)
		p := &cloneCall{}
		p.install(t)

		err := runClone("shop")
		if err == nil || !strings.Contains(err.Error(), "belongs to app 'other'") {
			t.Fatalf("want ownership error, got: %v", err)
		}
		if p.called {
			t.Error("clone must not run into another app's directory")
		}
	})

	t.Run("fails when app is not registered", func(t *testing.T) {
		prepareCloneTarget(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		meta := newMockMeta(t, 200, "ok") // data 为空 → GetApp 返回 ErrNotFound
		t.Cleanup(meta.Close)
		stubMetaServer(t, meta.URL)
		t.Setenv(EnvRepoServerURL, noNetRepoServer(t).URL)
		p := &cloneCall{}
		p.install(t)

		err := runClone("myapp")
		if err == nil {
			t.Fatal("expected error when app is not registered")
		}
		if !strings.Contains(err.Error(), "app create") {
			t.Errorf("error should guide to `app create`, got: %v", err)
		}
		if p.called {
			t.Error("clone must not run for an unregistered app")
		}
	})

	t.Run("fails without credentials", func(t *testing.T) {
		prepareCloneTarget(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		p := &cloneCall{}
		p.install(t)

		if err := runClone("myapp"); err == nil {
			t.Fatal("expected error for missing credentials")
		}
		if p.called {
			t.Error("clone should not run without credentials")
		}
	})
}

// ---------------------------------- cloneDeployBranch 真实 go-git（本地裸仓库做 remote） ----------------------------------

// seedRemote 在临时工作树里提交 files 并推到一个新裸仓库，返回（裸仓库路径，工作树仓库）——模拟「别处已 deploy」。
func seedRemote(t *testing.T, files map[string]string) (bare string, src *git.Repository) {
	t.Helper()
	work := t.TempDir()
	chdir(t, work)
	t.Setenv("HOME", t.TempDir())
	for name, content := range files {
		writeTestFile(t, filepath.Join(work, name), []byte(content))
	}
	gitCommitAll(t)
	bare = newBareRemote(t)
	src, err := git.PlainOpen(work)
	if err != nil {
		t.Fatal(err)
	}
	if err := pushCurrentHead(src, bare, "", false); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	return bare, src
}

// pushMore 在 src 工作树追加一次提交并推到 bare（远端前进）。
func pushMore(t *testing.T, src *git.Repository, bare, name, content string) {
	t.Helper()
	w, err := src.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(w.Filesystem.Root(), name), []byte(content))
	if _, err := stageAndCommit(src, "more"); err != nil {
		t.Fatal(err)
	}
	if err := pushCurrentHead(src, bare, "", false); err != nil {
		t.Fatalf("push more: %v", err)
	}
}

// freshRepo 在一个空目录 init 仓库（等价 runClone 对空目录的处理），返回仓库与目录。
func freshRepo(t *testing.T) (*git.Repository, string) {
	t.Helper()
	dir := t.TempDir()
	if _, err := initGitRepo(dir); err != nil {
		t.Fatal(err)
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo, dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestCloneDeployBranch(t *testing.T) {
	t.Run("first clone into empty repo checks out main", func(t *testing.T) {
		bare, _ := seedRemote(t, map[string]string{"code.txt": "v1"})
		repo, dir := freshRepo(t)

		res, err := captureResult(t, repo, bare)
		if err != nil {
			t.Fatalf("cloneDeployBranch: %v", err)
		}
		if !res.updated || !res.from.IsZero() {
			t.Errorf("result = %+v, want updated with zero from", res)
		}
		if got := readFile(t, filepath.Join(dir, "code.txt")); got != "v1" {
			t.Errorf("code.txt = %q, want v1", got)
		}
		head, err := repo.Head()
		if err != nil {
			t.Fatal(err)
		}
		if head.Name() != plumbing.NewBranchReferenceName(deployBranch) {
			t.Errorf("HEAD on %s, want %s", head.Name(), deployBranch)
		}
		if head.Hash() != res.to {
			t.Errorf("HEAD %s != cloned %s", head.Hash(), res.to)
		}
		if _, err := repo.Reference(cloneTrackingRef, true); err != nil {
			t.Errorf("tracking ref missing: %v", err)
		}
	})

	t.Run("second clone is up to date", func(t *testing.T) {
		bare, _ := seedRemote(t, map[string]string{"code.txt": "v1"})
		repo, _ := freshRepo(t)
		if _, err := captureResult(t, repo, bare); err != nil {
			t.Fatal(err)
		}

		res, err := captureResult(t, repo, bare)
		if err != nil {
			t.Fatalf("second clone: %v", err)
		}
		if res.updated {
			t.Errorf("second clone should be up to date, got %+v", res)
		}
	})

	t.Run("fast-forwards to new remote commit", func(t *testing.T) {
		bare, src := seedRemote(t, map[string]string{"code.txt": "v1"})
		repo, dir := freshRepo(t)
		first, err := captureResult(t, repo, bare)
		if err != nil {
			t.Fatal(err)
		}
		pushMore(t, src, bare, "code.txt", "v2")

		res, err := captureResult(t, repo, bare)
		if err != nil {
			t.Fatalf("ff clone: %v", err)
		}
		if !res.updated || res.from != first.to {
			t.Errorf("result = %+v, want ff from %s", res, first.to)
		}
		if got := readFile(t, filepath.Join(dir, "code.txt")); got != "v2" {
			t.Errorf("code.txt = %q, want v2", got)
		}
		head, _ := repo.Head()
		if head.Hash() != res.to {
			t.Errorf("HEAD %s != cloned %s", head.Hash(), res.to)
		}
	})

	t.Run("local ahead is up to date and untouched", func(t *testing.T) {
		bare, _ := seedRemote(t, map[string]string{"code.txt": "v1"})
		repo, dir := freshRepo(t)
		if _, err := captureResult(t, repo, bare); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(dir, "local.txt"), []byte("mine"))
		if _, err := stageAndCommit(repo, "local work"); err != nil {
			t.Fatal(err)
		}
		before, _ := repo.Head()

		res, err := captureResult(t, repo, bare)
		if err != nil {
			t.Fatalf("clone with local ahead: %v", err)
		}
		if res.updated {
			t.Errorf("local ahead should be up to date, got %+v", res)
		}
		after, _ := repo.Head()
		if after.Hash() != before.Hash() {
			t.Error("HEAD must not move when local is ahead")
		}
	})

	t.Run("diverged history is rejected without touching the tree", func(t *testing.T) {
		bare, src := seedRemote(t, map[string]string{"code.txt": "v1"})
		repo, dir := freshRepo(t)
		if _, err := captureResult(t, repo, bare); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(dir, "local.txt"), []byte("mine"))
		if _, err := stageAndCommit(repo, "local work"); err != nil {
			t.Fatal(err)
		}
		before, _ := repo.Head()
		pushMore(t, src, bare, "code.txt", "v2")

		_, err := captureResult(t, repo, bare)
		if err == nil || !strings.Contains(err.Error(), "diverged") {
			t.Fatalf("want diverged error, got: %v", err)
		}
		after, _ := repo.Head()
		if after.Hash() != before.Hash() {
			t.Error("HEAD must not move on divergence")
		}
		if got := readFile(t, filepath.Join(dir, "code.txt")); got != "v1" {
			t.Errorf("code.txt = %q, want untouched v1", got)
		}
	})

	t.Run("empty remote guides to deploy", func(t *testing.T) {
		bare := newBareRemote(t)
		repo, _ := freshRepo(t)

		_, err := captureResult(t, repo, bare)
		if err == nil || !strings.Contains(err.Error(), "app deploy") {
			t.Fatalf("want guidance to deploy, got: %v", err)
		}
	})
}

// captureResult 跑真实 cloneDeployBranch 并吞掉其 stdout 进度输出。
func captureResult(t *testing.T, repo *git.Repository, bare string) (res cloneResult, err error) {
	t.Helper()
	_ = captureStdout(t, func() { res, err = cloneDeployBranch(repo, bare, "") })
	return res, err
}
