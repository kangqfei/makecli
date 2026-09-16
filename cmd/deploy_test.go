/**
 * [INPUT]: 依赖 cmd 包内的 runDeploy / runDeployStatus / pushCurrentHead / gitPushFunc / buildPollInterval / errBuildFailed / errWaitTimeout / initGitRepo / stageAndCommit（包内白盒）、enterAppDir(写 apps/dsl/app.yaml + chdir)、gitCommitAll(init+commit 当前目录)，encoding/json、errors、fmt、net/http、net/http/httptest、os、path/filepath、strings、testing、time、github.com/go-git/go-git/v5（及 plumbing/object 子包）
 * [OUTPUT]: 覆盖 deploy 子命令核心逻辑的单元测试（runDeploy 编排：本地真仓库门控 + Meta 注册门控（GetApp）+ production 确认门控 + gitPushFunc 桩隔离推送；app 未注册/校验错误/production abort 均短路在触达仓库服务之前且不 push；--yes 与 preview 不触发确认；非交互真门控拒绝 production 并指引 --yes；默认 env=preview；pushCurrentHead 真 go-git 推到本地裸仓库；fail-fast 脏/无仓库/无提交报错且不触网；--wait 等待：轮询至 SUCCESS/FAILED/CANCELED 终态、跃迁行去重、not-found 窗口期容忍、errBuildFailed/errWaitTimeout 哨兵、查询错误即刻失败、json 模式 stdout 纯 JSON 进度走 stderr；成功带出环境 URL：preview/production 按 task.Environment 选址、失败不渲染 URL、总览失败降级为空不阻断、json 平铺 url 字段、快照同路径；--output json 快照；旗标组合校验先于门控；默认 auto 无 --status 时落回 table 不报错）
 * [POS]: cmd 模块 deploy.go 的配套测试，用 httptest 隔离网络（newAppExistsMeta 放行注册门控、newBuildSeqMeta 按序答复构建快照模拟状态推进且兼答注册门控与部署总览 URL 夹具（previewURLFixture/productionURLFixture）、stubMetaServer 临时指向 Meta、newMockRepoServer 答仓库地址、noNetRepoServer 证短路不触网）、gitPushFunc 打桩隔离推送、stubPollInterval 调小轮询间隔、stubConfirmDeploy 打桩 confirmDeployFunc 隔离终端确认、临时裸仓库做本地 remote 验证真实 go-git 行为
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/qfeius/makecli/internal/api"
)

// pushCall 打桩 gitPushFunc：记录 runDeploy 传入的推送参数，按 err 返回。
type pushCall struct {
	cloneURL string
	token    string
	force    bool
	called   bool
	err      error
}

func (p *pushCall) install(t *testing.T) {
	t.Helper()
	old := gitPushFunc
	gitPushFunc = func(_ *git.Repository, cloneURL, token string, force bool) error {
		p.called = true
		p.cloneURL, p.token, p.force = cloneURL, token, force
		return p.err
	}
	t.Cleanup(func() { gitPushFunc = old })
}

// enterAppDir 切到一个含 apps/dsl/app.yaml（key=<key>）的临时工程根目录。
// deploy 的 app 身份取自 DSL 文件而非目录名——临时目录名是随机的，证明 key 来自 app.yaml。
func enterAppDir(t *testing.T, key string) {
	t.Helper()
	dir := t.TempDir()
	chdir(t, dir)
	dslDir := filepath.Join(dir, "apps", "dsl")
	if err := os.MkdirAll(dslDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("key: %s\nname: %s\ntype: Make.App\nmeta:\n  version: 1.0.0\nproperties: {}\n", key, key)
	writeTestFile(t, filepath.Join(dslDir, "app.yaml"), []byte(content))
}

// gitCommitAll 在 cwd 就地 init 仓库并提交全部文件，留下一个有 HEAD、工作树干净的可部署仓库。
func gitCommitAll(t *testing.T) {
	t.Helper()
	if _, err := initGitRepo("."); err != nil {
		t.Fatal(err)
	}
	repo, err := git.PlainOpen(".")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stageAndCommit(repo, "test commit"); err != nil {
		t.Fatal(err)
	}
}

// newMockRepoServer 启动返回双环境仓库响应的代码仓库服务 mock
func newMockRepoServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200, "msg": "repositories are ready",
			"data": map[string]any{
				"appKey": "myapp", "type": "Make.Code.Repository",
				"properties": map[string]any{
					"env": map[string]any{
						"preview":    map[string]any{"repository": map[string]any{"cloneUrl": "https://repo.example/org/myapp-preview.git"}},
						"production": map[string]any{"repository": map[string]any{"cloneUrl": "https://repo.example/org/myapp-production.git"}},
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newAppExistsMeta 启动一个把 GetResource 都答成「app 已注册」的 Meta Server mock
// （data.key 非空 → GetApp 不返回 ErrNotFound），放行 deploy 的注册门控。
func newAppExistsMeta(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200, "msg": "ok",
			"data": map[string]any{"key": "app", "name": "app", "type": "Make.App"},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stubMetaServer 测试期间把 Meta Server 指向给定 URL，结束自动还原
func stubMetaServer(t *testing.T, url string) {
	t.Helper()
	old := MetaServerURL
	MetaServerURL = url
	t.Cleanup(func() { MetaServerURL = old })
}

// stubConfirmDeploy 临时替换 confirmDeployFunc，t.Cleanup 自动还原，隔离真实终端交互
func stubConfirmDeploy(t *testing.T, err error) {
	t.Helper()
	orig := confirmDeployFunc
	confirmDeployFunc = func(string) error { return err }
	t.Cleanup(func() { confirmDeployFunc = orig })
}

// noNetRepoServer 启动一个被调用即令测试失败的仓库服务——证明 fail-fast 在网络之前短路。
func noNetRepoServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("repository service must not be called when local git gate fails")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// setupDeployEnv 准备工程目录(app.yaml key=myapp，已 init+commit 干净) + 凭证 + repo server 指向，返回安装好的 push 桩
func setupDeployEnv(t *testing.T) *pushCall {
	t.Helper()
	enterAppDir(t, "myapp")
	t.Setenv("HOME", t.TempDir())
	saveDefaultToken(t)
	gitCommitAll(t)
	stubMetaServer(t, newAppExistsMeta(t).URL)
	RepoServerURL = newMockRepoServer(t).URL
	t.Cleanup(func() { RepoServerURL = "" })
	p := &pushCall{}
	p.install(t)
	return p
}

// ---------------------------------- runDeploy 编排（真仓库门控 + 推送桩） ----------------------------------

func TestRunDeploy(t *testing.T) {
	t.Run("deploys to preview", func(t *testing.T) {
		p := setupDeployEnv(t)

		out := captureStdout(t, func() {
			if err := runDeploy("preview", false, false); err != nil {
				t.Errorf("runDeploy: %v", err)
			}
		})

		if p.cloneURL != "https://repo.example/org/myapp-preview.git" {
			t.Errorf("clone url = %q, want preview repo", p.cloneURL)
		}
		if p.force {
			t.Errorf("force=%v, want false", p.force)
		}
		if p.token == "" {
			t.Error("token should not be empty")
		}
		if !strings.Contains(out, "Deployed 'myapp' to preview") {
			t.Errorf("output missing success line: %q", out)
		}
	})

	t.Run("passes production env and force", func(t *testing.T) {
		p := setupDeployEnv(t)

		_ = captureStdout(t, func() {
			if err := runDeploy("production", true, true); err != nil { // --yes 跳过确认
				t.Errorf("runDeploy: %v", err)
			}
		})

		if p.cloneURL != "https://repo.example/org/myapp-production.git" {
			t.Errorf("clone url = %q, want production repo", p.cloneURL)
		}
		if !p.force {
			t.Errorf("force=%v, want true", p.force)
		}
	})

	t.Run("reads app key from app.yaml", func(t *testing.T) {
		// 工程目录名是随机临时名，部署 key 取自 app.yaml 的 fromdsl
		enterAppDir(t, "fromdsl")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		gitCommitAll(t)
		stubMetaServer(t, newAppExistsMeta(t).URL)
		RepoServerURL = newMockRepoServer(t).URL
		t.Cleanup(func() { RepoServerURL = "" })
		p := &pushCall{}
		p.install(t)

		out := captureStdout(t, func() {
			if err := runDeploy("preview", false, false); err != nil {
				t.Errorf("runDeploy: %v", err)
			}
		})

		if !p.called {
			t.Error("expected push to be called")
		}
		if !strings.Contains(out, "Deployed 'fromdsl' to preview") {
			t.Errorf("expected app key from app.yaml in output, got: %q", out)
		}
	})

	t.Run("rejects invalid env", func(t *testing.T) {
		p := setupDeployEnv(t)

		if err := runDeploy("staging", false, false); err == nil {
			t.Fatal("expected error for invalid env")
		}
		if p.called {
			t.Error("push should not run on invalid env")
		}
	})

	t.Run("fails when app.yaml missing", func(t *testing.T) {
		chdir(t, t.TempDir()) // 干净目录，无 apps/dsl/app.yaml

		if err := runDeploy("preview", false, false); err == nil {
			t.Fatal("expected error when app.yaml is missing")
		}
	})

	t.Run("fails when app.yaml has invalid key", func(t *testing.T) {
		enterAppDir(t, "_bad") // 下划线开头，validResourceKey 拒绝

		if err := runDeploy("preview", false, false); err == nil {
			t.Fatal("expected error for invalid key in app.yaml")
		}
	})

	t.Run("fails fast when no git repository", func(t *testing.T) {
		enterAppDir(t, "myapp") // 未 init
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		RepoServerURL = noNetRepoServer(t).URL
		t.Cleanup(func() { RepoServerURL = "" })
		p := &pushCall{}
		p.install(t)

		err := runDeploy("preview", false, false)
		if err == nil {
			t.Fatal("expected error when no git repository")
		}
		if !strings.Contains(err.Error(), "app init") {
			t.Errorf("error should guide to `app init`, got: %v", err)
		}
		if p.called {
			t.Error("push must not run without a repository")
		}
	})

	t.Run("fails fast when nothing committed", func(t *testing.T) {
		enterAppDir(t, "myapp")
		if _, err := initGitRepo("."); err != nil { // init 但不 commit
			t.Fatal(err)
		}
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		RepoServerURL = noNetRepoServer(t).URL
		t.Cleanup(func() { RepoServerURL = "" })
		p := &pushCall{}
		p.install(t)

		err := runDeploy("preview", false, false)
		if err == nil {
			t.Fatal("expected error when nothing committed")
		}
		if !strings.Contains(err.Error(), "commit") {
			t.Errorf("error should ask to commit, got: %v", err)
		}
		if p.called {
			t.Error("push must not run with no commits")
		}
	})

	t.Run("fails fast when working tree is dirty", func(t *testing.T) {
		enterAppDir(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		gitCommitAll(t)
		writeTestFile(t, "uncommitted.txt", []byte("dirty")) // 提交后再造未跟踪改动
		RepoServerURL = noNetRepoServer(t).URL
		t.Cleanup(func() { RepoServerURL = "" })
		p := &pushCall{}
		p.install(t)

		err := runDeploy("preview", false, false)
		if err == nil {
			t.Fatal("expected error when working tree is dirty")
		}
		if !strings.Contains(err.Error(), "uncommitted") {
			t.Errorf("error should mention uncommitted changes, got: %v", err)
		}
		if p.called {
			t.Error("push must not run with a dirty tree")
		}
	})

	t.Run("fails without credentials", func(t *testing.T) {
		enterAppDir(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		gitCommitAll(t) // 本地门控通过，才走到凭证检查
		p := &pushCall{}
		p.install(t)

		if err := runDeploy("preview", false, false); err == nil {
			t.Fatal("expected error for missing credentials")
		}
		if p.called {
			t.Error("push should not run without credentials")
		}
	})

	t.Run("fails when app is not registered", func(t *testing.T) {
		enterAppDir(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		gitCommitAll(t) // 本地门控通过，才走到 app 注册门控
		meta := newMockMeta(t, 200, "ok")
		t.Cleanup(meta.Close)
		stubMetaServer(t, meta.URL) // data 为空 → GetApp 返回 ErrNotFound
		RepoServerURL = noNetRepoServer(t).URL
		t.Cleanup(func() { RepoServerURL = "" })
		p := &pushCall{}
		p.install(t)

		err := runDeploy("preview", false, false)
		if err == nil {
			t.Fatal("expected error when app is not registered")
		}
		if !strings.Contains(err.Error(), "app create") {
			t.Errorf("error should guide to `app create`, got: %v", err)
		}
		if p.called {
			t.Error("push must not run for an unregistered app")
		}
	})

	t.Run("fails when registration check errors", func(t *testing.T) {
		enterAppDir(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		gitCommitAll(t)
		meta := newMockMeta(t, 500, "meta exploded")
		t.Cleanup(meta.Close)
		stubMetaServer(t, meta.URL) // 非 not-found 错误 → 不放行，且不当作「不存在」
		RepoServerURL = noNetRepoServer(t).URL
		t.Cleanup(func() { RepoServerURL = "" })
		p := &pushCall{}
		p.install(t)

		err := runDeploy("preview", false, false)
		if err == nil {
			t.Fatal("expected error when registration check fails")
		}
		if strings.Contains(err.Error(), "app create") {
			t.Errorf("transport/server error must not be reported as not-registered, got: %v", err)
		}
		if p.called {
			t.Error("push must not run when registration check errors")
		}
	})

	t.Run("fails on repository API error", func(t *testing.T) {
		enterAppDir(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		gitCommitAll(t)
		stubMetaServer(t, newAppExistsMeta(t).URL)
		srv := newMockMeta(t, 500, "repository could not be prepared")
		t.Cleanup(srv.Close)
		RepoServerURL = srv.URL
		t.Cleanup(func() { RepoServerURL = "" })
		(&pushCall{}).install(t)

		if err := runDeploy("preview", false, false); err == nil {
			t.Fatal("expected error on API failure")
		}
	})

	t.Run("fails when env clone url missing", func(t *testing.T) {
		enterAppDir(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		gitCommitAll(t)
		stubMetaServer(t, newAppExistsMeta(t).URL)
		srv := newMockMeta(t, 200, "ok") // data 为空 → 无 cloneUrl
		t.Cleanup(srv.Close)
		RepoServerURL = srv.URL
		t.Cleanup(func() { RepoServerURL = "" })
		(&pushCall{}).install(t)

		if err := runDeploy("preview", false, false); err == nil {
			t.Fatal("expected error when clone url missing")
		}
	})

	t.Run("propagates push error", func(t *testing.T) {
		p := setupDeployEnv(t)
		p.err = errors.New("push rejected")

		var err error
		_ = captureStdout(t, func() { err = runDeploy("preview", false, false) })
		if err == nil {
			t.Fatal("expected push error to propagate")
		}
	})
}

// ---------------------------------- production 部署确认门控 ----------------------------------

func TestRunDeployProductionConfirm(t *testing.T) {
	t.Run("deploys after confirmation succeeds", func(t *testing.T) {
		p := setupDeployEnv(t)
		stubConfirmDeploy(t, nil)

		out := captureStdout(t, func() {
			if err := runDeploy("production", false, false); err != nil {
				t.Errorf("runDeploy: %v", err)
			}
		})
		if p.cloneURL != "https://repo.example/org/myapp-production.git" {
			t.Errorf("clone url = %q, want production repo", p.cloneURL)
		}
		if !strings.Contains(out, "Deployed 'myapp' to production") {
			t.Errorf("output missing success line: %q", out)
		}
	})

	t.Run("abort stops before repo prep and push", func(t *testing.T) {
		enterAppDir(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		gitCommitAll(t)
		stubMetaServer(t, newAppExistsMeta(t).URL)
		RepoServerURL = noNetRepoServer(t).URL // 取消必须在触达仓库服务之前短路
		t.Cleanup(func() { RepoServerURL = "" })
		p := &pushCall{}
		p.install(t)
		sentinel := errors.New("aborted")
		stubConfirmDeploy(t, sentinel)

		err := runDeploy("production", false, false)
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected confirm error, got %v", err)
		}
		if p.called {
			t.Error("push must not run when production deploy is aborted")
		}
	})

	t.Run("--yes skips confirmation entirely", func(t *testing.T) {
		p := setupDeployEnv(t)
		orig := confirmDeployFunc
		confirmDeployFunc = func(string) error {
			t.Error("confirm must not run with --yes")
			return nil
		}
		t.Cleanup(func() { confirmDeployFunc = orig })

		if err := runDeploy("production", false, true); err != nil {
			t.Errorf("runDeploy: %v", err)
		}
		if !p.called {
			t.Error("expected push to run with --yes")
		}
	})

	t.Run("preview never prompts", func(t *testing.T) {
		p := setupDeployEnv(t)
		orig := confirmDeployFunc
		confirmDeployFunc = func(string) error {
			t.Error("confirm must not run for preview")
			return nil
		}
		t.Cleanup(func() { confirmDeployFunc = orig })

		if err := runDeploy("preview", false, false); err != nil {
			t.Errorf("runDeploy: %v", err)
		}
		if !p.called {
			t.Error("expected preview push")
		}
	})

	t.Run("real gate refuses production in non-interactive shell", func(t *testing.T) {
		enterAppDir(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		gitCommitAll(t)
		stubMetaServer(t, newAppExistsMeta(t).URL)
		RepoServerURL = noNetRepoServer(t).URL
		t.Cleanup(func() { RepoServerURL = "" })
		p := &pushCall{}
		p.install(t)
		// 不打桩 confirmDeployFunc，走真 confirmProductionDeploy；go test 下 stdin 非 TTY → 拒绝

		err := runDeploy("production", false, false)
		if err == nil {
			t.Fatal("expected refusal without --yes in non-interactive shell")
		}
		if !strings.Contains(err.Error(), "--yes") {
			t.Errorf("error should guide to --yes, got: %v", err)
		}
		if p.called {
			t.Error("push must not run when production confirm is refused")
		}
	})
}

// TestDeployDefaultsToPreview 走真实 cobra 解析：不传 --env 时 `app deploy` 不报缺参，
// 且默认部署目标是 preview（production 须显式 opt-in）。
func TestDeployDefaultsToPreview(t *testing.T) {
	p := setupDeployEnv(t)

	cmd := newDeployCmd()
	cmd.SetArgs([]string{}) // 不传 --env
	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Errorf("bare `app deploy` should not error: %v", err)
		}
	})

	if p.cloneURL != "https://repo.example/org/myapp-preview.git" {
		t.Errorf("default deploy target = %q, want preview", p.cloneURL)
	}
	if !strings.Contains(out, "Deployed 'myapp' to preview") {
		t.Errorf("output should confirm preview deploy, got: %q", out)
	}
}

// ---------------------------------- pushCurrentHead 真实 go-git（本地裸仓库做 remote） ----------------------------------

func TestPushCurrentHead(t *testing.T) {
	t.Run("pushes committed HEAD to dev branch", func(t *testing.T) {
		work := t.TempDir()
		chdir(t, work)
		t.Setenv("HOME", t.TempDir())
		writeTestFile(t, filepath.Join(work, "code.txt"), []byte("v1"))
		gitCommitAll(t)
		bare := newBareRemote(t)

		repo, err := git.PlainOpen(work)
		if err != nil {
			t.Fatal(err)
		}
		if err := pushCurrentHead(repo, bare, "", false); err != nil {
			t.Fatalf("pushCurrentHead: %v", err)
		}

		tree := devTree(t, bare)
		if _, err := tree.File("code.txt"); err != nil {
			t.Errorf("code.txt not pushed to dev: %v", err)
		}
	})

	t.Run("clean redeploy is an up-to-date no-op", func(t *testing.T) {
		work := t.TempDir()
		chdir(t, work)
		t.Setenv("HOME", t.TempDir())
		writeTestFile(t, filepath.Join(work, "code.txt"), []byte("v1"))
		gitCommitAll(t)
		bare := newBareRemote(t)
		repo, err := git.PlainOpen(work)
		if err != nil {
			t.Fatal(err)
		}

		if err := pushCurrentHead(repo, bare, "", false); err != nil {
			t.Fatalf("first push: %v", err)
		}
		// 无任何新提交，再次推送应成功（远端已是该提交 → up-to-date）
		if err := pushCurrentHead(repo, bare, "", false); err != nil {
			t.Errorf("clean redeploy should succeed, got: %v", err)
		}
	})

	t.Run("push after a new commit updates dev", func(t *testing.T) {
		work := t.TempDir()
		chdir(t, work)
		t.Setenv("HOME", t.TempDir())
		codePath := filepath.Join(work, "code.txt")
		writeTestFile(t, codePath, []byte("v1"))
		gitCommitAll(t)
		bare := newBareRemote(t)
		repo, err := git.PlainOpen(work)
		if err != nil {
			t.Fatal(err)
		}

		if err := pushCurrentHead(repo, bare, "", false); err != nil {
			t.Fatalf("first push: %v", err)
		}
		writeTestFile(t, codePath, []byte("v2")) // 用户改并提交，再推
		if _, err := stageAndCommit(repo, "v2"); err != nil {
			t.Fatal(err)
		}
		if err := pushCurrentHead(repo, bare, "", false); err != nil {
			t.Fatalf("second push: %v", err)
		}

		f, err := devTree(t, bare).File("code.txt")
		if err != nil {
			t.Fatalf("code.txt missing on dev: %v", err)
		}
		content, err := f.Contents()
		if err != nil {
			t.Fatal(err)
		}
		if content != "v2" {
			t.Errorf("dev has %q, want v2", content)
		}
	})
}

// newBareRemote 建一个临时裸仓库作为本地 push 目标（file transport，无需网络）
func newBareRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := git.PlainInit(dir, true); err != nil {
		t.Fatal(err)
	}
	return dir
}

// ---------------------------------- runDeployStatus（--status 构建进度查询） ----------------------------------

// newBuildTaskMeta 启动答复构建任务详情的 Meta mock，透传 data 并记录请求供断言。
func newBuildTaskMeta(t *testing.T, data map[string]any) (srv *httptest.Server, gotPath, gotTarget *string, gotBody *map[string]any) {
	t.Helper()
	gotPath, gotTarget, gotBody = new(string), new(string), new(map[string]any)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotPath = r.URL.Path
		*gotTarget = r.Header.Get("X-Make-Target")
		_ = json.NewDecoder(r.Body).Decode(gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "success", "data": data})
	}))
	t.Cleanup(srv.Close)
	return srv, gotPath, gotTarget, gotBody
}

// headSha 取 cwd 仓库当前 HEAD 的完整 sha
func headSha(t *testing.T) string {
	t.Helper()
	repo, err := git.PlainOpen(".")
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	return head.Hash().String()
}

func TestRunDeployStatus(t *testing.T) {
	t.Run("queries build task by current HEAD sha and renders it", func(t *testing.T) {
		enterAppDir(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		gitCommitAll(t)
		sha := headSha(t)
		srv, gotPath, gotTarget, gotBody := newBuildTaskMeta(t, map[string]any{
			"id": 582, "appKey": "myapp", "environment": "preview",
			"deploymentVersion": "deploy_myapp_20260722_004",
			"commitSha":         sha, "commitMessage": "test commit",
			"status": "RUNNING", "phase": "BUILD",
			"createTime": "2026-07-22T14:35:24+08:00",
			"startTime":  "2026-07-22T14:35:25+08:00",
		})
		stubMetaServer(t, srv.URL)

		out := captureStdout(t, func() {
			if err := runDeployStatus(false, 0, outputTable); err != nil {
				t.Errorf("runDeployStatus: %v", err)
			}
		})

		if *gotPath != "/api/make/build/v1/build" {
			t.Errorf("path = %q, want /api/make/build/v1/build", *gotPath)
		}
		if *gotTarget != "MakeService.GetResource" {
			t.Errorf("X-Make-Target = %q, want MakeService.GetResource", *gotTarget)
		}
		if (*gotBody)["commitSha"] != sha {
			t.Errorf("request commitSha = %v, want HEAD %s", (*gotBody)["commitSha"], sha)
		}
		for _, want := range []string{"App:", "myapp", "Build:", "#582", "Status:", "RUNNING", "Phase:", "BUILD", shortSha(sha)} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q, got: %q", want, out)
			}
		}
		if strings.Contains(out, "Error:") || strings.Contains(out, "Finished:") {
			t.Errorf("empty optional rows must not render, got: %q", out)
		}
	})

	t.Run("reports missing build task with deploy guidance", func(t *testing.T) {
		enterAppDir(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		gitCommitAll(t)
		srv, _, _, _ := newBuildTaskMeta(t, map[string]any{}) // 软空响应 → ErrNotFound
		stubMetaServer(t, srv.URL)

		err := runDeployStatus(false, 0, outputTable)
		if err == nil {
			t.Fatal("expected error when build task is missing")
		}
		if !strings.Contains(err.Error(), "尚无构建任务") || !strings.Contains(err.Error(), "app deploy") {
			t.Errorf("error should guide to deploy, got: %v", err)
		}
	})

	t.Run("fails when app.yaml missing", func(t *testing.T) {
		chdir(t, t.TempDir())

		if err := runDeployStatus(false, 0, outputTable); err == nil {
			t.Fatal("expected error when app.yaml is missing")
		}
	})

	t.Run("fails fast when no git repository", func(t *testing.T) {
		enterAppDir(t, "myapp") // 未 init

		err := runDeployStatus(false, 0, outputTable)
		if err == nil {
			t.Fatal("expected error when no git repository")
		}
		if !strings.Contains(err.Error(), "app init") {
			t.Errorf("error should guide to `app init`, got: %v", err)
		}
	})

	t.Run("fails fast when nothing committed", func(t *testing.T) {
		enterAppDir(t, "myapp")
		if _, err := initGitRepo("."); err != nil { // init 但不 commit
			t.Fatal(err)
		}

		err := runDeployStatus(false, 0, outputTable)
		if err == nil {
			t.Fatal("expected error when nothing committed")
		}
		if !strings.Contains(err.Error(), "commit") {
			t.Errorf("error should ask to commit first, got: %v", err)
		}
	})

	t.Run("fails without credentials", func(t *testing.T) {
		enterAppDir(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		gitCommitAll(t)

		if err := runDeployStatus(false, 0, outputTable); err == nil {
			t.Fatal("expected error for missing credentials")
		}
	})
}

// TestDeployStatusFlagShortCircuitsDeploy 走真实 cobra 解析：--status 只查进度，
// 不碰仓库服务、不推送——查询是只读操作，绝不能顺手触发一次部署。
func TestDeployStatusFlagShortCircuitsDeploy(t *testing.T) {
	enterAppDir(t, "myapp")
	t.Setenv("HOME", t.TempDir())
	saveDefaultToken(t)
	gitCommitAll(t)
	srv, _, _, _ := newBuildTaskMeta(t, map[string]any{
		"id": 7, "appKey": "myapp", "environment": "preview",
		"commitSha": headSha(t), "status": "SUCCESS", "phase": "DEPLOY",
	})
	stubMetaServer(t, srv.URL)
	RepoServerURL = noNetRepoServer(t).URL // --status 不得触达仓库服务
	t.Cleanup(func() { RepoServerURL = "" })
	p := &pushCall{}
	p.install(t)
	stubStdoutTerminal(t, true) // 默认 --output auto：钉成终端视角，本测试断言的是 table 渲染

	cmd := newDeployCmd()
	cmd.SetArgs([]string{"--status"})
	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Errorf("`app deploy --status` should not error: %v", err)
		}
	})

	if p.called {
		t.Error("push must not run with --status")
	}
	if !strings.Contains(out, "Status:") || !strings.Contains(out, "SUCCESS") {
		t.Errorf("output should render build status, got: %q", out)
	}
}

// ---------------------------------- --wait 阻塞等待构建终态 ----------------------------------

// stubPollInterval 把 --wait 轮询间隔调到 1ms，让等待类测试瞬时完成。
func stubPollInterval(t *testing.T) {
	t.Helper()
	old := buildPollInterval
	buildPollInterval = time.Millisecond
	t.Cleanup(func() { buildPollInterval = old })
}

// taskSnap 构造一份最小构建任务快照（--wait 轮询序列的单帧）。
func taskSnap(sha, status, phase string) map[string]any {
	return map[string]any{
		"id": 654, "appKey": "myapp", "environment": "preview",
		"commitSha": sha, "status": status, "phase": phase,
	}
}

// 双环境访问地址夹具（newBuildSeqMeta 的 overview 路由答复，URL 断言与此对照）
const (
	previewURLFixture    = "https://myapp-preview-87.dev-make.example"
	productionURLFixture = "https://myapp-production-87.make.example"
)

// newBuildSeqMeta 启动按序答复构建任务快照的 Meta mock（超出序列后重复最后一帧，模拟状态推进）；
// 部署总览路径答双环境 URL 夹具，其余路径一律答「app 已注册」放行 deploy 的注册门控。
// 返回 build 查询计数指针。
func newBuildSeqMeta(t *testing.T, seq ...map[string]any) (*httptest.Server, *int) {
	t.Helper()
	calls := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/build/v1/build"):
			i := min(*calls, len(seq)-1)
			*calls++
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "success", "data": seq[i]})
		case strings.Contains(r.URL.Path, "/deployment/v1/deployment/overview"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "success",
				"data": map[string]any{
					"appKey":     "myapp",
					"preview":    map[string]any{"status": "Ready", "url": previewURLFixture},
					"production": map[string]any{"status": "Ready", "url": productionURLFixture},
				},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "msg": "ok",
				"data": map[string]any{"key": "myapp", "name": "myapp", "type": "Make.App"},
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func TestRunDeployStatusWait(t *testing.T) {
	// setup 进入可查询的已提交工程并调小轮询间隔，返回 HEAD sha。
	setup := func(t *testing.T) string {
		t.Helper()
		enterAppDir(t, "myapp")
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		gitCommitAll(t)
		stubPollInterval(t)
		return headSha(t)
	}

	t.Run("polls until success and renders final detail", func(t *testing.T) {
		sha := setup(t)
		srv, calls := newBuildSeqMeta(t,
			taskSnap(sha, "RUNNING", "BUILD"),
			taskSnap(sha, "RUNNING", "DEPLOY"),
			taskSnap(sha, "SUCCESS", "DEPLOY"),
		)
		stubMetaServer(t, srv.URL)

		out := captureStdout(t, func() {
			if err := runDeployStatus(true, time.Minute, outputTable); err != nil {
				t.Errorf("runDeployStatus --wait: %v", err)
			}
		})

		if *calls < 3 {
			t.Errorf("expected >=3 polls, got %d", *calls)
		}
		for _, want := range []string{"RUNNING / BUILD", "RUNNING / DEPLOY", "SUCCESS / DEPLOY", "Status:", "SUCCESS", "URL:", previewURLFixture} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q, got: %q", want, out)
			}
		}
	})

	t.Run("production build renders production url", func(t *testing.T) {
		sha := setup(t)
		snap := taskSnap(sha, "SUCCESS", "DEPLOY")
		snap["environment"] = "production"
		srv, _ := newBuildSeqMeta(t, snap)
		stubMetaServer(t, srv.URL)

		out := captureStdout(t, func() {
			if err := runDeployStatus(true, time.Minute, outputTable); err != nil {
				t.Errorf("runDeployStatus --wait: %v", err)
			}
		})
		if !strings.Contains(out, productionURLFixture) {
			t.Errorf("output should carry production url, got: %q", out)
		}
		if strings.Contains(out, previewURLFixture) {
			t.Errorf("preview url must not leak into production build, got: %q", out)
		}
	})

	t.Run("overview failure degrades to no url row", func(t *testing.T) {
		sha := setup(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.URL.Path, "/deployment/v1/deployment/overview") {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "msg": "overview exploded"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "success", "data": taskSnap(sha, "SUCCESS", "DEPLOY")})
		}))
		t.Cleanup(srv.Close)
		stubMetaServer(t, srv.URL)

		out := captureStdout(t, func() {
			if err := runDeployStatus(true, time.Minute, outputTable); err != nil {
				t.Errorf("url is decoration, overview failure must not fail deploy result: %v", err)
			}
		})
		if strings.Contains(out, "URL:") {
			t.Errorf("URL row must not render when overview unavailable, got: %q", out)
		}
	})

	t.Run("progress prints only on state transitions", func(t *testing.T) {
		sha := setup(t)
		srv, _ := newBuildSeqMeta(t,
			taskSnap(sha, "RUNNING", "BUILD"),
			taskSnap(sha, "RUNNING", "BUILD"),
			taskSnap(sha, "RUNNING", "BUILD"),
			taskSnap(sha, "SUCCESS", "DEPLOY"),
		)
		stubMetaServer(t, srv.URL)

		out := captureStdout(t, func() {
			if err := runDeployStatus(true, time.Minute, outputTable); err != nil {
				t.Errorf("runDeployStatus --wait: %v", err)
			}
		})

		if n := strings.Count(out, "RUNNING / BUILD"); n != 1 {
			t.Errorf("transition line printed %d times, want 1: %q", n, out)
		}
	})

	t.Run("tolerates task not yet created", func(t *testing.T) {
		sha := setup(t)
		srv, _ := newBuildSeqMeta(t,
			map[string]any{}, // 软空 → ErrNotFound：push 后 webhook 尚未建任务的窗口期
			taskSnap(sha, "SUCCESS", "DEPLOY"),
		)
		stubMetaServer(t, srv.URL)

		out := captureStdout(t, func() {
			if err := runDeployStatus(true, time.Minute, outputTable); err != nil {
				t.Errorf("not-found during wait must not fail: %v", err)
			}
		})
		if !strings.Contains(out, "not created yet") {
			t.Errorf("output should mention pending task creation, got: %q", out)
		}
	})

	t.Run("failed build returns errBuildFailed with detail", func(t *testing.T) {
		sha := setup(t)
		snap := taskSnap(sha, "FAILED", "BUILD")
		snap["errorCode"], snap["errorMessage"] = "BUILD_FAILED", "npm install failed"
		srv, _ := newBuildSeqMeta(t, snap)
		stubMetaServer(t, srv.URL)

		var err error
		out := captureStdout(t, func() { err = runDeployStatus(true, time.Minute, outputTable) })
		if !errors.Is(err, errBuildFailed) {
			t.Fatalf("expected errBuildFailed, got %v", err)
		}
		if !strings.Contains(out, "[BUILD_FAILED] npm install failed") {
			t.Errorf("final detail should carry error line, got: %q", out)
		}
		if strings.Contains(out, "URL:") {
			t.Errorf("failed build must not render URL (old release still serving), got: %q", out)
		}
	})

	t.Run("canceled build is also not-success", func(t *testing.T) {
		sha := setup(t)
		srv, _ := newBuildSeqMeta(t, taskSnap(sha, "CANCELED", "BUILD"))
		stubMetaServer(t, srv.URL)

		var err error
		_ = captureStdout(t, func() { err = runDeployStatus(true, time.Minute, outputTable) })
		if !errors.Is(err, errBuildFailed) {
			t.Fatalf("expected errBuildFailed for CANCELED, got %v", err)
		}
	})

	t.Run("times out with errWaitTimeout while still running", func(t *testing.T) {
		sha := setup(t)
		srv, _ := newBuildSeqMeta(t, taskSnap(sha, "RUNNING", "BUILD"))
		stubMetaServer(t, srv.URL)

		var err error
		_ = captureStdout(t, func() { err = runDeployStatus(true, 30*time.Millisecond, outputTable) })
		if !errors.Is(err, errWaitTimeout) {
			t.Fatalf("expected errWaitTimeout, got %v", err)
		}
		if !strings.Contains(err.Error(), "--status") {
			t.Errorf("timeout error should guide to --status, got: %v", err)
		}
	})

	t.Run("query error fails wait immediately", func(t *testing.T) {
		setup(t)
		meta := newMockMeta(t, 500, "boom")
		t.Cleanup(meta.Close)
		stubMetaServer(t, meta.URL)

		var err error
		_ = captureStdout(t, func() { err = runDeployStatus(true, time.Minute, outputTable) })
		if err == nil || errors.Is(err, errWaitTimeout) || errors.Is(err, errBuildFailed) {
			t.Fatalf("expected plain query error, got %v", err)
		}
	})

	t.Run("json wait keeps stdout pure JSON with progress on stderr", func(t *testing.T) {
		sha := setup(t)
		srv, _ := newBuildSeqMeta(t,
			taskSnap(sha, "RUNNING", "BUILD"),
			taskSnap(sha, "SUCCESS", "DEPLOY"),
		)
		stubMetaServer(t, srv.URL)

		var err error
		var stdout string
		stderr := captureStderr(t, func() {
			stdout = captureStdout(t, func() { err = runDeployStatus(true, time.Minute, outputJSON) })
		})
		if err != nil {
			t.Fatalf("runDeployStatus --wait --output json: %v", err)
		}
		var view struct {
			api.BuildTask
			URL string `json:"url"`
		}
		if uerr := json.Unmarshal([]byte(stdout), &view); uerr != nil {
			t.Fatalf("stdout is not pure JSON: %v\nstdout: %q", uerr, stdout)
		}
		if view.ID != 654 || view.Status != "SUCCESS" {
			t.Errorf("unexpected task in JSON: %+v", view)
		}
		if view.URL != previewURLFixture {
			t.Errorf("json url = %q, want %q", view.URL, previewURLFixture)
		}
		if !strings.Contains(stderr, "RUNNING / BUILD") {
			t.Errorf("progress should stream to stderr, got: %q", stderr)
		}
	})
}

// TestDeployFlagValidation 走真实 cobra 解析：非法/无意义的旗标组合在触达任何门控之前被拒绝
// （测试目录无工程文件，校验若未先行会先撞上 app.yaml 缺失的错误文案）。
func TestDeployFlagValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"output json requires status", []string{"--output", "json"}, "--status"},
		{"timeout requires wait", []string{"--timeout", "10s"}, "--wait"},
		{"non-positive timeout rejected", []string{"--wait", "--timeout", "0s"}, "positive"},
		{"unsupported output format", []string{"--status", "--output", "yaml"}, "unsupported output format"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chdir(t, t.TempDir())
			cmd := newDeployCmd()
			cmd.SetArgs(c.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("args %v: want error containing %q, got %v", c.args, c.want, err)
			}
		})
	}
}

// TestDeployAutoOutputWithoutStatusFallsBackToTable：默认 --output auto 在管道下解析为 json，
// 但无 --status 时 json 不合法——auto 必须静默落回 table 而非把 agent 挡在「需要 --status」上。
// 测试目录无工程文件：校验放行后撞上的是 app.yaml 缺失，正是「没被 --status 拦住」的证据。
func TestDeployAutoOutputWithoutStatusFallsBackToTable(t *testing.T) {
	stubStdoutTerminal(t, false)
	chdir(t, t.TempDir())
	cmd := newDeployCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil || strings.Contains(err.Error(), "--status") {
		t.Fatalf("auto output must not demand --status, got %v", err)
	}
}

// TestDeployWaitAfterPush 走真实 cobra：--wait 先推送、后阻塞至终态——一次调用 = 一个部署结果。
func TestDeployWaitAfterPush(t *testing.T) {
	p := setupDeployEnv(t)
	stubPollInterval(t)
	sha := headSha(t)
	srv, _ := newBuildSeqMeta(t,
		taskSnap(sha, "PENDING", ""),
		taskSnap(sha, "RUNNING", "BUILD"),
		taskSnap(sha, "SUCCESS", "DEPLOY"),
	)
	stubMetaServer(t, srv.URL) // 覆盖 setupDeployEnv 的 app-exists mock（本 mock 兼答注册门控）

	cmd := newDeployCmd()
	cmd.SetArgs([]string{"--wait"})
	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Errorf("deploy --wait: %v", err)
		}
	})

	if !p.called {
		t.Error("--wait must still push first")
	}
	for _, want := range []string{"Deployed 'myapp' to preview", "Waiting for build", "SUCCESS / DEPLOY", "Status:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got: %q", want, out)
		}
	}
}

// TestDeployStatusJSONSnapshot 快照模式 --output json：stdout 是单个可解析的 BuildTask 对象。
func TestDeployStatusJSONSnapshot(t *testing.T) {
	enterAppDir(t, "myapp")
	t.Setenv("HOME", t.TempDir())
	saveDefaultToken(t)
	gitCommitAll(t)
	srv, _, _, _ := newBuildTaskMeta(t, map[string]any{
		"id": 582, "appKey": "myapp", "environment": "preview",
		"commitSha": headSha(t), "status": "RUNNING", "phase": "BUILD",
	})
	stubMetaServer(t, srv.URL)

	out := captureStdout(t, func() {
		if err := runDeployStatus(false, 0, outputJSON); err != nil {
			t.Errorf("runDeployStatus json: %v", err)
		}
	})

	var task api.BuildTask
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatalf("stdout is not pure JSON: %v\nstdout: %q", err, out)
	}
	if task.ID != 582 || task.Status != "RUNNING" || task.Phase != "BUILD" {
		t.Errorf("unexpected task in JSON: %+v", task)
	}
}

// TestDeployStatusSnapshotURL 快照模式下成功任务同样带出对应环境 URL（与 --wait 同一渲染路径）。
func TestDeployStatusSnapshotURL(t *testing.T) {
	enterAppDir(t, "myapp")
	t.Setenv("HOME", t.TempDir())
	saveDefaultToken(t)
	gitCommitAll(t)
	srv, _ := newBuildSeqMeta(t, taskSnap(headSha(t), "SUCCESS", "DEPLOY"))
	stubMetaServer(t, srv.URL)

	out := captureStdout(t, func() {
		if err := runDeployStatus(false, 0, outputTable); err != nil {
			t.Errorf("runDeployStatus: %v", err)
		}
	})
	if !strings.Contains(out, "URL:") || !strings.Contains(out, previewURLFixture) {
		t.Errorf("snapshot of finished build should carry url, got: %q", out)
	}
}

// TestRenderBuildStatus 直测渲染：失败任务出 Error 行、空可选字段不渲染、URL 行随值出现。
func TestRenderBuildStatus(t *testing.T) {
	t.Run("failed task renders error line", func(t *testing.T) {
		out := captureStdout(t, func() {
			renderBuildStatus(&api.BuildTask{
				ID: 7, AppKey: "myapp", Environment: "preview",
				CommitSha: "abc1234def", Status: "FAILED", Phase: "BUILD",
				ErrorCode: "BUILD_FAILED", ErrorMessage: "npm install failed",
			}, "")
		})
		if !strings.Contains(out, "[BUILD_FAILED] npm install failed") {
			t.Errorf("output missing error detail, got: %q", out)
		}
		if strings.Contains(out, "Image:") || strings.Contains(out, "Version:") || strings.Contains(out, "URL:") {
			t.Errorf("empty optional rows must not render, got: %q", out)
		}
	})

	t.Run("url renders as trailing row when present", func(t *testing.T) {
		out := captureStdout(t, func() {
			renderBuildStatus(&api.BuildTask{ID: 7, AppKey: "myapp", Status: "SUCCESS"}, "https://myapp.example")
		})
		if !strings.Contains(out, "URL:") || !strings.Contains(out, "https://myapp.example") {
			t.Errorf("URL row missing, got: %q", out)
		}
	})

	t.Run("formatBuildError single-sided fields", func(t *testing.T) {
		if got := formatBuildError(&api.BuildTask{ErrorMessage: "boom"}); got != "boom" {
			t.Errorf("message only = %q, want boom", got)
		}
		if got := formatBuildError(&api.BuildTask{ErrorCode: "E1"}); got != "E1" {
			t.Errorf("code only = %q, want E1", got)
		}
		if got := formatBuildError(&api.BuildTask{}); got != "" {
			t.Errorf("no error fields = %q, want empty", got)
		}
	})
}

// devTree 取裸仓库 deployBranch 分支最新提交的文件树
func devTree(t *testing.T, bareDir string) *object.Tree {
	t.Helper()
	r, err := git.PlainOpen(bareDir)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := r.Reference(plumbing.NewBranchReferenceName(deployBranch), true)
	if err != nil {
		t.Fatalf("dev branch missing on remote: %v", err)
	}
	c, err := r.CommitObject(ref.Hash())
	if err != nil {
		t.Fatal(err)
	}
	tree, err := c.Tree()
	if err != nil {
		t.Fatal(err)
	}
	return tree
}
