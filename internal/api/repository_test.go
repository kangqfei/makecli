/**
 * [INPUT]: 依赖 api 包内的 Client.CreateRepository、CodeRepoResource.CloneURL（包内白盒），encoding/json、net/http、net/http/httptest、testing，依赖 internal/build 的 Version
 * [OUTPUT]: 覆盖代码仓库服务调用（按 app 划分的仓库响应）与 CloneURL 回退的单元测试
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
	t.Run("sends correct request and parses per-app repository response", func(t *testing.T) {
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
					"appKey": "myapp_beta_", "appRole": "beta", "type": "Make.Code.Repository",
					"meta": {"version": "1.0.0", "owner": "1120076349311025262"},
					"properties": {
						"orgId": 1120076349311025262, "private": true,
						"createdOrg": false, "createdRepos": ["beta"],
						"env": {"repository": {"repoName": "myapp_beta_", "giteaRepoId": 259, "cloneUrl": "https://repo.example/org/myapp_beta_.git"}}
					}
				}
			}`))
		}))
		defer srv.Close()

		repo, err := New(srv.URL, "test-token").CreateRepository("myapp_beta_")
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
		if gotBody["type"] != "Make.Code.Repository" || gotBody["appKey"] != "myapp_beta_" {
			t.Errorf("unexpected request body: %v", gotBody)
		}
		if repo.AppRole != "beta" {
			t.Errorf("appRole = %q, want beta", repo.AppRole)
		}
		if got := repo.CloneURL(); got != "https://repo.example/org/myapp_beta_.git" {
			t.Errorf("cloneUrl = %q", got)
		}
		if repo.Properties.Env.Repository.MakeRepoID != 259 {
			t.Errorf("MakeRepoID = %d, want 259", repo.Properties.Env.Repository.MakeRepoID)
		}
	})

	t.Run("fails on non-200 business code", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code": 422, "msg": "app not found"}`))
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

func TestCloneURL(t *testing.T) {
	t.Run("prefers properties.env.repository", func(t *testing.T) {
		r := &CodeRepoResource{
			Meta:       CodeRepoMeta{CloneURL: "https://legacy.git"},
			Properties: CodeRepoProperties{Env: CodeRepoEnv{Repository: CodeRepo{CloneURL: "https://env.git"}}},
		}
		if got := r.CloneURL(); got != "https://env.git" {
			t.Errorf("CloneURL = %q, want https://env.git", got)
		}
	})
	t.Run("falls back to meta.cloneUrl", func(t *testing.T) {
		r := &CodeRepoResource{Meta: CodeRepoMeta{CloneURL: "https://single.git"}}
		if got := r.CloneURL(); got != "https://single.git" {
			t.Errorf("CloneURL = %q, want https://single.git", got)
		}
	})
	t.Run("empty when nothing present", func(t *testing.T) {
		if got := (&CodeRepoResource{}).CloneURL(); got != "" {
			t.Errorf("CloneURL = %q, want empty", got)
		}
	})
}
