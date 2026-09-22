/**
 * [INPUT]: 依赖 pull/clone 的命令与编排、cloneCall 拉取桩、deploy_test 的工程/仓库/HTTP 夹具、stdout_test 的输出捕获、testing/os/strings/plumbing
 * [OUTPUT]: 验证 pull 隐藏入口、无参约束、当前工程身份和目录，以及本地前置门控
 * [POS]: cmd/pull.go 的配套测试；真实 fetch/快进/分叉测试由 clone_test.go 共享覆盖
 * [PROTOCOL]: 变更时更新此头部，然后检查 AGENTS.md
 */

package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
)

func TestPullCommand(t *testing.T) {
	app := newAppCmd()
	pull, _, err := app.Find([]string{"pull"})
	if err != nil || pull == app || !pull.Hidden {
		t.Fatalf("hidden app pull not registered: %v", err)
	}
	if err := pull.ValidateArgs(nil); err != nil {
		t.Fatal(err)
	}
	cmd := newPullCmd()
	cmd.SetArgs([]string{"shop"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("pull must reject appKey before running")
	}
}

func TestRunPullCurrentProject(t *testing.T) {
	enterAppDir(t, "myapp") // 临时目录名与 appKey 不同，身份必须来自清单。
	gitCommitAll(t)
	t.Setenv("HOME", t.TempDir())
	saveDefaultToken(t)
	stubMetaServer(t, newAppExistsMeta(t).URL)
	t.Setenv(EnvRepoServerURL, newMockRepoServer(t).URL)
	p := &cloneCall{res: cloneResult{to: plumbing.NewHash("b"), updated: true}}
	p.install(t)
	out := captureStdout(t, func() {
		if err := runPull(); err != nil {
			t.Fatal(err)
		}
	})
	if p.cloneURL != "https://repo.example/org/myapp_beta_.git" || p.token == "" {
		t.Fatalf("wrong repository or missing credentials: %s", p.cloneURL)
	}
	if !strings.Contains(out, "Pulled 'myapp' from beta") {
		t.Fatalf("missing pull result: %s", out)
	}
	if _, err := os.Stat("myapp"); !os.IsNotExist(err) {
		t.Fatalf("pull must not create an appKey subdirectory: %v", err)
	}
}

func TestRunPullLocalGates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T)
		want  string
	}{
		{"missing manifest", func(t *testing.T) { chdir(t, t.TempDir()) }, "project root"},
		{"invalid key", func(t *testing.T) { enterAppDir(t, "_bad") }, "invalid app key"},
		{"missing repository", func(t *testing.T) { enterAppDir(t, "myapp") }, "git repository"},
		{"dirty tree", func(t *testing.T) {
			enterAppDir(t, "myapp")
			gitCommitAll(t)
			writeTestFile(t, "dirty.txt", []byte("dirty"))
		}, "uncommitted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			stubMetaServer(t, noNetRepoServer(t).URL)
			t.Setenv(EnvRepoServerURL, noNetRepoServer(t).URL)
			p := &cloneCall{}
			p.install(t)
			if err := runPull(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q error, got %v", tc.want, err)
			}
			if p.called {
				t.Fatal("pull must stop before fetching")
			}
		})
	}
}
