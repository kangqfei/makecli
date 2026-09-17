/**
 * [INPUT]: 依赖 api 包内的 Client.CreateRepository、CodeRepoResource.CloneURLFor（包内白盒），encoding/json、net/http、net/http/httptest、testing，依赖 internal/build 的 Version
 * [OUTPUT]: 覆盖代码仓库服务调用与 cloneUrl 收口逻辑的单元测试
 * [POS]: internal/api 模块 repository.go 的配套测试，用 httptest 隔离网络
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qfeius/makecli/internal/build"
)

func TestCreateRepository(t *testing.T) {
	t.Run("sends correct request and parses dual-env response", func(t *testing.T) {
		var gotTarget, gotPath, gotVersion string
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotTarget = r.Header.Get("X-Make-Target")
			gotPath = r.URL.Path
			gotVersion = r.URL.Query().Get("version")
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"code": 200, "msg": "repositories are ready",
				"data": {
					"appKey": "myapp", "type": "Make.Code.Repository",
					"meta": {"version": "1.0.0", "owner": "1120076349311025262"},
					"properties": {
						"orgId": 1120076349311025262, "private": true,
						"createdOrg": false, "createdRepos": ["preview", "production"],
						"env": {
							"preview":    {"repository": {"repoName": "myapp-preview", "giteaRepoId": 321, "cloneUrl": "https://repo.example/org/myapp-preview.git"}},
							"production": {"repository": {"repoName": "myapp-production", "giteaRepoId": 322, "cloneUrl": "https://repo.example/org/myapp-production.git"}}
						}
					}
				}
			}`))
		}))
		defer srv.Close()

		repo, err := New(srv.URL, "test-token").CreateRepository("myapp")
		if err != nil {
			t.Fatalf("CreateRepository: %v", err)
		}
		if gotTarget != "MakeService.CreateResource" {
			t.Errorf("X-Make-Target = %q, want MakeService.CreateResource", gotTarget)
		}
		if gotPath != "/code/v1/repository" {
			t.Errorf("path = %q, want /code/v1/repository", gotPath)
		}
		if gotVersion != build.Version {
			t.Errorf("version query = %q, want %q", gotVersion, build.Version)
		}
		if gotBody["type"] != "Make.Code.Repository" || gotBody["appKey"] != "myapp" {
			t.Errorf("unexpected request body: %v", gotBody)
		}
		if got := repo.CloneURLFor("preview"); got != "https://repo.example/org/myapp-preview.git" {
			t.Errorf("preview cloneUrl = %q", got)
		}
		if got := repo.CloneURLFor("production"); got != "https://repo.example/org/myapp-production.git" {
			t.Errorf("production cloneUrl = %q", got)
		}
		if repo.Properties.Env["preview"].Repository.MakeRepoID != 321 {
			t.Errorf("preview MakeRepoID = %d, want 321", repo.Properties.Env["preview"].Repository.MakeRepoID)
		}
	})

	t.Run("fails on API error response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code": 422, "msg": "appKey hits gitea naming rule"}`))
		}))
		defer srv.Close()

		if _, err := New(srv.URL, "t").CreateRepository("bad"); err == nil {
			t.Fatal("expected error on code 422")
		}
	})

	t.Run("fails on transport error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
		srv.Close() // 立刻关掉制造连接失败

		if _, err := New(srv.URL, "t").CreateRepository("myapp"); err == nil {
			t.Fatal("expected transport error")
		}
	})
}

func TestCloneURLFor(t *testing.T) {
	t.Run("prefers properties.env", func(t *testing.T) {
		r := &CodeRepoResource{
			Meta: CodeRepoMeta{CloneURL: "https://legacy.git"},
			Properties: CodeRepoProperties{Env: map[string]CodeRepoEnv{
				"preview": {Repository: CodeRepo{CloneURL: "https://env.git"}},
			}},
		}
		if got := r.CloneURLFor("preview"); got != "https://env.git" {
			t.Errorf("CloneURLFor = %q, want https://env.git", got)
		}
	})

	t.Run("falls back to meta.repositories by environment", func(t *testing.T) {
		r := &CodeRepoResource{
			Meta: CodeRepoMeta{Repositories: []CodeRepo{
				{Environment: "preview", CloneURL: "https://meta-preview.git"},
				{Environment: "production", CloneURL: "https://meta-production.git"},
			}},
		}
		if got := r.CloneURLFor("production"); got != "https://meta-production.git" {
			t.Errorf("CloneURLFor = %q, want https://meta-production.git", got)
		}
	})

	t.Run("falls back to legacy meta.cloneUrl for any env", func(t *testing.T) {
		r := &CodeRepoResource{Meta: CodeRepoMeta{CloneURL: "https://single.git"}}
		if got := r.CloneURLFor("production"); got != "https://single.git" {
			t.Errorf("CloneURLFor = %q, want https://single.git", got)
		}
	})

	t.Run("returns empty when nothing matches", func(t *testing.T) {
		r := &CodeRepoResource{}
		if got := r.CloneURLFor("preview"); got != "" {
			t.Errorf("CloneURLFor = %q, want empty", got)
		}
	})
}
