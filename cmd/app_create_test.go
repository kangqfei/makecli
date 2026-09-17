/**
 * [INPUT]: 依赖 cmd 包内的 runAppCreate/runAppCreateFromFile/writeScaffold/renderAppDSL/newAppManifest/assertDeployable（包内白盒），internal/config、github.com/go-git/go-git/v5、encoding/json、net/http、net/http/httptest、os、path/filepath
 * [OUTPUT]: 覆盖 app create 子命令核心逻辑的单元测试（脚手架 + 远端创建合并；生成 AGENTS.md 导航契约；成功静默 / 失败警告；含 -f 文件模式；含 --dry-run：X-Dry-Run 头到达线缆 + 跳过本地副作用 + would-be 输出 + 失败透传）
 * [POS]: cmd 模块 app_create.go 的配套测试，用 httptest 隔离网络、t.Setenv 隔离凭证、t.TempDir 隔离文件系统
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/qfeius/makecli/internal/config"
)

// ---------------------------------- app.yaml 生成 ----------------------------------

func TestRenderAppDSL(t *testing.T) {
	t.Run("renders canonical Make.App DSL", func(t *testing.T) {
		data, err := renderAppDSL(newAppManifest("shop", "商城", "demo shop"))
		if err != nil {
			t.Fatalf("renderAppDSL: %v", err)
		}
		got := string(data)
		for _, want := range []string{"key: shop", "name: 商城", "type: Make.App", "version: 1.0.0", "description: demo shop"} {
			if !strings.Contains(got, want) {
				t.Errorf("rendered DSL missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("uses 2-space indent", func(t *testing.T) {
		data, err := renderAppDSL(newAppManifest("shop", "shop", ""))
		if err != nil {
			t.Fatalf("renderAppDSL: %v", err)
		}
		// meta.version 应缩进 2 空格（对齐 DSL 例子），而非 yaml.v3 默认的 4 空格
		if !strings.Contains(string(data), "\n  version: 1.0.0") {
			t.Errorf("expected 2-space indented version, got:\n%s", data)
		}
	})

	t.Run("round-trips through loadAppManifestFromFile", func(t *testing.T) {
		dir := t.TempDir()
		f := filepath.Join(dir, "app.yaml")
		data, err := renderAppDSL(newAppManifest("shop", "商城", "demo"))
		if err != nil {
			t.Fatalf("renderAppDSL: %v", err)
		}
		writeTestFile(t, f, data)

		m, err := loadAppManifestFromFile(f)
		if err != nil {
			t.Fatalf("loadAppManifestFromFile: %v", err)
		}
		if m.Key != "shop" || m.Name != "商城" || m.Type != "Make.App" {
			t.Errorf("round-trip mismatch: %+v", m)
		}
	})
}

// ---------------------------------- 本地脚手架 ----------------------------------

func TestWriteScaffold(t *testing.T) {
	// .gitignore 不再由 writeScaffold 写出（移交 ensureGitignore）
	scaffoldFiles := []string{"CLAUDE.md", "AGENTS.md", filepath.Join("apps", "dsl", "app.yaml")}

	t.Run("creates agent files and app.yaml, reporting them as created", func(t *testing.T) {
		dir := t.TempDir()
		created, err := writeScaffold(dir, newAppManifest("shop", "shop", ""))
		if err != nil {
			t.Fatalf("writeScaffold: %v", err)
		}
		for _, name := range scaffoldFiles {
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Errorf("expected %s to exist: %v", name, err)
			}
		}
		if len(created) != len(scaffoldFiles) {
			t.Errorf("created = %v, want all %d scaffold files", created, len(scaffoldFiles))
		}
	})

	t.Run("is idempotent: skips existing files, preserves edits", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := writeScaffold(dir, newAppManifest("shop", "shop", "")); err != nil {
			t.Fatalf("first writeScaffold: %v", err)
		}
		// 用户编辑其中一个文件
		edited := filepath.Join(dir, "CLAUDE.md")
		writeTestFile(t, edited, []byte("MY EDITS"))

		created, err := writeScaffold(dir, newAppManifest("shop", "shop", ""))
		if err != nil {
			t.Fatalf("second writeScaffold: %v", err)
		}
		if len(created) != 0 {
			t.Errorf("re-run should create nothing, got: %v", created)
		}
		data, _ := os.ReadFile(edited)
		if string(data) != "MY EDITS" {
			t.Errorf("user edits must be preserved, got: %q", data)
		}
	})

	t.Run("creates nested folder if not exists", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "newapp")
		if _, err := writeScaffold(dir, newAppManifest("newapp", "newapp", "")); err != nil {
			t.Fatalf("writeScaffold: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "apps", "dsl", "app.yaml")); err != nil {
			t.Errorf("expected app.yaml to exist: %v", err)
		}
	})

	t.Run("app.yaml round-trips with the manifest", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := writeScaffold(dir, newAppManifest("shop", "商城", "")); err != nil {
			t.Fatalf("writeScaffold: %v", err)
		}
		m, err := loadAppManifestFromFile(filepath.Join(dir, "apps", "dsl", "app.yaml"))
		if err != nil {
			t.Fatalf("loadAppManifestFromFile: %v", err)
		}
		if m.Key != "shop" || m.Name != "商城" || m.Type != "Make.App" {
			t.Errorf("scaffolded app.yaml mismatch: %+v", m)
		}
	})

	t.Run("AGENTS.md includes vibe guidance, auth, and runtime contracts", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := writeScaffold(dir, newAppManifest("shop", "shop", "")); err != nil {
			t.Fatalf("writeScaffold: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		if err != nil {
			t.Fatalf("read AGENTS.md: %v", err)
		}
		assertGeneratedAgentsContract(t, string(data))
	})
}

// ---------------------------------- 合并后的 create（脚手架 + 远端） ----------------------------------

func TestRunAppCreate(t *testing.T) {
	t.Run("scaffolds locally and creates app remotely", func(t *testing.T) {
		srv := newMockMeta(t, 200, "create app success")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL
		stubRepoServer(t, srv.URL)

		folder := filepath.Join(t.TempDir(), "shop")
		if err := runAppCreate(folder, "", "", false); err != nil {
			t.Fatalf("runAppCreate: %v", err)
		}
		// 本地脚手架已落地
		m, err := loadAppManifestFromFile(filepath.Join(folder, "apps", "dsl", "app.yaml"))
		if err != nil {
			t.Fatalf("scaffolded app.yaml: %v", err)
		}
		if m.Key != "shop" {
			t.Errorf("appKey from folder name = %q, want shop", m.Key)
		}
	})

	t.Run("scaffolds a clean git repo with an initial commit", func(t *testing.T) {
		srv := newMockMeta(t, 200, "create app success")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL
		stubRepoServer(t, srv.URL)

		folder := filepath.Join(t.TempDir(), "shop")
		if err := runAppCreate(folder, "", "", false); err != nil {
			t.Fatalf("runAppCreate: %v", err)
		}

		// .gitignore 由 scaffoldGit 写出并 ignore node_modules
		data, err := os.ReadFile(filepath.Join(folder, ".gitignore"))
		if err != nil {
			t.Fatalf("read .gitignore: %v", err)
		}
		if !strings.Contains(string(data), "node_modules") {
			t.Errorf(".gitignore must ignore node_modules, got: %q", data)
		}

		// 是一个有 HEAD（初始提交）且工作树干净的仓库——可立即 deploy
		repo, err := git.PlainOpen(folder)
		if err != nil {
			t.Fatalf("create should leave a git repo: %v", err)
		}
		if _, err := repo.Head(); err != nil {
			t.Errorf("expected an initial commit (HEAD): %v", err)
		}
		if err := assertDeployable(repo); err != nil {
			t.Errorf("scaffolded repo should be immediately deployable: %v", err)
		}
	})

	t.Run("derives appKey from current dir on '.'", func(t *testing.T) {
		srv := newMockMeta(t, 200, "create app success")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL
		stubRepoServer(t, srv.URL)

		work := filepath.Join(t.TempDir(), "shopapp")
		if err := os.MkdirAll(work, 0755); err != nil {
			t.Fatal(err)
		}
		chdir(t, work)

		if err := runAppCreate(".", "", "", false); err != nil {
			t.Fatalf("runAppCreate '.': %v", err)
		}
		m, err := loadAppManifestFromFile(filepath.Join("apps", "dsl", "app.yaml"))
		if err != nil {
			t.Fatalf("scaffolded app.yaml: %v", err)
		}
		if m.Key != "shopapp" {
			t.Errorf("appKey from '.' = %q, want shopapp", m.Key)
		}
	})

	t.Run("success output is concise: only the created line", func(t *testing.T) {
		srv := newMockMeta(t, 200, "create app success")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL
		// 仓库服务指向不可用地址——create 不再预热代码仓库（服务端要求先建 Beta 环境，由 deploy 幂等准备），根本不该触达
		stubRepoServer(t, "http://127.0.0.1:0")

		folder := filepath.Join(t.TempDir(), "myapp")
		out := captureStdout(t, func() {
			if err := runAppCreate(folder, "", "", false); err != nil {
				t.Errorf("runAppCreate: %v", err)
			}
		})
		if strings.Contains(out, "Code repositories") || strings.Contains(out, ".git") {
			t.Errorf("success output should not include repository info, got:\n%s", out)
		}
		if strings.TrimSpace(out) != "App 'myapp' created successfully" {
			t.Errorf("success output not concise, got:\n%s", out)
		}
	})

	t.Run("writes description into app.yaml", func(t *testing.T) {
		srv := newMockMeta(t, 200, "create app success")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL
		stubRepoServer(t, srv.URL)

		folder := filepath.Join(t.TempDir(), "myapp")
		if err := runAppCreate(folder, "My App", "awesome", false); err != nil {
			t.Fatalf("runAppCreate with description: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(folder, "apps", "dsl", "app.yaml"))
		if err != nil {
			t.Fatalf("read app.yaml: %v", err)
		}
		if !strings.Contains(string(data), "description: awesome") {
			t.Errorf("app.yaml missing description:\n%s", data)
		}
	})

	t.Run("rejects invalid directory name as app key", func(t *testing.T) {
		cases := []string{"my-app", "my app", "my.app", "我的app", "a_very_long_name_that_is"}
		for _, folder := range cases {
			if err := runAppCreate(folder, "", "", false); err == nil {
				t.Errorf("expected error for invalid folder name %q", folder)
			}
		}
	})

	t.Run("fails without credentials before scaffolding", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		MetaServerURL = "http://unused"
		folder := filepath.Join(t.TempDir(), "myapp")
		if err := runAppCreate(folder, "", "", false); err == nil {
			t.Fatal("expected error for missing credentials")
		}
		// 无 token 应在脚手架前失败，不留下任何文件
		if _, err := os.Stat(filepath.Join(folder, "CLAUDE.md")); err == nil {
			t.Error("scaffold should not run without credentials")
		}
	})

	t.Run("API failure leaves no local files", func(t *testing.T) {
		srv := newMockMeta(t, 400, "invalid app name")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		folder := filepath.Join(t.TempDir(), "myapp")
		if err := runAppCreate(folder, "", "", false); err == nil {
			t.Fatal("expected error on API failure")
		}
		// 远端先行：CreateApp 失败时本地尚未落任何文件，保证换 profile 重跑干净
		if _, err := os.Stat(folder); err == nil {
			t.Error("remote failure should leave no local scaffold")
		}
	})

	t.Run("composes with a pre-existing local scaffold (idempotent, no reject)", func(t *testing.T) {
		srv := newMockMeta(t, 200, "create app success")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL
		stubRepoServer(t, srv.URL)

		// 先用 init 内核铺一份本地脚手架并编辑——模拟「先 app init 本地、再 app create 补远端」
		folder := filepath.Join(t.TempDir(), "shop")
		if _, err := writeScaffold(folder, newAppManifest("shop", "shop", "")); err != nil {
			t.Fatal(err)
		}
		edited := filepath.Join(folder, "CLAUDE.md")
		writeTestFile(t, edited, []byte("MY EDITS"))

		// create 不再硬拒已存在文件：补远端 + commit，且保留用户编辑
		if err := runAppCreate(folder, "", "", false); err != nil {
			t.Fatalf("create should compose with a pre-existing scaffold: %v", err)
		}
		if data, _ := os.ReadFile(edited); string(data) != "MY EDITS" {
			t.Errorf("create must not clobber user edits, got: %q", data)
		}
		repo, err := git.PlainOpen(folder)
		if err != nil {
			t.Fatalf("expected a git repo after compose: %v", err)
		}
		if err := assertDeployable(repo); err != nil {
			t.Errorf("composed repo should be immediately deployable: %v", err)
		}
	})

	t.Run("fails with unknown profile", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = "http://unused"
		setProfile(t, "nonexistent")

		folder := filepath.Join(t.TempDir(), "myapp")
		if err := runAppCreate(folder, "", "", false); err == nil {
			t.Fatal("expected error for unknown profile")
		}
	})

	t.Run("dry-run sends X-Dry-Run, skips scaffold/git/repo, prints would-be line", func(t *testing.T) {
		var gotDryRun string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotDryRun = r.Header.Get("X-Dry-Run")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "create app success", "data": map[string]any{}})
		}))
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL
		// 仓库服务指向一个不可用地址——dry-run 必须根本不触达它
		stubRepoServer(t, "http://127.0.0.1:0")

		folder := filepath.Join(t.TempDir(), "shop")
		out := captureStdout(t, func() {
			if err := runAppCreate(folder, "", "", true); err != nil {
				t.Fatalf("runAppCreate dry-run: %v", err)
			}
		})

		// 1. X-Dry-Run 头穿过 cmd→client→api 全链路到达线缆
		if gotDryRun != "true" {
			t.Errorf("X-Dry-Run header = %q, want %q", gotDryRun, "true")
		}
		// 2. 无任何本地副作用：目录都不该被创建
		if _, err := os.Stat(folder); err == nil {
			t.Errorf("dry-run must not scaffold any local files")
		}
		// 3. 输出是 would-be 语义（"would be created"），不是真实创建（"App 'shop' created successfully"）
		if !strings.Contains(out, "Dry run") || !strings.Contains(out, "would be created") || !strings.Contains(out, "shop") {
			t.Errorf("dry-run output = %q, want a 'Dry run ... shop ... would be created' line", out)
		}
		if strings.Contains(out, "App 'shop' created successfully") {
			t.Errorf("dry-run must not claim real creation, got: %q", out)
		}
	})

	t.Run("dry-run propagates API failure (e.g. key conflict)", func(t *testing.T) {
		srv := newMockMeta(t, 400, "app key 'shop' already exists")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL

		folder := filepath.Join(t.TempDir(), "shop")
		if err := runAppCreate(folder, "", "", true); err == nil {
			t.Fatal("dry-run should surface the would-fail error from the API")
		}
		if _, err := os.Stat(folder); err == nil {
			t.Errorf("dry-run failure must leave no local files")
		}
	})
}

func TestNewAppCreateCmd(t *testing.T) {
	t.Run("fails without appKey and -f", func(t *testing.T) {
		cmd := newAppCreateCmd()
		cmd.SetArgs([]string{})
		cmd.SilenceErrors = true
		if err := cmd.Execute(); err == nil {
			t.Fatal("expected error without appKey and -f")
		}
	})
}

func TestRunAppCreateFromFile(t *testing.T) {
	t.Run("creates app from YAML file", func(t *testing.T) {
		srv := newMockMeta(t, 200, "create app success")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL
		stubRepoServer(t, srv.URL)

		f := filepath.Join(t.TempDir(), "app.yaml")
		writeTestFile(t, f, []byte("key: fileapp\nname: 文件应用\ntype: Make.App\nproperties:\n  description: from file\n"))

		if err := runAppCreateFromFile(f, false); err != nil {
			t.Fatalf("runAppCreateFromFile: %v", err)
		}
	})

	t.Run("creates app from YAML file without properties", func(t *testing.T) {
		srv := newMockMeta(t, 200, "create app success")
		defer srv.Close()
		t.Setenv("HOME", t.TempDir())
		saveDefaultToken(t)
		MetaServerURL = srv.URL
		stubRepoServer(t, srv.URL)

		f := filepath.Join(t.TempDir(), "app.yml")
		writeTestFile(t, f, []byte("key: bareapp\nname: 简易应用\ntype: Make.App\n"))

		if err := runAppCreateFromFile(f, false); err != nil {
			t.Fatalf("runAppCreateFromFile without props: %v", err)
		}
	})

	t.Run("fails on non-yaml file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "app.json")
		writeTestFile(t, f, []byte(`{}`))

		if err := runAppCreateFromFile(f, false); err == nil {
			t.Fatal("expected error for non-yaml file")
		}
	})

	t.Run("fails when no Make.App in file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "entity.yaml")
		writeTestFile(t, f, []byte("key: foo\nname: 测试\ntype: Make.Entity\nappKey: bar\n"))

		if err := runAppCreateFromFile(f, false); err == nil {
			t.Fatal("expected error for missing Make.App")
		}
	})

	t.Run("fails when multiple Make.App in file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "multi.yaml")
		writeTestFile(t, f, []byte("key: appone\nname: 一号\ntype: Make.App\n---\nkey: apptwo\nname: 二号\ntype: Make.App\n"))

		if err := runAppCreateFromFile(f, false); err == nil {
			t.Fatal("expected error for multiple Make.App")
		}
	})
}

func TestValidResourceKey(t *testing.T) {
	valid := []string{"ab", "abc", "MyApp", "app_01", "A1_b2_C3", "12345678901234567890"}
	for _, key := range valid {
		if err := validResourceKey(key); err != nil {
			t.Errorf("validResourceKey(%q) unexpected error: %v", key, err)
		}
	}

	invalid := []string{"", "a", "_underscore", "my-app", "my app", "my.app", "我的app", "a_very_long_name_that_is", "app@home"}
	for _, key := range invalid {
		if err := validResourceKey(key); err == nil {
			t.Errorf("validResourceKey(%q) expected error, got nil", key)
		}
	}
}

// ---------------------------------- 测试辅助 ----------------------------------

// chdir 临时切换工作目录，测试结束自动还原（Go 1.22 无 t.Chdir）
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

// writeTestFile 在指定路径写入测试文件，失败则终止测试
func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

// newMockMeta 启动一个返回固定 code/message 的测试 Meta Server
func newMockMeta(t *testing.T, code int, message string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    code,
			"message": message,
			"data":    map[string]any{},
		})
	}))
}

// stubRepoServer 测试期间把代码仓库服务指向给定 URL，结束自动还原
func stubRepoServer(t *testing.T, url string) {
	t.Helper()
	t.Setenv(EnvRepoServerURL, url)
}

// saveDefaultToken 在当前 HOME 下写入 default profile 的测试 JWT
func saveDefaultToken(t *testing.T) {
	t.Helper()
	// 合法 JWT 格式（三段 base64url），validateJWT 校验通过
	fakeToken := "eyJ0eXAiOiJKV1QifQ.eyJzdWIiOiJ0ZXN0In0.c2lnbmF0dXJl"
	if err := config.Save(config.Credentials{
		"default": config.Profile{AccessToken: fakeToken},
	}); err != nil {
		t.Fatal(err)
	}
}
