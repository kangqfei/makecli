/**
 * [INPUT]: 依赖 client.go 的 Client.do / notFoundCode / ErrNotFound、fmt、net/url，依赖 internal/build 的 Version
 * [OUTPUT]: 对外提供 CodeRepo / CodeRepoEnv / CodeRepoMeta / CodeRepoProperties / CodeRepoResource 类型、
 *           Client.CreateRepository(appKey) 方法、CodeRepoResource.CloneURLFor(env) 收口方法
 * [POS]: internal/api 的代码仓库服务（make-repo）调用层，POST /code/v1/repository?version=<cli 版本>，
 *        经 X-Make-Target 区分动作；与 client.go 的 Meta 操作共用 Client 与 do 原语。
 *        version 查询参数是服务端强制升级门禁的依据（缺失即拒绝并提示 makecli update）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package api

import (
	"fmt"
	"net/url"

	"github.com/qfeius/makecli/internal/build"
)

// codeRepoType 是代码仓库资源的固定 type 标识
const codeRepoType = "Make.Code.Repository"

// codeRepoPath 是代码仓库端点，version 查询参数带上 CLI 版本供服务端做升级门禁：
// 服务端按 version 缺失（后续按具体版本）拒绝请求并提示 makecli update
func codeRepoPath() string {
	return "/code/v1/repository?version=" + url.QueryEscape(build.Version)
}

// CodeRepo 描述单个远端仓库（repoName / cloneUrl；MakeRepoID 映射服务端 giteaRepoId 字段——
// json tag 是线上契约不可改，Go 字段名去耦底层实现）。
// Environment 仅在 meta.repositories 兼容形态中出现。
type CodeRepo struct {
	RepoName    string `json:"repoName"`
	MakeRepoID  int64  `json:"giteaRepoId"`
	CloneURL    string `json:"cloneUrl"`
	Environment string `json:"environment,omitempty"`
}

// CodeRepoEnv 包装单个环境下的仓库信息（properties.env.<env>.repository）
type CodeRepoEnv struct {
	Repository CodeRepo `json:"repository"`
}

// CodeRepoMeta 是响应的 meta 段；CloneURL / Repositories 为历史兼容形态
type CodeRepoMeta struct {
	Version      string     `json:"version"`
	Owner        string     `json:"owner"`
	CloneURL     string     `json:"cloneUrl"`
	Repositories []CodeRepo `json:"repositories"`
}

// CodeRepoProperties 是响应的 properties 段，Env 以环境名（preview/production）为 key
type CodeRepoProperties struct {
	OrgID        int64                  `json:"orgId"`
	Private      bool                   `json:"private"`
	CreatedOrg   bool                   `json:"createdOrg"`
	CreatedRepos []string               `json:"createdRepos"`
	Env          map[string]CodeRepoEnv `json:"env"`
}

// CodeRepoResource 是 /code/v1/repository 各动作返回的 data 段
type CodeRepoResource struct {
	AppKey     string             `json:"appKey"`
	Type       string             `json:"type"`
	Meta       CodeRepoMeta       `json:"meta"`
	Properties CodeRepoProperties `json:"properties"`
}

// CloneURLFor 返回指定环境的仓库推送地址，把三种响应形态收口为一个查询：
//  1. properties.env.<env>.repository.cloneUrl（双环境标准形态）
//  2. meta.repositories[].cloneUrl（按 environment 匹配的兼容形态）
//  3. meta.cloneUrl（历史单仓库形态，对所有环境生效）
//
// 找不到时返回空串，由调用方决定如何报错。
func (r *CodeRepoResource) CloneURLFor(env string) string {
	if e, ok := r.Properties.Env[env]; ok && e.Repository.CloneURL != "" {
		return e.Repository.CloneURL
	}
	for _, repo := range r.Meta.Repositories {
		if repo.Environment == env && repo.CloneURL != "" {
			return repo.CloneURL
		}
	}
	return r.Meta.CloneURL
}

// CreateRepository 调用 MakeService.CreateResource 按租户幂等准备代码仓库：
// Organization / Repository 不存在则创建，存在则复用。成功即代表仓库已就绪，可以 git push。
// 请求携带 version 查询参数（CLI 版本），服务端据此做强制升级判定。
func (c *Client) CreateRepository(appKey string) (*CodeRepoResource, error) {
	body := map[string]any{
		"type":   codeRepoType,
		"appKey": appKey,
	}
	var result struct {
		Code    int              `json:"code"`
		Message string           `json:"msg"`
		Data    CodeRepoResource `json:"data"`
	}
	if err := c.do("MakeService.CreateResource", codeRepoPath(), body, &result); err != nil {
		return nil, err
	}
	if result.Code == notFoundCode {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, result.Message)
	}
	if result.Code != 200 {
		return nil, fmt.Errorf("API 错误 [%d]: %s", result.Code, result.Message)
	}
	return &result.Data, nil
}
